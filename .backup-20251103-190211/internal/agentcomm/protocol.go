package agentcomm

import (
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Task represents a delegated task from one agent to another
type Task struct {
	ID          string                 `json:"id"`
	WorkspaceID string                 `json:"workspace_id"`
	From        string                 `json:"from"`         // Agent delegating the task
	To          string                 `json:"to"`           // Agent receiving the task
	Description string                 `json:"description"`  // What needs to be done
	Priority    int                    `json:"priority"`     // 1 (low) to 5 (high)
	Context     map[string]interface{} `json:"context"`      // Additional context
	Timeout     time.Duration          `json:"timeout"`      // Max time to complete
	Status      TaskStatus             `json:"status"`       // Current status
	Result      string                 `json:"result"`       // Result when completed
	Error       string                 `json:"error"`        // Error message if failed
	CreatedAt   time.Time              `json:"created_at"`
	StartedAt   *time.Time             `json:"started_at,omitempty"`
	CompletedAt *time.Time             `json:"completed_at,omitempty"`
}

// TaskStatus represents the current state of a task
type TaskStatus string

const (
	TaskStatusPending   TaskStatus = "pending"    // Task created but not started
	TaskStatusAssigned  TaskStatus = "assigned"   // Task assigned to agent
	TaskStatusInProgress TaskStatus = "in_progress" // Agent is working on task
	TaskStatusCompleted TaskStatus = "completed"  // Task completed successfully
	TaskStatusFailed    TaskStatus = "failed"     // Task failed
	TaskStatusCancelled TaskStatus = "cancelled"  // Task was cancelled
	TaskStatusTimeout   TaskStatus = "timeout"    // Task exceeded timeout
)

// DelegationRequest represents a request to delegate a task
type DelegationRequest struct {
	WorkspaceID string                 `json:"workspace_id"`
	From        string                 `json:"from"`
	To          string                 `json:"to"`
	Description string                 `json:"description"`
	Priority    int                    `json:"priority"`
	Context     map[string]interface{} `json:"context"`
	Timeout     time.Duration          `json:"timeout"`
}

// DelegationResponse represents the response to a delegation request
type DelegationResponse struct {
	TaskID    string    `json:"task_id"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// MessageRequest is a convenient wrapper for sending messages
type MessageRequest struct {
	WorkspaceID string                 `json:"workspace_id"`
	From        string                 `json:"from"`
	To          string                 `json:"to"`          // Empty for broadcast
	Type        workspace.MessageType  `json:"type"`
	Content     string                 `json:"content"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// NewTask creates a new task with generated ID and timestamps
func NewTask(req DelegationRequest) *Task {
	now := time.Now()
	return &Task{
		ID:          uuid.New().String(),
		WorkspaceID: req.WorkspaceID,
		From:        req.From,
		To:          req.To,
		Description: req.Description,
		Priority:    req.Priority,
		Context:     req.Context,
		Timeout:     req.Timeout,
		Status:      TaskStatusPending,
		CreatedAt:   now,
	}
}

// Start marks the task as in progress
func (t *Task) Start() {
	now := time.Now()
	t.Status = TaskStatusInProgress
	t.StartedAt = &now
}

// Complete marks the task as completed with a result
func (t *Task) Complete(result string) {
	now := time.Now()
	t.Status = TaskStatusCompleted
	t.Result = result
	t.CompletedAt = &now
}

// Fail marks the task as failed with an error message
func (t *Task) Fail(errorMsg string) {
	now := time.Now()
	t.Status = TaskStatusFailed
	t.Error = errorMsg
	t.CompletedAt = &now
}

// Cancel marks the task as cancelled
func (t *Task) Cancel() {
	now := time.Now()
	t.Status = TaskStatusCancelled
	t.CompletedAt = &now
}

// IsComplete returns true if the task is in a terminal state
func (t *Task) IsComplete() bool {
	return t.Status == TaskStatusCompleted ||
		t.Status == TaskStatusFailed ||
		t.Status == TaskStatusCancelled ||
		t.Status == TaskStatusTimeout
}

// Duration returns how long the task took to complete (if completed)
func (t *Task) Duration() time.Duration {
	if t.CompletedAt == nil || t.StartedAt == nil {
		return 0
	}
	return t.CompletedAt.Sub(*t.StartedAt)
}
