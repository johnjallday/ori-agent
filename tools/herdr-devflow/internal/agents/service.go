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
	// FocusedWorkspace and TabCreateInfo place a feature. Note the deliberate
	// absence of OpenExistingWorktree: that call mints a workspace per feature,
	// which is exactly the sprawl tab-backed handoff exists to end.
	FocusedWorkspace(context.Context) (herdr.WorkspaceInfo, error)
	TabCreateInfo(context.Context, string, string, string) (herdr.TabCreateResult, error)
	TabGetInfo(context.Context, string) (herdr.TabInfo, error)
	PaneGetInfo(context.Context, string) (herdr.PaneInfo, error)
	PaneProcessInfo(context.Context, string) (herdr.PaneProcessInfo, error)
	PaneSplitInfo(context.Context, string, string, string) (herdr.PaneInfo, error)
	AgentListInfo(context.Context) ([]herdr.AgentInfo, error)
	AgentGetInfo(context.Context, string) (herdr.AgentInfo, error)
	AgentStartInfo(context.Context, string, string, string, time.Duration) (herdr.AgentInfo, error)
	AgentPromptInfo(context.Context, string, string, time.Duration) (herdr.AgentInfo, error)
	AgentRenameInfo(context.Context, string, string) (herdr.AgentInfo, error)
	FocusAgent(context.Context, string) error
	AgentReadText(context.Context, string, int) (string, error)
	// Only pane metadata is written here. A workspace hosts a tab per feature,
	// so workspace-scoped display tokens can no longer describe one feature;
	// status keeps the workspace call for legacy single-feature records.
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
	PrimaryKind  string
	Resend       bool
	// SkipPrompt starts the agent without the bootstrap prompt, for ad-hoc
	// features that have no PRD or task list to be sent to. It is recorded on
	// the feature so retries inherit it.
	SkipPrompt bool
}

type HandoffResult struct {
	Feature        model.Feature `json:"feature"`
	WorkspaceID    string        `json:"workspace_id"`
	WorkspaceLabel string        `json:"workspace_label,omitempty"`
	TabID          string        `json:"tab_id"`
	// TabReused is true when a retry re-entered the feature's existing tab.
	// Tab creation is not idempotent the way `worktree open` was, so this
	// distinguishes "resumed" from "placed" in diagnostics.
	TabReused       bool            `json:"tab_reused,omitempty"`
	RootPaneID      string          `json:"root_pane_id"`
	Primary         model.RoleAgent `json:"primary"`
	PromptDelivered bool            `json:"prompt_delivered"`
	PromptSkipped   bool            `json:"prompt_skipped"`
	// Warnings are non-fatal observations the shell prints. Placing a feature
	// into a workspace bound to another repository is allowed but surprising
	// enough to say out loud.
	Warnings []string `json:"warnings,omitempty"`
}

// featurePlacement is where a feature's primary agent lives in Herdr: one tab
// inside a workspace shared with other features, plus that tab's root pane. It
// carries the same three identities the workspace-open result used to, so the
// validation below stays independent of how the placement was obtained.
type featurePlacement struct {
	WorkspaceID    string
	WorkspaceLabel string
	TabID          string
	RootPane       herdr.PaneInfo
	Reused         bool
	Warnings       []string
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
	primaryRole := featureState.Handoff.PrimaryRole
	if primaryRole == "" {
		primaryRole = s.Config.Primary.Role
	}
	primaryKind := featureState.Handoff.PrimaryKind
	if request.PrimaryKind != "" {
		if primaryKind != "" && primaryKind != request.PrimaryKind {
			return HandoffResult{}, &model.StageError{Stage: "record handoff", Code: model.ErrAgentAmbiguous, Message: "the managed primary kind is already recorded as " + primaryKind, Recovery: "run wt herd retry to preserve the original primary kind"}
		}
		primaryKind = request.PrimaryKind
	}
	if primaryKind == "" {
		if saved, ok := featureState.Agents[primaryRole]; ok && saved.Kind != "" {
			primaryKind = saved.Kind
		} else {
			primaryKind = s.Config.Primary.Kind
		}
	}
	if !config.IsSupportedAgentKind(primaryKind) {
		return HandoffResult{}, &model.StageError{Stage: "primary agent kind", Code: model.ErrConfigInvalid, Message: "the requested primary agent kind is not supported by Herdr", Recovery: "choose a supported --kind or update .herdr/devflow.toml"}
	}
	// The installed plugin can outlive this checkout, so persist the
	// configured display-metadata source on this feature record. State is
	// shared across repositories, therefore this must not be a global value.
	featureState.SourceID = s.Config.Bridge.SourceID
	metadataEnabled := s.Config.Metadata.Enabled
	featureState.MetadataEnabled = &metadataEnabled
	if featureState.Handoff.Stage == "" {
		featureState.Handoff = model.HandoffState{Stage: model.HandoffRecorded, PrimaryRole: primaryRole, PrimaryKind: primaryKind, UpdatedAt: s.now()}
	} else {
		if featureState.Handoff.PrimaryRole == "" {
			featureState.Handoff.PrimaryRole = primaryRole
		}
		if featureState.Handoff.PrimaryKind == "" {
			featureState.Handoff.PrimaryKind = primaryKind
		}
	}
	// Set after the stage initialisation above, which replaces the whole
	// HandoffState for a feature being recorded for the first time and would
	// otherwise discard this. Recorded once and never cleared: a feature that
	// started without planning documents does not acquire them by being retried.
	if request.SkipPrompt {
		featureState.Handoff.SkipBootstrapPrompt = true
	}
	featureState.UpdatedAt = s.now()
	state.Features[featureKey] = featureState
	if err := s.Store.Save(state); err != nil {
		return HandoffResult{}, &model.StageError{Stage: "record handoff", Code: model.ErrStateCorrupt, Message: "could not persist the feature handoff record before contacting Herdr", Recovery: "check the local bridge state directory, then run wt herd retry", Cause: err}
	}

	placement, err := s.resolvePlacement(ctx, featureState, feature, primaryRole, primaryKind, gitWorktree.SourcePath)
	if err != nil {
		return HandoffResult{}, err
	}
	// The worktree-open response used to assert the opened path itself. A tab
	// is told where to start rather than asked, so the pane's own cwd is now
	// the evidence — and it is checked before any agent is launched into it.
	if err := s.validateRootPane(placement.RootPane, feature.Path); err != nil {
		return HandoffResult{}, err
	}

	featureState = state.Features[featureKey]
	featureState.WorkspaceID = placement.WorkspaceID
	featureState.TabID = placement.TabID
	if !featureState.Handoff.BootstrapPrompted {
		featureState.Handoff.Stage = model.HandoffTabCreated
	}
	featureState.Handoff.RootPaneID = placement.RootPane.PaneID
	featureState.Handoff.UpdatedAt = s.now()
	featureState.UpdatedAt = s.now()
	state.Features[featureKey] = featureState
	if err := s.Store.Save(state); err != nil {
		return HandoffResult{}, stateSaveError(err)
	}

	// Metadata is display-only and source-scoped. It never changes Herdr's
	// semantic agent lifecycle authority.
	//
	// It is reported to the feature's own pane, not to the workspace. A
	// workspace now holds a tab per feature, so writing these at workspace
	// scope would mean each handoff overwrote the previous feature's branch and
	// path with its own — the last one to start would be the only one labelled
	// correctly.
	metadata := map[string]string{
		"repository": feature.RepositoryID,
		"feature":    feature.Name,
		"branch":     feature.Branch,
		"path":       feature.Path,
	}
	if metadataEnabledFor(featureState) {
		if _, err := s.Client.ReportPaneMetadata(ctx, placement.RootPane.PaneID, s.Config.Bridge.SourceID, metadata); err != nil {
			return HandoffResult{}, wrapHerdrError("report feature metadata", err, "wt herd retry")
		}
	}

	primaryRole = featureState.Handoff.PrimaryRole
	primaryKind = featureState.Handoff.PrimaryKind
	name, err := ScopedAgentName(feature.RepositoryID, feature.Name, primaryRole)
	if err != nil {
		return HandoffResult{}, &model.StageError{Stage: "primary agent name", Code: model.ErrConfigInvalid, Message: "could not create a safe primary agent name", Recovery: "check the feature and role names", Cause: err}
	}
	primary, _, err := s.ensurePrimary(ctx, &state, featureKey, featureState, placement, name, primaryRole, primaryKind)
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
		Feature:        feature,
		WorkspaceID:    placement.WorkspaceID,
		WorkspaceLabel: placement.WorkspaceLabel,
		TabID:          placement.TabID,
		TabReused:      placement.Reused,
		RootPaneID:     placement.RootPane.PaneID,
		Primary:        primary,
		Warnings:       placement.Warnings,
	}
	if featureState.Handoff.BootstrapPrompted && !request.Resend {
		result.PromptSkipped = true
		return result, nil
	}
	// An ad-hoc feature has no PRD and no checklist, so there is nothing
	// truthful for a bootstrap prompt to point at. The agent is started and
	// left to its user rather than sent a message about documents that do not
	// exist. --resend does not override this: resending nothing is still
	// nothing.
	if featureState.Handoff.SkipBootstrapPrompt {
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

// resolvePlacement returns the tab this feature occupies, creating one only
// when the feature does not already have a live tab. `worktree open` was
// idempotent — reopening an open worktree returned the same workspace — but
// `tab create` is not, so a bare `wt herd retry` that always created would
// leave a dead tab behind on every attempt.
func (s *Service) resolvePlacement(ctx context.Context, featureState model.FeatureState, feature model.Feature, role, kind, sourceCheckout string) (featurePlacement, error) {
	if featureState.TabID != "" && featureState.Handoff.RootPaneID != "" {
		placement, err := s.rehydratePlacement(ctx, featureState, feature)
		switch {
		case err == nil:
			return placement, nil
		case isMissingTarget(err):
			// The user closed the tab by hand. That is a recoverable state, not
			// a failure: fall through and place the feature again.
		default:
			return featurePlacement{}, err
		}
	}

	// An agent already running for this feature owns a pane, and that pane's tab
	// is the feature's placement. Resolving it before creating anything is what
	// keeps a retry — or a legacy workspace-backed feature with no recorded tab —
	// from stranding an empty tab beside the agent it was about to adopt.
	if placement, found, err := s.placementFromLiveAgent(ctx, featureState, feature, role, kind); err != nil {
		return featurePlacement{}, err
	} else if found {
		return placement, nil
	}

	workspace, err := s.Client.FocusedWorkspace(ctx)
	if err != nil {
		return featurePlacement{}, wrapHerdrError("resolve focused workspace", err, "focus a Herdr workspace, then run wt herd retry")
	}
	created, err := s.Client.TabCreateInfo(ctx, workspace.WorkspaceID, feature.Path, feature.Name)
	if err != nil {
		return featurePlacement{}, wrapHerdrError("create feature tab", err, "wt herd retry")
	}
	if created.RootPane.WorkspaceID != "" && created.RootPane.WorkspaceID != workspace.WorkspaceID {
		return featurePlacement{}, &model.StageError{Stage: "resolve root pane", Code: model.ErrHerdrUnavailable, Message: "Herdr returned a root pane from a different workspace", Recovery: "wt herd retry"}
	}
	return featurePlacement{
		WorkspaceID:    workspace.WorkspaceID,
		WorkspaceLabel: workspace.Label,
		TabID:          created.Tab.TabID,
		RootPane:       created.RootPane,
		Warnings:       foreignRepositoryWarning(workspace, sourceCheckout),
	}, nil
}

// placementFromLiveAgent derives the placement from an agent that is already
// running for this feature, so adoption keeps working when no tab is recorded.
// A saved agent is authoritative: if state names one and Herdr cannot resolve
// it, that is reported rather than papered over with a fresh tab, because the
// user needs to rebind it and a new tab would not help.
func (s *Service) placementFromLiveAgent(ctx context.Context, featureState model.FeatureState, feature model.Feature, role, kind string) (featurePlacement, bool, error) {
	if saved, ok := featureState.Agents[role]; ok && saved.Name != "" {
		live, err := s.Client.AgentGetInfo(ctx, saved.Name)
		if err != nil {
			return featurePlacement{}, false, wrapHerdrError("resolve saved primary agent", err, "wt herd rebind "+role+" --target <live-target>")
		}
		placement, err := s.placementFromAgent(live, feature)
		return placement, err == nil, err
	}
	adopted, found, err := s.findAgentInWorktree(ctx, feature.Path, kind)
	if err != nil || !found {
		// An ambiguous or unreachable agent list is not a reason to refuse a
		// placement; ensurePrimary re-runs the same lookup and reports it there.
		return featurePlacement{}, false, nil
	}
	placement, err := s.placementFromAgent(adopted, feature)
	if err != nil {
		return featurePlacement{}, false, err
	}
	return placement, true, nil
}

func (s *Service) placementFromAgent(live herdr.AgentInfo, feature model.Feature) (featurePlacement, error) {
	pane := herdr.PaneInfo(live)
	if err := s.validateRootPane(pane, feature.Path); err != nil {
		return featurePlacement{}, err
	}
	return featurePlacement{
		WorkspaceID: live.WorkspaceID,
		TabID:       live.TabID,
		RootPane:    pane,
		Reused:      true,
	}, nil
}

// rehydratePlacement re-enters the tab recorded in local state. `tab get`
// returns no panes, so the pane is fetched separately and re-checked against
// the recorded tab: a pane id can be reused after a tab is closed, and adopting
// a stranger's pane would aim the agent at someone else's terminal.
func (s *Service) rehydratePlacement(ctx context.Context, featureState model.FeatureState, feature model.Feature) (featurePlacement, error) {
	tab, err := s.Client.TabGetInfo(ctx, featureState.TabID)
	if err != nil {
		return featurePlacement{}, err
	}
	pane, err := s.Client.PaneGetInfo(ctx, featureState.Handoff.RootPaneID)
	if err != nil {
		return featurePlacement{}, err
	}
	if pane.TabID != "" && pane.TabID != tab.TabID {
		return featurePlacement{}, &model.StageError{Stage: "resolve root pane", Code: model.ErrAgentMissing, Message: "the recorded feature pane now belongs to a different tab", Recovery: "wt herd retry"}
	}
	if err := s.validateRootPane(pane, feature.Path); err != nil {
		return featurePlacement{}, err
	}
	workspaceID := tab.WorkspaceID
	if workspaceID == "" {
		workspaceID = featureState.WorkspaceID
	}
	return featurePlacement{
		WorkspaceID: workspaceID,
		TabID:       tab.TabID,
		RootPane:    pane,
		Reused:      true,
	}, nil
}

// foreignRepositoryWarning reports, without refusing, that the focused
// workspace belongs to another checkout. Refusing would strand the worktree for
// a placement that still works; staying silent would let a feature land in a
// workspace the user does not associate with this repository.
func foreignRepositoryWarning(workspace herdr.WorkspaceInfo, sourceCheckout string) []string {
	if sourceCheckout == "" || workspace.Worktree == nil || workspace.Worktree.RepoRoot == "" {
		return nil
	}
	if samePath(workspace.Worktree.RepoRoot, sourceCheckout) {
		return nil
	}
	label := workspace.Label
	if label == "" {
		label = workspace.WorkspaceID
	}
	return []string{"the focused workspace " + label + " is bound to " + workspace.Worktree.RepoName + "; this feature's tab was added there anyway"}
}

// isMissingTarget distinguishes "the recorded Herdr object is gone" from every
// other failure. Only the former may be repaired by placing the feature again.
func isMissingTarget(err error) bool {
	var stage *model.StageError
	return errors.As(err, &stage) && stage.Code == model.ErrAgentMissing
}

func (s *Service) ensurePrimary(ctx context.Context, state *model.BridgeState, featureKey string, featureState model.FeatureState, placement featurePlacement, expectedName, role, kind string) (model.RoleAgent, bool, error) {
	if saved, ok := featureState.Agents[role]; ok {
		if saved.Name != expectedName {
			return model.RoleAgent{}, true, &model.StageError{Stage: "resolve primary agent", Code: model.ErrAgentAmbiguous, Message: "saved primary agent name does not match this feature's identity", Recovery: "wt herd rebind " + role + " --target <live-target>"}
		}
		live, err := s.Client.AgentGetInfo(ctx, saved.Name)
		if err != nil {
			return model.RoleAgent{}, true, wrapHerdrError("resolve saved primary agent", err, "wt herd rebind "+role+" --target <live-target>")
		}
		primary, err := s.validateLivePrimary(live, placement, expectedName, role, kind)
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
		primary, err := s.validateLivePrimary(live, placement, expectedName, role, kind)
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

	// An agent may already be working in this worktree under a different name —
	// a workspace reopened, or a pane a human started. Adopting it is both
	// safer and more useful than launching a second agent beside it, or than
	// failing because the root pane is busy running exactly the agent we were
	// about to duplicate.
	if adopted, found, adoptErr := s.findAgentInWorktree(ctx, featureState.Feature.Path, kind); adoptErr == nil && found {
		primary := roleAgentFrom(adopted, role, kind, s.now())
		featureState.Agents[role] = primary
		if !featureState.Handoff.BootstrapPrompted {
			featureState.Handoff.Stage = model.HandoffReady
		}
		featureState.Handoff.PrimaryAgentName = primary.Name
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
	if err := s.validateRootShell(ctx, placement.RootPane, featureState.Feature.Path, !placement.Reused); err != nil {
		return model.RoleAgent{}, false, err
	}

	started, err := s.Client.AgentStartInfo(ctx, expectedName, kind, placement.RootPane.PaneID, time.Duration(s.Config.Bootstrap.TimeoutSeconds)*time.Second)
	if err != nil {
		return model.RoleAgent{}, false, wrapHerdrError("start primary agent", err, "wt herd retry")
	}
	ready, err := s.waitForReady(ctx, expectedName, started)
	if err != nil {
		return model.RoleAgent{}, false, err
	}
	primary, err := s.validateLivePrimary(ready, placement, expectedName, role, kind)
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

// A pane that has existed for a while is judged immediately: a foreground
// process there is someone else's work, and launching a second agent on top of
// it is exactly what this guard prevents. A pane created moments ago is
// different — `tab create` answers as soon as the pane exists, and for a beat
// the starting shell reports itself as a foreground process with no settled
// shell identity. Refusing there would fail a perfectly good handoff, so a pane
// we just created gets a bounded chance to settle before the same strict check
// decides.
const (
	rootShellSettleAttempts = 20
	rootShellSettleInterval = 250 * time.Millisecond
)

func (s *Service) validateRootShell(ctx context.Context, pane herdr.PaneInfo, worktreePath string, allowSettle bool) error {
	if err := s.validateRootPane(pane, worktreePath); err != nil {
		return err
	}
	attempts := 1
	if allowSettle {
		attempts = rootShellSettleAttempts
	}
	var lastErr error
	for attempt := range attempts {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return &model.StageError{Stage: "resolve root pane", Code: model.ErrHerdrUnavailable, Message: "waiting for the new tab's shell was canceled", Recovery: "wt herd retry", Cause: ctx.Err()}
			case <-time.After(rootShellSettleInterval):
			}
		}
		settling, err := s.rootShellIdle(ctx, pane, worktreePath)
		if err == nil {
			return nil
		}
		if !settling {
			return err
		}
		lastErr = err
	}
	return lastErr
}

// rootShellIdle reports whether the failure it returns is one a starting shell
// produces transiently, so only those are worth waiting out.
func (s *Service) rootShellIdle(ctx context.Context, pane herdr.PaneInfo, worktreePath string) (settling bool, err error) {
	process, processErr := s.Client.PaneProcessInfo(ctx, pane.PaneID)
	if processErr != nil {
		return false, wrapHerdrError("inspect root pane", processErr, "wt herd retry")
	}
	if process.PaneID != pane.PaneID || process.ShellPID == nil {
		return true, &model.StageError{Stage: "resolve root pane", Code: model.ErrHerdrUnavailable, Message: "Herdr root pane has no interactive shell identity", Recovery: "wt herd retry"}
	}
	for _, foreground := range process.ForegroundProcesses {
		if foreground.PID != *process.ShellPID {
			return true, &model.StageError{Stage: "resolve root pane", Code: model.ErrHerdrUnavailable, Message: "Herdr root pane is busy with a non-shell foreground process", Recovery: "wait for the shell, then run wt herd retry"}
		}
		// A cwd mismatch is a real mismatch, not a startup artifact: the tab was
		// told where to start, so waiting cannot turn this into the right pane.
		if foreground.Cwd != "" && !samePath(foreground.Cwd, worktreePath) {
			return false, &model.StageError{Stage: "resolve root pane", Code: model.ErrWorktreeInvalid, Message: "Herdr root shell is not in the feature worktree", Recovery: "wt herd retry"}
		}
	}
	return false, nil
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

func (s *Service) validateLivePrimary(live herdr.AgentInfo, placement featurePlacement, expectedName, role, kind string) (model.RoleAgent, error) {
	if live.Name != expectedName {
		return model.RoleAgent{}, &model.StageError{Stage: "resolve primary agent", Code: model.ErrAgentAmbiguous, Message: "Herdr returned a different agent than the managed primary", Recovery: "wt herd status"}
	}
	if live.WorkspaceID != placement.WorkspaceID || live.PaneID != placement.RootPane.PaneID || live.TerminalID == "" {
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
	// #nosec G304 -- callers compose this from the canonical validated feature worktree and a fixed task filename.
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
