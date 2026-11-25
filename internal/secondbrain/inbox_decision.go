package secondbrain

import (
	"context"
	"errors"
)

var ErrReviewConflict = errors.New("inbox review: decision could not be confirmed")

// Decide persists the first decision in a single statement, including when an
// interrupted capture has a receipt but no indexed row. Never overwrite a
// previous claim, even when the next attempt selects a different workspace.
func (l ReviewLedger) Decide(ctx context.Context, key string, a InboxAction, workspace string) (InboxAction, error) {
	state := "claiming"
	if workspace == "" {
		state = "dismissed"
	}
	err := l.DB.QueryRowContext(ctx, `INSERT INTO inbox_review_actions(note_key,action_id,revision,state,workspace_id) VALUES(?,?,?,?,?)
 ON CONFLICT(note_key,revision,action_id) DO UPDATE SET
 workspace_id=CASE WHEN state='pending' THEN excluded.workspace_id ELSE workspace_id END,
 state=CASE WHEN state='pending' THEN excluded.state ELSE state END
 RETURNING state,workspace_id,ticket_id`, key, a.ID, a.Revision, state, workspace).Scan(&a.State, &a.Workspace, &a.Ticket)
	return a, err
}

func (l ReviewLedger) Finish(ctx context.Context, key string, a InboxAction, ticket string) error {
	if ticket == "" {
		return ErrReviewConflict
	}
	result, err := l.DB.ExecContext(ctx, `UPDATE inbox_review_actions SET state='accepted',ticket_id=? WHERE note_key=? AND revision=? AND action_id=? AND workspace_id=? AND (state='claiming' OR (state='accepted' AND ticket_id=?))`, ticket, key, a.Revision, a.ID, a.Workspace, ticket)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrReviewConflict
	}
	return nil
}
