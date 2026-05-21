package orchestrationhttp

import (
	"context"

	"fmt"
	"net/http"
	"strings"
	"time"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

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
		orihttp.MethodNotAllowed(w)
	}
}

// handleListSchedulerNodes lists all scheduler nodes (scheduled tasks) for a workspace
// This includes both canvas-created schedulers (with CanvasNodeID) and dashboard-created schedulers (without CanvasNodeID)
// Dashboard-created schedulers are automatically assigned a canvas_node_id when loaded
func (th *TaskHandler) handleListSchedulerNodes(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.URL.Query().Get("workspace_id")
	if workspaceID == "" {
		orihttp.BadRequest(w, "workspace_id parameter is required")
		return
	}

	ws, err := th.workspaceStore.Get(workspaceID)
	if err != nil {
		logger.Error("Error getting workspace", logger.Fields{"workspace_id": workspaceID, "err": err})
		orihttp.RespondErrorWithErr(w, http.StatusNotFound, "Workspace not found", err)
		return
	}

	// Return ALL scheduled tasks as scheduler nodes (both canvas and dashboard-created)
	// Auto-assign canvas_node_id to dashboard-created schedulers for display
	schedulerNodes := make([]map[string]any, 0)
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
		var position *workspace.Position
		if ws.Layout != nil && ws.Layout.SchedulerPositions != nil {
			if pos, exists := ws.Layout.SchedulerPositions[st.CanvasNodeID]; exists {
				position = &pos
			}
		}

		// If no position exists, assign a default position (centered, with offset per scheduler)
		if position == nil {
			defaultX := 100.0 + float64(i*150) // Offset horizontally for each scheduler
			defaultY := 100.0
			position = &workspace.Position{X: defaultX, Y: defaultY}

			// Save position to layout
			if ws.Layout == nil {
				ws.Layout = &workspace.CanvasLayout{
					SchedulerPositions: make(map[string]workspace.Position),
				}
			}
			if ws.Layout.SchedulerPositions == nil {
				ws.Layout.SchedulerPositions = make(map[string]workspace.Position)
			}
			ws.Layout.SchedulerPositions[st.CanvasNodeID] = *position
			needsSave = true
		}

		node := map[string]any{
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

	orihttp.WriteJSON(w, map[string]any{
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
		workspaceID = r.URL.Query().Get("workspace_id")
	}

	if workspaceID == "" {
		orihttp.BadRequest(w, "workspace_id is required in URL path")
		return
	}

	var req struct {
		Name        string                   `json:"name"`
		Description string                   `json:"description"`
		From        string                   `json:"from"`
		To          string                   `json:"to"`
		Prompt      string                   `json:"prompt"`
		Priority    int                      `json:"priority"`
		Schedule    workspace.ScheduleConfig `json:"schedule"`
		Enabled     bool                     `json:"enabled"`
		X           float64                  `json:"x"`
		Y           float64                  `json:"y"`
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	// Validate required fields
	if req.Name == "" {
		orihttp.BadRequest(w, "name is required")
		return
	}

	// Defaults for scheduler nodes
	if req.From == "" {
		req.From = "scheduler"
	}

	// Validate schedule configuration
	if err := validateScheduleConfig(req.Schedule); err != nil {
		orihttp.RespondErrorWithErr(w, http.StatusBadRequest, "Invalid schedule configuration", err)
		return
	}

	// Get workspace
	ws, err := th.workspaceStore.Get(workspaceID)
	if err != nil {
		logger.Error("Error getting workspace", logger.Fields{"workspace_id": workspaceID, "err": err})
		orihttp.RespondErrorWithErr(w, http.StatusNotFound, "Workspace not found", err)
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
		orihttp.BadRequest(w, "Maximum of 50 scheduler nodes per workspace reached")
		return
	}

	// Generate unique CanvasNodeID
	nodeID := "scheduler-" + generateNodeID()

	// Create scheduled task
	now := time.Now()
	st := workspace.ScheduledTask{
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
		orihttp.RespondErrorWithErr(w, http.StatusBadRequest, "Failed to add scheduler node", err)
		return
	}

	// Initialize layout if needed
	if ws.Layout == nil {
		ws.Layout = &workspace.CanvasLayout{}
	}
	if ws.Layout.SchedulerPositions == nil {
		ws.Layout.SchedulerPositions = make(map[string]workspace.Position)
	}

	// Add position to layout
	ws.Layout.SchedulerPositions[nodeID] = workspace.Position{
		X: req.X,
		Y: req.Y,
	}

	// Save workspace
	if err := th.workspaceStore.Save(ws); err != nil {
		logger.Error("Failed to save workspace", logger.Fields{"error": err})
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to save workspace", err)
		return
	}

	// Get the created scheduled task (now has ID)
	var createdTask *workspace.ScheduledTask
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
	orihttp.WriteJSON(w, map[string]any{
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
		orihttp.BadRequest(w, "Invalid URL: missing node ID")
		return
	}
	nodeID := parts[len(parts)-1]

	switch r.Method {
	case http.MethodGet:
		th.handleGetSchedulerNode(w, r, nodeID)
	case http.MethodPut, http.MethodPatch:
		th.handleUpdateSchedulerNode(w, r, nodeID)
	case http.MethodDelete:
		th.handleDeleteSchedulerNode(w, r, nodeID)
	default:
		orihttp.MethodNotAllowed(w)
	}
}

func (th *TaskHandler) handleGetSchedulerNode(w http.ResponseWriter, r *http.Request, nodeID string) {
	workspaceID := r.URL.Query().Get("workspace_id")
	if workspaceID == "" {
		orihttp.BadRequest(w, "workspace_id parameter is required")
		return
	}

	ws, err := th.workspaceStore.Get(workspaceID)
	if err != nil {
		logger.Error("Error getting workspace", logger.Fields{"workspace_id": workspaceID, "err": err})
		orihttp.RespondErrorWithErr(w, http.StatusNotFound, "Workspace not found", err)
		return
	}

	// Find scheduled task by CanvasNodeID
	var foundTask *workspace.ScheduledTask
	for i := range ws.ScheduledTasks {
		if ws.ScheduledTasks[i].CanvasNodeID == nodeID {
			foundTask = &ws.ScheduledTasks[i]
			break
		}
	}

	if foundTask == nil {
		orihttp.NotFound(w, fmt.Sprintf("Scheduler node %s not found", nodeID))
		return
	}

	// Get position from layout
	var position *workspace.Position
	if ws.Layout != nil && ws.Layout.SchedulerPositions != nil {
		if pos, exists := ws.Layout.SchedulerPositions[nodeID]; exists {
			position = &pos
		}
	}

	orihttp.WriteJSON(w, map[string]any{
		"node_id":        nodeID,
		"scheduled_task": foundTask,
		"position":       position,
	})
}

// handleUpdateSchedulerNode updates a scheduler node
func (th *TaskHandler) handleUpdateSchedulerNode(w http.ResponseWriter, r *http.Request, nodeID string) {
	var req struct {
		WorkspaceID  string                    `json:"workspace_id"`
		To           *string                   `json:"to,omitempty"`
		TargetTaskID *string                   `json:"target_task_id,omitempty"`
		Name         *string                   `json:"name,omitempty"`
		Description  *string                   `json:"description,omitempty"`
		Prompt       *string                   `json:"prompt,omitempty"`
		Priority     *int                      `json:"priority,omitempty"`
		Schedule     *workspace.ScheduleConfig `json:"schedule,omitempty"`
		Enabled      *bool                     `json:"enabled,omitempty"`
		X            *float64                  `json:"x,omitempty"`
		Y            *float64                  `json:"y,omitempty"`
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	// Get workspace_id from query parameter or request body
	workspaceID := r.URL.Query().Get("workspace_id")
	if workspaceID == "" {
		workspaceID = req.WorkspaceID
	}

	if workspaceID == "" {
		orihttp.BadRequest(w, "workspace_id is required")
		return
	}

	// Validate schedule configuration if provided
	if req.Schedule != nil {
		if err := validateScheduleConfig(*req.Schedule); err != nil {
			orihttp.RespondErrorWithErr(w, http.StatusBadRequest, "Invalid schedule configuration", err)
			return
		}
	}

	ws, err := th.workspaceStore.Get(workspaceID)
	if err != nil {
		logger.Error("Error getting workspace", logger.Fields{"workspace_id": workspaceID, "err": err})
		orihttp.RespondErrorWithErr(w, http.StatusNotFound, "Workspace not found", err)
		return
	}

	// Find scheduled task by CanvasNodeID
	var taskIndex = -1
	var st *workspace.ScheduledTask
	for i := range ws.ScheduledTasks {
		if ws.ScheduledTasks[i].CanvasNodeID == nodeID {
			taskIndex = i
			st = &ws.ScheduledTasks[i]
			break
		}
	}

	if st == nil {
		orihttp.NotFound(w, fmt.Sprintf("Scheduler node %s not found", nodeID))
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
			ws.Layout = &workspace.CanvasLayout{}
		}
		if ws.Layout.SchedulerPositions == nil {
			ws.Layout.SchedulerPositions = make(map[string]workspace.Position)
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
		logger.Error("Failed to save workspace", logger.Fields{"error": err})
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to save workspace", err)
		return
	}

	logger.Info("Updated scheduler node", logger.Fields{"node_id": nodeID})

	orihttp.WriteJSON(w, map[string]any{
		"success":        true,
		"node_id":        nodeID,
		"scheduled_task": st,
	})
}

// handleDeleteSchedulerNode deletes a scheduler node
func (th *TaskHandler) handleDeleteSchedulerNode(w http.ResponseWriter, r *http.Request, nodeID string) {
	workspaceID := r.URL.Query().Get("workspace_id")
	if workspaceID == "" {
		orihttp.BadRequest(w, "workspace_id parameter is required")
		return
	}

	ws, err := th.workspaceStore.Get(workspaceID)
	if err != nil {
		logger.Error("Error getting workspace", logger.Fields{"workspace_id": workspaceID, "err": err})
		orihttp.RespondErrorWithErr(w, http.StatusNotFound, "Workspace not found", err)
		return
	}

	// Find and delete scheduled task by CanvasNodeID
	found := false
	for i := range ws.ScheduledTasks {
		if ws.ScheduledTasks[i].CanvasNodeID == nodeID {
			scheduledTaskID := ws.ScheduledTasks[i].ID
			if err := ws.DeleteScheduledTask(scheduledTaskID); err != nil {
				logger.Error("Failed to delete scheduled task", logger.Fields{"scheduled_task_id": scheduledTaskID, "err": err})
				orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to delete scheduler node", err)
				return
			}
			found = true
			break
		}
	}

	if !found {
		orihttp.NotFound(w, fmt.Sprintf("Scheduler node %s not found", nodeID))
		return
	}

	// Remove position from layout
	if ws.Layout != nil && ws.Layout.SchedulerPositions != nil {
		delete(ws.Layout.SchedulerPositions, nodeID)
	}

	// Save workspace
	if err := th.workspaceStore.Save(ws); err != nil {
		logger.Error("Failed to save workspace", logger.Fields{"error": err})
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to save workspace", err)
		return
	}

	logger.Info("Deleted scheduler node", logger.Fields{"node_id": nodeID})

	orihttp.WriteJSON(w, map[string]any{
		"success": true,
		"message": "Scheduler node deleted successfully",
		"node_id": nodeID,
	})
}

// SchedulerNodeTriggerHandler handles manual triggering of a scheduler node
func (th *TaskHandler) SchedulerNodeTriggerHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	// Extract node ID from URL path
	parts := strings.Split(strings.TrimSuffix(r.URL.Path, "/"), "/")
	if len(parts) < 2 {
		orihttp.BadRequest(w, "Invalid URL: missing node ID")
		return
	}
	// node ID is before "trigger"
	nodeID := parts[len(parts)-2]

	workspaceID := r.URL.Query().Get("workspace_id")
	if workspaceID == "" {
		orihttp.BadRequest(w, "workspace_id parameter is required")
		return
	}

	ws, err := th.workspaceStore.Get(workspaceID)
	if err != nil {
		logger.Error("Error getting workspace", logger.Fields{"workspace_id": workspaceID, "err": err})
		orihttp.RespondErrorWithErr(w, http.StatusNotFound, "Workspace not found", err)
		return
	}

	// Find scheduled task by CanvasNodeID
	var foundTask *workspace.ScheduledTask
	for i := range ws.ScheduledTasks {
		if ws.ScheduledTasks[i].CanvasNodeID == nodeID {
			foundTask = &ws.ScheduledTasks[i]
			break
		}
	}

	if foundTask == nil {
		orihttp.NotFound(w, fmt.Sprintf("Scheduler node %s not found", nodeID))
		return
	}

	now := time.Now()
	var taskID string
	var targetTask *workspace.Task

	// If linked to a specific task node, reset and execute that task immediately
	if foundTask.TargetTaskID != "" {
		task, err := ws.GetTask(foundTask.TargetTaskID)
		if err != nil {
			logger.Error("Target task not found for scheduler node", logger.Fields{"node_id": nodeID, "target_task_id": foundTask.TargetTaskID, "err": err})
			orihttp.RespondErrorWithErr(w, http.StatusBadRequest, "Linked task not found for scheduler", err)
			return
		}
		targetTask = task

		if targetTask.Status == workspace.TaskStatusInProgress {
			orihttp.RespondErrorWithErr(w, http.StatusBadRequest, "Linked task is already running", fmt.Errorf("task %s in progress", targetTask.ID))
			return
		}

		if targetTask.To == "" || targetTask.To == "unassigned" {
			orihttp.RespondErrorWithErr(w, http.StatusBadRequest, "Linked task must be assigned to an agent", fmt.Errorf("task %s unassigned", targetTask.ID))
			return
		}

		// Reset task state for rerun (terminal/pending → InProgress is a legal
		// rerun shortcut in the transition table).
		if err := targetTask.SetStatus(workspace.TaskStatusInProgress); err != nil {
			orihttp.RespondErrorWithErr(w, http.StatusConflict, fmt.Sprintf("cannot rerun task in state %q", targetTask.Status), err)
			return
		}
		targetTask.Result = ""
		workspace.ApplyTaskResultMetadata(targetTask, "")
		targetTask.Error = ""
		targetTask.Progress = nil
		targetTask.StartedAt = &now
		targetTask.CompletedAt = nil

		if err := ws.UpdateTask(*targetTask); err != nil {
			logger.Error("Failed to reset task for immediate execution", logger.Fields{"node_id": nodeID, "task_id": targetTask.ID, "err": err})
			orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to reset task for execution", err)
			return
		}

		taskID = targetTask.ID
	} else {
		orihttp.RespondErrorWithErr(w, http.StatusBadRequest, "Scheduler node is not linked to a task. Connect it to a task node first.", fmt.Errorf("missing target_task_id"))
		return
	}

	// Update scheduler bookkeeping
	foundTask.LastRun = &now
	foundTask.ExecutionCount++
	foundTask.FailureCount = 0
	foundTask.LastError = ""

	execution := workspace.TaskExecution{
		TaskID:     taskID,
		ExecutedAt: now,
		Status:     "success",
	}
	foundTask.ExecutionHistory = append(foundTask.ExecutionHistory, execution)
	if len(foundTask.ExecutionHistory) > 20 {
		foundTask.ExecutionHistory = foundTask.ExecutionHistory[len(foundTask.ExecutionHistory)-20:]
	}

	nextRun := workspace.CalculateNextRun(foundTask.Schedule, now)
	foundTask.NextRun = nextRun
	if nextRun == nil {
		foundTask.Enabled = false
	}

	if err := ws.UpdateScheduledTask(*foundTask); err != nil {
		logger.Error("Failed to update scheduler node", logger.Fields{"node_id": nodeID, "err": err})
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to update scheduler node", err)
		return
	}

	if err := th.workspaceStore.Save(ws); err != nil {
		logger.Error("Failed to save workspace", logger.Fields{"error": err})
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to save workspace", err)
		return
	}

	// Execute task immediately in background with a timeout
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		logger.Info("Executing scheduler-triggered task", logger.Fields{"task_id": taskID, "agent": targetTask.To})

		taskRun, execErr := workspace.ExecuteTaskWithRunMetadata(ctx, th.taskHandler, targetTask.To, *targetTask)
		result := taskRun.Result
		if execErr != nil {
			logger.Error("Task execution failed", logger.Fields{"task_id": taskID, "err": execErr})

			// Update task with error
			ws, wsErr := th.workspaceStore.Get(workspaceID)
			if wsErr == nil {
				if task, getErr := ws.GetTask(taskID); getErr == nil {
					if taskRun.RunID != "" {
						task.CurrentRunID = taskRun.RunID
					}
					if err := task.SetStatus(workspace.TaskStatusFailed); err != nil {
						logger.Error("Scheduler node failure transition rejected", logger.Fields{"task_id": taskID, "error": err})
					} else {
						task.Error = execErr.Error()
						completedAt := time.Now()
						task.CompletedAt = &completedAt
						_ = ws.UpdateTask(*task)
						_ = th.workspaceStore.Save(ws)
					}
				}
			}
		} else {
			logger.Info("Task execution completed", logger.Fields{"task_id": taskID, "result_length": len(result)})

			// Update task with result
			ws, wsErr := th.workspaceStore.Get(workspaceID)
			if wsErr == nil {
				if task, getErr := ws.GetTask(taskID); getErr == nil {
					if taskRun.RunID != "" {
						task.CurrentRunID = taskRun.RunID
					}
					if err := task.SetStatus(workspace.TaskStatusCompleted); err != nil {
						logger.Error("Scheduler node completion transition rejected", logger.Fields{"task_id": taskID, "error": err})
					} else {
						task.Result = result
						workspace.ApplyTaskResultMetadata(task, result)
						completedAt := time.Now()
						task.CompletedAt = &completedAt
						_ = ws.UpdateTask(*task)
						_ = th.workspaceStore.Save(ws)
					}
				}
			}
		}
	}()

	if th.eventBus != nil {
		payload := map[string]any{
			"task_id":         taskID,
			"task_created":    foundTask.TargetTaskID == "",
			"execution_count": foundTask.ExecutionCount,
			"next_run":        nextRun,
			"timestamp":       now,
			"scheduled_task":  foundTask,
			"target_task_id":  foundTask.TargetTaskID,
		}
		th.eventBus.Publish(workspace.NewScheduledTaskEvent(workspace.EventScheduledTaskTriggered, ws.ID, foundTask.ID, foundTask.Name, payload))
	}

	logger.Info("Manually triggered scheduler node", logger.Fields{"node_id": nodeID, "task_id": taskID, "target_task_id": foundTask.TargetTaskID})

	orihttp.WriteJSON(w, map[string]any{
		"success": true,
		"task_id": taskID,
		"message": "Scheduler node triggered successfully - task execution started",
	})
}

// validateScheduleConfig validates a schedule configuration
func validateScheduleConfig(config workspace.ScheduleConfig) error {
	switch config.Type {
	case workspace.ScheduleOnce:
		if config.ExecuteAt == nil {
			return fmt.Errorf("execute_at is required for 'once' schedule type")
		}
		if config.ExecuteAt.Before(time.Now()) {
			return fmt.Errorf("execute_at must be in the future")
		}

	case workspace.ScheduleInterval:
		if config.Interval <= 0 {
			return fmt.Errorf("interval must be positive for 'interval' schedule type")
		}

	case workspace.ScheduleDaily:
		if config.TimeOfDay == "" {
			return fmt.Errorf("time_of_day is required for 'daily' schedule type")
		}
		if _, _, err := parseScheduleTime(config.TimeOfDay); err != nil {
			return fmt.Errorf("invalid time_of_day format: %w", err)
		}

	case workspace.ScheduleWeekly:
		if config.TimeOfDay == "" {
			return fmt.Errorf("time_of_day is required for 'weekly' schedule type")
		}
		if _, _, err := parseScheduleTime(config.TimeOfDay); err != nil {
			return fmt.Errorf("invalid time_of_day format: %w", err)
		}
		if config.DayOfWeek < 0 || config.DayOfWeek > 6 {
			return fmt.Errorf("day_of_week must be between 0 (Sunday) and 6 (Saturday)")
		}

	case workspace.ScheduleCron:
		if config.CronExpr == "" {
			return fmt.Errorf("cron_expr is required for 'cron' schedule type")
		}
		if err := workspace.ValidateCronExpression(config.CronExpr); err != nil {
			return fmt.Errorf("invalid cron expression: %w", err)
		}

	case workspace.ScheduleRelativeDelay:
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
