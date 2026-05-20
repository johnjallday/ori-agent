package workspace

import (
	"strings"
	"sync"
)

// TaskValidationMirrorFunc lets outer packages mirror task output validation
// to storage they own, such as durable workspace-run records. It deliberately
// stays in terms of workspace package types to avoid package cycles.
type TaskValidationMirrorFunc func(workspaceID, taskID, runID string, validation TaskValidationResult)

var taskValidationMirror struct {
	mu sync.RWMutex
	fn TaskValidationMirrorFunc
}

// SetTaskValidationMirror installs a process-wide best-effort validation mirror.
func SetTaskValidationMirror(fn TaskValidationMirrorFunc) {
	taskValidationMirror.mu.Lock()
	defer taskValidationMirror.mu.Unlock()
	taskValidationMirror.fn = fn
}

// MirrorTaskValidationResult mirrors validation metadata for a workspace run.
func MirrorTaskValidationResult(workspaceID, taskID, runID string, validation *TaskValidationResult) {
	workspaceID = strings.TrimSpace(workspaceID)
	taskID = strings.TrimSpace(taskID)
	runID = strings.TrimSpace(runID)
	if workspaceID == "" || taskID == "" || runID == "" || validation == nil {
		return
	}

	taskValidationMirror.mu.RLock()
	fn := taskValidationMirror.fn
	taskValidationMirror.mu.RUnlock()
	if fn == nil {
		return
	}

	fn(workspaceID, taskID, runID, cloneTaskValidationResultValue(validation))
}

func mirrorLatestTaskValidationResult(workspaceID string, task *Task, validation *TaskValidationResult) {
	if task == nil || validation == nil {
		return
	}
	runID := ""
	if len(task.ExecutionHistory) > 0 {
		runID = task.ExecutionHistory[len(task.ExecutionHistory)-1].RunID
	}
	MirrorTaskValidationResult(workspaceID, task.ID, runID, validation)
}

func cloneTaskValidationResultValue(validation *TaskValidationResult) TaskValidationResult {
	out := *validation
	if validation.ValidatedAt != nil {
		t := *validation.ValidatedAt
		out.ValidatedAt = &t
	}
	out.Errors = append([]TaskValidationError(nil), validation.Errors...)
	if validation.ManualApproval != nil {
		approval := *validation.ManualApproval
		out.ManualApproval = &approval
	}
	return out
}
