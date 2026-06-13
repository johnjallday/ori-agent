package trigger

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// fakeWorkspaceStore is a minimal in-memory workspace.Store.
type fakeWorkspaceStore struct {
	mu sync.Mutex
	ws map[string]*workspace.Workspace
}

func newFakeWorkspaceStore(wss ...*workspace.Workspace) *fakeWorkspaceStore {
	s := &fakeWorkspaceStore{ws: make(map[string]*workspace.Workspace)}
	for _, w := range wss {
		s.ws[w.ID] = w
	}
	return s
}

func (s *fakeWorkspaceStore) Save(ws *workspace.Workspace) error { return nil }
func (s *fakeWorkspaceStore) Get(id string) (*workspace.Workspace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.ws[id]
	if !ok {
		return nil, fmt.Errorf("workspace %s not found", id)
	}
	return w, nil
}
func (s *fakeWorkspaceStore) List() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ids []string
	for id := range s.ws {
		ids = append(ids, id)
	}
	return ids, nil
}
func (s *fakeWorkspaceStore) Delete(id string) error { return nil }
func (s *fakeWorkspaceStore) ListActive() ([]*workspace.Workspace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*workspace.Workspace
	for _, w := range s.ws {
		out = append(out, w)
	}
	return out, nil
}
func (s *fakeWorkspaceStore) GetFilesPath(string) string   { return "" }
func (s *fakeWorkspaceStore) GetOutputsPath(string) string { return "" }
func (s *fakeWorkspaceStore) GetWorkspaceAgent(string, string) (*agent.Agent, bool, error) {
	return nil, false, nil
}
func (s *fakeWorkspaceStore) SaveWorkspaceAgent(string, string, *agent.Agent) error { return nil }
func (s *fakeWorkspaceStore) Lock(string) func()                                    { return func() {} }
func (s *fakeWorkspaceStore) Update(id string, fn func(*workspace.Workspace) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.ws[id]
	if !ok {
		return fmt.Errorf("workspace %s not found", id)
	}
	return fn(w)
}

// fakeMissionRunner records mission run invocations.
type fakeMissionRunner struct {
	mu    sync.Mutex
	calls []workspace.MissionRunOptions
	ords  []int
	err   error
}

func (f *fakeMissionRunner) TriggerMissionRunOpts(_ context.Context, _ string, ord int, opts workspace.MissionRunOptions) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, opts)
	f.ords = append(f.ords, ord)
	if f.err != nil {
		return "", f.err
	}
	return "run-123", nil
}

// fakeOppStore records Upserts.
type fakeOppStore struct {
	mu   sync.Mutex
	opps []workspace.Opportunity
}

func (f *fakeOppStore) List(string) ([]workspace.Opportunity, error) { return nil, nil }
func (f *fakeOppStore) Get(string, string) (workspace.Opportunity, error) {
	return workspace.Opportunity{}, workspace.ErrOpportunityNotFound
}
func (f *fakeOppStore) Upsert(o workspace.Opportunity) (workspace.Opportunity, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opps = append(f.opps, o)
	return o, false, nil
}
func (f *fakeOppStore) Delete(string, string) error                             { return nil }
func (f *fakeOppStore) MarkSeen(string, string) error                           { return nil }
func (f *fakeOppStore) Dismiss(string, string, workspace.DismissalReason) error { return nil }
func (f *fakeOppStore) Snooze(string, string, time.Time) error                  { return nil }
func (f *fakeOppStore) MarkResolved(string, string) error                       { return nil }

func webhookFire() PendingFire {
	return PendingFire{
		FireID:    "fire-1",
		Events:    []Event{{Kind: "webhook", Body: `{"action":"opened"}`, ContentType: "application/json", RemoteAddr: "127.0.0.1", Timestamp: time.Now()}},
		CreatedAt: time.Now(),
	}
}

func TestDispatchMissionRun(t *testing.T) {
	store, _ := newTestStore(t, "ws1")
	trg, _ := store.Create(webhookTrigger("ws1"))

	ws := &workspace.Workspace{ID: "ws1", Name: "W", MissionEnabled: true, MissionExecutionCount: 4}
	runner := &fakeMissionRunner{}
	d := NewDispatcher(store, newFakeWorkspaceStore(ws), runner, &fakeOppStore{})

	d.Dispatch(trg, webhookFire())

	if len(runner.calls) != 1 {
		t.Fatalf("mission runs = %d, want 1", len(runner.calls))
	}
	if runner.ords[0] != 5 {
		t.Errorf("cycle ordinal = %d, want MissionExecutionCount+1 = 5", runner.ords[0])
	}
	opts := runner.calls[0]
	if opts.Event == nil || opts.Event.TriggerName != "pr-opened" {
		t.Errorf("event context missing: %+v", opts.Event)
	}
	if opts.HoldCadence {
		t.Error("HoldCadence should be false when heartbeat is off")
	}

	got, _ := store.Get("ws1", trg.ID)
	if len(got.FireHistory) != 1 || got.FireHistory[0].RunID != "run-123" {
		t.Errorf("fire record missing run ID: %+v", got.FireHistory)
	}
}

func TestDispatchMissionRunHonorsHeartbeat(t *testing.T) {
	store, _ := newTestStore(t, "ws1")
	trg, _ := store.Create(webhookTrigger("ws1"))

	ws := &workspace.Workspace{ID: "ws1", MissionEnabled: true, MissionCadenceHeartbeat: true}
	runner := &fakeMissionRunner{}
	d := NewDispatcher(store, newFakeWorkspaceStore(ws), runner, nil)

	d.Dispatch(trg, webhookFire())

	if len(runner.calls) != 1 || !runner.calls[0].HoldCadence {
		t.Error("HoldCadence not propagated from MissionCadenceHeartbeat")
	}
}

func TestDispatchMissionDisabledRecordsFailureAndFinding(t *testing.T) {
	store, _ := newTestStore(t, "ws1")
	trg, _ := store.Create(webhookTrigger("ws1"))

	ws := &workspace.Workspace{ID: "ws1", MissionEnabled: false}
	runner := &fakeMissionRunner{}
	opps := &fakeOppStore{}
	d := NewDispatcher(store, newFakeWorkspaceStore(ws), runner, opps)

	d.Dispatch(trg, webhookFire())

	if len(runner.calls) != 0 {
		t.Error("mission must not run when disabled")
	}
	got, _ := store.Get("ws1", trg.ID)
	if got.FailureCount != 1 || got.LastError == "" {
		t.Errorf("failure not recorded: %+v", got)
	}
	if len(opps.opps) != 1 || !strings.Contains(opps.opps[0].Title, "pr-opened") {
		t.Errorf("Action Center finding not filed: %+v", opps.opps)
	}
}

func TestDispatchTaskPrompt(t *testing.T) {
	store, _ := newTestStore(t, "ws1")
	trg, err := store.Create(Trigger{
		WorkspaceID: "ws1",
		Name:        "invoice-drop",
		Type:        TypeWebhook,
		Enabled:     true,
		Action:      Action{Kind: ActionTaskPrompt, Agent: "bookkeeper", Prompt: "File the new invoice."},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	ws := &workspace.Workspace{ID: "ws1", SharedData: map[string]any{}}
	wsStore := newFakeWorkspaceStore(ws)
	d := NewDispatcher(store, wsStore, nil, nil)

	d.Dispatch(trg, webhookFire())

	if len(ws.Tasks) != 1 {
		t.Fatalf("tasks = %d, want 1", len(ws.Tasks))
	}
	task := ws.Tasks[0]
	if task.To != "bookkeeper" || task.Status != workspace.TaskStatusAssigned {
		t.Errorf("task not queued for executor: to=%q status=%q", task.To, task.Status)
	}
	if task.Context[TaskContextTriggerIDKey] != trg.ID || task.Context[TaskContextFireIDKey] != "fire-1" {
		t.Errorf("trigger context keys missing: %+v", task.Context)
	}
	if !strings.Contains(task.Description, "File the new invoice.") || !strings.Contains(task.Description, "TRIGGERING EVENT") {
		t.Errorf("task description missing prompt or event block:\n%s", task.Description)
	}

	got, _ := store.Get("ws1", trg.ID)
	if len(got.FireHistory) != 1 || got.FireHistory[0].TaskID != task.ID {
		t.Errorf("fire record missing task ID: %+v", got.FireHistory)
	}
}

func TestBuildEventContextTestKind(t *testing.T) {
	trg := Trigger{Name: "t", Type: TypeWebhook}
	fire := PendingFire{FireID: "f", Events: []Event{{Kind: "test", Timestamp: time.Now()}}}
	ctx := buildEventContext(trg, fire, time.Now())
	if ctx.TriggerType != "test" {
		t.Errorf("test fires should be labeled test, got %q", ctx.TriggerType)
	}
}
