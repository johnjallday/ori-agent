package agentcomm

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Communicator handles inter-agent communication and task delegation
type Communicator struct {
	workspaceStore workspace.Store
	tasks          map[string]*Task // taskID -> Task
	tasksMu        sync.RWMutex
}

// NewCommunicator creates a new agent communicator
func NewCommunicator(workspaceStore workspace.Store) *Communicator {
	return &Communicator{
		workspaceStore: workspaceStore,
		tasks:          make(map[string]*Task),
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
func (c *Communicator) DelegateTask(req DelegationRequest) (*Task, error) {
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

	// Create task
	task := NewTask(req)

	// Store task
	c.tasksMu.Lock()
	c.tasks[task.ID] = task
	c.tasksMu.Unlock()

	// Send task request message
	if err := c.SendMessage(MessageRequest{
		WorkspaceID: req.WorkspaceID,
		From:        req.From,
		To:          req.To,
		Type:        workspace.MessageTaskRequest,
		Content:     req.Description,
		Metadata: map[string]interface{}{
			"task_id":  task.ID,
			"priority": req.Priority,
			"timeout":  req.Timeout.String(),
			"context":  req.Context,
		},
	}); err != nil {
		return nil, fmt.Errorf("failed to send task message: %w", err)
	}

	// Update task status
	task.Status = TaskStatusAssigned

	log.Printf("📋 Task %s delegated from %s to %s: %s", task.ID, req.From, req.To, req.Description)
	return task, nil
}

// GetTask retrieves a task by ID
func (c *Communicator) GetTask(taskID string) (*Task, error) {
	c.tasksMu.RLock()
	defer c.tasksMu.RUnlock()

	task, ok := c.tasks[taskID]
	if !ok {
		return nil, fmt.Errorf("task %s not found", taskID)
	}

	return task, nil
}

// UpdateTaskStatus updates the status of a task
func (c *Communicator) UpdateTaskStatus(taskID string, status TaskStatus, result string, errorMsg string) error {
	c.tasksMu.Lock()
	defer c.tasksMu.Unlock()

	task, ok := c.tasks[taskID]
	if !ok {
		return fmt.Errorf("task %s not found", taskID)
	}

	switch status {
	case TaskStatusInProgress:
		task.Start()
	case TaskStatusCompleted:
		task.Complete(result)
		// Send result message back to delegator
		c.sendTaskResult(task, result, "")
	case TaskStatusFailed:
		task.Fail(errorMsg)
		// Send failure message back to delegator
		c.sendTaskResult(task, "", errorMsg)
	case TaskStatusCancelled:
		task.Cancel()
	}

	log.Printf("📋 Task %s status updated: %s", taskID, status)
	return nil
}

// sendTaskResult sends the task result back to the delegating agent
func (c *Communicator) sendTaskResult(task *Task, result string, errorMsg string) {
	content := result
	if errorMsg != "" {
		content = fmt.Sprintf("Task failed: %s", errorMsg)
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
			"duration": task.Duration().String(),
		},
	}); err != nil {
		log.Printf("❌ Failed to send task result: %v", err)
	}
}

// ListTasks returns all tasks for a workspace
func (c *Communicator) ListTasks(workspaceID string) []*Task {
	c.tasksMu.RLock()
	defer c.tasksMu.RUnlock()

	var tasks []*Task
	for _, task := range c.tasks {
		if task.WorkspaceID == workspaceID {
			tasks = append(tasks, task)
		}
	}

	return tasks
}

// ListTasksForAgent returns all tasks assigned to or from a specific agent
func (c *Communicator) ListTasksForAgent(agentName string) []*Task {
	c.tasksMu.RLock()
	defer c.tasksMu.RUnlock()

	var tasks []*Task
	for _, task := range c.tasks {
		if task.From == agentName || task.To == agentName {
			tasks = append(tasks, task)
		}
	}

	return tasks
}

// CleanupCompletedTasks removes completed tasks older than the specified duration
func (c *Communicator) CleanupCompletedTasks(olderThan time.Duration) int {
	c.tasksMu.Lock()
	defer c.tasksMu.Unlock()

	cutoff := time.Now().Add(-olderThan)
	removed := 0

	for id, task := range c.tasks {
		if task.IsComplete() && task.CompletedAt != nil && task.CompletedAt.Before(cutoff) {
			delete(c.tasks, id)
			removed++
		}
	}

	if removed > 0 {
		log.Printf("🧹 Cleaned up %d completed tasks", removed)
	}

	return removed
}

// CheckTimeouts checks for tasks that have exceeded their timeout
func (c *Communicator) CheckTimeouts() []*Task {
	c.tasksMu.Lock()
	defer c.tasksMu.Unlock()

	var timedOut []*Task
	now := time.Now()

	for _, task := range c.tasks {
		if task.Status == TaskStatusInProgress && task.Timeout > 0 {
			if task.StartedAt != nil && now.Sub(*task.StartedAt) > task.Timeout {
				task.Status = TaskStatusTimeout
				completedAt := now
				task.CompletedAt = &completedAt
				timedOut = append(timedOut, task)
				log.Printf("⏰ Task %s timed out after %v", task.ID, task.Timeout)
			}
		}
	}

	return timedOut
}

// GetTaskStats returns statistics about tasks
func (c *Communicator) GetTaskStats(workspaceID string) map[string]int {
	c.tasksMu.RLock()
	defer c.tasksMu.RUnlock()

	stats := map[string]int{
		"total":       0,
		"pending":     0,
		"assigned":    0,
		"in_progress": 0,
		"completed":   0,
		"failed":      0,
		"cancelled":   0,
		"timeout":     0,
	}

	for _, task := range c.tasks {
		if task.WorkspaceID == workspaceID {
			stats["total"]++
			switch task.Status {
			case TaskStatusPending:
				stats["pending"]++
			case TaskStatusAssigned:
				stats["assigned"]++
			case TaskStatusInProgress:
				stats["in_progress"]++
			case TaskStatusCompleted:
				stats["completed"]++
			case TaskStatusFailed:
				stats["failed"]++
			case TaskStatusCancelled:
				stats["cancelled"]++
			case TaskStatusTimeout:
				stats["timeout"]++
			}
		}
	}

	return stats
}
