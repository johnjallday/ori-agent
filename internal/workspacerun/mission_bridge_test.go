package workspacerun

import (
	"context"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// stubAgentReader implements agentStoreReader without pulling in the real
// agent store. Returns whatever agent it was constructed with for any name.
type stubAgentReader struct {
	a *agent.Agent
}

func (s *stubAgentReader) GetAgent(name string) (*agent.Agent, bool) {
	if s.a == nil {
		return nil, false
	}
	return s.a, true
}

func makeMissionWorkspace(t *testing.T, store workspace.Store) *workspace.Workspace {
	t.Helper()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Brand"})
	ws.Mission = "Keep brand consistent."
	ws.MissionEnabled = true
	ws.AutonomyPolicy = workspace.AutonomyPropose
	// Add the entry agent so EntryAgentName() returns it.
	if err := ws.AddAgent("brand-manager"); err != nil {
		t.Fatalf("AddAgent: %v", err)
	}
	if err := ws.SetEntryAgentName("brand-manager"); err != nil {
		t.Fatalf("SetEntryAgentName: %v", err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("Save workspace: %v", err)
	}
	return ws
}

func newTestMissionBridge(t *testing.T, runResult string, runErr error) (*MissionBridge, workspace.Store, *workspace.Workspace) {
	t.Helper()
	runStore := NewMemoryStore()
	wsStore := workspace.NewInMemoryStore()
	ws := makeMissionWorkspace(t, wsStore)
	executors := NewExecutorRegistry()
	executors.Register(ExecutorKindOriAgent, NewOriAgentExecutor(&stubOriAgentTaskRunner{result: runResult, err: runErr}))
	service := NewService(runStore, NewProfileRegistry(), executors, NewLocalEnvironmentManager(t.TempDir()), NewValidator(), nil)
	opps := workspace.NewOpportunityStore(wsStore)
	bridge := NewMissionBridge(MissionBridgeConfig{
		RunStore:         runStore,
		Service:          service,
		WorkspaceStore:   wsStore,
		Agents:           &stubAgentReader{a: &agent.Agent{Settings: types.Settings{SystemPrompt: "You are the brand manager."}}},
		OpportunityStore: opps,
	})
	if bridge == nil {
		t.Fatal("NewMissionBridge returned nil")
	}
	return bridge, wsStore, ws
}

func TestMissionBridge_CreatesRunWithOriginMetadata(t *testing.T) {
	bridge, wsStore, ws := newTestMissionBridge(t, `{"findings": []}`, nil)

	runID, err := bridge.TriggerMissionRun(context.Background(), ws.ID, 1)
	if err != nil {
		t.Fatalf("TriggerMissionRun: %v", err)
	}
	if runID == "" {
		t.Fatal("expected run ID")
	}

	run, err := bridge.store.GetRun(context.Background(), ws.ID, runID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if run.OriginType != OriginMission {
		t.Errorf("OriginType = %q; want %q", run.OriginType, OriginMission)
	}
	if run.CycleOrdinal != 1 {
		t.Errorf("CycleOrdinal = %d; want 1", run.CycleOrdinal)
	}
	if run.Executor.Ref != "brand-manager" {
		t.Errorf("Executor.Ref = %q; want brand-manager", run.Executor.Ref)
	}
	if run.Status != RunStatusSucceeded {
		t.Errorf("Status = %q; want succeeded", run.Status)
	}

	// Workspace mission counters should have advanced.
	fresh, _ := wsStore.Get(ws.ID)
	if fresh.MissionExecutionCount != 1 {
		t.Errorf("MissionExecutionCount = %d; want 1", fresh.MissionExecutionCount)
	}
	if fresh.MissionFailureCount != 0 {
		t.Errorf("MissionFailureCount = %d; want 0 on success", fresh.MissionFailureCount)
	}
	if fresh.LastMissionRunAt == nil {
		t.Error("LastMissionRunAt should be set after a successful run")
	}
}

func TestMissionBridge_ParsesAndUpsertsOpportunities(t *testing.T) {
	out := `{"findings": [
		{"title": "Brand voice drift", "priority": "high", "summary": "3 posts diverge"},
		{"title": "Missing alt text",   "priority": "medium"}
	]}`
	bridge, wsStore, ws := newTestMissionBridge(t, out, nil)

	if _, err := bridge.TriggerMissionRun(context.Background(), ws.ID, 1); err != nil {
		t.Fatalf("TriggerMissionRun: %v", err)
	}

	fresh, _ := wsStore.Get(ws.ID)
	if len(fresh.Opportunities) != 2 {
		t.Fatalf("expected 2 opportunities; got %d", len(fresh.Opportunities))
	}
	titles := map[string]bool{}
	for _, o := range fresh.Opportunities {
		titles[o.Title] = true
		if o.SourceRunID == "" {
			t.Errorf("opportunity %q missing SourceRunID", o.Title)
		}
		if o.Status != workspace.OpportunityNew {
			t.Errorf("opportunity %q status = %q; want new", o.Title, o.Status)
		}
	}
	if !titles["Brand voice drift"] || !titles["Missing alt text"] {
		t.Errorf("expected both findings persisted; got titles %v", titles)
	}
}

func TestMissionBridge_MergesDuplicateAcrossCycles(t *testing.T) {
	// First cycle reports a finding.
	bridge, wsStore, ws := newTestMissionBridge(t, `{"findings": [{"title": "Brand voice drift", "priority": "medium"}]}`, nil)
	if _, err := bridge.TriggerMissionRun(context.Background(), ws.ID, 1); err != nil {
		t.Fatalf("first run: %v", err)
	}

	// Swap the stub runner's result for the second cycle by re-wiring the
	// executor registry on the same service via a new bridge instance that
	// reuses the workspace + opp store.
	runStore := NewMemoryStore()
	executors := NewExecutorRegistry()
	executors.Register(ExecutorKindOriAgent, NewOriAgentExecutor(&stubOriAgentTaskRunner{
		result: `{"findings": [{"title": "Brand-voice drift!", "priority": "high"}]}`,
	}))
	service := NewService(runStore, NewProfileRegistry(), executors, NewLocalEnvironmentManager(t.TempDir()), NewValidator(), nil)
	opps := workspace.NewOpportunityStore(wsStore)
	secondBridge := NewMissionBridge(MissionBridgeConfig{
		RunStore: runStore, Service: service, WorkspaceStore: wsStore, OpportunityStore: opps,
	})
	if _, err := secondBridge.TriggerMissionRun(context.Background(), ws.ID, 2); err != nil {
		t.Fatalf("second run: %v", err)
	}

	fresh, _ := wsStore.Get(ws.ID)
	if len(fresh.Opportunities) != 1 {
		t.Fatalf("expected dedup-merge to keep 1 opportunity; got %d", len(fresh.Opportunities))
	}
	if fresh.Opportunities[0].Priority != "high" {
		t.Errorf("merged priority = %q; want high (highest wins)", fresh.Opportunities[0].Priority)
	}
}

func TestMissionBridge_RecordsFailureWhenExecutorErrors(t *testing.T) {
	bridge, wsStore, ws := newTestMissionBridge(t, "", errStubExecution)

	_, err := bridge.TriggerMissionRun(context.Background(), ws.ID, 1)
	if err == nil {
		t.Fatal("expected error when executor errors")
	}
	if !strings.Contains(err.Error(), "execute mission run") {
		t.Errorf("unexpected error wrap: %v", err)
	}
	fresh, _ := wsStore.Get(ws.ID)
	if fresh.MissionExecutionCount != 1 {
		t.Errorf("execution count = %d; want 1 (failed run still counts as attempted)", fresh.MissionExecutionCount)
	}
	if fresh.MissionFailureCount != 1 {
		t.Errorf("failure count = %d; want 1", fresh.MissionFailureCount)
	}
}

func TestMissionBridge_NoEntryAgentReturnsError(t *testing.T) {
	runStore := NewMemoryStore()
	wsStore := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "X"})
	// Note: no entry agent set.
	if err := wsStore.Save(ws); err != nil {
		t.Fatalf("Save: %v", err)
	}
	executors := NewExecutorRegistry()
	executors.Register(ExecutorKindOriAgent, NewOriAgentExecutor(&stubOriAgentTaskRunner{result: "{}"}))
	service := NewService(runStore, NewProfileRegistry(), executors, NewLocalEnvironmentManager(t.TempDir()), NewValidator(), nil)
	opps := workspace.NewOpportunityStore(wsStore)
	bridge := NewMissionBridge(MissionBridgeConfig{
		RunStore: runStore, Service: service, WorkspaceStore: wsStore, OpportunityStore: opps,
	})

	_, err := bridge.TriggerMissionRun(context.Background(), ws.ID, 1)
	if err == nil || !strings.Contains(err.Error(), "no entry agent") {
		t.Errorf("expected entry-agent error; got %v", err)
	}
}

func TestNewMissionBridge_RejectsPartialConfig(t *testing.T) {
	// Missing required dependencies must return nil so the caller can refuse
	// to wire a half-built bridge into the scheduler.
	cases := []MissionBridgeConfig{
		{Service: nil, WorkspaceStore: workspace.NewInMemoryStore(), OpportunityStore: workspace.NewOpportunityStore(workspace.NewInMemoryStore()), RunStore: NewMemoryStore()},
		{Service: &Service{}, WorkspaceStore: nil, OpportunityStore: workspace.NewOpportunityStore(workspace.NewInMemoryStore()), RunStore: NewMemoryStore()},
	}
	for i, c := range cases {
		if b := NewMissionBridge(c); b != nil {
			t.Errorf("case %d: expected nil bridge for partial config", i)
		}
	}
}

func TestBuildMissionRunPolicy(t *testing.T) {
	watch := buildMissionRunPolicy(workspace.AutonomyWatch)
	if watch.Mutation != PolicyMutationDenied || watch.ExternalEffects != PolicyExternalEffectsDenied {
		t.Errorf("Watch policy = %+v; want mutation=denied external=denied", watch)
	}
	propose := buildMissionRunPolicy(workspace.AutonomyPropose)
	if propose.Mutation != PolicyMutationAllowed || propose.ExternalEffects != PolicyExternalEffectsDenied {
		t.Errorf("Propose policy = %+v; want mutation=allowed external=denied", propose)
	}
}

// errStubExecution is a sentinel error used by the failure test to assert
// the bridge propagates executor errors. Declared at package level so the
// stub runner can return it without import gymnastics.
var errStubExecution = errStubExec("simulated executor failure")

type errStubExec string

func (e errStubExec) Error() string { return string(e) }
