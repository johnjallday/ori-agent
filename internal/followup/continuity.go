package followup

import (
	"bytes"
	"context"
	"database/sql"
	"errors"

	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// ContinuityFollowUp carries historical source identity but no approval. Its
// AccountID and linked task are inert external references until the destination
// coordinator verifies local ownership. Decoding a record never connects a
// source, captures a follow-up, sends a notification, or creates work.
type ContinuityFollowUp struct {
	Version             int      `json:"version"`
	FollowUp            FollowUp `json:"follow_up"`
	SourceNeedsReview   bool     `json:"source_needs_review"`
	TaskLinkNeedsReview bool     `json:"task_link_needs_review"`
}

func SnapshotContinuityFollowUp(f *FollowUp, workspaceID string) (workspacecontinuity.Record, error) {
	if f == nil || !workspacecontinuity.ValidID(workspaceID) || !workspacecontinuity.ValidID(f.ID) || f.UserID != "local" || f.WorkspaceID != workspaceID || !ValidCategory(f.Category) || !validContinuityStatus(f.Status) || f.CreatedAt.IsZero() || f.UpdatedAt.IsZero() {
		return workspacecontinuity.Record{}, workspacecontinuity.ErrInvalid
	}
	if f.Direction != DirectionOutbound && f.Direction != DirectionInbound && f.Direction != DirectionNone ||
		f.Provenance != ProvenanceExplicit && f.Provenance != ProvenanceInferred && f.Provenance != ProvenanceManual ||
		len([]rune(f.Title)) > MaxTitleLen || len([]rune(f.Detail)) > MaxDetailLen || f.Title == "" ||
		len(f.Source.ID) > 4096 || len(f.Source.AccountID) > 4096 || len(f.DedupKey) > 4096 ||
		f.RelatedTask != nil && (f.RelatedTask.WorkspaceID == "" || f.RelatedTask.TaskID == "" || len(f.RelatedTask.WorkspaceID) > 4096 || len(f.RelatedTask.TaskID) > 4096) {
		return workspacecontinuity.Record{}, workspacecontinuity.ErrInvalid
	}
	value := ContinuityFollowUp{Version: 1, FollowUp: *f, SourceNeedsReview: f.DedupKey != "" || f.Source.AccountID != "" || f.Source.Type != "" && f.Source.Type != "manual", TaskLinkNeedsReview: f.RelatedTask != nil}
	return workspacecontinuity.EncodeRecord(f.ID, value)
}

func DecodeContinuityFollowUp(record workspacecontinuity.Record, workspaceID string) (ContinuityFollowUp, error) {
	var value ContinuityFollowUp
	if err := workspacecontinuity.DecodeRecord(record, &value); err != nil {
		return value, err
	}
	if value.Version != 1 {
		return value, workspacecontinuity.ErrVersion
	}
	ref, err := SnapshotContinuityFollowUp(&value.FollowUp, workspaceID)
	if err != nil {
		return value, err
	}
	if ref.ID != record.ID || !bytes.Equal(ref.Data, record.Data) {
		return value, workspacecontinuity.ErrInvalid
	}
	return value, nil
}

func validContinuityStatus(status Status) bool {
	switch status {
	case StatusCandidate, StatusActive, StatusSnoozed, StatusReopened, StatusCompleted, StatusDismissed:
		return true
	}
	return false
}

// CollectContinuityFollowUps pages the canonical owner from the caller's
// shared SQL read view. No HQ projection or same-name agent participates:
// follow-ups from Email Ops do not become owned by HQ just because Today showed
// them. A collector error poisons the operation; callers MUST discard the
// provisional Spool and must not publish a partial component.
func CollectContinuityFollowUps(ctx context.Context, query workspacecontinuity.Queryer, workspaceID string, spool *workspacecontinuity.Spool) error {
	if query == nil || spool == nil || !workspacecontinuity.ValidID(workspaceID) {
		return workspacecontinuity.ErrInvalid
	}
	const pageSize = 256
	count, after := 0, ""
	for {
		rows, err := query.QueryContext(ctx, `SELECT `+followUpColumns+` FROM personal_hq_followup WHERE workspace_id=? AND id>? ORDER BY id LIMIT ?`, workspaceID, after, pageSize)
		if err != nil {
			return err
		}
		page := 0
		for rows.Next() {
			if err := ctx.Err(); err != nil {
				_ = rows.Close()
				return err
			}
			if count >= workspacecontinuity.MaxReferences*workspacecontinuity.MaxRecords {
				_ = rows.Close()
				return workspacecontinuity.ErrLimit
			}
			f, err := scanFollowUp(rows)
			if err != nil {
				_ = rows.Close()
				return err
			}
			if f.UserID != "local" {
				_ = rows.Close()
				return workspacecontinuity.ErrInvalid
			}
			// A prior import keeps historical foreign IDs outside its
			// operational row; re-export without granting their permission.
			var historicalAccount, historicalDedup, relatedWorkspace, relatedTask string
			err = query.QueryRowContext(ctx, `SELECT source_account_id,source_dedup_key,related_workspace_id,related_task_id FROM followup_continuity_source_refs WHERE followup_id=? AND workspace_id=?`, f.ID, workspaceID).Scan(&historicalAccount, &historicalDedup, &relatedWorkspace, &relatedTask)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				_ = rows.Close()
				return err
			}
			if err == nil {
				f.Source.AccountID, f.DedupKey = historicalAccount, historicalDedup
				if relatedWorkspace != "" || relatedTask != "" {
					f.RelatedTask = &TaskRef{WorkspaceID: relatedWorkspace, TaskID: relatedTask}
				}
			}
			record, err := SnapshotContinuityFollowUp(f, workspaceID)
			if err != nil {
				_ = rows.Close()
				return err
			}
			if err := spool.AddRecord(ctx, "followups", "followups", record); err != nil {
				_ = rows.Close()
				return err
			}
			after = f.ID
			count++
			page++
		}
		readErr, closeErr := rows.Err(), rows.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			return err
		}
		if page < pageSize {
			break
		}
	}
	if count == 0 {
		return spool.SetAvailability(ctx, "followups", workspacecontinuity.Empty, "")
	}
	return nil
}

// A receipt-owned restore must resolve the source reference into an inert local
// projection, isolate unverified task links, and insert in
// the caller's transaction. Ordinary Create is not an import API.
