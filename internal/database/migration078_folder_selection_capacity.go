package database

import (
	"context"
	"database/sql"
)

// Keep the snapshot's existing size limit, but allow its event envelope to hold
// every recorded focus ID. Copy only this column, transactionally; message IDs,
// contents, attribution, relationships and indexes remain untouched.
func (db *DB) migration078FolderSelectionCapacity(ctx context.Context) error {
	var column int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('messages') WHERE name='folder_context_json'`).Scan(&column); err != nil {
		return err
	}
	if column == 0 {
		return nil // Partial-schema fixtures do not own folder events.
	}
	return db.InTransaction(ctx, func(tx *sql.Tx) error {
		for _, statement := range []string{
			`DROP TRIGGER IF EXISTS assistant_folder_context_owner_changed`,
			`ALTER TABLE messages ADD COLUMN folder_context_expanded_json TEXT
				CHECK (folder_context_expanded_json IS NULL OR (length(CAST(folder_context_expanded_json AS BLOB)) <= 9216 AND role = 'system'))`,
			`UPDATE messages SET folder_context_expanded_json = folder_context_json WHERE folder_context_json IS NOT NULL`,
			`ALTER TABLE messages DROP COLUMN folder_context_json`,
			`ALTER TABLE messages RENAME COLUMN folder_context_expanded_json TO folder_context_json`,
			`CREATE TRIGGER assistant_folder_context_owner_changed
				AFTER UPDATE OF workspace_id, agent_name ON sessions
				WHEN OLD.workspace_id IS NOT NEW.workspace_id OR OLD.agent_name IS NOT NEW.agent_name
				BEGIN UPDATE messages SET folder_context_json = NULL WHERE session_id = NEW.id AND folder_context_json IS NOT NULL; END`,
		} {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
		return nil
	})
}
