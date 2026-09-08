package workspace

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/resetstate"
)

func TestResetAdmissionLegacyWorkspaceHTTPTransfersBeforeResponse(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	}()
	ws := newWorkspaceWithTasks(t, []Task{{ID: "task", To: "agent-a", Status: TaskStatusAssigned}})
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	worker := &resetPausedTaskHandler{started: make(chan context.Context, 1), release: make(chan struct{})}
	gate := &resetstate.WorkGate{}
	orchestrator := NewOrchestrator(store, nil, nil, nil)
	orchestrator.SetAdmissionGate(gate)
	orchestrator.SetTaskHandler(worker)
	handler := NewHTTPHandler(store, orchestrator, nil)
	handler.SetAdmissionGate(gate)
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/owned/tasks/task/execute", nil).WithContext(ctx)
	req.SetPathValue("workspaceID", ws.ID)
	req.SetPathValue("taskId", "task")
	response := httptest.NewRecorder()
	handler.ExecuteTaskManually(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("execute status = %d: %s", response.Code, response.Body.String())
	}
	workCtx := resetWait(t, worker.started)
	cancel()
	if err := gate.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
		t.Fatalf("HTTP child not tracked: %v", err)
	}
	select {
	case <-workCtx.Done():
		t.Fatalf("response cancellation stopped accepted task: %v", workCtx.Err())
	default:
	}
	finish := sync.OnceFunc(func() { close(worker.release) })
	t.Cleanup(finish)
	finish()
	deadline := time.Now().Add(5 * time.Second)
	for gate.Snapshot().Active != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	persisted, err := store.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := persisted.GetTask("task")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != TaskStatusCompleted || task.Result != "finished normally" {
		t.Fatalf("lost accepted task: %+v", task)
	}
}
