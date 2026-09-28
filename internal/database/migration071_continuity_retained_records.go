package database

import (
	"context"
	"database/sql"
	"fmt"
)

// migration071ContinuityRetainedRecords keeps the incoming assistant evidence
// of a workspace imported without adoption (its agreement and verified setup
// results), inert, so that workspace's own checkpoint can keep carrying it.
// It is local receipt state: reset erases it, and removing the workspace row
// removes it.
func (db *DB) migration071ContinuityRetainedRecords(ctx context.Context) error {
	statements := []string{`CREATE TABLE IF NOT EXISTS continuity_retained_records (
		workspace_id TEXT NOT NULL,
		domain TEXT NOT NULL CHECK(domain IN ('assistant','setup')),
		family TEXT NOT NULL,
		record_id TEXT NOT NULL,
		data BLOB NOT NULL,
		operation_id TEXT NOT NULL,
		PRIMARY KEY(workspace_id, domain, record_id)
	)`}
	var workspaces int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='workspaces'`).Scan(&workspaces); err != nil {
		return err
	}
	if workspaces == 1 {
		statements = append(statements, `CREATE TRIGGER IF NOT EXISTS continuity_retained_records_cleanup AFTER DELETE ON workspaces
			BEGIN DELETE FROM continuity_retained_records WHERE workspace_id=OLD.id; END`)
	}
	return db.InTransaction(ctx, func(tx *sql.Tx) error {
		for _, statement := range statements {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("install continuity retained records: %w", err)
			}
		}
		return nil
	})
}
