package database

import (
	"context"
	"database/sql"
)

// The registration witness persists even if the workspace is deleted before
// the purchase receipt can be written. Absence alone would otherwise mistake
// a delivered, removed workspace for an attempt safe to refund and repeat.
func (db *DB) migration092FilingExpansion(ctx context.Context) error {
	// Some legacy upgrade paths have no workspace table at all. Keep the
	// claim schema, but only install a registration witness on that table
	// when present; a reserved ID is globally unique regardless of owner.
	hasWorkspaces, err := db.tableExists(ctx, "workspaces")
	if err != nil {
		return err
	}
	return db.InTransaction(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS economy_filing_claims (
				id TEXT PRIMARY KEY,
				owner_user_id TEXT NOT NULL,
				request_id TEXT NOT NULL,
				workspace_id TEXT NOT NULL UNIQUE,
				base_cost INTEGER NOT NULL CHECK (base_cost > 0),
				cost INTEGER NOT NULL CHECK (cost = 0 OR cost = base_cost),
				plan_json TEXT NOT NULL,
				state TEXT NOT NULL DEFAULT 'building' CHECK (state IN ('building','delivered','refunded')),
				registered INTEGER NOT NULL DEFAULT 0 CHECK (registered IN (0,1)),
				UNIQUE (owner_user_id, request_id)
			);
			CREATE UNIQUE INDEX IF NOT EXISTS economy_filing_one_active
				ON economy_filing_claims(owner_user_id) WHERE state != 'refunded';
		`)
		if err != nil || !hasWorkspaces {
			return err
		}
		_, err = tx.ExecContext(ctx, `CREATE TRIGGER IF NOT EXISTS economy_filing_remember_registration
			AFTER INSERT ON workspaces
			BEGIN
				UPDATE economy_filing_claims SET registered = 1
				WHERE workspace_id = NEW.id AND state = 'building';
			END;
		`)
		return err
	})
}
