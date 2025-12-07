package orchestrationhttp

import (
	"context"
	"encoding/json"
	"fmt"

	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/agentcomm"
	"github.com/johnjallday/ori-agent/internal/agentstudio"
	"github.com/johnjallday/ori-agent/internal/httputil"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/robfig/cron/v3"
)

// TaskHandler manages task and scheduled task operations
type TaskHandler struct {
	workspaceStore agentstudio.Store
	communicator   *agentcomm.Communicator
	taskHandler    agentstudio.TaskHandler
	eventBus       *agentstudio.EventBus
}

// NewTaskHandler creates a new task handler
func NewTaskHandler(workspaceStore agentstudio.Store,
	communicator *agentcomm.Communicator,
	taskHandler agentstudio.TaskHandler,
	eventBus *agentstudio.EventBus) *TaskHandler {
	return &TaskHandler{
		workspaceStore: workspaceStore,
		communicator:   communicator,
		taskHandler:    taskHandler,
		eventBus:       eventBus,
	}
}

// TasksHandler handles task queries
// GET: Get task by ID or list tasks for workspace/agent
// PUT: Update task status
func (th *TaskHandler) TasksHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		th.handleGetTasks(w, r)
	case http.MethodPost:
		th.handleCreateTask(w, r)
	case http.MethodPut:
		th.handleUpdateTask(w, r)
	case http.MethodDelete:
		th.handleDeleteTask(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// handleGetTasks retrieves tasks
func (th *TaskHandler) handleGetTasks(w http.ResponseWriter, r *http.Request) {
	taskID := r.URL.Query().Get("id")
	workspaceID := r.URL.Query().Get("studio_id")
	agentName := r.URL.Query().Get("agent")

	if taskID != "" {
		// Get specific task
		task, err := th.communicator.GetTask(taskID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(task)
		return
	}

	if workspaceID != "" {
		// List tasks for workspace
		tasks := th.communicator.ListTasks(workspaceID)
		stats := th.communicator.GetTaskStats(workspaceID)

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"tasks": tasks,
			"stats": stats,
			"count": len(tasks),
		})
		return
	}

	if agentName != "" {
		// List tasks for agent
		tasks := th.communicator.ListTasksForAgent(agentName)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"tasks": tasks,
			"count": len(tasks),
		})
		return
	}

	http.Error(w, "id, workspace_id, or agent parameter required", http.StatusBadRequest)
}

// handleCreateTask creates a new task in a workspace
func (th *TaskHandler) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkspaceID            string   `json:"studio_id"`
		From                   string   `json:"from"`
		To                     string   `json:"to"`
		AssignedNodeID         string   `json:"assigned_node_id"`
		Description            string   `json:"description"`
		Priority               int      `json:"priority"`
		InputTaskIDs           []string `json:"input_task_ids"`
		ResultCombinationMode  string   `json:"result_combination_mode"`
		CombinationInstruction string   `json:"combination_instruction"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.RespondError(w, http.StatusBadRequest, "Invalid request body", err)
		return
	}

	// Validate required fields
	if req.WorkspaceID == "" {
		http.Error(w, "workspace_id is required", http.StatusBadRequest)
		return
	}
	if req.From == "" {
		http.Error(w, "from (sender agent) is required", http.StatusBadRequest)
		return
	}
	if req.To == "" {
		http.Error(w, "to (recipient agent) is required", http.StatusBadRequest)
		return
	}
	if req.Description == "" {
		http.Error(w, "description is required", http.StatusBadRequest)
		return
	}

	// Get workspace
	ws, err := th.workspaceStore.Get(req.WorkspaceID)
	if err != nil {
		logger.Error("Error getting workspace", logger.Fields{"err": err, "error": req.WorkspaceID})
		httputil.RespondError(w, http.StatusNotFound, "Workspace not found", err)
		return
	}

	// Create task
	task := agentstudio.Task{
		WorkspaceID:    req.WorkspaceID,
		From:           req.From,
		To:             req.To,
		AssignedNodeID: req.AssignedNodeID,
		Description:    req.Description,
		Priority:       req.Priority,
		InputTaskIDs:   req.InputTaskIDs,
		Status:         agentstudio.TaskStatusPending,
	}

	// Add task to workspace
	if err := ws.AddTask(task); err != nil {
		logger.Error("Failed to add task to workspace", logger.Fields{"workspace_id": err})
		httputil.RespondError(w, http.StatusBadRequest, "Failed to add task", err)
		return
	}

	// Save workspace
	if err := th.workspaceStore.Save(ws); err != nil {
		logger.Error("Failed to save workspace", logger.Fields{"workspace_id": err})
		httputil.RespondError(w, http.StatusInternalServerError, "Failed to save workspace", err)
		return
	}

	// Get the task we just added (it now has an ID)
	// Find the most recently added task with matching properties
	var createdTask *agentstudio.Task
	for i := len(ws.Tasks) - 1; i >= 0; i-- {
		if ws.Tasks[i].Description == req.Description && ws.Tasks[i].From == req.From && ws.Tasks[i].To == req.To {
			createdTask = &ws.Tasks[i]
			break
		}
	}

	if createdTask == nil {
		logger.Error("Could not find created task", logger.Fields{})
		http.Error(w, "Task created but could not be retrieved", http.StatusInternalServerError)
		return
	}

	if len(req.InputTaskIDs) > 0 {
		logger.Info("Created connected task in workspace (receiving input from task(s))", logger.Fields{
			"task_id":          createdTask.ID,
			"workspace_id":     req.WorkspaceID,
			"from":             req.From,
			"to":               req.To,
			"input_task_count": len(req.InputTaskIDs),
		})
	} else {
		logger.Info("Created task in workspace", logger.Fields{"task_id": createdTask.ID, "workspace_id": req.WorkspaceID, "from": req.From, "to": req.To})
	}

	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"task":    createdTask,
	})
}

func (th *TaskHandler) handleUpdateTask(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TaskID                 string   `json:"task_id"`
		Status                 string   `json:"status"`
		Result                 string   `json:"result"`
		Error                  string   `json:"error"`
		To                     *string  `json:"to"`                      // Optional: reassign task to different agent
		AssignedNodeID         *string  `json:"assigned_node_id"`        // Optional: target specific agent instance/node
		InputTaskIDs           []string `json:"input_task_ids"`          // Optional: update input task connections
		ResultCombinationMode  *string  `json:"result_combination_mode"` // Optional: update combination mode
		CombinationInstruction *string  `json:"combination_instruction"` // Optional: update combination instruction
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.RespondError(w, http.StatusBadRequest, "Invalid request body", err)
		return
	}

	// Extract task ID from URL path if present (e.g., /api/orchestration/tasks/{id})
	pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/orchestration/tasks/"), "/")
	if len(pathParts) > 0 && pathParts[0] != "" {
		req.TaskID = pathParts[0]
	}

	if req.TaskID == "" {
		httputil.RespondValidationError(w, "task_id", "is required")
		return
	}

	// Handle task updates (input connections, reassignment, or combination mode)
	if req.InputTaskIDs != nil || req.To != nil || req.ResultCombinationMode != nil {
		logger.Debug("Updating task", logger.Fields{"task_id": req.TaskID})

		// Get task and workspace using helper
		task, ws, err := th.getTaskWithWorkspace(req.TaskID)
		if err != nil {
			logger.Error("", logger.Fields{"err": err})
			httputil.RespondError(w, http.StatusNotFound, "Failed to retrieve task or workspace", err)
			return
		}

		// Find and update task
		taskIndex := -1
		for i := range ws.Tasks {
			if ws.Tasks[i].ID == req.TaskID {
				taskIndex = i

				// Update input connections
				if req.InputTaskIDs != nil {
					ws.Tasks[i].InputTaskIDs = req.InputTaskIDs
					logger.Debug("📝 Updated task input connections", logger.Fields{"task_id": req.TaskID, "inputtaskids": req.InputTaskIDs})
				}

				// Update assignment using helper
				if req.To != nil {
					if req.AssignedNodeID != nil {
						logger.Debug("📝 Reassigning task to (node: )", logger.Fields{"task_id": req.TaskID, "to": *req.To, "assignednodeid": *req.AssignedNodeID})
					} else {
						logger.Debug("📝 Reassigning task to (no node id)", logger.Fields{"task_id": req.TaskID, "to": *req.To})
					}
					_, err = th.updateTaskAssignment(ws, req.TaskID, req.To, req.AssignedNodeID)
					if err != nil {
						logger.Error("", logger.Fields{"err": err})
						http.Error(w, err.Error(), http.StatusInternalServerError)
						return
					}
				}
				break
			}
		}

		if taskIndex == -1 {
			logger.Error("Task not found in workspace", logger.Fields{"task_id": req.TaskID, "workspaceid": task.WorkspaceID})
			http.Error(w, "Task not found in workspace", http.StatusNotFound)
			return
		}

		// Save workspace
		if err := th.workspaceStore.Save(ws); err != nil {
			logger.Error("Failed to save workspace", logger.Fields{"workspace_id": err})
			httputil.RespondError(w, http.StatusInternalServerError, "Failed to update task", err)
			return
		}

		logger.Info("Updated task", logger.Fields{"task_id": req.TaskID})

		// Publish event
		if th.eventBus != nil {
			eventData := map[string]interface{}{
				"task_id":     req.TaskID,
				"update_type": "task_update",
			}
			if req.InputTaskIDs != nil {
				eventData["input_task_ids"] = req.InputTaskIDs
			}
			if req.To != nil {
				eventData["to"] = *req.To
			}
			if req.AssignedNodeID != nil {
				eventData["assigned_node_id"] = *req.AssignedNodeID
			}

			th.eventBus.Publish(agentstudio.Event{
				Type:        agentstudio.EventWorkspaceUpdated,
				WorkspaceID: task.WorkspaceID,
				Data:        eventData,
			})
		}

		// Return updated task
		updatedTask, _ := th.communicator.GetTask(req.TaskID)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(updatedTask)
		return
	}

	// Legacy: Handle task reassignment alone (for backwards compatibility)
	if req.To != nil {
		logger.Debug("🔄 Reassigning task to", logger.Fields{"task_id": req.TaskID, "to": *req.To})

		// Get task and workspace using helper
		task, ws, err := th.getTaskWithWorkspace(req.TaskID)
		if err != nil {
			logger.Error("", logger.Fields{"err": err})
			httputil.RespondError(w, http.StatusNotFound, "Failed to retrieve task or workspace", err)
			return
		}

		// Update task assignment using helper
		_, err = th.updateTaskAssignment(ws, req.TaskID, req.To, req.AssignedNodeID)
		if err != nil {
			logger.Error("", logger.Fields{"err": err})
			http.Error(w, "Task not found in workspace", http.StatusNotFound)
			return
		}
		logger.Debug("📝 Updated task in workspace: ->", logger.Fields{"task_id": req.TaskID, "to": *req.To})

		// Save workspace
		if err := th.workspaceStore.Save(ws); err != nil {
			logger.Error("Failed to save workspace", logger.Fields{"workspace_id": err})
			httputil.RespondError(w, http.StatusInternalServerError, "Failed to update task", err)
			return
		}

		logger.Info("Reassigned task to", logger.Fields{"task_id": req.TaskID, "to": *req.To})

		// Publish event
		th.eventBus.Publish(agentstudio.Event{
			Type:        agentstudio.EventTaskAssigned,
			WorkspaceID: task.WorkspaceID,
			Data: map[string]interface{}{
				"task_id": req.TaskID,
				"to":      *req.To,
			},
		})

		// Return updated task
		updatedTask, _ := th.communicator.GetTask(req.TaskID)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(updatedTask)
		return
	}

	// Handle status update
	if req.Status == "" {
		http.Error(w, "status is required when not reassigning task", http.StatusBadRequest)
		return
	}

	// Update task status
	err := th.communicator.UpdateTaskStatus(
		req.TaskID,
		agentstudio.TaskStatus(req.Status),
		req.Result,
		req.Error,
	)

	if err != nil {
		logger.Error("Failed to update task status", logger.Fields{"task_id": err})
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"task_id": req.TaskID,
		"status":  req.Status,
	})
}

// handleDeleteTask deletes a task
func (th *TaskHandler) handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	taskID := r.URL.Query().Get("id")
	if taskID == "" {
		http.Error(w, "id parameter required", http.StatusBadRequest)
		return
	}

	workspaceID := r.URL.Query().Get("workspace_id")
	if workspaceID == "" {
		workspaceID = r.URL.Query().Get("studio_id")
	}

	if workspaceID != "" {
		ws, err := th.workspaceStore.Get(workspaceID)
		if err != nil {
			httputil.RespondError(w, http.StatusNotFound, "Workspace not found", err)
			return
		}

		if err := ws.DeleteTask(taskID); err != nil {
			httputil.RespondError(w, http.StatusNotFound, "Task not found", err)
			return
		}

		if err := th.workspaceStore.Save(ws); err != nil {
			logger.Error("Failed to save workspace", logger.Fields{"workspace_id": err})
			httputil.RespondError(w, http.StatusInternalServerError, "Failed to save workspace", err)
			return
		}

		logger.Info("Deleted task", logger.Fields{"task_id": taskID, "workspace_id": workspaceID})
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Task deleted successfully",
			"task_id": taskID,
		})
		return
	}

	// Fallback: search all workspaces
	if err := th.communicator.DeleteTask(taskID); err != nil {
		logger.Error("Failed to delete task", logger.Fields{"task_id": err})
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	logger.Info("Deleted task", logger.Fields{"task_id": taskID})
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Task deleted successfully",
		"task_id": taskID,
	})
}

// TaskResultsHandler retrieves results from one or more tasks
// GET /api/orchestration/task-results?task_ids=id1,id2,id3
func (th *TaskHandler) TaskResultsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// Get task IDs from query parameter
	taskIDsStr := r.URL.Query().Get("task_ids")
	if taskIDsStr == "" {
		http.Error(w, "task_ids parameter required (comma-separated)", http.StatusBadRequest)
		return
	}

	// Split comma-separated task IDs
	taskIDs := strings.Split(taskIDsStr, ",")
	for i := range taskIDs {
		taskIDs[i] = strings.TrimSpace(taskIDs[i])
	}

	// We need to find the workspace that contains these tasks
	// For simplicity, we'll search through all workspaces
	workspaceIDs, err := th.workspaceStore.List()
	if err != nil {
		logger.Error("Error listing workspaces", logger.Fields{"error": err})
		http.Error(w, "Failed to retrieve workspaces", http.StatusInternalServerError)
		return
	}

	// Collect results from all workspaces
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
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"results": allResults,
	})
}

// ExecuteTaskHandler handles manual task execution
func (th *TaskHandler) ExecuteTaskHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		TaskID string `json:"task_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.RespondError(w, http.StatusBadRequest, "Invalid request body", err)
		return
	}

	if req.TaskID == "" {
		http.Error(w, "task_id is required", http.StatusBadRequest)
		return
	}

	// Find the task across all workspaces
	workspaceIDs, err := th.workspaceStore.List()
	if err != nil {
		logger.Error("Error listing workspaces", logger.Fields{"error": err})
		httputil.RespondError(w, http.StatusInternalServerError, "Failed to list workspaces", err)
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
		http.Error(w, fmt.Sprintf("Task %s not found", req.TaskID), http.StatusNotFound)
		return
	}

	// Check if task is in a state that can be executed
	if foundTask.Status == agentstudio.TaskStatusCompleted {
		// Allow rerun of completed tasks by resetting status
		logger.Info("🔄 Rerunning completed task", logger.Fields{"task_id": req.TaskID})
		foundTask.Status = agentstudio.TaskStatusPending
		foundTask.Result = ""
		foundTask.Error = ""
		foundTask.StartedAt = nil
		foundTask.CompletedAt = nil

		// Save the reset task status
		if err := foundWorkspace.UpdateTask(*foundTask); err != nil {
			logger.Error("Failed to reset task status", logger.Fields{"status": err})
			http.Error(w, "Failed to reset task for rerun", http.StatusInternalServerError)
			return
		}
		if err := th.workspaceStore.Save(foundWorkspace); err != nil {
			logger.Error("Failed to save workspace", logger.Fields{"workspace_id": err})
			http.Error(w, "Failed to save workspace", http.StatusInternalServerError)
			return
		}
	}

	if foundTask.Status == agentstudio.TaskStatusInProgress {
		http.Error(w, "Task is already in progress", http.StatusBadRequest)
		return
	}

	// Check if task handler is available
	if th.taskHandler == nil {
		logger.Error("Task handler not set", logger.Fields{})
		http.Error(w, "Task execution not available", http.StatusInternalServerError)
		return
	}

	// Execute any pending input tasks first (cascading execution)
	if len(foundTask.InputTaskIDs) > 0 {
		logger.Info("🔗 Task has input tasks, checking if they need execution first", logger.Fields{"task_id": foundTask.ID, "input_count": len(foundTask.InputTaskIDs)})

		if err := th.executeInputTasksIfNeeded(foundWorkspace, foundTask); err != nil {
			logger.Error("Failed to execute input tasks", logger.Fields{"task_id": foundTask.ID, "error": err})
			http.Error(w, fmt.Sprintf("Failed to execute input tasks: %v", err), http.StatusInternalServerError)
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
			logger.Error("Failed to save workspace", logger.Fields{"workspace_id": err})
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

		logger.Debug("▶️ Manually executing task for agent", logger.Fields{"description": foundTask.Description, "agent": foundTask.ID, "to": foundTask.To})

		// Gather input task results and substitute placeholders in description
		var inputResults []string
		if len(foundTask.InputTaskIDs) > 0 {
			logger.Debug("🔗 Task has input task IDs", logger.Fields{"task_id": foundTask.ID, "inputtaskids)": len(foundTask.InputTaskIDs), "inputtaskids": foundTask.InputTaskIDs})

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
					logger.Debug("🔄 Substituted placeholders in description", logger.Fields{})
					logger.Debug("Original", logger.Fields{"originalDesc": originalDesc})
					logger.Debug("Processed", logger.Fields{"description": foundTask.Description})
				}
			}
		} else {
			logger.Debug("ℹ️ Task has no input task IDs", logger.Fields{"task_id": foundTask.ID})
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
			logger.Error("Failed to save workspace", logger.Fields{"workspace_id": err})
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
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
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

	logger.Info("🔍 Checking input tasks for execution", logger.Fields{"task_id": task.ID, "input_count": len(task.InputTaskIDs)})

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
			logger.Debug("✅ Input task already completed, skipping", logger.Fields{"input_task_id": inputTaskID, "status": inputTask.Status})
			continue
		}

		logger.Info("🚀 Auto-executing input task", logger.Fields{"input_task_id": inputTaskID, "description": inputTask.Description})

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

			logger.Info("🔄 Auto-assigning input task to parent's agent", logger.Fields{
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
		logger.Debug("▶️ Executing input task", logger.Fields{"input_task_id": inputTaskID, "agent": inputTask.To})
		result, err := th.taskHandler.ExecuteTask(ctx, inputTask.To, *inputTask)

		// Update task with result
		completed := time.Now()
		inputTask.CompletedAt = &completed

		if err != nil {
			logger.Error("Input task execution failed", logger.Fields{"input_task_id": inputTaskID, "error": err})
			inputTask.Status = agentstudio.TaskStatusFailed
			inputTask.Error = err.Error()
		} else {
			logger.Info("✅ Input task completed successfully", logger.Fields{"input_task_id": inputTaskID})
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

	logger.Info("✅ All input tasks executed successfully", logger.Fields{"task_id": task.ID})
	return nil
}

// ScheduledTasksHandler handles listing and creating scheduled tasks
func (th *TaskHandler) ScheduledTasksHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		th.handleListScheduledTasks(w, r)
	case http.MethodPost:
		th.handleCreateScheduledTask(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleListScheduledTasks lists all scheduled tasks for a workspace
func (th *TaskHandler) handleListScheduledTasks(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.URL.Query().Get("studio_id")
	if workspaceID == "" {
		http.Error(w, "workspace_id is required", http.StatusBadRequest)
		return
	}

	ws, err := th.workspaceStore.Get(workspaceID)
	if err != nil {
		logger.Error("Error getting workspace", logger.Fields{"error": workspaceID, "err": err})
		httputil.RespondError(w, http.StatusNotFound, "Workspace not found", err)
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"scheduled_tasks": ws.ScheduledTasks,
		"count":           len(ws.ScheduledTasks),
	})
}

// handleCreateScheduledTask creates a new scheduled task
func (th *TaskHandler) handleCreateScheduledTask(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkspaceID string                     `json:"studio_id"`
		Name        string                     `json:"name"`
		Description string                     `json:"description"`
		From        string                     `json:"from"`
		To          string                     `json:"to"`
		Prompt      string                     `json:"prompt"`
		Priority    int                        `json:"priority"`
		Schedule    agentstudio.ScheduleConfig `json:"schedule"`
		Enabled     bool                       `json:"enabled"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.RespondError(w, http.StatusBadRequest, "Invalid request body", err)
		return
	}

	// Validate required fields
	if req.WorkspaceID == "" {
		http.Error(w, "workspace_id is required", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	if req.Prompt == "" {
		http.Error(w, "prompt is required", http.StatusBadRequest)
		return
	}
	if req.From == "" {
		http.Error(w, "from is required", http.StatusBadRequest)
		return
	}
	if req.To == "" {
		http.Error(w, "to is required", http.StatusBadRequest)
		return
	}

	// Get workspace
	ws, err := th.workspaceStore.Get(req.WorkspaceID)
	if err != nil {
		logger.Error("Error getting workspace", logger.Fields{"err": err, "workspace_id": req.WorkspaceID})
		httputil.RespondError(w, http.StatusNotFound, "Workspace not found", err)
		return
	}

	// Create scheduled task
	now := time.Now()
	st := agentstudio.ScheduledTask{
		WorkspaceID: req.WorkspaceID,
		Name:        req.Name,
		Description: req.Description,
		From:        req.From,
		To:          req.To,
		Prompt:      req.Prompt,
		Priority:    req.Priority,
		Schedule:    req.Schedule,
		Enabled:     req.Enabled,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	// Calculate initial NextRun if enabled
	if st.Enabled {
		nextRun := calculateInitialNextRun(st.Schedule, now)
		st.NextRun = nextRun
	}

	// Add to workspace
	if err := ws.AddScheduledTask(st); err != nil {
		logger.Error("Failed to add scheduled task", logger.Fields{"task_id": err})
		httputil.RespondError(w, http.StatusBadRequest, "Failed to add scheduled task", err)
		return
	}

	// Save workspace
	if err := th.workspaceStore.Save(ws); err != nil {
		logger.Error("Failed to save workspace", logger.Fields{"workspace_id": err})
		httputil.RespondError(w, http.StatusInternalServerError, "Failed to save workspace", err)
		return
	}

	// Get the created scheduled task (now has ID)
	var createdTask *agentstudio.ScheduledTask
	for i := len(ws.ScheduledTasks) - 1; i >= 0; i-- {
		if ws.ScheduledTasks[i].Name == req.Name {
			createdTask = &ws.ScheduledTasks[i]
			break
		}
	}

	logger.Info("Created scheduled task in workspace", logger.Fields{"workspace_id": createdTask.ID, "workspaceid": req.WorkspaceID, "name": req.Name})

	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":        true,
		"scheduled_task": createdTask,
	})
}

// ScheduledTaskHandler handles get/update/delete for a specific scheduled task
func (th *TaskHandler) ScheduledTaskHandler(w http.ResponseWriter, r *http.Request) {
	// Extract ID from URL path
	// Path format: /api/orchestration/scheduled-tasks/{id} or /api/orchestration/scheduled-tasks/{id}/{action}
	parts := strings.Split(strings.TrimSuffix(r.URL.Path, "/"), "/")

	// Minimum parts: ["", "api", "orchestration", "scheduled-tasks", "{id}"] = 5
	if len(parts) < 5 {
		http.Error(w, "Invalid URL: missing task ID", http.StatusBadRequest)
		return
	}

	id := parts[4] // The ID is always at index 4

	// Handle special actions (e.g., /api/orchestration/scheduled-tasks/{id}/enable)
	if len(parts) >= 6 {
		action := parts[5]

		switch action {
		case "enable":
			th.handleEnableScheduledTask(w, r, id, true)
			return
		case "disable":
			th.handleEnableScheduledTask(w, r, id, false)
			return
		case "trigger":
			th.handleTriggerScheduledTask(w, r, id)
			return
		default:
			http.Error(w, fmt.Sprintf("Unknown action: %s", action), http.StatusBadRequest)
			return
		}
	}

	switch r.Method {
	case http.MethodGet:
		th.handleGetScheduledTask(w, r, id)
	case http.MethodPut:
		th.handleUpdateScheduledTask(w, r, id)
	case http.MethodDelete:
		th.handleDeleteScheduledTask(w, r, id)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleGetScheduledTask retrieves a specific scheduled task
func (th *TaskHandler) handleGetScheduledTask(w http.ResponseWriter, r *http.Request, id string) {
	// Find the scheduled task across all workspaces
	workspaceIDs, err := th.workspaceStore.List()
	if err != nil {
		logger.Error("Error listing workspaces", logger.Fields{"error": err})
		httputil.RespondError(w, http.StatusInternalServerError, "Failed to list workspaces", err)
		return
	}

	for _, wsID := range workspaceIDs {
		ws, err := th.workspaceStore.Get(wsID)
		if err != nil {
			continue
		}

		st, err := ws.GetScheduledTask(id)
		if err == nil {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"scheduled_task": st,
			})
			return
		}
	}

	http.Error(w, fmt.Sprintf("Scheduled task %s not found", id), http.StatusNotFound)
}

// handleUpdateScheduledTask updates a scheduled task
func (th *TaskHandler) handleUpdateScheduledTask(w http.ResponseWriter, r *http.Request, id string) {
	var req struct {
		Name        *string                     `json:"name,omitempty"`
		Description *string                     `json:"description,omitempty"`
		Prompt      *string                     `json:"prompt,omitempty"`
		Priority    *int                        `json:"priority,omitempty"`
		Schedule    *agentstudio.ScheduleConfig `json:"schedule,omitempty"`
		Enabled     *bool                       `json:"enabled,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.RespondError(w, http.StatusBadRequest, "Invalid request body", err)
		return
	}

	// Find the scheduled task
	workspaceIDs, err := th.workspaceStore.List()
	if err != nil {
		logger.Error("Error listing workspaces", logger.Fields{"error": err})
		httputil.RespondError(w, http.StatusInternalServerError, "Failed to list workspaces", err)
		return
	}

	for _, wsID := range workspaceIDs {
		ws, err := th.workspaceStore.Get(wsID)
		if err != nil {
			continue
		}

		st, err := ws.GetScheduledTask(id)
		if err != nil {
			continue
		}

		// Update fields if provided
		if req.Name != nil {
			st.Name = *req.Name
		}
		if req.Description != nil {
			st.Description = *req.Description
		}
		if req.Prompt != nil {
			st.Prompt = *req.Prompt
		}
		if req.Priority != nil {
			st.Priority = *req.Priority
		}
		if req.Schedule != nil {
			st.Schedule = *req.Schedule
			// Recalculate NextRun if schedule changed
			if st.Enabled {
				now := time.Now()
				nextRun := calculateInitialNextRun(st.Schedule, now)
				st.NextRun = nextRun
			}
		}
		if req.Enabled != nil {
			wasEnabled := st.Enabled
			st.Enabled = *req.Enabled

			// Calculate NextRun when enabling
			if st.Enabled && !wasEnabled {
				now := time.Now()
				nextRun := calculateInitialNextRun(st.Schedule, now)
				st.NextRun = nextRun
			} else if !st.Enabled && wasEnabled {
				st.NextRun = nil
			}
		}

		st.UpdatedAt = time.Now()

		if err := ws.UpdateScheduledTask(*st); err != nil {
			logger.Error("Failed to update scheduled task", logger.Fields{"task_id": err})
			httputil.RespondError(w, http.StatusInternalServerError, "Failed to update scheduled task", err)
			return
		}

		if err := th.workspaceStore.Save(ws); err != nil {
			logger.Error("Failed to save workspace", logger.Fields{"workspace_id": err})
			httputil.RespondError(w, http.StatusInternalServerError, "Failed to save workspace", err)
			return
		}

		logger.Info("Updated scheduled task", logger.Fields{"task_id": id})

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success":        true,
			"scheduled_task": st,
		})
		return
	}

	http.Error(w, fmt.Sprintf("Scheduled task %s not found", id), http.StatusNotFound)
}

// handleDeleteScheduledTask deletes a scheduled task
func (th *TaskHandler) handleDeleteScheduledTask(w http.ResponseWriter, r *http.Request, id string) {
	workspaceIDs, err := th.workspaceStore.List()
	if err != nil {
		logger.Error("Error listing workspaces", logger.Fields{"error": err})
		httputil.RespondError(w, http.StatusInternalServerError, "Failed to list workspaces", err)
		return
	}

	for _, wsID := range workspaceIDs {
		ws, err := th.workspaceStore.Get(wsID)
		if err != nil {
			continue
		}

		if err := ws.DeleteScheduledTask(id); err == nil {
			if err := th.workspaceStore.Save(ws); err != nil {
				logger.Error("Failed to save workspace", logger.Fields{"workspace_id": err})
				httputil.RespondError(w, http.StatusInternalServerError, "Failed to save workspace", err)
				return
			}

			logger.Info("Deleted scheduled task", logger.Fields{"task_id": id})

			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
			})
			return
		}
	}

	http.Error(w, fmt.Sprintf("Scheduled task %s not found", id), http.StatusNotFound)
}

// handleEnableScheduledTask enables or disables a scheduled task
func (th *TaskHandler) handleEnableScheduledTask(w http.ResponseWriter, r *http.Request, id string, enable bool) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	workspaceIDs, err := th.workspaceStore.List()
	if err != nil {
		logger.Error("Error listing workspaces", logger.Fields{"error": err})
		httputil.RespondError(w, http.StatusInternalServerError, "Failed to list workspaces", err)
		return
	}

	for _, wsID := range workspaceIDs {
		ws, err := th.workspaceStore.Get(wsID)
		if err != nil {
			continue
		}

		st, err := ws.GetScheduledTask(id)
		if err != nil {
			continue
		}

		st.Enabled = enable
		st.UpdatedAt = time.Now()

		// Calculate NextRun when enabling
		if enable {
			now := time.Now()
			nextRun := calculateInitialNextRun(st.Schedule, now)
			st.NextRun = nextRun
		} else {
			st.NextRun = nil
		}

		if err := ws.UpdateScheduledTask(*st); err != nil {
			logger.Error("Failed to update scheduled task", logger.Fields{"task_id": err})
			httputil.RespondError(w, http.StatusInternalServerError, "Failed to update scheduled task", err)
			return
		}

		if err := th.workspaceStore.Save(ws); err != nil {
			logger.Error("Failed to save workspace", logger.Fields{"workspace_id": err})
			httputil.RespondError(w, http.StatusInternalServerError, "Failed to save workspace", err)
			return
		}

		action := "disabled"
		if enable {
			action = "enabled"
		}
		// Capitalize first letter manually (strings.Title is deprecated)
		capitalizedAction := action
		if len(action) > 0 {
			capitalizedAction = strings.ToUpper(action[:1]) + action[1:]
		}
		logger.Info("scheduled task", logger.Fields{"task_id": capitalizedAction, "id": id})

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success":        true,
			"enabled":        enable,
			"scheduled_task": st,
		})
		return
	}

	http.Error(w, fmt.Sprintf("Scheduled task %s not found", id), http.StatusNotFound)
}

// handleTriggerScheduledTask manually triggers a scheduled task
func (th *TaskHandler) handleTriggerScheduledTask(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	workspaceIDs, err := th.workspaceStore.List()
	if err != nil {
		logger.Error("Error listing workspaces", logger.Fields{"error": err})
		httputil.RespondError(w, http.StatusInternalServerError, "Failed to list workspaces", err)
		return
	}

	for _, wsID := range workspaceIDs {
		ws, err := th.workspaceStore.Get(wsID)
		if err != nil {
			continue
		}

		st, err := ws.GetScheduledTask(id)
		if err != nil {
			continue
		}

		// Create a task from the scheduled task
		task := agentstudio.Task{
			WorkspaceID: ws.ID,
			From:        st.From,
			To:          st.To,
			Description: st.Prompt,
			Priority:    st.Priority,
			Context:     st.Context,
			Status:      agentstudio.TaskStatusPending,
		}

		if err := ws.AddTask(task); err != nil {
			logger.Error("Failed to create task from scheduled task", logger.Fields{"task_id": err})
			httputil.RespondError(w, http.StatusBadRequest, "Failed to create task", err)
			return
		}

		if err := th.workspaceStore.Save(ws); err != nil {
			logger.Error("Failed to save workspace", logger.Fields{"workspace_id": err})
			httputil.RespondError(w, http.StatusInternalServerError, "Failed to save workspace", err)
			return
		}

		// Get the created task ID
		var taskID string
		if len(ws.Tasks) > 0 {
			taskID = ws.Tasks[len(ws.Tasks)-1].ID
		}

		logger.Info("Manually triggered scheduled task , created task", logger.Fields{"task_id": id, "taskID": taskID})

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"task_id": taskID,
		})
		return
	}

	http.Error(w, fmt.Sprintf("Scheduled task %s not found", id), http.StatusNotFound)
}

// calculateInitialNextRun calculates the initial next run time for a schedule
func calculateInitialNextRun(config agentstudio.ScheduleConfig, now time.Time) *time.Time {
	switch config.Type {
	case agentstudio.ScheduleOnce:
		if config.ExecuteAt != nil {
			return config.ExecuteAt
		}
		return nil

	case agentstudio.ScheduleInterval:
		if config.Interval == 0 {
			return nil
		}
		next := now.Add(config.Interval)
		return &next

	case agentstudio.ScheduleDaily:
		hour, minute, err := parseScheduleTime(config.TimeOfDay)
		if err != nil {
			return nil
		}

		// Calculate next occurrence
		next := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
		if next.Before(now) || next.Equal(now) {
			// If time has passed today, schedule for tomorrow
			next = next.AddDate(0, 0, 1)
		}

		return &next

	case agentstudio.ScheduleWeekly:
		if config.DayOfWeek < 0 || config.DayOfWeek > 6 {
			return nil
		}

		hour, minute, err := parseScheduleTime(config.TimeOfDay)
		if err != nil {
			return nil
		}

		targetWeekday := time.Weekday(config.DayOfWeek)
		currentWeekday := now.Weekday()

		daysUntil := int(targetWeekday - currentWeekday)
		if daysUntil < 0 {
			daysUntil += 7
		} else if daysUntil == 0 {
			// Same day - check if time has passed
			testTime := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
			if testTime.Before(now) || testTime.Equal(now) {
				daysUntil = 7 // Next week
			}
		}

		next := time.Date(
			now.Year(),
			now.Month(),
			now.Day()+daysUntil,
			hour,
			minute,
			0,
			0,
			now.Location(),
		)

		return &next

	case agentstudio.ScheduleCron:
		if config.CronExpr == "" {
			return nil
		}

		// Validate and parse cron expression using agentstudio's validator
		if err := agentstudio.ValidateCronExpression(config.CronExpr); err != nil {
			return nil
		}

		// Parse cron expression
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		schedule, err := parser.Parse(config.CronExpr)
		if err != nil {
			return nil
		}

		// Calculate next execution time from now
		next := schedule.Next(now)
		return &next

	case agentstudio.ScheduleRelativeDelay:
		if config.DelayDuration == 0 {
			return nil
		}

		// Calculate initial next run as now + DelayDuration
		next := now.Add(config.DelayDuration)
		return &next

	default:
		return nil
	}
}

// parseScheduleTime converts "HH:MM" strings into hour/minute integers and rejects invalid ranges.
func parseScheduleTime(timeOfDay string) (int, int, error) {
	if timeOfDay == "" {
		return 0, 0, fmt.Errorf("time of day is empty")
	}

	parts := strings.Split(timeOfDay, ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("time of day must be in HH:MM format")
	}

	hour, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid hour value")
	}

	minute, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid minute value")
	}

	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, 0, fmt.Errorf("time of day out of range")
	}

	return hour, minute, nil
}

// substituteInputPlaceholders replaces {inputN}, {previous}, {result} with actual values
// from input task results. This enables task description templating for chaining operations.
// Example: "{input1} * 2" with inputs ["4"] becomes "4 * 2"
func substituteInputPlaceholders(description string, inputs []string) string {
	if description == "" || len(inputs) == 0 {
		return description
	}

	if !strings.Contains(description, "{") {
		return description
	}

	result := description

	// Replace numbered placeholders: {input1}, {input2}, etc.
	for i, input := range inputs {
		placeholder := fmt.Sprintf("{input%d}", i+1)
		result = strings.ReplaceAll(result, placeholder, input)
	}

	// Replace shortcuts: {previous} and {result} (both map to first input)
	result = strings.ReplaceAll(result, "{previous}", inputs[0])
	result = strings.ReplaceAll(result, "{result}", inputs[0])

	return result
}

// getTaskWithWorkspace retrieves a task and its associated workspace
// Returns the task, workspace, and any error encountered
func (th *TaskHandler) getTaskWithWorkspace(taskID string) (*agentstudio.Task, *agentstudio.Workspace, error) {
	task, err := th.communicator.GetTask(taskID)
	if err != nil {
		return nil, nil, fmt.Errorf("task not found: %w", err)
	}

	ws, err := th.workspaceStore.Get(task.WorkspaceID)
	if err != nil {
		return nil, nil, fmt.Errorf("workspace not found: %w", err)
	}

	return task, ws, nil
}

// updateTaskAssignment updates the assignment (To and AssignedNodeID) of a task within a workspace
// Returns the index of the updated task, or -1 if not found
func (th *TaskHandler) updateTaskAssignment(ws *agentstudio.Workspace, taskID string, newTo *string, assignedNodeID *string) (int, error) {
	for i := range ws.Tasks {
		if ws.Tasks[i].ID == taskID {
			if newTo != nil {
				ws.Tasks[i].To = *newTo
			}

			if assignedNodeID != nil {
				ws.Tasks[i].AssignedNodeID = *assignedNodeID
			} else if newTo != nil {
				// If reassigning but no node ID specified, clear it to avoid stale linkage
				ws.Tasks[i].AssignedNodeID = ""
			}

			return i, nil
		}
	}

	return -1, fmt.Errorf("task not found in workspace")
}

// SchedulerNodesHandler handles CRUD operations for scheduler nodes (canvas-based scheduled tasks)
// GET: List all scheduler nodes in a workspace
// POST: Create a new scheduler node
func (th *TaskHandler) SchedulerNodesHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		th.handleListSchedulerNodes(w, r)
	case http.MethodPost:
		th.handleCreateSchedulerNode(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleListSchedulerNodes lists all scheduler nodes (scheduled tasks) for a workspace
// This includes both canvas-created schedulers (with CanvasNodeID) and dashboard-created schedulers (without CanvasNodeID)
// Dashboard-created schedulers are automatically assigned a canvas_node_id when loaded
func (th *TaskHandler) handleListSchedulerNodes(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.URL.Query().Get("studio_id")
	if workspaceID == "" {
		http.Error(w, "studio_id parameter is required", http.StatusBadRequest)
		return
	}

	ws, err := th.workspaceStore.Get(workspaceID)
	if err != nil {
		logger.Error("Error getting workspace", logger.Fields{"workspace_id": workspaceID, "err": err})
		httputil.RespondError(w, http.StatusNotFound, "Workspace not found", err)
		return
	}

	// Return ALL scheduled tasks as scheduler nodes (both canvas and dashboard-created)
	// Auto-assign canvas_node_id to dashboard-created schedulers for display
	schedulerNodes := make([]map[string]interface{}, 0)
	needsSave := false

	for i := range ws.ScheduledTasks {
		st := &ws.ScheduledTasks[i]

		// Auto-assign canvas_node_id if missing (dashboard-created scheduler)
		// Use scheduler ID to generate a stable, deterministic canvas node ID
		if st.CanvasNodeID == "" {
			st.CanvasNodeID = fmt.Sprintf("scheduler-%s", st.ID)
			needsSave = true
			logger.Debug("Auto-assigned canvas_node_id to dashboard scheduler", logger.Fields{
				"scheduler_id":   st.ID,
				"canvas_node_id": st.CanvasNodeID,
			})
		}

		// Get position from layout if available, otherwise use default position
		var position *agentstudio.Position
		if ws.Layout != nil && ws.Layout.SchedulerPositions != nil {
			if pos, exists := ws.Layout.SchedulerPositions[st.CanvasNodeID]; exists {
				position = &pos
			}
		}

		// If no position exists, assign a default position (centered, with offset per scheduler)
		if position == nil {
			defaultX := 100.0 + float64(i*150) // Offset horizontally for each scheduler
			defaultY := 100.0
			position = &agentstudio.Position{X: defaultX, Y: defaultY}

			// Save position to layout
			if ws.Layout == nil {
				ws.Layout = &agentstudio.CanvasLayout{
					SchedulerPositions: make(map[string]agentstudio.Position),
				}
			}
			if ws.Layout.SchedulerPositions == nil {
				ws.Layout.SchedulerPositions = make(map[string]agentstudio.Position)
			}
			ws.Layout.SchedulerPositions[st.CanvasNodeID] = *position
			needsSave = true
		}

		node := map[string]interface{}{
			"node_id":           st.CanvasNodeID,
			"scheduled_task":    st,
			"scheduled_task_id": st.ID,
			"position":          position,
		}
		schedulerNodes = append(schedulerNodes, node)
	}

	// Save workspace if any changes were made (auto-assigned IDs or positions)
	if needsSave {
		if err := th.workspaceStore.Save(ws); err != nil {
			logger.Error("Failed to save workspace after auto-assigning canvas IDs", logger.Fields{
				"workspace_id": workspaceID,
				"err":          err,
			})
		}
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"scheduler_nodes": schedulerNodes,
		"count":           len(schedulerNodes),
	})
}

// handleCreateSchedulerNode creates a new scheduler node (scheduled task with canvas position)
func (th *TaskHandler) handleCreateSchedulerNode(w http.ResponseWriter, r *http.Request) {
	// Extract workspace_id from URL path
	// Path format: /api/orchestration/workspaces/{workspace_id}/scheduler-nodes
	parts := strings.Split(strings.TrimSuffix(r.URL.Path, "/"), "/")

	// Find workspace_id in path (should be after "workspaces")
	var workspaceID string
	for i, part := range parts {
		if part == "workspaces" && i+1 < len(parts) {
			workspaceID = parts[i+1]
			break
		}
	}

	// Fallback: try getting from query param if not in path
	if workspaceID == "" {
		workspaceID = r.URL.Query().Get("studio_id")
	}

	if workspaceID == "" {
		http.Error(w, "workspace_id is required in URL path", http.StatusBadRequest)
		return
	}

	var req struct {
		Name        string                     `json:"name"`
		Description string                     `json:"description"`
		From        string                     `json:"from"`
		To          string                     `json:"to"`
		Prompt      string                     `json:"prompt"`
		Priority    int                        `json:"priority"`
		Schedule    agentstudio.ScheduleConfig `json:"schedule"`
		Enabled     bool                       `json:"enabled"`
		X           float64                    `json:"x"`
		Y           float64                    `json:"y"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.RespondError(w, http.StatusBadRequest, "Invalid request body", err)
		return
	}

	// Validate required fields
	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	// Defaults for scheduler nodes
	if req.From == "" {
		req.From = "scheduler"
	}

	// Validate schedule configuration
	if err := validateScheduleConfig(req.Schedule); err != nil {
		httputil.RespondError(w, http.StatusBadRequest, "Invalid schedule configuration", err)
		return
	}

	// Get workspace
	ws, err := th.workspaceStore.Get(workspaceID)
	if err != nil {
		logger.Error("Error getting workspace", logger.Fields{"workspace_id": workspaceID, "err": err})
		httputil.RespondError(w, http.StatusNotFound, "Workspace not found", err)
		return
	}

	// Check scheduler node limit (max 50 per workspace)
	schedulerNodeCount := 0
	for _, st := range ws.ScheduledTasks {
		if st.CanvasNodeID != "" {
			schedulerNodeCount++
		}
	}
	if schedulerNodeCount >= 50 {
		http.Error(w, "Maximum of 50 scheduler nodes per workspace reached", http.StatusBadRequest)
		return
	}

	// Generate unique CanvasNodeID
	nodeID := "scheduler-" + generateNodeID()

	// Create scheduled task
	now := time.Now()
	st := agentstudio.ScheduledTask{
		WorkspaceID:  workspaceID,
		CanvasNodeID: nodeID,
		Name:         req.Name,
		Description:  req.Description,
		From:         req.From,
		To:           req.To,
		Prompt:       req.Prompt,
		Priority:     req.Priority,
		Schedule:     req.Schedule,
		Enabled:      req.Enabled,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	// Calculate initial NextRun if enabled
	if st.Enabled {
		nextRun := calculateInitialNextRun(st.Schedule, now)
		st.NextRun = nextRun
	}

	// Add to workspace
	if err := ws.AddScheduledTask(st); err != nil {
		logger.Error("Failed to add scheduled task", logger.Fields{"err": err})
		httputil.RespondError(w, http.StatusBadRequest, "Failed to add scheduler node", err)
		return
	}

	// Initialize layout if needed
	if ws.Layout == nil {
		ws.Layout = &agentstudio.CanvasLayout{}
	}
	if ws.Layout.SchedulerPositions == nil {
		ws.Layout.SchedulerPositions = make(map[string]agentstudio.Position)
	}

	// Add position to layout
	ws.Layout.SchedulerPositions[nodeID] = agentstudio.Position{
		X: req.X,
		Y: req.Y,
	}

	// Save workspace
	if err := th.workspaceStore.Save(ws); err != nil {
		logger.Error("Failed to save workspace", logger.Fields{"workspace_id": err})
		httputil.RespondError(w, http.StatusInternalServerError, "Failed to save workspace", err)
		return
	}

	// Get the created scheduled task (now has ID)
	var createdTask *agentstudio.ScheduledTask
	for i := len(ws.ScheduledTasks) - 1; i >= 0; i-- {
		if ws.ScheduledTasks[i].CanvasNodeID == nodeID {
			createdTask = &ws.ScheduledTasks[i]
			break
		}
	}

	logger.Info("Created scheduler node in workspace", logger.Fields{
		"node_id":           nodeID,
		"scheduled_task_id": createdTask.ID,
		"workspace_id":      workspaceID,
		"name":              req.Name,
	})

	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":           true,
		"node_id":           nodeID,
		"scheduled_task_id": createdTask.ID,
		"scheduled_task":    createdTask,
	})
}

// SchedulerNodeHandler handles operations for a specific scheduler node
// GET: Get scheduler node details
// PUT: Update scheduler node
// DELETE: Delete scheduler node
func (th *TaskHandler) SchedulerNodeHandler(w http.ResponseWriter, r *http.Request) {
	// Extract node ID from URL path
	// Path format: /api/orchestration/workspaces/{workspace_id}/scheduler-nodes/{node_id}
	parts := strings.Split(strings.TrimSuffix(r.URL.Path, "/"), "/")

	// Find node_id in path (should be last part)
	if len(parts) < 2 {
		http.Error(w, "Invalid URL: missing node ID", http.StatusBadRequest)
		return
	}
	nodeID := parts[len(parts)-1]

	// Note: Special actions like /trigger would be handled here if needed
	// Example: /scheduler-nodes/{node_id}/trigger
	// Currently only supporting direct node operations (GET, PUT, DELETE)

	switch r.Method {
	case http.MethodGet:
		th.handleGetSchedulerNode(w, r, nodeID)
	case http.MethodPut:
		th.handleUpdateSchedulerNode(w, r, nodeID)
	case http.MethodDelete:
		th.handleDeleteSchedulerNode(w, r, nodeID)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleGetSchedulerNode retrieves a specific scheduler node
func (th *TaskHandler) handleGetSchedulerNode(w http.ResponseWriter, r *http.Request, nodeID string) {
	workspaceID := r.URL.Query().Get("studio_id")
	if workspaceID == "" {
		http.Error(w, "studio_id parameter is required", http.StatusBadRequest)
		return
	}

	ws, err := th.workspaceStore.Get(workspaceID)
	if err != nil {
		logger.Error("Error getting workspace", logger.Fields{"workspace_id": workspaceID, "err": err})
		httputil.RespondError(w, http.StatusNotFound, "Workspace not found", err)
		return
	}

	// Find scheduled task by CanvasNodeID
	var foundTask *agentstudio.ScheduledTask
	for i := range ws.ScheduledTasks {
		if ws.ScheduledTasks[i].CanvasNodeID == nodeID {
			foundTask = &ws.ScheduledTasks[i]
			break
		}
	}

	if foundTask == nil {
		http.Error(w, fmt.Sprintf("Scheduler node %s not found", nodeID), http.StatusNotFound)
		return
	}

	// Get position from layout
	var position *agentstudio.Position
	if ws.Layout != nil && ws.Layout.SchedulerPositions != nil {
		if pos, exists := ws.Layout.SchedulerPositions[nodeID]; exists {
			position = &pos
		}
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"node_id":        nodeID,
		"scheduled_task": foundTask,
		"position":       position,
	})
}

// handleUpdateSchedulerNode updates a scheduler node
func (th *TaskHandler) handleUpdateSchedulerNode(w http.ResponseWriter, r *http.Request, nodeID string) {
	var req struct {
		WorkspaceID  string                      `json:"studio_id"`
		To           *string                     `json:"to,omitempty"`
		TargetTaskID *string                     `json:"target_task_id,omitempty"`
		Name         *string                     `json:"name,omitempty"`
		Description  *string                     `json:"description,omitempty"`
		Prompt       *string                     `json:"prompt,omitempty"`
		Priority     *int                        `json:"priority,omitempty"`
		Schedule     *agentstudio.ScheduleConfig `json:"schedule,omitempty"`
		Enabled      *bool                       `json:"enabled,omitempty"`
		X            *float64                    `json:"x,omitempty"`
		Y            *float64                    `json:"y,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.RespondError(w, http.StatusBadRequest, "Invalid request body", err)
		return
	}

	// Get workspace_id from query parameter or request body
	workspaceID := r.URL.Query().Get("studio_id")
	if workspaceID == "" {
		workspaceID = req.WorkspaceID
	}

	if workspaceID == "" {
		http.Error(w, "studio_id is required", http.StatusBadRequest)
		return
	}

	// Validate schedule configuration if provided
	if req.Schedule != nil {
		if err := validateScheduleConfig(*req.Schedule); err != nil {
			httputil.RespondError(w, http.StatusBadRequest, "Invalid schedule configuration", err)
			return
		}
	}

	ws, err := th.workspaceStore.Get(workspaceID)
	if err != nil {
		logger.Error("Error getting workspace", logger.Fields{"workspace_id": workspaceID, "err": err})
		httputil.RespondError(w, http.StatusNotFound, "Workspace not found", err)
		return
	}

	// Find scheduled task by CanvasNodeID
	var taskIndex int = -1
	var st *agentstudio.ScheduledTask
	for i := range ws.ScheduledTasks {
		if ws.ScheduledTasks[i].CanvasNodeID == nodeID {
			taskIndex = i
			st = &ws.ScheduledTasks[i]
			break
		}
	}

	if st == nil {
		http.Error(w, fmt.Sprintf("Scheduler node %s not found", nodeID), http.StatusNotFound)
		return
	}

	// Update fields if provided
	if req.To != nil {
		st.To = *req.To
	}
	if req.TargetTaskID != nil {
		st.TargetTaskID = *req.TargetTaskID
	}
	if req.Name != nil {
		st.Name = *req.Name
	}
	if req.Description != nil {
		st.Description = *req.Description
	}
	if req.Prompt != nil {
		st.Prompt = *req.Prompt
	}
	if req.Priority != nil {
		st.Priority = *req.Priority
	}
	if req.Schedule != nil {
		st.Schedule = *req.Schedule
		// Recalculate NextRun if schedule changed
		if st.Enabled {
			now := time.Now()
			nextRun := calculateInitialNextRun(st.Schedule, now)
			st.NextRun = nextRun
		}
	}
	if req.Enabled != nil {
		wasEnabled := st.Enabled
		st.Enabled = *req.Enabled

		// Calculate NextRun when enabling
		if st.Enabled && !wasEnabled {
			now := time.Now()
			nextRun := calculateInitialNextRun(st.Schedule, now)
			st.NextRun = nextRun
		} else if !st.Enabled && wasEnabled {
			st.NextRun = nil
		}
	}

	st.UpdatedAt = time.Now()

	// Update in workspace
	ws.ScheduledTasks[taskIndex] = *st

	// Update position if provided
	if req.X != nil || req.Y != nil {
		if ws.Layout == nil {
			ws.Layout = &agentstudio.CanvasLayout{}
		}
		if ws.Layout.SchedulerPositions == nil {
			ws.Layout.SchedulerPositions = make(map[string]agentstudio.Position)
		}

		pos := ws.Layout.SchedulerPositions[nodeID]
		if req.X != nil {
			pos.X = *req.X
		}
		if req.Y != nil {
			pos.Y = *req.Y
		}
		ws.Layout.SchedulerPositions[nodeID] = pos
	}

	// Save workspace
	if err := th.workspaceStore.Save(ws); err != nil {
		logger.Error("Failed to save workspace", logger.Fields{"workspace_id": err})
		httputil.RespondError(w, http.StatusInternalServerError, "Failed to save workspace", err)
		return
	}

	logger.Info("Updated scheduler node", logger.Fields{"node_id": nodeID})

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":        true,
		"node_id":        nodeID,
		"scheduled_task": st,
	})
}

// handleDeleteSchedulerNode deletes a scheduler node
func (th *TaskHandler) handleDeleteSchedulerNode(w http.ResponseWriter, r *http.Request, nodeID string) {
	workspaceID := r.URL.Query().Get("studio_id")
	if workspaceID == "" {
		http.Error(w, "studio_id parameter is required", http.StatusBadRequest)
		return
	}

	ws, err := th.workspaceStore.Get(workspaceID)
	if err != nil {
		logger.Error("Error getting workspace", logger.Fields{"workspace_id": workspaceID, "err": err})
		httputil.RespondError(w, http.StatusNotFound, "Workspace not found", err)
		return
	}

	// Find and delete scheduled task by CanvasNodeID
	found := false
	for i := range ws.ScheduledTasks {
		if ws.ScheduledTasks[i].CanvasNodeID == nodeID {
			scheduledTaskID := ws.ScheduledTasks[i].ID
			if err := ws.DeleteScheduledTask(scheduledTaskID); err != nil {
				logger.Error("Failed to delete scheduled task", logger.Fields{"scheduled_task_id": scheduledTaskID, "err": err})
				httputil.RespondError(w, http.StatusInternalServerError, "Failed to delete scheduler node", err)
				return
			}
			found = true
			break
		}
	}

	if !found {
		http.Error(w, fmt.Sprintf("Scheduler node %s not found", nodeID), http.StatusNotFound)
		return
	}

	// Remove position from layout
	if ws.Layout != nil && ws.Layout.SchedulerPositions != nil {
		delete(ws.Layout.SchedulerPositions, nodeID)
	}

	// Save workspace
	if err := th.workspaceStore.Save(ws); err != nil {
		logger.Error("Failed to save workspace", logger.Fields{"workspace_id": err})
		httputil.RespondError(w, http.StatusInternalServerError, "Failed to save workspace", err)
		return
	}

	logger.Info("Deleted scheduler node", logger.Fields{"node_id": nodeID})

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Scheduler node deleted successfully",
		"node_id": nodeID,
	})
}

// SchedulerNodeTriggerHandler handles manual triggering of a scheduler node
func (th *TaskHandler) SchedulerNodeTriggerHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract node ID from URL path
	parts := strings.Split(strings.TrimSuffix(r.URL.Path, "/"), "/")
	if len(parts) < 2 {
		http.Error(w, "Invalid URL: missing node ID", http.StatusBadRequest)
		return
	}
	nodeID := parts[len(parts)-2] // node ID is before "trigger"

	workspaceID := r.URL.Query().Get("studio_id")
	if workspaceID == "" {
		http.Error(w, "studio_id parameter is required", http.StatusBadRequest)
		return
	}

	ws, err := th.workspaceStore.Get(workspaceID)
	if err != nil {
		logger.Error("Error getting workspace", logger.Fields{"workspace_id": workspaceID, "err": err})
		httputil.RespondError(w, http.StatusNotFound, "Workspace not found", err)
		return
	}

	// Find scheduled task by CanvasNodeID
	var foundTask *agentstudio.ScheduledTask
	for i := range ws.ScheduledTasks {
		if ws.ScheduledTasks[i].CanvasNodeID == nodeID {
			foundTask = &ws.ScheduledTasks[i]
			break
		}
	}

	if foundTask == nil {
		http.Error(w, fmt.Sprintf("Scheduler node %s not found", nodeID), http.StatusNotFound)
		return
	}

	now := time.Now()
	var taskID string
	var targetTask *agentstudio.Task

	// If linked to a specific task node, reset and execute that task immediately
	if foundTask.TargetTaskID != "" {
		task, err := ws.GetTask(foundTask.TargetTaskID)
		if err != nil {
			logger.Error("Target task not found for scheduler node", logger.Fields{"node_id": nodeID, "target_task_id": foundTask.TargetTaskID, "err": err})
			httputil.RespondError(w, http.StatusBadRequest, "Linked task not found for scheduler", err)
			return
		}
		targetTask = task

		if targetTask.Status == agentstudio.TaskStatusInProgress {
			httputil.RespondError(w, http.StatusBadRequest, "Linked task is already running", fmt.Errorf("task %s in progress", targetTask.ID))
			return
		}

		if targetTask.To == "" || targetTask.To == "unassigned" {
			httputil.RespondError(w, http.StatusBadRequest, "Linked task must be assigned to an agent", fmt.Errorf("task %s unassigned", targetTask.ID))
			return
		}

		// Reset task state for rerun
		targetTask.Status = agentstudio.TaskStatusInProgress
		targetTask.Result = ""
		targetTask.Error = ""
		targetTask.Progress = nil
		targetTask.StartedAt = &now
		targetTask.CompletedAt = nil

		if err := ws.UpdateTask(*targetTask); err != nil {
			logger.Error("Failed to reset task for immediate execution", logger.Fields{"node_id": nodeID, "task_id": targetTask.ID, "err": err})
			httputil.RespondError(w, http.StatusInternalServerError, "Failed to reset task for execution", err)
			return
		}

		taskID = targetTask.ID
	} else {
		httputil.RespondError(w, http.StatusBadRequest, "Scheduler node is not linked to a task. Connect it to a task node first.", fmt.Errorf("missing target_task_id"))
		return
	}

	// Update scheduler bookkeeping
	foundTask.LastRun = &now
	foundTask.ExecutionCount++
	foundTask.FailureCount = 0
	foundTask.LastError = ""

	execution := agentstudio.TaskExecution{
		TaskID:     taskID,
		ExecutedAt: now,
		Status:     "success",
	}
	foundTask.ExecutionHistory = append(foundTask.ExecutionHistory, execution)
	if len(foundTask.ExecutionHistory) > 20 {
		foundTask.ExecutionHistory = foundTask.ExecutionHistory[len(foundTask.ExecutionHistory)-20:]
	}

	nextRun := agentstudio.CalculateNextRun(foundTask.Schedule, now)
	foundTask.NextRun = nextRun
	if nextRun == nil {
		foundTask.Enabled = false
	}

	if err := ws.UpdateScheduledTask(*foundTask); err != nil {
		logger.Error("Failed to update scheduler node", logger.Fields{"node_id": nodeID, "err": err})
		httputil.RespondError(w, http.StatusInternalServerError, "Failed to update scheduler node", err)
		return
	}

	if err := th.workspaceStore.Save(ws); err != nil {
		logger.Error("Failed to save workspace", logger.Fields{"workspace_id": err})
		httputil.RespondError(w, http.StatusInternalServerError, "Failed to save workspace", err)
		return
	}

	// Execute task immediately in background
	go func() {
		ctx := context.Background()
		logger.Info("Executing scheduler-triggered task", logger.Fields{"task_id": taskID, "agent": targetTask.To})

		result, execErr := th.taskHandler.ExecuteTask(ctx, targetTask.To, *targetTask)
		if execErr != nil {
			logger.Error("Task execution failed", logger.Fields{"task_id": taskID, "err": execErr})

			// Update task with error
			ws, wsErr := th.workspaceStore.Get(workspaceID)
			if wsErr == nil {
				if task, getErr := ws.GetTask(taskID); getErr == nil {
					task.Status = agentstudio.TaskStatusFailed
					task.Error = execErr.Error()
					completedAt := time.Now()
					task.CompletedAt = &completedAt
					_ = ws.UpdateTask(*task)
					_ = th.workspaceStore.Save(ws)
				}
			}
		} else {
			logger.Info("Task execution completed", logger.Fields{"task_id": taskID, "result_length": len(result)})

			// Update task with result
			ws, wsErr := th.workspaceStore.Get(workspaceID)
			if wsErr == nil {
				if task, getErr := ws.GetTask(taskID); getErr == nil {
					task.Status = agentstudio.TaskStatusCompleted
					task.Result = result
					completedAt := time.Now()
					task.CompletedAt = &completedAt
					_ = ws.UpdateTask(*task)
					_ = th.workspaceStore.Save(ws)
				}
			}
		}
	}()

	if th.eventBus != nil {
		payload := map[string]interface{}{
			"task_id":         taskID,
			"task_created":    foundTask.TargetTaskID == "",
			"execution_count": foundTask.ExecutionCount,
			"next_run":        nextRun,
			"timestamp":       now,
			"scheduled_task":  foundTask,
			"target_task_id":  foundTask.TargetTaskID,
		}
		th.eventBus.Publish(agentstudio.NewScheduledTaskEvent(agentstudio.EventScheduledTaskTriggered, ws.ID, foundTask.ID, foundTask.Name, payload))
	}

	logger.Info("Manually triggered scheduler node", logger.Fields{"node_id": nodeID, "task_id": taskID, "target_task_id": foundTask.TargetTaskID})

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"task_id": taskID,
		"message": "Scheduler node triggered successfully - task execution started",
	})
}

// validateScheduleConfig validates a schedule configuration
func validateScheduleConfig(config agentstudio.ScheduleConfig) error {
	switch config.Type {
	case agentstudio.ScheduleOnce:
		if config.ExecuteAt == nil {
			return fmt.Errorf("execute_at is required for 'once' schedule type")
		}
		if config.ExecuteAt.Before(time.Now()) {
			return fmt.Errorf("execute_at must be in the future")
		}

	case agentstudio.ScheduleInterval:
		if config.Interval <= 0 {
			return fmt.Errorf("interval must be positive for 'interval' schedule type")
		}

	case agentstudio.ScheduleDaily:
		if config.TimeOfDay == "" {
			return fmt.Errorf("time_of_day is required for 'daily' schedule type")
		}
		if _, _, err := parseScheduleTime(config.TimeOfDay); err != nil {
			return fmt.Errorf("invalid time_of_day format: %w", err)
		}

	case agentstudio.ScheduleWeekly:
		if config.TimeOfDay == "" {
			return fmt.Errorf("time_of_day is required for 'weekly' schedule type")
		}
		if _, _, err := parseScheduleTime(config.TimeOfDay); err != nil {
			return fmt.Errorf("invalid time_of_day format: %w", err)
		}
		if config.DayOfWeek < 0 || config.DayOfWeek > 6 {
			return fmt.Errorf("day_of_week must be between 0 (Sunday) and 6 (Saturday)")
		}

	case agentstudio.ScheduleCron:
		if config.CronExpr == "" {
			return fmt.Errorf("cron_expr is required for 'cron' schedule type")
		}
		if err := agentstudio.ValidateCronExpression(config.CronExpr); err != nil {
			return fmt.Errorf("invalid cron expression: %w", err)
		}

	case agentstudio.ScheduleRelativeDelay:
		if config.DelayDuration <= 0 {
			return fmt.Errorf("delay_duration must be positive for 'relative_delay' schedule type")
		}

	default:
		return fmt.Errorf("unknown schedule type: %s", config.Type)
	}

	return nil
}

// generateNodeID generates a unique node ID
func generateNodeID() string {
	return time.Now().Format("20060102150405") + "-" + fmt.Sprintf("%d", time.Now().UnixNano()%10000)
}
