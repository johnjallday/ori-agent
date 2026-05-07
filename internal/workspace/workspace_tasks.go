package workspace

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// AddTask adds a task to the workspace
func (w *Workspace) AddTask(task Task) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Set task ID and timestamp if not set
	if task.ID == "" {
		task.ID = uuid.New().String()
	}
	if task.CreatedAt.IsZero() {
		task.CreatedAt = time.Now()
	}

	// Ensure workspace ID matches
	task.WorkspaceID = w.ID

	w.Tasks = append(w.Tasks, task)
	// Update task index for O(1) lookups
	if w.taskIndex == nil {
		w.taskIndex = make(map[string]int)
	}
	w.taskIndex[task.ID] = len(w.Tasks) - 1
	w.UpdatedAt = time.Now()

	return nil
}

// GetTask retrieves a task by ID using O(1) index lookup.
//
// Returns a pointer to a shallow copy, not the workspace's internal slice
// element. Callers may freely mutate the returned task without racing the
// workspace's other readers; persist changes back via UpdateTask. (Note:
// reference fields like Context maps still alias the original — callers
// that mutate those should rebuild the map rather than delete in place.)
func (w *Workspace) GetTask(taskID string) (*Task, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	// Use index for O(1) lookup if available
	if w.taskIndex != nil {
		if idx, ok := w.taskIndex[taskID]; ok && idx < len(w.Tasks) && w.Tasks[idx].ID == taskID {
			t := w.Tasks[idx]
			return &t, nil
		}
	}

	// Fallback to linear scan (for backward compatibility with workspaces loaded without index)
	for i := range w.Tasks {
		if w.Tasks[i].ID == taskID {
			t := w.Tasks[i]
			return &t, nil
		}
	}

	return nil, fmt.Errorf("task %q not found in workspace", taskID)
}

// UpdateTask updates an existing task in the workspace using O(1) index lookup
func (w *Workspace) UpdateTask(task Task) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Use index for O(1) lookup if available
	if w.taskIndex != nil {
		if idx, ok := w.taskIndex[task.ID]; ok && idx < len(w.Tasks) && w.Tasks[idx].ID == task.ID {
			w.Tasks[idx] = task
			w.UpdatedAt = time.Now()
			return nil
		}
	}

	// Fallback to linear scan (for backward compatibility)
	for i := range w.Tasks {
		if w.Tasks[i].ID == task.ID {
			w.Tasks[i] = task
			w.UpdatedAt = time.Now()
			return nil
		}
	}

	return fmt.Errorf("task %q not found in workspace", task.ID)
}

// MutateTaskAndSave applies fn to the task identified by taskID and persists
// the workspace via store. It is the canonical "mutate + persist" pattern.
//
// Cross-instance race safety: this helper delegates to store.Update, which
// re-loads the authoritative workspace under a per-workspace lock before
// running fn. The caller's ws argument is therefore advisory — its fields are
// not the ones fn mutates, and it should not be read after this call returns
// because the on-disk and in-cache state has moved past it. Callers that need
// to read the post-mutation workspace should re-Get it.
//
// Use this helper only when Save is the very next thing you would call. If
// other workspace mutations (e.g. AddMessage) need to land in the same Save,
// call store.Update directly with a closure that does all of them.
func MutateTaskAndSave(store Store, ws *Workspace, taskID string, fn func(*Task) error) error {
	return store.Update(ws.ID, func(fresh *Workspace) error {
		return fresh.MutateTask(taskID, fn)
	})
}

// MutateTask applies fn to the task identified by id while holding the workspace
// lock, eliminating the read-modify-write race that GetTask + UpdateTask exposed.
//
// fn receives a pointer to the live slice element and may mutate it freely;
// returning a non-nil error aborts the mutation (the in-memory state is left
// untouched). On success, UpdatedAt is bumped. Callers are still responsible
// for persisting the workspace via the store after MutateTask returns.
//
// fn must not call back into Workspace methods that take w.mu (deadlock) and
// must not retain the *Task beyond its own scope (the slice may be reallocated
// later by AddTask).
func (w *Workspace) MutateTask(id string, fn func(*Task) error) error {
	if fn == nil {
		return fmt.Errorf("MutateTask: fn is nil")
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	idx := -1
	if w.taskIndex != nil {
		if i, ok := w.taskIndex[id]; ok && i < len(w.Tasks) && w.Tasks[i].ID == id {
			idx = i
		}
	}
	if idx == -1 {
		for i := range w.Tasks {
			if w.Tasks[i].ID == id {
				idx = i
				break
			}
		}
	}
	if idx == -1 {
		return fmt.Errorf("task %q not found in workspace", id)
	}

	if err := fn(&w.Tasks[idx]); err != nil {
		return err
	}
	w.UpdatedAt = time.Now()
	return nil
}

// DeleteTask removes a task from the workspace by ID and cleans up layout metadata.
func (w *Workspace) DeleteTask(id string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Try to use index for O(1) lookup
	idx := -1
	if w.taskIndex != nil {
		if i, ok := w.taskIndex[id]; ok && i < len(w.Tasks) && w.Tasks[i].ID == id {
			idx = i
		}
	}

	// Fallback to linear scan if index not available or stale
	if idx == -1 {
		for i := range w.Tasks {
			if w.Tasks[i].ID == id {
				idx = i
				break
			}
		}
	}

	if idx == -1 {
		return fmt.Errorf("task %q not found in workspace", id)
	}

	// Remove task from slice
	w.Tasks = append(w.Tasks[:idx], w.Tasks[idx+1:]...)

	// Update index: remove deleted task and reindex tasks that shifted
	if w.taskIndex != nil {
		delete(w.taskIndex, id)
		// Update indices for tasks that shifted down
		for i := idx; i < len(w.Tasks); i++ {
			w.taskIndex[w.Tasks[i].ID] = i
		}
	}

	for i := range w.Tasks {
		if w.Tasks[i].ParentTaskID == id {
			w.Tasks[i].ParentTaskID = ""
			w.Tasks[i].SubtaskIndex = 0
		}
	}

	if w.Layout != nil && w.Layout.TaskPositions != nil {
		delete(w.Layout.TaskPositions, id)
	}
	w.UpdatedAt = time.Now()
	return nil
}

// GetTasksForAgent returns all tasks assigned to a specific agent
func (w *Workspace) GetTasksForAgent(agentName string) []Task {
	w.mu.RLock()
	defer w.mu.RUnlock()

	// Pre-allocate with estimated capacity to reduce allocations
	estimatedCap := len(w.Tasks) / 4
	if estimatedCap < 4 {
		estimatedCap = 4
	}
	tasks := make([]Task, 0, estimatedCap)
	for _, task := range w.Tasks {
		if task.To == agentName {
			tasks = append(tasks, task)
		}
	}
	return tasks
}

// GetPendingTasksForAgent returns pending/assigned tasks for an agent
func (w *Workspace) GetPendingTasksForAgent(agentName string) []Task {
	w.mu.RLock()
	defer w.mu.RUnlock()

	// Pre-allocate with estimated capacity to reduce allocations
	estimatedCap := len(w.Tasks) / 8
	if estimatedCap < 4 {
		estimatedCap = 4
	}
	tasks := make([]Task, 0, estimatedCap)
	for _, task := range w.Tasks {
		if task.To == agentName &&
			(task.Status == TaskStatusPending || task.Status == TaskStatusAssigned) {
			tasks = append(tasks, task)
		}
	}
	return tasks
}

// GetTaskStats returns statistics about tasks in the workspace
func (w *Workspace) GetTaskStats() map[string]int {
	w.mu.RLock()
	defer w.mu.RUnlock()

	stats := map[string]int{
		"total":              len(w.Tasks),
		"pending":            0,
		"assigned":           0,
		"in_progress":        0,
		"waiting_for_choice": 0,
		"completed":          0,
		"failed":             0,
		"cancelled":          0,
		"timeout":            0,
		"scheduled":          0,
	}

	for _, task := range w.Tasks {
		switch task.Status {
		case TaskStatusPending:
			stats["pending"]++
		case TaskStatusAssigned:
			stats["assigned"]++
		case TaskStatusInProgress:
			stats["in_progress"]++
		case TaskStatusWaitingForChoice:
			stats["waiting_for_choice"]++
		case TaskStatusCompleted:
			stats["completed"]++
		case TaskStatusFailed:
			stats["failed"]++
		case TaskStatusCancelled:
			stats["cancelled"]++
		case TaskStatusTimeout:
			stats["timeout"]++
		}
		// Count scheduled tasks separately
		if task.ScheduleEnabled {
			stats["scheduled"]++
		}
	}

	return stats
}

// GetSubtasks returns tasks that reference a parent task ID.
func (w *Workspace) GetSubtasks(parentTaskID string) []Task {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if parentTaskID == "" {
		return nil
	}

	subtasks := make([]Task, 0, 4)
	for _, task := range w.Tasks {
		if task.ParentTaskID == parentTaskID {
			subtasks = append(subtasks, task)
		}
	}
	return subtasks
}

// GetTaskResults returns the results of tasks by their IDs
func (w *Workspace) GetTaskResults(taskIDs []string) map[string]string {
	w.mu.RLock()
	defer w.mu.RUnlock()

	results := make(map[string]string)
	for _, taskID := range taskIDs {
		for _, task := range w.Tasks {
			if task.ID == taskID {
				// If task has been executed and has a result, use that
				if task.Result != "" {
					results[taskID] = task.Result
				} else {
					// Otherwise, use the task description
					// This handles cases where tasks are not assigned to agents
					// but their descriptions should be included in merge context
					results[taskID] = task.Description
				}
				break
			}
		}
	}
	return results
}

// GetInputContext builds a context map that includes results from input tasks
func (w *Workspace) GetInputContext(task *Task) map[string]interface{} {
	context := make(map[string]interface{})

	// Copy existing context
	for k, v := range task.Context {
		context[k] = v
	}

	// Add input task results if any
	if len(task.InputTaskIDs) > 0 {
		inputResults := w.GetTaskResults(task.InputTaskIDs)
		if len(inputResults) > 0 {
			context["input_task_results"] = inputResults
		}
		if structuredOutputs := w.GetTaskStructuredOutputs(task.InputTaskIDs); len(structuredOutputs) > 0 {
			context["input_task_structured_outputs"] = structuredOutputs
		}
	}

	return context
}

// GetTaskStructuredOutputs returns parsed structured outputs for tasks whose result matches a schema.
func (w *Workspace) GetTaskStructuredOutputs(taskIDs []string) map[string]interface{} {
	w.mu.RLock()
	defer w.mu.RUnlock()

	outputs := make(map[string]interface{})
	for _, taskID := range taskIDs {
		for _, task := range w.Tasks {
			if task.ID != taskID {
				continue
			}
			parsed, err := ValidateTaskStructuredOutput(task.OutputSchema, task.Result)
			if err == nil && len(parsed) > 0 {
				outputs[taskID] = parsed
			}
			break
		}
	}
	return outputs
}

// rebuildTaskIndex rebuilds the task index from the current Tasks slice.
// This should be called after deserializing a workspace from JSON.
func (w *Workspace) rebuildTaskIndex() {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.taskIndex = make(map[string]int, len(w.Tasks))
	for i, task := range w.Tasks {
		w.taskIndex[task.ID] = i
	}
}
