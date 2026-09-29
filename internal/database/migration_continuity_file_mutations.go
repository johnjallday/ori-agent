package database

import (
	"context"
	"fmt"
)

// A pre-write dirty sequence alone does not fence an unfinished file mutation.
// These installation-local barriers survive crashes and never travel with the
// workspace. One target has at most one pending/failed attempt; successful full
// replacement or explicit canonical reconciliation removes it.
func (db *DB) migration066ContinuityFileMutations(ctx context.Context) error {
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS continuity_file_mutations (
		workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
		target TEXT NOT NULL,
		token TEXT NOT NULL UNIQUE,
		state TEXT NOT NULL CHECK(state IN ('pending','failed')),
		started_at DATETIME NOT NULL,
		PRIMARY KEY(workspace_id,target)
	)`)
	if err != nil {
		return fmt.Errorf("create continuity file mutation barriers: %w", err)
	}
	return nil
}
