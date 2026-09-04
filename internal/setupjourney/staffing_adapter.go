package setupjourney

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const maxStaffingRoles = workspace.AssistantProgramMaxRoles

// StaffingProjection is safe response-only staffing state. Stable agent
// instance IDs, prompts, memories, credentials, histories, paths, and runtime
// grants are deliberately absent.
type StaffingProjection struct {
	Scopes []StaffingScopeProjection `json:"scopes"`
}

type StaffingScopeProjection struct {
	Scope             workspace.AssistantRoleScope `json:"scope"`
	WorkspaceID       string                       `json:"workspace_id"`
	WorkspaceLabel    string                       `json:"workspace_label"`
	BindingRevision   int64                        `json:"binding_revision"`
	RequiredComplete  bool                         `json:"required_complete"`
	RuntimeReady      bool                         `json:"runtime_ready"`
	ModelsReady       bool                         `json:"models_ready"`
	ToolGrantsReady   bool                         `json:"tool_grants_ready"`
	AuthorityBoundary string                       `json:"authority_boundary"`
	SelectedModeID    string                       `json:"selected_mode_id,omitempty"`
	Roles             []StaffingRoleProjection     `json:"roles"`
}

type StaffingRoleProjection struct {
	RoleID         string   `json:"role_id"`
	Label          string   `json:"label"`
	Responsibility string   `json:"responsibility,omitempty"`
	Required       bool     `json:"required"`
	Primary        bool     `json:"primary"`
	Configured     bool     `json:"configured"`
	ChatAvailable  bool     `json:"chat_available"`
	ProfileName    string   `json:"profile_name,omitempty"`
	Provider       string   `json:"provider,omitempty"`
	Model          string   `json:"model,omitempty"`
	UsesDefaults   bool     `json:"uses_defaults,omitempty"`
	ToolGrants     []string `json:"tool_grants,omitempty"`
}

type StaffingToolGrants interface {
	Available(skillName string) bool
	Grant(agentName, skillName string) error
	Revoke(agentName, skillName string) error
}

type StaffingModelDefaults func() (provider, model string)
type StaffingModelValidator func(provider, model string) error

type AssistantStaffingAdapter struct {
	workspaces workspace.Store
	profiles   store.Store
	grants     StaffingToolGrants
	defaults   StaffingModelDefaults
	validate   StaffingModelValidator
	mu         sync.Mutex
}

func NewAssistantStaffingAdapter(workspaces workspace.Store, profiles store.Store, grants StaffingToolGrants, defaults StaffingModelDefaults, validate StaffingModelValidator) *AssistantStaffingAdapter {
	return &AssistantStaffingAdapter{workspaces: workspaces, profiles: profiles, grants: grants, defaults: defaults, validate: validate}
}

type staffingRoleInput struct {
	RoleID   string `json:"role_id"`
	Name     string `json:"name"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
}

type staffingInput struct {
	Roles []staffingRoleInput `json:"roles"`
}

type staffingOwner struct {
	station     *workspace.Workspace
	project     *workspace.Workspace
	declaration *workspace.AssistantProgramDeclaration
}

func (a *AssistantStaffingAdapter) Read(_ context.Context, scope ReadScope) (CanonicalStepRead, error) {
	owner, err := a.owner(scope)
	if err != nil {
		return CanonicalStepRead{BlockedReason: staffingReason(err)}, nil
	}
	state := owner.station.GetAssistantProgramState()
	if !state.PluginAvailable {
		return CanonicalStepRead{BlockedReason: ReasonIntegrationDisabled}, nil
	}
	projection, malformed := a.currentProjection(scope, owner)
	if malformed {
		return CanonicalStepRead{BlockedReason: ReasonStaffingNeedsAttention, Staffing: projection}, nil
	}
	homeComplete := projection.Scopes[0].RequiredComplete
	projectComplete := projection.Scopes[1].RequiredComplete
	actions := make([]ActionID, 0, 2)
	if !homeComplete {
		actions = append(actions, ActionReviewHomeStaffing)
	} else {
		actions = append(actions, ActionOpenHomeStaffing)
	}
	if !projectComplete {
		actions = append(actions, ActionReviewProjectStaffing)
	} else {
		actions = append(actions, ActionOpenProjectStaffing)
	}
	return CanonicalStepRead{Complete: homeComplete && projectComplete, AvailableActions: actions, Staffing: projection}, nil
}

func (a *AssistantStaffingAdapter) InputDigest(action ActionID, raw json.RawMessage) (string, error) {
	if !isStaffingAction(action) {
		return "", ErrInvalid
	}
	input, err := decodeStaffingInput(raw)
	if err != nil {
		return "", err
	}
	encoded, _ := json.Marshal(input)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func (a *AssistantStaffingAdapter) Review(_ context.Context, scope ReadScope, action ActionID, raw json.RawMessage) (ActionReviewMaterial, error) {
	commit, targetScope, ok := staffingCommitForReview(action)
	if !ok {
		return ActionReviewMaterial{}, ErrInvalid
	}
	return a.reviewMaterial(scope, commit, targetScope, raw)
}

func (a *AssistantStaffingAdapter) PrepareCommit(_ context.Context, scope ReadScope, action ActionID, raw json.RawMessage) (ActionReviewMaterial, error) {
	targetScope, ok := staffingScopeForCommit(action)
	if !ok {
		return ActionReviewMaterial{}, ErrInvalid
	}
	return a.reviewMaterial(scope, action, targetScope, raw)
}

func (a *AssistantStaffingAdapter) reviewMaterial(scope ReadScope, commit ActionID, targetScope workspace.AssistantRoleScope, raw json.RawMessage) (ActionReviewMaterial, error) {
	if a == nil || a.workspaces == nil || a.profiles == nil {
		return ActionReviewMaterial{}, ErrInvalid
	}
	input, err := decodeStaffingInput(raw)
	if err != nil {
		return ActionReviewMaterial{}, err
	}
	owner, err := a.owner(scope)
	if err != nil {
		return ActionReviewMaterial{}, ErrConflict
	}
	projection, err := a.reviewProjection(scope, owner, targetScope, input)
	if err != nil {
		return ActionReviewMaterial{}, err
	}
	if len(projection.Scopes) != 1 || !projection.Scopes[0].ToolGrantsReady {
		return ActionReviewMaterial{}, ErrConflict
	}
	inputDigest, _ := a.InputDigest(commit, raw)
	ownerBytes, _ := json.Marshal(struct {
		StationRevision int64
		BindingRevision int64
		Scope           workspace.AssistantRoleScope
		WorkspaceID     string
	}{
		StationRevision: owner.station.GetAssistantProgramState().StateRevision,
		BindingRevision: projection.Scopes[0].BindingRevision,
		Scope:           targetScope, WorkspaceID: projection.Scopes[0].WorkspaceID,
	})
	disclosureBytes, _ := json.Marshal(projection)
	ownerDigest := sha256.Sum256(ownerBytes)
	disclosureDigest := sha256.Sum256(disclosureBytes)
	return ActionReviewMaterial{
		CommitAction: commit, InputDigest: inputDigest,
		OwnerRevisionDigest: hex.EncodeToString(ownerDigest[:]),
		DisclosureDigest:    hex.EncodeToString(disclosureDigest[:]),
		Staffing:            projection,
	}, nil
}

func (a *AssistantStaffingAdapter) Commit(_ context.Context, scope ReadScope, action ActionID, raw json.RawMessage, reviewed ActionReviewMaterial) (CanonicalResult, error) {
	targetScope, ok := staffingScopeForCommit(action)
	if !ok || reviewed.CommitAction != action || reviewed.Staffing == nil {
		return CanonicalResult{}, ErrInvalid
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	input, err := decodeStaffingInput(raw)
	if err != nil {
		return CanonicalResult{}, err
	}
	owner, err := a.owner(scope)
	if err != nil {
		return CanonicalResult{}, ErrConflict
	}
	projection, err := a.reviewProjection(scope, owner, targetScope, input)
	if err != nil || !equalStaffingProjection(projection, reviewed.Staffing) {
		return CanonicalResult{}, ErrConflict
	}
	target := owner.station
	if targetScope == workspace.AssistantRoleScopeProject {
		target = owner.project
	}
	rolesByID := make(map[string]workspace.AssistantProgramRoleSpec, len(owner.declaration.Roles))
	for _, role := range owner.declaration.Roles {
		rolesByID[role.ID] = role
	}
	createdNames := make([]string, 0, len(input.Roles))
	granted := make(map[string][]string, len(input.Roles))
	rollback := func() {
		for name, skills := range granted {
			for _, skill := range skills {
				_ = a.grants.Revoke(name, skill)
			}
		}
		for index := len(createdNames) - 1; index >= 0; index-- {
			_ = a.profiles.DeleteAgent(createdNames[index])
		}
	}
	instances := make([]workspace.AgentInstance, 0, len(input.Roles))
	bindings := make([]workspace.AssistantRoleBinding, 0, len(input.Roles))
	for _, requested := range input.Roles {
		role := rolesByID[requested.RoleID]
		profileType := normalizeStaffingAgentType(role.Type)
		if err := a.profiles.CreateAgent(requested.Name, &store.CreateAgentConfig{
			Type: profileType, Role: types.AgentRole(role.Role), Model: requested.Model,
			LLMProvider: requested.Provider, SystemPrompt: role.SystemPrompt,
		}); err != nil {
			rollback()
			return CanonicalResult{}, ErrConflict
		}
		createdNames = append(createdNames, requested.Name)
		profile, found := a.profiles.GetAgent(requested.Name)
		if !found || profile == nil {
			rollback()
			return CanonicalResult{}, ErrConflict
		}
		if err := a.workspaces.SaveWorkspaceAgent(target.ID, requested.Name, profile); err != nil {
			rollback()
			return CanonicalResult{}, ErrConflict
		}
		for _, skill := range role.Skills {
			if a.grants == nil || a.grants.Grant(requested.Name, skill) != nil {
				rollback()
				return CanonicalResult{}, ErrConflict
			}
			granted[requested.Name] = append(granted[requested.Name], skill)
		}
		instance := workspace.AgentInstance{
			ID:   uuid.NewSHA1(uuid.NameSpaceOID, []byte(target.ID+"\x00"+role.ID)).String(),
			Name: requested.Name, InstanceNumber: 1,
			NodeID: "assistant-" + role.ID, Role: role.Label, Description: role.Description,
			EntryPoint: role.Primary, CreatedAt: time.Now().UTC(),
		}
		instances = append(instances, instance)
		bindings = append(bindings, workspace.AssistantRoleBinding{RoleID: role.ID, AgentInstanceID: instance.ID, AgentName: instance.Name})
	}
	expectedRevision := projection.Scopes[0].BindingRevision
	err = a.workspaces.Update(target.ID, func(current *workspace.Workspace) error {
		for _, existing := range current.GetAgentInstances() {
			for _, instance := range instances {
				if existing.ID == instance.ID || strings.EqualFold(existing.Name, instance.Name) {
					return workspace.ErrAssistantBindingInvalid
				}
			}
		}
		current.AgentInstances = append(current.GetAgentInstances(), instances...)
		if targetScope == workspace.AssistantRoleScopeHome {
			state := current.GetAssistantProgramState()
			if state == nil || state.HomeBindings.StateRevision != expectedRevision {
				return workspace.ErrAssistantBindingVersionConflict
			}
			state.HomeBindings = workspace.AssistantRoleBindingSet{StateRevision: expectedRevision + 1, Bindings: append(state.HomeBindings.Bindings, bindings...)}
			if !state.Hired {
				now := time.Now().UTC()
				state.Hired = true
				state.HiredAt = &now
				state.StageID = state.Declaration.Stages[0].ID
				state.Level = 1
				state.StageEnteredAt = map[string]time.Time{state.StageID: now}
			}
			for _, role := range input.Roles {
				if rolesByID[role.RoleID].Primary {
					state.PrimaryName = role.Name
					if err := current.SetEntryAgentName(role.Name); err != nil {
						return err
					}
				}
			}
			current.SetAssistantProgramState(state)
		} else {
			link := current.GetAssistantProjectLink()
			if link == nil || link.ProjectBindings.StateRevision != expectedRevision || link.StationWorkspaceID != owner.station.ID {
				return workspace.ErrAssistantBindingVersionConflict
			}
			link.ProjectBindings = workspace.AssistantRoleBindingSet{StateRevision: expectedRevision + 1, Bindings: append(link.ProjectBindings.Bindings, bindings...)}
			for _, role := range input.Roles {
				if rolesByID[role.RoleID].Primary {
					if err := current.SetEntryAgentName(role.Name); err != nil {
						return err
					}
				}
			}
			current.SetAssistantProjectLink(link)
		}
		return nil
	})
	if err != nil {
		rollback()
		return CanonicalResult{}, ErrConflict
	}
	return CanonicalResult{}, nil
}

func (a *AssistantStaffingAdapter) ConsequenceObserved(action ActionID, read CanonicalStepRead) bool {
	if read.Staffing == nil {
		return false
	}
	scope, ok := staffingScopeForCommit(action)
	if !ok {
		return false
	}
	for _, current := range read.Staffing.Scopes {
		if current.Scope == scope {
			return current.RequiredComplete
		}
	}
	return false
}

func (a *AssistantStaffingAdapter) owner(scope ReadScope) (*staffingOwner, error) {
	if a == nil || a.workspaces == nil || scope.HomeWorkspaceID == "" || scope.ProjectWorkspaceID == "" {
		return nil, workspace.ErrAssistantProgramUnavailable
	}
	station, err := a.workspaces.Get(scope.HomeWorkspaceID)
	if err != nil || station == nil {
		return nil, workspace.ErrAssistantStationNotFound
	}
	state := station.GetAssistantProgramState()
	if state == nil || state.SchemaVersion < workspace.AssistantProgramStateSchemaVersion || state.Declaration == nil ||
		state.Key.OwnerUserID != scope.OwnerUserID || state.Key.ProgramID != scope.ExpectedAssistantProgramID {
		return nil, workspace.ErrAssistantProgramUnavailable
	}
	project, err := a.workspaces.Get(scope.ProjectWorkspaceID)
	if err != nil || project == nil {
		return nil, workspace.ErrAssistantProgramUnavailable
	}
	link := project.GetAssistantProjectLink()
	if link == nil || link.SchemaVersion < workspace.AssistantProjectLinkSchemaVersion || link.StationWorkspaceID != station.ID || link.Key.Normalize() != state.Key.Normalize() {
		return nil, workspace.ErrAssistantProgramVersionConflict
	}
	return &staffingOwner{station: station, project: project, declaration: state.Declaration}, nil
}

func (a *AssistantStaffingAdapter) currentProjection(scope ReadScope, owner *staffingOwner) (*StaffingProjection, bool) {
	state := owner.station.GetAssistantProgramState()
	home, homeMalformed := a.scopeProjection(owner.station, owner.declaration, workspace.AssistantRoleScopeHome, state.HomeBindings, "")
	link := owner.project.GetAssistantProjectLink()
	project, projectMalformed := a.scopeProjection(owner.project, owner.declaration, workspace.AssistantRoleScopeProject, link.ProjectBindings, scope.SelectedModeID)
	return &StaffingProjection{Scopes: []StaffingScopeProjection{home, project}}, homeMalformed || projectMalformed
}

func (a *AssistantStaffingAdapter) scopeProjection(target *workspace.Workspace, declaration *workspace.AssistantProgramDeclaration, roleScope workspace.AssistantRoleScope, set workspace.AssistantRoleBindingSet, modeID string) (StaffingScopeProjection, bool) {
	projection := StaffingScopeProjection{
		Scope: roleScope, WorkspaceID: target.ID, WorkspaceLabel: target.Name,
		BindingRevision: set.StateRevision, RuntimeReady: true, ModelsReady: true,
		ToolGrantsReady: true, SelectedModeID: modeID,
	}
	if roleScope == workspace.AssistantRoleScopeHome {
		projection.AuthorityBoundary = "Home roles receive no linked-child folders, MCP bindings, runtime grants, project entries, prompts, memories, task history, or live state."
	} else {
		projection.RuntimeReady = strings.TrimSpace(modeID) != ""
		projection.AuthorityBoundary = "Project roles are bound only to this exact linked child and receive no Home or sibling state."
	}
	bindings := make(map[string]workspace.AssistantRoleBinding, len(set.Bindings))
	malformed := false
	for _, binding := range set.Bindings {
		if _, exists := bindings[binding.RoleID]; exists {
			malformed = true
		}
		bindings[binding.RoleID] = binding
	}
	instances := make(map[string]workspace.AgentInstance, len(target.GetAgentInstances()))
	for _, instance := range target.GetAgentInstances() {
		instances[instance.ID] = instance
	}
	projection.RequiredComplete = true
	for _, role := range declaration.Roles {
		if role.Scope != roleScope {
			continue
		}
		item := StaffingRoleProjection{RoleID: role.ID, Label: role.Label, Responsibility: role.Description, Required: role.Required, Primary: role.Primary, ToolGrants: append([]string(nil), role.Skills...)}
		if binding, found := bindings[role.ID]; found {
			instance, instanceFound := instances[binding.AgentInstanceID]
			profile, snapshotFound, snapshotErr := a.workspaces.GetWorkspaceAgent(target.ID, binding.AgentName)
			if !instanceFound || instance.Name != binding.AgentName || snapshotErr != nil || !snapshotFound || profile == nil {
				malformed = true
			} else {
				item.Configured = true
				item.ProfileName = binding.AgentName
				item.Provider = profile.Settings.Provider
				item.Model = profile.Settings.Model
				item.UsesDefaults = item.Provider == "" && item.Model == ""
				item.ChatAvailable = staffingModelAvailable(a.validate, item.Provider, item.Model)
				if role.Required && !item.ChatAvailable {
					projection.ModelsReady = false
				}
			}
		}
		if role.Required && !item.Configured {
			projection.RequiredComplete = false
			projection.ModelsReady = false
		}
		projection.Roles = append(projection.Roles, item)
	}
	return projection, malformed
}

func (a *AssistantStaffingAdapter) reviewProjection(scope ReadScope, owner *staffingOwner, targetScope workspace.AssistantRoleScope, input staffingInput) (*StaffingProjection, error) {
	current, malformed := a.currentProjection(scope, owner)
	if malformed {
		return nil, ErrConflict
	}
	var target StaffingScopeProjection
	for _, candidate := range current.Scopes {
		if candidate.Scope == targetScope {
			target = candidate
			break
		}
	}
	target.ModelsReady = true
	target.ToolGrantsReady = true
	missing := make(map[string]workspace.AssistantProgramRoleSpec)
	for _, role := range owner.declaration.Roles {
		if role.Scope != targetScope || !role.Required {
			continue
		}
		configured := false
		for _, item := range target.Roles {
			if item.RoleID == role.ID {
				configured = item.Configured
				break
			}
		}
		if !configured {
			missing[role.ID] = role
		}
	}
	if len(missing) == 0 || len(input.Roles) != len(missing) {
		return nil, ErrConflict
	}
	seenNames := make(map[string]struct{}, len(input.Roles))
	planned := make([]StaffingRoleProjection, 0, len(input.Roles))
	for index := range input.Roles {
		requested := &input.Roles[index]
		role, ok := missing[requested.RoleID]
		if !ok {
			return nil, ErrInvalid
		}
		delete(missing, requested.RoleID)
		nameKey := strings.ToLower(requested.Name)
		if _, duplicate := seenNames[nameKey]; duplicate || profileNameExists(a.profiles, requested.Name) || workspaceNameExists(owner.station, requested.Name) || workspaceNameExists(owner.project, requested.Name) {
			return nil, ErrConflict
		}
		seenNames[nameKey] = struct{}{}
		explicitModel := requested.Provider != "" || requested.Model != ""
		if requested.Provider == "" && requested.Model == "" && a.defaults != nil {
			requested.Provider, requested.Model = a.defaults()
		}
		if requested.Provider == "" && requested.Model != "" {
			return nil, ErrInvalid
		}
		chatAvailable := staffingModelAvailable(a.validate, requested.Provider, requested.Model)
		if explicitModel && !chatAvailable {
			return nil, ErrInvalid
		}
		if !chatAvailable {
			target.ModelsReady = false
		}
		for _, skill := range role.Skills {
			if a.grants == nil || !a.grants.Available(skill) {
				target.ToolGrantsReady = false
			}
		}
		planned = append(planned, StaffingRoleProjection{
			RoleID: role.ID, Label: role.Label, Responsibility: role.Description, Required: true, Primary: role.Primary,
			ProfileName: requested.Name, Provider: requested.Provider, Model: requested.Model,
			UsesDefaults: requested.Provider == "" && requested.Model == "", ChatAvailable: chatAvailable,
			ToolGrants: append([]string(nil), role.Skills...), Configured: false,
		})
	}
	// Preserve declaration order in the review regardless of client input order.
	ordered := make([]StaffingRoleProjection, 0, len(planned))
	for _, role := range owner.declaration.Roles {
		for _, item := range planned {
			if item.RoleID == role.ID {
				ordered = append(ordered, item)
			}
		}
	}
	target.Roles = ordered
	return &StaffingProjection{Scopes: []StaffingScopeProjection{target}}, nil
}

func decodeStaffingInput(raw json.RawMessage) (staffingInput, error) {
	if len(bytes.TrimSpace(raw)) == 0 || len(raw) > MaxActionInputBytes {
		return staffingInput{}, ErrInvalid
	}
	var input staffingInput
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || len(input.Roles) == 0 || len(input.Roles) > maxStaffingRoles {
		return staffingInput{}, ErrInvalid
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return staffingInput{}, ErrInvalid
	}
	seen := make(map[string]struct{}, len(input.Roles))
	for index := range input.Roles {
		role := &input.Roles[index]
		role.RoleID = strings.ToLower(strings.TrimSpace(role.RoleID))
		role.Name = strings.TrimSpace(role.Name)
		role.Provider = strings.ToLower(strings.TrimSpace(role.Provider))
		role.Model = strings.TrimSpace(role.Model)
		if role.RoleID == "" || role.Name == "" || len(role.RoleID) > 80 || len(role.Name) > 80 || len(role.Provider) > 120 || len(role.Model) > 240 || strings.ContainsAny(role.Name, "\r\n\x00") {
			return staffingInput{}, ErrInvalid
		}
		if _, duplicate := seen[role.RoleID]; duplicate {
			return staffingInput{}, ErrInvalid
		}
		seen[role.RoleID] = struct{}{}
	}
	return input, nil
}

func staffingCommitForReview(action ActionID) (ActionID, workspace.AssistantRoleScope, bool) {
	switch action {
	case ActionReviewHomeStaffing:
		return ActionAddHomeStaffing, workspace.AssistantRoleScopeHome, true
	case ActionReviewProjectStaffing:
		return ActionAddProjectStaffing, workspace.AssistantRoleScopeProject, true
	default:
		return "", "", false
	}
}

func staffingScopeForCommit(action ActionID) (workspace.AssistantRoleScope, bool) {
	switch action {
	case ActionAddHomeStaffing:
		return workspace.AssistantRoleScopeHome, true
	case ActionAddProjectStaffing:
		return workspace.AssistantRoleScopeProject, true
	default:
		return "", false
	}
}

func isStaffingAction(action ActionID) bool {
	_, _, review := staffingCommitForReview(action)
	if review {
		return true
	}
	_, commit := staffingScopeForCommit(action)
	return commit
}

func staffingReason(err error) ReasonCode {
	switch {
	case errors.Is(err, workspace.ErrAssistantStationNotFound):
		return ReasonHomeUnavailable
	case errors.Is(err, workspace.ErrAssistantProgramVersionConflict):
		return ReasonAssistantProgramMismatch
	default:
		return ReasonStaffingRequired
	}
}

func profileNameExists(profiles store.Store, name string) bool {
	for _, existing := range profiles.ListAgents() {
		if strings.EqualFold(strings.TrimSpace(existing), name) {
			return true
		}
	}
	return false
}

func workspaceNameExists(target *workspace.Workspace, name string) bool {
	for _, existing := range target.GetAgentInstances() {
		if strings.EqualFold(strings.TrimSpace(existing.Name), name) {
			return true
		}
	}
	return false
}

func staffingModelAvailable(validate StaffingModelValidator, provider, model string) bool {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if provider == "" && model == "" {
		return false
	}
	if provider == "" {
		return false
	}
	return validate == nil || validate(provider, model) == nil
}

func normalizeStaffingAgentType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "general":
		return agent.TypeGeneral
	case "research":
		return agent.TypeResearch
	default:
		return agent.TypeToolCalling
	}
}

func equalStaffingProjection(left, right *StaffingProjection) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}

func cloneStaffingProjection(source *StaffingProjection) *StaffingProjection {
	if source == nil {
		return nil
	}
	clone := &StaffingProjection{Scopes: make([]StaffingScopeProjection, len(source.Scopes))}
	copy(clone.Scopes, source.Scopes)
	for index := range clone.Scopes {
		clone.Scopes[index].Roles = append([]StaffingRoleProjection(nil), source.Scopes[index].Roles...)
		for roleIndex := range clone.Scopes[index].Roles {
			clone.Scopes[index].Roles[roleIndex].ToolGrants = append([]string(nil), source.Scopes[index].Roles[roleIndex].ToolGrants...)
		}
	}
	return clone
}

func validStaffingProjection(projection *StaffingProjection) bool {
	if projection == nil {
		return true
	}
	if len(projection.Scopes) == 0 || len(projection.Scopes) > 2 {
		return false
	}
	seenScopes := make(map[workspace.AssistantRoleScope]struct{}, len(projection.Scopes))
	for _, current := range projection.Scopes {
		if (current.Scope != workspace.AssistantRoleScopeHome && current.Scope != workspace.AssistantRoleScopeProject) ||
			strings.TrimSpace(current.WorkspaceID) == "" || len(current.WorkspaceID) > 160 || len(current.WorkspaceLabel) > 160 ||
			current.BindingRevision < 0 || len(current.Roles) > maxStaffingRoles || len(current.SelectedModeID) > 80 || len(current.AuthorityBoundary) > 600 {
			return false
		}
		if _, duplicate := seenScopes[current.Scope]; duplicate {
			return false
		}
		seenScopes[current.Scope] = struct{}{}
		seenRoles := make(map[string]struct{}, len(current.Roles))
		for _, role := range current.Roles {
			if strings.TrimSpace(role.RoleID) == "" || len(role.RoleID) > 80 || len(role.Label) > 160 || len(role.Responsibility) > 1000 || len(role.ProfileName) > 80 || len(role.Provider) > 120 || len(role.Model) > 240 || len(role.ToolGrants) > 16 {
				return false
			}
			if _, duplicate := seenRoles[role.RoleID]; duplicate {
				return false
			}
			seenRoles[role.RoleID] = struct{}{}
			for _, grant := range role.ToolGrants {
				if strings.TrimSpace(grant) == "" || len(grant) > 160 {
					return false
				}
			}
		}
	}
	return true
}

var _ JourneyActionAdapter = (*AssistantStaffingAdapter)(nil)
var _ CanonicalReader = (*AssistantStaffingAdapter)(nil)
