package agentstudio

import (
	"fmt"
	"strings"
	"time"
)

// AgentContext provides workspace context for an agent
type AgentContext struct {
	AgentName      string
	WorkspaceStore Store
}

// NewAgentContext creates a new agent context
func NewAgentContext(agentName string, store Store) *AgentContext {
	return &AgentContext{
		AgentName:      agentName,
		WorkspaceStore: store,
	}
}

// GetActiveWorkspaces returns workspaces where this agent is participating
func (ac *AgentContext) GetActiveWorkspaces() ([]*Workspace, error) {
	workspaceIDs, err := ac.WorkspaceStore.List()
	if err != nil {
		return nil, fmt.Errorf("failed to list workspaces: %w", err)
	}

	var activeWorkspaces []*Workspace
	for _, wsID := range workspaceIDs {
		ws, err := ac.WorkspaceStore.Get(wsID)
		if err != nil {
			continue
		}

		// Check if agent is in this workspace
		if ws.HasAgent(ac.AgentName) {
			if ws.Status == StatusActive {
				activeWorkspaces = append(activeWorkspaces, ws)
			}
		}
	}

	return activeWorkspaces, nil
}

// GetPendingTasks returns all pending tasks for this agent across all workspaces
func (ac *AgentContext) GetPendingTasks() ([]Task, error) {
	workspaceIDs, err := ac.WorkspaceStore.List()
	if err != nil {
		return nil, fmt.Errorf("failed to list workspaces: %w", err)
	}

	var pendingTasks []Task
	for _, wsID := range workspaceIDs {
		ws, err := ac.WorkspaceStore.Get(wsID)
		if err != nil {
			continue
		}

		// Only get tasks from active workspaces where agent participates
		if ws.Status == StatusActive && ws.HasAgent(ac.AgentName) {
			tasks := ws.GetPendingTasksForAgent(ac.AgentName)
			pendingTasks = append(pendingTasks, tasks...)
		}
	}

	return pendingTasks, nil
}

// GetAllTasks returns all tasks for this agent (any status) across all workspaces
func (ac *AgentContext) GetAllTasks() ([]Task, error) {
	workspaceIDs, err := ac.WorkspaceStore.List()
	if err != nil {
		return nil, fmt.Errorf("failed to list workspaces: %w", err)
	}

	var allTasks []Task
	for _, wsID := range workspaceIDs {
		ws, err := ac.WorkspaceStore.Get(wsID)
		if err != nil {
			continue
		}

		// Only get tasks from workspaces where agent participates
		if ws.HasAgent(ac.AgentName) {
			tasks := ws.GetTasksForAgent(ac.AgentName)
			allTasks = append(allTasks, tasks...)
		}
	}

	return allTasks, nil
}

// GetTaskByID finds a specific task by ID across all workspaces
func (ac *AgentContext) GetTaskByID(taskID string) (*Task, *Workspace, error) {
	workspaceIDs, err := ac.WorkspaceStore.List()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list workspaces: %w", err)
	}

	for _, wsID := range workspaceIDs {
		ws, err := ac.WorkspaceStore.Get(wsID)
		if err != nil {
			continue
		}

		task, err := ws.GetTask(taskID)
		if err == nil {
			return task, ws, nil
		}
	}

	return nil, nil, fmt.Errorf("task %s not found", taskID)
}

// GetWorkspaceSummary returns a formatted summary of workspace participation
func (ac *AgentContext) GetWorkspaceSummary() (string, error) {
	workspaces, err := ac.GetActiveWorkspaces()
	if err != nil {
		return "", err
	}

	if len(workspaces) == 0 {
		return "You are not currently participating in any workspaces.", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Active Workspaces (%d)\n\n", len(workspaces)))

	for i, ws := range workspaces {
		sb.WriteString(fmt.Sprintf("### %d. %s\n", i+1, ws.Name))
		if ws.Description != "" {
			sb.WriteString(fmt.Sprintf("   %s\n", ws.Description))
		}
		sb.WriteString(fmt.Sprintf("   - **ID**: `%s`\n", ws.ID))
		sb.WriteString(fmt.Sprintf("   - **Status**: %s\n", ws.Status))
		sb.WriteString(fmt.Sprintf("   - **Agents**: %s\n", strings.Join(ws.Agents, ", ")))

		// Count tasks
		stats := ws.GetTaskStats()
		sb.WriteString(fmt.Sprintf("   - **Tasks**: %d total", stats["total"]))
		if stats["pending"] > 0 || stats["assigned"] > 0 {
			sb.WriteString(fmt.Sprintf(" (%d pending/assigned)", stats["pending"]+stats["assigned"]))
		}
		sb.WriteString("\n\n")
	}

	return sb.String(), nil
}

// GetTasksSummary returns a formatted summary of tasks for this agent
func (ac *AgentContext) GetTasksSummary() (string, error) {
	pendingTasks, err := ac.GetPendingTasks()
	if err != nil {
		return "", err
	}

	allTasks, err := ac.GetAllTasks()
	if err != nil {
		return "", err
	}

	var sb strings.Builder

	// Summary counts
	completed := 0
	inProgress := 0
	failed := 0
	for _, task := range allTasks {
		switch task.Status {
		case TaskStatusCompleted:
			completed++
		case TaskStatusInProgress:
			inProgress++
		case TaskStatusFailed:
			failed++
		}
	}

	sb.WriteString(fmt.Sprintf("## Your Tasks\n\n"))
	sb.WriteString(fmt.Sprintf("**Total**: %d | **Pending**: %d | **In Progress**: %d | **Completed**: %d | **Failed**: %d\n\n",
		len(allTasks), len(pendingTasks), inProgress, completed, failed))

	if len(pendingTasks) == 0 {
		sb.WriteString("You have no pending tasks.\n")
		return sb.String(), nil
	}

	// Show pending tasks
	sb.WriteString(fmt.Sprintf("### Pending Tasks (%d)\n\n", len(pendingTasks)))
	for i, task := range pendingTasks {
		sb.WriteString(fmt.Sprintf("%d. **%s**\n", i+1, task.Description))
		sb.WriteString(fmt.Sprintf("   - **Task ID**: `%s`\n", task.ID))
		sb.WriteString(fmt.Sprintf("   - **From**: %s\n", task.From))
		sb.WriteString(fmt.Sprintf("   - **Priority**: %d/5\n", task.Priority))
		sb.WriteString(fmt.Sprintf("   - **Status**: %s\n", task.Status))

		// Show age
		age := time.Since(task.CreatedAt)
		if age < time.Minute {
			sb.WriteString(fmt.Sprintf("   - **Age**: %d seconds\n", int(age.Seconds())))
		} else if age < time.Hour {
			sb.WriteString(fmt.Sprintf("   - **Age**: %d minutes\n", int(age.Minutes())))
		} else if age < 24*time.Hour {
			sb.WriteString(fmt.Sprintf("   - **Age**: %d hours\n", int(age.Hours())))
		} else {
			sb.WriteString(fmt.Sprintf("   - **Age**: %d days\n", int(age.Hours()/24)))
		}

		// Show timeout if set
		if task.Timeout > 0 {
			sb.WriteString(fmt.Sprintf("   - **Timeout**: %v\n", task.Timeout))
		}

		sb.WriteString("\n")
	}

	return sb.String(), nil
}

// GetTaskDetails returns detailed information about a specific task
func (ac *AgentContext) GetTaskDetails(taskID string) (string, error) {
	task, ws, err := ac.GetTaskByID(taskID)
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Task Details: %s\n\n", task.Description))
	sb.WriteString(fmt.Sprintf("**Task ID**: `%s`\n\n", task.ID))
	sb.WriteString(fmt.Sprintf("**Workspace**: %s (`%s`)\n\n", ws.Name, ws.ID))
	sb.WriteString(fmt.Sprintf("**Status**: %s\n\n", task.Status))
	sb.WriteString(fmt.Sprintf("**Priority**: %d/5\n\n", task.Priority))
	sb.WriteString(fmt.Sprintf("**From**: %s\n\n", task.From))
	sb.WriteString(fmt.Sprintf("**To**: %s\n\n", task.To))
	sb.WriteString(fmt.Sprintf("**Created**: %s\n\n", task.CreatedAt.Format(time.RFC3339)))

	if task.StartedAt != nil {
		sb.WriteString(fmt.Sprintf("**Started**: %s\n\n", task.StartedAt.Format(time.RFC3339)))
	}

	if task.CompletedAt != nil {
		sb.WriteString(fmt.Sprintf("**Completed**: %s\n\n", task.CompletedAt.Format(time.RFC3339)))

		if task.StartedAt != nil {
			duration := task.CompletedAt.Sub(*task.StartedAt)
			sb.WriteString(fmt.Sprintf("**Duration**: %v\n\n", duration))
		}
	}

	if task.Timeout > 0 {
		sb.WriteString(fmt.Sprintf("**Timeout**: %v\n\n", task.Timeout))
	}

	if len(task.Context) > 0 {
		sb.WriteString("**Context**:\n")
		for key, value := range task.Context {
			sb.WriteString(fmt.Sprintf("- %s: %v\n", key, value))
		}
		sb.WriteString("\n")
	}

	if task.Result != "" {
		sb.WriteString(fmt.Sprintf("**Result**:\n```\n%s\n```\n\n", task.Result))
	}

	if task.Error != "" {
		sb.WriteString(fmt.Sprintf("**Error**:\n```\n%s\n```\n\n", task.Error))
	}

	return sb.String(), nil
}

// HasPendingTasks returns true if the agent has any pending tasks
func (ac *AgentContext) HasPendingTasks() (bool, error) {
	tasks, err := ac.GetPendingTasks()
	if err != nil {
		return false, err
	}
	return len(tasks) > 0, nil
}

// GetPendingTaskCount returns the number of pending tasks
func (ac *AgentContext) GetPendingTaskCount() (int, error) {
	tasks, err := ac.GetPendingTasks()
	if err != nil {
		return 0, err
	}
	return len(tasks), nil
}
