package workspace

import (
	"strings"
	"time"
)

const maxRecordedTaskExecutions = 20

// RecordTaskExecution appends a compact execution record for the task.
func RecordTaskExecution(task *Task, status, summary string, executedAt time.Time, duration time.Duration) {
	if task == nil {
		return
	}

	startedAt := executedAt
	if startedAt.IsZero() {
		startedAt = time.Now()
	}

	task.LastRun = &startedAt
	task.ExecutionCount++

	trimmedStatus := strings.TrimSpace(status)
	record := TaskExecution{
		TaskID:     task.ID,
		ExecutedAt: startedAt,
		Status:     trimmedStatus,
		Summary:    summarizeTaskExecutionSummary(summary),
		Duration:   duration.Milliseconds(),
	}

	if (trimmedStatus == "failed" || trimmedStatus == "blocked") && strings.TrimSpace(summary) != "" {
		record.Error = strings.TrimSpace(summary)
	}

	task.ExecutionHistory = append(task.ExecutionHistory, record)
	if len(task.ExecutionHistory) > maxRecordedTaskExecutions {
		task.ExecutionHistory = task.ExecutionHistory[len(task.ExecutionHistory)-maxRecordedTaskExecutions:]
	}
}

func summarizeTaskExecutionSummary(value string) string {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) <= 360 {
		return trimmed
	}
	return strings.TrimSpace(trimmed[:360]) + "..."
}
