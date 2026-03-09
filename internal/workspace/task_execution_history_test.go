package workspace

import (
	"strings"
	"testing"
	"time"
)

func TestRecordTaskExecution_AppendsAndCapsHistory(t *testing.T) {
	task := &Task{ID: "task-1"}
	startedAt := time.Date(2026, time.March, 8, 12, 0, 0, 0, time.UTC)

	for i := 0; i < maxRecordedTaskExecutions+3; i++ {
		RecordTaskExecution(task, "success", "summary", startedAt.Add(time.Duration(i)*time.Minute), 2*time.Second)
	}

	if task.ExecutionCount != maxRecordedTaskExecutions+3 {
		t.Fatalf("expected execution count %d, got %d", maxRecordedTaskExecutions+3, task.ExecutionCount)
	}
	if len(task.ExecutionHistory) != maxRecordedTaskExecutions {
		t.Fatalf("expected execution history capped at %d, got %d", maxRecordedTaskExecutions, len(task.ExecutionHistory))
	}
	if task.ExecutionHistory[0].ExecutedAt != startedAt.Add(3*time.Minute) {
		t.Fatalf("expected oldest retained entry to be the fourth execution, got %s", task.ExecutionHistory[0].ExecutedAt)
	}
	if task.LastRun == nil || !task.LastRun.Equal(startedAt.Add((maxRecordedTaskExecutions+2)*time.Minute)) {
		t.Fatalf("expected last run to be updated")
	}
}

func TestRecordTaskExecution_PreservesFailureDetails(t *testing.T) {
	task := &Task{ID: "task-2"}
	longSummary := strings.Repeat("failure detail ", 40)

	RecordTaskExecution(task, "blocked", longSummary, time.Time{}, 1500*time.Millisecond)

	if len(task.ExecutionHistory) != 1 {
		t.Fatalf("expected a single execution history entry, got %d", len(task.ExecutionHistory))
	}
	entry := task.ExecutionHistory[0]
	if entry.Status != "blocked" {
		t.Fatalf("expected blocked status, got %q", entry.Status)
	}
	if entry.Error != strings.TrimSpace(longSummary) {
		t.Fatalf("expected full error text to be preserved")
	}
	if len(entry.Summary) >= len(strings.TrimSpace(longSummary)) {
		t.Fatalf("expected summary to be truncated for long content")
	}
	if entry.Duration != 1500 {
		t.Fatalf("expected duration 1500ms, got %d", entry.Duration)
	}
}
