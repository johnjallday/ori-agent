package workspace

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// stubMissionTrigger records calls so tests can assert on what fired with
// what arguments. Returns a configurable run ID and optional error.
type stubMissionTrigger struct {
	mu          sync.Mutex
	calls       []stubCall
	returnRunID string
	returnErr   error
	// onCall lets a test simulate the trigger advancing mission tracking
	// fields as the real bridge would; nil means "don't touch the workspace".
	onCall func(workspaceID string, cycleOrdinal int)
}

type stubCall struct {
	WorkspaceID  string
	CycleOrdinal int
}

func (s *stubMissionTrigger) TriggerMissionRun(ctx context.Context, workspaceID string, cycleOrdinal int) (string, error) {
	s.mu.Lock()
	s.calls = append(s.calls, stubCall{WorkspaceID: workspaceID, CycleOrdinal: cycleOrdinal})
	s.mu.Unlock()
	if s.onCall != nil {
		s.onCall(workspaceID, cycleOrdinal)
	}
	return s.returnRunID, s.returnErr
}

func (s *stubMissionTrigger) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func newSchedulerWithStub(t *testing.T) (*TaskScheduler, *InMemoryStore, *stubMissionTrigger) {
	t.Helper()
	store := NewInMemoryStore()
	ts := NewTaskScheduler(store, SchedulerConfig{PollInterval: time.Minute})
	trig := &stubMissionTrigger{returnRunID: "run-1"}
	ts.SetMissionTrigger(trig)
	return ts, store, trig
}

func TestCheckMissionCadence_SkipsDisabled(t *testing.T) {
	ts, _, trig := newSchedulerWithStub(t)
	past := time.Now().Add(-time.Hour)
	ws := &Workspace{ID: "ws-1", MissionEnabled: false, NextMissionRunAt: &past}
	ts.checkMissionCadence(ws, time.Now())
	if trig.callCount() != 0 {
		t.Errorf("disabled mission should not fire; got %d calls", trig.callCount())
	}
}

func TestCheckMissionCadence_SkipsWhenNoNextRun(t *testing.T) {
	ts, _, trig := newSchedulerWithStub(t)
	ws := &Workspace{ID: "ws-1", MissionEnabled: true, NextMissionRunAt: nil}
	ts.checkMissionCadence(ws, time.Now())
	if trig.callCount() != 0 {
		t.Errorf("missing next-run should not fire; got %d calls", trig.callCount())
	}
}

func TestCheckMissionCadence_SkipsWhenFutureNextRun(t *testing.T) {
	ts, _, trig := newSchedulerWithStub(t)
	future := time.Now().Add(time.Hour)
	ws := &Workspace{ID: "ws-1", MissionEnabled: true, NextMissionRunAt: &future}
	ts.checkMissionCadence(ws, time.Now())
	if trig.callCount() != 0 {
		t.Errorf("future next-run should not fire; got %d calls", trig.callCount())
	}
}

func TestCheckMissionCadence_NoTriggerConfigured(t *testing.T) {
	store := NewInMemoryStore()
	ts := NewTaskScheduler(store, SchedulerConfig{PollInterval: time.Minute})
	// no SetMissionTrigger call
	past := time.Now().Add(-time.Hour)
	ws := &Workspace{ID: "ws-1", MissionEnabled: true, NextMissionRunAt: &past}
	// Must not panic.
	ts.checkMissionCadence(ws, time.Now())
}

func TestCheckMissionCadence_FiresAndPassesCycleOrdinal(t *testing.T) {
	ts, store, trig := newSchedulerWithStub(t)
	now := time.Now()
	past := now.Add(-time.Hour)
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Brand"})
	ws.MissionEnabled = true
	ws.MissionExecutionCount = 4 // next run is #5
	ws.NextMissionRunAt = &past
	ws.Cadence = &ScheduleConfig{Type: ScheduleDaily, TimeOfDay: "09:00"}
	// Simulate the bridge advancing state inside the trigger so the
	// belt-and-braces guard doesn't kick in.
	trig.onCall = func(workspaceID string, cycleOrdinal int) {
		_ = store.Update(workspaceID, func(w *Workspace) error {
			ApplyMissionRunOutcome(w, MissionRunOutcome{StartedAt: now, Succeeded: true})
			return nil
		})
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("Save: %v", err)
	}

	ts.checkMissionCadence(ws, now)

	if trig.callCount() != 1 {
		t.Fatalf("expected 1 trigger call; got %d", trig.callCount())
	}
	if trig.calls[0].CycleOrdinal != 5 {
		t.Errorf("cycle ordinal = %d; want 5 (execution count + 1)", trig.calls[0].CycleOrdinal)
	}
	if trig.calls[0].WorkspaceID != ws.ID {
		t.Errorf("workspace ID mismatch: %q vs %q", trig.calls[0].WorkspaceID, ws.ID)
	}
}

func TestCheckMissionCadence_FailureAdvancesStateToPreventStorm(t *testing.T) {
	ts, store, trig := newSchedulerWithStub(t)
	trig.returnErr = errors.New("bridge offline")
	now := time.Now()
	past := now.Add(-time.Hour)

	ws := NewWorkspace(CreateWorkspaceParams{Name: "Brand"})
	ws.MissionEnabled = true
	ws.NextMissionRunAt = &past
	ws.Cadence = &ScheduleConfig{Type: ScheduleDaily, TimeOfDay: "09:00"}
	if err := store.Save(ws); err != nil {
		t.Fatalf("Save: %v", err)
	}

	ts.checkMissionCadence(ws, now)

	got, _ := store.Get(ws.ID)
	if got.MissionFailureCount != 1 {
		t.Errorf("failure count = %d; want 1", got.MissionFailureCount)
	}
	if got.MissionExecutionCount != 1 {
		t.Errorf("execution count = %d; want 1 (failure still counts as attempted)", got.MissionExecutionCount)
	}
	if got.NextMissionRunAt == nil || !got.NextMissionRunAt.After(now) {
		t.Errorf("next run not advanced past now to prevent storm: %v", got.NextMissionRunAt)
	}
}

func TestCheckMissionCadence_FailureDoesNotDoubleCountWhenTriggerRecorded(t *testing.T) {
	ts, store, trig := newSchedulerWithStub(t)
	now := time.Now()
	past := now.Add(-time.Hour)

	ws := NewWorkspace(CreateWorkspaceParams{Name: "Brand"})
	ws.MissionEnabled = true
	ws.NextMissionRunAt = &past
	ws.Cadence = &ScheduleConfig{Type: ScheduleDaily, TimeOfDay: "09:00"}
	if err := store.Save(ws); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Simulate the real bridge on an execution failure: the run was created, so
	// the bridge records the failed outcome (advancing NextMissionRunAt) and
	// then returns an error. The scheduler must NOT record the outcome again,
	// or the failure is counted twice.
	trig.returnErr = errors.New("execute mission run: boom")
	trig.onCall = func(workspaceID string, cycleOrdinal int) {
		_ = store.Update(workspaceID, func(w *Workspace) error {
			ApplyMissionRunOutcome(w, MissionRunOutcome{StartedAt: now, Succeeded: false})
			return nil
		})
	}

	ts.checkMissionCadence(ws, now)

	got, _ := store.Get(ws.ID)
	if got.MissionExecutionCount != 1 {
		t.Errorf("execution count = %d; want 1 (no double-count)", got.MissionExecutionCount)
	}
	if got.MissionFailureCount != 1 {
		t.Errorf("failure count = %d; want 1 (no double-count)", got.MissionFailureCount)
	}
	if got.NextMissionRunAt == nil || !got.NextMissionRunAt.After(now) {
		t.Errorf("next run should remain advanced: %v", got.NextMissionRunAt)
	}
}

func TestTriggerMissionManually_NoTriggerConfigured(t *testing.T) {
	store := NewInMemoryStore()
	ts := NewTaskScheduler(store, SchedulerConfig{PollInterval: time.Minute})
	ws := NewWorkspace(CreateWorkspaceParams{Name: "X"})
	_ = store.Save(ws)
	_, err := ts.TriggerMissionManually(context.Background(), ws.ID)
	if !errors.Is(err, ErrMissionTriggerNotConfigured) {
		t.Errorf("err = %v; want ErrMissionTriggerNotConfigured", err)
	}
}

func TestTriggerMissionManually_RejectsUnclassifiedBindings(t *testing.T) {
	ts, store, _ := newSchedulerWithStub(t)
	ws := NewWorkspace(CreateWorkspaceParams{Name: "X"})
	ws.MCPBindings = []WorkspaceMCPBinding{
		{ID: "b-unclassified", Enabled: true}, // no DefaultSideEffect
	}
	_ = store.Save(ws)

	_, err := ts.TriggerMissionManually(context.Background(), ws.ID)
	if !errors.Is(err, ErrMissionBindingsUnclassified) {
		t.Errorf("err = %v; want ErrMissionBindingsUnclassified", err)
	}
}

func TestTriggerMissionManually_FiresWithCorrectOrdinal(t *testing.T) {
	ts, store, trig := newSchedulerWithStub(t)
	ws := NewWorkspace(CreateWorkspaceParams{Name: "X"})
	ws.MissionExecutionCount = 2
	ws.MCPBindings = []WorkspaceMCPBinding{
		{ID: "b-1", Enabled: true, DefaultSideEffect: SideEffectRead},
	}
	_ = store.Save(ws)

	runID, err := ts.TriggerMissionManually(context.Background(), ws.ID)
	if err != nil {
		t.Fatalf("TriggerMissionManually: %v", err)
	}
	if runID != "run-1" {
		t.Errorf("run ID = %q; want %q", runID, "run-1")
	}
	if trig.callCount() != 1 {
		t.Fatalf("trigger call count = %d; want 1", trig.callCount())
	}
	if trig.calls[0].CycleOrdinal != 3 {
		t.Errorf("cycle ordinal = %d; want 3 (execution count + 1)", trig.calls[0].CycleOrdinal)
	}
}
