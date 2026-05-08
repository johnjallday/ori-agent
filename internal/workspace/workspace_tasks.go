package workspace

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// AddTasks atomically appends a batch of tasks to the workspace under a
// single lock. The whole batch is validated together at end-of-append, so
// callers may include forward references between siblings (e.g. a workflow's
// subtasks pointing at each other via input_task_ids) — something the
// per-task AddTask path cannot accept.
//
// On any validation failure the workspace state is fully restored: every
// task added by this call is rolled back from both Tasks and taskIndex.
// Caller-supplied IDs are preserved when present, so workflows can wire
// up sibling references with UUIDs they generate before the call.
func (w *Workspace) AddTasks(tasks []Task) error {
	if len(tasks) == 0 {
		return nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.taskIndex == nil {
		w.taskIndex = make(map[string]int)
	}

	originalLen := len(w.Tasks)
	addedIDs := make([]string, 0, len(tasks))
	now := time.Now()

	for i := range tasks {
		t := tasks[i]
		if t.ID == "" {
			t.ID = uuid.New().String()
		}
		if t.CreatedAt.IsZero() {
			t.CreatedAt = now
		}
		t.WorkspaceID = w.ID

		w.Tasks = append(w.Tasks, t)
		w.taskIndex[t.ID] = len(w.Tasks) - 1
		addedIDs = append(addedIDs, t.ID)
	}

	if err := validateTaskGraph(w.Tasks); err != nil {
		w.Tasks = w.Tasks[:originalLen]
		for _, id := range addedIDs {
			delete(w.taskIndex, id)
		}
		return err
	}

	w.UpdatedAt = now
	return nil
}

// AddTask adds a task to the workspace.
//
// The candidate task plus the existing graph are validated before commit; if
// the addition would introduce a cycle, self-reference, or unknown
// parent/input ID, the workspace state is left unchanged and the validation
// error is returned. Forward references (a task whose parent/input has not
// been added yet) are rejected here — batch importers that may not have
// inserted dependencies in topological order should accumulate tasks first
// and call ValidateTaskGraph at end-of-batch instead.
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

	// Only check when the candidate actually contributes graph edges, so the
	// hot path for the common (no parent/no inputs) AddTask stays free of the
	// O(V+E) walk.
	if task.ParentTaskID != "" || len(task.InputTaskIDs) > 0 {
		if err := validateTaskGraph(w.Tasks); err != nil {
			w.Tasks = w.Tasks[:len(w.Tasks)-1]
			delete(w.taskIndex, task.ID)
			return err
		}
	}

	w.UpdatedAt = time.Now()

	return nil
}

// findTaskIdxLocked returns the slice index of the task with the given ID, or
// -1 if it does not exist. Caller must hold w.mu (read or write). Uses the
// taskIndex when available and falls back to a linear scan for workspaces
// constructed as zero-value literals (mostly tests) or whose index has drifted.
func (w *Workspace) findTaskIdxLocked(taskID string) int {
	if w.taskIndex != nil {
		if idx, ok := w.taskIndex[taskID]; ok && idx < len(w.Tasks) && w.Tasks[idx].ID == taskID {
			return idx
		}
	}
	for i := range w.Tasks {
		if w.Tasks[i].ID == taskID {
			return i
		}
	}
	return -1
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

	idx := w.findTaskIdxLocked(taskID)
	if idx == -1 {
		return nil, fmt.Errorf("task %q not found in workspace", taskID)
	}
	t := w.Tasks[idx]
	return &t, nil
}

// UpdateTask updates an existing task in the workspace using O(1) index lookup
func (w *Workspace) UpdateTask(task Task) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	idx := w.findTaskIdxLocked(task.ID)
	if idx == -1 {
		return fmt.Errorf("task %q not found in workspace", task.ID)
	}
	w.Tasks[idx] = task
	w.UpdatedAt = time.Now()
	return nil
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

	idx := w.findTaskIdxLocked(id)
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

	idx := w.findTaskIdxLocked(id)
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

// GetTaskResults returns the results of tasks by their IDs.
// Uses the task index for O(M) lookup where M is len(taskIDs).
func (w *Workspace) GetTaskResults(taskIDs []string) map[string]string {
	w.mu.RLock()
	defer w.mu.RUnlock()

	results := make(map[string]string, len(taskIDs))
	for _, taskID := range taskIDs {
		idx := w.findTaskIdxLocked(taskID)
		if idx == -1 {
			continue
		}
		task := &w.Tasks[idx]
		// If the task has been executed, use its result; otherwise fall back
		// to its description so unassigned/pending tasks still contribute
		// merge context.
		if task.Result != "" {
			results[taskID] = task.Result
		} else {
			results[taskID] = task.Description
		}
	}
	return results
}

// BuildRuntimeInputs assembles a fresh TaskRuntimeInputs for the given task,
// pulling current results and structured outputs for every InputTaskIDs entry.
//
// The returned value is intended to be assigned to task.RuntimeInputs only for
// the duration of an execution. It must NOT be merged into task.Context: that
// would corrupt the persisted task by interleaving runtime state with authored
// context, and re-runs would see stale injection from prior executions.
//
// Returns nil if the task has no input tasks or none of them have results yet.
func (w *Workspace) BuildRuntimeInputs(task *Task) *TaskRuntimeInputs {
	if task == nil || len(task.InputTaskIDs) == 0 {
		return nil
	}

	results := w.GetTaskResults(task.InputTaskIDs)
	structured := w.GetTaskStructuredOutputs(task.InputTaskIDs)

	if len(results) == 0 && len(structured) == 0 {
		return nil
	}

	out := &TaskRuntimeInputs{}
	if len(results) > 0 {
		out.TaskResults = results
	}
	if len(structured) > 0 {
		out.StructuredOutputs = make(map[string]map[string]interface{}, len(structured))
		for id, val := range structured {
			if m, ok := val.(map[string]interface{}); ok {
				out.StructuredOutputs[id] = m
			}
		}
	}
	return out
}

// GetTaskStructuredOutputs returns parsed structured outputs for tasks whose
// result matches a schema. Uses the task index for O(M) lookup.
func (w *Workspace) GetTaskStructuredOutputs(taskIDs []string) map[string]interface{} {
	w.mu.RLock()
	defer w.mu.RUnlock()

	outputs := make(map[string]interface{}, len(taskIDs))
	for _, taskID := range taskIDs {
		idx := w.findTaskIdxLocked(taskID)
		if idx == -1 {
			continue
		}
		task := &w.Tasks[idx]
		parsed, err := ValidateTaskStructuredOutput(task.OutputSchema, task.Result)
		if err == nil && len(parsed) > 0 {
			outputs[taskID] = parsed
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
