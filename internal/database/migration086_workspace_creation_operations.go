package database

import (
	"context"
	"fmt"
)

// Ordinary reviewed creation has its own delivery journal. Group-policy,
// assistant preparation and import operations retain their separate protocols.
// No legacy workspace is assigned a claim or inferred consent by this migration.
func (db *DB) migration086WorkspaceCreationOperations(ctx context.Context) error {
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS workspace_creation_operations (
		id TEXT PRIMARY KEY,
		owner_user_id TEXT NOT NULL,
		request_digest TEXT NOT NULL,
		source_digest TEXT NOT NULL,
		request_json TEXT NOT NULL,
		workspace_id TEXT NOT NULL UNIQUE,
		state TEXT NOT NULL CHECK (state IN ('reviewed', 'accepted', 'complete', 'refused', 'abandoned', 'reconciliation_required')),
		response_status INTEGER NOT NULL DEFAULT 0,
		response_json TEXT NOT NULL DEFAULT '',
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		return fmt.Errorf("create ordinary workspace operation journal: %w", err)
	}
	return nil
}
