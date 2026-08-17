package runtimecapability

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

var sensitiveRuntimeTextPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(?:^|\s)(?:/users/|/home/|/private/|/tmp/|~/)\S+`),
	regexp.MustCompile(`(?i)\b[a-z]:\\\S+`),
	regexp.MustCompile(`(?i)\b(?:https?|file)://\S+`),
	regexp.MustCompile(`(?i)\b(?:localhost|127\.0\.0\.1|\[::1\])(?::\d+)?\b`),
	regexp.MustCompile(`(?i)\bport\s*[=:]?\s*\d{2,5}\b`),
	regexp.MustCompile(`(?i)\b(?:token|password|secret|credential|authorization)\s*[=:]\s*\S+`),
	regexp.MustCompile(`(?i)\b(?:command|command_id|cmd_id|account|account_id)\s*[=:]\s*\S+`),
	regexp.MustCompile(`\b\S+@\S+\b`),
	regexp.MustCompile(`(?i)\b\S+\.(?:rpp|lua|ini|cfg|conf)\b`),
}

const (
	maxSafeSummaryLength = 500
	maxActionTokenLength = 64
	maxActionLabelLength = 120
	maxActionURLLength   = 512
)

// Store exposes the canonical folder-backed workspace record. Runtime contract,
// mode, grant, and verification data are workspace.json fields; a SQLite-only
// projection is insufficient and must not be treated as an empty contract.
type Store interface {
	GetFolderWorkspace(id string) (*workspace.Workspace, error)
	Update(id string, mutate func(*workspace.Workspace) error) error
}

// Service resolves only persisted workspace data through a compiled registry.
type Service struct {
	store    Store
	registry *Registry
	now      func() time.Time
}

func NewService(store Store, registry *Registry) *Service {
	if registry == nil {
		registry = NewRegistry()
	}
	return &Service{store: store, registry: registry, now: time.Now}
}

// Status evaluates durable requirements without making a live probe. It may
// persist a newly derived durable completion/regression, but it never invokes a
// confirmed action or verifier and never persists connectivity.
func (s *Service) Status(ctx context.Context, workspaceID string) (Status, error) {
	return s.evaluate(ctx, workspaceID, false)
}

// Recheck evaluates durable state and performs fresh read-only live checks.
func (s *Service) Recheck(ctx context.Context, workspaceID string) (Status, error) {
	return s.evaluate(ctx, workspaceID, true)
}

// ValidateTaskCapabilities rejects known runtime adapter/capability keys that
// a new task tries to use outside this workspace's declared contract. Keys not
// claimed by the compiled runtime registry remain ordinary planning/toolbox
// vocabulary and preserve their existing behavior.
func (s *Service) ValidateTaskCapabilities(workspaceID string, capabilities []string) error {
	if s == nil || s.store == nil {
		return nil
	}
	ws, err := s.store.GetFolderWorkspace(workspaceID)
	if err != nil || ws == nil {
		return err
	}
	contract := ws.RuntimeRequirementsSnapshot()
	for _, key := range workspace.NormalizeCapabilityKeys(capabilities) {
		declared := contractDeclaresRequirement(contract, key)
		_, knownAdapterKey := s.registry.Lookup(key)
		switch {
		case declared && contract != nil && contract.StructurallyValid():
			continue
		case declared || knownAdapterKey:
			return fmt.Errorf("%w: runtime capability %q is not declared by a usable workspace runtime contract", workspace.ErrInvalidRuntimeTaskCapability, key)
		}
	}
	return nil
}

// EvaluateTaskCapability lets the composite execution gate claim only keys
// declared by this workspace's persisted runtime contract. It performs a fresh
// durable and live preflight and returns one exact repair before any provider or
// model invocation begins.
func (s *Service) EvaluateTaskCapability(workspaceID, capability string) (bool, *workspace.TaskBlockedError) {
	key := workspace.NormalizeRuntimeIdentifier(capability)
	if key == "" || s == nil || s.store == nil {
		return false, nil
	}
	ws, err := s.store.GetFolderWorkspace(workspaceID)
	if err != nil || ws == nil {
		return false, nil
	}
	contract := ws.RuntimeRequirementsSnapshot()
	if contract == nil || !contractDeclaresRequirement(contract, key) {
		return false, nil
	}
	if !contract.StructurallyValid() {
		return true, runtimeTaskBlocked(workspaceID, &Blocker{
			RequirementKey: key,
			ReasonCode:     ReasonUnsupportedSnapshot,
			Summary:        "This workspace's runtime contract is not supported by this version of Ori.",
			Action:         &Action{Token: "review_runtime_setup", Code: "review_runtime_setup", Label: "Review runtime setup"},
		})
	}
	state := ws.GetRuntimeState()
	mode, selected := resolveSelectedMode(contract, state)
	if !selected {
		return true, runtimeTaskBlocked(workspaceID, &Blocker{
			RequirementKey: key,
			ReasonCode:     ReasonModeSelectionRequired,
			Summary:        "Choose an operating mode before starting this task.",
			Action:         &Action{Token: "review_runtime_setup", Code: "review_runtime_setup", Label: "Review runtime setup"},
		})
	}
	if _, required := selectedRequirement(contract, mode, key); !required {
		return true, runtimeTaskBlocked(workspaceID, &Blocker{
			RequirementKey: key,
			ReasonCode:     ReasonModeNotEnabled,
			Summary:        "The selected operating mode does not enable this runtime capability.",
			Action:         &Action{Token: "review_runtime_setup", Code: "review_runtime_setup", Label: "Review live-control setup"},
		})
	}
	status, statusErr := s.Recheck(context.Background(), workspaceID)
	if statusErr != nil {
		return true, runtimeTaskBlocked(workspaceID, &Blocker{
			RequirementKey: key,
			ReasonCode:     ReasonRequirementUnsupported,
			Summary:        "This runtime capability could not be checked.",
			Action:         &Action{Token: "review_runtime_setup", Code: "review_runtime_setup", Label: "Review runtime setup"},
		})
	}
	if status.FirstBlocker != nil {
		return true, runtimeTaskBlocked(workspaceID, status.FirstBlocker)
	}
	return true, nil
}

// SelectMode persists an explicit operating-mode choice, then evaluates that
// mode from canonical server state. No requirement action runs during selection.
func (s *Service) SelectMode(ctx context.Context, workspaceID, modeID string) (Status, error) {
	ws, contract, _, err := s.load(workspaceID)
	if err != nil {
		return Status{}, err
	}
	mode, found := contract.Mode(modeID)
	if !found {
		return Status{}, fmt.Errorf("%w: %q", ErrUnknownMode, workspace.NormalizeRuntimeIdentifier(modeID))
	}
	state := ws.GetRuntimeState()
	if state == nil {
		state = &workspace.WorkspaceRuntimeState{}
	}
	if state.SelectedModeID != mode.ID {
		state.SelectedModeID = mode.ID
		if err := s.saveRuntimeState(workspaceID, state); err != nil {
			return Status{}, err
		}
	}
	return s.Status(ctx, workspaceID)
}

// ConfirmAction executes only the exact token a fresh durable evaluation
// currently offers for the selected requirement.
func (s *Service) ConfirmAction(ctx context.Context, workspaceID, requirementKey, actionToken string) (Status, error) {
	if len(strings.TrimSpace(actionToken)) == 0 || len(actionToken) > maxActionTokenLength {
		return Status{}, ErrUnknownAction
	}
	ws, contract, mode, err := s.loadSelected(workspaceID)
	if err != nil {
		return Status{}, err
	}
	requirement, found := selectedRequirement(contract, mode, requirementKey)
	if !found {
		return Status{}, fmt.Errorf("%w: %q", ErrUnknownRequirement, workspace.NormalizeRuntimeIdentifier(requirementKey))
	}
	adapter, found := s.registry.Lookup(requirement.Adapter)
	if !found {
		return Status{}, ErrUnknownAdapter
	}
	confirmer, ok := adapter.(ActionConfirmer)
	if !ok {
		return Status{}, ErrUnknownAction
	}
	request := evaluationRequest(ws, mode, requirement)
	result, evalErr := adapter.EvaluateDurable(ctx, request)
	if evalErr != nil {
		return Status{}, ErrUnknownAction
	}
	action := sanitizeAction(result.Action)
	if action == nil || action.Token != strings.TrimSpace(actionToken) {
		return Status{}, ErrUnknownAction
	}
	if err := confirmer.ConfirmAction(ctx, ConfirmedActionRequest{EvaluationRequest: request, ActionToken: action.Token}); err != nil {
		return Status{}, fmt.Errorf("runtime action failed: %w", err)
	}
	return s.Status(ctx, workspaceID)
}

// Verify runs an adapter-owned explicit verification and records timestamps only
// after a fresh durable evaluation reports the target requirement configured.
func (s *Service) Verify(ctx context.Context, workspaceID, requirementKey string) (Status, error) {
	ws, contract, mode, err := s.loadSelected(workspaceID)
	if err != nil {
		return Status{}, err
	}
	requirement, found := selectedRequirement(contract, mode, requirementKey)
	if !found {
		return Status{}, fmt.Errorf("%w: %q", ErrUnknownRequirement, workspace.NormalizeRuntimeIdentifier(requirementKey))
	}
	adapter, found := s.registry.Lookup(requirement.Adapter)
	if !found {
		return Status{}, ErrUnknownAdapter
	}
	verifier, ok := adapter.(Verifier)
	if !ok {
		return Status{}, ErrVerificationFailed
	}
	request := evaluationRequest(ws, mode, requirement)
	result, verifyErr := verifier.Verify(ctx, VerificationRequest{EvaluationRequest: request})
	if verifyErr != nil || !result.Succeeded {
		return Status{}, ErrVerificationFailed
	}

	// Ignore the verifier's success assertion until durable state agrees.
	durable, durableErr := adapter.EvaluateDurable(ctx, request)
	if durableErr != nil || normalizeDurableState(durable.State) != DurableConfigured {
		return Status{}, ErrVerificationFailed
	}
	if err := s.recordVerification(workspaceID, requirement.Key); err != nil {
		return Status{}, err
	}
	return s.Recheck(ctx, workspaceID)
}

func (s *Service) evaluate(ctx context.Context, workspaceID string, checkLive bool) (Status, error) {
	_, contract, state, err := s.load(workspaceID)
	if errors.Is(err, ErrNoRuntimeContract) {
		return Status{
			WorkspaceID:  workspaceID,
			Applicable:   false,
			DurableState: DurableNotStarted,
			LiveState:    LiveNotApplicable,
		}, nil
	}
	if err != nil {
		return Status{}, err
	}

	status := Status{
		WorkspaceID:     workspaceID,
		Applicable:      true,
		ContractVersion: contract.SchemaVersion,
		DurableState:    DurableNotStarted,
		LiveState:       LiveNotApplicable,
		Modes:           make([]ModeStatus, 0, len(contract.OperatingModes)),
	}
	for _, mode := range contract.OperatingModes {
		status.Modes = append(status.Modes, ModeStatus{
			ID: mode.ID, Label: safeText(mode.Label, workspace.MaxRuntimeLabelLength), Description: safeText(mode.Description, workspace.MaxRuntimeDescriptionLength),
		})
	}

	mode, selected := resolveSelectedMode(contract, state)
	if !selected {
		status.ModeSelectionRequired = len(contract.OperatingModes) > 1
		if status.ModeSelectionRequired {
			status.FirstBlocker = &Blocker{
				ReasonCode: ReasonModeSelectionRequired,
				Summary:    "Choose how this workspace should operate.",
			}
		}
		return status, nil
	}
	status.SelectedModeID = mode.ID
	for i := range status.Modes {
		status.Modes[i].Selected = status.Modes[i].ID == mode.ID
	}

	requirements, ok := contract.RequirementsForMode(mode.ID)
	if !ok {
		return Status{}, ErrUnsupportedSnapshot
	}
	if len(requirements) == 0 {
		status.DurableState = DurableConfigured
		status.LiveState = LiveNotApplicable
		return status, nil
	}
	status.LiveState = LiveNotChecked
	status.Requirements = make([]RequirementStatus, 0, len(requirements))

	for _, requirement := range requirements {
		persisted := persistedRequirementState(state, requirement.Key)
		projected := RequirementStatus{
			Key:             requirement.Key,
			Label:           safeText(requirement.Label, workspace.MaxRuntimeLabelLength),
			Description:     safeText(requirement.Description, workspace.MaxRuntimeDescriptionLength),
			Disclosure:      safeText(requirement.Disclosure, workspace.MaxRuntimeDisclosureLength),
			DurableState:    DurableInProgress,
			LiveState:       LiveNotChecked,
			FirstVerifiedAt: cloneTime(persisted.FirstVerifiedAt),
			LastVerifiedAt:  cloneTime(persisted.LastVerifiedAt),
		}

		adapter, registered := s.registry.Lookup(requirement.Adapter)
		if !registered {
			projected.ReasonCode = ReasonAdapterUnavailable
			projected.Summary = "This runtime requirement is unavailable in this build."
		} else {
			request := EvaluationRequest{WorkspaceID: workspaceID, Mode: mode, Requirement: requirement, Persisted: persisted}
			result, evalErr := adapter.EvaluateDurable(ctx, request)
			if evalErr != nil {
				runtimeFailureEvent(EventDurableCheckFailed, requirement.Adapter, ReasonCheckFailed)
				projected.ReasonCode = ReasonCheckFailed
				projected.Summary = "This runtime requirement could not be checked."
			} else {
				projected.DurableState = normalizeDurableState(result.State)
				projected.ReasonCode = safeCode(result.ReasonCode)
				projected.Summary = safeText(result.Summary, maxSafeSummaryLength)
				projected.Action = sanitizeAction(result.Action)
				projected.VerificationNeeded = result.VerificationRequired && projected.FirstVerifiedAt == nil
				if projected.VerificationNeeded && projected.DurableState == DurableConfigured {
					projected.DurableState = DurableInProgress
					projected.ReasonCode = ReasonVerificationRequired
					if projected.Summary == "" {
						projected.Summary = "Verify this runtime requirement to finish setup."
					}
				}
			}
		}

		if status.FirstBlocker == nil && projected.DurableState != DurableConfigured {
			status.FirstBlocker = blockerFromRequirement(projected)
			status.DurableState = projected.DurableState
		}
		status.Requirements = append(status.Requirements, projected)
	}
	if status.FirstBlocker == nil {
		status.DurableState = DurableConfigured
	}
	setVerificationRange(&status)

	if err := s.persistDurableProjection(workspaceID, state, status.Requirements); err != nil {
		return Status{}, err
	}
	if !checkLive || status.DurableState != DurableConfigured {
		return status, nil
	}

	status.LiveState = LiveNotApplicable
	for index, requirement := range requirements {
		adapter, registered := s.registry.Lookup(requirement.Adapter)
		if !registered {
			continue
		}
		checker, supported := adapter.(LiveChecker)
		if !supported {
			status.Requirements[index].LiveState = LiveNotApplicable
			continue
		}
		request := EvaluationRequest{
			WorkspaceID: workspaceID,
			Mode:        mode,
			Requirement: requirement,
			Persisted:   persistedRequirementState(state, requirement.Key),
		}
		result, liveErr := checker.CheckLive(ctx, request)
		if liveErr != nil {
			runtimeFailureEvent(EventLiveCheckFailed, requirement.Adapter, ReasonCheckFailed)
			result = LiveResult{State: LiveCheckFailed, ReasonCode: ReasonCheckFailed, Summary: "This runtime requirement could not be checked."}
		}
		liveState := normalizeLiveState(result.State)
		status.Requirements[index].LiveState = liveState
		if liveState != LiveAvailable && liveState != LiveNotApplicable {
			status.Requirements[index].ReasonCode = safeCode(result.ReasonCode)
			status.Requirements[index].Summary = safeText(result.Summary, maxSafeSummaryLength)
			status.Requirements[index].Action = sanitizeAction(result.Action)
			if status.FirstBlocker == nil {
				status.FirstBlocker = blockerFromRequirement(status.Requirements[index])
			}
		}
		status.LiveState = combineLiveState(status.LiveState, liveState)
	}
	return status, nil
}

func (s *Service) load(workspaceID string) (*workspace.Workspace, *workspace.RuntimeRequirementsContract, *workspace.WorkspaceRuntimeState, error) {
	if s == nil || s.store == nil {
		return nil, nil, nil, errors.New("runtime capability service is unavailable")
	}
	workspaceID = strings.TrimSpace(workspaceID)
	ws, err := s.store.GetFolderWorkspace(workspaceID)
	if err != nil || ws == nil {
		if err == nil {
			err = errors.New("workspace not found")
		}
		return nil, nil, nil, err
	}
	contract := ws.RuntimeRequirementsSnapshot()
	if contract == nil {
		return ws, nil, ws.GetRuntimeState(), ErrNoRuntimeContract
	}
	if !contract.StructurallyValid() {
		return ws, contract, ws.GetRuntimeState(), ErrUnsupportedSnapshot
	}
	return ws, contract, ws.GetRuntimeState(), nil
}

func (s *Service) loadSelected(workspaceID string) (*workspace.Workspace, *workspace.RuntimeRequirementsContract, workspace.RuntimeOperatingMode, error) {
	ws, contract, state, err := s.load(workspaceID)
	if err != nil {
		return nil, nil, workspace.RuntimeOperatingMode{}, err
	}
	mode, ok := resolveSelectedMode(contract, state)
	if !ok {
		return nil, nil, workspace.RuntimeOperatingMode{}, ErrModeRequired
	}
	return ws, contract, mode, nil
}

func resolveSelectedMode(contract *workspace.RuntimeRequirementsContract, state *workspace.WorkspaceRuntimeState) (workspace.RuntimeOperatingMode, bool) {
	if contract == nil {
		return workspace.RuntimeOperatingMode{}, false
	}
	if state != nil && strings.TrimSpace(state.SelectedModeID) != "" {
		if mode, ok := contract.Mode(state.SelectedModeID); ok {
			return mode, true
		}
		return workspace.RuntimeOperatingMode{}, false
	}
	return contract.ImplicitMode()
}

func contractDeclaresRequirement(contract *workspace.RuntimeRequirementsContract, key string) bool {
	key = workspace.NormalizeRuntimeIdentifier(key)
	if contract == nil || key == "" {
		return false
	}
	// Scan the inert declaration directly so a structurally invalid snapshot can
	// still be claimed and blocked as unsupported instead of falling through as
	// an ordinary planning capability.
	for _, requirement := range contract.Requirements {
		if workspace.NormalizeRuntimeIdentifier(requirement.Key) == key {
			return true
		}
	}
	return false
}

func selectedRequirement(contract *workspace.RuntimeRequirementsContract, mode workspace.RuntimeOperatingMode, key string) (workspace.RuntimeRequirement, bool) {
	key = workspace.NormalizeRuntimeIdentifier(key)
	if key == "" {
		return workspace.RuntimeRequirement{}, false
	}
	for _, required := range mode.Requires {
		if workspace.NormalizeRuntimeIdentifier(required) == key {
			return contract.Requirement(key)
		}
	}
	return workspace.RuntimeRequirement{}, false
}

func evaluationRequest(ws *workspace.Workspace, mode workspace.RuntimeOperatingMode, requirement workspace.RuntimeRequirement) EvaluationRequest {
	return EvaluationRequest{
		WorkspaceID: ws.ID,
		Mode:        mode,
		Requirement: requirement,
		Persisted:   persistedRequirementState(ws.GetRuntimeState(), requirement.Key),
	}
}

func persistedRequirementState(state *workspace.WorkspaceRuntimeState, key string) workspace.RuntimeRequirementState {
	key = workspace.NormalizeRuntimeIdentifier(key)
	if state != nil {
		for _, requirement := range state.RequirementStates {
			if workspace.NormalizeRuntimeIdentifier(requirement.RequirementKey) == key {
				return requirement
			}
		}
	}
	return workspace.RuntimeRequirementState{RequirementKey: key, ConfigurationState: workspace.RuntimeConfigurationNotStarted}
}

func (s *Service) persistDurableProjection(workspaceID string, current *workspace.WorkspaceRuntimeState, requirements []RequirementStatus) error {
	desired := workspace.CloneWorkspaceRuntimeState(current)
	if desired == nil {
		desired = &workspace.WorkspaceRuntimeState{}
	}
	for _, projected := range requirements {
		index := -1
		for i := range desired.RequirementStates {
			if workspace.NormalizeRuntimeIdentifier(desired.RequirementStates[i].RequirementKey) == projected.Key {
				index = i
				break
			}
		}
		next := workspace.RuntimeRequirementState{
			RequirementKey:     projected.Key,
			ConfigurationState: normalizeDurableState(projected.DurableState),
			FirstVerifiedAt:    cloneTime(projected.FirstVerifiedAt),
			LastVerifiedAt:     cloneTime(projected.LastVerifiedAt),
		}
		if index < 0 {
			desired.RequirementStates = append(desired.RequirementStates, next)
		} else {
			desired.RequirementStates[index] = next
		}
	}
	if reflect.DeepEqual(workspace.CloneWorkspaceRuntimeState(current), desired) {
		return nil
	}
	return s.saveRuntimeState(workspaceID, desired)
}

func (s *Service) saveRuntimeState(workspaceID string, state *workspace.WorkspaceRuntimeState) error {
	return s.store.Update(workspaceID, func(current *workspace.Workspace) error {
		current.SetRuntimeState(state)
		return nil
	})
}

func (s *Service) recordVerification(workspaceID, requirementKey string) error {
	ws, err := s.store.GetFolderWorkspace(workspaceID)
	if err != nil || ws == nil {
		return err
	}
	state := ws.GetRuntimeState()
	if state == nil {
		state = &workspace.WorkspaceRuntimeState{}
	}
	now := s.now().UTC()
	found := false
	for i := range state.RequirementStates {
		if workspace.NormalizeRuntimeIdentifier(state.RequirementStates[i].RequirementKey) != requirementKey {
			continue
		}
		found = true
		state.RequirementStates[i].ConfigurationState = DurableConfigured
		if state.RequirementStates[i].FirstVerifiedAt == nil {
			state.RequirementStates[i].FirstVerifiedAt = cloneTime(&now)
		}
		state.RequirementStates[i].LastVerifiedAt = cloneTime(&now)
		break
	}
	if !found {
		state.RequirementStates = append(state.RequirementStates, workspace.RuntimeRequirementState{
			RequirementKey:     requirementKey,
			ConfigurationState: DurableConfigured,
			FirstVerifiedAt:    cloneTime(&now),
			LastVerifiedAt:     cloneTime(&now),
		})
	}
	return s.saveRuntimeState(workspaceID, state)
}

func normalizeDurableState(state string) string {
	return workspace.NormalizeRuntimeConfigurationState(state)
}

func normalizeLiveState(state string) string {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case LiveNotApplicable:
		return LiveNotApplicable
	case LiveNotChecked:
		return LiveNotChecked
	case LiveAvailable:
		return LiveAvailable
	case LiveOffline:
		return LiveOffline
	case LiveWrongTarget:
		return LiveWrongTarget
	case LiveUnavailable:
		return LiveUnavailable
	default:
		return LiveCheckFailed
	}
}

func combineLiveState(current, next string) string {
	if current == LiveNotApplicable {
		return next
	}
	if next == LiveNotApplicable {
		return current
	}
	if current != LiveAvailable {
		return current
	}
	return next
}

func runtimeTaskBlocked(workspaceID string, blocker *Blocker) *workspace.TaskBlockedError {
	if blocker == nil {
		return nil
	}
	reason := safeText(blocker.Summary, maxSafeSummaryLength)
	if reason == "" {
		reason = "This task's runtime capability is not available."
	}
	repair := &workspace.TaskRepairAction{
		Code:  "review_runtime_setup",
		Label: "Review runtime setup",
		URL:   "/workspaces/" + url.PathEscape(strings.TrimSpace(workspaceID)) + "?runtime_setup=1",
	}
	if action := sanitizeAction(blocker.Action); action != nil {
		repair.Code = action.Code
		repair.Label = action.Label
		if action.URL != "" {
			repair.URL = action.URL
		}
	}
	return &workspace.TaskBlockedError{
		ReasonCode:       safeCode(blocker.ReasonCode),
		Reason:           reason,
		Question:         reason,
		SuggestedActions: []string{repair.Label},
		Repair:           repair,
	}
}

func blockerFromRequirement(requirement RequirementStatus) *Blocker {
	return &Blocker{
		RequirementKey: requirement.Key,
		ReasonCode:     requirement.ReasonCode,
		Summary:        requirement.Summary,
		Action:         sanitizeAction(requirement.Action),
	}
}

func setVerificationRange(status *Status) {
	for _, requirement := range status.Requirements {
		if requirement.FirstVerifiedAt != nil && (status.FirstVerifiedAt == nil || requirement.FirstVerifiedAt.Before(*status.FirstVerifiedAt)) {
			status.FirstVerifiedAt = cloneTime(requirement.FirstVerifiedAt)
		}
		if requirement.LastVerifiedAt != nil && (status.LastVerifiedAt == nil || requirement.LastVerifiedAt.After(*status.LastVerifiedAt)) {
			status.LastVerifiedAt = cloneTime(requirement.LastVerifiedAt)
		}
	}
}

func safeCode(value string) string {
	return workspace.NormalizeRuntimeIdentifier(value)
}

func safeText(value string, max int) string {
	value = strings.TrimSpace(strings.ToValidUTF8(value, ""))
	for _, pattern := range sensitiveRuntimeTextPatterns {
		if pattern.MatchString(value) {
			return ""
		}
	}
	var builder strings.Builder
	for _, r := range value {
		if r == '\n' || r == '\t' || r >= 0x20 && r != 0x7f {
			if builder.Len()+utf8.RuneLen(r) > max {
				break
			}
			builder.WriteRune(r)
		}
	}
	return strings.TrimSpace(builder.String())
}

func sanitizeAction(action *Action) *Action {
	if action == nil {
		return nil
	}
	code := safeCode(action.Code)
	token := workspace.NormalizeRuntimeIdentifier(action.Token)
	label := safeText(action.Label, maxActionLabelLength)
	if code == "" || token == "" || label == "" || len(token) > maxActionTokenLength {
		return nil
	}
	out := &Action{Token: token, Code: code, Label: label}
	rawURL := strings.TrimSpace(action.URL)
	if rawURL != "" && len(rawURL) <= maxActionURLLength {
		parsed, err := url.Parse(rawURL)
		if err == nil && parsed.IsAbs() == false && parsed.Host == "" && strings.HasPrefix(parsed.Path, "/") {
			out.URL = rawURL
		}
	}
	return out
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
