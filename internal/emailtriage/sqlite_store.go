package emailtriage

import (
	"context"
	"fmt"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
)

// stateRetention is how long a thread's state is kept after its newest
// message. The list reads a week back, so anything older is never looked up;
// keeping a little longer lets a thread that comes back inside a month keep
// the user's call on it.
const stateRetention = 30 * 24 * time.Hour

// SQLiteStore keeps triage state in the local app database (migration 075).
type SQLiteStore struct {
	db  *database.DB
	now func() time.Time
}

// NewSQLiteStore builds the store over db.
func NewSQLiteStore(db *database.DB) *SQLiteStore {
	return &SQLiteStore{db: db, now: time.Now}
}

var _ Store = (*SQLiteStore)(nil)

// Load returns every saved thread state for scope, by thread ID.
func (s *SQLiteStore) Load(ctx context.Context, scope Scope) (map[string]State, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT thread_id, last_message_at, bucket, kind, rule, why,
			subject, from_label, user_bucket, followup_id, updated_at
		FROM email_triage_state WHERE workspace_id = ? AND account_id = ?`,
		scope.WorkspaceID, scope.AccountID)
	if err != nil {
		return nil, fmt.Errorf("load email triage state: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[string]State{}
	for rows.Next() {
		var state State
		var bucket, kind, userBucket string
		if err := rows.Scan(&state.ThreadID, &state.LastMessageAt, &bucket, &kind, &state.Rule, &state.Why,
			&state.Subject, &state.From, &userBucket, &state.FollowUpID, &state.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan email triage state: %w", err)
		}
		state.Bucket, state.Kind, state.UserBucket = Bucket(bucket), Kind(kind), Bucket(userBucket)
		state.LastMessageAt, state.UpdatedAt = state.LastMessageAt.UTC(), state.UpdatedAt.UTC()
		out[state.ThreadID] = state
	}
	return out, rows.Err()
}

// Save writes states for scope, replacing earlier rows for the same threads,
// and drops rows whose thread has been quiet longer than stateRetention.
func (s *SQLiteStore) Save(ctx context.Context, scope Scope, states []State) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin email triage save: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, state := range states {
		if _, err := tx.ExecContext(ctx, `INSERT INTO email_triage_state
				(workspace_id, account_id, thread_id, last_message_at, bucket, kind, rule, why,
				 subject, from_label, user_bucket, followup_id, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (workspace_id, account_id, thread_id) DO UPDATE SET
				last_message_at = excluded.last_message_at, bucket = excluded.bucket, kind = excluded.kind,
				rule = excluded.rule, why = excluded.why, subject = excluded.subject,
				from_label = excluded.from_label, user_bucket = excluded.user_bucket,
				followup_id = excluded.followup_id, updated_at = excluded.updated_at`,
			scope.WorkspaceID, scope.AccountID, state.ThreadID, state.LastMessageAt.UTC(),
			string(state.Bucket), string(state.Kind), state.Rule, state.Why,
			state.Subject, state.From, string(state.UserBucket), state.FollowUpID, state.UpdatedAt.UTC()); err != nil {
			return fmt.Errorf("save email triage state: %w", err)
		}
	}
	cutoff := s.now().UTC().Add(-stateRetention)
	if _, err := tx.ExecContext(ctx, `DELETE FROM email_triage_state
		WHERE workspace_id = ? AND account_id = ? AND last_message_at < ?`,
		scope.WorkspaceID, scope.AccountID, cutoff); err != nil {
		return fmt.Errorf("prune email triage state: %w", err)
	}
	return tx.Commit()
}
