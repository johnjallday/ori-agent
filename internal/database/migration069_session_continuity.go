package database

import (
	"context"
	"database/sql"
	"fmt"
)

// Session history dirties its exact SQL workspace, including the old owner on
// reassignment/deletion. Message and tag writes do not go through workspace
// saves. The source sequence is historical display order, never a replay token.
func (db *DB) migration069SessionContinuity(ctx context.Context) error {
	for _, table := range []string{"sessions", "messages", "session_tags", "tool_calls"} {
		var count int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			return nil
		} // Partial-schema migration fixtures do not own this domain.
	}
	// Some migration fixtures carry current tables but intentionally rewind
	// schema_migrations. DDL must also survive a retry after a partial upgrade.
	var hasSequence int
	columns, err := db.QueryContext(ctx, `PRAGMA table_info(messages)`)
	if err != nil {
		return err
	}
	for columns.Next() {
		var cid, notNull, pk int
		var name, kind string
		var defaultValue any
		if err := columns.Scan(&cid, &name, &kind, &notNull, &defaultValue, &pk); err != nil {
			_ = columns.Close()
			return err
		}
		if name == "continuity_source_sequence" {
			hasSequence++
		}
	}
	if err := columns.Err(); err != nil {
		_ = columns.Close()
		return err
	}
	if err := columns.Close(); err != nil {
		return err
	}
	// idx_messages_session_id already orders equal session IDs by rowid.
	statements := []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_messages_continuity_sequence ON messages(session_id,continuity_source_sequence) WHERE continuity_source_sequence IS NOT NULL`,
	}
	if hasSequence == 0 {
		statements = append([]string{`ALTER TABLE messages ADD COLUMN continuity_source_sequence INTEGER`}, statements...)
	}
	statements = append(statements, []string{
		`CREATE TRIGGER IF NOT EXISTS continuity_sessions_insert AFTER INSERT ON sessions WHEN COALESCE(NEW.workspace_id,'')<>'' BEGIN
			INSERT INTO continuity_dirty(workspace_id,sequence) VALUES(NEW.workspace_id,1) ON CONFLICT(workspace_id) DO UPDATE SET sequence=sequence+1,last_error=''; END`,
		`CREATE TRIGGER IF NOT EXISTS continuity_sessions_update AFTER UPDATE ON sessions BEGIN
			INSERT INTO continuity_dirty(workspace_id,sequence) SELECT OLD.workspace_id,1 WHERE COALESCE(OLD.workspace_id,'')<>'' AND OLD.workspace_id IS NOT NEW.workspace_id
			ON CONFLICT(workspace_id) DO UPDATE SET sequence=sequence+1,last_error='';
			INSERT INTO continuity_dirty(workspace_id,sequence) SELECT NEW.workspace_id,1 WHERE COALESCE(NEW.workspace_id,'')<>''
			ON CONFLICT(workspace_id) DO UPDATE SET sequence=sequence+1,last_error=''; END`,
		`CREATE TRIGGER IF NOT EXISTS continuity_sessions_delete BEFORE DELETE ON sessions WHEN COALESCE(OLD.workspace_id,'')<>'' BEGIN
			INSERT INTO continuity_dirty(workspace_id,sequence) VALUES(OLD.workspace_id,1) ON CONFLICT(workspace_id) DO UPDATE SET sequence=sequence+1,last_error=''; END`,
	}...)
	for _, owner := range []struct{ table, join string }{
		{"messages", "session_id"}, {"session_tags", "session_id"}, {"tool_calls", "session_id"},
	} {
		for _, event := range []string{"INSERT", "UPDATE", "DELETE"} {
			refs := []string{"NEW"}
			when := "AFTER"
			if event == "DELETE" {
				refs, when = []string{"OLD"}, "BEFORE"
			}
			if event == "UPDATE" {
				refs = []string{"OLD", "NEW"}
			}
			body := ""
			for _, ref := range refs {
				body += fmt.Sprintf(`INSERT INTO continuity_dirty(workspace_id,sequence)
					SELECT s.workspace_id,1 FROM sessions s WHERE s.id=%[1]s.%[2]s AND COALESCE(s.workspace_id,'')<>''
					ON CONFLICT(workspace_id) DO UPDATE SET sequence=sequence+1,last_error='';`, ref, owner.join)
			}
			statements = append(statements, fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS continuity_%s_%s %s %s ON %s BEGIN %s END`, owner.table, event, when, event, owner.table, body))
		}
	}
	return db.InTransaction(ctx, func(tx *sql.Tx) error {
		for _, stmt := range statements {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("install session continuity: %w", err)
			}
		}
		return nil
	})
}
