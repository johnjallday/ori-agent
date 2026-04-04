package orchestrationhttp

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/robfig/cron/v3"
)

// ScheduledTasksHandler handles listing and creating scheduled tasks
func (th *TaskHandler) ScheduledTasksHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		th.handleListScheduledTasks(w, r)
	case http.MethodPost:
		th.handleCreateScheduledTask(w, r)
	default:
		orihttp.MethodNotAllowed(w)
	}
}

func (th *TaskHandler) handleListScheduledTasks(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.URL.Query().Get("workspace_id")
	if workspaceID == "" {
		orihttp.BadRequest(w, "workspace_id is required")
		return
	}

	ws, err := th.workspaceStore.Get(workspaceID)
	if err != nil {
		logger.Error("Error getting workspace", logger.Fields{"error": workspaceID, "err": err})
		orihttp.RespondErrorWithErr(w, http.StatusNotFound, "Workspace not found", err)
		return
	}

	orihttp.WriteJSON(w, map[string]interface{}{
		"scheduled_tasks": ws.ScheduledTasks,
		"count":           len(ws.ScheduledTasks),
	})
}

// handleCreateScheduledTask creates a new scheduled task
func (th *TaskHandler) handleCreateScheduledTask(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkspaceID string                   `json:"workspace_id"`
		Name        string                   `json:"name"`
		Description string                   `json:"description"`
		From        string                   `json:"from"`
		To          string                   `json:"to"`
		Prompt      string                   `json:"prompt"`
		Priority    int                      `json:"priority"`
		Schedule    workspace.ScheduleConfig `json:"schedule"`
		Enabled     bool                     `json:"enabled"`
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	// Validate required fields
	if req.WorkspaceID == "" {
		orihttp.BadRequest(w, "workspace_id is required")
		return
	}
	if req.Name == "" {
		orihttp.BadRequest(w, "name is required")
		return
	}
	if req.Prompt == "" {
		orihttp.BadRequest(w, "prompt is required")
		return
	}
	if req.From == "" {
		orihttp.BadRequest(w, "from is required")
		return
	}
	if req.To == "" {
		orihttp.BadRequest(w, "to is required")
		return
	}

	ws, err := th.workspaceStore.Get(req.WorkspaceID)
	if err != nil {
		logger.Error("Error getting workspace", logger.Fields{"err": err, "workspace_id": req.WorkspaceID})
		orihttp.RespondErrorWithErr(w, http.StatusNotFound, "Workspace not found", err)
		return
	}

	// Create scheduled task
	now := time.Now()
	st := workspace.ScheduledTask{
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
		orihttp.RespondErrorWithErr(w, http.StatusBadRequest, "Failed to add scheduled task", err)
		return
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
		if ws.ScheduledTasks[i].Name == req.Name {
			createdTask = &ws.ScheduledTasks[i]
			break
		}
	}

	logger.Info("Created scheduled task in workspace", logger.Fields{"workspace_id": createdTask.ID, "workspaceid": req.WorkspaceID, "name": req.Name})

	w.WriteHeader(http.StatusCreated)
	orihttp.WriteJSON(w, map[string]interface{}{
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
		orihttp.BadRequest(w, "Invalid URL: missing task ID")
		return
	}

	id := parts[4]

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
			orihttp.BadRequest(w, fmt.Sprintf("Unknown action: %s", action))
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
		orihttp.MethodNotAllowed(w)
	}
}

func (th *TaskHandler) handleGetScheduledTask(w http.ResponseWriter, r *http.Request, id string) {
	// Find the scheduled task across all workspaces
	workspaceIDs, err := th.workspaceStore.List()
	if err != nil {
		logger.Error("Error listing workspaces", logger.Fields{"error": err})
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to list workspaces", err)
		return
	}

	for _, wsID := range workspaceIDs {
		ws, err := th.workspaceStore.Get(wsID)
		if err != nil {
			continue
		}

		st, err := ws.GetScheduledTask(id)
		if err == nil {
			orihttp.WriteJSON(w, map[string]interface{}{
				"scheduled_task": st,
			})
			return
		}
	}
	orihttp.NotFound(w, fmt.Sprintf("Scheduled task %s not found", id))
}

func (th *TaskHandler) handleUpdateScheduledTask(w http.ResponseWriter, r *http.Request, id string) {
	var req struct {
		Name        *string                   `json:"name,omitempty"`
		Description *string                   `json:"description,omitempty"`
		Prompt      *string                   `json:"prompt,omitempty"`
		Priority    *int                      `json:"priority,omitempty"`
		Schedule    *workspace.ScheduleConfig `json:"schedule,omitempty"`
		Enabled     *bool                     `json:"enabled,omitempty"`
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	// Find the scheduled task
	workspaceIDs, err := th.workspaceStore.List()
	if err != nil {
		logger.Error("Error listing workspaces", logger.Fields{"error": err})
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to list workspaces", err)
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
			orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to update scheduled task", err)
			return
		}

		if err := th.workspaceStore.Save(ws); err != nil {
			logger.Error("Failed to save workspace", logger.Fields{"error": err})
			orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to save workspace", err)
			return
		}

		logger.Info("Updated scheduled task", logger.Fields{"task_id": id})

		orihttp.WriteJSON(w, map[string]interface{}{
			"success":        true,
			"scheduled_task": st,
		})
		return
	}
	orihttp.NotFound(w, fmt.Sprintf("Scheduled task %s not found", id))
}

func (th *TaskHandler) handleDeleteScheduledTask(w http.ResponseWriter, r *http.Request, id string) {
	workspaceIDs, err := th.workspaceStore.List()
	if err != nil {
		logger.Error("Error listing workspaces", logger.Fields{"error": err})
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to list workspaces", err)
		return
	}

	for _, wsID := range workspaceIDs {
		ws, err := th.workspaceStore.Get(wsID)
		if err != nil {
			continue
		}

		if err := ws.DeleteScheduledTask(id); err == nil {
			if err := th.workspaceStore.Save(ws); err != nil {
				logger.Error("Failed to save workspace", logger.Fields{"error": err})
				orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to save workspace", err)
				return
			}

			logger.Info("Deleted scheduled task", logger.Fields{"task_id": id})

			orihttp.WriteJSON(w, map[string]interface{}{
				"success": true,
			})
			return
		}
	}
	orihttp.NotFound(w, fmt.Sprintf("Scheduled task %s not found", id))
}

func (th *TaskHandler) handleEnableScheduledTask(w http.ResponseWriter, r *http.Request, id string, enable bool) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	workspaceIDs, err := th.workspaceStore.List()
	if err != nil {
		logger.Error("Error listing workspaces", logger.Fields{"error": err})
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to list workspaces", err)
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
			orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to update scheduled task", err)
			return
		}

		if err := th.workspaceStore.Save(ws); err != nil {
			logger.Error("Failed to save workspace", logger.Fields{"error": err})
			orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to save workspace", err)
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

		orihttp.WriteJSON(w, map[string]interface{}{
			"success":        true,
			"enabled":        enable,
			"scheduled_task": st,
		})
		return
	}
	orihttp.NotFound(w, fmt.Sprintf("Scheduled task %s not found", id))
}

func (th *TaskHandler) handleTriggerScheduledTask(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	workspaceIDs, err := th.workspaceStore.List()
	if err != nil {
		logger.Error("Error listing workspaces", logger.Fields{"error": err})
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to list workspaces", err)
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
		task := workspace.Task{
			WorkspaceID: ws.ID,
			From:        st.From,
			To:          st.To,
			Description: st.Prompt,
			Priority:    st.Priority,
			Context:     st.Context,
			Status:      workspace.TaskStatusPending,
		}

		if err := ws.AddTask(task); err != nil {
			logger.Error("Failed to create task from scheduled task", logger.Fields{"task_id": err})
			orihttp.RespondErrorWithErr(w, http.StatusBadRequest, "Failed to create task", err)
			return
		}

		if err := th.workspaceStore.Save(ws); err != nil {
			logger.Error("Failed to save workspace", logger.Fields{"error": err})
			orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to save workspace", err)
			return
		}

		// Get the created task ID
		var taskID string
		if len(ws.Tasks) > 0 {
			taskID = ws.Tasks[len(ws.Tasks)-1].ID
		}

		logger.Info("Manually triggered scheduled task , created task", logger.Fields{"task_id": id, "taskID": taskID})

		orihttp.WriteJSON(w, map[string]interface{}{
			"success": true,
			"task_id": taskID,
		})
		return
	}
	orihttp.NotFound(w, fmt.Sprintf("Scheduled task %s not found", id))
}

// calculateInitialNextRun calculates the initial next run time for a schedule
func calculateInitialNextRun(config workspace.ScheduleConfig, now time.Time) *time.Time {
	switch config.Type {
	case workspace.ScheduleOnce:
		if config.ExecuteAt != nil {
			return config.ExecuteAt
		}
		return nil

	case workspace.ScheduleInterval:
		if config.Interval == 0 {
			return nil
		}
		next := now.Add(config.Interval)
		return &next

	case workspace.ScheduleDaily:
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

	case workspace.ScheduleWeekly:
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

	case workspace.ScheduleCron:
		if config.CronExpr == "" {
			return nil
		}

		// Validate and parse cron expression using workspace's validator
		if err := workspace.ValidateCronExpression(config.CronExpr); err != nil {
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

	case workspace.ScheduleRelativeDelay:
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
func (th *TaskHandler) getTaskWithWorkspace(taskID string) (*workspace.Task, *workspace.Workspace, error) {
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
func (th *TaskHandler) updateTaskAssignment(ws *workspace.Workspace, taskID string, newTo *string, assignedNodeID *string) (int, error) {
	for i := range ws.Tasks {
		if ws.Tasks[i].ID == taskID {
			if newTo != nil {
				ws.Tasks[i].To = *newTo

				// Auto-add agent to workspace if not already present
				if *newTo != "" && *newTo != "unassigned" && !ws.HasAgent(*newTo) {
					if err := ws.AddAgent(*newTo); err != nil {
						logger.Warn("Failed to auto-add agent to workspace", logger.Fields{"agent": *newTo, "error": err})
					} else {
						logger.Info("Auto-added agent to workspace on task reassignment", logger.Fields{"agent": *newTo, "workspace_id": ws.ID})
					}
				}
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
