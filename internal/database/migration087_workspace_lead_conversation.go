package database

import "context"

// Local delivery bookkeeping, not portable workspace state or an execution
// grant. Deliberately no session FK: deleting a welcome must not resurrect it.
func (db *DB) migration087WorkspaceLeadConversation(ctx context.Context) error {
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS workspace_lead_conversations (
		workspace_id TEXT PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
		owner_user_id TEXT NOT NULL,
		agent_instance_id TEXT NOT NULL,
		agent_name TEXT NOT NULL,
		session_id TEXT NOT NULL DEFAULT '',
		assessment_delivered INTEGER NOT NULL DEFAULT 0 CHECK (assessment_delivered IN (0,1)),
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	return err
}
