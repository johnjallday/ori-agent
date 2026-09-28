package followup

import (
	"context"
	"database/sql"

	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// RestoreContinuityFollowUp inserts one exact owned historical record in the
// caller's receipt transaction. Local source IDs, dedup keys and related-task
// links remain inert in an installation-local provenance sidecar until explicit
// reconciliation; no Capture/notification/update hooks run. A claimed retry
// never replays the old record over a later local edit or deletion.
func (s *SQLiteStore) RestoreContinuityFollowUp(ctx context.Context, tx *sql.Tx, scope workspacecontinuity.RestoreScope, record workspacecontinuity.Record) (bool, error) {
	if s == nil || tx == nil || scope.UserID != "local" {
		return false, workspacecontinuity.ErrInvalid
	}
	value, err := DecodeContinuityFollowUp(record, scope.WorkspaceID)
	if err != nil {
		return false, err
	}
	claimed, err := workspacecontinuity.ClaimRecord(ctx, tx, scope, "followups", "followups", record.ID, workspacecontinuity.Digest(record.Data))
	if err != nil || !claimed {
		return false, err
	}
	if err := workspacecontinuity.RequireOwnedWorkspace(ctx, tx, scope); err != nil {
		return false, err
	}
	f := value.FollowUp
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM personal_hq_followup WHERE id=?`, f.ID).Scan(&count); err != nil {
		return false, err
	}
	if count != 0 {
		return false, workspacecontinuity.ErrConflict
	}
	account, dedup := f.Source.AccountID, f.DedupKey
	linkedWorkspace, linkedTask := taskWorkspace(f.RelatedTask), taskID(f.RelatedTask)
	f.Source.AccountID, f.DedupKey, f.RelatedTask = "", "", nil
	_, err = tx.ExecContext(ctx, `INSERT INTO personal_hq_followup (`+followUpColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		f.ID, f.UserID, f.WorkspaceID, string(f.Category), string(f.Direction), f.Title, f.Detail, f.Counterparty,
		f.Source.Type, f.Source.ID, f.Source.AccountID, f.DedupKey, string(f.Provenance), string(f.Confidence), string(f.Status),
		nullTime(f.DueAt), nullTime(f.SnoozedUntil), nullTime(f.LastNudgedAt), "", "", f.CreatedAt, f.UpdatedAt, nullTime(f.CompletedAt), nullTime(f.DismissedAt))
	if err != nil {
		return false, workspacecontinuity.ErrConflict
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO followup_continuity_source_refs
		(followup_id,workspace_id,operation_id,source_account_id,source_dedup_key,related_workspace_id,related_task_id,saved_at)
		VALUES (?,?,?,?,?,?,?,CURRENT_TIMESTAMP)`, f.ID, scope.WorkspaceID, scope.OperationID, account, dedup, linkedWorkspace, linkedTask)
	if err != nil {
		return false, err
	}
	return true, nil
}
