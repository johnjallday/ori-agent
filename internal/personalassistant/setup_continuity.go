package personalassistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// ContinuityAssignment is completion evidence for a first assignment: which
// records it created and when. The preview payload and apply journal are an
// executable plan and never travel; nothing here can be applied again.
type ContinuityAssignment struct {
	Version         int            `json:"version"`
	WorkspaceID     string         `json:"workspace_id"`
	AssistantID     string         `json:"assistant_id"`
	PreviewID       string         `json:"preview_id"`
	CanonicalRefs   []CanonicalRef `json:"canonical_refs"`
	BriefRevisionID string         `json:"brief_revision_id"`
	BriefStatus     string         `json:"brief_status"`
	CreatedAt       time.Time      `json:"created_at"`
	CompletedAt     time.Time      `json:"completed_at"`
}

func (a ContinuityAssignment) validate(owner string) error {
	if a.Version != 1 || a.WorkspaceID != owner || !workspacecontinuity.ValidID(owner) || !workspacecontinuity.ValidID(a.AssistantID) ||
		!workspacecontinuity.ValidID(a.PreviewID) || a.CanonicalRefs == nil || len(a.CanonicalRefs) > 256 ||
		a.CreatedAt.IsZero() || a.CompletedAt.IsZero() || a.BriefRevisionID != "" && !workspacecontinuity.ValidID(a.BriefRevisionID) || len(a.BriefStatus) > 32 {
		return workspacecontinuity.ErrInvalid
	}
	for _, ref := range a.CanonicalRefs {
		if ref.Kind != string(AssignmentRecordTicket) && ref.Kind != string(AssignmentRecordFollowUp) ||
			!workspacecontinuity.ValidID(ref.ID) || !workspacecontinuity.ValidID(ref.WorkspaceID) {
			return workspacecontinuity.ErrInvalid
		}
	}
	return nil
}

// CollectContinuitySetup reads the completed first-assignment evidence of the
// relationship whose HQ is this workspace. Any other workspace has none.
func CollectContinuitySetup(ctx context.Context, query workspacecontinuity.Queryer, workspaceID string, spool *workspacecontinuity.Spool) error {
	if query == nil || spool == nil || !workspacecontinuity.ValidID(workspaceID) {
		return workspacecontinuity.ErrInvalid
	}
	rows, err := query.QueryContext(ctx, `SELECT a.preview_id,a.assistant_id,a.created_canonical_refs_json,a.brief_revision_id,a.brief_status,a.created_at,a.updated_at
		FROM personal_assistant_assignment a JOIN personal_assistant_state s ON s.user_id=a.user_id AND s.assistant_id=a.assistant_id
		WHERE s.hq_workspace_id=? AND a.status=? ORDER BY a.preview_id LIMIT 257`, workspaceID, AssignmentCompleted)
	if err != nil {
		return err
	}
	var values []ContinuityAssignment
	for rows.Next() {
		if len(values) == 256 {
			_ = rows.Close()
			return workspacecontinuity.ErrLimit
		}
		var value ContinuityAssignment
		var refs string
		if err := rows.Scan(&value.PreviewID, &value.AssistantID, &refs, &value.BriefRevisionID, &value.BriefStatus, &value.CreatedAt, &value.CompletedAt); err != nil {
			_ = rows.Close()
			return err
		}
		value.Version, value.WorkspaceID, value.CanonicalRefs = 1, workspaceID, []CanonicalRef{}
		if len(refs) > workspacecontinuity.MaxRecordBytes || json.Unmarshal([]byte(refs), &value.CanonicalRefs) != nil || value.CanonicalRefs == nil {
			_ = rows.Close()
			return workspacecontinuity.ErrInvalid
		}
		values = append(values, value)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	if len(values) == 0 {
		return collectRetainedSetup(ctx, query, workspaceID, spool)
	}
	for _, value := range values {
		if err := value.validate(workspaceID); err != nil {
			return err
		}
		record, err := workspacecontinuity.EncodeRecord(value.PreviewID, value)
		if err != nil {
			return err
		}
		if err := spool.AddRecord(ctx, "setup", "assignments", record); err != nil {
			return err
		}
	}
	return nil
}

// collectRetainedSetup re-exports the setup results a workspace arrived with
// when it was imported without adopting its assistant. A relationship here that
// owns the HQ always wins: then the retained copy is not this workspace's truth.
func collectRetainedSetup(ctx context.Context, query workspacecontinuity.Queryer, workspaceID string, spool *workspacecontinuity.Spool) error {
	var owned int
	if err := query.QueryRowContext(ctx, `SELECT COUNT(*) FROM personal_assistant_state WHERE hq_workspace_id=?`, workspaceID).Scan(&owned); err != nil {
		return err
	}
	var retained []workspacecontinuity.RetainedRecord
	if owned == 0 {
		var err error
		if retained, err = workspacecontinuity.RetainedRecords(ctx, query, workspaceID, "setup"); err != nil {
			return err
		}
	}
	if len(retained) == 0 {
		return spool.SetAvailability(ctx, "setup", workspacecontinuity.Empty, "")
	}
	for _, value := range retained {
		if _, err := DecodeContinuityAssignment(value.Record, workspaceID); err != nil || value.Family != "assignments" {
			return errors.Join(workspacecontinuity.ErrInvalid, err)
		}
		if err := spool.AddRecord(ctx, "setup", value.Family, value.Record); err != nil {
			return err
		}
	}
	return nil
}

func DecodeContinuityAssignment(record workspacecontinuity.Record, owner string) (ContinuityAssignment, error) {
	var value ContinuityAssignment
	if err := workspacecontinuity.DecodeRecord(record, &value); err != nil {
		return value, err
	}
	if record.ID != value.PreviewID {
		return value, workspacecontinuity.ErrInvalid
	}
	return value, value.validate(owner)
}

// RestoreContinuityAssignment records a first assignment as completed only
// when every record it created was itself restored into this import's
// workspaces. The adopted relationship then shows first assignment complete
// without replaying the apply, recreating tasks or paying a reward again.
// Unverifiable evidence is left out and reported: false, nil with verified
// false. The caller must roll back tx on any error.
func (s *SQLiteStore) RestoreContinuityAssignment(ctx context.Context, tx *sql.Tx, scope workspacecontinuity.RestoreScope, record workspacecontinuity.Record) (inserted, verified bool, err error) {
	if s == nil || tx == nil || scope.UserID != userprofile.LocalUserID {
		return false, false, workspacecontinuity.ErrInvalid
	}
	value, err := DecodeContinuityAssignment(record, scope.WorkspaceID)
	if err != nil {
		return false, false, err
	}
	members, err := workspacecontinuity.ImportWorkspaceIDs(ctx, tx, scope)
	if err != nil {
		return false, false, err
	}
	allowed := make(map[string]bool, len(members))
	for _, id := range members {
		allowed[id] = true
	}
	for _, ref := range value.CanonicalRefs {
		if !allowed[ref.WorkspaceID] {
			return false, false, nil
		}
		var count int
		switch ref.Kind {
		case string(AssignmentRecordFollowUp):
			err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM personal_hq_followup WHERE id=? AND workspace_id=?`, ref.ID, ref.WorkspaceID).Scan(&count)
		default:
			err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspaces w, json_each(w.tasks_json) t
				WHERE w.id=? AND json_extract(t.value,'$.id')=?`, ref.WorkspaceID, ref.ID).Scan(&count)
		}
		if err != nil {
			return false, false, err
		}
		if count == 0 {
			return false, false, nil
		}
	}
	claimed, err := workspacecontinuity.ClaimRecord(ctx, tx, scope, "setup", "assignments", record.ID, workspacecontinuity.Digest(record.Data))
	if err != nil || !claimed {
		return false, err == nil, err
	}
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM personal_assistant_state WHERE user_id=? AND assistant_id=? AND hq_workspace_id=?`,
		scope.UserID, value.AssistantID, scope.WorkspaceID).Scan(&exists); err != nil {
		return false, false, err
	}
	if exists != 1 {
		return false, false, workspacecontinuity.ErrConflict
	}
	refs, err := json.Marshal(value.CanonicalRefs)
	if err != nil {
		return false, false, workspacecontinuity.ErrInvalid
	}
	const payload = `{}`
	if _, err := tx.ExecContext(ctx, `INSERT INTO personal_assistant_assignment
		(preview_id,user_id,assistant_id,assignment_version,normalized_payload_json,normalized_payload_hash,status,
		created_canonical_refs_json,brief_revision_id,brief_status,created_at,updated_at)
		VALUES (?,?,?,1,?,?,?,?,?,?,?,?)`, value.PreviewID, scope.UserID, value.AssistantID, payload, PayloadHash([]byte(payload)),
		AssignmentCompleted, string(refs), value.BriefRevisionID, value.BriefStatus, value.CreatedAt, value.CompletedAt); err != nil {
		return false, false, workspacecontinuity.ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `UPDATE personal_assistant_state SET first_assignment_status=? WHERE user_id=? AND assistant_id=?`,
		FirstAssignmentCompleted, scope.UserID, value.AssistantID); err != nil {
		return false, false, err
	}
	return true, true, nil
}
