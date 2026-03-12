package workspace

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

var nonAlphaNumericStepChars = regexp.MustCompile(`[^a-z0-9]+`)

// NormalizeTaskExecutionMode clamps task execution mode to supported values.
func NormalizeTaskExecutionMode(value string) TaskExecutionMode {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(TaskExecutionModeStepThrough):
		return TaskExecutionModeStepThrough
	default:
		return TaskExecutionModeAuto
	}
}

// IsLikelyFilesystemExecutionIntent detects tasks that likely require file operations.
func IsLikelyFilesystemExecutionIntent(description string) bool {
	lower := strings.ToLower(strings.TrimSpace(description))
	if lower == "" {
		return false
	}

	directPhrases := []string{
		"move files",
		"copy files",
		"rename files",
		"organize files",
		"organise files",
		"gather files",
		"collect files",
		"sort files",
		"clean up files",
		"into folder",
		"into directory",
		"filesystem",
		"file management",
	}
	for _, phrase := range directPhrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}

	actionSignals := []string{"move", "copy", "rename", "organize", "organise", "gather", "collect", "sort", "group", "archive"}
	nounSignals := []string{"file", "files", "folder", "folders", "directory", "directories", "filesystem", "path", "paths"}

	actionCount := 0
	for _, signal := range actionSignals {
		if strings.Contains(lower, signal) {
			actionCount++
		}
	}

	nounCount := 0
	for _, signal := range nounSignals {
		if strings.Contains(lower, signal) {
			nounCount++
		}
	}

	return (actionCount > 0 && nounCount > 0) || nounCount > 1
}

// InferTaskExecutionSteps generates a structured execution plan for tasks that benefit from step tracking.
func InferTaskExecutionSteps(task Task) []TaskExecutionStep {
	description := strings.TrimSpace(task.Description)
	lower := strings.ToLower(description)

	toStep := func(index int, title, detail, tag string) TaskExecutionStep {
		now := (*time.Time)(nil)
		return TaskExecutionStep{
			ID:          buildTaskExecutionStepID(index, title),
			Index:       index,
			Title:       title,
			Detail:      detail,
			Tag:         tag,
			Status:      TaskExecutionStepPending,
			StartedAt:   now,
			CompletedAt: now,
		}
	}

	if IsLikelyFilesystemExecutionIntent(description) {
		return []TaskExecutionStep{
			toStep(1, "Check allowed filesystem scope", "Confirm which directories are available before making changes.", "Discovery"),
			toStep(2, "Inspect candidate directories", "Look through likely source folders for relevant material.", "Discovery"),
			toStep(3, "Identify matching files", "Match files by filename, path, and the task context.", "Analysis"),
			toStep(4, "Create the destination folder if needed", "Prepare the destination without overwriting unrelated content.", "Mutation"),
			toStep(5, "Move or copy matching files", "Relocate the selected files safely into the destination.", "Mutation"),
			toStep(6, "Verify the final folder contents", "Confirm the destination contains the expected files and note anything skipped.", "Verify"),
			toStep(7, "Return a summary", "Report what changed, what was skipped, and any follow-up needed.", "Summary"),
		}
	}

	if isLikelyBrowserAutomationIntent(description) {
		return []TaskExecutionStep{
			toStep(1, "Check browser capability", "Confirm the agent can open and interact with the required site.", "Discovery"),
			toStep(2, "Open the target page", "Navigate to the relevant website or URL for this task.", "Action"),
			toStep(3, "Inspect the required information or controls", "Locate the data, form, or interface needed to complete the task.", "Analysis"),
			toStep(4, "Perform the requested browser action", "Carry out the required interaction or extraction.", "Action"),
			toStep(5, "Verify the outcome", "Confirm the page state or extracted result matches the task goal.", "Verify"),
			toStep(6, "Return a summary", "Report what was done and any follow-up needed.", "Summary"),
		}
	}

	if strings.Contains(lower, "summarize") || strings.Contains(lower, "summary") || strings.Contains(lower, "review") {
		return []TaskExecutionStep{
			toStep(1, "Collect the relevant context", "Gather the information needed to answer the request.", "Discovery"),
			toStep(2, "Synthesize the main findings", "Turn the collected context into a concise result.", "Analysis"),
			toStep(3, "Return a summary", "Present the final result with the most relevant details.", "Summary"),
		}
	}

	return nil
}

// ResetTaskExecutionSteps clears runtime state for a structured execution plan.
func ResetTaskExecutionSteps(task *Task) {
	if task == nil {
		return
	}

	task.Progress = nil
	if task.Context == nil {
		task.Context = map[string]interface{}{}
	} else {
		delete(task.Context, "execution_step_waiting")
		delete(task.Context, "execution_step_waiting_index")
	}

	if len(task.ExecutionSteps) == 0 {
		return
	}

	for i := range task.ExecutionSteps {
		task.ExecutionSteps[i].Status = TaskExecutionStepPending
		task.ExecutionSteps[i].Result = ""
		task.ExecutionSteps[i].Error = ""
		task.ExecutionSteps[i].StartedAt = nil
		task.ExecutionSteps[i].CompletedAt = nil
	}
}

// PrepareTaskExecutionStepsForResume converts blocked/in-progress steps back into runnable steps.
func PrepareTaskExecutionStepsForResume(task *Task) {
	if task == nil || len(task.ExecutionSteps) == 0 {
		return
	}

	for i := range task.ExecutionSteps {
		switch task.ExecutionSteps[i].Status {
		case TaskExecutionStepBlocked, TaskExecutionStepFailed, TaskExecutionStepInProgress:
			task.ExecutionSteps[i].Status = TaskExecutionStepPending
			task.ExecutionSteps[i].Error = ""
			task.ExecutionSteps[i].StartedAt = nil
			task.ExecutionSteps[i].CompletedAt = nil
			return
		}
	}
}

// IsTaskAwaitingNextStep reports whether a step-through task is paused between internal steps.
func IsTaskAwaitingNextStep(task *Task) bool {
	if task == nil || NormalizeTaskExecutionMode(string(task.ExecutionMode)) != TaskExecutionModeStepThrough {
		return false
	}
	if task.Context == nil {
		return false
	}
	waiting, ok := task.Context["execution_step_waiting"].(bool)
	return ok && waiting
}

// GetNextRunnableExecutionStepIndex returns the next step that should execute.
func GetNextRunnableExecutionStepIndex(task *Task) int {
	if task == nil {
		return -1
	}
	for index, step := range task.ExecutionSteps {
		switch step.Status {
		case TaskExecutionStepPending, TaskExecutionStepBlocked, TaskExecutionStepFailed, TaskExecutionStepInProgress:
			return index
		}
	}
	return -1
}

// CountCompletedExecutionSteps returns the number of finished internal steps.
func CountCompletedExecutionSteps(task *Task) int {
	if task == nil {
		return 0
	}
	completed := 0
	for _, step := range task.ExecutionSteps {
		if step.Status == TaskExecutionStepCompleted || step.Status == TaskExecutionStepSkipped {
			completed++
		}
	}
	return completed
}

// BuildTaskExecutionSummary returns the best available final result from structured steps.
func BuildTaskExecutionSummary(task *Task) string {
	if task == nil {
		return ""
	}
	for i := len(task.ExecutionSteps) - 1; i >= 0; i-- {
		if result := strings.TrimSpace(task.ExecutionSteps[i].Result); result != "" {
			return result
		}
	}
	return strings.TrimSpace(task.Result)
}

func buildTaskExecutionStepID(index int, title string) string {
	normalizedTitle := strings.ToLower(strings.TrimSpace(title))
	normalizedTitle = nonAlphaNumericStepChars.ReplaceAllString(normalizedTitle, "_")
	normalizedTitle = strings.Trim(normalizedTitle, "_")
	if normalizedTitle == "" {
		normalizedTitle = "step"
	}
	return fmt.Sprintf("step_%d_%s", index, normalizedTitle)
}
