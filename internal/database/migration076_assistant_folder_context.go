package database

import "context"

// Observations are typed canonical messages, not reusable filesystem grants.
// Existing rows and all ordinary/imported message writers leave this NULL.
func (db *DB) migration076AssistantFolderContext(ctx context.Context) error {
	// Partial-schema fixtures do not own Messages; rewound fixtures and a
	// retried migration may already carry the new column.
	var exists, column int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='messages'`).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return nil
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('messages') WHERE name='folder_context_json'`).Scan(&column); err != nil {
		return err
	}
	if column == 0 {
		if _, err := db.ExecContext(ctx, `ALTER TABLE messages ADD COLUMN folder_context_json TEXT
			CHECK (folder_context_json IS NULL OR (length(CAST(folder_context_json AS BLOB)) <= 8448 AND role = 'system'))`); err != nil {
			return err
		}
	}
	// Shallow workspace deletion moves Sessions to root (ON DELETE SET NULL).
	// Reassignment/rename cannot carry a prior owner's active folder binding.
	_, err := db.ExecContext(ctx, `CREATE TRIGGER IF NOT EXISTS assistant_folder_context_owner_changed
		AFTER UPDATE OF workspace_id, agent_name ON sessions
		WHEN OLD.workspace_id IS NOT NEW.workspace_id OR OLD.agent_name IS NOT NEW.agent_name
		BEGIN UPDATE messages SET folder_context_json = NULL WHERE session_id = NEW.id AND folder_context_json IS NOT NULL; END`)
	return err
}
