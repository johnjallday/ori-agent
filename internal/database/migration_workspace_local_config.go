package database

import (
	"context"
	"fmt"
)

// Private workspace configuration is installation-local, never a continuity
// domain. Rows are encrypted with a key held by the installation secret backend.
// A canonical admitted workspace must already exist. Private rows precede file
// reference publication, not workspace creation. Deleting the canonical workspace
// forgets its private configuration; unpublished slots are pruned on retry.
func (db *DB) migration065WorkspaceLocalConfig(ctx context.Context) error {
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS workspace_local_config (
		workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
		kind TEXT NOT NULL CHECK(kind IN ('agent','bindings')),
		item_id TEXT NOT NULL,
		authority_id TEXT NOT NULL,
		slot_id TEXT NOT NULL,
		ciphertext BLOB NOT NULL,
		created_at DATETIME NOT NULL,
		PRIMARY KEY(workspace_id,kind,item_id,slot_id)
	)`)
	if err != nil {
		return fmt.Errorf("create private workspace configuration: %w", err)
	}
	return nil
}
