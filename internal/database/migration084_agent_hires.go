package database

import (
	"context"
	"fmt"
)

// A hire from the Agents page (agents-hire-with-a-brief) remembers what it
// created and how its first look went, so the assistant can report back and a
// retried request does not hire twice. Every column is short text the hire
// itself wrote — names, ids, the brief, a state — and none is a path.
func (db *DB) migration084AgentHires(ctx context.Context) error {
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS agent_hires (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			request_id TEXT NOT NULL UNIQUE,
			agent_name TEXT NOT NULL,
			workspace_id TEXT NOT NULL,
			run_id TEXT NOT NULL DEFAULT '',
			task_id TEXT NOT NULL DEFAULT '',
			brief TEXT NOT NULL,
			role TEXT NOT NULL,
			blueprint TEXT NOT NULL,
			state TEXT NOT NULL CHECK (state IN ('looking', 'reported', 'started', 'dismissed', 'failed')),
			failure TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			finished_at DATETIME
		)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_hires_user_created
			ON agent_hires(user_id, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_hires_workspace
			ON agent_hires(workspace_id)`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("create agent hires schema: %w", err)
		}
	}
	return nil
}
