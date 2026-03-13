package orchestrationhttp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

type taskExecutionAwaitingStepError struct {
	Result string
}

func (e *taskExecutionAwaitingStepError) Error() string {
	return "task is waiting for the next internal execution step"
}

func shouldUseStructuredExecution(task *workspace.Task, taskForExecution workspace.Task) bool {
	if task == nil {
		return false
	}
	inferred := workspace.InferTaskExecutionSteps(taskForExecution)
	if len(task.ExecutionSteps) > 0 {
		return len(inferred) > 0
	}
	return len(inferred) > 1
}

func buildStructuredExecutionExtra(task *workspace.Task) map[string]interface{} {
	if task == nil || task.Context == nil {
		return nil
	}

	extra := map[string]interface{}{}
	if value, ok := task.Context["execution_blocked_step_index"]; ok {
		extra["blocked_step_index"] = value
	}
	if value, ok := task.Context["execution_blocked_step_title"]; ok {
		extra["blocked_step_title"] = value
	}
	if len(extra) == 0 {
		return nil
	}
	return extra
}

func (th *TaskHandler) executeTaskWithStructuredSteps(
	ctx context.Context,
	ws *workspace.Workspace,
	persistedTask *workspace.Task,
	taskForExecution workspace.Task,
	manual bool,
) (string, error) {
	if persistedTask == nil {
		return "", fmt.Errorf("task is required")
	}

	if len(persistedTask.ExecutionSteps) == 0 {
		persistedTask.ExecutionSteps = workspace.InferTaskExecutionSteps(taskForExecution)
	}
	if len(persistedTask.ExecutionSteps) == 0 {
		return th.executeTaskIteratively(ctx, ws, persistedTask, taskForExecution, manual)
	}

	if persistedTask.Context == nil {
		persistedTask.Context = map[string]interface{}{}
	}
	delete(persistedTask.Context, "execution_blocked_step_index")
	delete(persistedTask.Context, "execution_blocked_step_title")

	mode := workspace.NormalizeTaskExecutionMode(string(persistedTask.ExecutionMode))
	if !manual || strings.TrimSpace(persistedTask.ParentTaskID) != "" {
		mode = workspace.TaskExecutionModeAuto
	}
	persistedTask.ExecutionMode = mode

	for {
		nextIndex := workspace.GetNextRunnableExecutionStepIndex(persistedTask)
		if nextIndex < 0 {
			persistedTask.Progress = &workspace.TaskProgress{
				Percentage:     100,
				CurrentStep:    "All execution steps completed.",
				TotalSteps:     len(persistedTask.ExecutionSteps),
				CompletedSteps: workspace.CountCompletedExecutionSteps(persistedTask),
				UpdatedAt:      time.Now(),
			}
			delete(persistedTask.Context, "execution_step_waiting")
			delete(persistedTask.Context, "execution_step_waiting_index")
			return workspace.BuildTaskExecutionSummary(persistedTask), nil
		}

		step := &persistedTask.ExecutionSteps[nextIndex]
		if step.Status == workspace.TaskExecutionStepBlocked || step.Status == workspace.TaskExecutionStepFailed {
			step.Status = workspace.TaskExecutionStepPending
			step.Error = ""
			step.StartedAt = nil
			step.CompletedAt = nil
		}

		now := time.Now()
		step.Status = workspace.TaskExecutionStepInProgress
		step.StartedAt = &now
		step.CompletedAt = nil
		step.Error = ""

		persistedTask.Progress = &workspace.TaskProgress{
			Percentage:     calculateStructuredStepProgress(persistedTask, nextIndex, false),
			CurrentStep:    fmt.Sprintf("Step %d/%d: %s", step.Index, len(persistedTask.ExecutionSteps), step.Title),
			TotalSteps:     len(persistedTask.ExecutionSteps),
			CompletedSteps: workspace.CountCompletedExecutionSteps(persistedTask),
			UpdatedAt:      now,
		}
		delete(persistedTask.Context, "execution_step_waiting")
		delete(persistedTask.Context, "execution_step_waiting_index")

		if err := th.persistStructuredTaskState(ws, persistedTask); err != nil {
			return "", err
		}
		th.publishStructuredTaskProgress(ws, persistedTask, step, false)

		stepTask := buildStructuredExecutionStepTask(taskForExecution, *persistedTask, *step)
		result, execErr := th.executeTaskIteratively(ctx, ws, persistedTask, stepTask, manual)
		completedAt := time.Now()
		step.CompletedAt = &completedAt

		if execErr != nil {
			step.Error = execErr.Error()
			if blockedErr, ok := workspace.AsTaskBlockedError(execErr); ok {
				step.Status = workspace.TaskExecutionStepBlocked
				persistedTask.Context["execution_blocked_step_index"] = step.Index
				persistedTask.Context["execution_blocked_step_title"] = step.Title
				persistedTask.Progress = &workspace.TaskProgress{
					Percentage:     calculateStructuredStepProgress(persistedTask, nextIndex, false),
					CurrentStep:    fmt.Sprintf("Blocked on step %d/%d: %s", step.Index, len(persistedTask.ExecutionSteps), step.Title),
					TotalSteps:     len(persistedTask.ExecutionSteps),
					CompletedSteps: workspace.CountCompletedExecutionSteps(persistedTask),
					UpdatedAt:      completedAt,
				}
				if err := th.persistStructuredTaskState(ws, persistedTask); err != nil {
					return "", err
				}
				th.publishStructuredTaskProgress(ws, persistedTask, step, false)
				return "", blockedErr
			}

			step.Status = workspace.TaskExecutionStepFailed
			persistedTask.Progress = &workspace.TaskProgress{
				Percentage:     calculateStructuredStepProgress(persistedTask, nextIndex, false),
				CurrentStep:    fmt.Sprintf("Failed on step %d/%d: %s", step.Index, len(persistedTask.ExecutionSteps), step.Title),
				TotalSteps:     len(persistedTask.ExecutionSteps),
				CompletedSteps: workspace.CountCompletedExecutionSteps(persistedTask),
				UpdatedAt:      completedAt,
			}
			if err := th.persistStructuredTaskState(ws, persistedTask); err != nil {
				return "", err
			}
			th.publishStructuredTaskProgress(ws, persistedTask, step, false)
			return "", execErr
		}

		step.Status = workspace.TaskExecutionStepCompleted
		step.Result = strings.TrimSpace(result)
		persistedTask.Progress = &workspace.TaskProgress{
			Percentage:     calculateStructuredStepProgress(persistedTask, nextIndex, true),
			CurrentStep:    fmt.Sprintf("Completed step %d/%d: %s", step.Index, len(persistedTask.ExecutionSteps), step.Title),
			TotalSteps:     len(persistedTask.ExecutionSteps),
			CompletedSteps: workspace.CountCompletedExecutionSteps(persistedTask),
			UpdatedAt:      completedAt,
		}

		if err := th.persistStructuredTaskState(ws, persistedTask); err != nil {
			return "", err
		}
		th.publishStructuredTaskProgress(ws, persistedTask, step, false)

		nextRunnable := workspace.GetNextRunnableExecutionStepIndex(persistedTask)
		if mode == workspace.TaskExecutionModeStepThrough && nextRunnable >= 0 {
			nextStep := persistedTask.ExecutionSteps[nextRunnable]
			persistedTask.Context["execution_step_waiting"] = true
			persistedTask.Context["execution_step_waiting_index"] = nextStep.Index
			persistedTask.Progress = &workspace.TaskProgress{
				Percentage:     calculateStructuredStepProgress(persistedTask, nextIndex, true),
				CurrentStep:    fmt.Sprintf("Waiting to run step %d/%d: %s", nextStep.Index, len(persistedTask.ExecutionSteps), nextStep.Title),
				TotalSteps:     len(persistedTask.ExecutionSteps),
				CompletedSteps: workspace.CountCompletedExecutionSteps(persistedTask),
				UpdatedAt:      time.Now(),
			}
			if err := th.persistStructuredTaskState(ws, persistedTask); err != nil {
				return "", err
			}
			th.publishStructuredTaskProgress(ws, persistedTask, &nextStep, true)
			return result, &taskExecutionAwaitingStepError{Result: result}
		}
	}
}

func (th *TaskHandler) persistStructuredTaskState(ws *workspace.Workspace, task *workspace.Task) error {
	if ws == nil || task == nil {
		return nil
	}
	if err := ws.UpdateTask(*task); err != nil {
		return fmt.Errorf("failed to update task: %w", err)
	}
	if err := th.workspaceStore.Save(ws); err != nil {
		return fmt.Errorf("failed to save workspace: %w", err)
	}
	return nil
}

func (th *TaskHandler) publishStructuredTaskProgress(ws *workspace.Workspace, task *workspace.Task, step *workspace.TaskExecutionStep, waiting bool) {
	if th.eventBus == nil || ws == nil || task == nil {
		return
	}

	data := map[string]interface{}{
		"description":           task.Description,
		"execution_mode":        task.ExecutionMode,
		"waiting_for_next_step": waiting,
		"progress":              task.Progress,
		"steps":                 task.ExecutionSteps,
	}
	if step != nil {
		data["step_index"] = step.Index
		data["step_title"] = step.Title
		data["step_status"] = step.Status
	}
	th.eventBus.Publish(workspace.NewTaskEvent(workspace.EventTaskProgress, ws.ID, task.ID, task.To, data))
}

func buildStructuredExecutionStepTask(baseTask workspace.Task, persistedTask workspace.Task, step workspace.TaskExecutionStep) workspace.Task {
	stepTask := baseTask
	stepTask.Context = cloneTaskContext(baseTask.Context)
	if stepTask.Context == nil {
		stepTask.Context = map[string]interface{}{}
	}

	stepTask.Context["execution_step"] = map[string]interface{}{
		"index":       step.Index,
		"title":       step.Title,
		"detail":      step.Detail,
		"tag":         step.Tag,
		"total_steps": len(persistedTask.ExecutionSteps),
	}
	stepTask.Context["execution_overall_task_description"] = strings.TrimSpace(baseTask.Description)
	stepTask.Context["execution_previous_step_results"] = collectCompletedStructuredStepResults(persistedTask.ExecutionSteps)
	stepTask.Context["execution_parent_task_id"] = persistedTask.ID
	stepTask.Description = buildStructuredExecutionStepDescription(baseTask, persistedTask, step)
	return stepTask
}

func buildStructuredExecutionStepDescription(baseTask workspace.Task, persistedTask workspace.Task, step workspace.TaskExecutionStep) string {
	var prompt strings.Builder
	prompt.WriteString(fmt.Sprintf("Complete internal execution step %d of %d for this task.\n\n", step.Index, len(persistedTask.ExecutionSteps)))
	prompt.WriteString(fmt.Sprintf("Overall task: %s\n\n", strings.TrimSpace(baseTask.Description)))
	prompt.WriteString(fmt.Sprintf("Current step: %s\n", step.Title))
	if detail := strings.TrimSpace(step.Detail); detail != "" {
		prompt.WriteString(fmt.Sprintf("Step detail: %s\n", detail))
	}
	if tag := strings.TrimSpace(step.Tag); tag != "" {
		prompt.WriteString(fmt.Sprintf("Step type: %s\n", tag))
	}

	if previous := collectCompletedStructuredStepResults(persistedTask.ExecutionSteps); len(previous) > 0 {
		prompt.WriteString("\nCompleted step results so far:\n")
		for title, result := range previous {
			prompt.WriteString(fmt.Sprintf("- %s: %s\n", title, summarizeExecutionText(result)))
		}
	}

	prompt.WriteString("\nRules:\n")
	prompt.WriteString("- Focus only on completing this step.\n")
	prompt.WriteString("- Use tools when necessary.\n")
	prompt.WriteString("- If this step changes files or external state, do not stop after discovery alone.\n")
	prompt.WriteString("- If blocked, explain exactly what missing scope, tool, or path is required.\n")
	prompt.WriteString("- Return a concise step result that the next step can use.\n")
	return prompt.String()
}

func collectCompletedStructuredStepResults(steps []workspace.TaskExecutionStep) map[string]string {
	results := make(map[string]string)
	for _, step := range steps {
		if step.Status != workspace.TaskExecutionStepCompleted {
			continue
		}
		if result := strings.TrimSpace(step.Result); result != "" {
			results[step.Title] = result
		}
	}
	return results
}

func calculateStructuredStepProgress(task *workspace.Task, currentIndex int, currentStepCompleted bool) int {
	if task == nil || len(task.ExecutionSteps) == 0 {
		return 0
	}

	completed := workspace.CountCompletedExecutionSteps(task)
	if currentStepCompleted && currentIndex >= 0 && currentIndex < len(task.ExecutionSteps) &&
		task.ExecutionSteps[currentIndex].Status != workspace.TaskExecutionStepCompleted &&
		task.ExecutionSteps[currentIndex].Status != workspace.TaskExecutionStepSkipped {
		completed++
	}

	percentage := (completed * 100) / len(task.ExecutionSteps)
	if percentage < 0 {
		return 0
	}
	if percentage > 100 {
		return 100
	}
	return percentage
}
