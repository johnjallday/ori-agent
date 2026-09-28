package dailybrief

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// ContinuityRevision is historical prose, not a generation claim. Source
// trigger/status/config references must not authorize generation on import.
type ContinuityRevision struct {
	Version  int      `json:"version"`
	Revision Revision `json:"revision"`
}

func SnapshotContinuityRevision(rev *Revision, workspaceID string) (workspacecontinuity.Record, error) {
	if rev == nil || !workspacecontinuity.ValidID(workspaceID) || !workspacecontinuity.ValidID(rev.ID) || rev.WorkspaceID != workspaceID || rev.UserID != "local" || rev.CreatedAt.IsZero() || rev.RevisionNumber <= 0 || rev.RevisionNumber >= math.MaxInt32 || rev.ConfigRevision < 0 || rev.ConfigRevision >= math.MaxInt32 {
		return workspacecontinuity.Record{}, workspacecontinuity.ErrInvalid
	}
	date, err := time.Parse("2006-01-02", rev.LocalDate)
	if err != nil || date.Format("2006-01-02") != rev.LocalDate {
		return workspacecontinuity.Record{}, workspacecontinuity.ErrInvalid
	}
	switch rev.Trigger {
	case TriggerFirstOpen, TriggerScheduled, TriggerManual:
	default:
		return workspacecontinuity.Record{}, workspacecontinuity.ErrInvalid
	}
	switch rev.Status {
	case GenerationPending, GenerationRunning, GenerationSucceeded, GenerationPartial, GenerationFailed:
	default:
		return workspacecontinuity.Record{}, workspacecontinuity.ErrInvalid
	}
	if len(rev.ContentJSON) > workspacecontinuity.MaxRecordBytes || rev.ContentJSON != "" && !json.Valid([]byte(rev.ContentJSON)) {
		return workspacecontinuity.Record{}, workspacecontinuity.ErrInvalid
	}
	return workspacecontinuity.EncodeRecord(rev.ID, ContinuityRevision{Version: workspacecontinuity.Version, Revision: *rev})
}

func DecodeContinuityRevision(record workspacecontinuity.Record, workspaceID string) (ContinuityRevision, error) {
	var value ContinuityRevision
	if err := workspacecontinuity.DecodeRecord(record, &value); err != nil {
		return value, err
	}
	if value.Version != workspacecontinuity.Version {
		return value, workspacecontinuity.ErrVersion
	}
	canonical, err := SnapshotContinuityRevision(&value.Revision, workspaceID)
	if err != nil {
		return value, err
	}
	if canonical.ID != record.ID || !bytes.Equal(canonical.Data, record.Data) {
		return value, workspacecontinuity.ErrInvalid
	}
	return value, nil
}

// CollectContinuityRevisions uses a single caller-owned read view. Unlike
// ListHistory, it includes every retained same-date revision and exact current
// selection. Page failure invalidates the provisional Spool; nil rows mark an
// explicitly Empty component, not an unspecified missing one.
func CollectContinuityRevisions(ctx context.Context, query workspacecontinuity.Queryer, workspaceID string, spool *workspacecontinuity.Spool) error {
	if query == nil || spool == nil || !workspacecontinuity.ValidID(workspaceID) {
		return workspacecontinuity.ErrInvalid
	}
	const pageSize = 256
	count, after := 0, ""
	for {
		rows, err := query.QueryContext(ctx, `SELECT id,workspace_id,user_id,local_date,revision_number,is_current,trigger_type,status,
			config_revision,content_json,source_window_start,source_window_end,failure_reason,generated_at,created_at
			FROM daily_brief_revision WHERE workspace_id=? AND id>? ORDER BY id LIMIT ?`, workspaceID, after, pageSize)
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
			var r Revision
			var current int
			var trigger, status string
			var start, end, generated sql.NullTime
			if err := rows.Scan(&r.ID, &r.WorkspaceID, &r.UserID, &r.LocalDate, &r.RevisionNumber, &current, &trigger, &status,
				&r.ConfigRevision, &r.ContentJSON, &start, &end, &r.FailureReason, &generated, &r.CreatedAt); err != nil {
				_ = rows.Close()
				return err
			}
			if current != 0 && current != 1 {
				_ = rows.Close()
				return workspacecontinuity.ErrInvalid
			}
			r.IsCurrent, r.Trigger, r.Status = current == 1, Trigger(trigger), GenerationStatus(status)
			if start.Valid {
				r.SourceWindowStart = start.Time
			}
			if end.Valid {
				r.SourceWindowEnd = end.Time
			}
			if generated.Valid {
				r.GeneratedAt = generated.Time
			}
			record, err := SnapshotContinuityRevision(&r, workspaceID)
			if err != nil {
				_ = rows.Close()
				return err
			}
			if err := spool.AddRecord(ctx, "brief_history", "revisions", record); err != nil {
				_ = rows.Close()
				return err
			}
			after = r.ID
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
		return spool.SetAvailability(ctx, "brief_history", workspacecontinuity.Empty, "")
	}
	return nil
}
