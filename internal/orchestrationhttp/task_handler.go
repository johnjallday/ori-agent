package orchestrationhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/agentcomm"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/platform"
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

// FrontendScheduleConfigRaw is used for initial parsing to handle datetime strings without timezone
type FrontendScheduleConfigRaw struct {
	Type            string  `json:"type"`
	IntervalMinutes int     `json:"interval_minutes,omitempty"`
	Time            string  `json:"time,omitempty"`
	TimeOfDay       string  `json:"time_of_day,omitempty"`
	DayOfWeek       int     `json:"day_of_week,omitempty"`
	RunAt           *string `json:"run_at,omitempty"`     // String to handle various formats
	ExecuteAt       *string `json:"execute_at,omitempty"` // String to handle various formats
	CronExpr        string  `json:"cron_expr,omitempty"`
	MaxRuns         int     `json:"max_runs,omitempty"`
	EndDate         *string `json:"end_date,omitempty"`
}

// parseFlexibleTime parses a datetime string with or without timezone
func parseFlexibleTime(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}

	// Try RFC3339 first (with timezone)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return &t, nil
	}

	// Try without timezone (assume local)
	if t, err := time.ParseInLocation("2006-01-02T15:04:05", s, time.Local); err == nil {
		return &t, nil
	}

	// Try without seconds
	if t, err := time.ParseInLocation("2006-01-02T15:04", s, time.Local); err == nil {
		return &t, nil
	}

	return nil, fmt.Errorf("unable to parse time: %s", s)
}

// convertScheduleConfig converts frontend schedule format to backend format
func convertScheduleConfig(raw json.RawMessage) *workspace.ScheduleConfig {
	if raw == nil {
		return nil
	}

	// First try parsing with raw strings to handle flexible datetime formats
	var rawConfig FrontendScheduleConfigRaw
	if err := json.Unmarshal(raw, &rawConfig); err != nil {
		logger.Warn("Failed to parse schedule config", logger.Fields{"err": err})
		return nil
	}

	config := &workspace.ScheduleConfig{
		Type:     workspace.ScheduleType(rawConfig.Type),
		MaxRuns:  rawConfig.MaxRuns,
		CronExpr: rawConfig.CronExpr,
	}

	// Handle interval conversion (minutes to time.Duration)
	if rawConfig.IntervalMinutes > 0 {
		config.Interval = time.Duration(rawConfig.IntervalMinutes) * time.Minute
		logger.Debug("Converted interval_minutes to Duration", logger.Fields{
			"interval_minutes": rawConfig.IntervalMinutes,
			"interval":         config.Interval,
		})
	}

	// Handle time_of_day (frontend sends "time" or "time_of_day")
	if rawConfig.Time != "" {
		config.TimeOfDay = rawConfig.Time
	} else if rawConfig.TimeOfDay != "" {
		config.TimeOfDay = rawConfig.TimeOfDay
	}

	// Handle day_of_week
	config.DayOfWeek = rawConfig.DayOfWeek

	// Handle execute_at (frontend sends "run_at" or "execute_at") with flexible parsing
	if rawConfig.RunAt != nil {
		if t, err := parseFlexibleTime(*rawConfig.RunAt); err == nil {
			config.ExecuteAt = t
		} else {
			logger.Warn("Failed to parse run_at time", logger.Fields{"value": *rawConfig.RunAt, "err": err})
		}
	} else if rawConfig.ExecuteAt != nil {
		if t, err := parseFlexibleTime(*rawConfig.ExecuteAt); err == nil {
			config.ExecuteAt = t
		} else {
			logger.Warn("Failed to parse execute_at time", logger.Fields{"value": *rawConfig.ExecuteAt, "err": err})
		}
	}

	// Handle end_date with flexible parsing
	if rawConfig.EndDate != nil {
		if t, err := parseFlexibleTime(*rawConfig.EndDate); err == nil {
			config.EndDate = t
		} else {
			logger.Warn("Failed to parse end_date time", logger.Fields{"value": *rawConfig.EndDate, "err": err})
		}
	}

	logger.Debug("Converted frontend schedule to backend format", logger.Fields{
		"type":        config.Type,
		"interval":    config.Interval,
		"time_of_day": config.TimeOfDay,
		"day_of_week": config.DayOfWeek,
		"execute_at":  config.ExecuteAt,
	})

	return config
}

func normalizeTaskSleepPolicy(policy string) string {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "skip", "run_once_on_wake":
		return strings.ToLower(strings.TrimSpace(policy))
	default:
		return "run_once_on_wake"
	}
}

func normalizeWakeFallbackPolicy(policy string) string {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "run_on_next_wake", "skip":
		return strings.ToLower(strings.TrimSpace(policy))
	default:
		return "run_on_next_wake"
	}
}

func normalizeWakeLeadMinutes(minutes int) int {
	if minutes <= 0 {
		return 5
	}
	if minutes > 120 {
		return 120
	}
	return minutes
}

// TaskHandler manages task and scheduled task operations
type TaskHandler struct {
	workspaceStore workspace.Store
	communicator   *agentcomm.Communicator
	taskHandler    workspace.TaskHandler
	eventBus       *workspace.EventBus
	runningMu      sync.Mutex
	runningCancels map[string]context.CancelFunc
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
		runningCancels: make(map[string]context.CancelFunc),
	}
}

func (th *TaskHandler) registerRunningTask(taskID string, cancel context.CancelFunc) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" || cancel == nil {
		return
	}

	th.runningMu.Lock()
	defer th.runningMu.Unlock()
	if th.runningCancels == nil {
		th.runningCancels = make(map[string]context.CancelFunc)
	}
	th.runningCancels[taskID] = cancel
}

func (th *TaskHandler) unregisterRunningTask(taskID string) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return
	}

	th.runningMu.Lock()
	defer th.runningMu.Unlock()
	if th.runningCancels != nil {
		delete(th.runningCancels, taskID)
	}
}

func (th *TaskHandler) cancelRunningTask(taskID string) bool {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return false
	}

	th.runningMu.Lock()
	cancel, ok := th.runningCancels[taskID]
	th.runningMu.Unlock()
	if !ok || cancel == nil {
		return false
	}

	cancel()
	return true
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
	workspaceID := r.URL.Query().Get("workspace_id")
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
		ws, err := th.workspaceStore.Get(workspaceID)
		if err != nil {
			logger.Error("Failed to get workspace", logger.Fields{"error": err, "workspace_id": workspaceID})
			orihttp.RespondErrorWithErr(w, http.StatusNotFound, "Workspace not found", err)
			return
		}
		if result, err := workspace.ImportTaskMarkdownFromStore(th.workspaceStore, ws); err != nil {
			logger.Warn("Failed to import task markdown before listing tasks", logger.Fields{"workspace_id": workspaceID, "error": err})
		} else if result != nil {
			workspace.LogTaskMarkdownWarnings(workspaceID, result.Warnings)
			if result.Changed {
				if saveErr := th.workspaceStore.Save(ws); saveErr != nil {
					logger.Warn("Failed to save workspace after task markdown import", logger.Fields{"workspace_id": workspaceID, "error": saveErr})
				}
			}
		}
		tasks := ws.Tasks
		stats := ws.GetTaskStats()

		orihttp.WriteJSON(w, map[string]any{
			"tasks": tasks,
			"stats": stats,
			"count": len(tasks),
		})
		return
	}

	if agentName != "" {
		// List tasks for agent
		tasks := th.communicator.ListTasksForAgent(agentName)
		orihttp.WriteJSON(w, map[string]any{
			"tasks": tasks,
			"count": len(tasks),
		})
		return
	}

	orihttp.BadRequest(w, "id, workspace_id, or agent parameter required")
}

func (th *TaskHandler) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkspaceID            string                         `json:"workspace_id"`
		From                   string                         `json:"from"`
		To                     string                         `json:"to"`
		AssignedNodeID         string                         `json:"assigned_node_id"`
		Description            string                         `json:"description"`
		Details                string                         `json:"details"`
		Priority               int                            `json:"priority"`
		InputTaskIDs           []string                       `json:"input_task_ids"`
		ParentTaskID           string                         `json:"parent_task_id"`
		SubtaskIndex           int                            `json:"subtask_index"`
		OrchestrationMode      string                         `json:"orchestration_mode"`
		ResultCombinationMode  string                         `json:"result_combination_mode"`
		CombinationInstruction string                         `json:"combination_instruction"`
		OutputSchema           *workspace.TaskOutputSchema    `json:"output_schema"`
		OutputContract         *workspace.TaskOutputContract  `json:"output_contract"`
		OutputSpec             *workspace.TaskOutputSpec      `json:"output_spec"`
		DraftOutputSpec        *workspace.TaskOutputSpec      `json:"draft_output_spec"`
		TemplateRef            *workspace.TaskTemplateRef     `json:"template_ref"`
		Schedule               json.RawMessage                `json:"schedule"`
		ScheduleEnabled        bool                           `json:"schedule_enabled"`
		ScheduleName           string                         `json:"schedule_name"`
		SleepPolicy            string                         `json:"sleep_policy"`
		WakeMacEnabled         bool                           `json:"wake_mac_enabled"`
		WakeLeadMinutes        int                            `json:"wake_lead_minutes"`
		WakeFallbackPolicy     string                         `json:"wake_fallback_policy"`
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
	outputSpec, outputSpecErrors := workspace.NormalizeTaskOutputSpec(req.OutputSpec)
	if len(outputSpecErrors) > 0 {
		orihttp.BadRequest(w, "Invalid output_spec: "+strings.Join(outputSpecErrors, "; "))
		return
	}
	draftOutputSpec, draftSpecErrors := workspace.NormalizeTaskOutputSpec(req.DraftOutputSpec)
	if len(draftSpecErrors) > 0 {
		orihttp.BadRequest(w, "Invalid draft_output_spec: "+strings.Join(draftSpecErrors, "; "))
		return
	}

	// Create task
	task := workspace.Task{
		WorkspaceID:            req.WorkspaceID,
		From:                   req.From,
		To:                     req.To,
		AssignedNodeID:         req.AssignedNodeID,
		Description:            req.Description,
		Details:                req.Details,
		Priority:               normalizeTaskPriority(req.Priority),
		InputTaskIDs:           req.InputTaskIDs,
		ParentTaskID:           req.ParentTaskID,
		SubtaskIndex:           req.SubtaskIndex,
		OrchestrationMode:      workspace.NormalizeTaskOrchestrationMode(req.OrchestrationMode),
		ResultCombinationMode:  workspace.NormalizeTaskResultCombinationMode(req.ResultCombinationMode),
		CombinationInstruction: strings.TrimSpace(req.CombinationInstruction),
		OutputSchema:           workspace.NormalizeTaskOutputSchema(req.OutputSchema),
		OutputContract:         workspace.NormalizeTaskOutputContract(req.OutputContract),
		OutputSpec:             outputSpec,
		DraftOutputSpec:        draftOutputSpec,
		TemplateRef:            req.TemplateRef,
		Status:                 workspace.TaskStatusPending,
		Schedule:               schedule,
		ScheduleEnabled:        req.ScheduleEnabled,
		ScheduleName:           req.ScheduleName,
		SleepPolicy:            normalizeTaskSleepPolicy(req.SleepPolicy),
		WakeMacEnabled:         req.WakeMacEnabled,
		WakeLeadMinutes:        normalizeWakeLeadMinutes(req.WakeLeadMinutes),
		WakeFallback:           normalizeWakeFallbackPolicy(req.WakeFallbackPolicy),
		ResultStorage:          req.ResultStorage,
	}
	if task.OutputSpec != nil {
		task.OutputSchema = task.OutputSpec.Schema
		task.OutputContract = task.OutputSpec.Contract
	}

	// Validate: scheduled tasks must be assigned to an agent
	if task.ScheduleEnabled && task.Schedule != nil {
		if task.To == "" || task.To == "unassigned" {
			orihttp.BadRequest(w, "Scheduled tasks must be assigned to an agent. Please assign an agent before enabling the schedule.")
			return
		}
		// Calculate NextRun - pass zero time since task has never run
		nextRun := workspace.CalculateNextRun(*task.Schedule, time.Time{})
		task.NextRun = nextRun
	} else if task.WakeMacEnabled {
		task.WakeMacEnabled = false
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
		if respondTaskGraphError(w, err, "Failed to add task") {
			return
		}
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

	if th.eventBus != nil {
		th.eventBus.Publish(workspace.Event{
			Type:        workspace.EventTaskCreated,
			WorkspaceID: createdTask.WorkspaceID,
			Source:      "api",
			Data: map[string]any{
				"task_id":     createdTask.ID,
				"description": createdTask.Description,
				"to":          createdTask.To,
				"status":      createdTask.Status,
			},
			Metadata: map[string]string{},
		})
		th.eventBus.Publish(workspace.NewWorkspaceEvent(workspace.EventWorkspaceUpdated, createdTask.WorkspaceID, "task.create", map[string]any{
			"task_id": createdTask.ID,
		}))
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
	orihttp.WriteJSON(w, map[string]any{
		"success": true,
		"task":    createdTask,
	})
}

// taskUpdateRequest contains all fields for task update operations
type taskUpdateRequest struct {
	TaskID                 string                         `json:"task_id"`
	Status                 string                         `json:"status"`
	Result                 string                         `json:"result"`
	Error                  string                         `json:"error"`
	Description            *string                        `json:"description"`
	Details                *string                        `json:"details"`
	Priority               *int                           `json:"priority"`
	Context                map[string]any                 `json:"context"`
	To                     *string                        `json:"to"`
	AssignedNodeID         *string                        `json:"assigned_node_id"`
	InputTaskIDs           []string                       `json:"input_task_ids"`
	ParentTaskID           *string                        `json:"parent_task_id"`
	SubtaskIndex           *int                           `json:"subtask_index"`
	OrchestrationMode      *string                        `json:"orchestration_mode"`
	ResultCombinationMode  *string                        `json:"result_combination_mode"`
	CombinationInstruction *string                        `json:"combination_instruction"`
	OutputSchema           *workspace.TaskOutputSchema    `json:"output_schema"`
	OutputContract         *workspace.TaskOutputContract  `json:"output_contract"`
	OutputSpec             *workspace.TaskOutputSpec      `json:"output_spec"`
	DraftOutputSpec        *workspace.TaskOutputSpec      `json:"draft_output_spec"`
	TemplateRef            *workspace.TaskTemplateRef     `json:"template_ref"`
	Schedule               json.RawMessage                `json:"schedule"`
	ScheduleEnabled        *bool                          `json:"schedule_enabled"`
	ScheduleName           *string                        `json:"schedule_name"`
	SleepPolicy            *string                        `json:"sleep_policy"`
	WakeMacEnabled         *bool                          `json:"wake_mac_enabled"`
	WakeLeadMinutes        *int                           `json:"wake_lead_minutes"`
	WakeFallbackPolicy     *string                        `json:"wake_fallback_policy"`
	ResultStorage          *workspace.ResultStorageConfig `json:"result_storage"`
	KanbanColumnID         *string                        `json:"kanban_column_id"`
	KanbanLabels           []string                       `json:"kanban_labels"`
	KanbanDueDate          *string                        `json:"kanban_due_date"`
}

// hasFieldUpdates returns true if the request contains any field updates
func (r *taskUpdateRequest) hasFieldUpdates() bool {
	return r.Description != nil || r.Details != nil || r.Priority != nil || r.Context != nil || r.InputTaskIDs != nil ||
		r.To != nil || r.ParentTaskID != nil || r.SubtaskIndex != nil || r.OrchestrationMode != nil ||
		r.ResultCombinationMode != nil || r.CombinationInstruction != nil || r.OutputSchema != nil ||
		r.OutputContract != nil || r.OutputSpec != nil || r.DraftOutputSpec != nil ||
		r.TemplateRef != nil || r.ResultStorage != nil || r.KanbanColumnID != nil ||
		r.KanbanLabels != nil || r.KanbanDueDate != nil
}

// hasScheduleUpdates returns true if the request contains schedule-related updates
func (r *taskUpdateRequest) hasScheduleUpdates(schedule *workspace.ScheduleConfig, clearSchedule bool) bool {
	return schedule != nil || clearSchedule || r.ScheduleEnabled != nil || r.ScheduleName != nil ||
		r.SleepPolicy != nil || r.WakeMacEnabled != nil || r.WakeLeadMinutes != nil || r.WakeFallbackPolicy != nil
}

// applyBasicFieldUpdates applies description, details, input connections, parent, and subtask updates
func (th *TaskHandler) applyBasicFieldUpdates(task *workspace.Task, req *taskUpdateRequest) {
	if req.Description != nil {
		task.Description = *req.Description
		logger.Debug("Updated task description", logger.Fields{"task_id": req.TaskID})
	}
	if req.Details != nil {
		task.Details = *req.Details
		logger.Debug("Updated task details", logger.Fields{"task_id": req.TaskID})
	}
	if req.Priority != nil {
		task.Priority = normalizeTaskPriority(*req.Priority)
		logger.Debug("Updated task priority", logger.Fields{"task_id": req.TaskID, "priority": task.Priority})
	}
	if req.Context != nil {
		if task.Context == nil {
			task.Context = map[string]any{}
		}
		for key, value := range req.Context {
			if value == nil {
				delete(task.Context, key)
				continue
			}
			task.Context[key] = value
		}
		logger.Debug("Updated task context", logger.Fields{"task_id": req.TaskID, "context_keys": len(req.Context)})
	}
	if req.InputTaskIDs != nil {
		task.InputTaskIDs = req.InputTaskIDs
		logger.Debug("Updated task input connections", logger.Fields{"task_id": req.TaskID, "inputtaskids": req.InputTaskIDs})
	}
	if req.ParentTaskID != nil {
		task.ParentTaskID = strings.TrimSpace(*req.ParentTaskID)
		if task.ParentTaskID == "" {
			task.SubtaskIndex = 0
		}
		logger.Debug("Updated task parent", logger.Fields{"task_id": req.TaskID, "parent_task_id": task.ParentTaskID})
	}
	if req.SubtaskIndex != nil {
		task.SubtaskIndex = *req.SubtaskIndex
		logger.Debug("Updated task subtask index", logger.Fields{"task_id": req.TaskID, "subtask_index": *req.SubtaskIndex})
	}
	if req.OrchestrationMode != nil {
		task.OrchestrationMode = workspace.NormalizeTaskOrchestrationMode(*req.OrchestrationMode)
		logger.Debug("Updated task orchestration mode", logger.Fields{"task_id": req.TaskID, "orchestration_mode": task.OrchestrationMode})
	}
	if req.ResultCombinationMode != nil {
		task.ResultCombinationMode = workspace.NormalizeTaskResultCombinationMode(*req.ResultCombinationMode)
		logger.Debug("Updated task result combination mode", logger.Fields{"task_id": req.TaskID, "result_combination_mode": task.ResultCombinationMode})
	}
	if req.CombinationInstruction != nil {
		task.CombinationInstruction = strings.TrimSpace(*req.CombinationInstruction)
		logger.Debug("Updated task combination instruction", logger.Fields{"task_id": req.TaskID})
	}
	if req.OutputSchema != nil {
		task.OutputSchema = workspace.NormalizeTaskOutputSchema(req.OutputSchema)
		logger.Debug("Updated task output schema", logger.Fields{"task_id": req.TaskID, "has_output_schema": task.OutputSchema != nil})
	}
	if req.OutputContract != nil {
		task.OutputContract = workspace.NormalizeTaskOutputContract(req.OutputContract)
		logger.Debug("Updated task output contract", logger.Fields{"task_id": req.TaskID, "has_output_contract": task.OutputContract != nil})
	}
	if req.OutputSpec != nil {
		outputSpec, errs := workspace.NormalizeTaskOutputSpec(req.OutputSpec)
		if len(errs) == 0 {
			task.OutputSpec = outputSpec
			if outputSpec != nil {
				task.OutputSchema = outputSpec.Schema
				task.OutputContract = outputSpec.Contract
			}
		}
		logger.Debug("Updated task output spec", logger.Fields{"task_id": req.TaskID, "has_output_spec": task.OutputSpec != nil, "error_count": len(errs)})
	}
	if req.DraftOutputSpec != nil {
		draftOutputSpec, errs := workspace.NormalizeTaskOutputSpec(req.DraftOutputSpec)
		if len(errs) == 0 {
			task.DraftOutputSpec = draftOutputSpec
		}
		logger.Debug("Updated task draft output spec", logger.Fields{"task_id": req.TaskID, "has_draft_output_spec": task.DraftOutputSpec != nil, "error_count": len(errs)})
	}
	if req.TemplateRef != nil {
		task.TemplateRef = req.TemplateRef
		logger.Debug("Updated task template reference", logger.Fields{"task_id": req.TaskID})
	}
	if req.ResultStorage != nil {
		task.ResultStorage = req.ResultStorage
		logger.Debug("Updated task result storage", logger.Fields{"task_id": req.TaskID, "enabled": req.ResultStorage.Enabled})
	}
	if req.KanbanColumnID != nil {
		val := strings.TrimSpace(*req.KanbanColumnID)
		if task.Context == nil {
			task.Context = map[string]any{}
		}
		if val == "" {
			delete(task.Context, "kanban_column_id")
		} else {
			task.Context["kanban_column_id"] = val
		}
		logger.Debug("Updated task kanban column", logger.Fields{"task_id": req.TaskID, "kanban_column_id": val})
	}
	if req.KanbanLabels != nil {
		if task.Context == nil {
			task.Context = map[string]any{}
		}
		if len(req.KanbanLabels) == 0 {
			delete(task.Context, "kanban_labels")
		} else {
			task.Context["kanban_labels"] = req.KanbanLabels
		}
		logger.Debug("Updated task kanban labels", logger.Fields{"task_id": req.TaskID, "labels": req.KanbanLabels})
	}
	if req.KanbanDueDate != nil {
		val := strings.TrimSpace(*req.KanbanDueDate)
		if task.Context == nil {
			task.Context = map[string]any{}
		}
		if val == "" {
			delete(task.Context, "kanban_due_date")
		} else {
			task.Context["kanban_due_date"] = val
		}
		logger.Debug("Updated task kanban due date", logger.Fields{"task_id": req.TaskID, "kanban_due_date": val})
	}
}

// applyScheduleUpdates applies schedule configuration changes to a task
// Returns an error message if validation fails, empty string otherwise
func (th *TaskHandler) applyScheduleUpdates(task *workspace.Task, req *taskUpdateRequest, schedule *workspace.ScheduleConfig, clearSchedule bool) string {
	if schedule != nil {
		task.Schedule = schedule
		if task.ScheduleEnabled {
			task.NextRun = th.calculateNextRun(task)
		}
		logger.Debug("Updated task schedule", logger.Fields{"task_id": req.TaskID})
	}

	if clearSchedule {
		task.Schedule = nil
		task.ScheduleEnabled = false
		task.ScheduleName = ""
		task.SleepPolicy = ""
		task.WakeMacEnabled = false
		task.WakeLeadMinutes = 0
		task.WakeFallback = ""
		task.NextRun = nil
		logger.Debug("Cleared task schedule", logger.Fields{"task_id": req.TaskID})
	}

	if req.ScheduleEnabled != nil {
		if *req.ScheduleEnabled {
			taskTo := task.To
			if req.To != nil {
				taskTo = *req.To
			}
			if taskTo == "" || taskTo == "unassigned" {
				return "Scheduled tasks must be assigned to an agent. Please assign an agent before enabling the schedule."
			}
		}

		task.ScheduleEnabled = *req.ScheduleEnabled
		if *req.ScheduleEnabled && task.Schedule != nil {
			task.NextRun = th.calculateNextRun(task)
		} else if !*req.ScheduleEnabled {
			task.NextRun = nil
		}
		logger.Debug("Updated task schedule enabled", logger.Fields{"task_id": req.TaskID, "enabled": *req.ScheduleEnabled})
	}

	if req.ScheduleName != nil {
		task.ScheduleName = *req.ScheduleName
	}
	if req.SleepPolicy != nil {
		task.SleepPolicy = normalizeTaskSleepPolicy(*req.SleepPolicy)
	}
	if req.WakeMacEnabled != nil {
		task.WakeMacEnabled = *req.WakeMacEnabled
	}
	if req.WakeLeadMinutes != nil {
		task.WakeLeadMinutes = normalizeWakeLeadMinutes(*req.WakeLeadMinutes)
	}
	if req.WakeFallbackPolicy != nil {
		task.WakeFallback = normalizeWakeFallbackPolicy(*req.WakeFallbackPolicy)
	}

	if task.WakeMacEnabled && (task.Schedule == nil || !task.ScheduleEnabled) {
		task.WakeMacEnabled = false
	}

	return ""
}

// calculateNextRun calculates the next run time for a scheduled task
func (th *TaskHandler) calculateNextRun(task *workspace.Task) *time.Time {
	if task.Schedule == nil {
		return nil
	}
	lastRun := time.Time{}
	if task.LastRun != nil {
		lastRun = *task.LastRun
	}
	return workspace.CalculateNextRun(*task.Schedule, lastRun)
}

// buildTaskUpdateEventData builds the event data for a task update
func (th *TaskHandler) buildTaskUpdateEventData(req *taskUpdateRequest, schedule *workspace.ScheduleConfig) map[string]any {
	eventData := map[string]any{
		"task_id":     req.TaskID,
		"update_type": "task_update",
	}
	if req.InputTaskIDs != nil {
		eventData["input_task_ids"] = req.InputTaskIDs
	}
	if req.To != nil {
		eventData["to"] = *req.To
	}
	if req.Priority != nil {
		eventData["priority"] = normalizeTaskPriority(*req.Priority)
	}
	if req.AssignedNodeID != nil {
		eventData["assigned_node_id"] = *req.AssignedNodeID
	}
	if req.OrchestrationMode != nil {
		eventData["orchestration_mode"] = workspace.NormalizeTaskOrchestrationMode(*req.OrchestrationMode)
	}
	if req.ResultCombinationMode != nil {
		eventData["result_combination_mode"] = workspace.NormalizeTaskResultCombinationMode(*req.ResultCombinationMode)
	}
	if req.CombinationInstruction != nil {
		eventData["combination_instruction"] = strings.TrimSpace(*req.CombinationInstruction)
	}
	if req.OutputSchema != nil {
		eventData["output_schema"] = workspace.NormalizeTaskOutputSchema(req.OutputSchema)
	}
	if req.TemplateRef != nil {
		eventData["template_ref"] = req.TemplateRef
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
	if req.SleepPolicy != nil {
		eventData["sleep_policy"] = normalizeTaskSleepPolicy(*req.SleepPolicy)
	}
	if req.WakeMacEnabled != nil {
		eventData["wake_mac_enabled"] = *req.WakeMacEnabled
	}
	if req.WakeLeadMinutes != nil {
		eventData["wake_lead_minutes"] = normalizeWakeLeadMinutes(*req.WakeLeadMinutes)
	}
	if req.WakeFallbackPolicy != nil {
		eventData["wake_fallback_policy"] = normalizeWakeFallbackPolicy(*req.WakeFallbackPolicy)
	}
	if req.KanbanLabels != nil {
		eventData["kanban_labels"] = req.KanbanLabels
	}
	if req.KanbanDueDate != nil {
		eventData["kanban_due_date"] = *req.KanbanDueDate
	}
	return eventData
}

func (th *TaskHandler) handleUpdateTask(w http.ResponseWriter, r *http.Request) {
	var req taskUpdateRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	// Convert frontend schedule format to backend format
	var schedule *workspace.ScheduleConfig
	clearSchedule := false // Track if we should explicitly clear the schedule
	if len(req.Schedule) > 0 {
		// Check if schedule is explicitly set to null (4 bytes: "null")
		if string(req.Schedule) == "null" {
			clearSchedule = true
		} else {
			schedule = convertScheduleConfig(req.Schedule)
		}
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

	// Handle task updates (description, details, input connections, reassignment, schedule, or result storage)
	if req.hasFieldUpdates() || req.hasScheduleUpdates(schedule, clearSchedule) {
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

				// Apply basic field updates
				th.applyBasicFieldUpdates(&ws.Tasks[i], &req)

				// Update assignment using helper
				if req.To != nil {
					if req.AssignedNodeID != nil {
						logger.Debug("Reassigning task to (node)", logger.Fields{"task_id": req.TaskID, "to": *req.To, "assignednodeid": *req.AssignedNodeID})
					} else {
						logger.Debug("Reassigning task to (no node id)", logger.Fields{"task_id": req.TaskID, "to": *req.To})
					}
					if err := th.updateTaskAssignment(ws, req.TaskID, req.To, req.AssignedNodeID); err != nil {
						logger.Error("", logger.Fields{"err": err})
						orihttp.InternalError(w, err.Error())
						return
					}
				}

				// Apply schedule updates
				if errMsg := th.applyScheduleUpdates(&ws.Tasks[i], &req, schedule, clearSchedule); errMsg != "" {
					orihttp.BadRequest(w, errMsg)
					return
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
			th.eventBus.Publish(workspace.Event{
				Type:        workspace.EventWorkspaceUpdated,
				WorkspaceID: task.WorkspaceID,
				Data:        th.buildTaskUpdateEventData(&req, schedule),
			})
		}

		// Return updated task
		updatedTask, err := th.communicator.GetTask(req.TaskID)
		if err != nil {
			logger.Error("Failed to get updated task", logger.Fields{"task_id": req.TaskID, "error": err})
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
		if err := th.updateTaskAssignment(ws, req.TaskID, req.To, req.AssignedNodeID); err != nil {
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

		// Publish event (if eventBus is available)
		if th.eventBus != nil {
			th.eventBus.Publish(workspace.Event{
				Type:        workspace.EventTaskAssigned,
				WorkspaceID: task.WorkspaceID,
				Data: map[string]any{
					"task_id": req.TaskID,
					"to":      *req.To,
				},
			})
		}

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
		logger.Error("Failed to update task status", logger.Fields{"task_id": req.TaskID, "error": err})
		orihttp.BadRequest(w, err.Error())
		return
	}

	updatedTask, err := th.communicator.GetTask(req.TaskID)
	if err != nil {
		logger.Error("Failed to get updated task after status update", logger.Fields{"task_id": req.TaskID, "error": err})
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to retrieve updated task", err)
		return
	}

	w.WriteHeader(http.StatusOK)
	orihttp.WriteJSON(w, updatedTask)
}

// handleDeleteTask deletes a task
func (th *TaskHandler) handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	taskID := extractTaskIDForDelete(r)
	if taskID == "" {
		orihttp.BadRequest(w, "id parameter required")
		return
	}

	workspaceID := r.URL.Query().Get("workspace_id")

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

		if th.eventBus != nil {
			th.eventBus.Publish(workspace.Event{
				Type:        workspace.EventTaskDeleted,
				WorkspaceID: workspaceID,
				Source:      "api",
				Data: map[string]any{
					"task_id": taskID,
				},
				Metadata: map[string]string{},
			})
			th.eventBus.Publish(workspace.NewWorkspaceEvent(workspace.EventWorkspaceUpdated, workspaceID, "task.delete", map[string]any{
				"task_id": taskID,
			}))
		}

		logger.Info("Deleted task", logger.Fields{"task_id": taskID, "workspace_id": workspaceID})
		w.WriteHeader(http.StatusOK)
		orihttp.WriteJSON(w, map[string]any{
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
	orihttp.WriteJSON(w, map[string]any{
		"success": true,
		"message": "Task deleted successfully",
		"task_id": taskID,
	})
}

func extractTaskIDForDelete(r *http.Request) string {
	if r == nil || r.URL == nil {
		return ""
	}

	taskID := strings.TrimSpace(r.URL.Query().Get("id"))
	if taskID != "" {
		return taskID
	}

	path := strings.TrimSpace(r.URL.Path)
	if path == "" {
		return ""
	}

	trimmed := strings.TrimPrefix(path, "/api/orchestration/tasks/")
	if trimmed == path {
		return ""
	}
	trimmed = strings.Trim(trimmed, "/")
	if trimmed == "" {
		return ""
	}

	parts := strings.Split(trimmed, "/")
	if len(parts) == 0 {
		return ""
	}

	return strings.TrimSpace(parts[0])
}

// TasksPathHandler handles requests to /api/orchestration/tasks/{id}...
// Routes to appropriate handler based on path and method:
// - PUT /api/orchestration/tasks/{id} -> handleUpdateTask
// - GET /api/orchestration/tasks/{id} -> handleGetTasks (single task)
// - DELETE /api/orchestration/tasks/{id} -> handleDeleteTask
// - POST /api/orchestration/tasks/{id}/complete -> CompleteTaskHandler
// - POST /api/orchestration/tasks/{id}/cancel -> CancelTaskHandler
// - POST /api/orchestration/tasks/{id}/save-result -> SaveTaskResult (via workspace handler)
// - POST /api/orchestration/tasks/{id}/result/preview -> Preview typed task result
// - POST /api/orchestration/tasks/{id}/review -> Resolve a Needs Review output contract run
// - POST /api/orchestration/tasks/{id}/promote-result -> Promote task-list result into subtasks
func (th *TaskHandler) TasksPathHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Extract path after /api/orchestration/tasks/
	path := strings.TrimPrefix(r.URL.Path, "/api/orchestration/tasks/")

	// Check if this is a /complete endpoint
	if strings.HasSuffix(path, "/complete") {
		th.handleCompleteTask(w, r)
		return
	}

	// Check if this is a /cancel endpoint
	if strings.HasSuffix(path, "/cancel") {
		th.handleCancelTask(w, r)
		return
	}

	// Check if this is a /save-result endpoint
	if strings.HasSuffix(path, "/save-result") {
		th.handleSaveTaskResult(w, r)
		return
	}

	// Check if this is an output review endpoint
	if strings.HasSuffix(path, "/review") {
		th.handleTaskOutputReview(w, r)
		return
	}

	// Check if this is a /result/preview endpoint
	if strings.HasSuffix(path, "/result/preview") {
		th.handlePreviewTaskResult(w, r)
		return
	}

	// Check if this is a /promote-result endpoint
	if strings.HasSuffix(path, "/promote-result") {
		th.handlePromoteTaskResult(w, r)
		return
	}

	// Check if this is a /file-paths endpoint
	if strings.HasSuffix(path, "/file-paths") {
		th.handleFilePaths(w, r)
		return
	}

	// Check if this is an /assist endpoint
	if strings.HasSuffix(path, "/assist") {
		th.handleAssistTask(w, r)
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

// handleCancelTask handles POST /api/orchestration/tasks/{id}/cancel.
func (th *TaskHandler) handleCancelTask(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/orchestration/tasks/"), "/")
	if len(pathParts) < 2 || pathParts[0] == "" {
		orihttp.BadRequest(w, "task_id is required in URL path")
		return
	}
	taskID := pathParts[0]

	task, ws, err := th.getTaskWithWorkspace(taskID)
	if err != nil {
		orihttp.RespondErrorWithErr(w, http.StatusNotFound, "Task not found", err)
		return
	}

	cancelledRunning := th.cancelRunningTask(taskID)
	if task.Status != workspace.TaskStatusInProgress && !cancelledRunning {
		orihttp.BadRequest(w, "Task is not currently running")
		return
	}

	now := time.Now()
	for i := range ws.Tasks {
		if ws.Tasks[i].ID == taskID {
			if err := ws.Tasks[i].SetStatus(workspace.TaskStatusCancelled); err != nil {
				http.Error(w, fmt.Sprintf("cannot cancel task in state %q: %v", ws.Tasks[i].Status, err), http.StatusConflict)
				return
			}
			ws.Tasks[i].CompletedAt = &now
			ws.Tasks[i].Error = "Cancelled by user"
			ws.Tasks[i].Result = ""
			workspace.ApplyTaskResultMetadata(&ws.Tasks[i], "")
			break
		}
	}

	if err := th.workspaceStore.Save(ws); err != nil {
		logger.Error("Failed to save cancelled task", logger.Fields{"task_id": taskID, "error": err})
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to cancel task", err)
		return
	}

	logger.Info("Cancelled task manually", logger.Fields{"task_id": taskID, "workspace_id": task.WorkspaceID})

	if th.eventBus != nil {
		th.eventBus.Publish(workspace.NewWorkspaceEvent(workspace.EventWorkspaceUpdated, task.WorkspaceID, "task.cancel", map[string]any{
			"task_id": taskID,
			"status":  workspace.TaskStatusCancelled,
		}))
	}

	updatedTask, _ := ws.GetTask(taskID)
	w.WriteHeader(http.StatusOK)
	orihttp.WriteJSON(w, map[string]any{
		"success": true,
		"task":    updatedTask,
	})
}

type completeTaskRequest struct {
	Force  bool   `json:"force,omitempty"`
	Reason string `json:"reason,omitempty"`
}

func parseCompleteTaskRequest(r *http.Request) (completeTaskRequest, error) {
	var req completeTaskRequest
	if r == nil || r.Body == nil {
		return req, nil
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return req, err
	}
	if strings.TrimSpace(string(body)) == "" {
		return req, nil
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return req, err
	}
	req.Reason = strings.TrimSpace(req.Reason)
	return req, nil
}

func incompleteSubtaskLabels(subtasks []workspace.Task) []string {
	labels := make([]string, 0, len(subtasks))
	for _, subtask := range subtasks {
		if subtask.Status == workspace.TaskStatusCompleted {
			continue
		}
		label := strings.TrimSpace(subtask.Description)
		if label == "" {
			label = subtask.ID
		}
		labels = append(labels, label)
	}
	return labels
}

func recordManualCompletion(task *workspace.Task, req completeTaskRequest, completedAt time.Time) {
	if task.Context == nil {
		task.Context = map[string]any{}
	}
	record := map[string]any{
		"force":        req.Force,
		"completed_at": completedAt.UTC().Format(time.RFC3339),
	}
	if req.Reason != "" {
		record["reason"] = req.Reason
	}
	task.Context["manual_completion"] = record
}

func normalizeTaskPriority(priority int) int {
	if priority < 1 || priority > 5 {
		return 3
	}
	return priority
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

	completeReq, err := parseCompleteTaskRequest(r)
	if err != nil {
		orihttp.BadRequest(w, "Invalid request body")
		return
	}

	// Get task and workspace
	task, ws, err := th.getTaskWithWorkspace(taskID)
	if err != nil {
		orihttp.RespondErrorWithErr(w, http.StatusNotFound, "Task not found", err)
		return
	}

	incompleteSubtasks := incompleteSubtaskLabels(ws.GetSubtasks(taskID))
	if len(incompleteSubtasks) > 0 && !completeReq.Force {
		message := fmt.Sprintf("Cannot complete task while %d subtask(s) are incomplete", len(incompleteSubtasks))
		if len(incompleteSubtasks) <= 3 {
			message = fmt.Sprintf("%s: %s", message, strings.Join(incompleteSubtasks, ", "))
		}
		if err := orihttp.RespondError(w, http.StatusConflict, message); err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}
		return
	}

	// Find and update task status to completed
	for i := range ws.Tasks {
		if ws.Tasks[i].ID == taskID {
			now := time.Now()
			if err := ws.Tasks[i].SetStatus(workspace.TaskStatusCompleted); err != nil {
				orihttp.RespondErrorWithErr(w, http.StatusConflict, fmt.Sprintf("cannot complete task in state %q", ws.Tasks[i].Status), err)
				return
			}
			ws.Tasks[i].CompletedAt = &now
			ws.Tasks[i].Error = ""
			recordManualCompletion(&ws.Tasks[i], completeReq, now)
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
			Data: map[string]any{
				"task_id": taskID,
				"manual":  true,
			},
		})
	}

	// Return updated task
	updatedTask, _ := ws.GetTask(taskID)
	w.WriteHeader(http.StatusOK)
	orihttp.WriteJSON(w, map[string]any{
		"success": true,
		"task":    updatedTask,
	})
}

// SaveTaskResultRequest represents the request to save a task result
type SaveTaskResultRequest struct {
	TaskID      string `json:"task_id"`
	StoreNodeID string `json:"store_node_id,omitempty"` // Optional: save to specific store node
	FilePath    string `json:"file_path"`               // Required: relative file path within store or absolute path for direct save
	Format      string `json:"format,omitempty"`        // Optional: json, text, markdown, csv (default: text)
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

	// Security: Validate file path to prevent path traversal attacks
	cleanFilePath := filepath.Clean(req.FilePath)
	if strings.Contains(cleanFilePath, "..") {
		orihttp.BadRequest(w, "Invalid file path: path traversal not allowed")
		return
	}
	req.FilePath = cleanFilePath

	// Set default format
	if req.Format == "" {
		req.Format = "text"
	}

	// Validate format
	validFormats := map[string]bool{"json": true, "text": true, "markdown": true, "csv": true}
	if !validFormats[req.Format] {
		orihttp.BadRequest(w, "Format must be one of: json, text, markdown, csv")
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

		dataToStore := task.Result
		if req.Format == "csv" {
			dataToStore = workspace.TaskResultToCSV(task, task.Result, time.Now().Format("20060102-150405"), "")
		}

		// Write to store
		if err := workspace.WriteToStore(storeNode, req.FilePath, dataToStore); err != nil {
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
			var obj any
			if err := json.Unmarshal([]byte(task.Result), &obj); err != nil {
				// If not valid JSON, treat as plain text
				formattedData = []byte(task.Result)
			} else {
				formattedData, _ = json.MarshalIndent(obj, "", "  ")
			}
		case "csv":
			formattedData = []byte(workspace.TaskResultToCSV(task, task.Result, time.Now().Format("20060102-150405"), ""))
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
	orihttp.WriteJSON(w, map[string]any{
		"success":   true,
		"message":   "Result saved successfully",
		"file_path": finalPath,
		"task_id":   req.TaskID,
	})
}

type taskOutputReviewRequest struct {
	Action        string   `json:"action"`
	HistoryIndex  *int     `json:"history_index"`
	Result        string   `json:"result,omitempty"`
	ApprovedBy    string   `json:"approved_by,omitempty"`
	TargetColumns []string `json:"target_columns,omitempty"`
}

func (th *TaskHandler) handleTaskOutputReview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/orchestration/tasks/"), "/")
	if len(pathParts) < 2 || pathParts[0] == "" {
		orihttp.BadRequest(w, "task_id is required in URL path")
		return
	}
	taskID := pathParts[0]

	var req taskOutputReviewRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	action := strings.ToLower(strings.TrimSpace(req.Action))
	if action == "" {
		orihttp.BadRequest(w, "action is required")
		return
	}

	task, ws, err := th.getTaskWithWorkspace(taskID)
	if err != nil {
		orihttp.RespondErrorWithErr(w, http.StatusNotFound, "Task not found", err)
		return
	}
	historyIndex := resolveTaskReviewHistoryIndex(task, req.HistoryIndex)
	if historyIndex < 0 || historyIndex >= len(task.ExecutionHistory) {
		orihttp.BadRequest(w, "history_index does not identify a reviewable run")
		return
	}

	// Set by the cases that attempt to store a row so the shared response
	// below can report whether the row was actually written. A CSV header
	// mismatch holds the row for review (StorageStatus=skipped_invalid) while
	// the action itself succeeds, so "success" alone is not enough for a
	// non-UI client to tell that nothing was appended.
	var reviewValidation *workspace.TaskValidationResult

	switch action {
	case "inspect", "copy_raw":
		th.publishOutputContractReviewEvent(ws.ID, task.ID, action, task.ExecutionHistory[historyIndex].Validation)
		entry := task.ExecutionHistory[historyIndex]
		w.WriteHeader(http.StatusOK)
		orihttp.WriteJSON(w, map[string]any{
			"success":           true,
			"task_id":           task.ID,
			"history_index":     historyIndex,
			"result":            entry.Result,
			"summary":           entry.Summary,
			"validation_result": entry.Validation,
		})
		return
	case "retry_normalization":
		entry := task.ExecutionHistory[historyIndex]
		rawResult := strings.TrimSpace(entry.Result)
		if rawResult == "" {
			rawResult = strings.TrimSpace(entry.Summary)
		}
		if rawResult == "" {
			rawResult = strings.TrimSpace(task.Result)
		}
		if rawResult == "" {
			orihttp.BadRequest(w, "raw result is required to retry normalization")
			return
		}
		reviewTask := taskOutputReviewValidationTaskForEntry(task, entry)
		if reviewTask == nil || reviewTask.OutputSpec == nil {
			orihttp.BadRequest(w, "retry_normalization requires a structured output spec snapshot")
			return
		}
		var assistant workspace.TaskOutputSpecAssistant
		if candidate, ok := th.taskHandler.(workspace.TaskOutputSpecAssistant); ok {
			assistant = candidate
		}
		validation, csvData := workspace.ValidateTaskOutputSpecResultWithAssistant(r.Context(), reviewTask, rawResult, assistant)
		if validation.ValidationStatus == workspace.TaskValidationPassed {
			if err := appendApprovedTaskCSV(th.workspaceStore, ws, task, csvData); err != nil {
				if !recordTaskOutputReviewCSVHeaderMismatch(validation, err) {
					orihttp.InternalError(w, fmt.Sprintf("Failed to append retried normalization: %v", err))
					return
				}
			} else {
				validation.StorageStatus = workspace.TaskStorageAppended
			}
		}
		now := time.Now().UTC()
		validation.ValidatedAt = &now
		if err := th.recordTaskOutputReviewValidation(ws.ID, taskID, historyIndex, action, validation); err != nil {
			orihttp.InternalError(w, fmt.Sprintf("Failed to record normalization retry: %v", err))
			return
		}
		reviewValidation = validation
	case "rerun":
		if err := th.startTaskOutputReviewRerun(ws.ID, task.ID); err != nil {
			orihttp.RespondErrorWithErr(w, http.StatusBadRequest, "Failed to re-run task", err)
			return
		}
		th.publishOutputContractReviewEvent(ws.ID, task.ID, action, task.ExecutionHistory[historyIndex].Validation)
		w.WriteHeader(http.StatusAccepted)
		orihttp.WriteJSON(w, map[string]any{
			"success": true,
			"message": "Task re-run started",
			"task_id": task.ID,
		})
		return
	case "dismiss":
		now := time.Now().UTC()
		if err := th.workspaceStore.Update(ws.ID, func(fresh *workspace.Workspace) error {
			return fresh.MutateTask(taskID, func(t *workspace.Task) error {
				if historyIndex < 0 || historyIndex >= len(t.ExecutionHistory) {
					return fmt.Errorf("history entry no longer exists")
				}
				validation := t.ExecutionHistory[historyIndex].Validation
				if validation == nil {
					validation = &workspace.TaskValidationResult{}
				}
				validation.ValidationStatus = workspace.TaskValidationDismissed
				if validation.StorageStatus == "" {
					validation.StorageStatus = workspace.TaskStorageSkippedInvalid
				}
				validation.ValidatedAt = &now
				t.ExecutionHistory[historyIndex].Validation = validation
				workspace.MirrorTaskValidationResult(fresh.ID, t.ID, t.ExecutionHistory[historyIndex].RunID, validation)
				th.publishOutputContractReviewEvent(fresh.ID, t.ID, action, validation)
				return nil
			})
		}); err != nil {
			orihttp.InternalError(w, fmt.Sprintf("Failed to dismiss review: %v", err))
			return
		}
	case "approve_append":
		draft := strings.TrimSpace(req.Result)
		if draft == "" {
			draft = strings.TrimSpace(task.ExecutionHistory[historyIndex].Result)
		}
		if draft == "" {
			orihttp.BadRequest(w, "result is required for manual approval")
			return
		}
		reviewTask := taskOutputReviewValidationTask(task, task.ExecutionHistory[historyIndex].Validation)
		validation, csvData := validateTaskOutputReviewApproval(reviewTask, draft)
		if validation.ValidationStatus != workspace.TaskValidationPassed {
			th.publishOutputContractReviewEvent(ws.ID, task.ID, action, validation)
			w.WriteHeader(http.StatusBadRequest)
			orihttp.WriteJSON(w, map[string]any{
				"success":           false,
				"validation_result": validation,
				"message":           "Edited result does not match the output contract.",
			})
			return
		}
		if err := appendApprovedTaskCSV(th.workspaceStore, ws, task, csvData); err != nil {
			if recordTaskOutputReviewCSVHeaderMismatch(validation, err) {
				now := time.Now().UTC()
				validation.ValidatedAt = &now
				if err := th.recordTaskOutputReviewValidation(ws.ID, taskID, historyIndex, action, validation); err != nil {
					orihttp.InternalError(w, fmt.Sprintf("Failed to record append review: %v", err))
					return
				}
				reviewValidation = validation
				break
			}
			orihttp.InternalError(w, fmt.Sprintf("Failed to append approved result: %v", err))
			return
		}
		now := time.Now().UTC()
		validation.ValidationStatus = workspace.TaskValidationManuallyApproved
		validation.StorageStatus = workspace.TaskStorageManuallyAppended
		validation.Errors = nil
		validation.ManualApproval = &workspace.TaskManualApproval{
			ApprovedAt: now,
			ApprovedBy: strings.TrimSpace(req.ApprovedBy),
		}
		validation.ValidatedAt = &now
		if err := th.workspaceStore.Update(ws.ID, func(fresh *workspace.Workspace) error {
			return fresh.MutateTask(taskID, func(t *workspace.Task) error {
				if historyIndex < 0 || historyIndex >= len(t.ExecutionHistory) {
					return fmt.Errorf("history entry no longer exists")
				}
				t.ExecutionHistory[historyIndex].Validation = validation
				workspace.MirrorTaskValidationResult(fresh.ID, t.ID, t.ExecutionHistory[historyIndex].RunID, validation)
				th.publishOutputContractReviewEvent(fresh.ID, t.ID, action, validation)
				return nil
			})
		}); err != nil {
			orihttp.InternalError(w, fmt.Sprintf("Failed to record approval: %v", err))
			return
		}
		reviewValidation = validation
	case "reproject_to_destination":
		entry := task.ExecutionHistory[historyIndex]
		rawResult := strings.TrimSpace(entry.Result)
		if rawResult == "" {
			rawResult = strings.TrimSpace(entry.Summary)
		}
		if rawResult == "" {
			rawResult = strings.TrimSpace(task.Result)
		}
		if rawResult == "" {
			orihttp.BadRequest(w, "raw result is required to reproject")
			return
		}
		targetColumns := req.TargetColumns
		if len(targetColumns) == 0 {
			targetColumns = expectedColumnsFromValidation(entry.Validation)
		}
		if len(targetColumns) == 0 {
			orihttp.BadRequest(w, "no destination columns available to match; provide target_columns")
			return
		}
		var assistant workspace.TaskOutputSpecAssistant
		if candidate, ok := th.taskHandler.(workspace.TaskOutputSpecAssistant); ok {
			assistant = candidate
		}
		csvData, _, reprojErr := workspace.ReprojectResultToColumns(r.Context(), task, rawResult, targetColumns, assistant)
		if reprojErr != nil {
			orihttp.InternalError(w, fmt.Sprintf("Failed to reorganize result: %v", reprojErr))
			return
		}
		validation := entry.Validation
		if validation == nil {
			validation = &workspace.TaskValidationResult{}
		}
		if err := appendApprovedTaskCSV(th.workspaceStore, ws, task, csvData); err != nil {
			// A mismatch here would be unexpected (we matched the file's header),
			// but surface it for review rather than failing silently.
			if recordTaskOutputReviewCSVHeaderMismatch(validation, err) {
				now := time.Now().UTC()
				validation.ValidatedAt = &now
				if recordErr := th.recordTaskOutputReviewValidation(ws.ID, taskID, historyIndex, action, validation); recordErr != nil {
					orihttp.InternalError(w, fmt.Sprintf("Failed to record reorganize review: %v", recordErr))
					return
				}
				reviewValidation = validation
				break
			}
			orihttp.InternalError(w, fmt.Sprintf("Failed to append reorganized result: %v", err))
			return
		}
		now := time.Now().UTC()
		validation.ValidationStatus = workspace.TaskValidationManuallyApproved
		validation.StorageStatus = workspace.TaskStorageManuallyAppended
		validation.Errors = nil
		validation.ManualApproval = &workspace.TaskManualApproval{
			ApprovedAt: now,
			ApprovedBy: strings.TrimSpace(req.ApprovedBy),
		}
		validation.ValidatedAt = &now
		if err := th.recordTaskOutputReviewValidation(ws.ID, taskID, historyIndex, action, validation); err != nil {
			orihttp.InternalError(w, fmt.Sprintf("Failed to record reorganized append: %v", err))
			return
		}
		reviewValidation = validation
	default:
		orihttp.BadRequest(w, "action must be inspect, copy_raw, dismiss, rerun, retry_normalization, reproject_to_destination, or approve_append")
		return
	}

	updatedTask, err := th.communicator.GetTask(taskID)
	if err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to load updated task: %v", err))
		return
	}
	resp := map[string]any{
		"success": true,
		"task":    updatedTask,
	}
	// When the action attempted to store a row, report whether it actually
	// landed. A CSV header mismatch holds the row for review rather than
	// appending it, so "success" (the action was processed) must not be read
	// as "the row was stored".
	if reviewValidation != nil {
		resp["validation_status"] = reviewValidation.ValidationStatus
		resp["storage_status"] = reviewValidation.StorageStatus
		resp["stored"] = reviewValidation.StorageStatus == workspace.TaskStorageAppended ||
			reviewValidation.StorageStatus == workspace.TaskStorageSaved ||
			reviewValidation.StorageStatus == workspace.TaskStorageManuallyAppended
	}
	w.WriteHeader(http.StatusOK)
	orihttp.WriteJSON(w, resp)
}

func (th *TaskHandler) publishOutputContractReviewEvent(workspaceID, taskID, action string, validation *workspace.TaskValidationResult) {
	if th.eventBus == nil {
		return
	}
	data := map[string]any{
		"task_id": taskID,
		"action":  "review_action",
		"review":  strings.TrimSpace(action),
	}
	if validation != nil {
		data["validation_status"] = validation.ValidationStatus
		data["storage_status"] = validation.StorageStatus
		data["contract_version"] = validation.ContractVersion
		data["error_count"] = len(validation.Errors)
	}
	th.eventBus.Publish(workspace.NewWorkspaceEvent(workspace.EventTaskOutput, workspaceID, "task.output_contract", data))
}

func (th *TaskHandler) recordTaskOutputReviewValidation(workspaceID, taskID string, historyIndex int, action string, validation *workspace.TaskValidationResult) error {
	return th.workspaceStore.Update(workspaceID, func(fresh *workspace.Workspace) error {
		return fresh.MutateTask(taskID, func(t *workspace.Task) error {
			if historyIndex < 0 || historyIndex >= len(t.ExecutionHistory) {
				return fmt.Errorf("history entry no longer exists")
			}
			t.ExecutionHistory[historyIndex].Validation = validation
			workspace.MirrorTaskValidationResult(fresh.ID, t.ID, t.ExecutionHistory[historyIndex].RunID, validation)
			th.publishOutputContractReviewEvent(fresh.ID, t.ID, action, validation)
			return nil
		})
	})
}

// expectedColumnsFromValidation pulls the destination file's column header out
// of a recorded csv_header_mismatch error, so a reproject can target the exact
// columns the existing CSV expects.
func expectedColumnsFromValidation(validation *workspace.TaskValidationResult) []string {
	if validation == nil {
		return nil
	}
	for _, e := range validation.Errors {
		if strings.EqualFold(strings.TrimSpace(e.Code), "csv_header_mismatch") && len(e.Expected) > 0 {
			return append([]string(nil), e.Expected...)
		}
	}
	return nil
}

func recordTaskOutputReviewCSVHeaderMismatch(validation *workspace.TaskValidationResult, err error) bool {
	var mismatch *workspace.CSVHeaderMismatchError
	if !errors.As(err, &mismatch) {
		return false
	}
	if validation == nil {
		return false
	}
	validation.ValidationStatus = workspace.TaskValidationNeedsReview
	validation.StorageStatus = workspace.TaskStorageSkippedInvalid
	validation.Errors = append(validation.Errors, workspace.TaskValidationError{
		Code:     "csv_header_mismatch",
		Message:  mismatch.Error(),
		Expected: append([]string(nil), mismatch.Expected...),
		Actual:   append([]string(nil), mismatch.Actual...),
	})
	return true
}

func validateTaskOutputReviewApproval(task *workspace.Task, result string) (*workspace.TaskValidationResult, string) {
	if task != nil && task.OutputSpec != nil {
		return workspace.ValidateTaskOutputSpecResult(task, result)
	}
	return workspace.ValidateTaskOutputContractResult(task, result)
}

func taskOutputReviewValidationTask(task *workspace.Task, validation *workspace.TaskValidationResult) *workspace.Task {
	if task == nil {
		return nil
	}
	reviewTask := *task
	if validation != nil && validation.OutputSpec != nil {
		reviewTask.OutputSpec = workspace.SnapshotTaskOutputSpec(validation.OutputSpec)
		reviewTask.OutputSchema = nil
		reviewTask.OutputContract = nil
		if reviewTask.OutputSpec != nil {
			reviewTask.OutputSchema = reviewTask.OutputSpec.Schema
			reviewTask.OutputContract = reviewTask.OutputSpec.Contract
		}
	}
	return &reviewTask
}

func taskOutputReviewValidationTaskForEntry(task *workspace.Task, entry workspace.TaskExecution) *workspace.Task {
	reviewTask := taskOutputReviewValidationTask(task, entry.Validation)
	if reviewTask == nil {
		return nil
	}
	reviewTask.CurrentRunID = strings.TrimSpace(entry.RunID)
	reviewTask.ExecutionHistory = []workspace.TaskExecution{entry}
	if reviewTask.Context == nil {
		reviewTask.Context = map[string]any{}
	}
	return reviewTask
}

func resolveTaskReviewHistoryIndex(task *workspace.Task, requested *int) int {
	if task == nil || len(task.ExecutionHistory) == 0 {
		return -1
	}
	if requested != nil && *requested >= 0 && *requested < len(task.ExecutionHistory) {
		return *requested
	}
	for i := len(task.ExecutionHistory) - 1; i >= 0; i-- {
		validation := task.ExecutionHistory[i].Validation
		if validation != nil && validation.ValidationStatus == workspace.TaskValidationNeedsReview {
			return i
		}
	}
	return -1
}

func (th *TaskHandler) startTaskOutputReviewRerun(workspaceID, taskID string) error {
	if th.taskHandler == nil {
		return fmt.Errorf("task execution not available")
	}

	ws, err := th.workspaceStore.Get(workspaceID)
	if err != nil {
		return err
	}
	task, err := ws.GetTask(taskID)
	if err != nil {
		return err
	}
	if task.Status == workspace.TaskStatusInProgress {
		return fmt.Errorf("task is already in progress")
	}

	subtasks := ws.GetSubtasks(task.ID)
	if len(subtasks) > 0 {
		for _, subtask := range subtasks {
			if subtask.Status == workspace.TaskStatusInProgress {
				return fmt.Errorf("a subtask is already in progress")
			}
			if subtask.To == "" || subtask.To == "unassigned" {
				return fmt.Errorf("all subtasks must be assigned to an agent before execution")
			}
		}
		go th.executeParentTaskSequence(ws.ID, task.ID)
		return nil
	}

	if err := task.SetStatus(workspace.TaskStatusPending); err != nil {
		return err
	}
	workspace.ResetTaskRuntime(task)
	if err := ws.UpdateTask(*task); err != nil {
		return err
	}
	if err := th.workspaceStore.Save(ws); err != nil {
		return err
	}

	go func() {
		fresh, err := th.workspaceStore.Get(workspaceID)
		if err != nil {
			logger.Error("Failed to reload workspace for review re-run", logger.Fields{"workspace_id": workspaceID, "error": err})
			return
		}
		rerunTask, err := fresh.GetTask(taskID)
		if err != nil {
			logger.Error("Task not found for review re-run", logger.Fields{"task_id": taskID, "error": err})
			return
		}
		if _, err := th.executeTaskWithDependencies(fresh, rerunTask); err != nil {
			var blockedErr *workspace.TaskBlockedError
			if errors.As(err, &blockedErr) {
				return
			}
			logger.Error("Review task re-run failed", logger.Fields{"task_id": taskID, "error": err})
		}
	}()
	return nil
}

func appendApprovedTaskCSV(store workspace.Store, ws *workspace.Workspace, task *workspace.Task, csvData string) error {
	if ws == nil || task == nil {
		return fmt.Errorf("workspace and task are required")
	}
	storage := task.ResultStorage
	if storage == nil || !storage.Enabled {
		return fmt.Errorf("task result storage is not enabled")
	}
	if strings.ToLower(strings.TrimSpace(storage.WriteMode)) != "append" {
		return fmt.Errorf("manual approval append requires append-to-CSV storage")
	}
	if strings.TrimSpace(csvData) == "" {
		return fmt.Errorf("approved CSV is empty")
	}

	storeFilePath := storage.FilePath
	if strings.TrimSpace(storeFilePath) == "" {
		storeFilePath = workspace.AppendCSVFileName(task, storage)
	}

	if strings.TrimSpace(storage.StoreNodeID) != "" {
		var storeNode *workspace.StoreNode
		for i := range ws.StoreNodes {
			if ws.StoreNodes[i].ID == storage.StoreNodeID || ws.StoreNodes[i].CanvasNodeID == storage.StoreNodeID {
				storeNode = &ws.StoreNodes[i]
				break
			}
		}
		if storeNode == nil {
			return fmt.Errorf("store node %q not found", storage.StoreNodeID)
		}
		storeNodeCopy := *storeNode
		storeNodeCopy.WriteMode = "append"
		storeNodeCopy.Format = "csv"
		dataToStore := csvData
		strictData, err := workspace.CSVWithoutHeaderForExistingStoreStrict(&storeNodeCopy, storeFilePath, dataToStore)
		if err != nil {
			return err
		}
		dataToStore = strictData
		if err := workspace.WriteToStore(&storeNodeCopy, storeFilePath, dataToStore); err != nil {
			return err
		}
		storeNode.LastWriteTime = storeNodeCopy.LastWriteTime
		storeNode.WriteCount = storeNodeCopy.WriteCount
		storeNode.LastFilePath = storeNodeCopy.LastFilePath
		storeNode.LastError = storeNodeCopy.LastError
		storeNode.UpdatedAt = storeNodeCopy.UpdatedAt
		return nil
	}

	filePath := storage.FilePath
	if strings.TrimSpace(filePath) == "" {
		baseOutputDir := ""
		if store != nil {
			baseOutputDir = store.GetOutputsPath(ws.ID)
		}
		if baseOutputDir == "" {
			fallback, err := platform.GetDefaultOutputDir()
			if err != nil {
				fallback = "outputs"
			}
			baseOutputDir = filepath.Join(fallback, ws.Name)
		}
		filePath = filepath.Join(baseOutputDir, storeFilePath)
	} else if strings.HasSuffix(filePath, "/") || !strings.Contains(filepath.Base(filePath), ".") {
		filePath = filepath.Join(filePath, workspace.AppendCSVFileName(task, storage))
	}
	return workspace.AppendCSVToFileStrict(filePath, csvData)
}

// BulkDeleteTasksHandler handles DELETE /api/orchestration/tasks/bulk
// Deletes multiple tasks at once
func (th *TaskHandler) BulkDeleteTasksHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodDelete {
		orihttp.MethodNotAllowed(w)
		return
	}

	var req struct {
		TaskIDs     []string `json:"task_ids"`
		WorkspaceID string   `json:"workspace_id"` // Optional: if provided, only delete from this workspace
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if len(req.TaskIDs) == 0 {
		orihttp.BadRequest(w, "task_ids is required")
		return
	}

	successCount := 0
	failedCount := 0
	var errors []string

	// Group tasks by workspace for efficiency
	tasksByWorkspace := make(map[string][]string)
	for _, taskID := range req.TaskIDs {
		task, err := th.communicator.GetTask(taskID)
		if err != nil {
			failedCount++
			errors = append(errors, fmt.Sprintf("%s: task not found", taskID))
			continue
		}

		// If workspace_id is specified, verify task belongs to it
		if req.WorkspaceID != "" && task.WorkspaceID != req.WorkspaceID {
			failedCount++
			errors = append(errors, fmt.Sprintf("%s: task belongs to different workspace", taskID))
			continue
		}

		tasksByWorkspace[task.WorkspaceID] = append(tasksByWorkspace[task.WorkspaceID], taskID)
	}

	// Delete tasks from each workspace
	for workspaceID, taskIDs := range tasksByWorkspace {
		ws, err := th.workspaceStore.Get(workspaceID)
		if err != nil {
			for _, taskID := range taskIDs {
				failedCount++
				errors = append(errors, fmt.Sprintf("%s: workspace not found", taskID))
			}
			continue
		}

		for _, taskID := range taskIDs {
			if err := ws.DeleteTask(taskID); err != nil {
				failedCount++
				errors = append(errors, fmt.Sprintf("%s: %v", taskID, err))
				continue
			}
			successCount++
		}

		if err := th.workspaceStore.Save(ws); err != nil {
			logger.Error("Failed to save workspace after bulk delete", logger.Fields{"workspace_id": workspaceID, "error": err})
		}

		// Publish event for the workspace
		if th.eventBus != nil {
			th.eventBus.Publish(workspace.Event{
				Type:        workspace.EventWorkspaceUpdated,
				WorkspaceID: workspaceID,
				Data: map[string]any{
					"action":        "bulk_delete_tasks",
					"deleted_count": len(taskIDs),
				},
			})
		}
	}

	logger.Info("Bulk delete tasks completed", logger.Fields{
		"success_count": successCount,
		"failed_count":  failedCount,
	})

	orihttp.WriteJSON(w, map[string]any{
		"success":       true,
		"message":       "Bulk delete completed",
		"success_count": successCount,
		"failed_count":  failedCount,
		"errors":        errors,
	})
}

// FilePathsRequest represents a request to add/update file path references on a task
type FilePathsRequest struct {
	FilePaths []string `json:"file_paths"`
}

// handleFilePaths handles POST /api/orchestration/tasks/{id}/file-paths
// Adds file path references to a task (paths to local files, no upload)
func (th *TaskHandler) handleFilePaths(w http.ResponseWriter, r *http.Request) {
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

	var req FilePathsRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	// Validate file paths exist
	var validPaths []string
	var invalidPaths []string
	for _, p := range req.FilePaths {
		// Clean and validate path
		cleanPath := filepath.Clean(p)

		// Security: Reject paths with traversal sequences
		if strings.Contains(cleanPath, "..") {
			invalidPaths = append(invalidPaths, p)
			continue
		}

		if _, err := os.Stat(cleanPath); err != nil {
			invalidPaths = append(invalidPaths, p)
		} else {
			validPaths = append(validPaths, cleanPath)
		}
	}

	// Get task and workspace
	task, ws, err := th.getTaskWithWorkspace(taskID)
	if err != nil {
		orihttp.RespondErrorWithErr(w, http.StatusNotFound, "Task not found", err)
		return
	}

	// Find and update task
	for i := range ws.Tasks {
		if ws.Tasks[i].ID == taskID {
			if ws.Tasks[i].Context == nil {
				ws.Tasks[i].Context = map[string]any{}
			}

			// Get existing file paths and merge with new ones
			var existingPaths []string
			if existing, ok := ws.Tasks[i].Context["file_paths"].([]any); ok {
				for _, ep := range existing {
					if s, ok := ep.(string); ok {
						existingPaths = append(existingPaths, s)
					}
				}
			}

			// Add new valid paths (avoid duplicates)
			pathSet := make(map[string]bool)
			for _, p := range existingPaths {
				pathSet[p] = true
			}
			for _, p := range validPaths {
				if !pathSet[p] {
					existingPaths = append(existingPaths, p)
					pathSet[p] = true
				}
			}

			ws.Tasks[i].Context["file_paths"] = existingPaths
			break
		}
	}

	// Save workspace
	if err := th.workspaceStore.Save(ws); err != nil {
		logger.Error("Failed to save workspace", logger.Fields{"error": err})
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to save file paths", err)
		return
	}

	logger.Info("Added file paths to task", logger.Fields{
		"task_id":       taskID,
		"valid_count":   len(validPaths),
		"invalid_count": len(invalidPaths),
	})

	// Publish event
	if th.eventBus != nil {
		th.eventBus.Publish(workspace.Event{
			Type:        workspace.EventWorkspaceUpdated,
			WorkspaceID: task.WorkspaceID,
			Data: map[string]any{
				"task_id":    taskID,
				"file_paths": validPaths,
			},
		})
	}

	response := map[string]any{
		"success":     true,
		"task_id":     taskID,
		"valid_paths": validPaths,
	}

	if len(invalidPaths) > 0 {
		response["invalid_paths"] = invalidPaths
		response["warning"] = fmt.Sprintf("%d path(s) could not be verified", len(invalidPaths))
	}

	w.WriteHeader(http.StatusOK)
	orihttp.WriteJSON(w, response)
}
