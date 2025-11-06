package agentcomm

import (
	"fmt"
	"log"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Communicator handles inter-agent communication and task delegation
type Communicator struct {
	workspaceStore workspace.Store
	// Note: tasks are now stored in workspace files, not in memory
}

// NewCommunicator creates a new agent communicator
func NewCommunicator(workspaceStore workspace.Store) *Communicator {
	return &Communicator{
		workspaceStore: workspaceStore,
	}
}

// SendMessage sends a message to a workspace
func (c *Communicator) SendMessage(req MessageRequest) error {
	// Get workspace
	ws, err := c.workspaceStore.Get(req.WorkspaceID)
	if err != nil {
		return fmt.Errorf("workspace not found: %w", err)
	}

	// Create message
	msg := workspace.AgentMessage{
		From:     req.From,
		To:       req.To,
		Type:     req.Type,
		Content:  req.Content,
		Metadata: req.Metadata,
	}

	// Add message to workspace
	if err := ws.AddMessage(msg); err != nil {
		return fmt.Errorf("failed to add message: %w", err)
	}

	// Save workspace
	if err := c.workspaceStore.Save(ws); err != nil {
		return fmt.Errorf("failed to save workspace: %w", err)
	}

	log.Printf("✉️  Message sent from %s to %s in workspace %s", req.From, req.To, req.WorkspaceID)
	return nil
}

// GetMessages retrieves messages for a specific agent from a workspace
func (c *Communicator) GetMessages(workspaceID, agentName string) ([]workspace.AgentMessage, error) {
	ws, err := c.workspaceStore.Get(workspaceID)
	if err != nil {
		return nil, fmt.Errorf("workspace not found: %w", err)
	}

	messages := ws.GetMessagesForAgent(agentName)
	return messages, nil
}

// GetMessagesSince retrieves messages added after a specific time
func (c *Communicator) GetMessagesSince(workspaceID string, since time.Time) ([]workspace.AgentMessage, error) {
	ws, err := c.workspaceStore.Get(workspaceID)
	if err != nil {
		return nil, fmt.Errorf("workspace not found: %w", err)
	}

	messages := ws.GetMessagesSince(since)
	return messages, nil
}

// BroadcastToWorkspace sends a message to all agents in a workspace
func (c *Communicator) BroadcastToWorkspace(workspaceID, from, content string, msgType workspace.MessageType) error {
	return c.SendMessage(MessageRequest{
		WorkspaceID: workspaceID,
		From:        from,
		To:          "", // Empty = broadcast
		Type:        msgType,
		Content:     content,
	})
}

// DelegateTask delegates a task to another agent
func (c *Communicator) DelegateTask(req DelegationRequest) (*workspace.Task, error) {
	// Validate workspace exists
	ws, err := c.workspaceStore.Get(req.WorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("workspace not found: %w", err)
	}

	// Verify both agents are in the workspace
	if !ws.HasAgent(req.From) && ws.ParentAgent != req.From {
		return nil, fmt.Errorf("delegating agent %s not in workspace", req.From)
	}
	if !ws.HasAgent(req.To) && ws.ParentAgent != req.To {
		return nil, fmt.Errorf("receiving agent %s not in workspace", req.To)
	}

	// Create task using workspace.Task
	now := time.Now()
	task := workspace.Task{
		ID:          "",  // Will be set by workspace.AddTask
		WorkspaceID: req.WorkspaceID,
		From:        req.From,
		To:          req.To,
		Description: req.Description,
		Priority:    req.Priority,
		Context:     req.Context,
		Timeout:     req.Timeout,
		Status:      workspace.TaskStatusAssigned,
		CreatedAt:   now,
	}

	// Add task to workspace (this persists it)
	if err := ws.AddTask(task); err != nil {
		return nil, fmt.Errorf("failed to add task to workspace: %w", err)
	}

	// Save workspace
	if err := c.workspaceStore.Save(ws); err != nil {
		return nil, fmt.Errorf("failed to save workspace: %w", err)
	}

	// Get the task with assigned ID
	addedTask, err := ws.GetTask(task.ID)
	if err != nil {
		// If we can't find it, use the last task (it was just added)
		tasks := ws.Tasks
		if len(tasks) > 0 {
			addedTask = &tasks[len(tasks)-1]
		} else {
			return nil, fmt.Errorf("failed to retrieve added task")
		}
	}

	// Send task request message
	if err := c.SendMessage(MessageRequest{
		WorkspaceID: req.WorkspaceID,
		From:        req.From,
		To:          req.To,
		Type:        workspace.MessageTaskRequest,
		Content:     req.Description,
		Metadata: map[string]interface{}{
			"task_id":  addedTask.ID,
			"priority": req.Priority,
			"timeout":  req.Timeout.String(),
			"context":  req.Context,
		},
	}); err != nil {
		log.Printf("⚠️  Failed to send task message: %v", err)
	}

	log.Printf("📋 Task %s delegated from %s to %s: %s", addedTask.ID, req.From, req.To, req.Description)
	return addedTask, nil
}

// GetTask retrieves a task by ID across all workspaces
func (c *Communicator) GetTask(taskID string) (*workspace.Task, error) {
	// We need to search through all workspaces
	// This is less efficient but necessary without in-memory cache
	workspaceIDs, err := c.workspaceStore.List()
	if err != nil {
		return nil, fmt.Errorf("failed to list workspaces: %w", err)
	}

	for _, wsID := range workspaceIDs {
		ws, err := c.workspaceStore.Get(wsID)
		if err != nil {
			continue
		}

		task, err := ws.GetTask(taskID)
		if err == nil {
			return task, nil
		}
	}

	return nil, fmt.Errorf("task %s not found", taskID)
}

// UpdateTaskStatus updates the status of a task
func (c *Communicator) UpdateTaskStatus(taskID string, status workspace.TaskStatus, result string, errorMsg string) error {
	// Find the workspace containing this task
	workspaceIDs, err := c.workspaceStore.List()
	if err != nil {
		return fmt.Errorf("failed to list workspaces: %w", err)
	}

	for _, wsID := range workspaceIDs {
		ws, err := c.workspaceStore.Get(wsID)
		if err != nil {
			continue
		}

		task, err := ws.GetTask(taskID)
		if err != nil {
			continue // Task not in this workspace
		}

		// Update task based on status
		now := time.Now()
		switch status {
		case workspace.TaskStatusInProgress:
			task.Status = workspace.TaskStatusInProgress
			task.StartedAt = &now
		case workspace.TaskStatusCompleted:
			task.Status = workspace.TaskStatusCompleted
			task.Result = result
			task.CompletedAt = &now
			// Send result message back to delegator
			c.sendTaskResult(task, result, "")
		case workspace.TaskStatusFailed:
			task.Status = workspace.TaskStatusFailed
			task.Error = errorMsg
			task.CompletedAt = &now
			// Send failure message back to delegator
			c.sendTaskResult(task, "", errorMsg)
		case workspace.TaskStatusCancelled:
			task.Status = workspace.TaskStatusCancelled
			task.CompletedAt = &now
		}

		// Update task in workspace
		if err := ws.UpdateTask(*task); err != nil {
			return fmt.Errorf("failed to update task: %w", err)
		}

		// Save workspace
		if err := c.workspaceStore.Save(ws); err != nil {
			return fmt.Errorf("failed to save workspace: %w", err)
		}

		log.Printf("📋 Task %s status updated: %s", taskID, status)
		return nil
	}

	return fmt.Errorf("task %s not found", taskID)
}

// sendTaskResult sends the task result back to the delegating agent
func (c *Communicator) sendTaskResult(task *workspace.Task, result string, errorMsg string) {
	content := result
	if errorMsg != "" {
		content = fmt.Sprintf("Task failed: %s", errorMsg)
	}

	duration := time.Duration(0)
	if task.CompletedAt != nil && task.StartedAt != nil {
		duration = task.CompletedAt.Sub(*task.StartedAt)
	}

	if err := c.SendMessage(MessageRequest{
		WorkspaceID: task.WorkspaceID,
		From:        task.To,
		To:          task.From,
		Type:        workspace.MessageResult,
		Content:     content,
		Metadata: map[string]interface{}{
			"task_id":  task.ID,
			"status":   task.Status,
			"duration": duration.String(),
		},
	}); err != nil {
		log.Printf("❌ Failed to send task result: %v", err)
	}
}

// ListTasks returns all tasks for a workspace
func (c *Communicator) ListTasks(workspaceID string) []workspace.Task {
	ws, err := c.workspaceStore.Get(workspaceID)
	if err != nil {
		log.Printf("⚠️  Failed to get workspace %s: %v", workspaceID, err)
		return []workspace.Task{}
	}

	return ws.Tasks
}

// ListTasksForAgent returns all tasks assigned to or from a specific agent
func (c *Communicator) ListTasksForAgent(agentName string) []workspace.Task {
	var allTasks []workspace.Task

	workspaceIDs, err := c.workspaceStore.List()
	if err != nil {
		log.Printf("⚠️  Failed to list workspaces: %v", err)
		return allTasks
	}

	for _, wsID := range workspaceIDs {
		ws, err := c.workspaceStore.Get(wsID)
		if err != nil {
			continue
		}

		for _, task := range ws.Tasks {
			if task.From == agentName || task.To == agentName {
				allTasks = append(allTasks, task)
			}
		}
	}

	return allTasks
}

// CleanupCompletedTasks removes completed tasks older than the specified duration
func (c *Communicator) CleanupCompletedTasks(olderThan time.Duration) int {
	cutoff := time.Now().Add(-olderThan)
	removed := 0

	workspaceIDs, err := c.workspaceStore.List()
	if err != nil {
		log.Printf("⚠️  Failed to list workspaces for cleanup: %v", err)
		return 0
	}

	for _, wsID := range workspaceIDs {
		ws, err := c.workspaceStore.Get(wsID)
		if err != nil {
			continue
		}

		// Filter out old completed tasks
		newTasks := make([]workspace.Task, 0, len(ws.Tasks))
		for _, task := range ws.Tasks {
			isComplete := task.Status == workspace.TaskStatusCompleted ||
				task.Status == workspace.TaskStatusFailed ||
				task.Status == workspace.TaskStatusCancelled ||
				task.Status == workspace.TaskStatusTimeout

			if isComplete && task.CompletedAt != nil && task.CompletedAt.Before(cutoff) {
				removed++
			} else {
				newTasks = append(newTasks, task)
			}
		}

		// Update workspace if tasks were removed
		if len(newTasks) < len(ws.Tasks) {
			ws.Tasks = newTasks
			ws.UpdatedAt = time.Now()
			if err := c.workspaceStore.Save(ws); err != nil {
				log.Printf("⚠️  Failed to save workspace after cleanup: %v", err)
			}
		}
	}

	if removed > 0 {
		log.Printf("🧹 Cleaned up %d completed tasks", removed)
	}

	return removed
}

// CheckTimeouts checks for tasks that have exceeded their timeout
func (c *Communicator) CheckTimeouts() []workspace.Task {
	var timedOut []workspace.Task
	now := time.Now()

	workspaceIDs, err := c.workspaceStore.List()
	if err != nil {
		log.Printf("⚠️  Failed to list workspaces for timeout check: %v", err)
		return timedOut
	}

	for _, wsID := range workspaceIDs {
		ws, err := c.workspaceStore.Get(wsID)
		if err != nil {
			continue
		}

		modified := false
		for i := range ws.Tasks {
			task := &ws.Tasks[i]
			if task.Status == workspace.TaskStatusInProgress && task.Timeout > 0 {
				if task.StartedAt != nil && now.Sub(*task.StartedAt) > task.Timeout {
					task.Status = workspace.TaskStatusTimeout
					completedAt := now
					task.CompletedAt = &completedAt
					timedOut = append(timedOut, *task)
					modified = true
					log.Printf("⏰ Task %s timed out after %v", task.ID, task.Timeout)
				}
			}
		}

		// Save workspace if tasks were modified
		if modified {
			ws.UpdatedAt = now
			if err := c.workspaceStore.Save(ws); err != nil {
				log.Printf("⚠️  Failed to save workspace after timeout check: %v", err)
			}
		}
	}

	return timedOut
}

// GetTaskStats returns statistics about tasks
func (c *Communicator) GetTaskStats(workspaceID string) map[string]int {
	ws, err := c.workspaceStore.Get(workspaceID)
	if err != nil {
		log.Printf("⚠️  Failed to get workspace %s: %v", workspaceID, err)
		return map[string]int{
			"total":       0,
			"pending":     0,
			"assigned":    0,
			"in_progress": 0,
			"completed":   0,
			"failed":      0,
			"cancelled":   0,
			"timeout":     0,
		}
	}

	return ws.GetTaskStats()
}

// DeleteTask removes a task from its workspace
func (c *Communicator) DeleteTask(taskID string) error {
	// Find the workspace containing this task
	workspaceIDs, err := c.workspaceStore.List()
	if err != nil {
		return fmt.Errorf("failed to list workspaces: %w", err)
	}

	for _, wsID := range workspaceIDs {
		ws, err := c.workspaceStore.Get(wsID)
		if err != nil {
			continue
		}

		// Check if this workspace contains the task
		_, err = ws.GetTask(taskID)
		if err != nil {
			continue // Task not in this workspace
		}

		// Filter out the task to delete
		newTasks := make([]workspace.Task, 0, len(ws.Tasks))
		for _, task := range ws.Tasks {
			if task.ID != taskID {
				newTasks = append(newTasks, task)
			}
		}

		// Update workspace
		ws.Tasks = newTasks
		ws.UpdatedAt = time.Now()

		// Save workspace
		if err := c.workspaceStore.Save(ws); err != nil {
			return fmt.Errorf("failed to save workspace: %w", err)
		}

		log.Printf("🗑️  Task %s deleted from workspace %s", taskID, wsID)
		return nil
	}

	return fmt.Errorf("task %s not found", taskID)
}
