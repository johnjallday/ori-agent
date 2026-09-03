package orchestration

import (
	"fmt"

	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// WorkflowStatus represents the status of a workflow execution
type WorkflowStatus struct {
	WorkspaceID string                 `json:"workspace_id"`
	Phase       string                 `json:"phase"`
	Progress    float64                `json:"progress"` // 0.0 to 1.0
	Tasks       map[string]TaskSummary `json:"tasks"`
	StartTime   time.Time              `json:"start_time"`
	UpdatedAt   time.Time              `json:"updated_at"`
}

// TaskSummary provides a summary of a task's status
type TaskSummary struct {
	Agent       string    `json:"agent"`
	Status      string    `json:"status"`
	Description string    `json:"description"`
	StartedAt   time.Time `json:"started_at"`
}

// GetWorkflowStatus retrieves the status of an ongoing workflow
func (o *Orchestrator) GetWorkflowStatus(workspaceID string) (*WorkflowStatus, error) {
	ws, err := o.workspaceStore.Get(workspaceID)
	if err != nil {
		return nil, fmt.Errorf("workspace not found: %w", err)
	}

	// Get all tasks for this workspace
	tasks := o.communicator.ListTasks(workspaceID)
	taskSummaries := make(map[string]TaskSummary)

	completedCount := 0
	totalCount := len(tasks)

	for _, task := range tasks {
		taskSummaries[task.ID] = TaskSummary{
			Agent:       task.To,
			Status:      string(task.Status),
			Description: task.Description,
			StartedAt:   task.CreatedAt,
		}

		if task.Status == workspace.TaskStatusCompleted ||
			task.Status == workspace.TaskStatusFailed ||
			task.Status == workspace.TaskStatusCancelled ||
			task.Status == workspace.TaskStatusTimeout {
			completedCount++
		}
	}

	// Calculate progress
	progress := 0.0
	if totalCount > 0 {
		progress = float64(completedCount) / float64(totalCount)
	}

	// Determine current phase
	phase := "initializing"
	if progress > 0 && progress < 0.5 {
		phase = "executing"
	} else if progress >= 0.5 && progress < 1.0 {
		phase = "finalizing"
	} else if progress == 1.0 {
		phase = "completed"
	}

	return &WorkflowStatus{
		WorkspaceID: workspaceID,
		Phase:       phase,
		Progress:    progress,
		Tasks:       taskSummaries,
		StartTime:   ws.CreatedAt,
		UpdatedAt:   ws.UpdatedAt,
	}, nil
}
