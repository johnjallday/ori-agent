package personalassistant

import (
	"context"
	"database/sql"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// AdoptLegacyHQ makes the assistant proven by an older (pre-checkpoint)
// Personal HQ folder this installation's assistant, as part of the user's
// confirmed Import Folder (PRD FR-28/FR-29). binding is built from that
// folder's own evidence only — its presentation marker, its single entry
// instance and that instance's workspace profile — and is rechecked here
// against the imported SQL row. Nothing else of the relationship travelled
// with such a folder, so mandate and focus stay empty (unknown until the user
// chooses) and the relationship starts paused; a missing Daily Brief
// configuration is simply not configured.
//
// One transaction also designates the HQ and marks the workspace as an
// adopted import, so its profile is read from the folder and its routines stay
// off until turned on here. It refuses when this installation already has an
// assistant or a different HQ. Ordinary orphan recovery is unchanged: this is
// only reachable through an explicit import.
func (s *SQLiteStore) AdoptLegacyHQ(ctx context.Context, binding ContinuityBinding, hiredAt time.Time) (*State, error) {
	if s == nil || s.db == nil || !workspacecontinuity.ValidID(binding.workspaceID) {
		return nil, workspacecontinuity.ErrInvalid
	}
	now := time.Now().UTC()
	if hiredAt.IsZero() || hiredAt.After(now) {
		hiredAt = now
	}
	hiredAt = hiredAt.UTC()
	state := &State{UserID: binding.userID, AssistantID: binding.assistantID, Status: StatusPaused,
		DisplayName: binding.profileName, Appearance: binding.appearance.Clone(), HQWorkspaceID: binding.workspaceID,
		HQEntryAgentInstanceID: binding.instanceID, GlobalAgentProfileName: binding.profileName,
		FirstAssignmentStatus: FirstAssignmentNotStarted, LastHireRequestID: binding.hireID, LastHQRequestID: binding.hqID,
		StateVersion: 1, HiredAt: &hiredAt, CreatedAt: now, UpdatedAt: now}
	normalized, appearanceJSON, focusJSON, err := normalizeState(state)
	if err != nil || normalized.ValidateStateInvariants() != nil {
		return nil, workspacecontinuity.ErrInvalid
	}
	err = s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		var existing int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM personal_assistant_state WHERE user_id=?`, binding.userID).Scan(&existing); err != nil {
			return err
		}
		if existing != 0 {
			return workspacecontinuity.ErrConflict
		}
		if err := binding.checkWorkspace(ctx, tx); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE users SET personal_workspace_id=? WHERE id=? AND COALESCE(personal_workspace_id,'') IN ('',?)`,
			binding.workspaceID, binding.userID, binding.workspaceID)
		if err != nil {
			return err
		}
		if rows, err := result.RowsAffected(); err != nil || rows != 1 {
			return workspacecontinuity.ErrConflict
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO personal_assistant_state
			(user_id,assistant_id,status,display_name,appearance_json,hq_workspace_id,hq_entry_agent_instance_id,
			global_agent_profile_name,mandate,focus_areas_json,specialist_slug,specialist_offer_state,first_assignment_status,
			last_hire_request_id,last_hq_request_id,state_version,hired_at,created_at,updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,1,?,?,?)`, normalized.UserID, normalized.AssistantID, StatusPaused,
			normalized.DisplayName, appearanceJSON, normalized.HQWorkspaceID, normalized.HQEntryAgentInstanceID,
			normalized.GlobalAgentProfileName, normalized.Mandate, focusJSON, normalized.SpecialistSlug,
			normalized.SpecialistOfferState, normalized.FirstAssignmentStatus, normalized.LastHireRequestID,
			normalized.LastHQRequestID, normalized.HiredAt, normalized.CreatedAt, normalized.UpdatedAt)
		if isConstraintError(err) {
			return workspacecontinuity.ErrConflict
		}
		if err != nil {
			return err
		}
		return workspacecontinuity.MarkLegacyAdoption(ctx, tx, binding.workspaceID)
	})
	if err != nil {
		return nil, err
	}
	return s.GetState(ctx, binding.userID)
}
