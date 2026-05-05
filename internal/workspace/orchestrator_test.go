package workspace

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// orchestratorTestSetup seeds a workspace store with a single workspace that
// contains the orchestrator + a single test agent and one pending task.
// Returns the store, the workspace ID, and the seeded task ID.
func orchestratorTestSetup(t *testing.T) (Store, string, string) {
	t.Helper()
	store := newExecutorTestStore(t)
	ws := NewWorkspace(CreateWorkspaceParams{
		Name:   "orchestrator-test",
		Agents: []string{"orchestrator", "agent-a"},
	})
	task := Task{
		ID:          "task-1",
		To:          "agent-a",
		Description: "do the thing",
		Status:      TaskStatusPending,
	}
	if err := ws.AddTask(task); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}
	return store, ws.ID, task.ID
}

func TestOrchestrator_ExecuteTask_DelegatesToHandler(t *testing.T) {
	store, wsID, taskID := orchestratorTestSetup(t)
	handler := &fakeTaskHandler{}

	o := NewOrchestrator(store, nil, nil, nil)
	o.SetTaskHandler(handler)

	task := Task{ID: taskID, To: "agent-a", Description: "do the thing"}
	if err := o.ExecuteTask(context.Background(), wsID, task); err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}
	if got := handler.calls; got != 1 {
		t.Fatalf("expected 1 delegation call, got %d", got)
	}
	if seen := handler.seenIDs[taskID]; seen != 1 {
		t.Fatalf("expected handler to see task %q once, saw %d", taskID, seen)
	}

	// Reload from store and assert the task was marked completed with the result
	// from the stub.
	got, err := store.Get(wsID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	final := getTaskOrNil(t, got, taskID)
	if final == nil {
		t.Fatalf("task %q missing after ExecuteTask", taskID)
	}
	if final.Status != TaskStatusCompleted {
		t.Fatalf("expected status %q, got %q", TaskStatusCompleted, final.Status)
	}
	if final.Result != "ok" {
		t.Fatalf("expected result %q (from fakeTaskHandler), got %q", "ok", final.Result)
	}
}

// erroringTaskHandler returns a fixed error from ExecuteTask.
type erroringTaskHandler struct{ err error }

func (h *erroringTaskHandler) ExecuteTask(_ context.Context, _ string, _ Task) (string, error) {
	return "", h.err
}

func TestOrchestrator_ExecuteTask_PropagatesError(t *testing.T) {
	store, wsID, taskID := orchestratorTestSetup(t)
	wantErr := errors.New("boom")
	handler := &erroringTaskHandler{err: wantErr}

	o := NewOrchestrator(store, nil, nil, nil)
	o.SetTaskHandler(handler)

	task := Task{ID: taskID, To: "agent-a", Description: "do the thing"}
	err := o.ExecuteTask(context.Background(), wsID, task)
	if !errors.Is(err, wantErr) && err != wantErr {
		// ExecuteTask returns the handler error directly today.
		t.Fatalf("expected error %v, got %v", wantErr, err)
	}

	got, err := store.Get(wsID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	final := getTaskOrNil(t, got, taskID)
	if final == nil {
		t.Fatalf("task %q missing after ExecuteTask", taskID)
	}
	if final.Status != TaskStatusFailed {
		t.Fatalf("expected status %q, got %q", TaskStatusFailed, final.Status)
	}
	if !strings.Contains(final.Error, "boom") {
		t.Fatalf("expected task error to contain 'boom', got %q", final.Error)
	}
}

func TestOrchestrator_ExecuteTask_PublishesLifecycleEvents(t *testing.T) {
	store, wsID, taskID := orchestratorTestSetup(t)
	bus := NewEventBus(16, 64)
	handler := &fakeTaskHandler{}

	o := NewOrchestrator(store, nil, nil, bus)
	o.SetTaskHandler(handler)

	task := Task{ID: taskID, To: "agent-a", Description: "do the thing"}
	if err := o.ExecuteTask(context.Background(), wsID, task); err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}

	// Pull from event history. EventBus.GetHistory returns events in reverse
	// chronological order, so we expect the lifecycle in reverse below.
	hist := bus.GetHistory(nil, 100)
	wantReversed := []EventType{"task_completed", "message_sent", "task_started"}
	got := []EventType{}
	for _, ev := range hist {
		if slices.Contains(wantReversed, ev.Type) {
			got = append(got, ev.Type)
		}
	}
	if len(got) != len(wantReversed) {
		t.Fatalf("expected events %v in reverse-chronological history, got %v (full history: %v)", wantReversed, got, eventTypes(hist))
	}
	for i, w := range wantReversed {
		if got[i] != w {
			t.Fatalf("event %d: expected %q, got %q (sequence: %v)", i, w, got[i], got)
		}
	}
}

func TestOrchestrator_ExecuteTask_HandlerNil(t *testing.T) {
	store, wsID, taskID := orchestratorTestSetup(t)

	o := NewOrchestrator(store, nil, nil, nil) // SetTaskHandler intentionally not called

	task := Task{ID: taskID, To: "agent-a", Description: "do the thing"}
	err := o.ExecuteTask(context.Background(), wsID, task)
	if err == nil {
		t.Fatal("expected error when task handler is not configured, got nil")
	}
	if !strings.Contains(err.Error(), "task handler not configured") {
		t.Fatalf("expected error to mention 'task handler not configured', got %q", err.Error())
	}

	// Task should be marked failed in the store and the failure event should mention the same.
	got, err := store.Get(wsID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	final := getTaskOrNil(t, got, taskID)
	if final == nil {
		t.Fatalf("task %q missing after ExecuteTask", taskID)
	}
	if final.Status != TaskStatusFailed {
		t.Fatalf("expected status %q, got %q", TaskStatusFailed, final.Status)
	}
}

func eventTypes(events []Event) []EventType {
	out := make([]EventType, 0, len(events))
	for _, ev := range events {
		out = append(out, ev.Type)
	}
	return out
}

func getTaskOrNil(t *testing.T, ws *Workspace, taskID string) *Task {
	t.Helper()
	task, err := ws.GetTask(taskID)
	if err != nil {
		return nil
	}
	return task
}

// Compile-time assertion: LLMTaskHandler satisfies the orchestrator's
// taskExecutor interface. If the signature drifts, this fails to build —
// catching the regression at compile time rather than runtime.
var _ taskExecutor = (*LLMTaskHandler)(nil)
