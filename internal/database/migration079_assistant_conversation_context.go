package database

import (
	"context"
	"database/sql"
)

// Derived checkpoints stay with canonical Sessions, outside portable history.
// Reset explicitly clears them; Session deletion cascades and ownership changes
// invalidate orphaned context without moving its authority.
func (db *DB) migration079AssistantConversationContext(ctx context.Context) error {
	// The pre-merge feature also used version 78, for conversation context.
	// Reapply the idempotent delivered folder migration so databases from
	// either parent get its bounds without rewriting migration history.
	// Its column copy may invalidate disposable recaps, never message text.
	if err := db.migration078FolderSelectionCapacity(ctx); err != nil {
		return err
	}
	return db.InTransaction(ctx, func(tx *sql.Tx) error {
		var tables, column int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('sessions','messages')`).Scan(&tables); err != nil {
			return err
		}
		if tables != 2 {
			return nil
		} // Match existing sparse legacy migration fixtures.
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name='assistant_context_epoch'`).Scan(&column); err != nil {
			return err
		}
		if column == 0 {
			if _, err := tx.ExecContext(ctx, `ALTER TABLE sessions ADD COLUMN assistant_context_epoch INTEGER NOT NULL DEFAULT 0`); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS assistant_conversation_checkpoints (
			session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
			owner_user_id TEXT NOT NULL,
			hq_workspace_id TEXT NOT NULL,
			profile_name TEXT NOT NULL,
			state_version INTEGER NOT NULL,
			source_epoch INTEGER NOT NULL,
			through_rowid INTEGER NOT NULL CHECK(through_rowid > 0),
			generation INTEGER NOT NULL CHECK(generation > 0),
			recap_json TEXT NOT NULL CHECK(json_valid(recap_json) AND length(CAST(recap_json AS BLOB)) <= 16000),
			updated_at DATETIME NOT NULL
		);
		CREATE TRIGGER IF NOT EXISTS assistant_context_owner_changed AFTER UPDATE OF workspace_id,agent_name ON sessions WHEN COALESCE(OLD.workspace_id,'')<>COALESCE(NEW.workspace_id,'') OR OLD.agent_name<>NEW.agent_name BEGIN
			UPDATE sessions SET assistant_context_epoch=assistant_context_epoch+1 WHERE id=NEW.id;
			DELETE FROM assistant_conversation_checkpoints WHERE session_id=NEW.id;
		END;
		CREATE TRIGGER IF NOT EXISTS assistant_context_source_updated AFTER UPDATE ON messages BEGIN
			UPDATE sessions SET assistant_context_epoch=assistant_context_epoch+1 WHERE id=OLD.session_id OR id=NEW.session_id;
			DELETE FROM assistant_conversation_checkpoints WHERE session_id=OLD.session_id OR session_id=NEW.session_id;
		END;
		CREATE TRIGGER IF NOT EXISTS assistant_context_source_deleted AFTER DELETE ON messages BEGIN
			UPDATE sessions SET assistant_context_epoch=assistant_context_epoch+1 WHERE id=OLD.session_id;
			DELETE FROM assistant_conversation_checkpoints WHERE session_id=OLD.session_id;
		END;
		CREATE TRIGGER IF NOT EXISTS assistant_context_source_imported AFTER INSERT ON messages WHEN NEW.continuity_source_sequence IS NOT NULL BEGIN
			UPDATE sessions SET assistant_context_epoch=assistant_context_epoch+1 WHERE id=NEW.session_id;
			DELETE FROM assistant_conversation_checkpoints WHERE session_id=NEW.session_id;
		END;
	`)
		return err
	})
}
