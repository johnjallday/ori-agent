package orchestrationhttp

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

type parentTaskGraphPlan struct {
	sortedSubtasks []workspace.Task
	internalDeps   map[string][]string
	dependents     map[string][]string
	sinkIDs        []string
	orderIndex     map[string]int
}

func shouldUseGraphParentExecution(parentTask *workspace.Task, subtasks []workspace.Task) bool {
	if parentTask != nil && workspace.NormalizeTaskOrchestrationMode(string(parentTask.OrchestrationMode)) == workspace.TaskOrchestrationModeGraph {
		return true
	}

	siblingIDs := make(map[string]struct{}, len(subtasks))
	for _, subtask := range subtasks {
		siblingIDs[subtask.ID] = struct{}{}
	}
	for _, subtask := range subtasks {
		for _, inputTaskID := range subtask.InputTaskIDs {
			if _, ok := siblingIDs[inputTaskID]; ok {
				return true
			}
		}
	}
	return false
}

func buildParentTaskGraphPlan(subtasks []workspace.Task) (*parentTaskGraphPlan, error) {
	sortedSubtasks := sortParentSubtasks(subtasks)
	internalDeps := make(map[string][]string, len(sortedSubtasks))
	dependents := make(map[string][]string, len(sortedSubtasks))
	orderIndex := make(map[string]int, len(sortedSubtasks))
	siblingIDs := make(map[string]struct{}, len(sortedSubtasks))

	for index, subtask := range sortedSubtasks {
		siblingIDs[subtask.ID] = struct{}{}
		orderIndex[subtask.ID] = index
		internalDeps[subtask.ID] = nil
	}

	for _, subtask := range sortedSubtasks {
		for _, inputTaskID := range subtask.InputTaskIDs {
			if _, ok := siblingIDs[inputTaskID]; !ok {
				continue
			}
			internalDeps[subtask.ID] = append(internalDeps[subtask.ID], inputTaskID)
			dependents[inputTaskID] = append(dependents[inputTaskID], subtask.ID)
		}
	}

	if err := validateParentTaskGraph(sortedSubtasks, internalDeps); err != nil {
		return nil, err
	}

	sinkIDs := make([]string, 0, len(sortedSubtasks))
	for _, subtask := range sortedSubtasks {
		if len(dependents[subtask.ID]) == 0 {
			sinkIDs = append(sinkIDs, subtask.ID)
		}
	}
	sort.SliceStable(sinkIDs, func(i, j int) bool {
		return orderIndex[sinkIDs[i]] < orderIndex[sinkIDs[j]]
	})

	return &parentTaskGraphPlan{
		sortedSubtasks: sortedSubtasks,
		internalDeps:   internalDeps,
		dependents:     dependents,
		sinkIDs:        sinkIDs,
		orderIndex:     orderIndex,
	}, nil
}

func (th *TaskHandler) executeParentTaskGraph(ws *workspace.Workspace, parentTask *workspace.Task, subtasks []workspace.Task) (string, string, error) {
	plan, err := buildParentTaskGraphPlan(subtasks)
	if err != nil {
		return "", "", err
	}
	if err := th.prepareParentSubtasksForExecution(ws, plan.sortedSubtasks); err != nil {
		return "", "", err
	}

	var lastResult string
	for _, sinkID := range plan.sinkIDs {
		sinkTask, err := ws.GetTask(sinkID)
		if err != nil {
			return "", sinkID, fmt.Errorf("sink subtask %s not found: %w", sinkID, err)
		}

		if sinkTask.Status == workspace.TaskStatusCompleted {
			lastResult = sinkTask.Result
			continue
		}

		result, err := th.executeTaskWithDependencies(ws, sinkTask, true)
		if err != nil {
			var blockedErr *workspace.TaskBlockedError
			if errors.As(err, &blockedErr) {
				return "", sinkTask.ID, blockedErr
			}
			return "", sinkTask.ID, err
		}
		lastResult = result
	}

	return lastResult, "", nil
}

func sortParentSubtasks(subtasks []workspace.Task) []workspace.Task {
	sorted := append([]workspace.Task(nil), subtasks...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].SubtaskIndex > 0 && sorted[j].SubtaskIndex > 0 && sorted[i].SubtaskIndex != sorted[j].SubtaskIndex {
			return sorted[i].SubtaskIndex < sorted[j].SubtaskIndex
		}
		if !sorted[i].CreatedAt.Equal(sorted[j].CreatedAt) {
			return sorted[i].CreatedAt.Before(sorted[j].CreatedAt)
		}
		return sorted[i].ID < sorted[j].ID
	})
	return sorted
}

func (th *TaskHandler) prepareParentSubtasksForExecution(ws *workspace.Workspace, subtasks []workspace.Task) error {
	needsSave := false

	for _, subtaskInfo := range subtasks {
		subtask, err := ws.GetTask(subtaskInfo.ID)
		if err != nil {
			return fmt.Errorf("subtask %s not found", subtaskInfo.ID)
		}

		if subtask.Status == workspace.TaskStatusInProgress {
			return fmt.Errorf("subtask %s already in progress", subtask.Description)
		}
		if subtask.To == "" || subtask.To == "unassigned" {
			return fmt.Errorf("subtask %s has no assigned agent", subtask.Description)
		}

		if !resetParentSubtaskForExecution(subtask) {
			continue
		}

		if err := ws.UpdateTask(*subtask); err != nil {
			return fmt.Errorf("failed to reset subtask %s: %w", subtask.ID, err)
		}
		needsSave = true
	}

	if needsSave {
		if err := th.workspaceStore.Save(ws); err != nil {
			return fmt.Errorf("failed to save workspace before subtask execution: %w", err)
		}
	}

	return nil
}

func resetParentSubtaskForExecution(task *workspace.Task) bool {
	if task == nil {
		return false
	}

	changed := false
	switch task.Status {
	case workspace.TaskStatusCompleted, workspace.TaskStatusFailed, workspace.TaskStatusCancelled, workspace.TaskStatusTimeout:
		task.Status = workspace.TaskStatusPending
		changed = true
	}

	if strings.TrimSpace(task.Result) != "" {
		task.Result = ""
		changed = true
	}
	if strings.TrimSpace(task.Error) != "" {
		task.Error = ""
		changed = true
	}
	if task.StartedAt != nil {
		task.StartedAt = nil
		changed = true
	}
	if task.CompletedAt != nil {
		task.CompletedAt = nil
		changed = true
	}
	if task.Context != nil {
		if _, ok := task.Context["human_loop"]; ok {
			delete(task.Context, "human_loop")
			changed = true
		}
		if _, ok := task.Context["structured_output"]; ok {
			delete(task.Context, "structured_output")
			changed = true
		}
	}

	return changed
}

func validateParentTaskGraph(subtasks []workspace.Task, internalDeps map[string][]string) error {
	inDegree := make(map[string]int, len(subtasks))
	dependents := make(map[string][]string, len(subtasks))
	for _, subtask := range subtasks {
		inDegree[subtask.ID] = len(internalDeps[subtask.ID])
		for _, depID := range internalDeps[subtask.ID] {
			dependents[depID] = append(dependents[depID], subtask.ID)
		}
	}

	queue := make([]string, 0, len(subtasks))
	for _, subtask := range subtasks {
		if inDegree[subtask.ID] == 0 {
			queue = append(queue, subtask.ID)
		}
	}

	visited := 0
	for len(queue) > 0 {
		currentID := queue[0]
		queue = queue[1:]
		visited++
		for _, dependentID := range dependents[currentID] {
			inDegree[dependentID]--
			if inDegree[dependentID] == 0 {
				queue = append(queue, dependentID)
			}
		}
	}

	if visited != len(subtasks) {
		return fmt.Errorf("subtask graph contains a cycle")
	}
	return nil
}

func aggregateParentTaskResults(parentTask *workspace.Task, subtasks []workspace.Task, lastResult string) (string, error) {
	mode := workspace.NormalizeTaskResultCombinationMode(string(parentTask.ResultCombinationMode))

	switch mode {
	case workspace.TaskResultCombinationConcat:
		return buildConcatenatedParentTaskResult(subtasks), nil
	case workspace.TaskResultCombinationJSONMap:
		return buildJSONMapParentTaskResult(parentTask, subtasks)
	case workspace.TaskResultCombinationStructuredOutput:
		return buildStructuredParentTaskResult(parentTask, subtasks)
	default:
		return strings.TrimSpace(lastResult), nil
	}
}

func buildConcatenatedParentTaskResult(subtasks []workspace.Task) string {
	parts := make([]string, 0, len(subtasks))
	for _, subtask := range subtasks {
		result := strings.TrimSpace(subtask.Result)
		if result == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s:\n%s", parentTaskStepLabel(subtask), result))
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func buildJSONMapParentTaskResult(parentTask *workspace.Task, subtasks []workspace.Task) (string, error) {
	payload := map[string]interface{}{
		"parent_task_id": parentTask.ID,
		"template_ref":   parentTask.TemplateRef,
		"steps":          make([]map[string]interface{}, 0, len(subtasks)),
	}

	for _, subtask := range subtasks {
		payload["steps"] = append(payload["steps"].([]map[string]interface{}), map[string]interface{}{
			"task_id":        subtask.ID,
			"label":          parentTaskStepLabel(subtask),
			"status":         subtask.Status,
			"agent":          subtask.To,
			"result":         strings.TrimSpace(subtask.Result),
			"structured":     parseSubtaskStructuredOutput(subtask),
			"template_ref":   subtask.TemplateRef,
			"output_schema":  workspace.NormalizeTaskOutputSchema(subtask.OutputSchema),
			"subtask_index":  subtask.SubtaskIndex,
			"input_task_ids": append([]string(nil), subtask.InputTaskIDs...),
		})
	}

	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func buildStructuredParentTaskResult(parentTask *workspace.Task, subtasks []workspace.Task) (string, error) {
	steps := make([]map[string]interface{}, 0, len(subtasks))
	finalOutputs := make([]map[string]interface{}, 0, len(subtasks))

	for _, subtask := range subtasks {
		entry := map[string]interface{}{
			"task_id":      subtask.ID,
			"step_label":   parentTaskStepLabel(subtask),
			"status":       subtask.Status,
			"agent":        subtask.To,
			"template_ref": subtask.TemplateRef,
		}
		if structured := parseSubtaskStructuredOutput(subtask); structured != nil {
			entry["output"] = structured
			finalOutputs = append(finalOutputs, map[string]interface{}{
				"task_id":    subtask.ID,
				"step_label": parentTaskStepLabel(subtask),
				"output":     structured,
			})
		} else if result := strings.TrimSpace(subtask.Result); result != "" {
			entry["result"] = result
			finalOutputs = append(finalOutputs, map[string]interface{}{
				"task_id":    subtask.ID,
				"step_label": parentTaskStepLabel(subtask),
				"result":     result,
			})
		}
		steps = append(steps, entry)
	}

	payload := map[string]interface{}{
		"parent_task_id":     parentTask.ID,
		"template_ref":       parentTask.TemplateRef,
		"combination_mode":   parentTask.ResultCombinationMode,
		"combination_note":   strings.TrimSpace(parentTask.CombinationInstruction),
		"steps":              steps,
		"final_step_outputs": finalOutputs,
	}

	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func parentTaskStepLabel(task workspace.Task) string {
	if task.TemplateRef != nil && strings.TrimSpace(task.TemplateRef.StepName) != "" {
		return strings.TrimSpace(task.TemplateRef.StepName)
	}
	if strings.TrimSpace(task.Description) != "" {
		return strings.TrimSpace(task.Description)
	}
	return task.ID
}

func parseSubtaskStructuredOutput(task workspace.Task) interface{} {
	parsed, err := workspace.ValidateTaskStructuredOutput(task.OutputSchema, task.Result)
	if err != nil || len(parsed) == 0 {
		return nil
	}
	return parsed
}
