package orchestrationhttp

import (
	"context"

	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/agentstudio"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
)

// TaskResultsHandler retrieves results from one or more tasks
// GET /api/orchestration/task-results?task_ids=id1,id2,id3
func (th *TaskHandler) TaskResultsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodGet {
		if respErr := orihttp.RespondMethodNotAllowed(w); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}

	taskIDsStr := r.URL.Query().Get("task_ids")
	if taskIDsStr == "" {
		if respErr := orihttp.RespondBadRequest(w, "task_ids parameter required (comma-separated)"); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
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
		if respErr := orihttp.RespondInternalError(w, "Failed to retrieve workspaces"); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
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
		if respErr := orihttp.RespondMethodNotAllowed(w); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}

	var req struct {
		TaskID string `json:"task_id"`
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if req.TaskID == "" {
		if respErr := orihttp.RespondBadRequest(w, "task_id is required"); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}

	workspaceIDs, err := th.workspaceStore.List()
	if err != nil {
		logger.Error("Error listing workspaces", logger.Fields{"error": err})
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to list workspaces", err)
		return
	}

	var foundWorkspace *agentstudio.Workspace
	var foundTask *agentstudio.Task

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
		if respErr := orihttp.RespondNotFound(w, fmt.Sprintf("Task %s not found", req.TaskID)); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}

	if foundTask.Status == agentstudio.TaskStatusCompleted {
		// Allow rerun of completed tasks by resetting status
		logger.Info("Rerunning completed task", logger.Fields{"task_id": req.TaskID})
		foundTask.Status = agentstudio.TaskStatusPending
		foundTask.Result = ""
		foundTask.Error = ""
		foundTask.StartedAt = nil
		foundTask.CompletedAt = nil

		// Save the reset task status
		if err := foundWorkspace.UpdateTask(*foundTask); err != nil {
			logger.Error("Failed to reset task status", logger.Fields{"status": err})
			if respErr := orihttp.RespondInternalError(w, "Failed to reset task for rerun"); respErr != nil {
				logger.Error("Failed to write response", logger.Fields{"error": respErr})
			}
			return
		}
		if err := th.workspaceStore.Save(foundWorkspace); err != nil {
			logger.Error("Failed to save workspace", logger.Fields{"error": err})
			if respErr := orihttp.RespondInternalError(w, "Failed to save workspace"); respErr != nil {
				logger.Error("Failed to write response", logger.Fields{"error": respErr})
			}
			return
		}
	}

	if foundTask.Status == agentstudio.TaskStatusInProgress {
		if respErr := orihttp.RespondBadRequest(w, "Task is already in progress"); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}

	if th.taskHandler == nil {
		logger.Error("Task handler not set", logger.Fields{})
		if respErr := orihttp.RespondInternalError(w, "Task execution not available"); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}

	if len(foundTask.InputTaskIDs) > 0 {
		logger.Info("Task has input tasks, checking if they need execution first", logger.Fields{"task_id": foundTask.ID, "input_count": len(foundTask.InputTaskIDs)})

		if err := th.executeInputTasksIfNeeded(foundWorkspace, foundTask); err != nil {
			logger.Error("Failed to execute input tasks", logger.Fields{"task_id": foundTask.ID, "error": err})
			if respErr := orihttp.RespondInternalError(w, fmt.Sprintf("Failed to execute input tasks: %v", err)); respErr != nil {
				logger.Error("Failed to write response", logger.Fields{"error": respErr})
			}
			return
		}
	}

	// Execute the task immediately in a goroutine
	go func() {
		ctx := context.Background()

		// Update task status to in_progress
		foundTask.Status = agentstudio.TaskStatusInProgress
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
			event := agentstudio.NewTaskEvent(agentstudio.EventTaskStarted, foundWorkspace.ID, foundTask.ID, foundTask.To, map[string]interface{}{
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
			task.Status = agentstudio.TaskStatusFailed
			task.Error = execErr.Error()

			// Publish task failed event
			if th.eventBus != nil {
				event := agentstudio.NewTaskEvent(agentstudio.EventTaskFailed, ws.ID, task.ID, task.To, map[string]interface{}{
					"description": task.Description,
					"error":       execErr.Error(),
					"manual":      true,
				})
				th.eventBus.Publish(event)
			}
		} else {
			logger.Info("Task completed successfully", logger.Fields{"task_id": task.ID})
			task.Status = agentstudio.TaskStatusCompleted
			task.Result = result

			// Automatically store result if agent is connected to a store node
			agentstudio.AutoStoreResult(ws, task, result, th.workspaceStore)

			// Publish task completed event
			if th.eventBus != nil {
				event := agentstudio.NewTaskEvent(agentstudio.EventTaskCompleted, ws.ID, task.ID, task.To, map[string]interface{}{
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
			event := agentstudio.NewWorkspaceEvent(agentstudio.EventWorkspaceUpdated, ws.ID, "manual-execution", map[string]interface{}{
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

// executeInputTasksIfNeeded recursively executes any pending/unassigned input tasks
// before executing the main task. This ensures fresh results for all inputs.
func (th *TaskHandler) executeInputTasksIfNeeded(ws *agentstudio.Workspace, task *agentstudio.Task) error {
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
		needsExecution := inputTask.Status == agentstudio.TaskStatusPending ||
			inputTask.Status == "" ||
			inputTask.To == "unassigned" ||
			inputTask.To == "" ||
			inputTask.Status == agentstudio.TaskStatusFailed

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
			inputTask.Status = agentstudio.TaskStatusPending

			// Save the assignment
			if err := ws.UpdateTask(*inputTask); err != nil {
				return fmt.Errorf("failed to auto-assign input task: %w", err)
			}
			if err := th.workspaceStore.Save(ws); err != nil {
				return fmt.Errorf("failed to save workspace after auto-assignment: %w", err)
			}
		}

		// Execute the input task synchronously
		ctx := context.Background()

		// Update status to in_progress
		inputTask.Status = agentstudio.TaskStatusInProgress
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
			inputTask.Status = agentstudio.TaskStatusFailed
			inputTask.Error = err.Error()
		} else {
			logger.Info("Input task completed successfully", logger.Fields{"input_task_id": inputTaskID})
			inputTask.Status = agentstudio.TaskStatusCompleted
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
			if inputTask.Status == agentstudio.TaskStatusFailed {
				event := agentstudio.NewTaskEvent(agentstudio.EventTaskFailed, ws.ID, inputTask.ID, inputTask.To, map[string]interface{}{
					"description": inputTask.Description,
					"error":       inputTask.Error,
					"auto":        true,
				})
				th.eventBus.Publish(event)
			} else {
				event := agentstudio.NewTaskEvent(agentstudio.EventTaskCompleted, ws.ID, inputTask.ID, inputTask.To, map[string]interface{}{
					"description": inputTask.Description,
					"result":      inputTask.Result,
					"auto":        true,
				})
				th.eventBus.Publish(event)
			}
		}

		// If input task failed, don't continue
		if inputTask.Status == agentstudio.TaskStatusFailed {
			return fmt.Errorf("input task %s failed: %s", inputTaskID, inputTask.Error)
		}
	}

	logger.Info("All input tasks executed successfully", logger.Fields{"task_id": task.ID})
	return nil
}
