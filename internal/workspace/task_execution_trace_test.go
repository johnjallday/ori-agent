package workspace

import (
	"testing"
	"time"
)

func TestBuildTaskExecutionTrace_NormalizesTaskEventsChronologically(t *testing.T) {
	later := time.Date(2026, 5, 5, 10, 1, 0, 0, time.UTC)
	earlier := time.Date(2026, 5, 5, 10, 0, 0, 0, time.UTC)

	events := []Event{
		NewTaskEvent(EventTaskToolResult, "workspace-1", "task-1", "Agent", map[string]interface{}{
			"tool_name":      "web_fetch",
			"success":        true,
			"result_preview": "forecast found",
		}),
		NewTaskEvent(EventTaskToolCall, "workspace-1", "task-1", "Agent", map[string]interface{}{
			"tool_name": "web_fetch",
			"arguments": map[string]interface{}{"url": "https://example.com"},
		}),
	}
	events[0].Timestamp = later
	events[1].Timestamp = earlier

	trace := BuildTaskExecutionTrace(events)

	if len(trace) != 2 {
		t.Fatalf("expected two trace entries, got %d", len(trace))
	}
	if trace[0].Status != "tool call" || trace[0].Title != "Calling web_fetch" {
		t.Fatalf("expected first entry to be tool call, got %#v", trace[0])
	}
	if trace[1].Status != "tool result" || trace[1].Summary != "forecast found" {
		t.Fatalf("expected second entry to be successful tool result, got %#v", trace[1])
	}
}

func TestRecordTaskExecutionTraceFromEventBusFiltersTaskAndWindow(t *testing.T) {
	eventBus := DefaultEventBus()
	startedAt := time.Date(2026, 5, 5, 10, 0, 0, 0, time.UTC)
	completedAt := time.Date(2026, 5, 5, 10, 2, 0, 0, time.UTC)

	inWindow := NewTaskEvent(EventTaskStarted, "workspace-1", "task-1", "Agent", map[string]interface{}{"description": "run"})
	inWindow.Timestamp = startedAt.Add(time.Minute)
	eventBus.Publish(inWindow)

	otherTask := NewTaskEvent(EventTaskToolCall, "workspace-1", "task-2", "Agent", map[string]interface{}{"tool_name": "web_fetch"})
	otherTask.Timestamp = startedAt.Add(time.Minute)
	eventBus.Publish(otherTask)

	outsideWindow := NewTaskEvent(EventTaskToolCall, "workspace-1", "task-1", "Agent", map[string]interface{}{"tool_name": "late"})
	outsideWindow.Timestamp = completedAt.Add(10 * time.Second)
	eventBus.Publish(outsideWindow)

	task := &Task{ID: "task-1"}
	RecordTaskExecutionTraceFromEventBus(task, eventBus, "workspace-1", "task-1", startedAt, completedAt)

	if len(task.ExecutionTrace) != 1 {
		t.Fatalf("expected one trace entry, got %d: %#v", len(task.ExecutionTrace), task.ExecutionTrace)
	}
	if task.ExecutionTrace[0].Status != "started" {
		t.Fatalf("expected started trace entry, got %#v", task.ExecutionTrace[0])
	}
}
