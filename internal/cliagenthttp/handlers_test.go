package cliagenthttp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnjallday/ori-agent/internal/cliagent"
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
