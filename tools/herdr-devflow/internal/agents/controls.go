package agents

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/config"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/herdr"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/model"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/worktree"
)

var rolePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
var lowLevelTargetPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,120}$`)

// ContextRequest selects a managed feature. Commands default to the current
// linked worktree, but callers can select a saved feature explicitly when
// operating from another worktree in the same repository.
type ContextRequest struct {
	FeatureName  string
	WorktreePath string
}

type AddRequest struct {
	Context ContextRequest
	Role    string
	Kind    string
}

type AddResult struct {
	Feature model.Feature   `json:"feature"`
	Agent   model.RoleAgent `json:"agent"`
	Reused  bool            `json:"reused"`
}

type PromptRequest struct {
	Context ContextRequest
	Role    string
	Target  string
	Text    string
}

type PromptResult struct {
	Feature model.Feature   `json:"feature"`
	Agent   model.RoleAgent `json:"agent"`
}

type RenameRequest struct {
	Context ContextRequest
	Role    string
	NewRole string
}

type RenameResult struct {
	Feature model.Feature   `json:"feature"`
	Agent   model.RoleAgent `json:"agent"`
}

type TargetRequest struct {
	Context ContextRequest
	Role    string
	Target  string
}

type ReadResult struct {
	Feature model.Feature   `json:"feature"`
	Agent   model.RoleAgent `json:"agent"`
	Text    string          `json:"text"`
}

type RebindRequest struct {
	Context ContextRequest
	Role    string
	Target  string
}

type RebindResult struct {
	Feature model.Feature   `json:"feature"`
	Agent   model.RoleAgent `json:"agent"`
}

// ScheduleTargetResult is the exact saved role/session identity a one-time
// continuation may target. It deliberately has no low-level target override:
// schedules bind only to a reconciled managed role, never a loose label.
type ScheduleTargetResult struct {
	Feature model.Feature   `json:"feature"`
	Agent   model.RoleAgent `json:"agent"`
}

type resolvedFeature struct {
	key         string
	bridgeState model.BridgeState
	feature     model.FeatureState
	worktree    worktree.GitWorktree
	primaryRole string
}

func (s *Service) lock(ctx context.Context) (func(), error) {
	if locker, ok := s.Store.(LockingStore); ok {
		return locker.Lock(ctx)
	}
	return func() {}, nil
}

func (s *Service) resolveFeature(ctx context.Context, request ContextRequest) (resolvedFeature, error) {
	if s.Client == nil {
		return resolvedFeature{}, &model.StageError{Stage: "feature context", Code: model.ErrHerdrUnavailable, Message: "the Herdr bridge client is unavailable", Recovery: "wt herd doctor"}
	}
	if s.Store == nil {
		return resolvedFeature{}, &model.StageError{Stage: "feature context", Code: model.ErrStateCorrupt, Message: "the local bridge state store is unavailable", Recovery: "wt herd doctor"}
	}
	if request.FeatureName != "" && !featurePattern.MatchString(request.FeatureName) {
		return resolvedFeature{}, &model.StageError{Stage: "feature context", Code: model.ErrWorktreeInvalid, Message: "feature name is invalid", Recovery: "pass a feature created by wt start"}
	}
	bridgeState, err := s.Store.Load()
	if err != nil {
		return resolvedFeature{}, &model.StageError{Stage: "feature context", Code: model.ErrStateCorrupt, Message: "could not load the local bridge state", Recovery: "wt herd doctor", Cause: err}
	}

	candidatePath := request.WorktreePath
	if candidatePath == "" && request.FeatureName != "" {
		key := s.RepositoryID + ":" + request.FeatureName
		if saved, ok := bridgeState.Features[key]; ok {
			candidatePath = saved.Feature.Path
		}
	}
	if candidatePath == "" {
		return resolvedFeature{}, &model.StageError{Stage: "feature context", Code: model.ErrWorktreeInvalid, Message: "a managed feature worktree is required", Recovery: "run this command inside the feature worktree or pass --feature <name>"}
	}
	inspector := s.Inspector
	if inspector == nil {
		inspector = inspectorFunc(worktree.InspectLinkedGitWorktree)
	}
	linked, err := inspector.Inspect(ctx, candidatePath, "", s.GitCommonDir)
	if err != nil {
		return resolvedFeature{}, &model.StageError{Stage: "feature context", Code: model.ErrWorktreeInvalid, Message: "the selected path is not this repository's linked Git worktree", Recovery: "run wt herd doctor from the feature worktree", Cause: err}
	}

	var matches []string
	for key, featureState := range bridgeState.Features {
		if featureState.Feature.RepositoryID != s.RepositoryID {
			continue
		}
		if request.FeatureName != "" && featureState.Feature.Name != request.FeatureName {
			continue
		}
		if samePath(featureState.Feature.Path, linked.Path) {
			matches = append(matches, key)
		}
	}
	if len(matches) == 0 {
		return resolvedFeature{}, &model.StageError{Stage: "feature context", Code: model.ErrWorktreeInvalid, Message: "this linked worktree has no managed Herdr feature handoff", Recovery: "run wt herd retry after wt start, or pass --feature <managed-feature>"}
	}
	if len(matches) > 1 {
		return resolvedFeature{}, &model.StageError{Stage: "feature context", Code: model.ErrAgentAmbiguous, Message: "multiple managed features resolve to this worktree", Recovery: "pass --feature <name> explicitly"}
	}
	key := matches[0]
	featureState := bridgeState.Features[key]
	if request.FeatureName != "" && featureState.Feature.Name != request.FeatureName {
		return resolvedFeature{}, &model.StageError{Stage: "feature context", Code: model.ErrWorktreeInvalid, Message: "the selected feature does not own this worktree", Recovery: "check wt herd status and pass the matching --feature"}
	}
	if featureState.WorkspaceID == "" {
		return resolvedFeature{}, &model.StageError{Stage: "feature context", Code: model.ErrHerdrUnavailable, Message: "the feature has no saved Herdr workspace", Recovery: "wt herd retry --feature " + featureState.Feature.Name}
	}
	if featureState.Agents == nil {
		featureState.Agents = make(map[string]model.RoleAgent)
	}
	if featureState.Schedules == nil {
		featureState.Schedules = make(map[string]model.Schedule)
	}
	featureState.Feature.Path = linked.Path
	featureState.Feature.Branch = linked.Branch
	featureState.Feature.RepositoryID = s.RepositoryID
	primaryRole := featureState.Handoff.PrimaryRole
	if primaryRole == "" {
		primaryRole = s.Config.Primary.Role
		featureState.Handoff.PrimaryRole = primaryRole
	}
	return resolvedFeature{key: key, bridgeState: bridgeState, feature: featureState, worktree: linked, primaryRole: primaryRole}, nil
}

func (s *Service) saveResolved(resolved *resolvedFeature) error {
	resolved.feature.UpdatedAt = s.now()
	resolved.bridgeState.Features[resolved.key] = resolved.feature
	if err := s.Store.Save(resolved.bridgeState); err != nil {
		return stateSaveError(err)
	}
	return nil
}

func (s *Service) Add(ctx context.Context, request AddRequest) (AddResult, error) {
	if err := validateRole(request.Role); err != nil {
		return AddResult{}, err
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return AddResult{}, handoffLockError(err)
	}
	defer unlock()
	resolved, err := s.resolveFeature(ctx, request.Context)
	if err != nil {
		return AddResult{}, err
	}
	if request.Role == resolved.primaryRole {
		return AddResult{}, &model.StageError{Stage: "add role agent", Code: model.ErrAgentAmbiguous, Message: "the feature's primary role already exists; use wt herd retry to recover it", Recovery: "wt herd retry --feature " + resolved.feature.Feature.Name}
	}
	kind := request.Kind
	if kind == "" {
		kind = s.Config.RoleKind(request.Role)
	}
	if !config.IsSupportedAgentKind(kind) {
		return AddResult{}, &model.StageError{Stage: "add role agent", Code: model.ErrConfigInvalid, Message: "the requested agent kind is not supported by Herdr", Recovery: "choose a supported --kind or update .herdr/devflow.toml"}
	}

	if saved, exists := resolved.feature.Agents[request.Role]; exists {
		if saved.PaneID != "" && saved.TerminalID != "" {
			agent, err := s.resolveSavedRole(ctx, &resolved, request.Role)
			if err != nil {
				return AddResult{}, err
			}
			return AddResult{Feature: resolved.feature.Feature, Agent: agent, Reused: true}, nil
		}
		if saved.Kind != "" && saved.Kind != kind {
			return AddResult{}, &model.StageError{Stage: "add role agent", Code: model.ErrAgentAmbiguous, Message: "a partial role launch already uses a different kind", Recovery: "wt herd rebind " + request.Role + " --target <live-target>"}
		}
	} else {
		name, nameErr := ScopedAgentName(resolved.feature.Feature.RepositoryID, resolved.feature.Feature.Name, request.Role)
		if nameErr != nil {
			return AddResult{}, &model.StageError{Stage: "add role agent", Code: model.ErrConfigInvalid, Message: "could not create a safe role-agent name", Recovery: "choose a short lower-case role", Cause: nameErr}
		}
		resolved.feature.Agents[request.Role] = model.RoleAgent{
			Role:        request.Role,
			Name:        name,
			Kind:        kind,
			WorkspaceID: resolved.feature.WorkspaceID,
			Status:      model.AgentUnknown,
			UpdatedAt:   s.now(),
		}
		if err := s.saveResolved(&resolved); err != nil {
			return AddResult{}, err
		}
	}

	return s.resumeAdd(ctx, &resolved, request.Role, kind)
}

func (s *Service) resumeAdd(ctx context.Context, resolved *resolvedFeature, role, kind string) (AddResult, error) {
	saved := resolved.feature.Agents[role]
	if saved.Name == "" {
		return AddResult{}, &model.StageError{Stage: "add role agent", Code: model.ErrStateCorrupt, Message: "the saved role launch has no unique Herdr name", Recovery: "wt herd rebind " + role + " --target <live-target>"}
	}
	if live, err := s.Client.AgentGetInfo(ctx, saved.Name); err == nil {
		if saved.PaneID == "" {
			return AddResult{}, &model.StageError{Stage: "add role agent", Code: model.ErrAgentAmbiguous, Message: "the generated role-agent name is already live without a recorded feature pane", Recovery: "wt herd rebind " + role + " --target " + saved.Name}
		}
		agent, validateErr := s.roleAgentFromLive(live, resolved, role, kind)
		if validateErr != nil {
			return AddResult{}, validateErr
		}
		if saved.PaneID != "" && agent.PaneID != saved.PaneID {
			return AddResult{}, &model.StageError{Stage: "add role agent", Code: model.ErrAgentAmbiguous, Message: "the unique role-agent name is already bound to another pane", Recovery: "wt herd rebind " + role + " --target " + saved.Name}
		}
		resolved.feature.Agents[role] = agent
		if err := s.saveResolved(resolved); err != nil {
			return AddResult{}, err
		}
		return AddResult{Feature: resolved.feature.Feature, Agent: agent, Reused: true}, nil
	} else if !isMissingAgent(err) {
		return AddResult{}, wrapHerdrError("resolve partial role agent", err, "wt herd retry --feature "+resolved.feature.Feature.Name)
	}

	if saved.PaneID == "" {
		primary, err := s.resolveSavedRole(ctx, resolved, resolved.primaryRole)
		if err != nil {
			return AddResult{}, err
		}
		pane, err := s.Client.PaneSplitInfo(ctx, primary.PaneID, "right", resolved.feature.Feature.Path)
		if err != nil {
			return AddResult{}, wrapHerdrError("split role pane", err, "wt herd add "+role+" --feature "+resolved.feature.Feature.Name)
		}
		if pane.WorkspaceID != "" && pane.WorkspaceID != resolved.feature.WorkspaceID {
			return AddResult{}, &model.StageError{Stage: "split role pane", Code: model.ErrAgentAmbiguous, Message: "Herdr created the role pane in a different workspace", Recovery: "wt herd retry --feature " + resolved.feature.Feature.Name}
		}
		if err := s.validateInteractiveShell(ctx, pane, resolved.feature.Feature.Path, "resolve role pane"); err != nil {
			return AddResult{}, err
		}
		saved.PaneID = pane.PaneID
		saved.TerminalID = pane.TerminalID
		saved.TabID = pane.TabID
		saved.WorkspaceID = resolved.feature.WorkspaceID
		saved.Status = model.AgentUnknown
		saved.UpdatedAt = s.now()
		resolved.feature.Agents[role] = saved
		if err := s.saveResolved(resolved); err != nil {
			return AddResult{}, err
		}
	}

	pane := herdr.PaneInfo{PaneID: saved.PaneID, TerminalID: saved.TerminalID, WorkspaceID: resolved.feature.WorkspaceID, TabID: saved.TabID, Cwd: resolved.feature.Feature.Path, ForegroundCwd: resolved.feature.Feature.Path}
	if err := s.validateInteractiveShell(ctx, pane, resolved.feature.Feature.Path, "resolve role pane"); err != nil {
		return AddResult{}, err
	}
	started, err := s.Client.AgentStartInfo(ctx, saved.Name, kind, saved.PaneID, time.Duration(s.Config.Bootstrap.TimeoutSeconds)*time.Second)
	if err != nil {
		return AddResult{}, wrapHerdrError("start role agent", err, "wt herd add "+role+" --feature "+resolved.feature.Feature.Name)
	}
	ready, err := s.waitForReady(ctx, saved.Name, started)
	if err != nil {
		return AddResult{}, err
	}
	agent, err := s.roleAgentFromLive(ready, resolved, role, kind)
	if err != nil {
		return AddResult{}, err
	}
	if agent.PaneID != saved.PaneID {
		return AddResult{}, &model.StageError{Stage: "start role agent", Code: model.ErrAgentAmbiguous, Message: "Herdr started the role agent in a different pane", Recovery: "wt herd rebind " + role + " --target " + saved.Name}
	}
	resolved.feature.Agents[role] = agent
	if err := s.saveResolved(resolved); err != nil {
		return AddResult{}, err
	}
	if _, err := s.Client.ReportPaneMetadata(ctx, agent.PaneID, s.Config.Bridge.SourceID, map[string]string{
		"feature": resolved.feature.Feature.Name,
		"role":    agent.Role,
		"branch":  resolved.feature.Feature.Branch,
	}); err != nil {
		return AddResult{}, wrapHerdrError("report role metadata", err, "wt herd retry --feature "+resolved.feature.Feature.Name)
	}
	return AddResult{Feature: resolved.feature.Feature, Agent: agent}, nil
}

func (s *Service) Prompt(ctx context.Context, request PromptRequest) (PromptResult, error) {
	if strings.TrimSpace(request.Text) == "" {
		return PromptResult{}, &model.StageError{Stage: "prompt", Code: model.ErrConfigInvalid, Message: "prompt text is required", Recovery: "wt herd prompt [role] <text>"}
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return PromptResult{}, handoffLockError(err)
	}
	defer unlock()
	resolved, err := s.resolveFeature(ctx, request.Context)
	if err != nil {
		return PromptResult{}, err
	}
	agent, err := s.resolveTarget(ctx, &resolved, request.Role, request.Target)
	if err != nil {
		return PromptResult{}, err
	}
	ack, err := s.Client.AgentPromptInfo(ctx, agent.Name, request.Text, time.Duration(s.Config.Bootstrap.TimeoutSeconds)*time.Second)
	if err != nil {
		return PromptResult{}, wrapHerdrError("deliver role prompt", err, "wt herd rebind "+agent.Role+" --target <live-target>")
	}
	if err := validatePromptAcknowledgement(ack, agent); err != nil {
		return PromptResult{}, err
	}
	if request.Target == "" {
		if updated, ok := resolved.feature.Agents[agent.Role]; ok {
			updated.Status = ack.AgentStatus
			updated.UpdatedAt = s.now()
			resolved.feature.Agents[agent.Role] = updated
			if err := s.saveResolved(&resolved); err != nil {
				return PromptResult{}, err
			}
		}
	}
	return PromptResult{Feature: resolved.feature.Feature, Agent: agent}, nil
}

func (s *Service) Rename(ctx context.Context, request RenameRequest) (RenameResult, error) {
	if err := validateRole(request.Role); err != nil {
		return RenameResult{}, err
	}
	if err := validateRole(request.NewRole); err != nil {
		return RenameResult{}, err
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return RenameResult{}, handoffLockError(err)
	}
	defer unlock()
	resolved, err := s.resolveFeature(ctx, request.Context)
	if err != nil {
		return RenameResult{}, err
	}
	if request.Role == request.NewRole {
		agent, resolveErr := s.resolveSavedRole(ctx, &resolved, request.Role)
		if resolveErr != nil {
			return RenameResult{}, resolveErr
		}
		return RenameResult{Feature: resolved.feature.Feature, Agent: agent}, nil
	}
	if _, exists := resolved.feature.Agents[request.NewRole]; exists {
		return RenameResult{}, &model.StageError{Stage: "rename role agent", Code: model.ErrAgentAmbiguous, Message: "the new role name is already assigned in this feature", Recovery: "choose a different role name"}
	}
	agent, err := s.resolveSavedRole(ctx, &resolved, request.Role)
	if err != nil {
		return RenameResult{}, err
	}
	newName, err := ScopedAgentName(resolved.feature.Feature.RepositoryID, resolved.feature.Feature.Name, request.NewRole)
	if err != nil {
		return RenameResult{}, &model.StageError{Stage: "rename role agent", Code: model.ErrConfigInvalid, Message: "could not create a safe new Herdr name", Recovery: "choose a shorter role name", Cause: err}
	}
	if agent.Name != newName {
		renamed, renameErr := s.Client.AgentRenameInfo(ctx, agent.Name, newName)
		if renameErr != nil {
			return RenameResult{}, wrapHerdrError("rename role agent", renameErr, "wt herd rebind "+request.Role+" --target <live-target>")
		}
		if renamed.AgentSession == nil && agent.NativeSession.Value != "" {
			native := agent.NativeSession
			renamed.AgentSession = &native
		}
		agent, err = s.roleAgentFromLive(renamed, &resolved, request.NewRole, agent.Kind)
		if err != nil {
			return RenameResult{}, err
		}
	} else {
		agent.Role = request.NewRole
	}
	delete(resolved.feature.Agents, request.Role)
	resolved.feature.Agents[request.NewRole] = agent
	if request.Role == resolved.primaryRole {
		resolved.primaryRole = request.NewRole
		resolved.feature.Handoff.PrimaryRole = request.NewRole
		resolved.feature.Handoff.PrimaryAgentName = agent.Name
	}
	for id, schedule := range resolved.feature.Schedules {
		if schedule.Role == request.Role {
			schedule.Role = request.NewRole
			schedule.UpdatedAt = s.now()
			resolved.feature.Schedules[id] = schedule
		}
	}
	if err := s.saveResolved(&resolved); err != nil {
		return RenameResult{}, err
	}
	if _, err := s.Client.ReportPaneMetadata(ctx, agent.PaneID, s.Config.Bridge.SourceID, map[string]string{
		"feature": resolved.feature.Feature.Name,
		"role":    agent.Role,
		"branch":  resolved.feature.Feature.Branch,
	}); err != nil {
		return RenameResult{}, wrapHerdrError("report role metadata", err, "wt herd retry --feature "+resolved.feature.Feature.Name)
	}
	return RenameResult{Feature: resolved.feature.Feature, Agent: agent}, nil
}

func (s *Service) Focus(ctx context.Context, request TargetRequest) (model.RoleAgent, model.Feature, error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return model.RoleAgent{}, model.Feature{}, handoffLockError(err)
	}
	defer unlock()
	resolved, err := s.resolveFeature(ctx, request.Context)
	if err != nil {
		return model.RoleAgent{}, model.Feature{}, err
	}
	agent, err := s.resolveTarget(ctx, &resolved, request.Role, request.Target)
	if err != nil {
		return model.RoleAgent{}, model.Feature{}, err
	}
	if err := s.Client.FocusAgent(ctx, agent.Name); err != nil {
		return model.RoleAgent{}, model.Feature{}, wrapHerdrError("focus role agent", err, "wt herd rebind "+agent.Role+" --target <live-target>")
	}
	return agent, resolved.feature.Feature, nil
}

func (s *Service) Read(ctx context.Context, request TargetRequest, lines int) (ReadResult, error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return ReadResult{}, handoffLockError(err)
	}
	defer unlock()
	resolved, err := s.resolveFeature(ctx, request.Context)
	if err != nil {
		return ReadResult{}, err
	}
	agent, err := s.resolveTarget(ctx, &resolved, request.Role, request.Target)
	if err != nil {
		return ReadResult{}, err
	}
	text, err := s.Client.AgentReadText(ctx, agent.Name, lines)
	if err != nil {
		return ReadResult{}, wrapHerdrError("read role agent", err, "wt herd rebind "+agent.Role+" --target <live-target>")
	}
	return ReadResult{Feature: resolved.feature.Feature, Agent: agent, Text: text}, nil
}

// ResolveScheduleTarget reconciles a managed role before a schedule is
// written. It never starts or rebinds a replacement conversation; a missing
// target remains an actionable error from resolveSavedRole.
func (s *Service) ResolveScheduleTarget(ctx context.Context, request ContextRequest, role string) (ScheduleTargetResult, error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return ScheduleTargetResult{}, handoffLockError(err)
	}
	defer unlock()
	resolved, err := s.resolveFeature(ctx, request)
	if err != nil {
		return ScheduleTargetResult{}, err
	}
	agent, err := s.resolveTarget(ctx, &resolved, role, "")
	if err != nil {
		return ScheduleTargetResult{}, err
	}
	return ScheduleTargetResult{Feature: resolved.feature.Feature, Agent: agent}, nil
}

// ResolveScheduleFeature resolves only the managed feature context. Read-only
// schedule inspection remains available when Herdr is temporarily offline.
func (s *Service) ResolveScheduleFeature(ctx context.Context, request ContextRequest) (model.Feature, error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return model.Feature{}, handoffLockError(err)
	}
	defer unlock()
	resolved, err := s.resolveFeature(ctx, request)
	if err != nil {
		return model.Feature{}, err
	}
	return resolved.feature.Feature, nil
}

func (s *Service) Rebind(ctx context.Context, request RebindRequest) (RebindResult, error) {
	if err := validateRole(request.Role); err != nil {
		return RebindResult{}, err
	}
	if !lowLevelTargetPattern.MatchString(request.Target) {
		return RebindResult{}, &model.StageError{Stage: "rebind role agent", Code: model.ErrConfigInvalid, Message: "a safe explicit Herdr target is required", Recovery: "wt herd rebind <role> --target <agent-name-or-pane-id>"}
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return RebindResult{}, handoffLockError(err)
	}
	defer unlock()
	resolved, err := s.resolveFeature(ctx, request.Context)
	if err != nil {
		return RebindResult{}, err
	}
	saved, exists := resolved.feature.Agents[request.Role]
	if !exists {
		return RebindResult{}, s.roleMissingError(resolved, request.Role)
	}
	live, err := s.Client.AgentGetInfo(ctx, request.Target)
	if err != nil {
		return RebindResult{}, wrapHerdrError("resolve rebind target", err, "herdr agent list; then wt herd rebind "+request.Role+" --target <live-target>")
	}
	if err := s.validateLiveForFeature(live, &resolved, saved.Kind); err != nil {
		return RebindResult{}, err
	}
	desiredName, err := ScopedAgentName(resolved.feature.Feature.RepositoryID, resolved.feature.Feature.Name, request.Role)
	if err != nil {
		return RebindResult{}, &model.StageError{Stage: "rebind role agent", Code: model.ErrConfigInvalid, Message: "could not create a safe role-agent name", Recovery: "choose a short lower-case role", Cause: err}
	}
	if live.Name != desiredName {
		priorNative := live.AgentSession
		live, err = s.Client.AgentRenameInfo(ctx, request.Target, desiredName)
		if err != nil {
			return RebindResult{}, wrapHerdrError("rename rebind target", err, "choose a different target with herdr agent list")
		}
		if live.AgentSession == nil {
			live.AgentSession = priorNative
		}
	}
	agent, err := s.roleAgentFromLive(live, &resolved, request.Role, saved.Kind)
	if err != nil {
		return RebindResult{}, err
	}
	resolved.feature.Agents[request.Role] = agent
	if request.Role == resolved.primaryRole {
		resolved.feature.Handoff.PrimaryAgentName = agent.Name
	}
	if err := s.saveResolved(&resolved); err != nil {
		return RebindResult{}, err
	}
	return RebindResult{Feature: resolved.feature.Feature, Agent: agent}, nil
}

func (s *Service) resolveTarget(ctx context.Context, resolved *resolvedFeature, role, target string) (model.RoleAgent, error) {
	if role == "" {
		role = resolved.primaryRole
	}
	if err := validateRole(role); err != nil {
		return model.RoleAgent{}, err
	}
	if target == "" {
		return s.resolveSavedRole(ctx, resolved, role)
	}
	if !lowLevelTargetPattern.MatchString(target) {
		return model.RoleAgent{}, &model.StageError{Stage: "resolve role target", Code: model.ErrConfigInvalid, Message: "the explicit Herdr target contains unsupported characters", Recovery: "use an agent name or pane id from herdr agent list"}
	}
	live, err := s.Client.AgentGetInfo(ctx, target)
	if err != nil {
		return model.RoleAgent{}, wrapHerdrError("resolve explicit role target", err, "herdr agent list; then retry with --target <live-target>")
	}
	kind := s.Config.RoleKind(role)
	if saved, ok := resolved.feature.Agents[role]; ok && saved.Kind != "" {
		kind = saved.Kind
	}
	return s.roleAgentFromLive(live, resolved, role, kind)
}

func (s *Service) resolveSavedRole(ctx context.Context, resolved *resolvedFeature, role string) (model.RoleAgent, error) {
	if err := validateRole(role); err != nil {
		return model.RoleAgent{}, err
	}
	saved, exists := resolved.feature.Agents[role]
	if !exists || saved.Name == "" {
		return model.RoleAgent{}, s.roleMissingError(*resolved, role)
	}
	// A native session is the strongest identity Herdr exposes. Resolve it
	// before name/pane routing so a restored session can safely move after a
	// server restart without becoming a new conversation.
	live, foundNative, err := s.findByNativeSession(ctx, saved)
	if err != nil {
		return model.RoleAgent{}, err
	}
	if !foundNative {
		live, err = s.Client.AgentGetInfo(ctx, saved.Name)
		if err != nil && !isMissingAgent(err) {
			return model.RoleAgent{}, wrapHerdrError("resolve saved role agent", err, "wt herd rebind "+role+" --target <live-target>")
		}
		if err != nil {
			return model.RoleAgent{}, &model.StageError{Stage: "resolve saved role agent", Code: model.ErrAgentMissing, Message: "the saved role agent is no longer live", Recovery: "wt herd rebind " + role + " --target <live-target>; do not start a replacement automatically"}
		}
	}
	if err := s.validateLiveForFeature(live, resolved, saved.Kind); err != nil {
		return model.RoleAgent{}, err
	}
	if !sameSavedIdentity(saved, live) {
		return model.RoleAgent{}, &model.StageError{Stage: "resolve saved role agent", Code: model.ErrAgentAmbiguous, Message: "the saved role identity no longer matches the live Herdr agent", Recovery: "wt herd rebind " + role + " --target <live-target>"}
	}
	agent, err := s.roleAgentFromLive(live, resolved, role, saved.Kind)
	if err != nil {
		return model.RoleAgent{}, err
	}
	if agent.Name != saved.Name || agent.PaneID != saved.PaneID || agent.TerminalID != saved.TerminalID || !sameNativeSession(agent.NativeSession, saved.NativeSession) {
		resolved.feature.Agents[role] = agent
		if role == resolved.primaryRole {
			resolved.feature.Handoff.PrimaryAgentName = agent.Name
		}
		if err := s.saveResolved(resolved); err != nil {
			return model.RoleAgent{}, err
		}
	}
	return agent, nil
}

func (s *Service) findByNativeSession(ctx context.Context, saved model.RoleAgent) (herdr.AgentInfo, bool, error) {
	if saved.NativeSession.Value == "" {
		return herdr.AgentInfo{}, false, nil
	}
	liveAgents, err := s.Client.AgentListInfo(ctx)
	if err != nil {
		return herdr.AgentInfo{}, false, wrapHerdrError("restore native role session", err, "wt herd doctor")
	}
	var matches []herdr.AgentInfo
	for _, candidate := range liveAgents {
		if candidate.AgentSession != nil && sameNativeSession(saved.NativeSession, *candidate.AgentSession) {
			matches = append(matches, candidate)
		}
	}
	if len(matches) == 0 {
		return herdr.AgentInfo{}, false, nil
	}
	if len(matches) > 1 {
		return herdr.AgentInfo{}, false, &model.StageError{Stage: "restore native role session", Code: model.ErrAgentAmbiguous, Message: "multiple live Herdr agents match the saved native session", Recovery: "wt herd rebind " + saved.Role + " --target <live-target>"}
	}
	if matches[0].Name == "" {
		return herdr.AgentInfo{}, false, &model.StageError{Stage: "restore native role session", Code: model.ErrAgentMissing, Message: "Herdr restored the native session without a managed agent name", Recovery: "wt herd rebind " + saved.Role + " --target " + matches[0].PaneID}
	}
	return matches[0], true, nil
}

func (s *Service) roleAgentFromLive(live herdr.AgentInfo, resolved *resolvedFeature, role, kind string) (model.RoleAgent, error) {
	if err := s.validateLiveForFeature(live, resolved, kind); err != nil {
		return model.RoleAgent{}, err
	}
	if live.Name == "" {
		return model.RoleAgent{}, &model.StageError{Stage: "resolve role agent", Code: model.ErrAgentMissing, Message: "the selected live agent has no stable Herdr name", Recovery: "wt herd rebind " + role + " --target " + live.PaneID}
	}
	agent := model.RoleAgent{
		Role:        role,
		Name:        live.Name,
		Kind:        kind,
		WorkspaceID: live.WorkspaceID,
		TabID:       live.TabID,
		PaneID:      live.PaneID,
		TerminalID:  live.TerminalID,
		Status:      live.AgentStatus,
		UpdatedAt:   s.now(),
	}
	if live.AgentSession != nil {
		agent.NativeSession = *live.AgentSession
	}
	return agent, nil
}

func (s *Service) validateLiveForFeature(live herdr.AgentInfo, resolved *resolvedFeature, kind string) error {
	if live.WorkspaceID != resolved.feature.WorkspaceID || live.PaneID == "" || live.TerminalID == "" {
		return &model.StageError{Stage: "resolve role agent", Code: model.ErrAgentAmbiguous, Message: "the selected live agent does not belong to this feature workspace", Recovery: "use herdr agent list, then wt herd rebind <role> --target <live-target>"}
	}
	if live.Agent != "" && kind != "" && live.Agent != kind {
		return &model.StageError{Stage: "resolve role agent", Code: model.ErrAgentAmbiguous, Message: "the selected live agent has a different configured kind", Recovery: "choose a matching target or role kind"}
	}
	for _, cwd := range []string{live.Cwd, live.ForegroundCwd} {
		if cwd != "" && !samePath(cwd, resolved.feature.Feature.Path) {
			return &model.StageError{Stage: "resolve role agent", Code: model.ErrWorktreeInvalid, Message: "the selected live agent is not in this feature worktree", Recovery: "choose a target from the feature workspace"}
		}
	}
	return nil
}

func (s *Service) validateInteractiveShell(ctx context.Context, pane herdr.PaneInfo, worktreePath, stage string) error {
	if pane.PaneID == "" || pane.TerminalID == "" {
		return &model.StageError{Stage: stage, Code: model.ErrHerdrUnavailable, Message: "Herdr did not return a live shell pane", Recovery: "wt herd retry"}
	}
	for _, cwd := range []string{pane.Cwd, pane.ForegroundCwd} {
		if cwd != "" && !samePath(cwd, worktreePath) {
			return &model.StageError{Stage: stage, Code: model.ErrWorktreeInvalid, Message: "Herdr created a shell outside the feature worktree", Recovery: "wt herd retry"}
		}
	}
	process, err := s.Client.PaneProcessInfo(ctx, pane.PaneID)
	if err != nil {
		return wrapHerdrError(stage, err, "wt herd retry")
	}
	if process.PaneID != pane.PaneID || process.ShellPID == nil {
		return &model.StageError{Stage: stage, Code: model.ErrHerdrUnavailable, Message: "Herdr pane has no interactive shell identity", Recovery: "wt herd retry"}
	}
	for _, foreground := range process.ForegroundProcesses {
		if foreground.PID != *process.ShellPID {
			return &model.StageError{Stage: stage, Code: model.ErrHerdrUnavailable, Message: "Herdr pane is busy with a non-shell foreground process", Recovery: "wait for the shell, then retry"}
		}
		if foreground.Cwd != "" && !samePath(foreground.Cwd, worktreePath) {
			return &model.StageError{Stage: stage, Code: model.ErrWorktreeInvalid, Message: "Herdr shell is not in the feature worktree", Recovery: "wt herd retry"}
		}
	}
	return nil
}

func validateRole(role string) error {
	if rolePattern.MatchString(role) {
		return nil
	}
	return &model.StageError{Stage: "role", Code: model.ErrConfigInvalid, Message: "role must use lower-case letters, digits, underscores, or hyphens and begin with a letter", Recovery: "choose a short role such as reviewer or tester"}
}

func (s *Service) roleMissingError(resolved resolvedFeature, role string) error {
	kind := s.Config.RoleKind(role)
	if saved, ok := resolved.feature.Agents[role]; ok && saved.Kind != "" {
		kind = saved.Kind
	}
	return &model.StageError{Stage: "resolve role agent", Code: model.ErrAgentMissing, Message: fmt.Sprintf("feature %s has no live %s role agent (kind %s, workspace %s)", resolved.feature.Feature.Name, role, kind, resolved.feature.WorkspaceID), Recovery: "wt herd add " + role + " --feature " + resolved.feature.Feature.Name + " or wt herd rebind " + role + " --target <live-target>"}
}

func sameSavedIdentity(saved model.RoleAgent, live herdr.AgentInfo) bool {
	if saved.NativeSession.Value != "" && live.AgentSession != nil && sameNativeSession(saved.NativeSession, *live.AgentSession) {
		return true
	}
	return saved.Name == live.Name && saved.WorkspaceID == live.WorkspaceID && saved.PaneID == live.PaneID && saved.TerminalID == live.TerminalID
}

func sameNativeSession(left, right model.NativeSession) bool {
	if left.Value == "" || right.Value == "" {
		return left.Value == right.Value && left.Source == right.Source && left.Agent == right.Agent && left.Kind == right.Kind
	}
	return left.Source == right.Source && left.Agent == right.Agent && left.Kind == right.Kind && left.Value == right.Value
}

func isMissingAgent(err error) bool {
	var stage *model.StageError
	return errors.As(err, &stage) && stage.Code == model.ErrAgentMissing
}

func handoffLockError(err error) error {
	return &model.StageError{Stage: "bridge lock", Code: model.ErrStateCorrupt, Message: "could not acquire the local bridge operation lock", Recovery: "wait for the other bridge command to finish, then retry", Cause: err}
}
