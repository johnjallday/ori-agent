package cliagenthttp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/cliagent"
	"github.com/johnjallday/ori-agent/internal/resetstate"
)

func TestHandleListAgents(t *testing.T) {
	registry := cliagent.NewRegistry()
	handler := NewHandler(nil, registry, cliagent.NewEventLogger(t.TempDir()))

	req := httptest.NewRequest(http.MethodGet, "/api/cli-agents", nil)
	w := httptest.NewRecorder()

	handler.HandleListAgents(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected json content type, got %s", ct)
	}
}

func TestHandleListAgents_WrongMethod(t *testing.T) {
	handler := NewHandler(nil, cliagent.NewRegistry(), cliagent.NewEventLogger(t.TempDir()))

	req := httptest.NewRequest(http.MethodPost, "/api/cli-agents", nil)
	w := httptest.NewRecorder()

	handler.HandleListAgents(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleGetTask_NotFound(t *testing.T) {
	executor := &cliagent.MicroStepExecutor{} // zero-value is ok for this test
	handler := NewHandler(nil, cliagent.NewRegistry(), cliagent.NewEventLogger(t.TempDir()))
	handler.executor = executor

	req := httptest.NewRequest(http.MethodGet, "/api/cli-agents/tasks/nonexistent", nil)
	w := httptest.NewRecorder()

	handler.HandleGetTask(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestHandleGetTask_Found(t *testing.T) {
	handler := NewHandler(nil, cliagent.NewRegistry(), cliagent.NewEventLogger(t.TempDir()))
	handler.results["task123"] = &cliagent.TaskResult{
		TaskID:        "task123",
		Status:        cliagent.TaskCompleted,
		Summary:       "Done",
		StepsExecuted: 2,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/cli-agents/tasks/task123", nil)
	w := httptest.NewRecorder()

	handler.HandleGetTask(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandleStopTask_NotFound(t *testing.T) {
	registry := cliagent.NewRegistry()
	planner := cliagent.NewStepPlanner(nil, "")
	executor := cliagent.NewMicroStepExecutor(registry, planner, cliagent.NewEventLogger(t.TempDir()), cliagent.NewDiffDetector(), nil)
	handler := NewHandler(executor, registry, cliagent.NewEventLogger(t.TempDir()))

	req := httptest.NewRequest(http.MethodPost, "/api/cli-agents/tasks/nonexistent/stop", nil)
	w := httptest.NewRecorder()

	handler.HandleStopTask(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestExtractTaskID(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"/api/cli-agents/tasks/abc123", "abc123"},
		{"/api/cli-agents/tasks/", ""},
		{"/api/cli-agents", ""},
		{"tasks/xyz", "xyz"},
	}
	for _, tt := range tests {
		got := extractTaskID(tt.path)
		if got != tt.expected {
			t.Errorf("extractTaskID(%q) = %q, want %q", tt.path, got, tt.expected)
		}
	}
}

func TestHandleCreateTask_WrongMethod(t *testing.T) {
	handler := NewHandler(nil, cliagent.NewRegistry(), cliagent.NewEventLogger(t.TempDir()))

	req := httptest.NewRequest(http.MethodGet, "/api/cli-agents/tasks", nil)
	w := httptest.NewRecorder()

	handler.HandleCreateTask(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

// The 202 must name the task: the ID it returns is the one GET reports on,
// first while the task runs and then with its final result.
func TestHandleCreateTask_ReturnsPollableTaskID(t *testing.T) {
	release := make(chan struct{})
	registry := cliagent.NewRegistry(&resetPausedAdapter{release: release})
	executor := cliagent.NewMicroStepExecutor(registry, nil, nil, nil, nil)
	handler := NewHandler(executor, registry, cliagent.NewEventLogger(t.TempDir()))
	handler.SetAdmissionGate(&resetstate.WorkGate{})
	closeRelease := sync.OnceFunc(func() { close(release) })
	t.Cleanup(closeRelease)

	body, err := json.Marshal(createTaskRequest{CLIBackend: cliagent.BackendClaude, Prompt: "fixture", WorkingDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handler.HandleCreateTask(w, httptest.NewRequest(http.MethodPost, "/api/cli-agents/tasks", bytes.NewReader(body)))
	if w.Code != http.StatusAccepted {
		t.Fatalf("create=%d %s", w.Code, w.Body)
	}
	var created createTaskResponse
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.TaskID == "" {
		t.Fatalf("202 carried no task_id: %s", w.Body)
	}

	get := func() map[string]any {
		t.Helper()
		w := httptest.NewRecorder()
		handler.HandleGetTask(w, httptest.NewRequest(http.MethodGet, "/api/cli-agents/tasks/"+created.TaskID, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("get=%d %s", w.Code, w.Body)
		}
		var got map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := get(); got["status"] != "running" || got["task_id"] != created.TaskID {
		t.Fatalf("while running: %v", got)
	}

	closeRelease()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got := get()
		if got["status"] == string(cliagent.TaskFailed) {
			if got["task_id"] != created.TaskID {
				t.Fatalf("result names another task: %v", got)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("task never finished: %v", got)
		}
		time.Sleep(time.Millisecond)
	}

	// An ID nobody created is not "running" just because another task is.
	w = httptest.NewRecorder()
	handler.HandleGetTask(w, httptest.NewRequest(http.MethodGet, "/api/cli-agents/tasks/unknown", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown id=%d", w.Code)
	}
}
