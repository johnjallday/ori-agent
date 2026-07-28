package cleanup

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/herdr"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/model"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/worktree"
)

type fakeStore struct {
	state model.BridgeState
	err   error
}

func (s fakeStore) Load() (model.BridgeState, error) { return s.state, s.err }

type fakeClient struct {
	agents     []herdr.AgentInfo
	workspaces []herdr.WorkspaceInfo
	tabs       []herdr.TabInfo
	listErr    error
	closeErr   error
	closeCalls []string
}

func (c *fakeClient) AgentListInfo(context.Context) ([]herdr.AgentInfo, error) {
	return c.agents, c.listErr
}

func (c *fakeClient) WorkspaceListInfo(context.Context) ([]herdr.WorkspaceInfo, error) {
	return c.workspaces, c.listErr
}

func (c *fakeClient) TabListInfo(context.Context, string) ([]herdr.TabInfo, error) {
	return c.tabs, c.listErr
}

func (c *fakeClient) TabClose(_ context.Context, tabID string) (json.RawMessage, error) {
	c.closeCalls = append(c.closeCalls, tabID)
	return json.RawMessage(`{"result":{"type":"ok"}}`), c.closeErr
}

type fakeInspector struct {
	linked worktree.GitWorktree
	err    error
}

func (i fakeInspector) Inspect(context.Context, string, string, string) (worktree.GitWorktree, error) {
	return i.linked, i.err
}

func TestPreflightDecisionTable(t *testing.T) {
	feature := model.Feature{RepositoryID: "repo-1", Name: "bridge", Branch: "feature/bridge", Path: "/tmp/ori/bridge"}
	agent := model.RoleAgent{Role: "builder", Name: "ori-bridge-builder", Kind: "claude", WorkspaceID: "w1", PaneID: "w1:p1", TerminalID: "term-1", NativeSession: model.NativeSession{Source: "herdr:claude", Agent: "claude", Kind: "id", Value: "session-1"}}
	baseState := func() model.BridgeState {
		state := model.NewBridgeState()
		state.Features["repo-1:bridge"] = model.FeatureState{Feature: feature, WorkspaceID: "w1", TabID: "w1:t2", Agents: map[string]model.RoleAgent{"builder": agent}, Schedules: map[string]model.Schedule{}}
		return state
	}
	// The feature's own tab plus a sibling that must survive every close.
	liveTabs := []herdr.TabInfo{{TabID: "w1:t1", WorkspaceID: "w1"}, {TabID: "w1:t2", WorkspaceID: "w1"}}
	live := func(status model.AgentStatus) herdr.AgentInfo {
		return herdr.AgentInfo{Name: agent.Name, Agent: agent.Kind, AgentStatus: status, WorkspaceID: agent.WorkspaceID, PaneID: agent.PaneID, TerminalID: agent.TerminalID, AgentSession: &agent.NativeSession}
	}

	tests := []struct {
		name           string
		state          model.BridgeState
		client         fakeClient
		override       bool
		want           Outcome
		wantClose      int
		wantSchedule   bool
		wantCancelable bool
	}{
		{name: "idle agent closes the feature tab", state: baseState(), client: fakeClient{tabs: liveTabs, agents: []herdr.AgentInfo{live(model.AgentIdle)}}, want: OutcomeReady, wantClose: 1},
		{name: "done agent closes the feature tab", state: baseState(), client: fakeClient{tabs: liveTabs, agents: []herdr.AgentInfo{live(model.AgentDone)}}, want: OutcomeReady, wantClose: 1},
		{name: "working agent blocks cleanup", state: baseState(), client: fakeClient{tabs: liveTabs, agents: []herdr.AgentInfo{live(model.AgentWorking)}}, want: OutcomeBlocked},
		{name: "blocked agent blocks cleanup", state: baseState(), client: fakeClient{tabs: liveTabs, agents: []herdr.AgentInfo{live(model.AgentBlocked)}}, want: OutcomeBlocked},
		{name: "unknown agent needs override", state: baseState(), client: fakeClient{tabs: liveTabs, agents: []herdr.AgentInfo{live(model.AgentUnknown)}}, want: OutcomeUnavailable},
		// A saved record naming an agent Herdr can no longer see used to force
		// an override. Nothing is running to orphan, so a workspace closed
		// days ago must not make a worktree permanently un-removable.
		{name: "missing agent no longer blocks cleanup", state: baseState(), client: fakeClient{tabs: liveTabs, agents: nil}, want: OutcomeReady, wantClose: 1},
		{name: "live list outage needs override", state: baseState(), client: fakeClient{tabs: liveTabs, listErr: errors.New("server unavailable")}, want: OutcomeUnavailable},
		{name: "live list outage can be explicitly overridden", state: baseState(), client: fakeClient{tabs: liveTabs, listErr: errors.New("server unavailable")}, override: true, want: OutcomeOverridden},
		{name: "tab close failure needs override", state: baseState(), client: fakeClient{tabs: liveTabs, agents: []herdr.AgentInfo{live(model.AgentIdle)}, closeErr: errors.New("close unavailable")}, want: OutcomeCloseFailed, wantClose: 1},
		{name: "tab close failure can be explicitly overridden", state: baseState(), client: fakeClient{tabs: liveTabs, agents: []herdr.AgentInfo{live(model.AgentIdle)}, closeErr: errors.New("close unavailable")}, override: true, want: OutcomeOverridden, wantClose: 1},
		{name: "pending schedule blocks before any close", state: withSchedule(baseState(), model.Schedule{ID: "sch-pending", State: model.SchedulePending}), client: fakeClient{tabs: liveTabs, agents: []herdr.AgentInfo{live(model.AgentIdle)}}, want: OutcomeBlocked, wantSchedule: true, wantCancelable: true},
		{name: "waiting schedule blocks before any close", state: withSchedule(baseState(), model.Schedule{ID: "sch-waiting", State: model.ScheduleWaiting}), client: fakeClient{tabs: liveTabs, agents: []herdr.AgentInfo{live(model.AgentIdle)}}, want: OutcomeBlocked, wantSchedule: true, wantCancelable: true},
		{name: "delivering schedule blocks before any close", state: withSchedule(baseState(), model.Schedule{ID: "sch-delivering", State: model.ScheduleDelivering}), client: fakeClient{tabs: liveTabs, agents: []herdr.AgentInfo{live(model.AgentIdle)}}, want: OutcomeBlocked, wantSchedule: true},
		{name: "uncertain schedule blocks before any close", state: withSchedule(baseState(), model.Schedule{ID: "sch-uncertain", State: model.ScheduleUncertain}), client: fakeClient{tabs: liveTabs, agents: []herdr.AgentInfo{live(model.AgentIdle)}}, want: OutcomeBlocked, wantSchedule: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := test.client
			service := Service{
				Store:        fakeStore{state: test.state},
				Client:       &client,
				Inspector:    fakeInspector{linked: worktree.GitWorktree{Path: feature.Path, Branch: feature.Branch, CommonDir: "/tmp/ori/.git"}},
				RepositoryID: feature.RepositoryID,
				GitCommonDir: "/tmp/ori/.git",
			}
			got := service.Preflight(context.Background(), Request{WorktreePath: feature.Path, Override: test.override})
			if got.Outcome != test.want {
				t.Fatalf("outcome = %q, want %q (%#v)", got.Outcome, test.want, got)
			}
			if len(client.closeCalls) != test.wantClose {
				t.Fatalf("tab close calls = %#v, want %d", client.closeCalls, test.wantClose)
			}
			for _, closed := range client.closeCalls {
				if closed != "w1:t2" {
					t.Fatalf("cleanup closed %q; only the feature's own tab may be closed", closed)
				}
			}
			if test.wantSchedule {
				if len(got.Schedules) != 1 || got.Schedules[0].ShowCommand == "" {
					t.Fatalf("unresolved schedules = %#v", got.Schedules)
				}
				if (got.Schedules[0].CancelCommand != "") != test.wantCancelable {
					t.Fatalf("schedule cancellation guidance = %#v, want cancelable=%v", got.Schedules[0], test.wantCancelable)
				}
			}
			if got.Outcome == OutcomeBlocked && test.override && got.Overridden {
				t.Fatalf("active work must never be overridden: %#v", got)
			}
		})
	}
}

func TestPreflightUsesCanonicalFeatureIdentityAndTabCloseOnly(t *testing.T) {
	feature := model.Feature{RepositoryID: "repo-1", Name: "bridge", Path: "/tmp/ori/bridge"}
	agent := model.RoleAgent{Role: "builder", Name: "ori-bridge-builder", WorkspaceID: "w1", PaneID: "w1:p1", TerminalID: "term-1"}
	state := model.NewBridgeState()
	state.Features["repo-1:other"] = model.FeatureState{Feature: model.Feature{RepositoryID: "repo-1", Name: "other", Path: "/tmp/ori/other"}, WorkspaceID: "w-other"}
	state.Features["repo-1:bridge"] = model.FeatureState{Feature: feature, WorkspaceID: "w1", TabID: "w1:t2", Agents: map[string]model.RoleAgent{"builder": agent}}
	client := &fakeClient{
		agents: []herdr.AgentInfo{{Name: agent.Name, AgentStatus: model.AgentIdle, WorkspaceID: "w1", PaneID: "w1:p1", TerminalID: "term-1"}},
		tabs:   []herdr.TabInfo{{TabID: "w1:t1", WorkspaceID: "w1"}, {TabID: "w1:t2", WorkspaceID: "w1"}},
	}
	service := Service{
		Store:        fakeStore{state: state},
		Client:       client,
		Inspector:    fakeInspector{linked: worktree.GitWorktree{Path: feature.Path, CommonDir: "/tmp/ori/.git"}},
		RepositoryID: "repo-1",
		GitCommonDir: "/tmp/ori/.git",
	}
	got := service.Preflight(context.Background(), Request{WorktreePath: feature.Path})
	if got.Outcome != OutcomeReady || got.WorkspaceID != "w1" || len(client.closeCalls) != 1 || client.closeCalls[0] != "w1:t2" {
		t.Fatalf("preflight = %#v, closeCalls=%#v", got, client.closeCalls)
	}
	if !got.TabClosed || got.WorkspaceClosed {
		t.Fatalf("cleanup reported %#v; it must close a tab and never a workspace", got)
	}
	if got.Agents[0].FocusCommand != "wt herd focus 'builder' --worktree '/tmp/ori/bridge'" || got.Agents[0].ReadCommand != "wt herd read 'builder' --worktree '/tmp/ori/bridge'" {
		t.Fatalf("agent recovery commands = %#v", got.Agents[0])
	}
}

func TestShellQuoteKeepsMetacharactersLiteralAndDropsControls(t *testing.T) {
	t.Parallel()
	got := shellQuote("agent'; $(touch never)\n")
	want := `'agent'"'"'; $(touch never)'`
	if got != want {
		t.Fatalf("shellQuote() = %q, want %q", got, want)
	}
	if strings.ContainsAny(got, "\n\r\x00") {
		t.Fatalf("shellQuote() leaked a control character: %q", got)
	}
}

func TestPreflightSkipsUnmanagedAndAllowsRecordedNoWorkspace(t *testing.T) {
	service := Service{
		Store:        fakeStore{state: model.NewBridgeState()},
		Inspector:    fakeInspector{linked: worktree.GitWorktree{Path: "/tmp/ori/bridge", CommonDir: "/tmp/ori/.git"}},
		RepositoryID: "repo-1",
		GitCommonDir: "/tmp/ori/.git",
	}
	if got := service.Preflight(context.Background(), Request{WorktreePath: "/tmp/ori/bridge"}); got.Outcome != OutcomeSkipped {
		t.Fatalf("unmanaged outcome = %#v", got)
	}
	state := model.NewBridgeState()
	state.Features["repo-1:bridge"] = model.FeatureState{Feature: model.Feature{RepositoryID: "repo-1", Name: "bridge", Path: "/tmp/ori/bridge"}}
	service.Store = fakeStore{state: state}
	if got := service.Preflight(context.Background(), Request{WorktreePath: "/tmp/ori/bridge"}); got.Outcome != OutcomeReady || got.WorkspaceClosed {
		t.Fatalf("recorded/no-workspace outcome = %#v", got)
	}
}

func TestPreflightFailsClosedForMismatchedCanonicalFeatureState(t *testing.T) {
	feature := model.Feature{RepositoryID: "repo-1", Name: "bridge", Branch: "feature/bridge", Path: "/tmp/ori/bridge"}
	state := model.NewBridgeState()
	state.Features["repo-1:bridge"] = model.FeatureState{
		Feature:     feature,
		WorkspaceID: "w1",
		Schedules: map[string]model.Schedule{
			"sch-wrong-path": {ID: "sch-wrong-path", State: model.ScheduleDelivered, FeaturePath: "/tmp/ori/other", WorkspaceID: "w1"},
		},
	}
	client := &fakeClient{}
	service := Service{
		Store:        fakeStore{state: state},
		Client:       client,
		Inspector:    fakeInspector{linked: worktree.GitWorktree{Path: feature.Path, Branch: feature.Branch, CommonDir: "/tmp/ori/.git"}},
		RepositoryID: feature.RepositoryID,
		GitCommonDir: "/tmp/ori/.git",
	}
	got := service.Preflight(context.Background(), Request{WorktreePath: feature.Path})
	if got.Outcome != OutcomeUnavailable || len(client.closeCalls) != 0 {
		t.Fatalf("mismatched schedule outcome = %#v, closeCalls=%#v", got, client.closeCalls)
	}

	featureState := state.Features["repo-1:bridge"]
	featureState.Schedules = nil
	featureState.Feature.Branch = "feature/other"
	state.Features["repo-1:bridge"] = featureState
	service.Store = fakeStore{state: state}
	got = service.Preflight(context.Background(), Request{WorktreePath: feature.Path})
	if got.Outcome != OutcomeUnavailable || len(client.closeCalls) != 0 {
		t.Fatalf("mismatched branch outcome = %#v, closeCalls=%#v", got, client.closeCalls)
	}
}

func withSchedule(state model.BridgeState, schedule model.Schedule) model.BridgeState {
	feature := state.Features["repo-1:bridge"]
	if schedule.FeaturePath == "" {
		schedule.FeaturePath = feature.Feature.Path
	}
	if schedule.WorkspaceID == "" {
		schedule.WorkspaceID = feature.WorkspaceID
	}
	if feature.Schedules == nil {
		feature.Schedules = make(map[string]model.Schedule)
	}
	feature.Schedules[schedule.ID] = schedule
	state.Features["repo-1:bridge"] = feature
	return state
}

// TestPreflightReproducesTheStaleRecordCase is the 2026-07-26 incident: the
// bridge's saved builder pointed at a workspace that had been closed, nothing
// was running in the worktree, and cleanup demanded HERDR-OVERRIDE anyway.
func TestPreflightReproducesTheStaleRecordCase(t *testing.T) {
	feature := model.Feature{RepositoryID: "repo-1", Name: "bridge", Branch: "feature/bridge", Path: "/tmp/ori/bridge"}
	stale := model.RoleAgent{
		Role: "builder", Name: "ori-bridge-builder", Kind: "claude",
		WorkspaceID: "wK", PaneID: "wK:p1", TerminalID: "term-gone",
	}
	state := model.NewBridgeState()
	state.Features["repo-1:bridge"] = model.FeatureState{
		Feature: feature, WorkspaceID: "wK",
		Agents: map[string]model.RoleAgent{"builder": stale},
	}

	// Herdr is healthy and reports agents — just none in this worktree.
	elsewhere := herdr.AgentInfo{
		Name: "someone-else", Agent: "claude", AgentStatus: model.AgentWorking,
		WorkspaceID: "wE", PaneID: "wE:p1", Cwd: "/tmp/ori/other-feature",
	}
	client := &fakeClient{agents: []herdr.AgentInfo{elsewhere}}
	service := Service{
		Store:        fakeStore{state: state},
		Client:       client,
		Inspector:    fakeInspector{linked: worktree.GitWorktree{Path: feature.Path, Branch: feature.Branch, CommonDir: "/tmp/ori/.git"}},
		RepositoryID: feature.RepositoryID,
		GitCommonDir: "/tmp/ori/.git",
	}

	got := service.Preflight(context.Background(), Request{WorktreePath: feature.Path})
	if got.Outcome != OutcomeReady {
		t.Fatalf("outcome = %q, want ready — nothing is running in this worktree (%#v)", got.Outcome, got)
	}
	// This record predates tabs, so there is nothing narrow to close. The
	// worktree is still released; the workspace is not touched.
	if len(client.closeCalls) != 0 {
		t.Fatalf("close calls = %v, want none for a workspace-backed record", client.closeCalls)
	}
}

// TestPreflightBlocksOnAnAgentNoSavedRecordKnows covers the other half: an
// agent a human started in the worktree is just as real as one the bridge did.
func TestPreflightBlocksOnAnAgentNoSavedRecordKnows(t *testing.T) {
	feature := model.Feature{RepositoryID: "repo-1", Name: "bridge", Branch: "feature/bridge", Path: "/tmp/ori/bridge"}
	state := model.NewBridgeState()
	state.Features["repo-1:bridge"] = model.FeatureState{
		Feature: feature, WorkspaceID: "wK",
		Agents: map[string]model.RoleAgent{"builder": {
			Role: "builder", Name: "gone", WorkspaceID: "wK", PaneID: "wK:p1",
		}},
	}

	handOpened := herdr.AgentInfo{
		Name: "hand-opened", Agent: "claude", AgentStatus: model.AgentWorking,
		WorkspaceID: "wZ", PaneID: "wZ:p1", Cwd: "/tmp/ori/bridge/internal",
	}
	client := &fakeClient{agents: []herdr.AgentInfo{handOpened}}
	service := Service{
		Store:        fakeStore{state: state},
		Client:       client,
		Inspector:    fakeInspector{linked: worktree.GitWorktree{Path: feature.Path, Branch: feature.Branch, CommonDir: "/tmp/ori/.git"}},
		RepositoryID: feature.RepositoryID,
		GitCommonDir: "/tmp/ori/.git",
	}

	got := service.Preflight(context.Background(), Request{WorktreePath: feature.Path})
	if got.Outcome != OutcomeBlocked {
		t.Fatalf("outcome = %q, want blocked — an agent is working in this worktree (%#v)", got.Outcome, got)
	}
	if len(client.closeCalls) != 0 {
		t.Fatalf("a blocked cleanup closed the workspace: %v", client.closeCalls)
	}
	// The message must be actionable: what, doing what, where.
	for _, want := range []string{"claude", "working", "wZ:p1", "/tmp/ori/bridge/internal"} {
		if !strings.Contains(got.Detail, want) {
			t.Fatalf("detail = %q, want it to name %q", got.Detail, want)
		}
	}
}

func TestPreflightIdleUnmanagedAgentDoesNotBlock(t *testing.T) {
	feature := model.Feature{RepositoryID: "repo-1", Name: "bridge", Branch: "feature/bridge", Path: "/tmp/ori/bridge"}
	state := model.NewBridgeState()
	state.Features["repo-1:bridge"] = model.FeatureState{Feature: feature, WorkspaceID: "w1"}

	idle := herdr.AgentInfo{
		Name: "hand-opened", Agent: "claude", AgentStatus: model.AgentIdle,
		WorkspaceID: "wZ", PaneID: "wZ:p1", Cwd: "/tmp/ori/bridge",
	}
	client := &fakeClient{agents: []herdr.AgentInfo{idle}}
	service := Service{
		Store:        fakeStore{state: state},
		Client:       client,
		Inspector:    fakeInspector{linked: worktree.GitWorktree{Path: feature.Path, Branch: feature.Branch, CommonDir: "/tmp/ori/.git"}},
		RepositoryID: feature.RepositoryID,
		GitCommonDir: "/tmp/ori/.git",
	}

	if got := service.Preflight(context.Background(), Request{WorktreePath: feature.Path}); got.Outcome != OutcomeReady {
		t.Fatalf("outcome = %q, want ready — an idle agent is settled (%#v)", got.Outcome, got)
	}
}

func TestPreflightHerdrOutageStillFailsClosed(t *testing.T) {
	// Occupancy cannot be established, so the override remains required. Only
	// stale records stopped blocking, never unverifiable state.
	feature := model.Feature{RepositoryID: "repo-1", Name: "bridge", Branch: "feature/bridge", Path: "/tmp/ori/bridge"}
	state := model.NewBridgeState()
	state.Features["repo-1:bridge"] = model.FeatureState{
		Feature: feature, WorkspaceID: "w1",
		Agents: map[string]model.RoleAgent{"builder": {Role: "builder", Name: "b", WorkspaceID: "w1", PaneID: "w1:p1"}},
	}
	client := &fakeClient{listErr: errors.New("herdr socket closed")}
	service := Service{
		Store:        fakeStore{state: state},
		Client:       client,
		Inspector:    fakeInspector{linked: worktree.GitWorktree{Path: feature.Path, Branch: feature.Branch, CommonDir: "/tmp/ori/.git"}},
		RepositoryID: feature.RepositoryID,
		GitCommonDir: "/tmp/ori/.git",
	}

	if got := service.Preflight(context.Background(), Request{WorktreePath: feature.Path}); got.Outcome != OutcomeUnavailable {
		t.Fatalf("outcome = %q, want unavailable when Herdr cannot be reached (%#v)", got.Outcome, got)
	}
}

func TestPreflightIgnoresAgentsInSiblingWorktrees(t *testing.T) {
	feature := model.Feature{RepositoryID: "repo-1", Name: "feature-a", Branch: "feature/feature-a", Path: "/tmp/ori/feature-a"}
	state := model.NewBridgeState()
	state.Features["repo-1:feature-a"] = model.FeatureState{Feature: feature, WorkspaceID: "w1"}

	// Prefix collision: a working agent in feature-abc must not block
	// feature-a's cleanup.
	sibling := herdr.AgentInfo{
		Name: "other", Agent: "claude", AgentStatus: model.AgentWorking,
		WorkspaceID: "wZ", PaneID: "wZ:p1", Cwd: "/tmp/ori/feature-abc",
	}
	client := &fakeClient{agents: []herdr.AgentInfo{sibling}}
	service := Service{
		Store:        fakeStore{state: state},
		Client:       client,
		Inspector:    fakeInspector{linked: worktree.GitWorktree{Path: feature.Path, Branch: feature.Branch, CommonDir: "/tmp/ori/.git"}},
		RepositoryID: feature.RepositoryID,
		GitCommonDir: "/tmp/ori/.git",
	}

	if got := service.Preflight(context.Background(), Request{WorktreePath: feature.Path}); got.Outcome != OutcomeReady {
		t.Fatalf("outcome = %q, want ready — the working agent is in a sibling worktree (%#v)", got.Outcome, got)
	}
}

// FR-30. Every feature recorded before tab-scoped handoff has a workspace and
// no tab, including ones live right now. Closing that workspace is the exact
// shape of the 2026-07-26 cascade: it can be bound to a repository's main
// checkout and take unrelated workspaces with it. Cleanup must release the
// worktree, close nothing, and say which workspace is the user's to close.
func TestLegacyWorkspaceBackedFeatureIsNeverClosedAutomatically(t *testing.T) {
	feature := model.Feature{RepositoryID: "repo-1", Name: "legacy", Branch: "feature/legacy", Path: "/tmp/ori/legacy"}
	agent := model.RoleAgent{Role: "builder", Name: "ori-legacy-builder", WorkspaceID: "w12", PaneID: "w12:p1", TerminalID: "term-1"}
	state := model.NewBridgeState()
	state.Features["repo-1:legacy"] = model.FeatureState{
		Feature: feature, WorkspaceID: "w12",
		Agents: map[string]model.RoleAgent{"builder": agent},
	}
	client := &fakeClient{
		agents:     []herdr.AgentInfo{{Name: agent.Name, AgentStatus: model.AgentIdle, WorkspaceID: "w12", PaneID: "w12:p1", TerminalID: "term-1"}},
		workspaces: []herdr.WorkspaceInfo{{WorkspaceID: "w12", Label: "legacy"}},
		tabs:       []herdr.TabInfo{{TabID: "w12:t1", WorkspaceID: "w12"}},
	}
	service := Service{
		Store:        fakeStore{state: state},
		Client:       client,
		Inspector:    fakeInspector{linked: worktree.GitWorktree{Path: feature.Path, Branch: feature.Branch, CommonDir: "/tmp/ori/.git"}},
		RepositoryID: feature.RepositoryID,
		GitCommonDir: "/tmp/ori/.git",
	}

	got := service.Preflight(context.Background(), Request{WorktreePath: feature.Path})
	if got.Outcome != OutcomeReady {
		t.Fatalf("outcome = %q, want ready: the Git half must not be held hostage by a legacy record (%#v)", got.Outcome, got)
	}
	if len(client.closeCalls) != 0 {
		t.Fatalf("cleanup closed %v for a workspace-backed feature", client.closeCalls)
	}
	if got.TabClosed || got.WorkspaceClosed {
		t.Fatalf("cleanup reported a close it did not perform: %#v", got)
	}
	if !strings.Contains(got.Detail, "w12") || !strings.Contains(got.Detail, "close it yourself") {
		t.Fatalf("detail = %q, want it to name the workspace the user must close", got.Detail)
	}
}

// The structural guarantee tab-scoped cleanup buys: a feature sharing a
// workspace with other features takes only its own tab with it.
func TestCleanupLeavesSiblingTabsAndTheWorkspaceAlone(t *testing.T) {
	feature := model.Feature{RepositoryID: "repo-1", Name: "bridge", Branch: "feature/bridge", Path: "/tmp/ori/bridge"}
	agent := model.RoleAgent{Role: "builder", Name: "ori-bridge-builder", WorkspaceID: "w1", PaneID: "w1:p3", TerminalID: "term-3"}
	state := model.NewBridgeState()
	state.Features["repo-1:bridge"] = model.FeatureState{
		Feature: feature, WorkspaceID: "w1", TabID: "w1:t3",
		Agents: map[string]model.RoleAgent{"builder": agent},
	}
	client := &fakeClient{
		agents:     []herdr.AgentInfo{{Name: agent.Name, AgentStatus: model.AgentIdle, WorkspaceID: "w1", PaneID: "w1:p3", TerminalID: "term-3"}},
		workspaces: []herdr.WorkspaceInfo{{WorkspaceID: "w1", Label: "shared", TabCount: 3}},
		tabs: []herdr.TabInfo{
			{TabID: "w1:t1", WorkspaceID: "w1", Label: "dev"},
			{TabID: "w1:t2", WorkspaceID: "w1", Label: "sibling-feature"},
			{TabID: "w1:t3", WorkspaceID: "w1", Label: "bridge"},
		},
	}
	service := Service{
		Store:        fakeStore{state: state},
		Client:       client,
		Inspector:    fakeInspector{linked: worktree.GitWorktree{Path: feature.Path, Branch: feature.Branch, CommonDir: "/tmp/ori/.git"}},
		RepositoryID: feature.RepositoryID,
		GitCommonDir: "/tmp/ori/.git",
	}

	got := service.Preflight(context.Background(), Request{WorktreePath: feature.Path})
	if got.Outcome != OutcomeReady || !got.TabClosed || got.TabID != "w1:t3" {
		t.Fatalf("preflight = %#v, want the feature's own tab closed", got)
	}
	if len(client.closeCalls) != 1 || client.closeCalls[0] != "w1:t3" {
		t.Fatalf("close calls = %v, want exactly the feature's tab", client.closeCalls)
	}
	if got.WorkspaceClosed {
		t.Fatal("cleanup closed a workspace; that call is no longer reachable from here")
	}
}

// A tab the user already closed by hand must not make the worktree
// un-removable: there is nothing left to orphan.
func TestCleanupProceedsWhenTheRecordedTabIsAlreadyGone(t *testing.T) {
	feature := model.Feature{RepositoryID: "repo-1", Name: "bridge", Branch: "feature/bridge", Path: "/tmp/ori/bridge"}
	state := model.NewBridgeState()
	state.Features["repo-1:bridge"] = model.FeatureState{
		Feature: feature, WorkspaceID: "w1", TabID: "w1:t9",
		Agents: map[string]model.RoleAgent{},
	}
	client := &fakeClient{
		workspaces: []herdr.WorkspaceInfo{{WorkspaceID: "w1"}},
		tabs:       []herdr.TabInfo{{TabID: "w1:t1", WorkspaceID: "w1"}},
	}
	service := Service{
		Store:        fakeStore{state: state},
		Client:       client,
		Inspector:    fakeInspector{linked: worktree.GitWorktree{Path: feature.Path, Branch: feature.Branch, CommonDir: "/tmp/ori/.git"}},
		RepositoryID: feature.RepositoryID,
		GitCommonDir: "/tmp/ori/.git",
	}
	got := service.Preflight(context.Background(), Request{WorktreePath: feature.Path})
	if got.Outcome != OutcomeReady || got.TabClosed || len(client.closeCalls) != 0 {
		t.Fatalf("preflight = %#v, closeCalls=%v", got, client.closeCalls)
	}
	if !strings.Contains(got.Detail, "no longer exists") {
		t.Fatalf("detail = %q", got.Detail)
	}
}
