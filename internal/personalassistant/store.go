package personalassistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/types"
)

// Store is the relationship and first-assignment persistence contract.
type Store interface {
	CreateState(ctx context.Context, state *State) (*State, error)
	GetState(ctx context.Context, userID string) (*State, error)
	UpdateState(ctx context.Context, state *State, expectedVersion int64) (*State, error)
	CreateAssignment(ctx context.Context, assignment *Assignment) (*Assignment, error)
	GetAssignment(ctx context.Context, userID, previewID string) (*Assignment, error)
	UpdateAssignment(ctx context.Context, assignment *Assignment, expectedVersion int64) (*Assignment, error)
}

// SQLiteStore persists personal-assistant records in the shared application DB.
type SQLiteStore struct {
	db  *database.DB
	now func() time.Time
}

// NewSQLiteStore constructs a personal-assistant store.
func NewSQLiteStore(db *database.DB) *SQLiteStore {
	return &SQLiteStore{db: db, now: time.Now}
}

var _ Store = (*SQLiteStore)(nil)

// CreateState inserts one fresh user-owned relationship.
func (s *SQLiteStore) CreateState(ctx context.Context, state *State) (*State, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("personal assistant: store is not configured")
	}
	normalized, appearanceJSON, focusJSON, err := normalizeState(state)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	normalized.StateVersion = 1
	normalized.CreatedAt = now
	normalized.UpdatedAt = now
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO personal_assistant_state (
			user_id, assistant_id, status, display_name, appearance_json,
			hq_workspace_id, hq_entry_agent_instance_id, global_agent_profile_name,
			mandate, focus_areas_json, first_assignment_status,
			last_hire_request_id, hire_payload_hash, hire_payload_json, repair_step,
			state_version, hired_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?)
	`, normalized.UserID, normalized.AssistantID, normalized.Status, normalized.DisplayName,
		appearanceJSON, normalized.HQWorkspaceID, normalized.HQEntryAgentInstanceID,
		normalized.GlobalAgentProfileName, normalized.Mandate, focusJSON,
		normalized.FirstAssignmentStatus, normalized.LastHireRequestID,
		normalized.HirePayloadHash, normalized.HirePayloadJSON, normalized.RepairStep,
		normalized.HiredAt, normalized.CreatedAt, normalized.UpdatedAt)
	if err != nil {
		if isConstraintError(err) {
			return nil, fmt.Errorf("%w: relationship already exists", ErrConflict)
		}
		return nil, fmt.Errorf("personal assistant: create state: %w", err)
	}
	return normalized.Clone(), nil
}

// GetState returns the relationship owned by userID.
func (s *SQLiteStore) GetState(ctx context.Context, userID string) (*State, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("personal assistant: store is not configured")
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, fmt.Errorf("personal assistant: user id is required")
	}
	return s.scanState(ctx, `
		SELECT user_id, assistant_id, status, display_name, appearance_json,
			hq_workspace_id, hq_entry_agent_instance_id, global_agent_profile_name,
			mandate, focus_areas_json, first_assignment_status,
			last_hire_request_id, hire_payload_hash, hire_payload_json, repair_step,
			state_version, hired_at, created_at, updated_at
		FROM personal_assistant_state WHERE user_id = ?
	`, userID)
}

// UpdateState compare-and-swaps a relationship by state_version.
func (s *SQLiteStore) UpdateState(ctx context.Context, state *State, expectedVersion int64) (*State, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("personal assistant: store is not configured")
	}
	if expectedVersion <= 0 {
		return nil, fmt.Errorf("personal assistant: expected state version must be positive")
	}
	normalized, appearanceJSON, focusJSON, err := normalizeState(state)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	result, err := s.db.ExecContext(ctx, `
		UPDATE personal_assistant_state SET
			status = ?, display_name = ?, appearance_json = ?, hq_workspace_id = ?,
			hq_entry_agent_instance_id = ?, global_agent_profile_name = ?, mandate = ?,
			focus_areas_json = ?, first_assignment_status = ?, last_hire_request_id = ?,
			hire_payload_hash = ?, hire_payload_json = ?, repair_step = ?,
			state_version = state_version + 1, hired_at = ?, updated_at = ?
		WHERE user_id = ? AND assistant_id = ? AND state_version = ?
	`, normalized.Status, normalized.DisplayName, appearanceJSON, normalized.HQWorkspaceID,
		normalized.HQEntryAgentInstanceID, normalized.GlobalAgentProfileName,
		normalized.Mandate, focusJSON, normalized.FirstAssignmentStatus,
		normalized.LastHireRequestID, normalized.HirePayloadHash, normalized.HirePayloadJSON, normalized.RepairStep,
		normalized.HiredAt, now, normalized.UserID, normalized.AssistantID, expectedVersion)
	if err != nil {
		return nil, fmt.Errorf("personal assistant: update state: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("personal assistant: inspect state update: %w", err)
	}
	if rows == 0 {
		if _, getErr := s.GetState(ctx, normalized.UserID); errors.Is(getErr, ErrNotFound) {
			return nil, getErr
		}
		return nil, fmt.Errorf("%w: expected state version %d", ErrConflict, expectedVersion)
	}
	return s.GetState(ctx, normalized.UserID)
}

func (s *SQLiteStore) scanState(ctx context.Context, query string, args ...any) (*State, error) {
	var state State
	var status, firstStatus, appearanceJSON, focusJSON string
	var hiredAt sql.NullTime
	err := s.db.QueryRowContext(ctx, query, args...).Scan(
		&state.UserID, &state.AssistantID, &status, &state.DisplayName, &appearanceJSON,
		&state.HQWorkspaceID, &state.HQEntryAgentInstanceID, &state.GlobalAgentProfileName,
		&state.Mandate, &focusJSON, &firstStatus, &state.LastHireRequestID,
		&state.HirePayloadHash, &state.HirePayloadJSON, &state.RepairStep,
		&state.StateVersion, &hiredAt, &state.CreatedAt, &state.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("personal assistant: get state: %w", err)
	}
	state.Status, err = NormalizeRelationshipStatus(status)
	if err != nil {
		return nil, fmt.Errorf("personal assistant: malformed persisted state: %w", err)
	}
	state.FirstAssignmentStatus, err = NormalizeFirstAssignmentStatus(firstStatus)
	if err != nil {
		return nil, fmt.Errorf("personal assistant: malformed persisted state: %w", err)
	}
	var appearance types.AgentAppearance
	if err := json.Unmarshal([]byte(appearanceJSON), &appearance); err != nil {
		return nil, fmt.Errorf("personal assistant: malformed persisted appearance: %w", err)
	}
	appearance.Normalize()
	state.Appearance = &appearance
	var rawFocus []string
	if err := json.Unmarshal([]byte(focusJSON), &rawFocus); err != nil {
		return nil, fmt.Errorf("personal assistant: malformed persisted focus areas: %w", err)
	}
	state.FocusAreas, err = NormalizeFocusAreas(rawFocus)
	if err != nil {
		return nil, fmt.Errorf("personal assistant: malformed persisted focus areas: %w", err)
	}
	if hiredAt.Valid {
		t := hiredAt.Time
		state.HiredAt = &t
	}
	if _, _, _, err := normalizeState(&state); err != nil {
		return nil, fmt.Errorf("personal assistant: malformed persisted state: %w", err)
	}
	return state.Clone(), nil
}

// CreateAssignment inserts one immutable preview identity and initial payload.
func (s *SQLiteStore) CreateAssignment(ctx context.Context, assignment *Assignment) (*Assignment, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("personal assistant: store is not configured")
	}
	normalized, payloadJSON, refsJSON, err := normalizeAssignment(assignment)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	normalized.AssignmentVersion = 1
	normalized.CreatedAt = now
	normalized.UpdatedAt = now
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO personal_assistant_assignment (
			preview_id, user_id, assistant_id, assignment_version,
			normalized_payload_json, normalized_payload_hash, status,
			created_canonical_refs_json, created_at, updated_at
		) VALUES (?, ?, ?, 1, ?, ?, ?, ?, ?, ?)
	`, normalized.PreviewID, normalized.UserID, normalized.AssistantID, payloadJSON,
		normalized.NormalizedPayloadHash, normalized.Status, refsJSON,
		normalized.CreatedAt, normalized.UpdatedAt)
	if err != nil {
		if isConstraintError(err) {
			return nil, fmt.Errorf("%w: assignment preview already exists or has a foreign owner", ErrConflict)
		}
		return nil, fmt.Errorf("personal assistant: create assignment: %w", err)
	}
	return normalized.Clone(), nil
}

// GetAssignment returns previewID only when it belongs to userID.
func (s *SQLiteStore) GetAssignment(ctx context.Context, userID, previewID string) (*Assignment, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("personal assistant: store is not configured")
	}
	userID = strings.TrimSpace(userID)
	previewID = strings.TrimSpace(previewID)
	if userID == "" || previewID == "" {
		return nil, ErrNotFound
	}
	return s.scanAssignment(ctx, `
		SELECT preview_id, user_id, assistant_id, assignment_version,
			normalized_payload_json, normalized_payload_hash, status,
			created_canonical_refs_json, created_at, updated_at
		FROM personal_assistant_assignment
		WHERE user_id = ? AND preview_id = ?
	`, userID, previewID)
}

// UpdateAssignment compare-and-swaps one preview/apply journal row.
func (s *SQLiteStore) UpdateAssignment(ctx context.Context, assignment *Assignment, expectedVersion int64) (*Assignment, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("personal assistant: store is not configured")
	}
	if expectedVersion <= 0 {
		return nil, fmt.Errorf("personal assistant: expected assignment version must be positive")
	}
	normalized, payloadJSON, refsJSON, err := normalizeAssignment(assignment)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	result, err := s.db.ExecContext(ctx, `
		UPDATE personal_assistant_assignment SET
			normalized_payload_json = ?, normalized_payload_hash = ?, status = ?,
			created_canonical_refs_json = ?, assignment_version = assignment_version + 1,
			updated_at = ?
		WHERE user_id = ? AND assistant_id = ? AND preview_id = ? AND assignment_version = ?
	`, payloadJSON, normalized.NormalizedPayloadHash, normalized.Status, refsJSON,
		now, normalized.UserID, normalized.AssistantID, normalized.PreviewID, expectedVersion)
	if err != nil {
		return nil, fmt.Errorf("personal assistant: update assignment: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("personal assistant: inspect assignment update: %w", err)
	}
	if rows == 0 {
		if _, getErr := s.GetAssignment(ctx, normalized.UserID, normalized.PreviewID); errors.Is(getErr, ErrNotFound) {
			return nil, getErr
		}
		return nil, fmt.Errorf("%w: expected assignment version %d", ErrConflict, expectedVersion)
	}
	return s.GetAssignment(ctx, normalized.UserID, normalized.PreviewID)
}

func (s *SQLiteStore) scanAssignment(ctx context.Context, query string, args ...any) (*Assignment, error) {
	var assignment Assignment
	var payloadJSON, refsJSON, status string
	err := s.db.QueryRowContext(ctx, query, args...).Scan(
		&assignment.PreviewID, &assignment.UserID, &assignment.AssistantID,
		&assignment.AssignmentVersion, &payloadJSON, &assignment.NormalizedPayloadHash,
		&status, &refsJSON, &assignment.CreatedAt, &assignment.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("personal assistant: get assignment: %w", err)
	}
	assignment.Status, err = NormalizeAssignmentStatus(status)
	if err != nil {
		return nil, fmt.Errorf("personal assistant: malformed persisted assignment: %w", err)
	}
	assignment.NormalizedPayload = append(json.RawMessage(nil), []byte(payloadJSON)...)
	if !json.Valid(assignment.NormalizedPayload) {
		return nil, fmt.Errorf("personal assistant: malformed persisted assignment payload")
	}
	if err := json.Unmarshal([]byte(refsJSON), &assignment.CreatedCanonicalRefs); err != nil {
		return nil, fmt.Errorf("personal assistant: malformed persisted canonical refs: %w", err)
	}
	if _, _, _, err := normalizeAssignment(&assignment); err != nil {
		return nil, fmt.Errorf("personal assistant: malformed persisted assignment: %w", err)
	}
	return assignment.Clone(), nil
}

func normalizeState(input *State) (*State, string, string, error) {
	if input == nil {
		return nil, "", "", fmt.Errorf("personal assistant: state is required")
	}
	state := input.Clone()
	var err error
	if state.UserID, err = validateOpaqueID("user id", state.UserID, true); err != nil {
		return nil, "", "", err
	}
	if state.AssistantID, err = validateOpaqueID("assistant id", state.AssistantID, true); err != nil {
		return nil, "", "", err
	}
	if state.Status, err = NormalizeRelationshipStatus(string(state.Status)); err != nil {
		return nil, "", "", err
	}
	if state.FirstAssignmentStatus, err = NormalizeFirstAssignmentStatus(string(state.FirstAssignmentStatus)); err != nil {
		return nil, "", "", err
	}
	if state.DisplayName, err = validateText("display name", state.DisplayName, MaxDisplayNameLen, false); err != nil {
		return nil, "", "", err
	}
	if state.Mandate, err = validateText("mandate", state.Mandate, MaxMandateLen, false); err != nil {
		return nil, "", "", err
	}
	if state.HQWorkspaceID, err = validateOpaqueID("hq workspace id", state.HQWorkspaceID, false); err != nil {
		return nil, "", "", err
	}
	if state.HQEntryAgentInstanceID, err = validateOpaqueID("hq entry agent instance id", state.HQEntryAgentInstanceID, false); err != nil {
		return nil, "", "", err
	}
	if state.GlobalAgentProfileName, err = validateText("global agent profile name", state.GlobalAgentProfileName, MaxDisplayNameLen, false); err != nil {
		return nil, "", "", err
	}
	if state.LastHireRequestID, err = validateOpaqueID("last hire request id", state.LastHireRequestID, false); err != nil {
		return nil, "", "", err
	}
	if state.HirePayloadHash, err = validateOpaqueID("hire payload hash", state.HirePayloadHash, false); err != nil {
		return nil, "", "", err
	}
	if len(state.HirePayloadJSON) > MaxAssignmentJSONBytes || (state.HirePayloadJSON != "" && !json.Valid([]byte(state.HirePayloadJSON))) {
		return nil, "", "", errors.New("personal assistant: invalid hire operation payload")
	}
	if state.RepairStep, err = NormalizeRepairStep(string(state.RepairStep)); err != nil {
		return nil, "", "", err
	}
	rawFocus := make([]string, len(state.FocusAreas))
	for i, area := range state.FocusAreas {
		rawFocus[i] = string(area)
	}
	state.FocusAreas, err = NormalizeFocusAreas(rawFocus)
	if err != nil {
		return nil, "", "", err
	}
	if state.Appearance == nil {
		state.Appearance = types.NewAgentAppearance()
	} else {
		state.Appearance.Normalize()
	}
	appearance, err := json.Marshal(state.Appearance)
	if err != nil {
		return nil, "", "", fmt.Errorf("personal assistant: encode appearance: %w", err)
	}
	if len(appearance) > MaxAppearanceJSONBytes {
		return nil, "", "", fmt.Errorf("personal assistant: appearance is too large")
	}
	focus, err := json.Marshal(state.FocusAreas)
	if err != nil {
		return nil, "", "", fmt.Errorf("personal assistant: encode focus areas: %w", err)
	}
	return state, string(appearance), string(focus), nil
}

func normalizeAssignment(input *Assignment) (*Assignment, string, string, error) {
	if input == nil {
		return nil, "", "", fmt.Errorf("personal assistant: assignment is required")
	}
	assignment := input.Clone()
	var err error
	if assignment.PreviewID, err = validateOpaqueID("preview id", assignment.PreviewID, true); err != nil {
		return nil, "", "", err
	}
	if assignment.UserID, err = validateOpaqueID("user id", assignment.UserID, true); err != nil {
		return nil, "", "", err
	}
	if assignment.AssistantID, err = validateOpaqueID("assistant id", assignment.AssistantID, true); err != nil {
		return nil, "", "", err
	}
	if assignment.Status, err = NormalizeAssignmentStatus(string(assignment.Status)); err != nil {
		return nil, "", "", err
	}
	payload := []byte(assignment.NormalizedPayload)
	if len(payload) == 0 || len(payload) > MaxAssignmentJSONBytes || !json.Valid(payload) {
		return nil, "", "", fmt.Errorf("personal assistant: normalized assignment payload must be valid JSON up to %d bytes", MaxAssignmentJSONBytes)
	}
	if hash := PayloadHash(payload); assignment.NormalizedPayloadHash != hash {
		return nil, "", "", fmt.Errorf("personal assistant: assignment payload hash does not match")
	}
	if len(assignment.CreatedCanonicalRefs) > MaxCanonicalRefs {
		return nil, "", "", fmt.Errorf("personal assistant: too many canonical references")
	}
	for i := range assignment.CreatedCanonicalRefs {
		ref := &assignment.CreatedCanonicalRefs[i]
		ref.Kind = strings.TrimSpace(ref.Kind)
		ref.WorkspaceID = strings.TrimSpace(ref.WorkspaceID)
		ref.ID = strings.TrimSpace(ref.ID)
		if ref.Kind == "" || ref.ID == "" || len(ref.Kind) > 64 || len(ref.ID) > 200 || len(ref.WorkspaceID) > 200 {
			return nil, "", "", fmt.Errorf("personal assistant: invalid canonical reference")
		}
	}
	refs, err := json.Marshal(assignment.CreatedCanonicalRefs)
	if err != nil {
		return nil, "", "", fmt.Errorf("personal assistant: encode canonical references: %w", err)
	}
	return assignment, string(payload), string(refs), nil
}

func isConstraintError(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "UNIQUE constraint") || strings.Contains(message, "FOREIGN KEY constraint")
}
