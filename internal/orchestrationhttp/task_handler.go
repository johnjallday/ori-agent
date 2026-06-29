package orchestrationhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

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

// resolveCreateTaskProvenance picks the assignment provenance for a created task.
// The orchestration create endpoint serves both manual creates and coordinator-
// planned (auto-parse) creates, so it honors explicit, valid provenance from the
// request and otherwise defaults to manual.
func resolveCreateTaskProvenance(mode, assignedBy, reason string) (workspace.TaskAssignmentMode, string, string) {
	m := workspace.TaskAssignmentMode(strings.TrimSpace(mode))
	if workspace.IsValidTaskAssignmentMode(m) && m != workspace.TaskAssignmentModeLegacyUnknown {
		return m, strings.TrimSpace(assignedBy), strings.TrimSpace(reason)
	}
	return workspace.TaskAssignmentModeManual, workspace.TaskAssignedByManual, strings.TrimSpace(reason)
}

func (th *TaskHandler) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkspaceID            string                         `json:"workspace_id"`
		From                   string                         `json:"from"`
		To                     string                         `json:"to"`
		AssignedNodeID         string                         `json:"assigned_node_id"`
		AssignmentMode         string                         `json:"assignment_mode"`
		AssignedBy             string                         `json:"assigned_by"`
		AssignmentReason       string                         `json:"assignment_reason"`
		Description            string                         `json:"description"`
		Details                string                         `json:"details"`
		ReferenceURL           string                         `json:"reference_url"`
		Tags                   []string                       `json:"tags"`
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
	referenceURL, err := workspace.NormalizeReferenceURL(req.ReferenceURL)
	if err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}
	tags, err := workspace.ValidateWorkspaceTags(req.Tags)
	if err != nil {
		orihttp.BadRequest(w, err.Error())
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
		ReferenceURL:           referenceURL,
		Tags:                   tags,
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

	// Default an otherwise-unassigned task to the workspace coordinator (entry
	// agent) so created work is owned and routable instead of orphaned. No-op
	// when the request named an assignee or no coordinator can be resolved. Runs
	// before the scheduled-task validation below so a coordinator-defaulted
	// schedule is not rejected as unassigned (FR8), and before provenance
	// reconciliation so the entry_agent_default stamp it writes is preserved.
	defaultedToEntryAgent := ws.ApplyEntryAgentDefault(&task)

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

	// Record assignment provenance. This endpoint serves both manual creates and
	// coordinator-planned (auto-parse) creates, so honor explicit, valid
	// provenance from the request and default to manual otherwise. When the task
	// was defaulted to the entry agent above and the request carried no explicit
	// provenance, keep the entry_agent_default stamp instead of overwriting it
	// back to manual (FR9a).
	reqMode := workspace.TaskAssignmentMode(strings.TrimSpace(req.AssignmentMode))
	hasExplicitProvenance := workspace.IsValidTaskAssignmentMode(reqMode) && reqMode != workspace.TaskAssignmentModeLegacyUnknown
	if hasExplicitProvenance || !defaultedToEntryAgent {
		task.AssignmentMode, task.AssignedBy, task.AssignmentReason = resolveCreateTaskProvenance(
			req.AssignmentMode, req.AssignedBy, req.AssignmentReason)
	}

	// Pre-assign the ID so the created task can be located unambiguously after
	// save; matching on the request's To no longer works once defaulting may
	// have changed it (FR9).
	if task.ID == "" {
		task.ID = uuid.New().String()
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

	// Get the task we just added by its pre-assigned ID.
	createdTask, err := ws.GetTask(task.ID)
	if err != nil || createdTask == nil {
		logger.Error("Could not find created task", logger.Fields{"task_id": task.ID, "error": err})
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
	ReferenceURL           *string                        `json:"reference_url"`
	Tags                   *[]string                      `json:"tags"`
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
	return r.Description != nil || r.Details != nil || r.ReferenceURL != nil || r.Tags != nil || r.Priority != nil || r.Context != nil || r.InputTaskIDs != nil ||
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
	if req.ReferenceURL != nil {
		task.ReferenceURL = *req.ReferenceURL
		logger.Debug("Updated task reference URL", logger.Fields{"task_id": req.TaskID, "has_reference_url": task.ReferenceURL != ""})
	}
	if req.Tags != nil {
		// Lenient normalization (dedupe, lowercase, cap) — the strict
		// validation path lives on the workspace task endpoints; the shared
		// tag widget already enforces limits client-side.
		task.Tags = workspace.NormalizeWorkspaceTags(*req.Tags)
		logger.Debug("Updated task tags", logger.Fields{"task_id": req.TaskID, "tag_count": len(task.Tags)})
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
	if req.ReferenceURL != nil {
		eventData["reference_url"] = *req.ReferenceURL
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
	if req.ReferenceURL != nil {
		referenceURL, err := workspace.NormalizeReferenceURL(*req.ReferenceURL)
		if err != nil {
			orihttp.BadRequest(w, err.Error())
			return
		}
		req.ReferenceURL = &referenceURL
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

type taskOutputReviewRequest struct {
	Action        string   `json:"action"`
	HistoryIndex  *int     `json:"history_index"`
	Result        string   `json:"result,omitempty"`
	ApprovedBy    string   `json:"approved_by,omitempty"`
	TargetColumns []string `json:"target_columns,omitempty"`
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
