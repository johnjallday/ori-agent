package database

import "context"

// migration082AssistantArrangementEffects correlates a saved PA arrangement's
// create_group intentions with the existing reviewed folder setups that carry
// them out. A row holds canonical IDs and counts only: no path, folder name or
// membership copy. Older databases gain an empty table, never an execution.
// The owning store enforces state transitions and owner/context checks.
func (db *DB) migration082AssistantArrangementEffects(ctx context.Context) error {
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS assistant_arrangement_effects (
			arrangement_id TEXT NOT NULL REFERENCES assistant_arrangements(id) ON DELETE CASCADE,
			session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
			operation INTEGER NOT NULL CHECK(operation >= 0 AND operation < 32),
			record_revision INTEGER NOT NULL CHECK(record_revision > 0),
			offer_id TEXT NOT NULL UNIQUE CHECK(length(offer_id) BETWEEN 1 AND 200),
			message_id TEXT NOT NULL CHECK(length(message_id) BETWEEN 1 AND 200),
			state TEXT NOT NULL CHECK(state IN ('awaiting_user_review','applying','partially_applied','completed','closed')),
			workspace_id TEXT NOT NULL DEFAULT '' CHECK(length(workspace_id) <= 200),
			route TEXT NOT NULL DEFAULT '' CHECK(length(route) <= 512),
			workspaces INTEGER NOT NULL DEFAULT 0 CHECK(workspaces >= 0 AND workspaces <= 24),
			receipt_digest TEXT NOT NULL DEFAULT '' CHECK(receipt_digest = '' OR length(receipt_digest) = 64),
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL,
			PRIMARY KEY(arrangement_id, operation),
			CHECK((state = 'completed') = (workspace_id <> '' AND receipt_digest <> '' AND workspaces > 0))
		);
		CREATE INDEX IF NOT EXISTS idx_assistant_arrangement_effects_session ON assistant_arrangement_effects(session_id);
	`)
	return err
}
