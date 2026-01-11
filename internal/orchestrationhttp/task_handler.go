package orchestrationhttp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/agentcomm"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// FrontendScheduleConfig mirrors what the frontend sends
// and allows conversion to workspace.ScheduleConfig
type FrontendScheduleConfig struct {
	Type            string     `json:"type"`
	IntervalMinutes int        `json:"interval_minutes,omitempty"` // Frontend sends interval in minutes
	Time            string     `json:"time,omitempty"`             // Frontend sends "time" for daily/weekly
	TimeOfDay       string     `json:"time_of_day,omitempty"`      // Alternate field name
	DayOfWeek       int        `json:"day_of_week,omitempty"`
	RunAt           *time.Time `json:"run_at,omitempty"`     // Frontend sends "run_at" for once
	ExecuteAt       *time.Time `json:"execute_at,omitempty"` // Alternate field name
	CronExpr        string     `json:"cron_expr,omitempty"`
	MaxRuns         int        `json:"max_runs,omitempty"`
	EndDate         *time.Time `json:"end_date,omitempty"`
}

// convertScheduleConfig converts frontend schedule format to backend format
func convertScheduleConfig(raw json.RawMessage) *workspace.ScheduleConfig {
	if raw == nil {
		return nil
	}

	var frontend FrontendScheduleConfig
	if err := json.Unmarshal(raw, &frontend); err != nil {
		logger.Warn("Failed to parse schedule config", logger.Fields{"err": err})
		return nil
	}

	config := &workspace.ScheduleConfig{
		Type:     workspace.ScheduleType(frontend.Type),
		MaxRuns:  frontend.MaxRuns,
		EndDate:  frontend.EndDate,
		CronExpr: frontend.CronExpr,
	}

	// Handle interval conversion (minutes to time.Duration)
	if frontend.IntervalMinutes > 0 {
		config.Interval = time.Duration(frontend.IntervalMinutes) * time.Minute
		logger.Debug("Converted interval_minutes to Duration", logger.Fields{
			"interval_minutes": frontend.IntervalMinutes,
			"interval":         config.Interval,
		})
	}

	// Handle time_of_day (frontend sends "time" or "time_of_day")
	if frontend.Time != "" {
		config.TimeOfDay = frontend.Time
	} else if frontend.TimeOfDay != "" {
		config.TimeOfDay = frontend.TimeOfDay
	}

	// Handle day_of_week
	config.DayOfWeek = frontend.DayOfWeek

	// Handle execute_at (frontend sends "run_at" or "execute_at")
	if frontend.RunAt != nil {
		config.ExecuteAt = frontend.RunAt
	} else if frontend.ExecuteAt != nil {
		config.ExecuteAt = frontend.ExecuteAt
	}

	logger.Debug("Converted frontend schedule to backend format", logger.Fields{
		"type":        config.Type,
		"interval":    config.Interval,
		"time_of_day": config.TimeOfDay,
		"day_of_week": config.DayOfWeek,
	})

	return config
}

// TaskHandler manages task and scheduled task operations
type TaskHandler struct {
	workspaceStore workspace.Store
	communicator   *agentcomm.Communicator
	taskHandler    workspace.TaskHandler
	eventBus       *workspace.EventBus
}

// NewTaskHandler creates a new task handler
func NewTaskHandler(workspaceStore workspace.Store,
	communicator *agentcomm.Communicator,
	taskHandler workspace.TaskHandler,
	eventBus *workspace.EventBus) *TaskHandler {
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
			orihttp.NotFound(w, err.Error())
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
		WorkspaceID            string                         `json:"studio_id"`
		From                   string                         `json:"from"`
		To                     string                         `json:"to"`
		AssignedNodeID         string                         `json:"assigned_node_id"`
		Description            string                         `json:"description"`
		Details                string                         `json:"details"`
		Priority               int                            `json:"priority"`
		InputTaskIDs           []string                       `json:"input_task_ids"`
		ResultCombinationMode  string                         `json:"result_combination_mode"`
		CombinationInstruction string                         `json:"combination_instruction"`
		Schedule               json.RawMessage                `json:"schedule"`
		ScheduleEnabled        bool                           `json:"schedule_enabled"`
		ScheduleName           string                         `json:"schedule_name"`
		ResultStorage          *workspace.ResultStorageConfig `json:"result_storage"`
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	// Convert frontend schedule format to backend format
	var schedule *workspace.ScheduleConfig
	if len(req.Schedule) > 0 {
		schedule = convertScheduleConfig(req.Schedule)
	}

	// Validate required fields
	if req.WorkspaceID == "" {
		orihttp.BadRequest(w, "workspace_id is required")
		return
	}
	// from and to are optional - tasks without agents are manual tasks
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
	task := workspace.Task{
		WorkspaceID:     req.WorkspaceID,
		From:            req.From,
		To:              req.To,
		AssignedNodeID:  req.AssignedNodeID,
		Description:     req.Description,
		Details:         req.Details,
		Priority:        req.Priority,
		InputTaskIDs:    req.InputTaskIDs,
		Status:          workspace.TaskStatusPending,
		Schedule:        schedule,
		ScheduleEnabled: req.ScheduleEnabled,
		ScheduleName:    req.ScheduleName,
		ResultStorage:   req.ResultStorage,
	}

	// Validate: scheduled tasks must be assigned to an agent
	if task.ScheduleEnabled && task.Schedule != nil {
		if task.To == "" || task.To == "unassigned" {
			orihttp.BadRequest(w, "Scheduled tasks must be assigned to an agent. Please assign an agent before enabling the schedule.")
			return
		}
		// Calculate NextRun
		nextRun := workspace.CalculateNextRun(*task.Schedule, time.Now())
		task.NextRun = nextRun
	}

	// Auto-add agent to workspace if not already present
	if task.To != "" && task.To != "unassigned" && !ws.HasAgent(task.To) {
		if err := ws.AddAgent(task.To); err != nil {
			logger.Warn("Failed to auto-add agent to workspace", logger.Fields{"agent": task.To, "error": err})
		} else {
			logger.Info("Auto-added agent to workspace", logger.Fields{"agent": task.To, "workspace_id": ws.ID})
		}
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
	var createdTask *workspace.Task
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
		TaskID                 string                         `json:"task_id"`
		Status                 string                         `json:"status"`
		Result                 string                         `json:"result"`
		Error                  string                         `json:"error"`
		Description            *string                        `json:"description"`             // Optional: update task description
		Details                *string                        `json:"details"`                 // Optional: update task details
		To                     *string                        `json:"to"`                      // Optional: reassign task to different agent
		AssignedNodeID         *string                        `json:"assigned_node_id"`        // Optional: target specific agent instance/node
		InputTaskIDs           []string                       `json:"input_task_ids"`          // Optional: update input task connections
		ResultCombinationMode  *string                        `json:"result_combination_mode"` // Optional: update combination mode
		CombinationInstruction *string                        `json:"combination_instruction"` // Optional: update combination instruction
		Schedule               json.RawMessage                `json:"schedule"`                // Optional: schedule configuration (frontend format)
		ScheduleEnabled        *bool                          `json:"schedule_enabled"`        // Optional: enable/disable schedule
		ScheduleName           *string                        `json:"schedule_name"`           // Optional: schedule name
		ResultStorage          *workspace.ResultStorageConfig `json:"result_storage"`          // Optional: auto-save configuration
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	// Convert frontend schedule format to backend format
	var schedule *workspace.ScheduleConfig
	if len(req.Schedule) > 0 {
		schedule = convertScheduleConfig(req.Schedule)
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

	// Handle task updates (description, details, input connections, reassignment, combination mode, schedule, or result storage)
	if req.Description != nil || req.Details != nil || req.InputTaskIDs != nil || req.To != nil || req.ResultCombinationMode != nil || schedule != nil || req.ScheduleEnabled != nil || req.ScheduleName != nil || req.ResultStorage != nil {
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

				// Update description
				if req.Description != nil {
					ws.Tasks[i].Description = *req.Description
					logger.Debug("Updated task description", logger.Fields{"task_id": req.TaskID})
				}

				// Update details
				if req.Details != nil {
					ws.Tasks[i].Details = *req.Details
					logger.Debug("Updated task details", logger.Fields{"task_id": req.TaskID})
				}

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
						orihttp.InternalError(w, err.Error())
						return
					}
				}

				// Update schedule configuration
				if schedule != nil {
					ws.Tasks[i].Schedule = schedule
					// Calculate initial NextRun if schedule is being set
					if ws.Tasks[i].ScheduleEnabled {
						ws.Tasks[i].NextRun = workspace.CalculateNextRun(*schedule, time.Now())
					}
					logger.Debug("Updated task schedule", logger.Fields{"task_id": req.TaskID})
				}

				// Update schedule enabled state
				if req.ScheduleEnabled != nil {
					// Validate: can't enable schedule if task is unassigned
					if *req.ScheduleEnabled {
						taskTo := ws.Tasks[i].To
						// Check if we're also updating the assignment in this request
						if req.To != nil {
							taskTo = *req.To
						}
						if taskTo == "" || taskTo == "unassigned" {
							orihttp.BadRequest(w, "Scheduled tasks must be assigned to an agent. Please assign an agent before enabling the schedule.")
							return
						}
					}

					ws.Tasks[i].ScheduleEnabled = *req.ScheduleEnabled
					// Calculate NextRun when enabling, clear when disabling
					if *req.ScheduleEnabled && ws.Tasks[i].Schedule != nil {
						ws.Tasks[i].NextRun = workspace.CalculateNextRun(*ws.Tasks[i].Schedule, time.Now())
					} else if !*req.ScheduleEnabled {
						ws.Tasks[i].NextRun = nil
					}
					logger.Debug("Updated task schedule enabled", logger.Fields{"task_id": req.TaskID, "enabled": *req.ScheduleEnabled})
				}

				// Update schedule name
				if req.ScheduleName != nil {
					ws.Tasks[i].ScheduleName = *req.ScheduleName
				}

				// Update result storage configuration
				if req.ResultStorage != nil {
					ws.Tasks[i].ResultStorage = req.ResultStorage
					logger.Debug("Updated task result storage", logger.Fields{"task_id": req.TaskID, "enabled": req.ResultStorage.Enabled})
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
			if schedule != nil {
				eventData["schedule"] = schedule
			}
			if req.ScheduleEnabled != nil {
				eventData["schedule_enabled"] = *req.ScheduleEnabled
			}
			if req.ScheduleName != nil {
				eventData["schedule_name"] = *req.ScheduleName
			}

			th.eventBus.Publish(workspace.Event{
				Type:        workspace.EventWorkspaceUpdated,
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
		th.eventBus.Publish(workspace.Event{
			Type:        workspace.EventTaskAssigned,
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
		workspace.TaskStatus(req.Status),
		req.Result,
		req.Error,
	)

	if err != nil {
		logger.Error("Failed to update task status", logger.Fields{"task_id": err})
		orihttp.BadRequest(w, err.Error())
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
		orihttp.NotFound(w, err.Error())
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

// TasksPathHandler handles requests to /api/orchestration/tasks/{id}...
// Routes to appropriate handler based on path and method:
// - PUT /api/orchestration/tasks/{id} -> handleUpdateTask
// - GET /api/orchestration/tasks/{id} -> handleGetTasks (single task)
// - DELETE /api/orchestration/tasks/{id} -> handleDeleteTask
// - POST /api/orchestration/tasks/{id}/complete -> CompleteTaskHandler
// - POST /api/orchestration/tasks/{id}/save-result -> SaveTaskResult (via workspace handler)
func (th *TaskHandler) TasksPathHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Extract path after /api/orchestration/tasks/
	path := strings.TrimPrefix(r.URL.Path, "/api/orchestration/tasks/")

	// Check if this is a /complete endpoint
	if strings.HasSuffix(path, "/complete") {
		th.handleCompleteTask(w, r)
		return
	}

	// Check if this is a /save-result endpoint
	if strings.HasSuffix(path, "/save-result") {
		th.handleSaveTaskResult(w, r)
		return
	}

	// Route based on method
	switch r.Method {
	case http.MethodGet:
		th.handleGetTasks(w, r)
	case http.MethodPut, http.MethodPatch:
		th.handleUpdateTask(w, r)
	case http.MethodDelete:
		th.handleDeleteTask(w, r)
	default:
		orihttp.MethodNotAllowed(w)
	}
}

// handleCompleteTask handles POST /api/orchestration/tasks/{id}/complete
// Marks a task as completed (for manual task completion)
func (th *TaskHandler) handleCompleteTask(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	// Extract task ID from URL path
	pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/orchestration/tasks/"), "/")
	if len(pathParts) < 2 || pathParts[0] == "" {
		orihttp.BadRequest(w, "task_id is required in URL path")
		return
	}
	taskID := pathParts[0]

	// Get task and workspace
	task, ws, err := th.getTaskWithWorkspace(taskID)
	if err != nil {
		orihttp.RespondErrorWithErr(w, http.StatusNotFound, "Task not found", err)
		return
	}

	// Find and update task status to completed
	for i := range ws.Tasks {
		if ws.Tasks[i].ID == taskID {
			now := time.Now()
			ws.Tasks[i].Status = workspace.TaskStatusCompleted
			ws.Tasks[i].CompletedAt = &now
			break
		}
	}

	// Save workspace
	if err := th.workspaceStore.Save(ws); err != nil {
		logger.Error("Failed to save workspace", logger.Fields{"error": err})
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to complete task", err)
		return
	}

	logger.Info("Completed task manually", logger.Fields{"task_id": taskID, "workspace_id": task.WorkspaceID})

	// Publish event
	if th.eventBus != nil {
		th.eventBus.Publish(workspace.Event{
			Type:        workspace.EventTaskCompleted,
			WorkspaceID: task.WorkspaceID,
			Data: map[string]interface{}{
				"task_id": taskID,
				"manual":  true,
			},
		})
	}

	// Return updated task
	updatedTask, _ := ws.GetTask(taskID)
	w.WriteHeader(http.StatusOK)
	orihttp.WriteJSON(w, map[string]interface{}{
		"success": true,
		"task":    updatedTask,
	})
}

// SaveTaskResultRequest represents the request to save a task result
type SaveTaskResultRequest struct {
	TaskID      string `json:"task_id"`
	StoreNodeID string `json:"store_node_id,omitempty"` // Optional: save to specific store node
	FilePath    string `json:"file_path"`               // Required: relative file path within store or absolute path for direct save
	Format      string `json:"format,omitempty"`        // Optional: json, text, markdown (default: text)
}

// handleSaveTaskResult handles POST /api/orchestration/tasks/{id}/save-result
func (th *TaskHandler) handleSaveTaskResult(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	var req SaveTaskResultRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		orihttp.BadRequest(w, "Invalid request body")
		return
	}

	// Extract task ID from URL if not in body
	if req.TaskID == "" {
		pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/orchestration/tasks/"), "/")
		if len(pathParts) >= 1 && pathParts[0] != "" {
			req.TaskID = pathParts[0]
		}
	}

	if req.TaskID == "" {
		orihttp.BadRequest(w, "Task ID is required")
		return
	}
	if req.FilePath == "" {
		orihttp.BadRequest(w, "File path is required")
		return
	}

	// Set default format
	if req.Format == "" {
		req.Format = "text"
	}

	// Validate format
	validFormats := map[string]bool{"json": true, "text": true, "markdown": true}
	if !validFormats[req.Format] {
		orihttp.BadRequest(w, "Format must be one of: json, text, markdown")
		return
	}

	// Find the task
	task, ws, err := th.getTaskWithWorkspace(req.TaskID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Task not found: %v", err))
		return
	}

	if task.Result == "" {
		orihttp.BadRequest(w, "Task has no result to save")
		return
	}

	var finalPath string

	if req.StoreNodeID != "" {
		// Save via store node
		var storeNode *workspace.StoreNode
		for i := range ws.StoreNodes {
			if ws.StoreNodes[i].ID == req.StoreNodeID || ws.StoreNodes[i].CanvasNodeID == req.StoreNodeID {
				storeNode = &ws.StoreNodes[i]
				break
			}
		}

		if storeNode == nil {
			orihttp.NotFound(w, "Store node not found")
			return
		}

		// Override format with store node's format
		storeNode.Format = req.Format

		// Write to store
		if err := workspace.WriteToStore(storeNode, req.FilePath, task.Result); err != nil {
			orihttp.InternalError(w, fmt.Sprintf("Failed to save result: %v", err))
			return
		}

		finalPath = filepath.Join(storeNode.BaseDir, req.FilePath)

		// Save workspace to persist store node stats
		if err := th.workspaceStore.Save(ws); err != nil {
			logger.Warn("Failed to save workspace after store write", logger.Fields{"error": err})
		}
	} else {
		// Direct file save (for Quick Save or custom path)
		// Format data based on format type
		var formattedData []byte
		switch req.Format {
		case "json":
			// Pretty-print JSON
			var obj interface{}
			if err := json.Unmarshal([]byte(task.Result), &obj); err != nil {
				// If not valid JSON, treat as plain text
				formattedData = []byte(task.Result)
			} else {
				formattedData, _ = json.MarshalIndent(obj, "", "  ")
			}
		default:
			formattedData = []byte(task.Result)
		}

		// Create directories
		dir := filepath.Dir(req.FilePath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			orihttp.InternalError(w, fmt.Sprintf("Failed to create directories: %v", err))
			return
		}

		// Write file
		if err := os.WriteFile(req.FilePath, formattedData, 0644); err != nil {
			orihttp.InternalError(w, fmt.Sprintf("Failed to write file: %v", err))
			return
		}

		finalPath = req.FilePath
	}

	logger.Info("Saved task result", logger.Fields{
		"task_id":   req.TaskID,
		"file_path": finalPath,
		"format":    req.Format,
	})

	w.WriteHeader(http.StatusOK)
	orihttp.WriteJSON(w, map[string]interface{}{
		"success":   true,
		"message":   "Result saved successfully",
		"file_path": finalPath,
		"task_id":   req.TaskID,
	})
}
