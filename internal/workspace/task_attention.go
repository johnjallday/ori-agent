package workspace

import "strings"

// TaskAttentionState is the record-only presentation shared with task-presentation.js.
// It never checks providers or changes Ticket/execution eligibility. See the matched
// fixture in testdata/task_attention_cases.json for precedence and count semantics.
func TaskAttentionState(task Task) string {
	normalize := func(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
	switch normalize(string(task.TicketState)) {
	case "backlog":
		return "backlog"
	case "review", "done":
		return "completed"
	case "cancelled":
		return "cancelled"
	}
	status := normalize(string(task.Status))
	switch status {
	case "backlog", "cancelled", "skipped":
		return status
	case "completed", "success", "done":
		return "completed"
	case "failed", "error":
		return "failed"
	case "timeout":
		return "timed_out"
	}
	loop, _ := task.Context["human_loop"].(map[string]any)
	loopState, _ := loop["state"].(string)
	stepWaiting, _ := task.Context["execution_step_waiting"].(bool)
	if status == "waiting_for_choice" || normalize(loopState) == "waiting_for_choice" || stepWaiting {
		return "needs_input"
	}
	if status == "blocked" || normalize(loopState) == "blocked" {
		return "blocked"
	}
	switch status {
	case "in_progress":
		return "running"
	case "pending", "assigned", "ready", "queued", "todo", "not_started", "":
		if assignee := normalize(task.To); assignee == "" || assignee == "unassigned" {
			return "needs_assignment"
		}
		return "ready"
	default:
		return "unknown"
	}
}
