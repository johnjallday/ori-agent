package orchestrationhttp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type taskResultPreviewResponse struct {
	Success      bool                      `json:"success"`
	SourceTaskID string                    `json:"source_task_id"`
	WorkspaceID  string                    `json:"workspace_id"`
	ResultType   workspace.TaskResultType  `json:"result_type"`
	TaskList     *workspace.TaskListResult `json:"task_list,omitempty"`
	ItemCount    int                       `json:"item_count,omitempty"`
}

type promoteTaskResultRequest struct {
	TaskList *workspace.TaskListResult `json:"task_list,omitempty"`
}

type promoteTaskResultResponse struct {
	Success      bool                      `json:"success"`
	SourceTaskID string                    `json:"source_task_id"`
	WorkspaceID  string                    `json:"workspace_id"`
	ResultType   workspace.TaskResultType  `json:"result_type"`
	ParentTask   *workspace.Task           `json:"parent_task"`
	Subtasks     []workspace.Task          `json:"subtasks"`
	TaskList     *workspace.TaskListResult `json:"task_list"`
}

func (th *TaskHandler) handlePreviewTaskResult(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	taskID, err := taskIDFromResultActionPath(r.URL.Path, "result/preview")
	if err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}

	task, ws, err := th.getTaskWithWorkspace(taskID)
	if err != nil {
		orihttp.RespondErrorWithErr(w, http.StatusNotFound, "Task not found", err)
		return
	}

	taskList, err := workspace.TaskListResultFromTask(*task)
	if err != nil {
		orihttp.RespondErrorWithErr(w, http.StatusUnprocessableEntity, "Task result is not a promotable task list", err)
		return
	}

	orihttp.WriteJSON(w, taskResultPreviewResponse{
		Success:      true,
		SourceTaskID: task.ID,
		WorkspaceID:  ws.ID,
		ResultType:   workspace.TaskResultTypeTaskList,
		TaskList:     taskList,
		ItemCount:    workspace.CountTaskListResultItems(taskList),
	})
}

func (th *TaskHandler) handlePromoteTaskResult(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	taskID, err := taskIDFromResultActionPath(r.URL.Path, "promote-result")
	if err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}

	sourceTask, ws, err := th.getTaskWithWorkspace(taskID)
	if err != nil {
		orihttp.RespondErrorWithErr(w, http.StatusNotFound, "Task not found", err)
		return
	}

	req, err := parsePromoteTaskResultRequest(r)
	if err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}

	taskList := req.TaskList
	if taskList == nil {
		taskList, err = workspace.TaskListResultFromTask(*sourceTask)
		if err != nil {
			orihttp.RespondErrorWithErr(w, http.StatusUnprocessableEntity, "Task result is not a promotable task list", err)
			return
		}
	}
	if err := workspace.ValidateTaskListResult(taskList); err != nil {
		orihttp.RespondErrorWithErr(w, http.StatusUnprocessableEntity, "Task list draft is invalid", err)
		return
	}

	parentTask, subtasks, err := th.createTasksFromTaskListResult(ws, sourceTask, taskList)
	if err != nil {
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to promote task result", err)
		return
	}
	if err := th.workspaceStore.Save(ws); err != nil {
		logger.Error("Failed to save promoted task result", logger.Fields{"workspace_id": ws.ID, "source_task_id": sourceTask.ID, "error": err})
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to save promoted task result", err)
		return
	}

	th.publishTaskResultPromotionEvents(ws.ID, sourceTask.ID, parentTask, subtasks)

	orihttp.WriteJSON(w, promoteTaskResultResponse{
		Success:      true,
		SourceTaskID: sourceTask.ID,
		WorkspaceID:  ws.ID,
		ResultType:   workspace.TaskResultTypeTaskList,
		ParentTask:   parentTask,
		Subtasks:     subtasks,
		TaskList:     taskList,
	})
}

func taskIDFromResultActionPath(path, action string) (string, error) {
	trimmed := strings.TrimPrefix(path, "/api/orchestration/tasks/")
	suffix := "/" + strings.Trim(action, "/")
	if !strings.HasSuffix(trimmed, suffix) {
		return "", fmt.Errorf("invalid task result action path")
	}
	taskID := strings.TrimSuffix(trimmed, suffix)
	taskID = strings.Trim(taskID, "/")
	if taskID == "" {
		return "", fmt.Errorf("task_id is required in URL path")
	}
	return taskID, nil
}

func parsePromoteTaskResultRequest(r *http.Request) (promoteTaskResultRequest, error) {
	var req promoteTaskResultRequest
	if r == nil || r.Body == nil {
		return req, nil
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return req, fmt.Errorf("read request body: %w", err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return req, nil
	}
	if err := json.Unmarshal(data, &req); err != nil {
		return req, fmt.Errorf("invalid request body: %w", err)
	}
	return req, nil
}

func (th *TaskHandler) createTasksFromTaskListResult(ws *workspace.Workspace, sourceTask *workspace.Task, taskList *workspace.TaskListResult) (*workspace.Task, []workspace.Task, error) {
	if ws == nil || sourceTask == nil {
		return nil, nil, fmt.Errorf("workspace and source task are required")
	}
	parent := workspace.Task{
		WorkspaceID:           ws.ID,
		From:                  "system",
		To:                    sourceTask.To,
		AssignedNodeID:        sourceTask.AssignedNodeID,
		Description:           strings.TrimSpace(taskList.ParentTitle),
		Details:               buildPromotedParentDetails(sourceTask, taskList),
		Priority:              sourceTask.Priority,
		Status:                workspace.TaskStatusPending,
		OrchestrationMode:     workspace.TaskOrchestrationModeSequential,
		ResultCombinationMode: workspace.TaskResultCombinationLastResult,
		Context: map[string]interface{}{
			"promoted_from_task_id":     sourceTask.ID,
			"promoted_from_result_type": string(workspace.TaskResultTypeTaskList),
		},
	}
	if parent.Priority <= 0 {
		parent.Priority = 1
	}
	if err := ensurePromotedTaskAssignee(ws, parent.To); err != nil {
		return nil, nil, err
	}
	if err := ws.AddTask(parent); err != nil {
		return nil, nil, fmt.Errorf("add parent task: %w", err)
	}
	createdParent := ws.Tasks[len(ws.Tasks)-1]

	subtasks := make([]workspace.Task, 0, workspace.CountTaskListResultItems(taskList))
	if shouldPromoteTaskListWithGroupTasks(taskList) {
		groupIndex := 1
		for _, group := range taskList.Groups {
			groupTask := workspace.Task{
				WorkspaceID:           ws.ID,
				From:                  "system",
				To:                    sourceTask.To,
				AssignedNodeID:        sourceTask.AssignedNodeID,
				Description:           formatPromotedGroupTaskTitle(group.Title, groupIndex),
				Details:               buildPromotedGroupDetails(sourceTask, group.Title),
				Priority:              parent.Priority,
				Status:                workspace.TaskStatusPending,
				ParentTaskID:          createdParent.ID,
				SubtaskIndex:          groupIndex,
				OrchestrationMode:     workspace.TaskOrchestrationModeSequential,
				ResultCombinationMode: workspace.TaskResultCombinationLastResult,
				Context: map[string]interface{}{
					"promoted_from_task_id":     sourceTask.ID,
					"promoted_from_result_type": string(workspace.TaskResultTypeTaskList),
					"promoted_group_title":      strings.TrimSpace(group.Title),
					"promoted_group_index":      groupIndex,
					"promoted_group_task":       true,
				},
			}
			if err := ws.AddTask(groupTask); err != nil {
				return nil, nil, fmt.Errorf("add group task %d: %w", groupIndex, err)
			}
			createdGroupTask := ws.Tasks[len(ws.Tasks)-1]
			subtasks = append(subtasks, createdGroupTask)

			itemIndex := 1
			for _, item := range group.Items {
				createdSubtask, err := addPromotedLeafSubtask(ws, sourceTask, createdGroupTask.ID, itemIndex, group.Title, item)
				if err != nil {
					return nil, nil, err
				}
				subtasks = append(subtasks, createdSubtask)
				itemIndex++
			}
			groupIndex++
		}

		return &createdParent, subtasks, nil
	}

	subtaskIndex := 1
	for _, group := range taskList.Groups {
		for _, item := range group.Items {
			createdSubtask, err := addPromotedLeafSubtask(ws, sourceTask, createdParent.ID, subtaskIndex, group.Title, item)
			if err != nil {
				return nil, nil, err
			}
			subtasks = append(subtasks, createdSubtask)
			subtaskIndex++
		}
	}

	return &createdParent, subtasks, nil
}

func shouldPromoteTaskListWithGroupTasks(taskList *workspace.TaskListResult) bool {
	if taskList == nil {
		return false
	}
	namedGroups := 0
	for _, group := range taskList.Groups {
		if len(group.Items) == 0 || isGenericTaskListGroupTitle(group.Title) {
			continue
		}
		namedGroups++
	}
	return namedGroups > 1
}

func isGenericTaskListGroupTitle(title string) bool {
	normalized := strings.ToLower(strings.TrimSpace(title))
	return normalized == "" || normalized == "tasks" || normalized == "task list"
}

func formatPromotedGroupTaskTitle(title string, index int) string {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Tasks"
	}
	if index <= 0 {
		return title
	}
	prefix := fmt.Sprintf("%d.0 ", index)
	if strings.HasPrefix(title, prefix) {
		return title
	}
	return prefix + title
}

func addPromotedLeafSubtask(ws *workspace.Workspace, sourceTask *workspace.Task, parentTaskID string, subtaskIndex int, groupTitle string, item workspace.TaskListResultItem) (workspace.Task, error) {
	assignee := strings.TrimSpace(item.Assignee)
	assignedNodeID := ""
	if assignee == "" {
		assignee = sourceTask.To
		assignedNodeID = sourceTask.AssignedNodeID
	}
	if err := ensurePromotedTaskAssignee(ws, assignee); err != nil {
		return workspace.Task{}, err
	}
	priority := item.Priority
	if priority <= 0 {
		priority = sourceTask.Priority
	}
	if priority <= 0 {
		priority = 1
	}
	subtask := workspace.Task{
		WorkspaceID:    ws.ID,
		From:           "system",
		To:             assignee,
		AssignedNodeID: assignedNodeID,
		Description:    strings.TrimSpace(item.Title),
		Details:        buildPromotedSubtaskDetails(sourceTask, groupTitle, item),
		Priority:       priority,
		Status:         workspace.TaskStatusPending,
		ParentTaskID:   parentTaskID,
		SubtaskIndex:   subtaskIndex,
		Context: map[string]interface{}{
			"promoted_from_task_id":     sourceTask.ID,
			"promoted_from_result_type": string(workspace.TaskResultTypeTaskList),
			"promoted_group_title":      strings.TrimSpace(groupTitle),
		},
	}
	if err := ws.AddTask(subtask); err != nil {
		return workspace.Task{}, fmt.Errorf("add subtask %d: %w", subtaskIndex, err)
	}
	return ws.Tasks[len(ws.Tasks)-1], nil
}

func ensurePromotedTaskAssignee(ws *workspace.Workspace, assignee string) error {
	assignee = strings.TrimSpace(assignee)
	if ws == nil || assignee == "" || assignee == "unassigned" {
		return nil
	}
	if ws.HasAgent(assignee) {
		return nil
	}
	if err := ws.AddAgent(assignee); err != nil {
		return fmt.Errorf("add promoted task assignee %q: %w", assignee, err)
	}
	return nil
}

func buildPromotedParentDetails(sourceTask *workspace.Task, taskList *workspace.TaskListResult) string {
	parts := []string{
		fmt.Sprintf("Created from task result: %s", sourceTask.ID),
	}
	if sourceTask.Description != "" {
		parts = append(parts, fmt.Sprintf("Source task: %s", strings.TrimSpace(sourceTask.Description)))
	}
	if details := strings.TrimSpace(taskList.ParentDetails); details != "" {
		parts = append(parts, "", details)
	}
	parts = append(parts, "", "Use the subtasks below as the executable workflow.")
	return strings.Join(parts, "\n")
}

func buildPromotedGroupDetails(sourceTask *workspace.Task, groupTitle string) string {
	parts := []string{
		fmt.Sprintf("Created from task result: %s", sourceTask.ID),
	}
	if group := strings.TrimSpace(groupTitle); group != "" {
		parts = append(parts, fmt.Sprintf("Section: %s", group))
	}
	parts = append(parts, "", "Use the subtasks below as the executable work for this section.")
	return strings.Join(parts, "\n")
}

func buildPromotedSubtaskDetails(sourceTask *workspace.Task, groupTitle string, item workspace.TaskListResultItem) string {
	parts := []string{
		fmt.Sprintf("Created from task result: %s", sourceTask.ID),
	}
	if group := strings.TrimSpace(groupTitle); group != "" {
		parts = append(parts, fmt.Sprintf("Group: %s", group))
	}
	if details := strings.TrimSpace(item.Details); details != "" {
		parts = append(parts, "", details)
	}
	return strings.Join(parts, "\n")
}

func (th *TaskHandler) publishTaskResultPromotionEvents(workspaceID, sourceTaskID string, parentTask *workspace.Task, subtasks []workspace.Task) {
	if th == nil || th.eventBus == nil || parentTask == nil {
		return
	}
	th.eventBus.Publish(workspace.NewWorkspaceEvent(workspace.EventWorkspaceUpdated, workspaceID, "task.result.promote", map[string]interface{}{
		"source_task_id": sourceTaskID,
		"parent_task_id": parentTask.ID,
		"subtask_count":  len(subtasks),
	}))
	th.eventBus.Publish(workspace.Event{
		Type:        workspace.EventTaskCreated,
		WorkspaceID: workspaceID,
		Source:      "task.result.promote",
		Data: map[string]interface{}{
			"task_id":        parentTask.ID,
			"source_task_id": sourceTaskID,
			"subtask_count":  len(subtasks),
		},
		Metadata: map[string]string{},
	})
}
