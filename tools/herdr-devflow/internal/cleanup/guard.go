// Package cleanup protects Ori-managed Git worktrees from being removed while
// their bridge-owned Herdr work is still active or cannot be verified.
package cleanup

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/herdr"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/model"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/worktree"
)

// Exit codes let scripts distinguish a known active workload from a state that
// needs an explicit interactive safety override. They are intentionally kept
// separate from generic CLI parsing errors.
const (
	ExitBlocked       = 20
	ExitNeedsOverride = 21
)

type Outcome string

const (
	OutcomeSkipped     Outcome = "skipped"
	OutcomeReady       Outcome = "ready"
	OutcomeBlocked     Outcome = "blocked"
	OutcomeUnavailable Outcome = "unavailable"
	OutcomeCloseFailed Outcome = "close_failed"
	OutcomeOverridden  Outcome = "overridden"
)

type Store interface {
	Load() (model.BridgeState, error)
}

type LockingStore interface {
	Lock(context.Context) (func(), error)
}

// Herdr deliberately exposes only the two operations cleanup is permitted to
// use. In particular, no Herdr worktree create/remove method is available.
type Herdr interface {
	AgentListInfo(context.Context) ([]herdr.AgentInfo, error)
	WorkspaceClose(context.Context, string) (json.RawMessage, error)
}

type Inspector interface {
	Inspect(context.Context, string, string, string) (worktree.GitWorktree, error)
}

type inspectorFunc func(context.Context, string, string, string) (worktree.GitWorktree, error)

func (f inspectorFunc) Inspect(ctx context.Context, path, branch, commonDir string) (worktree.GitWorktree, error) {
	return f(ctx, path, branch, commonDir)
}

type Service struct {
	Store        Store
	Client       Herdr
	Inspector    Inspector
	RepositoryID string
	GitCommonDir string
}

type Request struct {
	WorktreePath string
	Override     bool
}

// Result is safe to render or serialize. It deliberately excludes prompts,
// environment values, agent output, and opaque Herdr command errors.
type Result struct {
	Outcome         Outcome       `json:"outcome"`
	Feature         model.Feature `json:"feature,omitempty"`
	WorkspaceID     string        `json:"workspace_id,omitempty"`
	Agents          []Agent       `json:"agents,omitempty"`
	Schedules       []Schedule    `json:"schedules,omitempty"`
	Detail          string        `json:"detail,omitempty"`
	WorkspaceClosed bool          `json:"workspace_closed"`
	Overridden      bool          `json:"overridden"`
}

type Agent struct {
	Role         string            `json:"role"`
	Name         string            `json:"name"`
	Status       model.AgentStatus `json:"status"`
	FocusCommand string            `json:"focus_command"`
	ReadCommand  string            `json:"read_command"`
}

type Schedule struct {
	ID            string              `json:"id"`
	State         model.ScheduleState `json:"state"`
	ShowCommand   string              `json:"show_command"`
	CancelCommand string              `json:"cancel_command,omitempty"`
}

// Preflight proves the linked Git checkout belongs to exactly one bridge
// record, refuses known active agents/schedules, then closes only the matching
// Herdr workspace when every observed agent is idle or done. Override can
// bypass only unavailable/close-failed states; it never bypasses active work.
func (s *Service) Preflight(ctx context.Context, request Request) Result {
	if s.Store == nil {
		return s.unavailable(request, Result{Detail: "local bridge state is unavailable"})
	}
	if locker, ok := s.Store.(LockingStore); ok {
		unlock, err := locker.Lock(ctx)
		if err != nil {
			return s.unavailable(request, Result{Detail: "the bridge safety lock could not be acquired"})
		}
		defer unlock()
	}
	return s.preflight(ctx, request)
}

func (s *Service) preflight(ctx context.Context, request Request) Result {
	if strings.TrimSpace(request.WorktreePath) == "" {
		return s.unavailable(request, Result{Detail: "a target Git worktree is required"})
	}
	if strings.TrimSpace(s.RepositoryID) == "" || strings.TrimSpace(s.GitCommonDir) == "" {
		return s.unavailable(request, Result{Detail: "repository identity is unavailable"})
	}

	inspector := s.Inspector
	if inspector == nil {
		inspector = inspectorFunc(worktree.InspectLinkedGitWorktree)
	}
	linked, err := inspector.Inspect(ctx, request.WorktreePath, "", s.GitCommonDir)
	if err != nil {
		return s.unavailable(request, Result{Detail: "the cleanup target is not a verified linked Git worktree"})
	}
	bridgeState, err := s.Store.Load()
	if err != nil {
		return s.unavailable(request, Result{Detail: "local bridge state could not be read"})
	}

	featureState, found, ambiguous := featureForLinkedWorktree(bridgeState, s.RepositoryID, linked.Path)
	if ambiguous {
		return s.unavailable(request, Result{Detail: "multiple bridge records match this linked Git worktree"})
	}
	if !found {
		return Result{Outcome: OutcomeSkipped, Detail: "no managed Herdr handoff is recorded for this linked Git worktree"}
	}
	result := Result{Feature: featureState.Feature, WorkspaceID: featureState.WorkspaceID}
	if featureState.Feature.Branch != "" && linked.Branch != featureState.Feature.Branch {
		result.Detail = "the linked Git worktree branch no longer matches its managed bridge record"
		return s.unavailable(request, result)
	}
	if !schedulesBelongToFeature(featureState.Feature, featureState.WorkspaceID, featureState.Schedules) {
		result.Detail = "saved continuation identity does not match this feature worktree and Herdr workspace"
		return s.unavailable(request, result)
	}

	if unresolved := unresolvedSchedules(featureState.Feature, featureState.Schedules); len(unresolved) > 0 {
		result.Outcome = OutcomeBlocked
		result.Schedules = unresolved
		result.Detail = "one or more continuation schedules remain unresolved"
		return result
	}

	if featureState.WorkspaceID == "" {
		if len(featureState.Agents) == 0 {
			result.Outcome = OutcomeReady
			result.Detail = "no Herdr workspace was opened for this managed handoff"
			return result
		}
		result.Agents = savedAgents(featureState.Feature, featureState.Agents, model.AgentUnknown)
		result.Detail = "the managed feature has agents but no saved Herdr workspace identity"
		return s.unavailable(request, result)
	}
	if !agentsBelongToWorkspace(featureState.Agents, featureState.WorkspaceID) {
		result.Agents = savedAgents(featureState.Feature, featureState.Agents, model.AgentUnknown)
		result.Detail = "saved agent identities do not agree on the feature Herdr workspace"
		return s.unavailable(request, result)
	}
	if s.Client == nil {
		result.Agents = savedAgents(featureState.Feature, featureState.Agents, model.AgentUnknown)
		result.Detail = "Herdr agent state cannot be verified"
		return s.unavailable(request, result)
	}

	liveAgents, err := s.Client.AgentListInfo(ctx)
	if err != nil {
		result.Agents = savedAgents(featureState.Feature, featureState.Agents, model.AgentUnknown)
		result.Detail = "Herdr agent state cannot be verified"
		return s.unavailable(request, result)
	}

	result.Agents = resolveAgents(featureState.Feature, featureState.Agents, liveAgents)
	for _, agent := range result.Agents {
		switch agent.Status {
		case model.AgentWorking, model.AgentBlocked:
			result.Outcome = OutcomeBlocked
			result.Detail = "one or more associated agents are still active"
			return result
		case model.AgentIdle, model.AgentDone:
			// Safe to continue checking the other associated agents.
		default:
			result.Detail = "one or more associated agents could not be matched to a safe live Herdr state"
			return s.unavailable(request, result)
		}
	}

	if _, err := s.Client.WorkspaceClose(ctx, featureState.WorkspaceID); err != nil {
		result.Outcome = OutcomeCloseFailed
		result.Detail = "Herdr workspace close did not complete; the Git worktree remains preserved"
		return s.closeFailed(request, result)
	}
	result.Outcome = OutcomeReady
	result.WorkspaceClosed = true
	result.Detail = "associated agents are settled, schedules are resolved, and the Herdr workspace is closed"
	return result
}

func (s *Service) unavailable(request Request, result Result) Result {
	if request.Override {
		result.Outcome = OutcomeOverridden
		result.Overridden = true
		result.Detail = "explicit Herdr-safety override accepted: " + result.Detail + "; the Git worktree may be orphaned from live Herdr state"
		return result
	}
	result.Outcome = OutcomeUnavailable
	return result
}

func (s *Service) closeFailed(request Request, result Result) Result {
	if request.Override {
		result.Outcome = OutcomeOverridden
		result.Overridden = true
		result.Detail = "explicit Herdr-safety override accepted: Herdr workspace close failed; the Git worktree may be orphaned from live Herdr state"
	}
	return result
}

func featureForLinkedWorktree(bridgeState model.BridgeState, repositoryID, linkedPath string) (model.FeatureState, bool, bool) {
	var matches []model.FeatureState
	for _, featureState := range bridgeState.Features {
		if featureState.Feature.RepositoryID != repositoryID || !samePath(featureState.Feature.Path, linkedPath) {
			continue
		}
		matches = append(matches, featureState)
	}
	if len(matches) == 0 {
		return model.FeatureState{}, false, false
	}
	if len(matches) != 1 {
		return model.FeatureState{}, false, true
	}
	return matches[0], true, false
}

func unresolvedSchedules(feature model.Feature, schedules map[string]model.Schedule) []Schedule {
	result := make([]Schedule, 0)
	for _, schedule := range schedules {
		if !schedule.State.IsUnresolved() {
			continue
		}
		entry := scheduleEntry(feature, schedule)
		result = append(result, entry)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func schedulesBelongToFeature(feature model.Feature, workspaceID string, schedules map[string]model.Schedule) bool {
	for _, schedule := range schedules {
		if schedule.FeaturePath == "" || !samePath(schedule.FeaturePath, feature.Path) {
			return false
		}
		if workspaceID != "" && schedule.WorkspaceID != workspaceID {
			return false
		}
	}
	return true
}

func scheduleEntry(feature model.Feature, schedule model.Schedule) Schedule {
	worktreeArg := shellQuote(feature.Path)
	entry := Schedule{
		ID:          schedule.ID,
		State:       schedule.State,
		ShowCommand: "wt herd schedule show " + shellQuote(schedule.ID) + " --worktree " + worktreeArg,
	}
	if schedule.State == model.SchedulePending || schedule.State == model.ScheduleWaiting {
		entry.CancelCommand = "wt herd schedule cancel " + shellQuote(schedule.ID) + " --worktree " + worktreeArg
	}
	return entry
}

func agentsBelongToWorkspace(agents map[string]model.RoleAgent, workspaceID string) bool {
	for _, agent := range agents {
		if agent.WorkspaceID != workspaceID {
			return false
		}
	}
	return true
}

func savedAgents(feature model.Feature, agents map[string]model.RoleAgent, fallback model.AgentStatus) []Agent {
	roles := make([]string, 0, len(agents))
	for role := range agents {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	result := make([]Agent, 0, len(roles))
	for _, role := range roles {
		saved := agents[role]
		result = append(result, agentEntry(feature, saved, fallback))
	}
	return result
}

func resolveAgents(feature model.Feature, agents map[string]model.RoleAgent, liveAgents []herdr.AgentInfo) []Agent {
	roles := make([]string, 0, len(agents))
	for role := range agents {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	result := make([]Agent, 0, len(roles))
	for _, role := range roles {
		saved := agents[role]
		live, found, ambiguous := findLive(saved, liveAgents)
		status := model.AgentMissing
		if ambiguous {
			status = model.AgentUnknown
		} else if found {
			status = normalizeStatus(live.AgentStatus)
		}
		result = append(result, agentEntry(feature, saved, status))
	}
	return result
}

func agentEntry(feature model.Feature, saved model.RoleAgent, status model.AgentStatus) Agent {
	worktreeArg := shellQuote(feature.Path)
	roleArg := shellQuote(saved.Role)
	return Agent{
		Role:         saved.Role,
		Name:         saved.Name,
		Status:       status,
		FocusCommand: "wt herd focus " + roleArg + " --worktree " + worktreeArg,
		ReadCommand:  "wt herd read " + roleArg + " --worktree " + worktreeArg,
	}
}

func findLive(saved model.RoleAgent, liveAgents []herdr.AgentInfo) (herdr.AgentInfo, bool, bool) {
	if saved.NativeSession.Value != "" {
		var matches []herdr.AgentInfo
		for _, candidate := range liveAgents {
			if candidate.AgentSession != nil && sameNative(saved.NativeSession, *candidate.AgentSession) && candidate.WorkspaceID == saved.WorkspaceID {
				matches = append(matches, candidate)
			}
		}
		if len(matches) == 1 {
			return matches[0], true, false
		}
		if len(matches) > 1 {
			return herdr.AgentInfo{}, false, true
		}
	}
	for _, candidate := range liveAgents {
		if candidate.Name == saved.Name && candidate.WorkspaceID == saved.WorkspaceID && candidate.PaneID == saved.PaneID && candidate.TerminalID == saved.TerminalID {
			return candidate, true, false
		}
	}
	return herdr.AgentInfo{}, false, false
}

func sameNative(left, right model.NativeSession) bool {
	return left.Value != "" && left.Value == right.Value && left.Source == right.Source && left.Agent == right.Agent && left.Kind == right.Kind
}

func normalizeStatus(status model.AgentStatus) model.AgentStatus {
	switch model.AgentStatus(strings.ToLower(strings.TrimSpace(string(status)))) {
	case model.AgentIdle:
		return model.AgentIdle
	case model.AgentWorking:
		return model.AgentWorking
	case model.AgentBlocked:
		return model.AgentBlocked
	case model.AgentDone:
		return model.AgentDone
	default:
		return model.AgentUnknown
	}
}

func samePath(left, right string) bool {
	leftResolved, leftErr := filepath.EvalSymlinks(left)
	rightResolved, rightErr := filepath.EvalSymlinks(right)
	if leftErr == nil {
		left = leftResolved
	}
	if rightErr == nil {
		right = rightResolved
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\\"'\\\"'") + "'"
}

func (o Outcome) String() string {
	if o == "" {
		return string(OutcomeUnavailable)
	}
	return string(o)
}

func (r Result) String() string {
	return fmt.Sprintf("cleanup %s", r.Outcome.String())
}
