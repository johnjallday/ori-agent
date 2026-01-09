package workspace

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/logger"
)

// AddTask adds a task to the workspace
func (w *Workspace) AddTask(task Task) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	logger.Debug("[DEBUG] AddTask - Workspace: , Agents", logger.Fields{"agent": w.ID, "agents": w.Agents})
	logger.Debug("[DEBUG] AddTask - Task: From=, To=", logger.Fields{"task_id": task.From, "to": task.To})
	logger.Debug("[DEBUG] AddTask - hasAgent(From)", logger.Fields{"agent": w.hasAgentUnlocked(task.From)})

	// Validate sender is part of workspace
	// Allow "user", "system", "scheduler", and empty string as special senders for UI-created tasks
	systemSources := map[string]bool{
		"user":      true,
		"system":    true,
		"scheduler": true,
		"":          true, // empty string allowed
	}
	if !systemSources[task.From] && !w.hasAgentUnlocked(task.From) {
		logger.Error("[DEBUG] AddTask - Validation FAILED: From agent not valid", logger.Fields{})
		return fmt.Errorf("task delegator %s is not part of workspace", task.From)
	}

	// Validate recipient if specified
	// Allow "unassigned" as a special value for tasks without a specific recipient
	if task.To != "" && task.To != "unassigned" && !w.hasAgentUnlocked(task.To) {
		logger.Error("[DEBUG] AddTask - Validation FAILED: To agent not valid", logger.Fields{})
		return fmt.Errorf("task recipient %s is not part of workspace", task.To)
	}

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

// GetTask retrieves a task by ID using O(1) index lookup
func (w *Workspace) GetTask(taskID string) (*Task, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	// Use index for O(1) lookup if available
	if w.taskIndex != nil {
		if idx, ok := w.taskIndex[taskID]; ok && idx < len(w.Tasks) && w.Tasks[idx].ID == taskID {
			return &w.Tasks[idx], nil
		}
	}

	// Fallback to linear scan (for backward compatibility with workspaces loaded without index)
	for i := range w.Tasks {
		if w.Tasks[i].ID == taskID {
			return &w.Tasks[i], nil
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
		"total":       len(w.Tasks),
		"pending":     0,
		"assigned":    0,
		"in_progress": 0,
		"completed":   0,
		"failed":      0,
		"cancelled":   0,
		"timeout":     0,
	}

	for _, task := range w.Tasks {
		switch task.Status {
		case TaskStatusPending:
			stats["pending"]++
		case TaskStatusAssigned:
			stats["assigned"]++
		case TaskStatusInProgress:
			stats["in_progress"]++
		case TaskStatusCompleted:
			stats["completed"]++
		case TaskStatusFailed:
			stats["failed"]++
		case TaskStatusCancelled:
			stats["cancelled"]++
		case TaskStatusTimeout:
			stats["timeout"]++
		}
	}

	return stats
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
	}

	return context
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
