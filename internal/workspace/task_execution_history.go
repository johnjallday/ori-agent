package workspace

import (
	"strings"
	"time"
)

// maxRecordedTaskExecutions caps the per-task history slice. Hourly
// scheduled tasks burn through 20 entries in under a day, leaving the
// observability UI with no record of why earlier runs failed; 200 buys
// roughly 8 days at one run per hour or 70 days at one run every 8h
// without making the JSON serialization meaningfully heavier.
//
// All trim sites across this package read this constant — keep them in
// sync so the cap is configurable in one place.
const maxRecordedTaskExecutions = 200

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
