package workspace

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// stubTaskHandler counts ExecuteTask calls so the test can prove how many
// times a step actually got dispatched. Each call blocks briefly to widen the
// race window between the two competing claim-attempts.
type stubTaskHandler struct {
	calls   atomic.Int64
	delay   time.Duration
	results []string
	mu      sync.Mutex
}

func (h *stubTaskHandler) ExecuteTask(_ context.Context, _ string, t Task) (string, error) {
	h.calls.Add(1)
	if h.delay > 0 {
		time.Sleep(h.delay)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.results = append(h.results, t.ID)
	return "ok", nil
}

func newSeededStore(t *testing.T) (*InMemoryStore, string, string, string) {
	t.Helper()
	store := NewInMemoryStore()
	ws := &Workspace{
		ID:     "ws1",
		Status: StatusActive,
		Workflows: map[string]Workflow{
			"wf1": {
				ID:     "wf1",
				Status: WorkflowStatusInProgress,
				Steps: []WorkflowStep{
					{
						ID:         "s1",
						Name:       "claim-target",
						Type:       StepTypeTask,
						Status:     StepStatusReady,
						AssignedTo: "agent-a",
					},
				},
			},
		},
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("seed save: %v", err)
	}
	return store, "ws1", "wf1", "s1"
}

// TestExecuteStep_AtomicClaimPreventsDoubleDispatch covers the cross-instance
// race that motivated this refactor: two StepExecutors sharing a store both
// see the step as Ready, both try to claim it, and only one should win.
//
// Before the SetStatus + Store.Update guard, both workers would write the
// step as InProgress (last-writer-wins) and dispatch ExecuteTask twice.
func TestExecuteStep_AtomicClaimPreventsDoubleDispatch(t *testing.T) {
	store, wsID, workflowID, stepID := newSeededStore(t)

	handler := &stubTaskHandler{delay: 30 * time.Millisecond}

	se1 := NewStepExecutor(store, handler, StepExecutorConfig{PollInterval: 1 * time.Hour})
	se2 := NewStepExecutor(store, handler, StepExecutorConfig{PollInterval: 1 * time.Hour})

	// Both workers grab their own snapshot of the workspace + workflow + step
	// and race to executeStep. The local runningSteps map only protects within
	// a single executor; the cross-executor race is what we want to verify.
	ws1, err := store.Get(wsID)
	if err != nil {
		t.Fatalf("get ws1: %v", err)
	}
	ws2, err := store.Get(wsID)
	if err != nil {
		t.Fatalf("get ws2: %v", err)
	}
	wf1, _ := ws1.GetWorkflow(workflowID)
	wf2, _ := ws2.GetWorkflow(workflowID)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		se1.executeStep(ws1, wf1, &wf1.Steps[0])
	}()
	go func() {
		defer wg.Done()
		se2.executeStep(ws2, wf2, &wf2.Steps[0])
	}()
	wg.Wait()

	// Wait for either executor's goroutine (whichever won) to finalize.
	se1.wg.Wait()
	se2.wg.Wait()

	if got := handler.calls.Load(); got != 1 {
		t.Fatalf("expected exactly one ExecuteTask call across both executors, got %d", got)
	}

	final, err := store.Get(wsID)
	if err != nil {
		t.Fatalf("final get: %v", err)
	}
	step := final.Workflows[workflowID].Steps[0]
	if step.Status != StepStatusCompleted {
		t.Fatalf("expected step to land Completed, got %q", step.Status)
	}
	if step.Result != "ok" {
		t.Fatalf("expected step result %q, got %q", "ok", step.Result)
	}
	_ = stepID
}

// TestUpdateStepStatuses_PromotesPendingToReadyWithoutDeps covers the basic
// dependency-resolution path under the new Store.Update wrapping.
func TestUpdateStepStatuses_PromotesPendingToReadyWithoutDeps(t *testing.T) {
	store := NewInMemoryStore()
	ws := &Workspace{
		ID:     "ws1",
		Status: StatusActive,
		Workflows: map[string]Workflow{
			"wf1": {
				ID:     "wf1",
				Status: WorkflowStatusInProgress,
				Steps: []WorkflowStep{
					{ID: "s1", Status: StepStatusPending, Type: StepTypeTask, AssignedTo: "a"},
				},
			},
		},
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("save: %v", err)
	}

	se := NewStepExecutor(store, &stubTaskHandler{}, StepExecutorConfig{PollInterval: 1 * time.Hour})
	se.updateStepStatuses("ws1", "wf1")

	out, err := store.Get("ws1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	got := out.Workflows["wf1"].Steps[0].Status
	if got != StepStatusReady {
		t.Fatalf("expected Ready, got %q", got)
	}
}

// TestCheckWorkflowCompletion_RollsUpWhenAllDone confirms the atomic
// workflow-finalize path now lives behind Store.Update and produces the
// expected terminal status.
func TestCheckWorkflowCompletion_RollsUpWhenAllDone(t *testing.T) {
	store := NewInMemoryStore()
	ws := &Workspace{
		ID:     "ws1",
		Status: StatusActive,
		Workflows: map[string]Workflow{
			"wf1": {
				ID:     "wf1",
				Status: WorkflowStatusInProgress,
				Steps: []WorkflowStep{
					{ID: "s1", Status: StepStatusCompleted},
					{ID: "s2", Status: StepStatusSkipped},
				},
			},
		},
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("save: %v", err)
	}

	se := NewStepExecutor(store, &stubTaskHandler{}, StepExecutorConfig{PollInterval: 1 * time.Hour})
	se.checkWorkflowCompletion("ws1", "wf1")

	out, err := store.Get("ws1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	wf := out.Workflows["wf1"]
	if wf.Status != WorkflowStatusCompleted {
		t.Fatalf("expected workflow Completed, got %q", wf.Status)
	}
	if wf.CompletedAt == nil {
		t.Fatal("expected CompletedAt to be set")
	}
}

func TestCheckWorkflowCompletion_FailsIfAnyStepFailed(t *testing.T) {
	store := NewInMemoryStore()
	ws := &Workspace{
		ID:     "ws1",
		Status: StatusActive,
		Workflows: map[string]Workflow{
			"wf1": {
				ID:     "wf1",
				Status: WorkflowStatusInProgress,
				Steps: []WorkflowStep{
					{ID: "s1", Status: StepStatusCompleted},
					{ID: "s2", Status: StepStatusFailed},
				},
			},
		},
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("save: %v", err)
	}

	se := NewStepExecutor(store, &stubTaskHandler{}, StepExecutorConfig{PollInterval: 1 * time.Hour})
	se.checkWorkflowCompletion("ws1", "wf1")

	out, _ := store.Get("ws1")
	if got := out.Workflows["wf1"].Status; got != WorkflowStatusFailed {
		t.Fatalf("expected Failed, got %q", got)
	}
}
