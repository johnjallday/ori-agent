// Package agents coordinates a feature-scoped primary coding agent without
// taking ownership of Git worktree creation or removal.
package agents

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/config"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/herdr"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/model"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/worktree"
)

var featurePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,80}$`)
var actionableTaskPattern = regexp.MustCompile(`^\d+\.[1-9]\d*\s+`)

type Herdr interface {
	OpenExistingWorktree(context.Context, string, string) (herdr.WorktreeOpenResult, error)
	PaneProcessInfo(context.Context, string) (herdr.PaneProcessInfo, error)
	PaneSplitInfo(context.Context, string, string, string) (herdr.PaneInfo, error)
	AgentListInfo(context.Context) ([]herdr.AgentInfo, error)
	AgentGetInfo(context.Context, string) (herdr.AgentInfo, error)
	AgentStartInfo(context.Context, string, string, string, time.Duration) (herdr.AgentInfo, error)
	AgentPromptInfo(context.Context, string, string, time.Duration) (herdr.AgentInfo, error)
	AgentRenameInfo(context.Context, string, string) (herdr.AgentInfo, error)
	FocusAgent(context.Context, string) error
	AgentReadText(context.Context, string, int) (string, error)
	ReportWorkspaceMetadata(context.Context, string, string, map[string]string) (json.RawMessage, error)
	ReportPaneMetadata(context.Context, string, string, map[string]string) (json.RawMessage, error)
}

type StateStore interface {
	Load() (model.BridgeState, error)
	Save(model.BridgeState) error
}

// LockingStore is an optional extension implemented by the durable file
// store. A handoff holds it across Herdr calls so concurrent `wt herd retry`
// processes cannot race into duplicate primary-agent starts or prompts.
type LockingStore interface {
	Lock(context.Context) (func(), error)
}

type Inspector interface {
	Inspect(context.Context, string, string, string) (worktree.GitWorktree, error)
}

type inspectorFunc func(context.Context, string, string, string) (worktree.GitWorktree, error)

func (f inspectorFunc) Inspect(ctx context.Context, path, branch, commonDir string) (worktree.GitWorktree, error) {
	return f(ctx, path, branch, commonDir)
}

type Service struct {
	Config       config.Config
	RepositoryID string
	GitCommonDir string
	Client       Herdr
	Store        StateStore
	Inspector    Inspector
	Now          func() time.Time
}

type HandoffRequest struct {
	FeatureName  string
	WorktreePath string
	Branch       string
	Resend       bool
}

type HandoffResult struct {
	Feature         model.Feature   `json:"feature"`
	WorkspaceID     string          `json:"workspace_id"`
	RootPaneID      string          `json:"root_pane_id"`
	Primary         model.RoleAgent `json:"primary"`
	PromptDelivered bool            `json:"prompt_delivered"`
	PromptSkipped   bool            `json:"prompt_skipped"`
}

func (s *Service) Handoff(ctx context.Context, request HandoffRequest) (HandoffResult, error) {
	if locker, ok := s.Store.(LockingStore); ok {
		unlock, err := locker.Lock(ctx)
		if err != nil {
			return HandoffResult{}, &model.StageError{Stage: "handoff lock", Code: model.ErrStateCorrupt, Message: "could not acquire the local handoff lock", Recovery: "wait for the other bridge operation to finish, then run wt herd retry", Cause: err}
		}
		defer unlock()
	}
	return s.handoff(ctx, request)
}

func (s *Service) handoff(ctx context.Context, request HandoffRequest) (HandoffResult, error) {
	if s.Client == nil || s.Store == nil {
		return HandoffResult{}, &model.StageError{Stage: "handoff", Code: model.ErrHerdrUnavailable, Message: "bridge handoff dependencies are unavailable", Recovery: "wt herd doctor"}
	}
	if !featurePattern.MatchString(request.FeatureName) {
		return HandoffResult{}, &model.StageError{Stage: "handoff", Code: model.ErrWorktreeInvalid, Message: "feature name is invalid", Recovery: "run wt start <feature> with a PRD-derived feature name"}
	}
	if request.WorktreePath == "" {
		return HandoffResult{}, &model.StageError{Stage: "handoff", Code: model.ErrWorktreeInvalid, Message: "worktree path is required", Recovery: "run wt herd retry from the feature worktree"}
	}
	inspector := s.Inspector
	if inspector == nil {
		inspector = inspectorFunc(worktree.InspectLinkedGitWorktree)
	}
	gitWorktree, err := inspector.Inspect(ctx, request.WorktreePath, request.Branch, s.GitCommonDir)
	if err != nil {
		return HandoffResult{}, &model.StageError{Stage: "validate worktree", Code: model.ErrWorktreeInvalid, Message: "target is not the expected linked Git worktree", Recovery: "run wt herd doctor from the feature worktree", Cause: err}
	}

	feature := model.Feature{
		RepositoryID: s.RepositoryID,
		Name:         request.FeatureName,
		Branch:       gitWorktree.Branch,
		Path:         gitWorktree.Path,
	}
	featureKey := feature.RepositoryID + ":" + feature.Name
	state, err := s.Store.Load()
	if err != nil {
		return HandoffResult{}, &model.StageError{Stage: "handoff state", Code: model.ErrStateCorrupt, Message: "could not load the local feature handoff state", Recovery: "wt herd doctor", Cause: err}
	}
	featureState, found := state.Features[featureKey]
	if found && featureState.Feature.Path != "" && !samePath(featureState.Feature.Path, feature.Path) {
		return HandoffResult{}, &model.StageError{Stage: "handoff state", Code: model.ErrWorktreeInvalid, Message: "the managed feature name is already bound to another worktree", Recovery: "choose a different feature name or inspect wt herd status"}
	}
	if featureState.Agents == nil {
		featureState.Agents = make(map[string]model.RoleAgent)
	}
	if featureState.Schedules == nil {
		featureState.Schedules = make(map[string]model.Schedule)
	}
	featureState.Feature = feature
	// The installed plugin can outlive this checkout, so persist the
	// configured display-metadata source on this feature record. State is
	// shared across repositories, therefore this must not be a global value.
	featureState.SourceID = s.Config.Bridge.SourceID
	metadataEnabled := s.Config.Metadata.Enabled
	featureState.MetadataEnabled = &metadataEnabled
	if featureState.Handoff.Stage == "" {
		featureState.Handoff = model.HandoffState{Stage: model.HandoffRecorded, PrimaryRole: s.Config.Primary.Role, UpdatedAt: s.now()}
	} else if featureState.Handoff.PrimaryRole == "" {
		featureState.Handoff.PrimaryRole = s.Config.Primary.Role
	}
	featureState.UpdatedAt = s.now()
	state.Features[featureKey] = featureState
	if err := s.Store.Save(state); err != nil {
		return HandoffResult{}, &model.StageError{Stage: "record handoff", Code: model.ErrStateCorrupt, Message: "could not persist the feature handoff record before contacting Herdr", Recovery: "check the local bridge state directory, then run wt herd retry", Cause: err}
	}

	if gitWorktree.SourcePath == "" {
		return HandoffResult{}, &model.StageError{Stage: "resolve source checkout", Code: model.ErrWorktreeInvalid, Message: "could not resolve the repository source checkout required by Herdr", Recovery: "wt herd doctor"}
	}
	opened, err := s.Client.OpenExistingWorktree(ctx, gitWorktree.SourcePath, feature.Path)
	if err != nil {
		return HandoffResult{}, wrapHerdrError("open existing worktree", err, "wt herd retry")
	}
	if opened.Worktree.Path != "" && !samePath(opened.Worktree.Path, feature.Path) {
		return HandoffResult{}, &model.StageError{Stage: "open existing worktree", Code: model.ErrWorktreeInvalid, Message: "Herdr opened a different worktree path", Recovery: "wt herd doctor"}
	}
	if opened.RootPane.WorkspaceID != "" && opened.RootPane.WorkspaceID != opened.Workspace.WorkspaceID {
		return HandoffResult{}, &model.StageError{Stage: "resolve root pane", Code: model.ErrHerdrUnavailable, Message: "Herdr returned a root pane from a different workspace", Recovery: "wt herd retry"}
	}
	if err := s.validateRootPane(opened.RootPane, feature.Path); err != nil {
		return HandoffResult{}, err
	}

	featureState = state.Features[featureKey]
	featureState.WorkspaceID = opened.Workspace.WorkspaceID
	if !featureState.Handoff.BootstrapPrompted {
		featureState.Handoff.Stage = model.HandoffWorkspaceOpened
	}
	featureState.Handoff.RootPaneID = opened.RootPane.PaneID
	featureState.Handoff.UpdatedAt = s.now()
	featureState.UpdatedAt = s.now()
	state.Features[featureKey] = featureState
	if err := s.Store.Save(state); err != nil {
		return HandoffResult{}, stateSaveError(err)
	}

	// Metadata is display-only and source-scoped. It never changes Herdr's
	// semantic agent lifecycle authority.
	metadata := map[string]string{
		"repository":  feature.RepositoryID,
		"feature":     feature.Name,
		"branch":      feature.Branch,
		"path":        feature.Path,
		"ori_devflow": "managed",
	}
	if metadataEnabledFor(featureState) {
		if _, err := s.Client.ReportWorkspaceMetadata(ctx, opened.Workspace.WorkspaceID, s.Config.Bridge.SourceID, metadata); err != nil {
			return HandoffResult{}, wrapHerdrError("report workspace metadata", err, "wt herd retry")
		}
	}

	primaryRole := featureState.Handoff.PrimaryRole
	name, err := ScopedAgentName(feature.RepositoryID, feature.Name, primaryRole)
	if err != nil {
		return HandoffResult{}, &model.StageError{Stage: "primary agent name", Code: model.ErrConfigInvalid, Message: "could not create a safe primary agent name", Recovery: "check the feature and role names", Cause: err}
	}
	primary, _, err := s.ensurePrimary(ctx, &state, featureKey, featureState, opened, name, primaryRole, s.Config.Primary.Kind)
	if err != nil {
		return HandoffResult{}, err
	}
	featureState = state.Features[featureKey]
	if metadataEnabledFor(featureState) {
		if _, err := s.Client.ReportPaneMetadata(ctx, primary.PaneID, s.Config.Bridge.SourceID, map[string]string{
			"feature": feature.Name,
			"role":    primary.Role,
			"branch":  feature.Branch,
		}); err != nil {
			return HandoffResult{}, wrapHerdrError("report agent metadata", err, "wt herd retry")
		}
	}

	result := HandoffResult{
		Feature:     feature,
		WorkspaceID: opened.Workspace.WorkspaceID,
		RootPaneID:  opened.RootPane.PaneID,
		Primary:     primary,
	}
	if featureState.Handoff.BootstrapPrompted && !request.Resend {
		result.PromptSkipped = true
		return result, nil
	}
	prompt := BootstrapPrompt(feature, primaryRole)
	prompted, err := s.Client.AgentPromptInfo(ctx, primary.Name, prompt, time.Duration(s.Config.Bootstrap.TimeoutSeconds)*time.Second)
	if err != nil {
		return HandoffResult{}, wrapHerdrError("deliver bootstrap prompt", err, "wt herd retry; use --resend only when you intentionally want a second prompt")
	}
	if err := validatePromptAcknowledgement(prompted, primary); err != nil {
		return HandoffResult{}, err
	}
	featureState = state.Features[featureKey]
	featureState.Handoff.Stage = model.HandoffPrompted
	featureState.Handoff.BootstrapPrompted = true
	featureState.Handoff.UpdatedAt = s.now()
	featureState.UpdatedAt = s.now()
	state.Features[featureKey] = featureState
	if err := s.Store.Save(state); err != nil {
		return HandoffResult{}, stateSaveError(err)
	}
	result.PromptDelivered = true
	return result, nil
}

func (s *Service) ensurePrimary(ctx context.Context, state *model.BridgeState, featureKey string, featureState model.FeatureState, opened herdr.WorktreeOpenResult, expectedName, role, kind string) (model.RoleAgent, bool, error) {
	if saved, ok := featureState.Agents[role]; ok {
		if saved.Name != expectedName {
			return model.RoleAgent{}, true, &model.StageError{Stage: "resolve primary agent", Code: model.ErrAgentAmbiguous, Message: "saved primary agent name does not match this feature's identity", Recovery: "wt herd rebind " + role + " --target <live-target>"}
		}
		live, err := s.Client.AgentGetInfo(ctx, saved.Name)
		if err != nil {
			return model.RoleAgent{}, true, wrapHerdrError("resolve saved primary agent", err, "wt herd rebind "+role+" --target <live-target>")
		}
		primary, err := s.validateLivePrimary(live, opened, expectedName, role, kind)
		if err != nil {
			return model.RoleAgent{}, true, err
		}
		primary.Role = role
		primary.Kind = saved.Kind
		if primary.Kind == "" {
			primary.Kind = kind
		}
		featureState.Agents[role] = primary
		if !featureState.Handoff.BootstrapPrompted {
			featureState.Handoff.Stage = model.HandoffReady
		}
		featureState.Handoff.PrimaryAgentName = expectedName
		featureState.Handoff.UpdatedAt = s.now()
		featureState.UpdatedAt = s.now()
		state.Features[featureKey] = featureState
		if err := s.Store.Save(*state); err != nil {
			return model.RoleAgent{}, true, stateSaveError(err)
		}
		return primary, true, nil
	}

	liveAgents, err := s.Client.AgentListInfo(ctx)
	if err != nil {
		return model.RoleAgent{}, false, wrapHerdrError("list existing agents", err, "wt herd retry")
	}
	for _, live := range liveAgents {
		if live.Name != expectedName {
			continue
		}
		primary, err := s.validateLivePrimary(live, opened, expectedName, role, kind)
		if err != nil {
			return model.RoleAgent{}, false, err
		}
		primary.Role = role
		primary.Kind = kind
		featureState.Agents[role] = primary
		if !featureState.Handoff.BootstrapPrompted {
			featureState.Handoff.Stage = model.HandoffReady
		}
		featureState.Handoff.PrimaryAgentName = expectedName
		featureState.Handoff.UpdatedAt = s.now()
		featureState.UpdatedAt = s.now()
		state.Features[featureKey] = featureState
		if err := s.Store.Save(*state); err != nil {
			return model.RoleAgent{}, false, stateSaveError(err)
		}
		return primary, true, nil
	}

	// A saved or already-live primary owns this pane and may legitimately be a
	// foreground coding-agent process. Only a new launch needs an idle shell.
	if err := s.validateRootShell(ctx, opened.RootPane, featureState.Feature.Path); err != nil {
		return model.RoleAgent{}, false, err
	}

	started, err := s.Client.AgentStartInfo(ctx, expectedName, kind, opened.RootPane.PaneID, time.Duration(s.Config.Bootstrap.TimeoutSeconds)*time.Second)
	if err != nil {
		return model.RoleAgent{}, false, wrapHerdrError("start primary agent", err, "wt herd retry")
	}
	ready, err := s.waitForReady(ctx, expectedName, started)
	if err != nil {
		return model.RoleAgent{}, false, err
	}
	primary, err := s.validateLivePrimary(ready, opened, expectedName, role, kind)
	if err != nil {
		return model.RoleAgent{}, false, err
	}
	primary.Role = role
	primary.Kind = kind
	featureState.Agents[role] = primary
	featureState.Handoff.Stage = model.HandoffPrimaryStarted
	featureState.Handoff.PrimaryAgentName = expectedName
	featureState.Handoff.UpdatedAt = s.now()
	featureState.UpdatedAt = s.now()
	state.Features[featureKey] = featureState
	if err := s.Store.Save(*state); err != nil {
		return model.RoleAgent{}, false, stateSaveError(err)
	}
	return primary, false, nil
}

func (s *Service) validateRootShell(ctx context.Context, pane herdr.PaneInfo, worktreePath string) error {
	if err := s.validateRootPane(pane, worktreePath); err != nil {
		return err
	}
	process, err := s.Client.PaneProcessInfo(ctx, pane.PaneID)
	if err != nil {
		return wrapHerdrError("inspect root pane", err, "wt herd retry")
	}
	if process.PaneID != pane.PaneID || process.ShellPID == nil {
		return &model.StageError{Stage: "resolve root pane", Code: model.ErrHerdrUnavailable, Message: "Herdr root pane has no interactive shell identity", Recovery: "wt herd retry"}
	}
	for _, foreground := range process.ForegroundProcesses {
		if foreground.PID != *process.ShellPID {
			return &model.StageError{Stage: "resolve root pane", Code: model.ErrHerdrUnavailable, Message: "Herdr root pane is busy with a non-shell foreground process", Recovery: "wait for the shell, then run wt herd retry"}
		}
		if foreground.Cwd != "" && !samePath(foreground.Cwd, worktreePath) {
			return &model.StageError{Stage: "resolve root pane", Code: model.ErrWorktreeInvalid, Message: "Herdr root shell is not in the feature worktree", Recovery: "wt herd retry"}
		}
	}
	return nil
}

// validateRootPane checks the stable, structured identity Herdr returned for
// the feature workspace. Unlike validateRootShell it remains valid after a
// coding agent has taken over the pane, so retries can resume a saved primary
// without trying to launch another conversation.
func (s *Service) validateRootPane(pane herdr.PaneInfo, worktreePath string) error {
	if pane.PaneID == "" || pane.TerminalID == "" {
		return &model.StageError{Stage: "resolve root pane", Code: model.ErrHerdrUnavailable, Message: "Herdr did not return a live root shell pane", Recovery: "wt herd retry"}
	}
	for _, cwd := range []string{pane.Cwd, pane.ForegroundCwd} {
		if cwd != "" && !samePath(cwd, worktreePath) {
			return &model.StageError{Stage: "resolve root pane", Code: model.ErrWorktreeInvalid, Message: "Herdr root pane is not in the feature worktree", Recovery: "wt herd retry"}
		}
	}
	return nil
}

func (s *Service) waitForReady(ctx context.Context, name string, started herdr.AgentInfo) (herdr.AgentInfo, error) {
	deadline := s.now().Add(time.Duration(s.Config.Bootstrap.TimeoutSeconds) * time.Second)
	current := started
	for {
		if current.InteractiveReady && !current.LaunchPending {
			return current, nil
		}
		if !s.now().Before(deadline) {
			return herdr.AgentInfo{}, &model.StageError{Stage: "wait for primary agent", Code: model.ErrHerdrUnavailable, Message: "Herdr did not report the primary agent ready before the timeout", Recovery: "wt herd retry"}
		}
		select {
		case <-ctx.Done():
			return herdr.AgentInfo{}, &model.StageError{Stage: "wait for primary agent", Code: model.ErrHerdrUnavailable, Message: "waiting for the primary agent was canceled", Recovery: "wt herd retry", Cause: ctx.Err()}
		case <-time.After(250 * time.Millisecond):
		}
		var err error
		current, err = s.Client.AgentGetInfo(ctx, name)
		if err != nil {
			return herdr.AgentInfo{}, wrapHerdrError("wait for primary agent", err, "wt herd retry")
		}
	}
}

func (s *Service) validateLivePrimary(live herdr.AgentInfo, opened herdr.WorktreeOpenResult, expectedName, role, kind string) (model.RoleAgent, error) {
	if live.Name != expectedName {
		return model.RoleAgent{}, &model.StageError{Stage: "resolve primary agent", Code: model.ErrAgentAmbiguous, Message: "Herdr returned a different agent than the managed primary", Recovery: "wt herd status"}
	}
	if live.WorkspaceID != opened.Workspace.WorkspaceID || live.PaneID != opened.RootPane.PaneID || live.TerminalID == "" {
		return model.RoleAgent{}, &model.StageError{Stage: "resolve primary agent", Code: model.ErrAgentAmbiguous, Message: "the named primary agent belongs to a different Herdr workspace or pane", Recovery: "wt herd rebind " + role + " --target <live-target>"}
	}
	if live.Agent != "" && live.Agent != kind {
		return model.RoleAgent{}, &model.StageError{Stage: "resolve primary agent", Code: model.ErrAgentAmbiguous, Message: "the named primary agent has a different configured kind", Recovery: "wt herd rebind " + role + " --target <live-target>"}
	}
	if !live.InteractiveReady || live.LaunchPending {
		return model.RoleAgent{}, &model.StageError{Stage: "resolve primary agent", Code: model.ErrHerdrUnavailable, Message: "the primary agent is not ready for a prompt", Recovery: "wt herd retry"}
	}
	roleAgent := model.RoleAgent{
		Name:        live.Name,
		WorkspaceID: live.WorkspaceID,
		TabID:       live.TabID,
		PaneID:      live.PaneID,
		TerminalID:  live.TerminalID,
		Status:      live.AgentStatus,
		UpdatedAt:   s.now(),
	}
	if live.AgentSession != nil {
		roleAgent.NativeSession = *live.AgentSession
	}
	return roleAgent, nil
}

// validatePromptAcknowledgement treats a structured prompt response as an
// acknowledgement for the exact saved agent only. Some Herdr versions omit
// redundant identity fields in a successful acknowledgement, so empty fields
// remain acceptable; supplied fields must match the selected native target.
func validatePromptAcknowledgement(ack herdr.AgentInfo, primary model.RoleAgent) error {
	if ack.Name != "" && ack.Name != primary.Name {
		return &model.StageError{Stage: "deliver bootstrap prompt", Code: model.ErrAgentAmbiguous, Message: "Herdr acknowledged a prompt for a different agent", Recovery: "wt herd status; do not resend until the target is verified"}
	}
	if ack.WorkspaceID != "" && ack.WorkspaceID != primary.WorkspaceID {
		return &model.StageError{Stage: "deliver bootstrap prompt", Code: model.ErrAgentAmbiguous, Message: "Herdr acknowledged a prompt in a different workspace", Recovery: "wt herd status; do not resend until the target is verified"}
	}
	if ack.PaneID != "" && ack.PaneID != primary.PaneID {
		return &model.StageError{Stage: "deliver bootstrap prompt", Code: model.ErrAgentAmbiguous, Message: "Herdr acknowledged a prompt in a different pane", Recovery: "wt herd status; do not resend until the target is verified"}
	}
	if ack.TerminalID != "" && ack.TerminalID != primary.TerminalID {
		return &model.StageError{Stage: "deliver bootstrap prompt", Code: model.ErrAgentAmbiguous, Message: "Herdr acknowledged a prompt for a different terminal", Recovery: "wt herd status; do not resend until the target is verified"}
	}
	return nil
}

// metadataEnabledFor treats records created before this setting was persisted
// as enabled, matching the original opt-in bridge default.
func metadataEnabledFor(feature model.FeatureState) bool {
	return feature.MetadataEnabled == nil || *feature.MetadataEnabled
}

// ScopedAgentName produces a globally unique, Herdr-valid name without
// trusting a human role alias as a global target. The digest keeps collisions
// distinct even when the readable parts are truncated to Herdr's 32-byte cap.
func ScopedAgentName(repositoryID, feature, role string) (string, error) {
	if !featurePattern.MatchString(feature) || !rolePattern.MatchString(role) || repositoryID == "" {
		return "", errors.New("invalid repository, feature, or role identity")
	}
	raw := strings.Join([]string{repositoryID, feature, role}, ":")
	clean := func(value string) string {
		value = strings.ToLower(value)
		var builder strings.Builder
		lastDash := false
		for _, r := range value {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
				builder.WriteRune(r)
				lastDash = false
			} else if !lastDash {
				builder.WriteByte('-')
				lastDash = true
			}
		}
		return strings.Trim(builder.String(), "-")
	}
	repoPart, featurePart, rolePart := clean(repositoryID), clean(feature), clean(role)
	if repoPart == "" || featurePart == "" || rolePart == "" {
		return "", errors.New("identity contains no usable name characters")
	}
	base := "ori-" + repoPart + "-" + featurePart + "-" + rolePart
	if len(base) <= 32 {
		return base, nil
	}
	sum := sha256.Sum256([]byte(raw))
	hash := hex.EncodeToString(sum[:])[:6]
	// `ori-` plus three separators and the six-byte digest reserve 12 bytes,
	// leaving 20 readable bytes within Herdr's 32-byte name cap.
	shortRepo := truncate(repoPart, 7)
	shortFeature := truncate(featurePart, 7)
	shortRole := truncate(rolePart, 5)
	name := "ori-" + shortRepo + "-" + shortFeature + "-" + shortRole + "-" + hash
	if !regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`).MatchString(name) {
		return "", errors.New("generated name does not satisfy Herdr target syntax")
	}
	return name, nil
}

func BootstrapPrompt(feature model.Feature, role string) string {
	prdPath := filepath.Join(feature.Path, "tasks", "prd-"+feature.Name+".md")
	taskPath := filepath.Join(feature.Path, "tasks", "tasks-"+feature.Name+".md")
	nextTask := nextIncompleteTask(taskPath)
	agentsPath := filepath.Join(feature.Path, "AGENTS.md")
	agentsInstruction := "Read and follow any applicable AGENTS.md instructions in this worktree before editing."
	if _, err := os.Stat(agentsPath); err == nil {
		agentsInstruction = "Read and follow: " + agentsPath
	}
	lines := []string{
		"You are the primary " + role + " for Ori feature " + feature.Name + ".",
		"Work only in this Git worktree: " + feature.Path,
		agentsInstruction,
		"PRD: " + prdPath,
		"Task checklist: " + taskPath,
		"Next incomplete checklist item: " + nextTask,
		"Begin working on that task. As each sub-task is completed, update the checklist from [ ] to [x].",
		"Do not create or remove Git worktrees. When the feature is ready, use the existing wt pr workflow; after merge, use wt done " + feature.Name + ".",
	}
	return strings.Join(lines, "\n")
}

// ContinuationPrompt is the conservative default for a one-time scheduled
// delivery. It names only local planning paths and never embeds task file
// contents in the persisted prompt.
func ContinuationPrompt(feature model.Feature, role string) string {
	prdPath := filepath.Join(feature.Path, "tasks", "prd-"+feature.Name+".md")
	taskPath := filepath.Join(feature.Path, "tasks", "tasks-"+feature.Name+".md")
	nextTask := nextIncompleteTask(taskPath)
	agentsPath := filepath.Join(feature.Path, "AGENTS.md")
	agentsInstruction := "Read and follow any applicable AGENTS.md instructions in this worktree before editing."
	if _, err := os.Stat(agentsPath); err == nil {
		agentsInstruction = "Read and follow: " + agentsPath
	}
	lines := []string{
		"This is a scheduled continuation for the managed " + role + " role on Ori feature " + feature.Name + ".",
		"Work only in this Git worktree: " + feature.Path,
		agentsInstruction,
		"Re-read the planning artifacts before making changes.",
		"PRD: " + prdPath,
		"Task checklist: " + taskPath,
		"Next incomplete checklist item: " + nextTask,
		"Continue safely from that task. Update the checklist from [ ] to [x] only after completing each sub-task.",
		"Do not create or remove Git worktrees. Use the existing wt pr and wt done " + feature.Name + " lifecycle when the feature is ready.",
	}
	return strings.Join(lines, "\n")
}

func nextIncompleteTask(path string) string {
	contents, err := os.ReadFile(path)
	if err != nil {
		return "No detailed task list was found; inspect the PRD and create or follow the next safe implementation step."
	}
	for _, line := range strings.Split(string(contents), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- [ ] ") {
			value := strings.TrimSpace(strings.TrimPrefix(trimmed, "- [ ] "))
			if !actionableTaskPattern.MatchString(value) {
				continue
			}
			if len(value) > 240 {
				value = value[:240] + "…"
			}
			return value
		}
	}
	return "All checklist items are marked complete; verify the feature before opening its PR."
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func wrapHerdrError(stage string, err error, recovery string) error {
	var stageErr *model.StageError
	if errors.As(err, &stageErr) {
		if stageErr.Recovery == "" {
			stageErr.Recovery = recovery
		}
		return stageErr
	}
	return &model.StageError{Stage: stage, Code: model.ErrHerdrUnavailable, Message: "Herdr did not complete the requested operation", Recovery: recovery, Cause: err}
}

func stateSaveError(err error) error {
	return &model.StageError{Stage: "handoff state", Code: model.ErrStateCorrupt, Message: "could not persist handoff progress", Recovery: "check local state permissions, then run wt herd retry", Cause: err}
}

func samePath(left, right string) bool {
	leftCanonical, leftErr := filepath.EvalSymlinks(left)
	rightCanonical, rightErr := filepath.EvalSymlinks(right)
	if leftErr == nil {
		left = leftCanonical
	}
	if rightErr == nil {
		right = rightCanonical
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
