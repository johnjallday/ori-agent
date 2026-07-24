package cleanup

import (
	"context"
	"encoding/json"
	"errors"
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
	listErr    error
	closeErr   error
	closeCalls []string
}

func (c *fakeClient) AgentListInfo(context.Context) ([]herdr.AgentInfo, error) {
	return c.agents, c.listErr
}

func (c *fakeClient) WorkspaceClose(_ context.Context, workspaceID string) (json.RawMessage, error) {
	c.closeCalls = append(c.closeCalls, workspaceID)
	return json.RawMessage(`{"result":{"type":"workspace_closed"}}`), c.closeErr
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
		state.Features["repo-1:bridge"] = model.FeatureState{Feature: feature, WorkspaceID: "w1", Agents: map[string]model.RoleAgent{"builder": agent}, Schedules: map[string]model.Schedule{}}
		return state
	}
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
		{name: "idle agent closes workspace", state: baseState(), client: fakeClient{agents: []herdr.AgentInfo{live(model.AgentIdle)}}, want: OutcomeReady, wantClose: 1},
		{name: "done agent closes workspace", state: baseState(), client: fakeClient{agents: []herdr.AgentInfo{live(model.AgentDone)}}, want: OutcomeReady, wantClose: 1},
		{name: "working agent blocks cleanup", state: baseState(), client: fakeClient{agents: []herdr.AgentInfo{live(model.AgentWorking)}}, want: OutcomeBlocked},
		{name: "blocked agent blocks cleanup", state: baseState(), client: fakeClient{agents: []herdr.AgentInfo{live(model.AgentBlocked)}}, want: OutcomeBlocked},
		{name: "unknown agent needs override", state: baseState(), client: fakeClient{agents: []herdr.AgentInfo{live(model.AgentUnknown)}}, want: OutcomeUnavailable},
		{name: "missing agent needs override", state: baseState(), client: fakeClient{agents: nil}, want: OutcomeUnavailable},
		{name: "live list outage needs override", state: baseState(), client: fakeClient{listErr: errors.New("server unavailable")}, want: OutcomeUnavailable},
		{name: "live list outage can be explicitly overridden", state: baseState(), client: fakeClient{listErr: errors.New("server unavailable")}, override: true, want: OutcomeOverridden},
		{name: "workspace close failure needs override", state: baseState(), client: fakeClient{agents: []herdr.AgentInfo{live(model.AgentIdle)}, closeErr: errors.New("close unavailable")}, want: OutcomeCloseFailed, wantClose: 1},
		{name: "workspace close failure can be explicitly overridden", state: baseState(), client: fakeClient{agents: []herdr.AgentInfo{live(model.AgentIdle)}, closeErr: errors.New("close unavailable")}, override: true, want: OutcomeOverridden, wantClose: 1},
		{name: "pending schedule blocks before any close", state: withSchedule(baseState(), model.Schedule{ID: "sch-pending", State: model.SchedulePending}), client: fakeClient{agents: []herdr.AgentInfo{live(model.AgentIdle)}}, want: OutcomeBlocked, wantSchedule: true, wantCancelable: true},
		{name: "waiting schedule blocks before any close", state: withSchedule(baseState(), model.Schedule{ID: "sch-waiting", State: model.ScheduleWaiting}), client: fakeClient{agents: []herdr.AgentInfo{live(model.AgentIdle)}}, want: OutcomeBlocked, wantSchedule: true, wantCancelable: true},
		{name: "delivering schedule blocks before any close", state: withSchedule(baseState(), model.Schedule{ID: "sch-delivering", State: model.ScheduleDelivering}), client: fakeClient{agents: []herdr.AgentInfo{live(model.AgentIdle)}}, want: OutcomeBlocked, wantSchedule: true},
		{name: "uncertain schedule blocks before any close", state: withSchedule(baseState(), model.Schedule{ID: "sch-uncertain", State: model.ScheduleUncertain}), client: fakeClient{agents: []herdr.AgentInfo{live(model.AgentIdle)}}, want: OutcomeBlocked, wantSchedule: true},
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
				t.Fatalf("workspace close calls = %#v, want %d", client.closeCalls, test.wantClose)
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

func TestPreflightUsesCanonicalFeatureIdentityAndWorkspaceCloseOnly(t *testing.T) {
	feature := model.Feature{RepositoryID: "repo-1", Name: "bridge", Path: "/tmp/ori/bridge"}
	agent := model.RoleAgent{Role: "builder", Name: "ori-bridge-builder", WorkspaceID: "w1", PaneID: "w1:p1", TerminalID: "term-1"}
	state := model.NewBridgeState()
	state.Features["repo-1:other"] = model.FeatureState{Feature: model.Feature{RepositoryID: "repo-1", Name: "other", Path: "/tmp/ori/other"}, WorkspaceID: "w-other"}
	state.Features["repo-1:bridge"] = model.FeatureState{Feature: feature, WorkspaceID: "w1", Agents: map[string]model.RoleAgent{"builder": agent}}
	client := &fakeClient{agents: []herdr.AgentInfo{{Name: agent.Name, AgentStatus: model.AgentIdle, WorkspaceID: "w1", PaneID: "w1:p1", TerminalID: "term-1"}}}
	service := Service{
		Store:        fakeStore{state: state},
		Client:       client,
		Inspector:    fakeInspector{linked: worktree.GitWorktree{Path: feature.Path, CommonDir: "/tmp/ori/.git"}},
		RepositoryID: "repo-1",
		GitCommonDir: "/tmp/ori/.git",
	}
	got := service.Preflight(context.Background(), Request{WorktreePath: feature.Path})
	if got.Outcome != OutcomeReady || got.WorkspaceID != "w1" || len(client.closeCalls) != 1 || client.closeCalls[0] != "w1" {
		t.Fatalf("preflight = %#v, closeCalls=%#v", got, client.closeCalls)
	}
	if got.Agents[0].FocusCommand != "wt herd focus 'builder' --worktree '/tmp/ori/bridge'" || got.Agents[0].ReadCommand != "wt herd read 'builder' --worktree '/tmp/ori/bridge'" {
		t.Fatalf("agent recovery commands = %#v", got.Agents[0])
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
