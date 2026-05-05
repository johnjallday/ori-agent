package orchestrationhttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestEventHistoryHandlerFiltersByTaskAndLimit(t *testing.T) {
	eventBus := workspace.DefaultEventBus()
	eventBus.Publish(workspace.NewTaskEvent(workspace.EventTaskStarted, "workspace-1", "task-a", "Agent", map[string]interface{}{"description": "first"}))
	eventBus.Publish(workspace.NewTaskEvent(workspace.EventTaskStarted, "workspace-1", "task-b", "Agent", map[string]interface{}{"description": "other"}))
	eventBus.Publish(workspace.NewTaskEvent(workspace.EventTaskToolCall, "workspace-1", "task-a", "Agent", map[string]interface{}{"tool_name": "web_fetch"}))

	handler := NewNotificationHandler(nil, nil, eventBus)
	request := httptest.NewRequest(http.MethodGet, "/api/orchestration/events?workspace_id=workspace-1&task_id=task-a&limit=1", nil)
	recorder := httptest.NewRecorder()

	handler.EventHistoryHandler(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}

	var payload struct {
		Events []workspace.Event `json:"events"`
		Count  int               `json:"count"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if payload.Count != 1 || len(payload.Events) != 1 {
		t.Fatalf("expected one event, got count=%d len=%d", payload.Count, len(payload.Events))
	}
	if got := payload.Events[0].Data["task_id"]; got != "task-a" {
		t.Fatalf("expected task-a event, got %#v", got)
	}
}

func TestEventHistoryHandlerRejectsInvalidLimit(t *testing.T) {
	handler := NewNotificationHandler(nil, nil, workspace.DefaultEventBus())
	request := httptest.NewRequest(http.MethodGet, "/api/orchestration/events?limit=bad", nil)
	recorder := httptest.NewRecorder()

	handler.EventHistoryHandler(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recorder.Code)
	}
}
