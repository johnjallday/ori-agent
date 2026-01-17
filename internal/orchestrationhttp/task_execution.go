package orchestrationhttp

import (
	"context"

	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// TaskResultsHandler retrieves results from one or more tasks
// GET /api/orchestration/task-results?task_ids=id1,id2,id3
func (th *TaskHandler) TaskResultsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	taskIDsStr := r.URL.Query().Get("task_ids")
	if taskIDsStr == "" {
		orihttp.BadRequest(w, "task_ids parameter required (comma-separated)")
		return
	}

	taskIDs := strings.Split(taskIDsStr, ",")
	for i := range taskIDs {
		taskIDs[i] = strings.TrimSpace(taskIDs[i])
	}

	// We need to find the workspace that contains these tasks
	// For simplicity, we'll search through all workspaces
	workspaceIDs, err := th.workspaceStore.List()
	if err != nil {
		logger.Error("Error listing workspaces", logger.Fields{"error": err})
		orihttp.InternalError(w, "Failed to retrieve workspaces")
		return
	}

	allResults := make(map[string]interface{})
	for _, wsID := range workspaceIDs {
		ws, err := th.workspaceStore.Get(wsID)
		if err != nil {
			logger.Error("Error getting workspace", logger.Fields{"error": wsID, "err": err})
			continue
		}

		results := ws.GetTaskResults(taskIDs)
		for taskID, result := range results {
			// Get full task info
			task, err := ws.GetTask(taskID)
			if err == nil {
				allResults[taskID] = map[string]interface{}{
					"task_id":      task.ID,
					"description":  task.Description,
					"status":       task.Status,
					"result":       result,
					"from":         task.From,
					"to":           task.To,
					"completed_at": task.CompletedAt,
				}
			} else {
				allResults[taskID] = map[string]interface{}{
					"task_id": taskID,
					"result":  result,
				}
			}
		}
	}

	w.WriteHeader(http.StatusOK)
	orihttp.WriteJSON(w, map[string]interface{}{
		"success": true,
		"results": allResults,
	})
}

// ExecuteTaskHandler handles manual task execution
func (th *TaskHandler) ExecuteTaskHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	var req struct {
		TaskID string `json:"task_id"`
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if req.TaskID == "" {
		orihttp.BadRequest(w, "task_id is required")
		return
	}

	workspaceIDs, err := th.workspaceStore.List()
	if err != nil {
		logger.Error("Error listing workspaces", logger.Fields{"error": err})
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to list workspaces", err)
		return
	}

	var foundWorkspace *workspace.Workspace
	var foundTask *workspace.Task

	for _, wsID := range workspaceIDs {
		ws, err := th.workspaceStore.Get(wsID)
		if err != nil {
			continue
		}

		task, err := ws.GetTask(req.TaskID)
		if err == nil {
			foundWorkspace = ws
			foundTask = task
			break
		}
	}

	if foundTask == nil {
		orihttp.NotFound(w, fmt.Sprintf("Task %s not found", req.TaskID))
		return
	}

	subtasks := foundWorkspace.GetSubtasks(foundTask.ID)
	if len(subtasks) > 0 {
		if foundTask.Status == workspace.TaskStatusInProgress {
			orihttp.BadRequest(w, "Task is already in progress")
			return
		}

		for _, subtask := range subtasks {
			if subtask.Status == workspace.TaskStatusInProgress {
				orihttp.BadRequest(w, "A subtask is already in progress")
				return
			}
			if subtask.To == "" || subtask.To == "unassigned" {
				orihttp.BadRequest(w, "All subtasks must be assigned to an agent before execution")
				return
			}
		}

		if th.taskHandler == nil {
			logger.Error("Task handler not set", logger.Fields{})
			orihttp.InternalError(w, "Task execution not available")
			return
		}

		go th.executeParentTaskSequence(foundWorkspace.ID, foundTask.ID)

		logger.Info("Started manual execution of task sequence", logger.Fields{"task_id": req.TaskID})

		w.WriteHeader(http.StatusAccepted)
		orihttp.WriteJSON(w, map[string]interface{}{
			"success": true,
			"message": "Task sequence started",
			"task_id": req.TaskID,
		})
		return
	}

	if foundTask.Status == workspace.TaskStatusCompleted {
		// Allow rerun of completed tasks by resetting status
		logger.Info("Rerunning completed task", logger.Fields{"task_id": req.TaskID})
		foundTask.Status = workspace.TaskStatusPending
		foundTask.Result = ""
		foundTask.Error = ""
		foundTask.StartedAt = nil
		foundTask.CompletedAt = nil

		// Save the reset task status
		if err := foundWorkspace.UpdateTask(*foundTask); err != nil {
			logger.Error("Failed to reset task status", logger.Fields{"status": err})
			orihttp.InternalError(w, "Failed to reset task for rerun")
			return
		}
		if err := th.workspaceStore.Save(foundWorkspace); err != nil {
			logger.Error("Failed to save workspace", logger.Fields{"error": err})
			orihttp.InternalError(w, "Failed to save workspace")
			return
		}
	}

	if foundTask.Status == workspace.TaskStatusInProgress {
		orihttp.BadRequest(w, "Task is already in progress")
		return
	}

	if th.taskHandler == nil {
		logger.Error("Task handler not set", logger.Fields{})
		orihttp.InternalError(w, "Task execution not available")
		return
	}

	if len(foundTask.InputTaskIDs) > 0 {
		logger.Info("Task has input tasks, checking if they need execution first", logger.Fields{"task_id": foundTask.ID, "input_count": len(foundTask.InputTaskIDs)})

		if err := th.executeInputTasksIfNeeded(foundWorkspace, foundTask); err != nil {
			logger.Error("Failed to execute input tasks", logger.Fields{"task_id": foundTask.ID, "error": err})
			orihttp.InternalError(w, fmt.Sprintf("Failed to execute input tasks: %v", err))
			return
		}
	}

	// Execute the task immediately in a goroutine with a timeout
	// Default timeout is 30 minutes to prevent runaway tasks
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()

		// Update task status to in_progress
		foundTask.Status = workspace.TaskStatusInProgress
		now := time.Now()
		foundTask.StartedAt = &now

		if err := foundWorkspace.UpdateTask(*foundTask); err != nil {
			logger.Error("Failed to update task status", logger.Fields{"task_id": err})
			return
		}
		if err := th.workspaceStore.Save(foundWorkspace); err != nil {
			logger.Error("Failed to save workspace", logger.Fields{"error": err})
			return
		}

		// Publish task started event
		if th.eventBus != nil {
			event := workspace.NewTaskEvent(workspace.EventTaskStarted, foundWorkspace.ID, foundTask.ID, foundTask.To, map[string]interface{}{
				"description": foundTask.Description,
				"priority":    foundTask.Priority,
				"manual":      true,
			})
			th.eventBus.Publish(event)
		}

		logger.Debug("Manually executing task for agent", logger.Fields{"description": foundTask.Description, "agent": foundTask.ID, "to": foundTask.To})

		// Gather input task results and substitute placeholders in description
		var inputResults []string
		if len(foundTask.InputTaskIDs) > 0 {
			logger.Debug("Task has input task IDs", logger.Fields{"task_id": foundTask.ID, "inputtaskids)": len(foundTask.InputTaskIDs), "inputtaskids": foundTask.InputTaskIDs})

			// Get input context (includes results map)
			enrichedContext := foundWorkspace.GetInputContext(foundTask)
			foundTask.Context = enrichedContext

			// Extract results for placeholder substitution (in order of InputTaskIDs)
			if inputResultsMap, ok := enrichedContext["input_task_results"]; ok {
				resultsMap := inputResultsMap.(map[string]string)
				logger.Debug("Injected input task results into task context", logger.Fields{"task_id": len(resultsMap), "id": foundTask.ID})

				// Build ordered results array matching InputTaskIDs order
				for _, inputTaskID := range foundTask.InputTaskIDs {
					if result, exists := resultsMap[inputTaskID]; exists {
						inputResults = append(inputResults, result)
						preview := result
						if len(preview) > 100 {
							preview = preview[:100] + "..."
						}
						logger.Debug("- Task result", logger.Fields{"result": inputTaskID, "preview": preview})
					}
				}
			} else {
				logger.Warn("Warning: No input results found for task despite having InputTaskIDs", logger.Fields{"task_id": foundTask.ID})
			}

			// Substitute placeholders in task description
			if len(inputResults) > 0 {
				originalDesc := foundTask.Description
				foundTask.Description = substituteInputPlaceholders(foundTask.Description, inputResults)
				if originalDesc != foundTask.Description {
					logger.Debug("Substituted placeholders in description", logger.Fields{})
					logger.Debug("Original", logger.Fields{"originalDesc": originalDesc})
					logger.Debug("Processed", logger.Fields{"description": foundTask.Description})
				}
			}
		} else {
			logger.Debug("Task has no input task IDs", logger.Fields{"task_id": foundTask.ID})
		}

		// Execute the task (with processed description if placeholders were substituted)
		result, execErr := th.taskHandler.ExecuteTask(ctx, foundTask.To, *foundTask)

		// Reload workspace (may have changed)
		ws, err := th.workspaceStore.Get(foundWorkspace.ID)
		if err != nil {
			logger.Error("Failed to reload workspace", logger.Fields{"err": err, "workspace_id": foundWorkspace.ID})
			return
		}

		// Find the task in the reloaded workspace
		task, err := ws.GetTask(foundTask.ID)
		if err != nil {
			logger.Error("Task not found in workspace after execution", logger.Fields{"task_id": foundTask.ID})
			return
		}

		// Update task with result
		completedAt := time.Now()
		task.CompletedAt = &completedAt

		if execErr != nil {
			logger.Error("Task failed", logger.Fields{"task_id": task.ID, "execErr": execErr})
			task.Status = workspace.TaskStatusFailed
			task.Error = execErr.Error()

			// Publish task failed event
			if th.eventBus != nil {
				event := workspace.NewTaskEvent(workspace.EventTaskFailed, ws.ID, task.ID, task.To, map[string]interface{}{
					"description": task.Description,
					"error":       execErr.Error(),
					"manual":      true,
				})
				th.eventBus.Publish(event)
			}
		} else {
			logger.Info("Task completed successfully", logger.Fields{"task_id": task.ID})
			task.Status = workspace.TaskStatusCompleted
			task.Result = result

			// Automatically store result if agent is connected to a store node
			workspace.AutoStoreResult(ws, task, result, th.workspaceStore)

			// Publish task completed event
			if th.eventBus != nil {
				event := workspace.NewTaskEvent(workspace.EventTaskCompleted, ws.ID, task.ID, task.To, map[string]interface{}{
					"description": task.Description,
					"result":      result,
					"manual":      true,
				})
				th.eventBus.Publish(event)
			}
		}

		// Save updated task
		if err := ws.UpdateTask(*task); err != nil {
			logger.Error("Failed to update task", logger.Fields{"task_id": err})
			return
		}
		if err := th.workspaceStore.Save(ws); err != nil {
			logger.Error("Failed to save workspace", logger.Fields{"error": err})
		}

		// Publish workspace updated event
		if th.eventBus != nil {
			event := workspace.NewWorkspaceEvent(workspace.EventWorkspaceUpdated, ws.ID, "manual-execution", map[string]interface{}{
				"task_id": task.ID,
				"status":  task.Status,
			})
			th.eventBus.Publish(event)
		}
	}()

	logger.Info("Started manual execution of task", logger.Fields{"task_id": req.TaskID})

	w.WriteHeader(http.StatusAccepted)
	orihttp.WriteJSON(w, map[string]interface{}{
		"success": true,
		"message": "Task execution started",
		"task_id": req.TaskID,
	})
}

func (th *TaskHandler) executeParentTaskSequence(workspaceID, parentTaskID string) {
	ws, err := th.workspaceStore.Get(workspaceID)
	if err != nil {
		logger.Error("Failed to load workspace for task sequence", logger.Fields{"workspace_id": workspaceID, "error": err})
		return
	}

	parentTask, err := ws.GetTask(parentTaskID)
	if err != nil {
		logger.Error("Parent task not found for task sequence", logger.Fields{"task_id": parentTaskID, "error": err})
		return
	}

	subtasks := ws.GetSubtasks(parentTaskID)
	if len(subtasks) == 0 {
		logger.Warn("No subtasks found for parent task sequence", logger.Fields{"task_id": parentTaskID})
		return
	}

	sort.SliceStable(subtasks, func(i, j int) bool {
		if subtasks[i].SubtaskIndex > 0 && subtasks[j].SubtaskIndex > 0 && subtasks[i].SubtaskIndex != subtasks[j].SubtaskIndex {
			return subtasks[i].SubtaskIndex < subtasks[j].SubtaskIndex
		}
		if !subtasks[i].CreatedAt.Equal(subtasks[j].CreatedAt) {
			return subtasks[i].CreatedAt.Before(subtasks[j].CreatedAt)
		}
		return subtasks[i].ID < subtasks[j].ID
	})

	startedAt := time.Now()
	parentTask.Status = workspace.TaskStatusInProgress
	parentTask.StartedAt = &startedAt
	parentTask.CompletedAt = nil
	parentTask.Result = ""
	parentTask.Error = ""

	if err := ws.UpdateTask(*parentTask); err != nil {
		logger.Error("Failed to update parent task status", logger.Fields{"task_id": parentTaskID, "error": err})
		return
	}
	if err := th.workspaceStore.Save(ws); err != nil {
		logger.Error("Failed to save workspace for parent task start", logger.Fields{"workspace_id": workspaceID, "error": err})
		return
	}

	if th.eventBus != nil {
		event := workspace.NewTaskEvent(workspace.EventTaskStarted, ws.ID, parentTask.ID, parentTask.To, map[string]interface{}{
			"description": parentTask.Description,
			"manual":      true,
		})
		th.eventBus.Publish(event)
		th.eventBus.Publish(workspace.NewWorkspaceEvent(workspace.EventWorkspaceUpdated, ws.ID, "manual-sequence-start", map[string]interface{}{
			"task_id": parentTask.ID,
			"status":  parentTask.Status,
		}))
	}

	var lastResult string
	var execErr error

	for _, subtaskInfo := range subtasks {
		subtask, err := ws.GetTask(subtaskInfo.ID)
		if err != nil {
			execErr = fmt.Errorf("subtask %s not found", subtaskInfo.ID)
			break
		}

		if subtask.Status == workspace.TaskStatusInProgress {
			execErr = fmt.Errorf("subtask %s already in progress", subtask.Description)
			break
		}
		if subtask.To == "" || subtask.To == "unassigned" {
			execErr = fmt.Errorf("subtask %s has no assigned agent", subtask.Description)
			break
		}

		if subtask.Status == workspace.TaskStatusCompleted ||
			subtask.Status == workspace.TaskStatusFailed ||
			subtask.Status == workspace.TaskStatusCancelled ||
			subtask.Status == workspace.TaskStatusTimeout {
			subtask.Status = workspace.TaskStatusPending
			subtask.Result = ""
			subtask.Error = ""
			subtask.StartedAt = nil
			subtask.CompletedAt = nil

			if err := ws.UpdateTask(*subtask); err != nil {
				execErr = fmt.Errorf("failed to reset subtask %s: %w", subtask.ID, err)
				break
			}
			if err := th.workspaceStore.Save(ws); err != nil {
				execErr = fmt.Errorf("failed to save workspace before subtask: %w", err)
				break
			}
		}

		result, err := th.executeTaskWithDependencies(ws, subtask, true)
		if err != nil {
			execErr = err
			break
		}
		lastResult = result
	}

	parentTask, err = ws.GetTask(parentTaskID)
	if err != nil {
		logger.Error("Failed to reload parent task after sequence", logger.Fields{"task_id": parentTaskID, "error": err})
		return
	}

	completedAt := time.Now()
	parentTask.CompletedAt = &completedAt

	if execErr != nil {
		logger.Error("Task sequence failed", logger.Fields{"task_id": parentTaskID, "error": execErr})
		parentTask.Status = workspace.TaskStatusFailed
		parentTask.Error = execErr.Error()
		parentTask.Result = ""

		if th.eventBus != nil {
			event := workspace.NewTaskEvent(workspace.EventTaskFailed, ws.ID, parentTask.ID, parentTask.To, map[string]interface{}{
				"description": parentTask.Description,
				"error":       execErr.Error(),
				"manual":      true,
			})
			th.eventBus.Publish(event)
		}
	} else {
		logger.Info("Task sequence completed successfully", logger.Fields{"task_id": parentTaskID})
		parentTask.Status = workspace.TaskStatusCompleted
		parentTask.Result = lastResult
		parentTask.Error = ""

		if th.eventBus != nil {
			event := workspace.NewTaskEvent(workspace.EventTaskCompleted, ws.ID, parentTask.ID, parentTask.To, map[string]interface{}{
				"description": parentTask.Description,
				"result":      lastResult,
				"manual":      true,
			})
			th.eventBus.Publish(event)
		}
	}

	if err := ws.UpdateTask(*parentTask); err != nil {
		logger.Error("Failed to update parent task after sequence", logger.Fields{"task_id": parentTaskID, "error": err})
		return
	}
	if err := th.workspaceStore.Save(ws); err != nil {
		logger.Error("Failed to save workspace after task sequence", logger.Fields{"workspace_id": workspaceID, "error": err})
		return
	}

	if th.eventBus != nil {
		th.eventBus.Publish(workspace.NewWorkspaceEvent(workspace.EventWorkspaceUpdated, ws.ID, "manual-sequence-complete", map[string]interface{}{
			"task_id": parentTask.ID,
			"status":  parentTask.Status,
		}))
	}
}

func (th *TaskHandler) executeTaskWithDependencies(ws *workspace.Workspace, task *workspace.Task, manual bool) (string, error) {
	if task.To == "" || task.To == "unassigned" {
		return "", fmt.Errorf("task %s has no assigned agent", task.Description)
	}

	if len(task.InputTaskIDs) > 0 {
		logger.Info("Task has input tasks, checking if they need execution first", logger.Fields{"task_id": task.ID, "input_count": len(task.InputTaskIDs)})
		if err := th.executeInputTasksIfNeeded(ws, task); err != nil {
			logger.Error("Failed to execute input tasks", logger.Fields{"task_id": task.ID, "error": err})
			return "", err
		}
	}

	timeout := 30 * time.Minute
	if task.Timeout > 0 {
		timeout = task.Timeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	task.Status = workspace.TaskStatusInProgress
	now := time.Now()
	task.StartedAt = &now
	task.Result = ""
	task.Error = ""

	if err := ws.UpdateTask(*task); err != nil {
		return "", fmt.Errorf("failed to update task status: %w", err)
	}
	if err := th.workspaceStore.Save(ws); err != nil {
		return "", fmt.Errorf("failed to save workspace: %w", err)
	}

	if th.eventBus != nil {
		event := workspace.NewTaskEvent(workspace.EventTaskStarted, ws.ID, task.ID, task.To, map[string]interface{}{
			"description": task.Description,
			"manual":      manual,
		})
		th.eventBus.Publish(event)
	}

	taskForExecution := *task
	var inputResults []string
	if len(task.InputTaskIDs) > 0 {
		logger.Debug("Task has input task IDs", logger.Fields{"task_id": task.ID, "inputtaskids)": len(task.InputTaskIDs), "inputtaskids": task.InputTaskIDs})

		enrichedContext := ws.GetInputContext(task)
		taskForExecution.Context = enrichedContext

		if inputResultsMap, ok := enrichedContext["input_task_results"]; ok {
			resultsMap := inputResultsMap.(map[string]string)
			logger.Debug("Injected input task results into task context", logger.Fields{"task_id": len(resultsMap), "id": task.ID})

			for _, inputTaskID := range task.InputTaskIDs {
				if result, exists := resultsMap[inputTaskID]; exists {
					inputResults = append(inputResults, result)
					preview := result
					if len(preview) > 100 {
						preview = preview[:100] + "..."
					}
					logger.Debug("- Task result", logger.Fields{"result": inputTaskID, "preview": preview})
				}
			}
		} else {
			logger.Warn("Warning: No input results found for task despite having InputTaskIDs", logger.Fields{"task_id": task.ID})
		}

		if len(inputResults) > 0 {
			originalDesc := taskForExecution.Description
			taskForExecution.Description = substituteInputPlaceholders(taskForExecution.Description, inputResults)
			if originalDesc != taskForExecution.Description {
				logger.Debug("Substituted placeholders in description", logger.Fields{})
				logger.Debug("Original", logger.Fields{"originalDesc": originalDesc})
				logger.Debug("Processed", logger.Fields{"description": taskForExecution.Description})
			}
		}
	}

	result, execErr := th.taskHandler.ExecuteTask(ctx, task.To, taskForExecution)

	completedAt := time.Now()
	task.CompletedAt = &completedAt

	if execErr != nil {
		logger.Error("Task failed", logger.Fields{"task_id": task.ID, "execErr": execErr})
		task.Status = workspace.TaskStatusFailed
		task.Error = execErr.Error()
	} else {
		logger.Info("Task completed successfully", logger.Fields{"task_id": task.ID})
		task.Status = workspace.TaskStatusCompleted
		task.Result = result
		task.Error = ""

		workspace.AutoStoreResult(ws, task, result, th.workspaceStore)
	}

	if err := ws.UpdateTask(*task); err != nil {
		return result, fmt.Errorf("failed to update task: %w", err)
	}
	if err := th.workspaceStore.Save(ws); err != nil {
		return result, fmt.Errorf("failed to save workspace: %w", err)
	}

	if th.eventBus != nil {
		if execErr != nil {
			event := workspace.NewTaskEvent(workspace.EventTaskFailed, ws.ID, task.ID, task.To, map[string]interface{}{
				"description": task.Description,
				"error":       execErr.Error(),
				"manual":      manual,
			})
			th.eventBus.Publish(event)
		} else {
			event := workspace.NewTaskEvent(workspace.EventTaskCompleted, ws.ID, task.ID, task.To, map[string]interface{}{
				"description": task.Description,
				"result":      result,
				"manual":      manual,
			})
			th.eventBus.Publish(event)
		}

		th.eventBus.Publish(workspace.NewWorkspaceEvent(workspace.EventWorkspaceUpdated, ws.ID, "manual-execution", map[string]interface{}{
			"task_id": task.ID,
			"status":  task.Status,
		}))
	}

	return result, execErr
}

// executeInputTasksIfNeeded recursively executes any pending/unassigned input tasks
// before executing the main task. This ensures fresh results for all inputs.
func (th *TaskHandler) executeInputTasksIfNeeded(ws *workspace.Workspace, task *workspace.Task) error {
	if len(task.InputTaskIDs) == 0 {
		return nil
	}

	logger.Info("Checking input tasks for execution", logger.Fields{"task_id": task.ID, "input_count": len(task.InputTaskIDs)})

	for _, inputTaskID := range task.InputTaskIDs {
		inputTask, err := ws.GetTask(inputTaskID)
		if err != nil {
			logger.Warn("Input task not found, skipping", logger.Fields{"input_task_id": inputTaskID})
			continue
		}

		// Check if input task needs execution
		needsExecution := inputTask.Status == workspace.TaskStatusPending ||
			inputTask.Status == "" ||
			inputTask.To == "unassigned" ||
			inputTask.To == "" ||
			inputTask.Status == workspace.TaskStatusFailed

		if !needsExecution {
			logger.Debug("Input task already completed, skipping", logger.Fields{"input_task_id": inputTaskID, "status": inputTask.Status})
			continue
		}

		logger.Info("Auto-executing input task", logger.Fields{"input_task_id": inputTaskID, "description": inputTask.Description})

		// Recursively execute this input task's inputs first
		if err := th.executeInputTasksIfNeeded(ws, inputTask); err != nil {
			return fmt.Errorf("failed to execute nested input task %s: %w", inputTaskID, err)
		}

		// Auto-assign unassigned input tasks to the parent task's agent
		if inputTask.To == "unassigned" || inputTask.To == "" {
			if task.To == "unassigned" || task.To == "" {
				logger.Error("Cannot auto-execute input task: both input and parent are unassigned", logger.Fields{"input_task_id": inputTaskID})
				return fmt.Errorf("input task %s is unassigned and parent task has no agent to inherit", inputTaskID)
			}

			logger.Info("Auto-assigning input task to parent's agent", logger.Fields{
				"input_task_id": inputTaskID,
				"agent":         task.To,
				"assigned_node": task.AssignedNodeID,
			})

			inputTask.To = task.To
			inputTask.AssignedNodeID = task.AssignedNodeID
			inputTask.Status = workspace.TaskStatusPending

			// Save the assignment
			if err := ws.UpdateTask(*inputTask); err != nil {
				return fmt.Errorf("failed to auto-assign input task: %w", err)
			}
			if err := th.workspaceStore.Save(ws); err != nil {
				return fmt.Errorf("failed to save workspace after auto-assignment: %w", err)
			}
		}

		// Execute the input task synchronously with a timeout
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()

		// Update status to in_progress
		inputTask.Status = workspace.TaskStatusInProgress
		now := time.Now()
		inputTask.StartedAt = &now

		if err := ws.UpdateTask(*inputTask); err != nil {
			return fmt.Errorf("failed to update input task status: %w", err)
		}
		if err := th.workspaceStore.Save(ws); err != nil {
			return fmt.Errorf("failed to save workspace: %w", err)
		}

		// Gather input context for this task
		if len(inputTask.InputTaskIDs) > 0 {
			enrichedContext := ws.GetInputContext(inputTask)
			inputTask.Context = enrichedContext
		}

		// Execute the task
		logger.Debug("Executing input task", logger.Fields{"input_task_id": inputTaskID, "agent": inputTask.To})
		result, err := th.taskHandler.ExecuteTask(ctx, inputTask.To, *inputTask)

		// Update task with result
		completed := time.Now()
		inputTask.CompletedAt = &completed

		if err != nil {
			logger.Error("Input task execution failed", logger.Fields{"input_task_id": inputTaskID, "error": err})
			inputTask.Status = workspace.TaskStatusFailed
			inputTask.Error = err.Error()
		} else {
			logger.Info("Input task completed successfully", logger.Fields{"input_task_id": inputTaskID})
			inputTask.Status = workspace.TaskStatusCompleted
			inputTask.Result = result
		}

		// Save updated task
		if err := ws.UpdateTask(*inputTask); err != nil {
			return fmt.Errorf("failed to save input task result: %w", err)
		}
		if err := th.workspaceStore.Save(ws); err != nil {
			return fmt.Errorf("failed to save workspace after input task: %w", err)
		}

		// Publish events
		if th.eventBus != nil {
			if inputTask.Status == workspace.TaskStatusFailed {
				event := workspace.NewTaskEvent(workspace.EventTaskFailed, ws.ID, inputTask.ID, inputTask.To, map[string]interface{}{
					"description": inputTask.Description,
					"error":       inputTask.Error,
					"auto":        true,
				})
				th.eventBus.Publish(event)
			} else {
				event := workspace.NewTaskEvent(workspace.EventTaskCompleted, ws.ID, inputTask.ID, inputTask.To, map[string]interface{}{
					"description": inputTask.Description,
					"result":      inputTask.Result,
					"auto":        true,
				})
				th.eventBus.Publish(event)
			}
		}

		// If input task failed, don't continue
		if inputTask.Status == workspace.TaskStatusFailed {
			return fmt.Errorf("input task %s failed: %s", inputTaskID, inputTask.Error)
		}
	}

	logger.Info("All input tasks executed successfully", logger.Fields{"task_id": task.ID})
	return nil
}
