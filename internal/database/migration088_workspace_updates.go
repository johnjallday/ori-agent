package database

import (
	"context"
	"database/sql"
)

// Welcome delivery/read state is local bookkeeping, never portable authority.
// There is deliberately no backfill: pre-feature messages remain baseline-seen,
// and legacy workspaces are not enrolled by opening an upgraded database.
func (db *DB) migration088WorkspaceUpdates(ctx context.Context) error {
	return db.InTransaction(ctx, func(tx *sql.Tx) error {
		// Like the earlier additive workspace migrations, tolerate reduced
		// historical schemas without inventing missing workspace authority.
		// Full application schemas always receive the bounded-listing index.
		var columns int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('workspaces')
			WHERE name IN ('id', 'owner_user_id', 'status')`).Scan(&columns); err != nil {
			return err
		}
		if columns == 3 {
			if _, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_workspaces_updates_owner_id
				ON workspaces(COALESCE(NULLIF(TRIM(owner_user_id), ''), 'local'), id)
				WHERE COALESCE(status, '') NOT IN ('trashed','missing')`); err != nil {
				return err
			}
		}
		for _, statement := range []string{
			`CREATE TABLE IF NOT EXISTS workspace_welcome_enrollments (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				workspace_id TEXT NOT NULL UNIQUE REFERENCES workspaces(id) ON DELETE CASCADE,
				owner_user_id TEXT NOT NULL,
				agent_instance_id TEXT NOT NULL,
				agent_name TEXT NOT NULL,
				settled INTEGER NOT NULL DEFAULT 0 CHECK (settled IN (0,1))
			)`,
			`CREATE INDEX IF NOT EXISTS idx_workspace_welcome_pending
				ON workspace_welcome_enrollments(id) WHERE settled=0`,
			// No message/session FK: removing history must not erase delivery
			// identity or lend an old acknowledgement to a replacement thread.
			`CREATE TABLE IF NOT EXISTS workspace_welcome_messages (
				workspace_id TEXT NOT NULL REFERENCES workspace_lead_conversations(workspace_id) ON DELETE CASCADE,
				revision INTEGER NOT NULL CHECK (revision IN (1,2)),
				session_id TEXT NOT NULL,
				message_id TEXT NOT NULL UNIQUE,
				delivered_at DATETIME NOT NULL,
				seen_at DATETIME,
				PRIMARY KEY (workspace_id, revision)
			)`,
		} {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
		return nil
	})
}
