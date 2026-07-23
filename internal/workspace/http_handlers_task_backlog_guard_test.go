package workspace

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Regression tests for a gap found via a Group 7 cross-surface audit: two
// task-mutation HTTP handlers in this package had no RequireTaskNotBacklog
// guard, unlike the already-guarded orchestrationhttp execution endpoints —
// letting a Backlog item be executed or assigned through these paths despite
// PRD workspace-backlog's "zero backlog items become assigned, scheduled, or
// executable before an explicit Promote to Ready action" guarantee.

func newBacklogGuardTestWorkspace(t *testing.T, store Store) *Workspace {
	t.Helper()
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Guard Test"})
	task := Task{
		ID:          "backlog-task-1",
		Description: "uncommitted idea",
		Status:      TaskStatusBacklog,
		CreatedAt:   ws.CreatedAt,
	}
	if err := ws.AddTask(task); err != nil {
		t.Fatalf("add backlog task: %v", err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}
	return ws
}

func TestHTTPHandler_ExecuteTaskManually_RejectsBacklogTask(t *testing.T) {
	store := NewInMemoryStore()
	handler := NewHTTPHandler(store, nil, nil)
	ws := newBacklogGuardTestWorkspace(t, store)

	req := httptest.NewRequest(http.MethodPost,
		"/api/workspaces/"+ws.ID+"/tasks/backlog-task-1/execute", nil)
	rec := httptest.NewRecorder()

	handler.ExecuteTaskManually(rec, withTaskPath(req))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (Backlog task must not execute); body=%s", rec.Code, rec.Body.String())
	}
}

func TestHTTPHandler_UpdateTask_RejectsAssigningBacklogTask(t *testing.T) {
	store := NewInMemoryStore()
	handler := NewHTTPHandler(store, nil, nil)
	ws := newBacklogGuardTestWorkspace(t, store)

	body, _ := json.Marshal(map[string]any{"to": "Some Agent"})
	req := httptest.NewRequest(http.MethodPut,
		"/api/workspaces/"+ws.ID+"/tasks/backlog-task-1", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.UpdateTask(rec, withTaskPath(req))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (Backlog task must not gain an assignee); body=%s", rec.Code, rec.Body.String())
	}

	got, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}
	task, err := got.GetTask("backlog-task-1")
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task.To != "" {
		t.Fatalf("task.To = %q, want unchanged/empty after a rejected assignment attempt", task.To)
	}
}

// A plain field edit (no assignment/schedule) must still be allowed on a
// Backlog task through this generic endpoint — only assignment/schedule are
// blocked.
func TestHTTPHandler_UpdateTask_AllowsPlainFieldEditOnBacklogTask(t *testing.T) {
	store := NewInMemoryStore()
	handler := NewHTTPHandler(store, nil, nil)
	ws := newBacklogGuardTestWorkspace(t, store)

	body, _ := json.Marshal(map[string]any{"details": "more context"})
	req := httptest.NewRequest(http.MethodPut,
		"/api/workspaces/"+ws.ID+"/tasks/backlog-task-1", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.UpdateTask(rec, withTaskPath(req))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for a plain field edit; body=%s", rec.Code, rec.Body.String())
	}
}
