package orchestrationhttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const (
	defaultTaskExecutionAttempts = 3
	maxTaskExecutionAttempts     = 6
)

var (
	blockedEnumeratedChoicePattern = regexp.MustCompile(`(?i)^\s*(?:[-*]\s*)?(?:(\d+)[.)]|option\s+([a-z])[:.)-]?|([a-z])[.)])\s*(.+)$`)
	blockedQuestionPromptPattern   = regexp.MustCompile(`^\s*(\d+)[.)]\s*(.+?)\s*$`)
	blockedLetteredOptionPattern   = regexp.MustCompile(`^\s*(?:[-*]\s*)?([A-Z])[.)]\s*(.+)$`)
	blockedMarkdownLinkPattern     = regexp.MustCompile(`\[(.*?)\]\((.*?)\)`)
	blockedInlineChoicePatterns    = []*regexp.Regexp{
		regexp.MustCompile(`(?i)^(?:want me to|would you like me to|do you want me to|should i)\s+(.+?)(?:,\s*|\s+)or\s+(.+?)\?\s*$`),
	}
)

type clarificationQuestionBlock struct {
	Question string
	Options  []workspace.TaskBlockedFieldOption
}

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
		TaskID        string `json:"task_id"`
		ExecutionMode string `json:"execution_mode"`
		StepAction    string `json:"step_action"`
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if req.TaskID == "" {
		orihttp.BadRequest(w, "task_id is required")
		return
	}

	requestedMode := workspace.NormalizeTaskExecutionMode(req.ExecutionMode)
	stepAction := strings.ToLower(strings.TrimSpace(req.StepAction))

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
		workspace.ApplyTaskResultMetadata(foundTask, "")
		foundTask.Error = ""
		foundTask.StartedAt = nil
		foundTask.CompletedAt = nil
		workspace.ResetTaskExecutionSteps(foundTask)

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
		if !workspace.IsTaskAwaitingNextStep(foundTask) {
			orihttp.BadRequest(w, "Task is already in progress")
			return
		}
		if stepAction == "" {
			stepAction = "next"
		}
	}

	if strings.TrimSpace(req.ExecutionMode) != "" {
		foundTask.ExecutionMode = requestedMode
	} else if foundTask.ExecutionMode == "" {
		foundTask.ExecutionMode = workspace.TaskExecutionModeAuto
	}
	if err := foundWorkspace.UpdateTask(*foundTask); err != nil {
		logger.Error("Failed to update task execution mode", logger.Fields{"task_id": foundTask.ID, "error": err})
		orihttp.InternalError(w, "Failed to update task execution settings")
		return
	}
	if err := th.workspaceStore.Save(foundWorkspace); err != nil {
		logger.Error("Failed to save workspace", logger.Fields{"error": err})
		orihttp.InternalError(w, "Failed to save workspace")
		return
	}

	if th.taskHandler == nil {
		logger.Error("Task handler not set", logger.Fields{})
		orihttp.InternalError(w, "Task execution not available")
		return
	}

	// Execute the task immediately in a goroutine with a timeout
	// Default timeout is 30 minutes to prevent runaway tasks
	go func(workspaceID, taskID string) {
		ws, err := th.workspaceStore.Get(workspaceID)
		if err != nil {
			logger.Error("Failed to reload workspace for manual task execution", logger.Fields{"workspace_id": workspaceID, "error": err})
			return
		}

		task, err := ws.GetTask(taskID)
		if err != nil {
			logger.Error("Task not found for manual execution", logger.Fields{"task_id": taskID, "error": err})
			return
		}

		if _, err := th.executeTaskWithDependencies(ws, task); err != nil {
			var blockedErr *workspace.TaskBlockedError
			if errors.As(err, &blockedErr) {
				return
			}
			logger.Error("Manual task execution failed", logger.Fields{"task_id": taskID, "error": err})
		}
	}(foundWorkspace.ID, foundTask.ID)

	logger.Info("Started manual execution of task", logger.Fields{"task_id": req.TaskID})

	w.WriteHeader(http.StatusAccepted)
	orihttp.WriteJSON(w, map[string]interface{}{
		"success":        true,
		"message":        "Task execution started",
		"task_id":        req.TaskID,
		"execution_mode": foundTask.ExecutionMode,
		"step_action":    stepAction,
	})
}

func (th *TaskHandler) handleAssistTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/orchestration/tasks/"), "/")
	if len(pathParts) < 2 || strings.TrimSpace(pathParts[0]) == "" {
		orihttp.BadRequest(w, "task_id is required in URL path")
		return
	}
	taskID := strings.TrimSpace(pathParts[0])

	var req struct {
		BlockID      string                            `json:"block_id"`
		Action       string                            `json:"action"`
		Agent        string                            `json:"agent"`
		Message      string                            `json:"message"`
		ChoiceID     string                            `json:"choice_id"`
		ChoiceLabel  string                            `json:"choice_label"`
		ChoiceNumber string                            `json:"choice_number"`
		FieldValues  []workspace.TaskBlockedFieldValue `json:"field_values"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	action := strings.ToLower(strings.TrimSpace(req.Action))
	if action == "" {
		action = "continue_with_instruction"
	}

	task, ws, err := th.getTaskWithWorkspace(taskID)
	if err != nil {
		orihttp.RespondErrorWithErr(w, http.StatusNotFound, "Task not found", err)
		return
	}

	if task.Context == nil {
		task.Context = map[string]interface{}{}
	}
	humanLoop := map[string]interface{}{}
	if existing, ok := task.Context["human_loop"].(map[string]interface{}); ok {
		for key, value := range existing {
			humanLoop[key] = value
		}
	}

	blockID := strings.TrimSpace(req.BlockID)
	if blockID == "" {
		if existingID, ok := humanLoop["block_id"].(string); ok {
			blockID = strings.TrimSpace(existingID)
		}
	}
	if blockID == "" {
		blockID = fmt.Sprintf("blk_%d", time.Now().UnixNano())
	}

	selectedChoice := normalizeTaskAssistChoice(req.ChoiceID, req.ChoiceLabel, req.ChoiceNumber)
	fieldValues := normalizeTaskAssistFieldValues(req.FieldValues)
	history := make([]interface{}, 0, 4)
	if existingHistory, ok := humanLoop["history"].([]interface{}); ok {
		history = append(history, existingHistory...)
	}
	historyEntry := map[string]interface{}{
		"at":      time.Now().UTC().Format(time.RFC3339),
		"action":  action,
		"agent":   strings.TrimSpace(req.Agent),
		"message": strings.TrimSpace(req.Message),
	}
	if selectedChoice != nil {
		historyEntry["choice_id"] = selectedChoice.ID
		historyEntry["choice_label"] = selectedChoice.Label
		historyEntry["choice_number"] = selectedChoice.Number
	}
	if len(fieldValues) > 0 {
		historyEntry["field_values"] = fieldValues
	}
	history = append(history, historyEntry)

	humanLoop["block_id"] = blockID
	humanLoop["history"] = history
	humanLoop["updated_at"] = time.Now().UTC().Format(time.RFC3339)
	humanLoop["last_action"] = action

	if selectedChoice != nil {
		humanLoop["selected_choice"] = selectedChoice
		task.Context["user_assist_choice"] = selectedChoice
	} else {
		delete(humanLoop, "selected_choice")
		delete(task.Context, "user_assist_choice")
	}

	if len(fieldValues) > 0 {
		humanLoop["field_values"] = fieldValues
		task.Context["user_assist_fields"] = fieldValues
	} else {
		delete(humanLoop, "field_values")
		delete(task.Context, "user_assist_fields")
	}

	if msg := buildUserAssistMessage(strings.TrimSpace(req.Message), selectedChoice, fieldValues); msg != "" {
		task.Context["user_assist_message"] = msg
	} else {
		delete(task.Context, "user_assist_message")
	}

	switch action {
	case "mark_failed":
		now := time.Now()
		task.Status = workspace.TaskStatusFailed
		task.CompletedAt = &now
		task.Result = ""
		workspace.ApplyTaskResultMetadata(task, "")
		if msg := strings.TrimSpace(req.Message); msg != "" {
			task.Error = fmt.Sprintf("Marked as failed by user: %s", msg)
		} else {
			task.Error = "Marked as failed by user"
		}
		humanLoop["state"] = "failed"
	case "switch_agent_retry":
		agentName := strings.TrimSpace(req.Agent)
		if agentName == "" {
			orihttp.BadRequest(w, "agent is required for switch_agent_retry")
			return
		}
		task.To = agentName
		task.AssignedNodeID = resolveAssignedNodeID(ws, agentName)
		task.Status = workspace.TaskStatusPending
		task.StartedAt = nil
		task.CompletedAt = nil
		task.Error = ""
		task.Result = ""
		workspace.ApplyTaskResultMetadata(task, "")
		workspace.PrepareTaskExecutionStepsForResume(task)
		humanLoop["state"] = "resumed"
	case "retry", "continue_with_instruction":
		task.Status = workspace.TaskStatusPending
		task.StartedAt = nil
		task.CompletedAt = nil
		task.Error = ""
		task.Result = ""
		workspace.ApplyTaskResultMetadata(task, "")
		workspace.PrepareTaskExecutionStepsForResume(task)
		humanLoop["state"] = "resumed"
	default:
		orihttp.BadRequest(w, "unsupported action; use retry, continue_with_instruction, switch_agent_retry, or mark_failed")
		return
	}

	task.Context["human_loop"] = humanLoop
	if err := ws.UpdateTask(*task); err != nil {
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to update task", err)
		return
	}
	if err := th.workspaceStore.Save(ws); err != nil {
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to save workspace", err)
		return
	}

	if th.eventBus != nil {
		if action == "mark_failed" {
			th.eventBus.Publish(workspace.NewTaskEvent(workspace.EventTaskFailed, ws.ID, task.ID, task.To, map[string]interface{}{
				"description": task.Description,
				"error":       task.Error,
				"manual":      true,
			}))
		} else {
			th.eventBus.Publish(workspace.NewTaskEvent(workspace.EventTaskResumed, ws.ID, task.ID, task.To, map[string]interface{}{
				"description": task.Description,
				"action":      action,
				"block_id":    blockID,
				"message":     strings.TrimSpace(req.Message),
				"agent":       task.To,
				"manual":      true,
			}))
		}

		th.eventBus.Publish(workspace.NewWorkspaceEvent(workspace.EventWorkspaceUpdated, ws.ID, "task.assist", map[string]interface{}{
			"task_id": task.ID,
			"status":  task.Status,
			"action":  action,
		}))
	}

	if action != "mark_failed" {
		th.resumeTaskExecutionAsync(ws.ID, task.ID)
	}

	w.WriteHeader(http.StatusOK)
	orihttp.WriteJSON(w, map[string]interface{}{
		"success": true,
		"task_id": task.ID,
		"status":  task.Status,
		"action":  action,
	})
}

func resolveAssignedNodeID(ws *workspace.Workspace, agentName string) string {
	normalizedAgent := strings.TrimSpace(agentName)
	if ws == nil || normalizedAgent == "" {
		return ""
	}
	for _, instance := range ws.AgentInstances {
		if strings.EqualFold(strings.TrimSpace(instance.Name), normalizedAgent) && strings.TrimSpace(instance.NodeID) != "" {
			return instance.NodeID
		}
	}
	return ""
}

func (th *TaskHandler) resumeTaskExecutionAsync(workspaceID, taskID string) {
	go func() {
		ws, err := th.workspaceStore.Get(workspaceID)
		if err != nil {
			logger.Error("Failed to load workspace for task resume", logger.Fields{"workspace_id": workspaceID, "error": err})
			return
		}

		task, err := ws.GetTask(taskID)
		if err != nil {
			logger.Error("Failed to load task for resume", logger.Fields{"task_id": taskID, "error": err})
			return
		}

		subtasks := ws.GetSubtasks(task.ID)
		if len(subtasks) > 0 {
			th.executeParentTaskSequence(workspaceID, taskID)
			return
		}

		if _, err := th.executeTaskWithDependencies(ws, task); err != nil {
			var blockedErr *workspace.TaskBlockedError
			if errors.As(err, &blockedErr) {
				return
			}
			logger.Error("Task resume failed", logger.Fields{"task_id": taskID, "error": err})
		}
	}()
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

	subtasks = sortParentSubtasks(subtasks)

	startedAt := time.Now()
	parentTask.Status = workspace.TaskStatusInProgress
	parentTask.StartedAt = &startedAt
	parentTask.CompletedAt = nil
	parentTask.Result = ""
	workspace.ApplyTaskResultMetadata(parentTask, "")
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
	var blockedErr *workspace.TaskBlockedError
	var blockedSubtaskID string

	if shouldUseGraphParentExecution(parentTask, subtasks) {
		lastResult, blockedSubtaskID, execErr = th.executeParentTaskGraph(ws, subtasks)
		if execErr != nil {
			errors.As(execErr, &blockedErr)
		}
	} else {
		if err := th.prepareParentSubtasksForExecution(ws, subtasks); err != nil {
			execErr = err
		}

		for _, subtaskInfo := range subtasks {
			if execErr != nil {
				break
			}

			subtask, err := ws.GetTask(subtaskInfo.ID)
			if err != nil {
				execErr = fmt.Errorf("subtask %s not found", subtaskInfo.ID)
				break
			}

			result, err := th.executeTaskWithDependencies(ws, subtask)
			if err != nil {
				if errors.As(err, &blockedErr) {
					blockedSubtaskID = subtask.ID
				}
				execErr = err
				break
			}
			lastResult = result
		}
	}

	parentTask, err = ws.GetTask(parentTaskID)
	if err != nil {
		logger.Error("Failed to reload parent task after sequence", logger.Fields{"task_id": parentTaskID, "error": err})
		return
	}

	if execErr == nil {
		subtasks = sortParentSubtasks(ws.GetSubtasks(parentTaskID))
		aggregatedResult, aggErr := aggregateParentTaskResults(parentTask, subtasks, lastResult)
		if aggErr != nil {
			execErr = fmt.Errorf("aggregate parent task results: %w", aggErr)
		} else {
			lastResult = aggregatedResult
		}
	}

	if execErr != nil {
		if blockedErr != nil {
			extra := map[string]interface{}{}
			if blockedSubtaskID != "" {
				extra["blocked_subtask_id"] = blockedSubtaskID
			}
			if err := th.markTaskBlocked(ws, parentTask, blockedErr, true, extra); err != nil {
				logger.Error("Failed to persist blocked parent task", logger.Fields{"task_id": parentTaskID, "error": err})
			}
			return
		}

		completedAt := time.Now()
		parentTask.CompletedAt = &completedAt
		if errors.Is(execErr, context.Canceled) {
			logger.Info("Task sequence cancelled", logger.Fields{"task_id": parentTaskID})
			parentTask.Status = workspace.TaskStatusCancelled
			parentTask.Error = "Cancelled by user"
			parentTask.Result = ""
			workspace.ApplyTaskResultMetadata(parentTask, "")
			if th.eventBus != nil {
				th.eventBus.Publish(workspace.NewWorkspaceEvent(workspace.EventWorkspaceUpdated, ws.ID, "manual-sequence-cancelled", map[string]interface{}{
					"task_id": parentTask.ID,
					"status":  parentTask.Status,
				}))
			}
		} else {
			logger.Error("Task sequence failed", logger.Fields{"task_id": parentTaskID, "error": execErr})
			parentTask.Status = workspace.TaskStatusFailed
			parentTask.Error = execErr.Error()
			parentTask.Result = ""
			workspace.ApplyTaskResultMetadata(parentTask, "")

			if th.eventBus != nil {
				event := workspace.NewTaskEvent(workspace.EventTaskFailed, ws.ID, parentTask.ID, parentTask.To, map[string]interface{}{
					"description": parentTask.Description,
					"error":       execErr.Error(),
					"manual":      true,
				})
				th.eventBus.Publish(event)
			}
		}
	} else {
		completedAt := time.Now()
		parentTask.CompletedAt = &completedAt
		logger.Info("Task sequence completed successfully", logger.Fields{"task_id": parentTaskID})
		parentTask.Status = workspace.TaskStatusCompleted
		parentTask.Result = lastResult
		workspace.ApplyTaskResultMetadata(parentTask, lastResult)
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

func (th *TaskHandler) executeTaskWithDependencies(ws *workspace.Workspace, task *workspace.Task) (string, error) {
	const manual = true

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
	th.registerRunningTask(task.ID, cancel)
	defer th.unregisterRunningTask(task.ID)

	if task.Status != workspace.TaskStatusInProgress {
		workspace.PrepareTaskExecutionStepsForResume(task)
	}

	task.Status = workspace.TaskStatusInProgress
	now := time.Now()
	if task.StartedAt == nil || task.StartedAt.IsZero() {
		task.StartedAt = &now
	}
	task.Result = ""
	workspace.ApplyTaskResultMetadata(task, "")
	task.Error = ""
	if task.Context == nil {
		task.Context = map[string]interface{}{}
	}
	delete(task.Context, "human_loop")
	delete(task.Context, "structured_output")

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

	var result string
	var execErr error
	inferredExecutionSteps := workspace.InferTaskExecutionSteps(taskForExecution)
	if len(task.ExecutionSteps) > 0 && len(inferredExecutionSteps) == 0 {
		if cachedResult := strings.TrimSpace(workspace.BuildTaskExecutionSummary(task)); cachedResult != "" {
			workspace.SkipPendingExecutionSteps(task)
			result = cachedResult
		} else {
			workspace.ClearTaskExecutionSteps(task)
			result, execErr = th.executeTaskIteratively(ctx, ws, task, taskForExecution, manual)
		}
	} else if shouldUseStructuredExecution(task, taskForExecution) {
		result, execErr = th.executeTaskWithStructuredSteps(ctx, ws, task, taskForExecution, manual)
	} else {
		result, execErr = th.executeTaskIteratively(ctx, ws, task, taskForExecution, manual)
	}

	var awaitingErr *taskExecutionAwaitingStepError
	if errors.Is(ctx.Err(), context.Canceled) {
		completedAt := time.Now()
		task.CompletedAt = &completedAt
		task.Status = workspace.TaskStatusCancelled
		task.Error = "Cancelled by user"
		startedAt := completedAt
		if task.StartedAt != nil && !task.StartedAt.IsZero() {
			startedAt = *task.StartedAt
		}
		workspace.RecordTaskExecution(task, "cancelled", task.Error, startedAt, completedAt.Sub(startedAt))
		execErr = context.Canceled
	} else if errors.As(execErr, &awaitingErr) {
		if err := ws.UpdateTask(*task); err != nil {
			return awaitingErr.Result, fmt.Errorf("failed to update waiting task: %w", err)
		}
		if err := th.workspaceStore.Save(ws); err != nil {
			return awaitingErr.Result, fmt.Errorf("failed to save waiting task: %w", err)
		}
		return awaitingErr.Result, nil
	}

	if task.Status != workspace.TaskStatusCancelled {
		if execErr != nil {
			var blockedErr *workspace.TaskBlockedError
			if errors.As(execErr, &blockedErr) {
				if err := th.markTaskBlocked(ws, task, blockedErr, manual, buildStructuredExecutionExtra(task)); err != nil {
					return "", fmt.Errorf("failed to persist blocked task state: %w", err)
				}
				return "", blockedErr
			}

			completedAt := time.Now()
			task.CompletedAt = &completedAt
			logger.Error("Task failed", logger.Fields{"task_id": task.ID, "execErr": execErr})
			task.Status = workspace.TaskStatusFailed
			task.Error = execErr.Error()
			startedAt := completedAt
			if task.StartedAt != nil && !task.StartedAt.IsZero() {
				startedAt = *task.StartedAt
			}
			workspace.RecordTaskExecution(task, "failed", execErr.Error(), startedAt, completedAt.Sub(startedAt))
		} else {
			completedAt := time.Now()
			task.CompletedAt = &completedAt
			logger.Info("Task completed successfully", logger.Fields{"task_id": task.ID})
			task.Status = workspace.TaskStatusCompleted
			task.Result = result
			workspace.ApplyTaskResultMetadata(task, result)
			task.Error = ""
			startedAt := completedAt
			if task.StartedAt != nil && !task.StartedAt.IsZero() {
				startedAt = *task.StartedAt
			}
			workspace.RecordTaskExecution(task, "success", result, startedAt, completedAt.Sub(startedAt))

			workspace.AutoStoreResult(ws, task, result, th.workspaceStore)
		}
	}

	if err := ws.UpdateTask(*task); err != nil {
		return result, fmt.Errorf("failed to update task: %w", err)
	}
	if err := th.workspaceStore.Save(ws); err != nil {
		return result, fmt.Errorf("failed to save workspace: %w", err)
	}

	if th.eventBus != nil {
		if task.Status == workspace.TaskStatusCancelled {
			th.eventBus.Publish(workspace.NewWorkspaceEvent(workspace.EventWorkspaceUpdated, ws.ID, "manual-execution-cancelled", map[string]interface{}{
				"task_id": task.ID,
				"status":  task.Status,
			}))
		} else if execErr != nil {
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

		traceStartedAt := time.Now()
		if task.StartedAt != nil && !task.StartedAt.IsZero() {
			traceStartedAt = *task.StartedAt
		}
		traceCompletedAt := time.Now()
		if task.CompletedAt != nil && !task.CompletedAt.IsZero() {
			traceCompletedAt = *task.CompletedAt
		}
		workspace.RecordTaskExecutionTraceFromEventBus(task, th.eventBus, ws.ID, task.ID, traceStartedAt, traceCompletedAt)
		if len(task.ExecutionTrace) > 0 {
			if err := ws.UpdateTask(*task); err != nil {
				logger.Error("Failed to persist task execution trace", logger.Fields{"task_id": task.ID, "error": err})
			} else if err := th.workspaceStore.Save(ws); err != nil {
				logger.Error("Failed to save task execution trace", logger.Fields{"task_id": task.ID, "error": err})
			}
		}
	}

	return result, execErr
}

func (th *TaskHandler) markTaskBlocked(ws *workspace.Workspace, task *workspace.Task, blockedErr *workspace.TaskBlockedError, manual bool, extra map[string]interface{}) error {
	if ws == nil || task == nil {
		return fmt.Errorf("workspace and task are required")
	}

	now := time.Now()
	task.Status = workspace.TaskStatusWaitingForChoice
	task.CompletedAt = nil
	task.Error = ""
	task.Result = ""
	workspace.ApplyTaskResultMetadata(task, "")
	startedAt := now
	if task.StartedAt != nil && !task.StartedAt.IsZero() {
		startedAt = *task.StartedAt
	}
	workspace.RecordTaskExecution(task, "blocked", blockedExecutionSummary(blockedErr), startedAt, now.Sub(startedAt))

	humanLoop := buildTaskBlockedContext(task, blockedErr, extra)
	if err := ws.UpdateTask(*task); err != nil {
		return fmt.Errorf("failed to update blocked task: %w", err)
	}
	if err := th.workspaceStore.Save(ws); err != nil {
		return fmt.Errorf("failed to save blocked task: %w", err)
	}

	if th.eventBus != nil {
		payload := map[string]interface{}{
			"description": task.Description,
			"manual":      manual,
			"human_loop":  humanLoop,
			"status":      task.Status,
			"updated_at":  now.UTC().Format(time.RFC3339),
		}
		if blockedErr != nil && strings.TrimSpace(blockedErr.RawResponse) != "" {
			payload["agent_response"] = blockedErr.RawResponse
		}
		event := workspace.NewTaskEvent(workspace.EventTaskBlocked, ws.ID, task.ID, task.To, payload)
		th.eventBus.Publish(event)

		th.eventBus.Publish(workspace.NewWorkspaceEvent(workspace.EventWorkspaceUpdated, ws.ID, "manual-execution-blocked", map[string]interface{}{
			"task_id": task.ID,
			"status":  task.Status,
		}))

		workspace.RecordTaskExecutionTraceFromEventBus(task, th.eventBus, ws.ID, task.ID, startedAt, now)
		if len(task.ExecutionTrace) > 0 {
			if err := ws.UpdateTask(*task); err != nil {
				logger.Error("Failed to persist blocked task execution trace", logger.Fields{"task_id": task.ID, "error": err})
			} else if err := th.workspaceStore.Save(ws); err != nil {
				logger.Error("Failed to save blocked task execution trace", logger.Fields{"task_id": task.ID, "error": err})
			}
		}
	}

	logger.Warn("Task blocked awaiting user assistance", logger.Fields{
		"task_id":     task.ID,
		"reason_code": blockedErrReasonCode(blockedErr),
	})

	return nil
}

func blockedErrReasonCode(blockedErr *workspace.TaskBlockedError) string {
	if blockedErr == nil {
		return "blocked"
	}
	code := strings.TrimSpace(blockedErr.ReasonCode)
	if code == "" {
		return "blocked"
	}
	return code
}

func buildTaskBlockedContext(task *workspace.Task, blockedErr *workspace.TaskBlockedError, extra map[string]interface{}) map[string]interface{} {
	if task.Context == nil {
		task.Context = map[string]interface{}{}
	}

	blockID := fmt.Sprintf("blk_%d", time.Now().UnixNano())
	if existing, ok := task.Context["human_loop"].(map[string]interface{}); ok {
		if prior, ok := existing["block_id"].(string); ok && strings.TrimSpace(prior) != "" {
			blockID = strings.TrimSpace(prior)
		}
	}

	humanLoop := map[string]interface{}{
		"state":       "waiting_for_choice",
		"block_id":    blockID,
		"reason_code": blockedErrReasonCode(blockedErr),
		"updated_at":  time.Now().UTC().Format(time.RFC3339),
	}
	if blockedErr != nil {
		if reason := strings.TrimSpace(blockedErr.Reason); reason != "" {
			humanLoop["reason"] = reason
		}
		if question := strings.TrimSpace(blockedErr.Question); question != "" {
			humanLoop["question"] = question
		}
		if len(blockedErr.SuggestedActions) > 0 {
			humanLoop["suggested_actions"] = blockedErr.SuggestedActions
		}
		if raw := strings.TrimSpace(blockedErr.RawResponse); raw != "" {
			humanLoop["agent_response"] = raw
		}
		if workflowStep := workspace.PrepareTaskBlockedWorkflowStep(blockedErr.WorkflowStep, blockedErrReasonCode(blockedErr)); workflowStep != nil && (len(workflowStep.Choices) > 0 || len(workflowStep.Fields) > 0) {
			humanLoop["workflow_step"] = workflowStep
		}
	}
	for key, value := range extra {
		humanLoop[key] = value
	}

	task.Context["human_loop"] = humanLoop
	return humanLoop
}

func normalizeTaskAssistChoice(choiceID, choiceLabel, choiceNumber string) *workspace.TaskBlockedChoice {
	id := strings.TrimSpace(choiceID)
	label := cleanBlockedChoiceText(choiceLabel)
	number := strings.TrimSpace(choiceNumber)
	if id == "" && label == "" {
		return nil
	}
	if label == "" {
		label = id
	}
	if id == "" {
		id = buildBlockedChoiceID(number, label)
	}
	return &workspace.TaskBlockedChoice{
		ID:     id,
		Label:  label,
		Number: number,
	}
}

func normalizeTaskAssistFieldValues(values []workspace.TaskBlockedFieldValue) []workspace.TaskBlockedFieldValue {
	if len(values) == 0 {
		return nil
	}

	normalized := make([]workspace.TaskBlockedFieldValue, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, item := range values {
		id := strings.TrimSpace(item.ID)
		label := strings.TrimSpace(item.Label)
		value := strings.TrimSpace(item.Value)
		if id == "" && label == "" {
			continue
		}
		if value == "" {
			continue
		}
		if id == "" {
			id = fmt.Sprintf("field_%d", len(normalized)+1)
		}
		key := strings.ToLower(id)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		if label == "" {
			label = id
		}
		normalized = append(normalized, workspace.TaskBlockedFieldValue{
			ID:    id,
			Label: label,
			Value: value,
		})
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func buildUserAssistMessage(message string, selectedChoice *workspace.TaskBlockedChoice, fieldValues []workspace.TaskBlockedFieldValue) string {
	trimmedMessage := strings.TrimSpace(message)
	parts := make([]string, 0, 3)

	if selectedChoice != nil {
		selectedLabel := cleanBlockedChoiceText(selectedChoice.Label)
		if selectedLabel == "" {
			selectedLabel = strings.TrimSpace(selectedChoice.ID)
		}
		if selectedLabel != "" {
			parts = append(parts, fmt.Sprintf("Selected next step: %s.", selectedLabel))
		}
	}

	if len(fieldValues) > 0 {
		lines := make([]string, 0, len(fieldValues)+1)
		lines = append(lines, "Provided details:")
		for _, field := range fieldValues {
			label := cleanBlockedChoiceText(field.Label)
			if label == "" {
				label = strings.TrimSpace(field.ID)
			}
			value := strings.TrimSpace(field.Value)
			if label == "" || value == "" {
				continue
			}
			lines = append(lines, fmt.Sprintf("- %s: %s", label, value))
		}
		if len(lines) > 1 {
			parts = append(parts, strings.Join(lines, "\n"))
		}
	}

	if trimmedMessage != "" {
		prefix := "Additional guidance: "
		if len(parts) == 0 {
			prefix = ""
		}
		parts = append(parts, fmt.Sprintf("%s%s", prefix, trimmedMessage))
	}

	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func (th *TaskHandler) executeTaskIteratively(ctx context.Context, ws *workspace.Workspace, persistedTask *workspace.Task, taskForExecution workspace.Task, manual bool) (string, error) {
	maxAttempts := resolveTaskExecutionAttempts(persistedTask)
	baseContext := cloneTaskContext(taskForExecution.Context)
	attemptHistory := make([]map[string]interface{}, 0, maxAttempts)

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if errors.Is(ctx.Err(), context.Canceled) {
			return "", context.Canceled
		}

		currentTask := taskForExecution
		currentTask.Context = cloneTaskContext(baseContext)
		applyIterationContext(&currentTask, attempt, maxAttempts, attemptHistory)

		if attempt > 1 && th.eventBus != nil {
			th.eventBus.Publish(workspace.NewTaskEvent(workspace.EventTaskThinking, ws.ID, persistedTask.ID, persistedTask.To, map[string]interface{}{
				"message":      fmt.Sprintf("Retrying task autonomously (%d/%d)...", attempt, maxAttempts),
				"attempt":      attempt,
				"max_attempts": maxAttempts,
				"manual":       manual,
			}))
		}

		attemptStartedAt := time.Now().UTC()
		result, execErr := th.taskHandler.ExecuteTask(ctx, currentTask.To, currentTask)
		attemptCompletedAt := time.Now().UTC()
		if errors.Is(ctx.Err(), context.Canceled) {
			return "", context.Canceled
		}
		if execErr != nil {
			var blockedErr *workspace.TaskBlockedError
			if errors.As(execErr, &blockedErr) {
				attemptHistory = append(attemptHistory, map[string]interface{}{
					"attempt":    attempt,
					"outcome":    "blocked",
					"summary":    summarizeExecutionText(blockedErr.Error()),
					"created_at": time.Now().UTC().Format(time.RFC3339),
				})
				recordIterationHistory(persistedTask, maxAttempts, attemptHistory, "blocked")
				return "", blockedErr
			}

			attemptHistory = append(attemptHistory, map[string]interface{}{
				"attempt":    attempt,
				"outcome":    "error",
				"summary":    summarizeExecutionText(execErr.Error()),
				"created_at": time.Now().UTC().Format(time.RFC3339),
			})
			if attempt < maxAttempts {
				continue
			}

			recordIterationHistory(persistedTask, maxAttempts, attemptHistory, "retry_exhausted")
			return "", &workspace.TaskBlockedError{
				ReasonCode: "retry_exhausted",
				Reason:     fmt.Sprintf("Task failed after %d attempts", maxAttempts),
				Question:   "I could not complete this task autonomously. Should I retry, switch agents, or continue with your guidance?",
				SuggestedActions: []string{
					"continue_with_instruction",
					"retry",
					"switch_agent_retry",
					"mark_failed",
				},
				RawResponse: execErr.Error(),
			}
		}

		if responseNeedsUserInput(result) {
			attemptHistory = append(attemptHistory, map[string]interface{}{
				"attempt":    attempt,
				"outcome":    "needs_input",
				"summary":    summarizeExecutionText(result),
				"created_at": time.Now().UTC().Format(time.RFC3339),
			})
			if attempt < maxAttempts {
				continue
			}

			recordIterationHistory(persistedTask, maxAttempts, attemptHistory, "needs_user_confirmation")
			return "", &workspace.TaskBlockedError{
				ReasonCode: "needs_user_confirmation",
				Reason:     fmt.Sprintf("Task still needs confirmation after %d autonomous attempts", maxAttempts),
				Question:   extractClarificationQuestion(result),
				SuggestedActions: []string{
					"continue_with_instruction",
					"retry",
					"switch_agent_retry",
					"mark_failed",
				},
				RawResponse:  result,
				WorkflowStep: extractClarificationWorkflowStep(result),
			}
		}

		if blockedErr := classifyToolAccessBlockedResponse(result); blockedErr != nil {
			attemptHistory = append(attemptHistory, map[string]interface{}{
				"attempt":    attempt,
				"outcome":    "blocked",
				"summary":    summarizeExecutionText(result),
				"created_at": time.Now().UTC().Format(time.RFC3339),
			})
			recordIterationHistory(persistedTask, maxAttempts, attemptHistory, "blocked")
			return "", blockedErr
		}

		if blockedErr := classifyInvalidTaskCompletionResponse(currentTask, result); blockedErr != nil {
			attemptHistory = append(attemptHistory, map[string]interface{}{
				"attempt":    attempt,
				"outcome":    "invalid_result",
				"summary":    summarizeExecutionText(blockedErr.Reason),
				"created_at": time.Now().UTC().Format(time.RFC3339),
			})
			if attempt < maxAttempts {
				continue
			}

			recordIterationHistory(persistedTask, maxAttempts, attemptHistory, "invalid_result")
			return "", blockedErr
		}

		evidence := th.collectTaskExecutionEvidence(ws.ID, persistedTask.ID, attemptStartedAt, attemptCompletedAt)
		if blockedErr := classifyFilesystemListingVerificationFailure(currentTask, result, evidence); blockedErr != nil {
			attemptHistory = append(attemptHistory, map[string]interface{}{
				"attempt":    attempt,
				"outcome":    "unverified",
				"summary":    summarizeExecutionText(blockedErr.Reason),
				"created_at": time.Now().UTC().Format(time.RFC3339),
			})
			if attempt < maxAttempts {
				continue
			}

			recordIterationHistory(persistedTask, maxAttempts, attemptHistory, "blocked")
			return "", blockedErr
		}
		if blockedErr := classifyFilesystemListingIncompleteResponse(currentTask, result); blockedErr != nil {
			attemptHistory = append(attemptHistory, map[string]interface{}{
				"attempt":    attempt,
				"outcome":    "incomplete",
				"summary":    summarizeExecutionText(blockedErr.Reason),
				"created_at": time.Now().UTC().Format(time.RFC3339),
			})
			if attempt < maxAttempts {
				continue
			}

			recordIterationHistory(persistedTask, maxAttempts, attemptHistory, "blocked")
			return "", blockedErr
		}

		parsedStructuredOutput, validationErr := workspace.ValidateTaskStructuredOutput(persistedTask.OutputSchema, result)
		if validationErr != nil {
			if persistedTask.Context != nil {
				delete(persistedTask.Context, "structured_output")
			}
			attemptHistory = append(attemptHistory, map[string]interface{}{
				"attempt":    attempt,
				"outcome":    "invalid_structured_output",
				"summary":    summarizeExecutionText(validationErr.Error()),
				"created_at": time.Now().UTC().Format(time.RFC3339),
			})
			if attempt < maxAttempts {
				continue
			}

			recordIterationHistory(persistedTask, maxAttempts, attemptHistory, "structured_output_invalid")
			return "", &workspace.TaskBlockedError{
				ReasonCode: "structured_output_invalid",
				Reason:     fmt.Sprintf("Task did not return valid structured output after %d attempts", maxAttempts),
				Question:   "The task result did not match the required JSON schema. Should I retry, revise the schema, or continue with your guidance?",
				SuggestedActions: []string{
					"continue_with_instruction",
					"retry",
					"mark_failed",
				},
				RawResponse: result,
			}
		}
		if parsedStructuredOutput != nil {
			if persistedTask.Context == nil {
				persistedTask.Context = map[string]interface{}{}
			}
			persistedTask.Context["structured_output"] = parsedStructuredOutput
		} else if persistedTask.Context != nil {
			delete(persistedTask.Context, "structured_output")
		}

		attemptHistory = append(attemptHistory, map[string]interface{}{
			"attempt":    attempt,
			"outcome":    "success",
			"summary":    summarizeExecutionText(result),
			"created_at": time.Now().UTC().Format(time.RFC3339),
		})
		recordIterationHistory(persistedTask, maxAttempts, attemptHistory, "success")
		return result, nil
	}

	recordIterationHistory(persistedTask, maxAttempts, attemptHistory, "retry_exhausted")
	return "", &workspace.TaskBlockedError{
		ReasonCode: "retry_exhausted",
		Reason:     fmt.Sprintf("Task failed after %d attempts", maxAttempts),
		Question:   "I could not complete this task autonomously. Should I retry, switch agents, or continue with your guidance?",
		SuggestedActions: []string{
			"continue_with_instruction",
			"retry",
			"switch_agent_retry",
			"mark_failed",
		},
	}
}

func cloneTaskContext(src map[string]interface{}) map[string]interface{} {
	if len(src) == 0 {
		return map[string]interface{}{}
	}
	cloned := make(map[string]interface{}, len(src))
	for key, value := range src {
		cloned[key] = value
	}
	return cloned
}

func resolveTaskExecutionAttempts(task *workspace.Task) int {
	attempts := defaultTaskExecutionAttempts
	if task == nil || task.Context == nil {
		return attempts
	}

	keys := []string{"max_iterations", "max_attempts", "retry_attempts", "execution_max_attempts"}
	for _, key := range keys {
		raw, ok := task.Context[key]
		if !ok {
			continue
		}
		if parsed, ok := parsePositiveInt(raw); ok {
			attempts = parsed
			break
		}
	}

	if attempts < 1 {
		attempts = 1
	}
	if attempts > maxTaskExecutionAttempts {
		attempts = maxTaskExecutionAttempts
	}
	return attempts
}

func parsePositiveInt(raw interface{}) (int, bool) {
	switch value := raw.(type) {
	case int:
		if value > 0 {
			return value, true
		}
	case int8:
		if value > 0 {
			return int(value), true
		}
	case int16:
		if value > 0 {
			return int(value), true
		}
	case int32:
		if value > 0 {
			return int(value), true
		}
	case int64:
		if value > 0 {
			return int(value), true
		}
	case uint:
		if value > 0 {
			return int(value), true
		}
	case uint8:
		if value > 0 {
			return int(value), true
		}
	case uint16:
		if value > 0 {
			return int(value), true
		}
	case uint32:
		if value > 0 {
			return int(value), true
		}
	case uint64:
		if value > 0 {
			return int(value), true
		}
	case float64:
		if value > 0 {
			return int(value), true
		}
	case float32:
		if value > 0 {
			return int(value), true
		}
	case string:
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return 0, false
		}
		if parsed, err := strconv.Atoi(trimmed); err == nil && parsed > 0 {
			return parsed, true
		}
	}
	return 0, false
}

func applyIterationContext(task *workspace.Task, attempt, maxAttempts int, history []map[string]interface{}) {
	if task == nil {
		return
	}
	if task.Context == nil {
		task.Context = map[string]interface{}{}
	}

	task.Context["execution_attempt"] = attempt
	task.Context["execution_max_attempts"] = maxAttempts

	if len(history) == 0 {
		return
	}

	last := history[len(history)-1]
	previousOutcome, _ := last["outcome"].(string)
	previousSummary, _ := last["summary"].(string)

	task.Context["execution_previous_attempts"] = history
	task.Context["execution_retry_guidance"] = buildRetryGuidance(task, previousOutcome)
	if strings.TrimSpace(previousSummary) != "" {
		task.Context["execution_previous_summary"] = previousSummary
	}
}

func buildRetryGuidance(task *workspace.Task, previousOutcome string) string {
	trimmedOutcome := strings.TrimSpace(previousOutcome)
	if trimmedOutcome == "invalid_structured_output" {
		return "Previous attempt returned output that did not match the required JSON schema. Return ONLY a valid JSON object matching the required fields and types. Do not include markdown fences, commentary, or extra text before or after the JSON."
	}
	if task != nil && (trimmedOutcome == "unverified" || trimmedOutcome == "incomplete") && workspace.IsReadOnlyFilesystemListingIntent(task.Description) {
		task.Context["execution_require_filesystem_verification"] = true
		task.Context["execution_required_filesystem_tools"] = []string{
			"list_directory",
			"list_directory_with_sizes",
			"search_files",
			"get_file_info",
			"read_file",
		}
		if trimmedOutcome == "incomplete" {
			return "Previous attempt did not return the requested file list. The user already asked for the list, so do not ask a follow-up question or offer to provide it later. If you locate the named folder inside a parent directory, call a filesystem tool on that folder itself and return its contents directly. Use a filesystem tool to verify the folder contents if needed, then answer directly with the actual verified file list or state clearly that the folder is empty."
		}
		return "Previous attempt returned an unverified filesystem listing. You must use a filesystem tool to verify the folder contents before answering. If you locate the named folder inside a parent directory, call a filesystem tool on that folder itself instead of stopping at the parent listing. Do not answer from the workspace snapshot, prior summaries, or assumptions alone. Call a filesystem verification tool first, then return only the verified file list."
	}
	if task != nil && (trimmedOutcome == "invalid_result" || trimmedOutcome == "needs_input") && taskLooksForFreshPublicInformation(task.Description) {
		return "Previous attempt did not produce a valid final answer for this public-information task. Do not ask the user what to do next unless all reasonable public sources are truly blocked. Use web_search first instead of guessing direct source URLs. If search results are empty, broaden the query, remove site-specific filters, and try multiple public sources instead of stopping. Verify that any fetched source page matches the requested location before using it. If a source says no locations found or shows a different city/ZIP, discard it and search for another source. Do not return raw Tool Results; synthesize a concise answer with source names or URLs."
	}

	return fmt.Sprintf(
		"Previous attempt was '%s'. Continue autonomously with reasonable assumptions and provide a concrete best-effort result. Only ask for user confirmation if absolutely necessary.",
		trimmedOutcome,
	)
}

func recordIterationHistory(task *workspace.Task, maxAttempts int, history []map[string]interface{}, finalOutcome string) {
	if task == nil {
		return
	}
	if task.Context == nil {
		task.Context = map[string]interface{}{}
	}

	task.Context["execution_retry"] = map[string]interface{}{
		"max_attempts":  maxAttempts,
		"attempts_used": len(history),
		"final_outcome": strings.TrimSpace(finalOutcome),
		"updated_at":    time.Now().UTC().Format(time.RFC3339),
		"history":       history,
	}
}

func summarizeExecutionText(value string) string {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) <= 260 {
		return trimmed
	}
	return strings.TrimSpace(trimmed[:260]) + "..."
}

type taskExecutionEvidence struct {
	SuccessfulToolNames               []string
	SuccessfulFilesystemReadToolNames []string
}

func (th *TaskHandler) collectTaskExecutionEvidence(workspaceID, taskID string, startedAt, completedAt time.Time) taskExecutionEvidence {
	if th == nil || th.eventBus == nil || strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(taskID) == "" {
		return taskExecutionEvidence{}
	}

	events := th.eventBus.GetHistory(func(event workspace.Event) bool {
		if event.Type != workspace.EventTaskToolResult || event.WorkspaceID != workspaceID {
			return false
		}
		if !startedAt.IsZero() && event.Timestamp.Before(startedAt) {
			return false
		}
		if !completedAt.IsZero() && event.Timestamp.After(completedAt) {
			return false
		}
		return eventDataString(event.Data, "task_id") == taskID
	}, 256)

	evidence := taskExecutionEvidence{
		SuccessfulToolNames:               make([]string, 0, len(events)),
		SuccessfulFilesystemReadToolNames: make([]string, 0, len(events)),
	}

	seenTools := make(map[string]struct{}, len(events))
	seenFilesystemTools := make(map[string]struct{}, len(events))
	for _, event := range events {
		if !eventDataBool(event.Data, "success") {
			continue
		}

		toolName := strings.ToLower(strings.TrimSpace(eventDataString(event.Data, "tool_name")))
		if toolName == "" {
			continue
		}
		if _, ok := seenTools[toolName]; !ok {
			seenTools[toolName] = struct{}{}
			evidence.SuccessfulToolNames = append(evidence.SuccessfulToolNames, toolName)
		}
		if isFilesystemReadVerificationTool(toolName) {
			if _, ok := seenFilesystemTools[toolName]; !ok {
				seenFilesystemTools[toolName] = struct{}{}
				evidence.SuccessfulFilesystemReadToolNames = append(evidence.SuccessfulFilesystemReadToolNames, toolName)
			}
		}
	}

	sort.Strings(evidence.SuccessfulToolNames)
	sort.Strings(evidence.SuccessfulFilesystemReadToolNames)
	return evidence
}

func eventDataString(data map[string]interface{}, key string) string {
	if len(data) == 0 {
		return ""
	}
	value, ok := data[key]
	if !ok {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func eventDataBool(data map[string]interface{}, key string) bool {
	if len(data) == 0 {
		return false
	}
	value, ok := data[key]
	if !ok {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true")
	default:
		return false
	}
}

func isFilesystemReadVerificationTool(toolName string) bool {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "list_directory", "list_directory_with_sizes", "search_files", "get_file_info", "read_file":
		return true
	default:
		return false
	}
}

func classifyToolAccessBlockedResponse(result string) *workspace.TaskBlockedError {
	normalized := strings.ToLower(strings.TrimSpace(result))
	if normalized == "" {
		return nil
	}

	explicitMarkers := []string{
		"the available tools are limited to",
		"i don't have filesystem browsing tools available in this context",
		"i do not have filesystem browsing tools available in this context",
		"i don't have filesystem access",
		"i do not have filesystem access",
		"cannot explore a general directory",
		"can't explore a general directory",
		"appropriate file-reading tools configured",
		"filesystem access enabled",
		"may not be loaded or configured in the current agent context",
		"may need the appropriate file-reading tools configured",
		"blocked by robots.txt",
		"robots.txt / network restrictions",
		"network restrictions",
		"no html content is available",
		"provide raw html",
		"paste the html",
		"attach a snapshot",
		"alternative data source",
		"access remains blocked",
	}

	if containsAnyExecutionMarker(normalized, explicitMarkers) {
		return buildToolAccessBlockedError(result)
	}

	accessMarkers := []string{
		"i don't have access to",
		"i do not have access to",
		"i don't have",
		"i do not have",
		"i can't access",
		"i cannot access",
		"i'm unable to access",
		"i am unable to access",
	}
	toolMarkers := []string{
		"tool",
		"tools",
		"filesystem",
		"directory",
		"file-reading",
		"weather data",
		"real-time weather",
		"html content",
		"web page",
		"source page",
		"available in this context",
		"agent context",
	}
	unresolvedMarkers := []string{
		"i'd need you to either",
		"you'd need to either",
		"share the directory listing",
		"paste the output of",
		"to complete this task autonomously",
		"to walk you through",
		"neither provides",
		"neither of which can",
		"configured to complete this task",
		"loaded or configured",
		"provide raw html",
		"paste the html",
		"alternative data source",
		"fill in data later",
	}

	if containsAnyExecutionMarker(normalized, accessMarkers) &&
		containsAnyExecutionMarker(normalized, toolMarkers) &&
		containsAnyExecutionMarker(normalized, unresolvedMarkers) {
		return buildToolAccessBlockedError(result)
	}

	return nil
}

func containsAnyExecutionMarker(value string, markers []string) bool {
	for _, marker := range markers {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func buildToolAccessBlockedError(result string) *workspace.TaskBlockedError {
	return &workspace.TaskBlockedError{
		ReasonCode: "tool_access_unavailable",
		Reason:     "The assigned agent reported that required tools or external access were unavailable for this task.",
		Question:   "This task could not be completed with the tools currently available. Do you want to provide the missing context, retry after enabling the needed tools, or switch agents?",
		SuggestedActions: []string{
			"continue_with_instruction",
			"retry",
			"switch_agent_retry",
			"mark_failed",
		},
		RawResponse: strings.TrimSpace(result),
	}
}

func classifyInvalidTaskCompletionResponse(task workspace.Task, result string) *workspace.TaskBlockedError {
	trimmed := strings.TrimSpace(result)
	normalized := strings.ToLower(trimmed)
	if normalized == "" {
		return nil
	}

	if taskLooksForFreshPublicInformation(task.Description) && responseLooksLikeTaskStatusSummary(normalized) {
		return &workspace.TaskBlockedError{
			ReasonCode: "invalid_status_summary",
			Reason:     "The agent summarized the task status instead of answering the current public-information request.",
			Question:   "Retry this task with web search/source fallback, provide another source, or switch agents?",
			SuggestedActions: []string{
				"retry",
				"continue_with_instruction",
				"switch_agent_retry",
				"mark_failed",
			},
			RawResponse: trimmed,
		}
	}

	if taskLooksForFreshPublicInformation(task.Description) {
		if responseLooksLikeRawToolSummary(normalized) {
			if responseLooksLikeEmptyWebSearchToolSummary(normalized) {
				return &workspace.TaskBlockedError{
					ReasonCode: "empty_web_search_results",
					Reason:     "The web search returned no results and the agent did not broaden the search or synthesize an answer.",
					Question:   "Retry this task with a broader search across public sources?",
					SuggestedActions: []string{
						"retry",
						"continue_with_instruction",
						"switch_agent_retry",
						"mark_failed",
					},
					RawResponse: trimmed,
				}
			}
			return &workspace.TaskBlockedError{
				ReasonCode: "tool_only_result",
				Reason:     "The agent returned raw tool output instead of a final answer.",
				Question:   "Retry this task and require the agent to synthesize the tool result into an answer?",
				SuggestedActions: []string{
					"retry",
					"continue_with_instruction",
					"switch_agent_retry",
					"mark_failed",
				},
				RawResponse: trimmed,
			}
		}

		if reason := publicInfoLocationMismatchReason(task.Description, normalized); reason != "" {
			return &workspace.TaskBlockedError{
				ReasonCode: "location_mismatch",
				Reason:     reason,
				Question:   "Retry with web search first and only use sources that match the requested location?",
				SuggestedActions: []string{
					"retry",
					"continue_with_instruction",
					"switch_agent_retry",
					"mark_failed",
				},
				RawResponse: trimmed,
			}
		}
	}

	if taskAllowsPlaceholderOutput(task.Description) {
		return nil
	}

	if responseLooksLikePlaceholderResult(normalized) {
		return &workspace.TaskBlockedError{
			ReasonCode: "placeholder_result",
			Reason:     "The task returned a placeholder result instead of the requested answer.",
			Question:   "Retry this task, provide missing source content, or switch agents?",
			SuggestedActions: []string{
				"retry",
				"continue_with_instruction",
				"switch_agent_retry",
				"mark_failed",
			},
			RawResponse: trimmed,
		}
	}

	return nil
}

func taskLooksForFreshPublicInformation(description string) bool {
	normalized := strings.ToLower(strings.TrimSpace(description))
	if normalized == "" {
		return false
	}

	markers := []string{
		"today",
		"current",
		"latest",
		"recent",
		"now",
		"weather",
		"forecast",
		"pollen",
		"air quality",
		"price",
		"stock",
		"score",
		"news",
		"flight",
		"hotel",
	}
	return containsAnyExecutionMarker(normalized, markers)
}

func responseLooksLikeTaskStatusSummary(normalized string) bool {
	markers := []string{
		"current status for task",
		"status: in_progress",
		"status: completed",
		"what happened so far",
		"what you'll likely want next",
	}
	return containsAnyExecutionMarker(normalized, markers) &&
		(strings.Contains(normalized, "task ") || strings.Contains(normalized, "status:"))
}

func responseLooksLikeRawToolSummary(normalized string) bool {
	return strings.HasPrefix(strings.TrimSpace(normalized), "tool results:")
}

func responseLooksLikeEmptyWebSearchToolSummary(normalized string) bool {
	if !strings.Contains(normalized, "web_search") {
		return false
	}
	compacted := strings.NewReplacer(" ", "", "\n", "", "\t", "", "\r", "").Replace(normalized)
	return strings.Contains(compacted, `"results":[]`) ||
		strings.Contains(compacted, `"results":null`) ||
		strings.Contains(normalized, "no search results") ||
		strings.Contains(normalized, "no results found")
}

func publicInfoLocationMismatchReason(description, normalizedResult string) string {
	if strings.Contains(normalizedResult, "no locations found") {
		return "The source page did not resolve the requested location."
	}

	requestedZip := firstFiveDigitToken(description)
	resultZip := firstFiveDigitToken(normalizedResult)
	if requestedZip != "" && resultZip != "" && requestedZip != resultZip {
		return fmt.Sprintf("The source page used ZIP %s, but the task requested ZIP %s.", resultZip, requestedZip)
	}

	normalizedDescription := strings.ToLower(strings.TrimSpace(description))
	if requestsNYCLocation(normalizedDescription) && resultMentionsNonNYCLocation(normalizedResult) {
		return "The source page appears to be for Austin, TX, but the task requested NYC."
	}

	return ""
}

func firstFiveDigitToken(value string) string {
	re := regexp.MustCompile(`\b\d{5}\b`)
	return re.FindString(value)
}

func requestsNYCLocation(normalizedDescription string) bool {
	return strings.Contains(normalizedDescription, "nyc") ||
		strings.Contains(normalizedDescription, "new york city") ||
		strings.Contains(normalizedDescription, "new york, ny") ||
		strings.Contains(normalizedDescription, "new york")
}

func resultMentionsNonNYCLocation(normalizedResult string) bool {
	return strings.Contains(normalizedResult, "austin, tx") ||
		strings.Contains(normalizedResult, "austin tx") ||
		strings.Contains(normalizedResult, "/73344") ||
		strings.Contains(normalizedResult, "(73344)")
}

func taskAllowsPlaceholderOutput(description string) bool {
	normalized := strings.ToLower(strings.TrimSpace(description))
	if normalized == "" {
		return false
	}
	markers := []string{
		"placeholder",
		"template",
		"draft",
		"boilerplate",
		"tbd",
	}
	return containsAnyExecutionMarker(normalized, markers)
}

func responseLooksLikePlaceholderResult(normalized string) bool {
	if strings.Contains(normalized, "fill in data later") || strings.Contains(normalized, "fill in later") {
		return true
	}
	if strings.Contains(normalized, "placeholder") && (strings.Contains(normalized, "tbd") || strings.Contains(normalized, "...")) {
		return true
	}
	if strings.Contains(normalized, "|") && (strings.Contains(normalized, "| tbd") || strings.Contains(normalized, " tbd |") || strings.Contains(normalized, "| ...") || strings.Contains(normalized, " ... |")) {
		return true
	}
	return false
}

func classifyFilesystemListingVerificationFailure(task workspace.Task, result string, evidence taskExecutionEvidence) *workspace.TaskBlockedError {
	if !workspace.IsReadOnlyFilesystemListingIntent(task.Description) {
		return nil
	}
	if len(evidence.SuccessfulFilesystemReadToolNames) > 0 {
		return nil
	}

	return &workspace.TaskBlockedError{
		ReasonCode: "filesystem_result_unverified",
		Reason:     "Task returned a filesystem listing answer without successful filesystem verification",
		Question:   "I need to verify the folder contents with filesystem tools before completing this task. Retry with explicit filesystem verification?",
		SuggestedActions: []string{
			"retry",
			"switch_agent_retry",
			"continue_with_instruction",
			"mark_failed",
		},
		RawResponse: strings.TrimSpace(result),
	}
}

func classifyFilesystemListingIncompleteResponse(task workspace.Task, result string) *workspace.TaskBlockedError {
	if !workspace.IsReadOnlyFilesystemListingIntent(task.Description) {
		return nil
	}
	if filesystemListingAnswerLooksComplete(result) {
		return nil
	}

	return &workspace.TaskBlockedError{
		ReasonCode: "filesystem_listing_incomplete",
		Reason:     "Task did not return the requested filesystem file list",
		Question:   "I need to return the actual file list, not a follow-up offer. Retry and return the verified list directly?",
		SuggestedActions: []string{
			"retry",
			"switch_agent_retry",
			"continue_with_instruction",
			"mark_failed",
		},
		RawResponse: strings.TrimSpace(result),
	}
}

func filesystemListingAnswerLooksComplete(result string) bool {
	normalized := strings.ToLower(strings.TrimSpace(result))
	if normalized == "" {
		return false
	}

	emptyMarkers := []string{
		"folder is empty",
		"directory is empty",
		"contains no files",
		"no files found",
		"there are no files",
		"empty folder",
		"empty directory",
	}
	for _, marker := range emptyMarkers {
		if strings.Contains(normalized, marker) {
			return true
		}
	}

	return responseContainsFilenameLikeEntry(result)
}

func responseContainsFilenameLikeEntry(result string) bool {
	for _, line := range strings.Split(result, "\n") {
		trimmed := strings.TrimSpace(strings.TrimLeft(line, "-*0123456789.) \t"))
		if trimmed == "" {
			continue
		}
		for _, token := range strings.Fields(trimmed) {
			cleaned := strings.Trim(token, "\"'`,;:()[]{}")
			if looksLikeFilenameToken(cleaned) {
				return true
			}
		}
	}
	return false
}

func looksLikeFilenameToken(token string) bool {
	dot := strings.LastIndex(token, ".")
	if dot <= 0 || dot >= len(token)-1 {
		return false
	}

	ext := token[dot+1:]
	if len(ext) > 8 {
		return false
	}
	for _, r := range ext {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func blockedExecutionSummary(blockedErr *workspace.TaskBlockedError) string {
	if blockedErr == nil {
		return ""
	}
	if raw := strings.TrimSpace(blockedErr.RawResponse); raw != "" {
		return raw
	}
	if reason := strings.TrimSpace(blockedErr.Reason); reason != "" {
		if question := strings.TrimSpace(blockedErr.Question); question != "" {
			return reason + "\n\n" + question
		}
		return reason
	}
	return blockedErr.Error()
}

func responseNeedsUserInput(result string) bool {
	normalized := strings.ToLower(strings.TrimSpace(result))
	if normalized == "" {
		return true
	}

	highConfidenceMarkers := []string{
		"i need clarification",
		"need clarification to complete this task",
		"please provide these details",
		"before i can complete this task",
		"i need more information",
		"awaiting your input",
		"could you please confirm",
		"please confirm if",
		"provide additional directions",
		"recommended next steps",
		"choose one",
		"choose an option",
		"choose one of the following",
		"tell me which option",
		"which option to take",
		"just say",
		"what would you like me to do next",
		"how you'd like to proceed",
		"how you’d like to proceed",
		"like to proceed",
	}
	for _, marker := range highConfidenceMarkers {
		if strings.Contains(normalized, marker) {
			return true
		}
	}

	softMarkers := []string{
		"could you clarify",
		"please clarify",
		"please confirm",
		"which location",
		"what specific",
		"how should i proceed",
		"what format",
		"i don't have direct access",
		"i do not have direct access",
		"located somewhere else",
	}

	if strings.Contains(normalized, "option a") && strings.Contains(normalized, "option b") {
		return true
	}

	matches := 0
	for _, marker := range softMarkers {
		if strings.Contains(normalized, marker) {
			matches++
		}
	}

	questionMarks := strings.Count(result, "?")
	if matches >= 2 && questionMarks >= 1 {
		return true
	}

	if strings.Contains(normalized, "1.") &&
		strings.Contains(normalized, "2.") &&
		questionMarks >= 2 &&
		(matches >= 1 || strings.Contains(normalized, "however")) {
		return true
	}

	return false
}

func extractClarificationQuestion(result string) string {
	lines := strings.Split(result, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "?") && len(trimmed) <= 200 {
			return trimmed
		}
	}
	return "I still need confirmation to continue. Should I retry, switch agents, or proceed with your guidance?"
}

func extractClarificationWorkflowStep(result string) *workspace.TaskBlockedWorkflowStep {
	if step := extractQuestionBlockWorkflowStep(result); step != nil {
		return step
	}
	if step := extractEnumeratedClarificationWorkflowStep(result); step != nil {
		return step
	}
	if step := extractInlineClarificationWorkflowStep(result); step != nil {
		return step
	}
	if step := extractQuestionFormWorkflowStep(result); step != nil {
		return step
	}
	return nil
}

func extractQuestionBlockWorkflowStep(result string) *workspace.TaskBlockedWorkflowStep {
	blocks := extractClarificationQuestionBlocks(result)
	if len(blocks) == 0 {
		return nil
	}

	fields := make([]workspace.TaskBlockedField, 0, len(blocks))
	for index, block := range blocks {
		field, ok := buildClarificationField(block.Question, index)
		if !ok {
			continue
		}

		field.Type = "select"
		field.Options = block.Options
		field.Description = strings.TrimSpace(block.Question)
		field.Evidence = deriveClarificationQuestionEvidence(block.Options)
		fields = append(fields, field)
	}

	if len(fields) == 0 {
		return nil
	}

	return &workspace.TaskBlockedWorkflowStep{
		StepType:        "ask_form",
		Title:           "Provide the missing details",
		Summary:         "Answer the questions below so the task can continue.",
		Fields:          fields,
		FreeTextAllowed: true,
	}
}

func extractEnumeratedClarificationWorkflowStep(result string) *workspace.TaskBlockedWorkflowStep {
	lines := strings.Split(result, "\n")
	choices := make([]workspace.TaskBlockedChoice, 0, 4)
	started := false

	for _, line := range lines {
		match := blockedEnumeratedChoicePattern.FindStringSubmatch(line)
		if len(match) == 5 {
			number := strings.TrimSpace(firstNonEmptyString(match[1], match[2], match[3]))
			number = strings.ToUpper(number)
			label := cleanBlockedChoiceText(match[4])
			if label == "" {
				continue
			}
			choices = append(choices, workspace.TaskBlockedChoice{
				ID:     buildBlockedChoiceID(number, label),
				Label:  label,
				Number: number,
			})
			started = true
			if len(choices) >= 5 {
				break
			}
			continue
		}

		if !started {
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		break
	}

	if len(choices) < 2 {
		return nil
	}
	markRecommendedBlockedChoices(result, choices)

	return &workspace.TaskBlockedWorkflowStep{
		StepType:        "ask_choice",
		Title:           "Choose the next step",
		Summary:         "Pick one option below to continue this task.",
		Choices:         choices,
		FreeTextAllowed: true,
	}
}

func extractInlineClarificationWorkflowStep(result string) *workspace.TaskBlockedWorkflowStep {
	lines := strings.Split(result, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if !strings.Contains(line, "?") {
			continue
		}
		for _, pattern := range blockedInlineChoicePatterns {
			match := pattern.FindStringSubmatch(line)
			if len(match) != 3 {
				continue
			}

			first := cleanBlockedChoiceText(match[1])
			second := cleanBlockedChoiceText(match[2])
			if first == "" || second == "" || strings.EqualFold(first, second) {
				continue
			}

			return &workspace.TaskBlockedWorkflowStep{
				StepType: "ask_choice",
				Title:    "Choose the next step",
				Summary:  "Pick one option below to continue this task.",
				Choices: []workspace.TaskBlockedChoice{
					{
						ID:     buildBlockedChoiceID("1", first),
						Label:  first,
						Number: "1",
					},
					{
						ID:     buildBlockedChoiceID("2", second),
						Label:  second,
						Number: "2",
					},
				},
				FreeTextAllowed: true,
			}
		}
	}
	return nil
}

func markRecommendedBlockedChoices(result string, choices []workspace.TaskBlockedChoice) {
	recommendedNumbers := extractRecommendedChoiceNumbers(result)
	for index := range choices {
		label, labelRecommended := stripRecommendedChoiceMarker(choices[index].Label)
		if label != "" {
			choices[index].Label = label
		}
		number := strings.ToUpper(strings.TrimSpace(choices[index].Number))
		if labelRecommended || recommendedNumbers[number] {
			choices[index].Recommended = true
		}
	}
}

func extractRecommendedChoiceNumbers(result string) map[string]bool {
	recommended := map[string]bool{}
	lines := strings.Split(result, "\n")
	optionPattern := regexp.MustCompile(`(?i)\boption\s+([a-z0-9]+)\b|\b([a-z])[.)]\b`)
	for _, line := range lines {
		lower := strings.ToLower(line)
		if !strings.Contains(lower, "recommend") && !strings.Contains(lower, "by default") {
			continue
		}
		for _, match := range optionPattern.FindAllStringSubmatch(line, -1) {
			if len(match) < 3 {
				continue
			}
			number := strings.ToUpper(strings.TrimSpace(firstNonEmptyString(match[1], match[2])))
			if number != "" {
				recommended[number] = true
			}
		}
	}
	return recommended
}

func stripRecommendedChoiceMarker(label string) (string, bool) {
	cleaned := cleanBlockedChoiceText(label)
	if cleaned == "" {
		return "", false
	}
	recommendedPattern := regexp.MustCompile(`(?i)\s*[\[(]recommended[\])]\s*`)
	recommended := recommendedPattern.MatchString(cleaned)
	cleaned = recommendedPattern.ReplaceAllString(cleaned, " ")
	cleaned = cleanBlockedChoiceText(cleaned)
	return cleaned, recommended
}

func extractClarificationQuestionBlocks(result string) []clarificationQuestionBlock {
	lines := strings.Split(result, "\n")
	blocks := make([]clarificationQuestionBlock, 0, 4)

	for i := 0; i < len(lines); {
		match := blockedQuestionPromptPattern.FindStringSubmatch(lines[i])
		if len(match) != 3 {
			i++
			continue
		}

		question := cleanBlockedChoiceText(match[2])
		if question == "" {
			i++
			continue
		}

		options := make([]workspace.TaskBlockedFieldOption, 0, 4)
		j := i + 1
		for ; j < len(lines); j++ {
			rawLine := lines[j]
			if blockedQuestionPromptPattern.MatchString(rawLine) {
				break
			}

			optionMatch := blockedLetteredOptionPattern.FindStringSubmatch(rawLine)
			if len(optionMatch) == 3 {
				label := cleanBlockedChoiceText(optionMatch[2])
				if label == "" {
					continue
				}
				label, evidence := splitClarificationOptionEvidence(label)
				options = append(options, workspace.TaskBlockedFieldOption{
					Value:       label,
					Label:       label,
					Description: evidence,
				})
				continue
			}

			trimmed := strings.TrimSpace(rawLine)
			if trimmed == "" {
				continue
			}
			if len(options) == 0 {
				break
			}

			continuation := cleanBlockedChoiceText(trimmed)
			if continuation == "" {
				continue
			}
			lastIndex := len(options) - 1
			options[lastIndex].Label = strings.TrimSpace(options[lastIndex].Label + " " + continuation)
			options[lastIndex].Label, options[lastIndex].Description = splitClarificationOptionEvidence(options[lastIndex].Label)
			options[lastIndex].Value = options[lastIndex].Label
		}

		if len(options) >= 2 {
			if !strings.HasSuffix(question, "?") {
				question += "?"
			}
			blocks = append(blocks, clarificationQuestionBlock{
				Question: question,
				Options:  options,
			})
			i = j
			continue
		}

		i++
	}

	return blocks
}

func splitClarificationOptionEvidence(label string) (string, string) {
	cleaned := cleanBlockedChoiceText(label)
	if cleaned == "" {
		return "", ""
	}

	start := strings.LastIndex(cleaned, "(")
	end := strings.LastIndex(cleaned, ")")
	if start >= 0 && end > start+1 && end == len(cleaned)-1 {
		mainLabel := cleanBlockedChoiceText(cleaned[:start])
		evidence := cleanBlockedChoiceText(cleaned[start+1 : end])
		if mainLabel != "" && evidence != "" {
			return mainLabel, ensureClarificationSentence(evidence)
		}
	}

	return cleaned, ""
}

func deriveClarificationQuestionEvidence(options []workspace.TaskBlockedFieldOption) string {
	if len(options) == 0 {
		return ""
	}

	seen := make(map[string]struct{}, len(options))
	evidence := make([]string, 0, 2)
	for _, option := range options {
		description := strings.TrimSpace(option.Description)
		if description == "" {
			continue
		}
		key := strings.ToLower(description)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		evidence = append(evidence, ensureClarificationSentence(description))
		if len(evidence) >= 2 {
			break
		}
	}

	return strings.TrimSpace(strings.Join(evidence, " "))
}

func ensureClarificationSentence(value string) string {
	cleaned := cleanBlockedChoiceText(value)
	if cleaned == "" {
		return ""
	}
	if strings.HasSuffix(cleaned, ".") || strings.HasSuffix(cleaned, "!") || strings.HasSuffix(cleaned, "?") {
		return cleaned
	}
	return cleaned + "."
}

func extractQuestionFormWorkflowStep(result string) *workspace.TaskBlockedWorkflowStep {
	questions := extractClarificationQuestions(result)
	if len(questions) == 0 {
		return nil
	}

	fields := make([]workspace.TaskBlockedField, 0, len(questions))
	for index, question := range questions {
		field, ok := buildClarificationField(question, index)
		if !ok {
			continue
		}
		fields = append(fields, field)
	}

	if len(fields) == 0 {
		return nil
	}

	if len(fields) == 1 && !fieldHasExplicitOptions(fields[0]) {
		lowerQuestion := strings.ToLower(strings.TrimSpace(questions[0]))
		if lowerQuestion == "" ||
			strings.Contains(lowerQuestion, "how should i proceed") ||
			strings.Contains(lowerQuestion, "should i retry") {
			return nil
		}
	}

	return &workspace.TaskBlockedWorkflowStep{
		StepType:        "ask_form",
		Title:           "Provide the missing details",
		Summary:         "Answer the questions below so the task can continue.",
		Fields:          fields,
		FreeTextAllowed: true,
	}
}

func extractClarificationQuestions(result string) []string {
	normalized := strings.Join(strings.Fields(strings.TrimSpace(result)), " ")
	if normalized == "" {
		return nil
	}

	rawParts := strings.Split(normalized, "?")
	if len(rawParts) < 2 {
		return nil
	}

	questions := make([]string, 0, len(rawParts)-1)
	seen := make(map[string]struct{}, len(rawParts)-1)
	for _, part := range rawParts[:len(rawParts)-1] {
		question := strings.TrimSpace(strings.Trim(part, " \t\r\n-*"))
		if len(question) < 5 || len(question) > 220 {
			continue
		}

		key := strings.ToLower(question)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		questions = append(questions, question+"?")
	}

	return questions
}

func buildClarificationField(question string, index int) (workspace.TaskBlockedField, bool) {
	cleanedQuestion := strings.TrimSpace(strings.TrimSuffix(question, "?"))
	cleanedQuestion = cleanBlockedChoiceText(cleanedQuestion)
	if cleanedQuestion == "" {
		return workspace.TaskBlockedField{}, false
	}

	lower := strings.ToLower(cleanedQuestion)
	field := workspace.TaskBlockedField{
		ID:          fmt.Sprintf("field_%d", index+1),
		Label:       cleanedQuestion,
		Description: strings.TrimSpace(question),
		Type:        "text",
		Required:    true,
	}

	switch {
	case strings.Contains(lower, "freestanding") && (strings.Contains(lower, "wall-mounted") || strings.Contains(lower, "wall mounted")):
		field.ID = "mounting_type"
		field.Label = "Mounting type"
		field.Type = "select"
		field.Options = []workspace.TaskBlockedFieldOption{
			{Value: "freestanding", Label: "Freestanding"},
			{Value: "wall-mounted", Label: "Wall-mounted"},
		}
	case strings.Contains(lower, "specific room") || strings.Contains(lower, "which room") || strings.Contains(lower, "what room") || strings.Contains(lower, " room"):
		field.ID = "room"
		field.Label = "Room"
		field.Placeholder = extractClarificationPlaceholder(question, "Bathroom, kitchen, living room")
	case strings.Contains(lower, "tools"):
		field.ID = "available_tools"
		field.Label = "Available tools"
		field.Type = "textarea"
		field.Placeholder = extractClarificationPlaceholder(question, "Saw, drill, square")
	case strings.Contains(lower, "how many") && strings.Contains(lower, "shel"):
		field.ID = "shelf_count"
		field.Label = "Shelf count"
		field.Type = "number"
		field.Placeholder = "3"
	case strings.Contains(lower, "material"):
		field.ID = "material"
		field.Label = "Material"
		field.Placeholder = extractClarificationPlaceholder(question, "Plywood, pine, MDF")
	case strings.Contains(lower, "what's it holding") || strings.Contains(lower, "what is it holding") || strings.Contains(lower, "what will it hold"):
		field.ID = "intended_load"
		field.Label = "What it will hold"
		field.Placeholder = "Books, decor, kitchen items"
	default:
		if options := extractClarificationSelectOptions(cleanedQuestion); len(options) >= 2 {
			field.Type = "select"
			field.Options = options
			field.Label = "Select an option"
		}
	}

	return field, true
}

func extractClarificationPlaceholder(question, fallback string) string {
	start := strings.Index(question, "(")
	end := strings.Index(question, ")")
	if start >= 0 && end > start+1 {
		return strings.TrimSpace(question[start+1 : end])
	}
	return fallback
}

func extractClarificationSelectOptions(question string) []workspace.TaskBlockedFieldOption {
	if strings.Count(strings.ToLower(question), " or ") != 1 {
		return nil
	}

	lower := strings.ToLower(question)
	parts := strings.SplitN(lower, " or ", 2)
	if len(parts) != 2 {
		return nil
	}

	left := cleanBlockedChoiceText(parts[0])
	right := cleanBlockedChoiceText(parts[1])
	if left == "" || right == "" {
		return nil
	}
	if strings.Contains(left, "should i") || strings.Contains(left, "want me to") {
		return nil
	}

	return []workspace.TaskBlockedFieldOption{
		{Value: strings.ReplaceAll(left, " ", "_"), Label: left},
		{Value: strings.ReplaceAll(right, " ", "_"), Label: right},
	}
}

func fieldHasExplicitOptions(field workspace.TaskBlockedField) bool {
	return field.Type == "select" && len(field.Options) > 0
}

func cleanBlockedChoiceText(value string) string {
	cleaned := strings.TrimSpace(value)
	cleaned = blockedMarkdownLinkPattern.ReplaceAllString(cleaned, "$1")
	cleaned = strings.NewReplacer("**", "", "__", "", "`", "", "*", "", "_", "", "#", "", ">", "").Replace(cleaned)
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	cleaned = strings.Trim(cleaned, " \t\r\n,;:.!?")
	return cleaned
}

func buildBlockedChoiceID(number, label string) string {
	normalized := normalizeBlockedChoiceToken(label)
	if normalized == "" {
		normalized = "choice"
	}
	if trimmedNumber := strings.TrimSpace(number); trimmedNumber != "" {
		return fmt.Sprintf("choice-%s-%s", trimmedNumber, normalized)
	}
	return "choice-" + normalized
}

func normalizeBlockedChoiceToken(value string) string {
	cleaned := strings.ToLower(cleanBlockedChoiceText(value))
	var builder strings.Builder
	lastHyphen := false
	for _, r := range cleaned {
		isAlphaNum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if isAlphaNum {
			builder.WriteRune(r)
			lastHyphen = false
			continue
		}
		if builder.Len() == 0 || lastHyphen {
			continue
		}
		builder.WriteRune('-')
		lastHyphen = true
	}
	return strings.Trim(builder.String(), "-")
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

		// Execute the task (iterative best-effort)
		logger.Debug("Executing input task", logger.Fields{"input_task_id": inputTaskID, "agent": inputTask.To})
		result, err := th.executeTaskIteratively(ctx, ws, inputTask, *inputTask, false)

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
			workspace.ApplyTaskResultMetadata(inputTask, result)
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
