package orchestrationhttp

import (
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/agentcomm"
	"github.com/johnjallday/ori-agent/internal/agentstudio"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
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
		orihttp.MethodNotAllowed(w)
	}
}

func (th *TaskHandler) handleGetTasks(w http.ResponseWriter, r *http.Request) {
	taskID := r.URL.Query().Get("id")
	workspaceID := r.URL.Query().Get("studio_id")
	agentName := r.URL.Query().Get("agent")

	if taskID != "" {
		// Get specific task
		task, err := th.communicator.GetTask(taskID)
		if err != nil {
			if respErr := orihttp.RespondNotFound(w, err.Error()); respErr != nil {
				logger.Error("Failed to write response", logger.Fields{"error": respErr})
			}
			return
		}
		orihttp.WriteJSON(w, task)
		return
	}

	if workspaceID != "" {
		// List tasks for workspace
		tasks := th.communicator.ListTasks(workspaceID)
		stats := th.communicator.GetTaskStats(workspaceID)

		orihttp.WriteJSON(w, map[string]interface{}{
			"tasks": tasks,
			"stats": stats,
			"count": len(tasks),
		})
		return
	}

	if agentName != "" {
		// List tasks for agent
		tasks := th.communicator.ListTasksForAgent(agentName)
		orihttp.WriteJSON(w, map[string]interface{}{
			"tasks": tasks,
			"count": len(tasks),
		})
		return
	}

	orihttp.BadRequest(w, "id, workspace_id, or agent parameter required")
}

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

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	// Validate required fields
	if req.WorkspaceID == "" {
		orihttp.BadRequest(w, "workspace_id is required")
		return
	}
	if req.From == "" {
		if respErr := orihttp.RespondBadRequest(w, "from (sender agent) is required"); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}
	if req.To == "" {
		if respErr := orihttp.RespondBadRequest(w, "to (recipient agent) is required"); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}
	if req.Description == "" {
		orihttp.BadRequest(w, "description is required")
		return
	}

	ws, err := th.workspaceStore.Get(req.WorkspaceID)
	if err != nil {
		logger.Error("Error getting workspace", logger.Fields{"error": err, "workspace_id": req.WorkspaceID})
		orihttp.RespondErrorWithErr(w, http.StatusNotFound, "Workspace not found", err)
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
		logger.Error("Failed to add task to workspace", logger.Fields{"error": err})
		orihttp.RespondErrorWithErr(w, http.StatusBadRequest, "Failed to add task", err)
		return
	}

	// Save workspace
	if err := th.workspaceStore.Save(ws); err != nil {
		logger.Error("Failed to save workspace", logger.Fields{"error": err})
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to save workspace", err)
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
		orihttp.InternalError(w, "Task created but could not be retrieved")
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
	orihttp.WriteJSON(w, map[string]interface{}{
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

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	// Extract task ID from URL path if present (e.g., /api/orchestration/tasks/{id})
	pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/orchestration/tasks/"), "/")
	if len(pathParts) > 0 && pathParts[0] != "" {
		req.TaskID = pathParts[0]
	}

	if req.TaskID == "" {
		orihttp.ValidationError(w, "task_id is required", nil)
		return
	}

	// Handle task updates (input connections, reassignment, or combination mode)
	if req.InputTaskIDs != nil || req.To != nil || req.ResultCombinationMode != nil {
		logger.Debug("Updating task", logger.Fields{"task_id": req.TaskID})

		// Get task and workspace using helper
		task, ws, err := th.getTaskWithWorkspace(req.TaskID)
		if err != nil {
			logger.Error("", logger.Fields{"err": err})
			orihttp.RespondErrorWithErr(w, http.StatusNotFound, "Failed to retrieve task or workspace", err)
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
					logger.Debug("Updated task input connections", logger.Fields{"task_id": req.TaskID, "inputtaskids": req.InputTaskIDs})
				}

				// Update assignment using helper
				if req.To != nil {
					if req.AssignedNodeID != nil {
						logger.Debug("Reassigning task to (node)", logger.Fields{"task_id": req.TaskID, "to": *req.To, "assignednodeid": *req.AssignedNodeID})
					} else {
						logger.Debug("Reassigning task to (no node id)", logger.Fields{"task_id": req.TaskID, "to": *req.To})
					}
					_, err = th.updateTaskAssignment(ws, req.TaskID, req.To, req.AssignedNodeID)
					if err != nil {
						logger.Error("", logger.Fields{"err": err})
						if respErr := orihttp.RespondInternalError(w, err.Error()); respErr != nil {
							logger.Error("Failed to write response", logger.Fields{"error": respErr})
						}
						return
					}
				}
				break
			}
		}

		if taskIndex == -1 {
			logger.Error("Task not found in workspace", logger.Fields{"task_id": req.TaskID, "workspaceid": task.WorkspaceID})
			orihttp.NotFound(w, "Task not found in workspace")
			return
		}

		// Save workspace
		if err := th.workspaceStore.Save(ws); err != nil {
			logger.Error("Failed to save workspace", logger.Fields{"error": err})
			orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to update task", err)
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
		updatedTask, err := th.communicator.GetTask(req.TaskID)
		if err != nil {
			logger.Error("Failed to get updated task", logger.Fields{"task_id": req.TaskID, "error": err})
			// Still return success since the update was performed, but log the retrieval error
		}
		w.WriteHeader(http.StatusOK)
		orihttp.WriteJSON(w, updatedTask)
		return
	}

	// Legacy: Handle task reassignment alone (for backwards compatibility)
	if req.To != nil {
		logger.Debug("Reassigning task to", logger.Fields{"task_id": req.TaskID, "to": *req.To})

		// Get task and workspace using helper
		task, ws, err := th.getTaskWithWorkspace(req.TaskID)
		if err != nil {
			logger.Error("", logger.Fields{"err": err})
			orihttp.RespondErrorWithErr(w, http.StatusNotFound, "Failed to retrieve task or workspace", err)
			return
		}

		// Update task assignment using helper
		_, err = th.updateTaskAssignment(ws, req.TaskID, req.To, req.AssignedNodeID)
		if err != nil {
			logger.Error("", logger.Fields{"err": err})
			orihttp.NotFound(w, "Task not found in workspace")
			return
		}
		logger.Debug("Updated task in workspace", logger.Fields{"task_id": req.TaskID, "to": *req.To})

		// Save workspace
		if err := th.workspaceStore.Save(ws); err != nil {
			logger.Error("Failed to save workspace", logger.Fields{"error": err})
			orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to update task", err)
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
		updatedTask, err := th.communicator.GetTask(req.TaskID)
		if err != nil {
			logger.Error("Failed to get updated task", logger.Fields{"task_id": req.TaskID, "error": err})
			// Still return success since the reassignment was performed, but log the retrieval error
		}
		w.WriteHeader(http.StatusOK)
		orihttp.WriteJSON(w, updatedTask)
		return
	}

	// Handle status update
	if req.Status == "" {
		orihttp.BadRequest(w, "status is required when not reassigning task")
		return
	}

	err := th.communicator.UpdateTaskStatus(
		req.TaskID,
		agentstudio.TaskStatus(req.Status),
		req.Result,
		req.Error,
	)

	if err != nil {
		logger.Error("Failed to update task status", logger.Fields{"task_id": err})
		if respErr := orihttp.RespondBadRequest(w, err.Error()); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}

	w.WriteHeader(http.StatusOK)
	orihttp.WriteJSON(w, map[string]interface{}{
		"success": true,
		"task_id": req.TaskID,
		"status":  req.Status,
	})
}

// handleDeleteTask deletes a task
func (th *TaskHandler) handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	taskID := r.URL.Query().Get("id")
	if taskID == "" {
		orihttp.BadRequest(w, "id parameter required")
		return
	}

	workspaceID := r.URL.Query().Get("workspace_id")
	if workspaceID == "" {
		workspaceID = r.URL.Query().Get("studio_id")
	}

	if workspaceID != "" {
		ws, err := th.workspaceStore.Get(workspaceID)
		if err != nil {
			orihttp.RespondErrorWithErr(w, http.StatusNotFound, "Workspace not found", err)
			return
		}

		if err := ws.DeleteTask(taskID); err != nil {
			orihttp.RespondErrorWithErr(w, http.StatusNotFound, "Task not found", err)
			return
		}

		if err := th.workspaceStore.Save(ws); err != nil {
			logger.Error("Failed to save workspace", logger.Fields{"error": err})
			orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to save workspace", err)
			return
		}

		logger.Info("Deleted task", logger.Fields{"task_id": taskID, "workspace_id": workspaceID})
		w.WriteHeader(http.StatusOK)
		orihttp.WriteJSON(w, map[string]interface{}{
			"success": true,
			"message": "Task deleted successfully",
			"task_id": taskID,
		})
		return
	}

	// Fallback: search all workspaces
	if err := th.communicator.DeleteTask(taskID); err != nil {
		logger.Error("Failed to delete task", logger.Fields{"task_id": err})
		if respErr := orihttp.RespondNotFound(w, err.Error()); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}

	logger.Info("Deleted task", logger.Fields{"task_id": taskID})
	w.WriteHeader(http.StatusOK)
	orihttp.WriteJSON(w, map[string]interface{}{
		"success": true,
		"message": "Task deleted successfully",
		"task_id": taskID,
	})
}
