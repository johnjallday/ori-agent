package personalassistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// ContinuityAgreement deliberately does not embed State: pending hire/HQ/rename
// payloads and assignment journals must never become portable commands. Request
// IDs below are immutable provenance receipts only. No runtime settings travel.
type ContinuityAgreement struct {
	Version               int                    `json:"version"`
	SourceUserID          string                 `json:"source_user_id"`
	AssistantID           string                 `json:"assistant_id"`
	WorkspaceID           string                 `json:"workspace_id"`
	EntryInstanceID       string                 `json:"entry_instance_id"`
	ProfileName           string                 `json:"profile_name"`
	HireRequestID         string                 `json:"hire_request_id"`
	HQRequestID           string                 `json:"hq_request_id"`
	DisplayName           string                 `json:"display_name"`
	Appearance            *types.AgentAppearance `json:"appearance"`
	Mandate               string                 `json:"mandate"`
	FocusAreas            []FocusArea            `json:"focus_areas"`
	SpecialistSlug        string                 `json:"specialist_slug"`
	SpecialistOfferState  SpecialistOfferState   `json:"specialist_offer_state"`
	SourceStatus          RelationshipStatus     `json:"source_status"`
	FirstAssignmentStatus FirstAssignmentStatus  `json:"first_assignment_status"`
	SourceStateVersion    int64                  `json:"source_state_version"`
	HiredAt               time.Time              `json:"hired_at"`
	CreatedAt             time.Time              `json:"created_at"`
	UpdatedAt             time.Time              `json:"updated_at"`
}

// ContinuityBinding is constructed from the exact reviewed workspace/entry
// profile, never the name-level global roster. It is evidence, not consent.
// The coordinator must fingerprint the files it reads and revalidate them before
// publication/application. Keeping these fields private prevents wire decoding
// from being mistaken for independently validated evidence.
type ContinuityBinding struct {
	workspaceID, userID, assistantID, instanceID, profileName, hireID, hqID string
	appearance                                                              *types.AgentAppearance
}

func NewContinuityBinding(ws *session.Workspace, profile *agent.Agent) (ContinuityBinding, error) {
	var binding ContinuityBinding
	if ws == nil || profile == nil || profile.Metadata == nil || profile.Role != types.RoleOrchestrator ||
		!workspacecontinuity.ValidID(ws.ID) || ws.OwnerUserID != userprofile.LocalUserID {
		return binding, workspacecontinuity.ErrInvalid
	}
	var entries []RecoveryEntryAgent
	seen := map[string]bool{}
	for _, instance := range ws.AgentInstances {
		if !workspacecontinuity.ValidID(instance.ID) || seen[instance.ID] {
			return binding, workspacecontinuity.ErrInvalid
		}
		seen[instance.ID] = true
		if instance.EntryPoint {
			entries = append(entries, RecoveryEntryAgent{ID: instance.ID, Name: instance.Name})
		}
	}
	if len(entries) != 1 || entries[0].Name == "" {
		return binding, workspacecontinuity.ErrInvalid
	}
	provenance, marked := recoveryProfileProvenance(entries[0].Name, profile.Metadata.Tags)
	presentation, err := continuityPresentation(ws)
	if err != nil {
		return binding, err
	}
	if !marked || !workspacecontinuity.ValidID(provenance.AssistantID) ||
		!workspacecontinuity.ValidID(provenance.HireRequestID) || !workspacecontinuity.ValidID(presentation.RequestID) ||
		presentation.AssistantID != provenance.AssistantID || provenance.Name != entries[0].Name {
		return binding, workspacecontinuity.ErrInvalid
	}
	if err := validateContinuityAppearance(profile.Appearance); err != nil {
		return binding, err
	}
	binding = ContinuityBinding{workspaceID: ws.ID, userID: ws.OwnerUserID, assistantID: provenance.AssistantID,
		instanceID: entries[0].ID, profileName: entries[0].Name, hireID: provenance.HireRequestID,
		hqID: presentation.RequestID, appearance: profile.Appearance.Clone()}
	return binding, nil
}

// Modern continuity recognizes the exact presentation schema, unlike the
// deliberately separate legacy orphan inspector. Unknown required versions or
// aliased identity keys cannot be silently treated as v1 ownership evidence.
func continuityPresentation(ws *session.Workspace) (recoveryHQPresentation, error) {
	var result recoveryHQPresentation
	record, err := workspacecontinuity.EncodeRecord(ws.ID, ws.SharedData[personalhq.PersonalAssistantPresentationKey])
	if err != nil || len(record.Data) > 4096 {
		return result, workspacecontinuity.ErrInvalid
	}
	var presentation struct {
		Version                 int      `json:"version"`
		AssistantID             string   `json:"assistant_id"`
		RequestID               string   `json:"request_id"`
		SupportGroup            string   `json:"support_group,omitempty"`
		SupportAgentInstanceIDs []string `json:"support_agent_instance_ids,omitempty"`
	}
	if err := workspacecontinuity.DecodeRecord(record, &presentation); err != nil {
		return result, err
	}
	if presentation.Version != 1 {
		return result, workspacecontinuity.ErrVersion
	}
	return recoveryHQPresentation{AssistantID: presentation.AssistantID, RequestID: presentation.RequestID}, nil
}

func validateContinuityAppearance(appearance *types.AgentAppearance) error {
	if appearance == nil || !types.IsValidAppearanceMode(appearance.Mode) || appearance.Generated == nil {
		return workspacecontinuity.ErrInvalid
	}
	if appearance.Uploaded != nil && !workspacecontinuity.ValidID(appearance.Uploaded.Image) ||
		appearance.Character != nil && (!workspacecontinuity.ValidID(appearance.Character.CatalogID) || appearance.Character.CatalogVersion < 0) {
		return workspacecontinuity.ErrInvalid
	}
	canonical := appearance.Clone()
	canonical.Normalize()
	if !reflect.DeepEqual(canonical, appearance) {
		return workspacecontinuity.ErrInvalid
	}
	data, err := json.Marshal(appearance)
	if err != nil || len(data) > MaxAppearanceJSONBytes {
		return workspacecontinuity.ErrLimit
	}
	return nil
}

func (a ContinuityAgreement) validate() error {
	if a.Version != workspacecontinuity.Version {
		return workspacecontinuity.ErrVersion
	}
	if a.SourceUserID != userprofile.LocalUserID || a.SourceStateVersion < 1 || a.HiredAt.IsZero() || a.CreatedAt.IsZero() || a.UpdatedAt.IsZero() ||
		(a.SourceStatus != StatusActive && a.SourceStatus != StatusPaused) {
		return workspacecontinuity.ErrInvalid
	}
	for _, id := range []string{a.AssistantID, a.WorkspaceID, a.EntryInstanceID, a.HireRequestID, a.HQRequestID} {
		if !workspacecontinuity.ValidID(id) {
			return workspacecontinuity.ErrInvalid
		}
	}
	if err := validateContinuityAppearance(a.Appearance); err != nil {
		return err
	}
	// Use the canonical domain validator, but reject normalization rather than
	// silently changing recovered choices or fabricating defaults.
	state := a.state()
	normalized, _, _, err := normalizeState(state)
	if err != nil || normalized.ValidateStateInvariants() != nil ||
		normalized.DisplayName != a.DisplayName || a.DisplayName == "" ||
		normalized.GlobalAgentProfileName != a.ProfileName || a.ProfileName == "" || normalized.Mandate != a.Mandate ||
		normalized.SpecialistSlug != a.SpecialistSlug || normalized.SpecialistOfferState != a.SpecialistOfferState ||
		!reflect.DeepEqual(normalized.FocusAreas, a.FocusAreas) {
		return workspacecontinuity.ErrInvalid
	}
	first, err := NormalizeFirstAssignmentStatus(string(a.FirstAssignmentStatus))
	if err != nil || first != a.FirstAssignmentStatus {
		return workspacecontinuity.ErrInvalid
	}
	return nil
}

func (a ContinuityAgreement) state() *State {
	// Source completion claims alone are not canonical completion evidence.
	// The setup adapter will validate actual result references separately. No
	// preview/apply journal is ever reconstructed by restoring an agreement.
	first := FirstAssignmentNotStarted
	if a.FirstAssignmentStatus != FirstAssignmentNotStarted {
		first = FirstAssignmentFailed
	}
	return &State{UserID: a.SourceUserID, AssistantID: a.AssistantID, Status: StatusPaused,
		DisplayName: a.DisplayName, Appearance: a.Appearance.Clone(), HQWorkspaceID: a.WorkspaceID,
		HQEntryAgentInstanceID: a.EntryInstanceID, GlobalAgentProfileName: a.ProfileName,
		Mandate: a.Mandate, FocusAreas: append([]FocusArea{}, a.FocusAreas...), SpecialistSlug: a.SpecialistSlug,
		SpecialistOfferState: a.SpecialistOfferState, FirstAssignmentStatus: first,
		LastHireRequestID: a.HireRequestID, LastHQRequestID: a.HQRequestID,
		StateVersion: 1, HiredAt: &a.HiredAt, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt}
}

func (b ContinuityBinding) matches(a ContinuityAgreement) bool {
	return b.workspaceID == a.WorkspaceID && b.userID == a.SourceUserID && b.assistantID == a.AssistantID &&
		b.instanceID == a.EntryInstanceID && b.profileName == a.ProfileName && b.hireID == a.HireRequestID &&
		b.hqID == a.HQRequestID && reflect.DeepEqual(b.appearance, a.Appearance)
}

// checkWorkspace compares the captured file identity to the canonical SQL read
// view, not a name match. Profiles themselves remain folder-owned.
func (b ContinuityBinding) checkWorkspace(ctx context.Context, query workspacecontinuity.Queryer) error {
	var ws session.Workspace
	var instances, shared string
	err := query.QueryRowContext(ctx, `SELECT id,owner_user_id,agent_instances,shared_data FROM workspaces WHERE id=? AND deleted_at IS NULL`, b.workspaceID).
		Scan(&ws.ID, &ws.OwnerUserID, &instances, &shared)
	if errors.Is(err, sql.ErrNoRows) {
		return workspacecontinuity.ErrConflict
	}
	if err != nil {
		return err
	}
	if len(instances) > workspacecontinuity.MaxRecordBytes || len(shared) > workspacecontinuity.MaxRecordBytes ||
		json.Unmarshal([]byte(instances), &ws.AgentInstances) != nil || json.Unmarshal([]byte(shared), &ws.SharedData) != nil {
		return workspacecontinuity.ErrInvalid
	}
	// Construct only the bounded provenance needed for the same validation.
	profile := &agent.Agent{Role: types.RoleOrchestrator, Appearance: b.appearance,
		Metadata: &types.AgentMetadata{Tags: []string{ProfileAssistantMarker(b.assistantID), ProfileHireMarker(b.hireID)}}}
	canonical, err := NewContinuityBinding(&ws, profile)
	if err != nil || !reflect.DeepEqual(canonical, b) {
		return workspacecontinuity.ErrConflict
	}
	return nil
}

// SnapshotContinuityAgreement uses the shared read view. An unfinished identity
// operation is incomplete, not a candidate for automatic journal replay. Missing
// relationship evidence is nil, allowing independent legacy identity inspection.
func (s *SQLiteStore) SnapshotContinuityAgreement(ctx context.Context, query workspacecontinuity.Queryer, binding ContinuityBinding) (*workspacecontinuity.Record, error) {
	if query == nil || !workspacecontinuity.ValidID(binding.workspaceID) {
		return nil, workspacecontinuity.ErrInvalid
	}
	if err := binding.checkWorkspace(ctx, query); err != nil {
		return nil, err
	}
	var count int
	if err := query.QueryRowContext(ctx, `SELECT COUNT(*) FROM personal_assistant_state WHERE hq_workspace_id=?`, binding.workspaceID).Scan(&count); err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, nil
	}
	if count != 1 {
		return nil, workspacecontinuity.ErrInvalid
	}
	var a ContinuityAgreement
	a.Version = workspacecontinuity.Version
	var appearance, focus, rename, repair string
	var hired sql.NullTime
	err := query.QueryRowContext(ctx, `SELECT user_id,assistant_id,hq_workspace_id,hq_entry_agent_instance_id,
		global_agent_profile_name,last_hire_request_id,last_hq_request_id,display_name,appearance_json,mandate,
		focus_areas_json,specialist_slug,specialist_offer_state,status,first_assignment_status,state_version,hired_at,
		created_at,updated_at,rename_step,repair_step FROM personal_assistant_state WHERE hq_workspace_id=?`, binding.workspaceID).Scan(
		&a.SourceUserID, &a.AssistantID, &a.WorkspaceID, &a.EntryInstanceID, &a.ProfileName, &a.HireRequestID,
		&a.HQRequestID, &a.DisplayName, &appearance, &a.Mandate, &focus, &a.SpecialistSlug, &a.SpecialistOfferState,
		&a.SourceStatus, &a.FirstAssignmentStatus, &a.SourceStateVersion, &hired, &a.CreatedAt, &a.UpdatedAt, &rename, &repair)
	if err != nil {
		return nil, err
	}
	if rename != "" || repair != "" || (a.SourceStatus != StatusActive && a.SourceStatus != StatusPaused) {
		return nil, workspacecontinuity.ErrIncomplete
	}
	if !hired.Valid || len(appearance) > MaxAppearanceJSONBytes || len(focus) > 4096 ||
		json.Unmarshal([]byte(appearance), &a.Appearance) != nil || json.Unmarshal([]byte(focus), &a.FocusAreas) != nil {
		return nil, workspacecontinuity.ErrInvalid
	}
	a.HiredAt = hired.Time
	if err := a.validate(); err != nil {
		return nil, err
	}
	if !binding.matches(a) {
		return nil, workspacecontinuity.ErrConflict
	}
	if err := query.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE id=? AND personal_workspace_id=?`, a.SourceUserID, a.WorkspaceID).Scan(&count); err != nil {
		return nil, err
	}
	if count != 1 {
		return nil, workspacecontinuity.ErrConflict
	}
	record, err := workspacecontinuity.EncodeRecord(a.AssistantID, a)
	return &record, err
}

// CollectContinuityAgreement adds only a fully validated assistant/HQ snapshot
// from the caller's shared SQL read view. Ordinary workspaces explicitly have
// no relationship; a provenance-bearing HQ without a finished relationship is
// unavailable, never a fabricated empty/successful agreement. The coordinator
// must supply exact owned profile-file evidence, retain its reset permit, and
// recheck source files/SQL through publication before acknowledging readiness.
func (s *SQLiteStore) CollectContinuityAgreement(ctx context.Context, query workspacecontinuity.Queryer, workspaceID string, binding *ContinuityBinding, spool *workspacecontinuity.Spool) error {
	if query == nil || spool == nil || !workspacecontinuity.ValidID(workspaceID) {
		return workspacecontinuity.ErrInvalid
	}
	if binding == nil {
		// Nil evidence cannot turn a designated/presentation-bearing HQ into an
		// intentionally empty relationship. It is only safe for an ordinary
		// workspace whose canonical SQL view independently agrees.
		var count int
		if err := query.QueryRowContext(ctx, `SELECT COUNT(*) FROM personal_assistant_state WHERE hq_workspace_id=?`, workspaceID).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return workspacecontinuity.ErrIncomplete
		}
		if err := query.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE personal_workspace_id=?`, workspaceID).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return workspacecontinuity.ErrIncomplete
		}
		var shared string
		err := query.QueryRowContext(ctx, `SELECT shared_data FROM workspaces WHERE id=? AND owner_user_id='local' AND deleted_at IS NULL`, workspaceID).Scan(&shared)
		if errors.Is(err, sql.ErrNoRows) {
			return workspacecontinuity.ErrIncomplete
		}
		if err != nil {
			return err
		}
		if len(shared) > workspacecontinuity.MaxRecordBytes {
			return workspacecontinuity.ErrLimit
		}
		var values map[string]json.RawMessage
		if err := json.Unmarshal([]byte(shared), &values); err != nil || values == nil {
			return workspacecontinuity.ErrInvalid
		}
		for key := range values {
			if strings.EqualFold(key, personalhq.PersonalAssistantPresentationKey) {
				return workspacecontinuity.ErrIncomplete // do not hide an aliased HQ marker
			}
		}
		return spool.SetAvailability(ctx, "assistant", workspacecontinuity.Empty, "")
	}
	if binding.workspaceID != workspaceID {
		return workspacecontinuity.ErrInvalid
	}
	record, err := s.SnapshotContinuityAgreement(ctx, query, *binding)
	if errors.Is(err, workspacecontinuity.ErrIncomplete) {
		return spool.SetAvailability(ctx, "assistant", workspacecontinuity.Unavailable, "unfinished_agreement")
	}
	if err != nil {
		return err
	}
	if record == nil {
		// No relationship here owns this HQ. One imported without adoption
		// still carries the agreement it arrived with, kept inert at import.
		if record, err = retainedAgreement(ctx, query, *binding); err != nil {
			return err
		}
	}
	if record == nil {
		return spool.SetAvailability(ctx, "assistant", workspacecontinuity.Unavailable, "missing_agreement")
	}
	return spool.AddRecord(ctx, "assistant", "agreements", *record)
}

// retainedAgreement returns the inert incoming agreement kept with a workspace
// imported without adoption, still bound to the same entry profile, or nil.
func retainedAgreement(ctx context.Context, query workspacecontinuity.Queryer, binding ContinuityBinding) (*workspacecontinuity.Record, error) {
	values, err := workspacecontinuity.RetainedRecords(ctx, query, binding.workspaceID, "assistant")
	if err != nil || len(values) == 0 {
		return nil, err
	}
	if len(values) != 1 || values[0].Family != "agreements" {
		return nil, workspacecontinuity.ErrInvalid
	}
	if _, err := VerifyContinuityAgreement(values[0].Record, binding); err != nil {
		return nil, err
	}
	return &values[0].Record, nil
}

func DecodeContinuityAgreement(record workspacecontinuity.Record) (ContinuityAgreement, error) {
	var a ContinuityAgreement
	if err := workspacecontinuity.DecodeRecord(record, &a); err != nil {
		return a, err
	}
	if record.ID != a.AssistantID {
		return a, workspacecontinuity.ErrInvalid
	}
	return a, a.validate()
}

// VerifyContinuityAgreement binds a typed assistant record to the exact
// reviewed workspace-local entry profile. A name-only/global roster match is
// never identity evidence, and validation performs no designation or writes.
func VerifyContinuityAgreement(record workspacecontinuity.Record, binding ContinuityBinding) (ContinuityAgreement, error) {
	agreement, err := DecodeContinuityAgreement(record)
	if err != nil {
		return agreement, err
	}
	if !binding.matches(agreement) {
		return agreement, workspacecontinuity.ErrConflict
	}
	return agreement, nil
}

// RestoreContinuityAgreement is only for the explicitly adopted member of a
// confirmed operation. It neither designates HQ nor changes any global profile.
// The coordinator must perform designation in this same transaction. Pending
// receipt/admission prevents observers from treating partial adoption as ready.
// The caller MUST roll back tx on any error; a false,nil retry never rewrites
// current state or revalidates identity against subsequent local edits.
func (s *SQLiteStore) RestoreContinuityAgreement(ctx context.Context, tx *sql.Tx, scope workspacecontinuity.RestoreScope, record workspacecontinuity.Record, binding ContinuityBinding) (bool, error) {
	a, err := DecodeContinuityAgreement(record)
	if err != nil {
		return false, err
	}
	if scope.WorkspaceID != a.WorkspaceID || scope.UserID != userprofile.LocalUserID {
		return false, workspacecontinuity.ErrInvalid
	}
	claimed, err := workspacecontinuity.ClaimRecord(ctx, tx, scope, "assistant", "agreements", record.ID, workspacecontinuity.Digest(record.Data))
	if err != nil || !claimed {
		return false, err
	}
	if !binding.matches(a) {
		return false, workspacecontinuity.ErrConflict
	}
	if err := workspacecontinuity.RequireOwnedWorkspace(ctx, tx, scope); err != nil {
		return false, err
	}
	if err := binding.checkWorkspace(ctx, tx); err != nil {
		return false, err
	}
	var allowed int
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM continuity_attachments a
		JOIN continuity_operations o ON o.id=a.operation_id JOIN users u ON u.id=o.owner_user_id
		WHERE a.operation_id=? AND a.workspace_id=? AND a.disposition='adopted_hq' AND o.action='continue'
		AND u.id=? AND (u.personal_workspace_id='' OR u.personal_workspace_id=a.workspace_id)`, scope.OperationID, scope.WorkspaceID, scope.UserID).Scan(&allowed)
	if err != nil {
		return false, err
	}
	if allowed != 1 {
		return false, workspacecontinuity.ErrConflict
	}
	state, appearanceJSON, focusJSON, err := normalizeState(a.state())
	if err != nil {
		return false, workspacecontinuity.ErrInvalid
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO personal_assistant_state
		(user_id,assistant_id,status,display_name,appearance_json,hq_workspace_id,hq_entry_agent_instance_id,
		global_agent_profile_name,mandate,focus_areas_json,specialist_slug,specialist_offer_state,first_assignment_status,
		last_hire_request_id,last_hq_request_id,state_version,hired_at,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,1,?,?,?)`, scope.UserID, state.AssistantID, StatusPaused,
		state.DisplayName, appearanceJSON, state.HQWorkspaceID, state.HQEntryAgentInstanceID, state.GlobalAgentProfileName,
		state.Mandate, focusJSON, state.SpecialistSlug, state.SpecialistOfferState, state.FirstAssignmentStatus,
		state.LastHireRequestID, state.LastHQRequestID, state.HiredAt, state.CreatedAt, state.UpdatedAt)
	if isConstraintError(err) {
		return false, workspacecontinuity.ErrConflict
	}
	return err == nil, err
}
