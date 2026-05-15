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

// maxRecordedTaskExecutionResult caps the per-record full-result blob. The
// short Summary field stays for inline preview; Result holds the full body
// up to this size so users can re-read past runs after the next run
// overwrites task.Result. 16 KiB × 200 records = 3.2 MB worst-case per
// task, which is negligible next to message/trace history; LLM responses
// past this size are rare enough that truncation is acceptable.
const maxRecordedTaskExecutionResult = 16 * 1024

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
	trimmedSummary := strings.TrimSpace(summary)
	record := TaskExecution{
		TaskID:     task.ID,
		RunID:      strings.TrimSpace(task.CurrentRunID),
		ExecutedAt: startedAt,
		Status:     trimmedStatus,
		Summary:    summarizeTaskExecutionSummary(trimmedSummary),
		Result:     capTaskExecutionResult(trimmedSummary),
		Duration:   duration.Milliseconds(),
	}

	if (trimmedStatus == "failed" || trimmedStatus == "blocked") && trimmedSummary != "" {
		record.Error = trimmedSummary
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

// capTaskExecutionResult trims a result body to the per-record cap, marking
// the truncation explicitly so the UI can surface "… N bytes truncated".
func capTaskExecutionResult(value string) string {
	if len(value) <= maxRecordedTaskExecutionResult {
		return value
	}
	truncated := value[:maxRecordedTaskExecutionResult]
	return truncated + "\n\n…[truncated]"
}
