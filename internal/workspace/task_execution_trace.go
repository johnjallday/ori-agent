package workspace

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const maxRecordedTaskExecutionTraceEntries = 120

// RecordTaskExecutionTraceFromEventBus stores the current run's execution events on the task.
func RecordTaskExecutionTraceFromEventBus(task *Task, eventBus *EventBus, workspaceID, taskID string, startedAt, completedAt time.Time) {
	if task == nil || eventBus == nil || strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(taskID) == "" {
		return
	}

	events := eventBus.GetHistory(func(event Event) bool {
		if event.WorkspaceID != workspaceID {
			return false
		}
		if taskExecutionTraceEventTaskID(event) != taskID {
			return false
		}
		if !startedAt.IsZero() && event.Timestamp.Before(startedAt) {
			return false
		}
		if !completedAt.IsZero() && event.Timestamp.After(completedAt.Add(2*time.Second)) {
			return false
		}
		return isTaskExecutionTraceEvent(event.Type)
	}, 512)

	RecordTaskExecutionTrace(task, events)
}

// RecordTaskExecutionTrace stores a normalized chronological trace on the task.
func RecordTaskExecutionTrace(task *Task, events []Event) {
	if task == nil {
		return
	}

	trace := BuildTaskExecutionTrace(events)
	if len(trace) == 0 {
		return
	}
	task.ExecutionTrace = trace
}

// BuildTaskExecutionTrace converts task execution events into persisted trace rows.
func BuildTaskExecutionTrace(events []Event) []TaskExecutionTrace {
	if len(events) == 0 {
		return nil
	}

	sort.SliceStable(events, func(i, j int) bool {
		return events[i].Timestamp.Before(events[j].Timestamp)
	})

	trace := make([]TaskExecutionTrace, 0, len(events))
	for _, event := range events {
		entry, ok := buildTaskExecutionTraceEntry(event)
		if !ok {
			continue
		}
		trace = append(trace, entry)
	}

	if len(trace) > maxRecordedTaskExecutionTraceEntries {
		trace = trace[len(trace)-maxRecordedTaskExecutionTraceEntries:]
	}
	return trace
}

func isTaskExecutionTraceEvent(eventType EventType) bool {
	switch eventType {
	case EventTaskStarted, EventTaskProgress, EventTaskThinking, EventTaskToolCall, EventTaskToolResult, EventTaskCompleted, EventTaskFailed, EventTaskBlocked, EventTaskResumed:
		return true
	default:
		return false
	}
}

func buildTaskExecutionTraceEntry(event Event) (TaskExecutionTrace, bool) {
	if !isTaskExecutionTraceEvent(event.Type) {
		return TaskExecutionTrace{}, false
	}

	data := event.Data
	toolName := taskExecutionTraceDataString(data, "tool_name")
	source := taskExecutionTraceDataString(data, "agent")
	if source == "" {
		source = event.Source
	}

	entry := TaskExecutionTrace{
		Type:      string(event.Type),
		Source:    source,
		Timestamp: event.Timestamp,
	}

	switch event.Type {
	case EventTaskStarted:
		entry.Status = "started"
		entry.Title = "Task started"
		entry.Summary = taskExecutionTraceDataString(data, "description")
	case EventTaskProgress:
		entry.Status = "progress"
		entry.Title = "Progress update"
		if progress, ok := data["progress"].(map[string]interface{}); ok {
			entry.Summary = taskExecutionTraceDataString(progress, "current_step")
		}
		if entry.Summary == "" {
			entry.Summary = taskExecutionTraceDataString(data, "step_title")
		}
	case EventTaskThinking:
		entry.Status = "thinking"
		entry.Title = "Agent thinking"
		entry.Summary = firstNonEmptyString(
			taskExecutionTraceDataString(data, "message"),
			taskExecutionTraceDataString(data, "summary"),
		)
	case EventTaskToolCall:
		entry.Status = "tool call"
		entry.Title = fmt.Sprintf("Calling %s", firstNonEmptyString(toolName, "tool"))
		entry.Detail = taskExecutionTraceDataStringified(data["arguments"], 1400)
	case EventTaskToolResult:
		success := taskExecutionTraceDataBool(data, "success")
		if success {
			entry.Status = "tool result"
			entry.Title = fmt.Sprintf("Completed %s", firstNonEmptyString(toolName, "tool"))
			entry.Summary = taskExecutionTraceDataString(data, "result_preview")
		} else {
			entry.Status = "tool error"
			entry.Title = fmt.Sprintf("Failed %s", firstNonEmptyString(toolName, "tool"))
			entry.Summary = taskExecutionTraceDataString(data, "error")
		}
	case EventTaskCompleted:
		entry.Status = "completed"
		entry.Title = "Task completed"
		entry.Summary = taskExecutionTraceDataString(data, "result")
	case EventTaskFailed:
		entry.Status = "failed"
		entry.Title = "Task failed"
		entry.Summary = taskExecutionTraceDataString(data, "error")
	case EventTaskBlocked:
		entry.Status = "blocked"
		entry.Title = "Task paused"
		entry.Summary = firstNonEmptyString(
			taskExecutionTraceDataString(data, "reason"),
			taskExecutionTraceDataString(data, "agent_response"),
			taskExecutionTraceDataString(data, "error"),
		)
	case EventTaskResumed:
		entry.Status = "resumed"
		entry.Title = "Task resumed"
		entry.Summary = taskExecutionTraceDataString(data, "message")
	}

	entry.Summary = taskExecutionTraceTrim(entry.Summary, 1600)
	entry.Detail = taskExecutionTraceTrim(entry.Detail, 1800)
	if strings.TrimSpace(entry.Title) == "" && strings.TrimSpace(entry.Summary) == "" && strings.TrimSpace(entry.Detail) == "" {
		return TaskExecutionTrace{}, false
	}
	return entry, true
}

func taskExecutionTraceEventTaskID(event Event) string {
	return taskExecutionTraceDataString(event.Data, "task_id")
}

func taskExecutionTraceDataString(data map[string]interface{}, key string) string {
	if len(data) == 0 {
		return ""
	}
	value, ok := data[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func taskExecutionTraceDataBool(data map[string]interface{}, key string) bool {
	if len(data) == 0 {
		return false
	}
	switch value := data[key].(type) {
	case bool:
		return value
	case string:
		return strings.EqualFold(strings.TrimSpace(value), "true")
	default:
		return false
	}
}

func taskExecutionTraceDataStringified(value interface{}, maxLength int) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return taskExecutionTraceTrim(text, maxLength)
	}
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return taskExecutionTraceTrim(fmt.Sprint(value), maxLength)
	}
	return taskExecutionTraceTrim(string(b), maxLength)
}

func taskExecutionTraceTrim(value string, maxLength int) string {
	trimmed := strings.TrimSpace(value)
	if maxLength <= 0 || len(trimmed) <= maxLength {
		return trimmed
	}
	return strings.TrimSpace(trimmed[:maxLength]) + "..."
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
