package followup

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ReviewedSnooze is an already reviewed Personal HQ command. It carries the
// server-resolved owner, the exact revision and a fixed absolute time. It is
// not the request DTO and never accepts a guessed row or relative date.
type ReviewedSnooze struct {
	UserID, HQWorkspaceID, AssistantID, ConversationID, FollowUpID string
	RelationshipVersion                                            int64
	ExpectedUpdatedAt, Until                                       time.Time
}

// nextUpdatedAt also advances under coarse or frozen clocks. UpdatedAt is
// the row revision: no two successful transitions from it may reuse it.
func nextUpdatedAt(now, expected time.Time) time.Time {
	if !now.After(expected) {
		now = expected.Add(time.Nanosecond)
	}
	return now.UTC()
}

// SnoozeReviewed changes only the reviewed row's snooze state. Manual Snooze
// keeps its wider eligibility; it is not called by this path.
func (s *Service) SnoozeReviewed(ctx context.Context, review ReviewedSnooze) (*FollowUp, error) {
	now := s.now()
	if review.UserID == "" || review.HQWorkspaceID == "" || review.AssistantID == "" || review.ConversationID == "" ||
		review.FollowUpID == "" || review.RelationshipVersion < 1 || review.ExpectedUpdatedAt.IsZero() || !review.Until.After(now) {
		return nil, ErrConflict
	}
	item, err := s.store.SnoozeReviewed(ctx, review, nextUpdatedAt(now, review.ExpectedUpdatedAt))
	if err != nil {
		return nil, err
	}
	s.notifyChanged(review.UserID)
	return item, nil
}

func (s *SQLiteStore) SnoozeReviewed(ctx context.Context, review ReviewedSnooze, updatedAt time.Time) (*FollowUp, error) {
	// Recheck both the row and the relationship in the write statement. A
	// concurrent HQ replacement, conversation delete, edit or completion
	// cannot fit between this check and the write. Drain the RETURNING rows
	// so deferred statement errors are checked before reporting success.
	rows, err := s.db.QueryContext(ctx, `UPDATE personal_hq_followup
		SET status=?, snoozed_until=?, updated_at=?
		WHERE user_id=? AND workspace_id=? AND id=? AND updated_at=? AND status IN (?,?)
		AND EXISTS (
			SELECT 1 FROM personal_assistant_state a
			JOIN sessions c ON c.workspace_id=a.hq_workspace_id AND c.agent_name=a.global_agent_profile_name COLLATE NOCASE
			JOIN workspaces w ON w.id=a.hq_workspace_id
			WHERE a.user_id=? AND a.assistant_id=? AND a.hq_workspace_id=? AND a.state_version=? AND a.status='active'
			AND c.id=? AND w.status NOT IN ('trashed','missing') AND (w.owner_user_id=a.user_id OR (w.owner_user_id='' AND a.user_id='local'))
		) RETURNING `+followUpColumns,
		string(StatusSnoozed), review.Until.UTC(), updatedAt,
		review.UserID, review.HQWorkspaceID, review.FollowUpID, review.ExpectedUpdatedAt, string(StatusActive), string(StatusReopened),
		review.UserID, review.AssistantID, review.HQWorkspaceID, review.RelationshipVersion, review.ConversationID)
	if err != nil {
		return nil, fmt.Errorf("followup reviewed snooze: %w", err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("followup reviewed snooze: %w", err)
		}
		return nil, ErrConflict
	}
	item, err := scanFollowUp(rows)
	if err != nil {
		return nil, fmt.Errorf("followup reviewed snooze result: %w", err)
	}
	if rows.Next() {
		return nil, errors.New("followup reviewed snooze: unexpected extra row")
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("followup reviewed snooze: %w", err)
	}
	return item, nil
}
