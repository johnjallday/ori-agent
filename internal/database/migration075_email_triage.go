package database

import (
	"context"
	"fmt"
)

// The Email Ops "Needs you" list remembers its call on each inbox thread, so
// the list is stable between reads and the model is asked only about what is
// new. A row holds until the thread gets a newer message. It carries the
// thread's subject, sender label, and a one-line reason, all derived from mail,
// so it lives here in the local app database rather than in a workspace
// folder that may be synced elsewhere.
func (db *DB) migration075EmailTriage(ctx context.Context) error {
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS email_triage_state (
			workspace_id TEXT NOT NULL,
			account_id TEXT NOT NULL,
			thread_id TEXT NOT NULL,
			last_message_at DATETIME NOT NULL,
			bucket TEXT NOT NULL CHECK (bucket IN ('needs_you', 'fyi', 'ignorable', 'handled')),
			kind TEXT NOT NULL DEFAULT '',
			rule TEXT NOT NULL DEFAULT '',
			why TEXT NOT NULL DEFAULT '',
			subject TEXT NOT NULL DEFAULT '',
			from_label TEXT NOT NULL DEFAULT '',
			user_bucket TEXT NOT NULL DEFAULT '' CHECK (user_bucket IN ('', 'needs_you', 'fyi', 'ignorable')),
			followup_id TEXT NOT NULL DEFAULT '',
			updated_at DATETIME NOT NULL,
			PRIMARY KEY (workspace_id, account_id, thread_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_email_triage_state_age
			ON email_triage_state(last_message_at)`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("create email triage schema: %w", err)
		}
	}
	return nil
}
