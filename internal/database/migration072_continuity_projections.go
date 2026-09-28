package database

import (
	"context"
	"fmt"
)

// migration072ContinuityProjections records, per confirmed import, the digest
// of each folder file the import rewrote (workspace.json, agent profiles,
// triggers, knowledge metadata). A retry then accepts a file only when it still
// holds either the reviewed bytes or exactly that projection — anything else
// changed after review, and the import stops instead of installing it raw.
// Local receipt state, reset with it.
func (db *DB) migration072ContinuityProjections(ctx context.Context) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS continuity_projections (
		operation_id TEXT NOT NULL,
		workspace_id TEXT NOT NULL,
		path TEXT NOT NULL,
		digest TEXT NOT NULL,
		PRIMARY KEY(operation_id, workspace_id, path)
	)`); err != nil {
		return fmt.Errorf("install continuity projections: %w", err)
	}
	return nil
}
