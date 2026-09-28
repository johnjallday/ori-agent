package dailybrief

import (
	"context"
	"database/sql"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// InterruptedByTransfer is the failure reason recorded for a brief attempt
// that was still pending or running when the source checkpoint was taken.
const InterruptedByTransfer = "Interrupted by workspace transfer; this brief was not finished."

// RestoreContinuityRevision inserts retained history under the exact
// operation/workspace receipt. A pending or running source attempt becomes a
// failed, non-current revision marked interrupted: a copied row never resumes
// generation. A finished first-open or scheduled revision also restores its
// terminal completion claim, so enabling automation here later does not
// regenerate (or notify about) a brief that already exists for that date.
// No live claim, notification, generation job or fresh timestamp is created.
// The caller must roll back the entire transaction on any error.
func (s *SQLiteStore) RestoreContinuityRevision(ctx context.Context, tx *sql.Tx, scope workspacecontinuity.RestoreScope, record workspacecontinuity.Record) (bool, error) {
	if s == nil || tx == nil || scope.UserID != "local" {
		return false, workspacecontinuity.ErrInvalid
	}
	value, err := DecodeContinuityRevision(record, scope.WorkspaceID)
	if err != nil {
		return false, err
	}
	r := value.Revision
	if r.Status == GenerationRunning || r.Status == GenerationPending {
		r.Status, r.IsCurrent, r.FailureReason = GenerationFailed, false, InterruptedByTransfer
	}
	claimed, err := workspacecontinuity.ClaimRecord(ctx, tx, scope, "brief_history", "revisions", record.ID, workspacecontinuity.Digest(record.Data))
	if err != nil || !claimed {
		return false, err
	}
	if err := workspacecontinuity.RequireOwnedWorkspace(ctx, tx, scope); err != nil {
		return false, err
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM daily_brief_revision WHERE id=? OR (workspace_id=? AND local_date=? AND revision_number=?)`, r.ID, r.WorkspaceID, r.LocalDate, r.RevisionNumber).Scan(&count); err != nil {
		return false, err
	}
	if count != 0 {
		return false, workspacecontinuity.ErrConflict
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO daily_brief_revision
		(id,workspace_id,user_id,local_date,revision_number,is_current,trigger_type,status,config_revision,content_json,
		source_window_start,source_window_end,failure_reason,generated_at,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.ID, r.WorkspaceID, r.UserID, r.LocalDate, r.RevisionNumber, r.IsCurrent, r.Trigger, r.Status, r.ConfigRevision, r.ContentJSON,
		nullableContinuityTime(r.SourceWindowStart), nullableContinuityTime(r.SourceWindowEnd), r.FailureReason,
		nullableContinuityTime(r.GeneratedAt), r.CreatedAt)
	if err != nil {
		return false, workspacecontinuity.ErrConflict
	}
	if r.Trigger != TriggerManual && (r.Status == GenerationSucceeded || r.Status == GenerationPartial) {
		finished := r.GeneratedAt
		if finished.IsZero() {
			finished = r.CreatedAt
		}
		// Completion evidence only; the dedupe index already admits at most one
		// non-failed automatic claim per date, and an existing one is kept.
		if _, err := tx.ExecContext(ctx, `INSERT INTO daily_brief_generation_claim
			(id,workspace_id,local_date,trigger_type,status,revision_id,error,claimed_at,finished_at)
			SELECT ?,?,?,?,?,?,'',?,? WHERE NOT EXISTS(SELECT 1 FROM daily_brief_generation_claim
			WHERE workspace_id=? AND local_date=? AND trigger_type!='manual' AND status!='failed')`,
			"continuity-"+r.ID, r.WorkspaceID, r.LocalDate, r.Trigger, r.Status, r.ID, r.CreatedAt, finished,
			r.WorkspaceID, r.LocalDate); err != nil {
			return false, workspacecontinuity.ErrConflict
		}
	}
	return true, nil
}

func nullableContinuityTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}
