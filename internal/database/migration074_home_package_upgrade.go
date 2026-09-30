package database

import (
	"context"
	"fmt"
)

// A reviewed Home package upgrade (docs/architecture/independent-program-homes.md
// §6.1) moves every Home pinned to one installed package, and their linked
// projects, onto one exact newer release. The review is short-lived consent
// bound to a digest of the whole plan; the operation is the durable record that
// lets a crash between the plugin replacement and the workspace rebind resume
// instead of stranding the Homes. The plan is host-derived package evidence and
// workspace IDs only: no path, credential, or user-authored text.
func (db *DB) migration074HomePackageUpgrade(ctx context.Context) error {
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS home_package_upgrade_review (
			token TEXT PRIMARY KEY,
			owner_user_id TEXT NOT NULL,
			plugin_id TEXT NOT NULL,
			plan_digest TEXT NOT NULL CHECK (length(plan_digest) = 64),
			created_at DATETIME NOT NULL,
			expires_at DATETIME NOT NULL,
			consumed_at DATETIME
		)`,
		`CREATE INDEX IF NOT EXISTS idx_home_package_upgrade_review_expiry
			ON home_package_upgrade_review(expires_at)`,
		// One active operation per package. While 'claimed', the plugin
		// replacement guard allows exactly its from→to replacement; a
		// 'reconcile_required' operation holds the slot until an owner-reviewed
		// reconciliation settles it.
		`CREATE TABLE IF NOT EXISTS home_package_upgrade_operation (
			id TEXT PRIMARY KEY,
			owner_user_id TEXT NOT NULL,
			plugin_id TEXT NOT NULL,
			review_token TEXT NOT NULL UNIQUE,
			plan_digest TEXT NOT NULL CHECK (length(plan_digest) = 64),
			plan_json TEXT NOT NULL,
			status TEXT NOT NULL CHECK (status IN ('claimed', 'replaced', 'reconcile_required', 'succeeded', 'cancelled')),
			target_generation INTEGER NOT NULL DEFAULT 0,
			outcome_json TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY (review_token) REFERENCES home_package_upgrade_review(token) ON DELETE RESTRICT
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_home_package_upgrade_operation_active
			ON home_package_upgrade_operation(plugin_id)
			WHERE status IN ('claimed', 'replaced', 'reconcile_required')`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("create Home package upgrade schema: %w", err)
		}
	}
	return nil
}
