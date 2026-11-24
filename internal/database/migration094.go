package database

import (
	"context"
	"database/sql"
)

// Device-local evidence, never part of a portable workspace. On upgrade no
// search grant or link is touched. Removal forgets the connection but leaves
// the physical witness so a replaced directory cannot inherit the old link.
func (db *DB) migration094FilenameWorkspaceLinks(ctx context.Context) error {
	return db.InTransaction(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS filename_workspace_links (
 owner TEXT NOT NULL, workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
 reference_id TEXT NOT NULL, witness TEXT NOT NULL,
 source TEXT UNIQUE REFERENCES filename_sources(id) ON DELETE SET NULL,
 PRIMARY KEY(owner,workspace_id,reference_id))`)
		return err
	})
}
