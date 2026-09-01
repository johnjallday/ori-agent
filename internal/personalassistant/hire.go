package personalassistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/charactercatalog"
	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/systemassistant"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

const (
	// DefaultAssistantName is the neutral editable hire default.
	DefaultAssistantName = "Assistant"
	// PersonalAssistantPromptFragment is trusted host copy layered after the
	// canonical Chief of Staff prompt. It adds relationship context but no new
	// capability or authority.
	PersonalAssistantPromptFragment = "You are the user's chosen personal assistant and the visible front door for Personal HQ. Follow the user's working agreement, keep confirmed priorities and commitments visible, and use existing routing and confirmation gates. Do not perform specialist project work yourself or expand tools, permissions, connections, or filesystem scope."
)

var (
	// ErrValidation marks a bounded hire input validation failure.
	ErrValidation = errors.New("personal assistant: invalid hire request")
)

// HireRequest is one confirmed, idempotent hire operation.
type HireRequest struct {
	RequestID     string
	IfVersion     int64
	DisplayName   string
	Appearance    *types.AgentAppearance
	Mandate       string
	FocusAreas    []string
	Timezone      string
	ScheduleDays  []string
	ScheduleTime  string
	NotifyOnReady bool
}

// HireResult contains canonical durable identities, never inferred names.
type HireResult struct {
	State       *State
	BriefConfig *dailybrief.Config
	Resumed     bool
}

// PartialHireError reports a visible durable partial result and a safe retry
// step without exposing the underlying provider/database message to clients.
type PartialHireError struct {
	Step  RepairStep
	State *State
	Err   error
}

func (e *PartialHireError) Error() string {
	return fmt.Sprintf("personal assistant: partial hire at %s: %v", e.Step, e.Err)
}

func (e *PartialHireError) Unwrap() error { return e.Err }

// HireHQManager is the designation/onboarding boundary implemented by
// personalhq.Service.
type HireHQManager interface {
	Status(ctx context.Context, userID string) (*personalhq.Status, error)
	Designate(ctx context.Context, userID, workspaceID string) (*personalhq.Status, error)
	SetOnboardingState(ctx context.Context, userID string, state userprofile.HQOnboardingState) (*personalhq.Status, error)
}

// HireBriefManager is the canonical Daily Brief config boundary.
type HireBriefManager interface {
	GetConfig(ctx context.Context, workspaceID string) (*dailybrief.Config, error)
	UpdateConfig(ctx context.Context, cfg dailybrief.Config) (*dailybrief.Config, error)
}

// HireCoordinator provisions one selected assistant and its Personal HQ.
type HireCoordinator struct {
	eligibility EligibilityReader
	store       Store
	creator     personalhq.AssistantWorkspaceCreator
	hq          HireHQManager
	briefs      HireBriefManager
	now         func() time.Time

	// A single-process lock prevents two same-user HTTP retries from entering
	// the workspace creator concurrently. Durable request/assistant IDs and
	// workspace metadata still provide restart-safe idempotency.
	mu sync.Mutex
}

// NewHireCoordinator constructs the hire operation coordinator.
func NewHireCoordinator(eligibility EligibilityReader, store Store, creator personalhq.AssistantWorkspaceCreator, hq HireHQManager, briefs HireBriefManager) *HireCoordinator {
	return &HireCoordinator{
		eligibility: eligibility, store: store, creator: creator, hq: hq, briefs: briefs,
		now: time.Now,
	}
}

type normalizedHireRequest struct {
	RequestID     string                 `json:"request_id"`
	DisplayName   string                 `json:"display_name"`
	Appearance    *types.AgentAppearance `json:"appearance"`
	Mandate       string                 `json:"mandate"`
	FocusAreas    []FocusArea            `json:"focus_areas"`
	Timezone      string                 `json:"timezone"`
	ScheduleDays  []string               `json:"schedule_days"`
	ScheduleTime  string                 `json:"schedule_time"`
	NotifyOnReady bool                   `json:"notify_on_ready"`
	Hash          string                 `json:"-"`
}

// Hire validates all user-controlled input before any persistence or
// provisioning consequence, then starts/resumes one durable operation.
func (c *HireCoordinator) Hire(ctx context.Context, userID string, request HireRequest) (*HireResult, error) {
	if c == nil || c.eligibility == nil || !c.eligibility.IsPersonalAssistantEligible() {
		return nil, ErrIneligible
	}
	if c.store == nil || c.creator == nil || c.hq == nil || c.briefs == nil {
		return nil, errors.New("personal assistant: hire coordinator is not configured")
	}
	normalized, err := normalizeHireRequest(request)
	if err != nil {
		return nil, err
	}
	userID, err = validateOpaqueID("user id", userID, true)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrValidation, err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	state, resumed, canonicalRequest, err := c.claimOperation(ctx, userID, request.IfVersion, normalized)
	if err != nil {
		return nil, err
	}
	normalized = canonicalRequest
	if state.Status == StatusActive || state.Status == StatusPaused {
		config, _ := c.briefs.GetConfig(ctx, state.HQWorkspaceID)
		return &HireResult{State: state.Clone(), BriefConfig: cloneBriefConfig(config), Resumed: true}, nil
	}
	if state.Status == StatusRepairNeeded && state.RepairStep == RepairNone {
		return nil, ErrRepairNeeded
	}
	recordEvent(EventHireStarted, EventData{AssistantID: state.AssistantID, WorkspaceID: state.HQWorkspaceID, State: string(state.Status)})

	created, err := c.creator.CreatePersonalAssistantHQ(ctx, "My HQ", personalhq.AssistantCreationOptions{
		AssistantID: state.AssistantID, RequestID: normalized.RequestID,
		DisplayName: normalized.DisplayName, Appearance: normalized.Appearance.Clone(),
		Role: types.RoleOrchestrator, SystemPromptFragment: PersonalAssistantPromptFragment,
	})
	if err != nil {
		// No canonical workspace IDs were returned, so the operation remains
		// hiring. The creator's request/assistant metadata makes a retry safe if
		// the workspace became visible just before the error.
		recordEvent(EventRecoverableFailure, EventData{
			AssistantID: state.AssistantID, WorkspaceID: state.HQWorkspaceID,
			State: string(state.Status), Recoverable: true, ReasonCode: "hq_creation",
		})
		return nil, fmt.Errorf("personal assistant: create personal hq: %w", err)
	}
	if created == nil || strings.TrimSpace(created.WorkspaceID) == "" ||
		strings.TrimSpace(created.EntryAgentInstanceID) == "" ||
		strings.TrimSpace(created.GlobalAgentProfileName) == "" {
		recordEvent(EventRecoverableFailure, EventData{
			AssistantID: state.AssistantID, WorkspaceID: state.HQWorkspaceID,
			State: string(state.Status), Recoverable: true, ReasonCode: "hq_creation",
		})
		return nil, errors.New("personal assistant: creator returned incomplete canonical identity")
	}

	if state.HQWorkspaceID != "" && (state.HQWorkspaceID != created.WorkspaceID ||
		state.HQEntryAgentInstanceID != created.EntryAgentInstanceID) {
		return nil, fmt.Errorf("%w: persisted hire points at a different hq or entry agent", ErrConflict)
	}
	if state.HQWorkspaceID == "" {
		next := state.Clone()
		next.HQWorkspaceID = created.WorkspaceID
		next.HQEntryAgentInstanceID = created.EntryAgentInstanceID
		next.GlobalAgentProfileName = created.GlobalAgentProfileName
		next.Status = StatusHiring
		next.RepairStep = RepairNone
		expectedVersion := state.StateVersion
		updated, updateErr := c.store.UpdateState(ctx, next, expectedVersion)
		if updateErr != nil {
			repair := next.Clone()
			repair.Status = StatusRepairNeeded
			repair.RepairStep = RepairFinalization
			if persisted, repairErr := c.store.UpdateState(ctx, repair, expectedVersion); repairErr == nil {
				repair = persisted
			}
			return nil, &PartialHireError{Step: RepairFinalization, State: repair, Err: updateErr}
		}
		state = updated
	}

	if err := c.ensureDesignation(ctx, userID, state.HQWorkspaceID); err != nil {
		return nil, c.partial(ctx, state, RepairDesignation, err)
	}

	config, err := c.ensureBriefConfig(ctx, userID, state.HQWorkspaceID, normalized)
	if err != nil {
		return nil, c.partial(ctx, state, RepairDailyBriefConfig, err)
	}

	if _, err := c.hq.SetOnboardingState(ctx, userID, userprofile.HQOnboardingCompleted); err != nil {
		return nil, c.partial(ctx, state, RepairFinalization, err)
	}

	final := state.Clone()
	final.Status = StatusActive
	final.RepairStep = RepairNone
	final.HirePayloadJSON = ""
	if final.HiredAt == nil {
		hiredAt := c.now().UTC()
		final.HiredAt = &hiredAt
	}
	updated, updateErr := c.store.UpdateState(ctx, final, state.StateVersion)
	if updateErr != nil {
		return nil, c.partial(ctx, state, RepairFinalization, updateErr)
	}
	state = updated
	recordEvent(EventHireCompleted, EventData{AssistantID: state.AssistantID, WorkspaceID: state.HQWorkspaceID, State: string(state.Status)})
	return &HireResult{State: state.Clone(), BriefConfig: cloneBriefConfig(config), Resumed: resumed}, nil
}

func (c *HireCoordinator) claimOperation(ctx context.Context, userID string, ifVersion int64, request normalizedHireRequest) (*State, bool, normalizedHireRequest, error) {
	state, err := c.store.GetState(ctx, userID)
	if errors.Is(err, ErrNotFound) {
		if ifVersion != 0 {
			return nil, false, request, fmt.Errorf("%w: expected no relationship version", ErrConflict)
		}
		state = NewState(userID)
		state.Status = StatusHiring
		state.DisplayName = request.DisplayName
		state.Appearance = request.Appearance.Clone()
		state.Mandate = request.Mandate
		state.FocusAreas = append([]FocusArea(nil), request.FocusAreas...)
		state.FirstAssignmentStatus = FirstAssignmentNotStarted
		state.LastHireRequestID = request.RequestID
		state.HirePayloadHash = request.Hash
		payload, marshalErr := json.Marshal(request)
		if marshalErr != nil {
			return nil, false, request, marshalErr
		}
		state.HirePayloadJSON = string(payload)
		created, createErr := c.store.CreateState(ctx, state)
		if createErr == nil {
			return created, false, request, nil
		}
		if !errors.Is(createErr, ErrConflict) {
			return nil, false, request, createErr
		}
		// A concurrent same-user claim won. Reload and apply the same replay
		// checks below rather than creating a second identity.
		state, err = c.store.GetState(ctx, userID)
	}
	if err != nil {
		return nil, false, request, err
	}
	if state.LastHireRequestID != request.RequestID {
		return nil, false, request, fmt.Errorf("%w: a different personal assistant relationship already exists", ErrConflict)
	}
	if state.HirePayloadHash != request.Hash {
		if state.Status != StatusHiring && state.Status != StatusRepairNeeded {
			return nil, false, request, fmt.Errorf("%w: the completed hire request payload changed", ErrConflict)
		}
		stored, storedErr := decodeStoredHireRequest(state)
		if storedErr != nil {
			return nil, false, request, ErrRepairNeeded
		}
		request = stored
	} else if state.HirePayloadJSON != "" && (state.Status == StatusHiring || state.Status == StatusRepairNeeded) {
		stored, storedErr := decodeStoredHireRequest(state)
		if storedErr != nil {
			return nil, false, request, ErrRepairNeeded
		}
		request = stored
	}
	if state.Status == StatusNotHired {
		if ifVersion != state.StateVersion {
			return nil, false, request, fmt.Errorf("%w: stale relationship version", ErrConflict)
		}
		next := state.Clone()
		next.Status = StatusHiring
		returnState, updateErr := c.store.UpdateState(ctx, next, state.StateVersion)
		return returnState, true, request, updateErr
	}
	return state, true, request, nil
}

func decodeStoredHireRequest(state *State) (normalizedHireRequest, error) {
	var request normalizedHireRequest
	if state == nil || state.HirePayloadJSON == "" || PayloadHash([]byte(state.HirePayloadJSON)) != state.HirePayloadHash {
		return request, errors.New("personal assistant: invalid stored hire operation")
	}
	if err := json.Unmarshal([]byte(state.HirePayloadJSON), &request); err != nil {
		return request, err
	}
	request.Hash = state.HirePayloadHash
	if request.RequestID != state.LastHireRequestID {
		return request, errors.New("personal assistant: stored hire request id mismatch")
	}
	return request, nil
}

func (c *HireCoordinator) ensureDesignation(ctx context.Context, userID, workspaceID string) error {
	status, err := c.hq.Status(ctx, userID)
	if err != nil {
		return err
	}
	if status != nil && status.Valid {
		if status.WorkspaceID == workspaceID {
			return nil
		}
		return fmt.Errorf("%w: another personal hq is already designated", ErrConflict)
	}
	_, err = c.hq.Designate(ctx, userID, workspaceID)
	return err
}

func (c *HireCoordinator) ensureBriefConfig(ctx context.Context, userID, workspaceID string, request normalizedHireRequest) (*dailybrief.Config, error) {
	desired, err := dailybrief.NormalizeConfig(dailybrief.Config{
		WorkspaceID: workspaceID, UserID: userID, Timezone: request.Timezone,
		ScheduleDays: append([]string(nil), request.ScheduleDays...),
		ScheduleTime: request.ScheduleTime, ScheduleEnabled: true,
		Scope: dailybrief.ScopeAll, IncludeFutureWorkspaces: true,
		NotifyOnReady: request.NotifyOnReady,
	})
	if err != nil {
		return nil, err
	}
	existing, err := c.briefs.GetConfig(ctx, workspaceID)
	if err == nil && equivalentBriefConfig(existing, &desired) {
		return existing, nil
	}
	if err != nil && !errors.Is(err, dailybrief.ErrConfigNotFound) {
		return nil, err
	}
	return c.briefs.UpdateConfig(ctx, desired)
}

func (c *HireCoordinator) partial(ctx context.Context, state *State, step RepairStep, cause error) error {
	recordEvent(EventRecoverableFailure, EventData{
		AssistantID: state.AssistantID, WorkspaceID: state.HQWorkspaceID,
		State: string(StatusRepairNeeded), Recoverable: true, ReasonCode: string(step),
	})
	next := state.Clone()
	next.Status = StatusRepairNeeded
	next.RepairStep = step
	updated, err := c.store.UpdateState(ctx, next, state.StateVersion)
	if err != nil {
		return &PartialHireError{Step: step, State: next, Err: cause}
	}
	return &PartialHireError{Step: step, State: updated, Err: cause}
}

func normalizeHireRequest(input HireRequest) (normalizedHireRequest, error) {
	var out normalizedHireRequest
	if input.IfVersion < 0 {
		return out, fmt.Errorf("%w: if_version cannot be negative", ErrValidation)
	}
	var err error
	if out.RequestID, err = validateOpaqueID("request id", input.RequestID, true); err != nil {
		return out, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	if out.DisplayName, err = validateText("display name", input.DisplayName, MaxDisplayNameLen, true); err != nil {
		return out, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	if systemassistant.IsKnownName(out.DisplayName) {
		return out, fmt.Errorf("%w: assistant name is reserved for Ori", ErrValidation)
	}
	for _, r := range out.DisplayName {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == ' ', r == '_', r == '-':
		default:
			return out, fmt.Errorf("%w: assistant name contains unsupported characters", ErrValidation)
		}
	}
	if out.Mandate, err = validateMandate(input.Mandate); err != nil {
		return out, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	out.FocusAreas, err = NormalizeFocusAreas(input.FocusAreas)
	if err != nil {
		return out, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	if len(out.FocusAreas) == 0 && out.Mandate == "" {
		return out, fmt.Errorf("%w: choose at least one focus area or provide a mandate", ErrValidation)
	}
	out.Appearance, err = normalizeHireAppearance(input.Appearance)
	if err != nil {
		return out, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	out.Timezone = strings.TrimSpace(input.Timezone)
	if out.Timezone == "" {
		out.Timezone = "UTC"
	}
	if _, err := time.LoadLocation(out.Timezone); err != nil {
		return out, fmt.Errorf("%w: invalid IANA timezone", ErrValidation)
	}
	out.ScheduleDays, err = normalizeHireDays(input.ScheduleDays)
	if err != nil {
		return out, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	out.ScheduleTime = strings.TrimSpace(input.ScheduleTime)
	if out.ScheduleTime == "" {
		out.ScheduleTime = "08:00"
	}
	if _, err := dailybrief.NormalizeConfig(dailybrief.Config{
		WorkspaceID: "validation", Timezone: out.Timezone,
		ScheduleDays: out.ScheduleDays, ScheduleTime: out.ScheduleTime,
	}); err != nil {
		return out, fmt.Errorf("%w: invalid Daily Brief rhythm", ErrValidation)
	}
	out.NotifyOnReady = input.NotifyOnReady
	payload, err := json.Marshal(out)
	if err != nil {
		return out, fmt.Errorf("%w: could not normalize hire request", ErrValidation)
	}
	out.Hash = PayloadHash(payload)
	return out, nil
}

func normalizeHireAppearance(input *types.AgentAppearance) (*types.AgentAppearance, error) {
	if input == nil {
		return types.NewAgentAppearance(), nil
	}
	appearance := input.Clone()
	if !types.IsValidAppearanceMode(appearance.Mode) {
		return nil, fmt.Errorf("invalid appearance mode %q", appearance.Mode)
	}
	if appearance.Generated != nil && strings.TrimSpace(appearance.Generated.Color) != "" {
		color, ok := types.NormalizeAppearanceColor(appearance.Generated.Color)
		if !ok {
			return nil, errors.New("invalid generated appearance color")
		}
		appearance.SetGeneratedColor(color)
	}
	if appearance.Uploaded != nil && strings.TrimSpace(appearance.Uploaded.Image) != "" {
		return nil, errors.New("uploaded appearance must use the appearance upload endpoint")
	}
	if appearance.Character != nil && strings.TrimSpace(appearance.Character.CatalogID) != "" {
		if appearance.Character.CatalogVersion != 0 {
			return nil, errors.New("appearance character version is server-managed")
		}
		catalog, catalogErr := charactercatalog.Load()
		if catalogErr != nil {
			return nil, errors.New("character catalog is unavailable")
		}
		characterID := charactercatalog.CharacterID(strings.TrimSpace(appearance.Character.CatalogID))
		entry, found := catalog.Get(characterID)
		if !found || !catalog.IsAssignable(characterID) {
			return nil, errors.New("appearance character is not assignable")
		}
		appearance.SetCharacter(string(characterID), entry.EntryVersion)
	}
	appearance.Normalize()
	if appearance.Mode == types.AppearanceModeCharacter && appearance.CharacterCatalogID() == "" {
		return nil, errors.New("character appearance requires a catalog selection")
	}
	encoded, err := json.Marshal(appearance)
	if err != nil || len(encoded) > MaxAppearanceJSONBytes {
		return nil, errors.New("appearance is too large")
	}
	return appearance, nil
}

func normalizeHireDays(input []string) ([]string, error) {
	if len(input) == 0 {
		return []string{"mon", "tue", "wed", "thu", "fri"}, nil
	}
	allowed := map[string]bool{"mon": true, "tue": true, "wed": true, "thu": true, "fri": true, "sat": true, "sun": true}
	seen := make(map[string]bool, 7)
	out := make([]string, 0, len(input))
	for _, raw := range input {
		day := strings.ToLower(strings.TrimSpace(raw))
		if !allowed[day] {
			return nil, fmt.Errorf("invalid schedule day %q", raw)
		}
		if !seen[day] {
			seen[day] = true
			out = append(out, day)
		}
	}
	return out, nil
}

func equivalentBriefConfig(left, right *dailybrief.Config) bool {
	if left == nil || right == nil {
		return false
	}
	return left.WorkspaceID == right.WorkspaceID && left.UserID == right.UserID &&
		left.Timezone == right.Timezone && slices.Equal(left.ScheduleDays, right.ScheduleDays) &&
		left.ScheduleTime == right.ScheduleTime && left.ScheduleEnabled == right.ScheduleEnabled &&
		left.Scope == right.Scope && slices.Equal(left.SelectedWorkspaceIDs, right.SelectedWorkspaceIDs) &&
		left.IncludeFutureWorkspaces == right.IncludeFutureWorkspaces && left.NotifyOnReady == right.NotifyOnReady
}

func cloneBriefConfig(config *dailybrief.Config) *dailybrief.Config {
	if config == nil {
		return nil
	}
	out := *config
	out.ScheduleDays = append([]string(nil), config.ScheduleDays...)
	out.SelectedWorkspaceIDs = append([]string(nil), config.SelectedWorkspaceIDs...)
	return &out
}
