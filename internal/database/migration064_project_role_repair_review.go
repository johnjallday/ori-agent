package database

import (
	"context"
	"fmt"
)

// A project role-snapshot repair is not a Setup Journey staffing step. Keep its
// short-lived review identity outside workspace mirrors so recording consent
// cannot itself change an old child's link, Home or portable provenance.
// This table authorizes no mutation until a separate typed commit/recovery
// protocol exists; a token alone must never be accepted as a staffing review.
func (db *DB) migration064ProjectRoleRepairReview(ctx context.Context) error {
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS project_role_repair_review (
			token TEXT PRIMARY KEY,
			owner_user_id TEXT NOT NULL,
			home_id TEXT NOT NULL,
			project_id TEXT NOT NULL,
			idempotency_key TEXT NOT NULL,
			evidence_digest TEXT NOT NULL CHECK (length(evidence_digest) = 64),
			created_at DATETIME NOT NULL,
			expires_at DATETIME NOT NULL,
			consumed_at DATETIME,
			UNIQUE(owner_user_id, home_id, project_id, idempotency_key)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_project_role_repair_review_expiry
			ON project_role_repair_review(expires_at)`,
		// A claimed operation is only a fenced intent: no child update is
		// authorized by this schema. An uncertain intent holds the slot until
		// a separately designed reconciliation path settles both mirrors.
		`CREATE TABLE IF NOT EXISTS project_role_repair_operation (
			token TEXT PRIMARY KEY,
			owner_user_id TEXT NOT NULL,
			home_id TEXT NOT NULL,
			project_id TEXT NOT NULL,
			idempotency_key TEXT NOT NULL,
			review_token TEXT NOT NULL UNIQUE,
			evidence_digest TEXT NOT NULL CHECK (length(evidence_digest) = 64),
			status TEXT NOT NULL CHECK (status IN ('claimed', 'reconcile_required', 'succeeded', 'cancelled')),
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			UNIQUE(owner_user_id, home_id, project_id, idempotency_key),
			FOREIGN KEY (review_token) REFERENCES project_role_repair_review(token) ON DELETE RESTRICT
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_project_role_repair_operation_active_child
			ON project_role_repair_operation(project_id)
			WHERE status IN ('claimed', 'reconcile_required')`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("create project role repair review schema: %w", err)
		}
	}
	return nil
}
