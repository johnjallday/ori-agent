package orchestrationhttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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

		if _, err := th.executeTaskWithDependencies(ws, task, true); err != nil {
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
		"success": true,
		"message": "Task execution started",
		"task_id": req.TaskID,
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
		BlockID string `json:"block_id"`
		Action  string `json:"action"`
		Agent   string `json:"agent"`
		Message string `json:"message"`
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

	history := make([]interface{}, 0, 4)
	if existingHistory, ok := humanLoop["history"].([]interface{}); ok {
		history = append(history, existingHistory...)
	}
	history = append(history, map[string]interface{}{
		"at":      time.Now().UTC().Format(time.RFC3339),
		"action":  action,
		"agent":   strings.TrimSpace(req.Agent),
		"message": strings.TrimSpace(req.Message),
	})

	humanLoop["block_id"] = blockID
	humanLoop["history"] = history
	humanLoop["updated_at"] = time.Now().UTC().Format(time.RFC3339)
	humanLoop["last_action"] = action

	if msg := strings.TrimSpace(req.Message); msg != "" {
		task.Context["user_assist_message"] = msg
	}

	switch action {
	case "mark_failed":
		now := time.Now()
		task.Status = workspace.TaskStatusFailed
		task.CompletedAt = &now
		task.Result = ""
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
		humanLoop["state"] = "resumed"
	case "retry", "continue_with_instruction":
		task.Status = workspace.TaskStatusPending
		task.StartedAt = nil
		task.CompletedAt = nil
		task.Error = ""
		task.Result = ""
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

		if _, err := th.executeTaskWithDependencies(ws, task, true); err != nil {
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
	var blockedErr *workspace.TaskBlockedError
	var blockedSubtaskID string

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
			if errors.As(err, &blockedErr) {
				blockedSubtaskID = subtask.ID
			}
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
		completedAt := time.Now()
		parentTask.CompletedAt = &completedAt
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

	result, execErr := th.executeTaskIteratively(ctx, ws, task, taskForExecution, manual)

	if execErr != nil {
		var blockedErr *workspace.TaskBlockedError
		if errors.As(execErr, &blockedErr) {
			if err := th.markTaskBlocked(ws, task, blockedErr, manual, nil); err != nil {
				return "", fmt.Errorf("failed to persist blocked task state: %w", err)
			}
			return "", blockedErr
		}

		completedAt := time.Now()
		task.CompletedAt = &completedAt
		logger.Error("Task failed", logger.Fields{"task_id": task.ID, "execErr": execErr})
		task.Status = workspace.TaskStatusFailed
		task.Error = execErr.Error()
	} else {
		completedAt := time.Now()
		task.CompletedAt = &completedAt
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

func (th *TaskHandler) markTaskBlocked(ws *workspace.Workspace, task *workspace.Task, blockedErr *workspace.TaskBlockedError, manual bool, extra map[string]interface{}) error {
	if ws == nil || task == nil {
		return fmt.Errorf("workspace and task are required")
	}

	now := time.Now()
	task.Status = workspace.TaskStatusPending
	task.CompletedAt = nil
	task.Error = ""
	task.Result = ""

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
		"state":       "blocked",
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
	}
	for key, value := range extra {
		humanLoop[key] = value
	}

	task.Context["human_loop"] = humanLoop
	return humanLoop
}

func (th *TaskHandler) executeTaskIteratively(ctx context.Context, ws *workspace.Workspace, persistedTask *workspace.Task, taskForExecution workspace.Task, manual bool) (string, error) {
	maxAttempts := resolveTaskExecutionAttempts(persistedTask)
	baseContext := cloneTaskContext(taskForExecution.Context)
	attemptHistory := make([]map[string]interface{}, 0, maxAttempts)

	for attempt := 1; attempt <= maxAttempts; attempt++ {
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

		result, execErr := th.taskHandler.ExecuteTask(ctx, currentTask.To, currentTask)
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
				RawResponse: result,
			}
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
	task.Context["execution_retry_guidance"] = fmt.Sprintf(
		"Previous attempt was '%s'. Continue autonomously with reasonable assumptions and provide a concrete best-effort result. Only ask for user confirmation if absolutely necessary.",
		previousOutcome,
	)
	if strings.TrimSpace(previousSummary) != "" {
		task.Context["execution_previous_summary"] = previousSummary
	}
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
	}
	for _, marker := range highConfidenceMarkers {
		if strings.Contains(normalized, marker) {
			return true
		}
	}

	softMarkers := []string{
		"could you clarify",
		"please clarify",
		"which location",
		"what specific",
		"how should i proceed",
		"what format",
		"i don't have direct access",
		"i do not have direct access",
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
