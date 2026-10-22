package database

import (
	"context"
	"fmt"
)

// A hire the personal assistant proposed from a plain request
// (assistant-hires-from-a-request) is kept beside the conversation it was
// offered in, so a reload shows the same card, a decline is remembered for the
// rest of that conversation, and the hire it became can be found again. Every
// column is short text the proposal itself wrote — a role, a name, the brief,
// a workspace name, a state — and none is a path.
func (db *DB) migration085AssistantHireProposals(ctx context.Context) error {
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS assistant_hire_proposals (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			conversation_id TEXT NOT NULL,
			message_id TEXT NOT NULL DEFAULT '',
			role TEXT NOT NULL,
			name TEXT NOT NULL,
			domain TEXT NOT NULL DEFAULT '',
			brief TEXT NOT NULL,
			workspace_name TEXT NOT NULL,
			blueprint TEXT NOT NULL,
			blueprint_label TEXT NOT NULL,
			open_roles INTEGER NOT NULL DEFAULT 0,
			state TEXT NOT NULL CHECK (state IN ('offered', 'declined', 'adjusted', 'hired')),
			hire_id TEXT NOT NULL DEFAULT '',
			agent_name TEXT NOT NULL DEFAULT '',
			workspace_id TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_assistant_hire_proposals_conversation
			ON assistant_hire_proposals(user_id, conversation_id, created_at)`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("create assistant hire proposals schema: %w", err)
		}
	}
	return nil
}
