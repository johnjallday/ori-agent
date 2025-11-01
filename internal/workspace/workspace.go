package workspace

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// WorkspaceStatus represents the current state of a workspace
type WorkspaceStatus string

const (
	StatusActive    WorkspaceStatus = "active"
	StatusCompleted WorkspaceStatus = "completed"
	StatusFailed    WorkspaceStatus = "failed"
	StatusCancelled WorkspaceStatus = "cancelled"
)

// MessageType represents the type of inter-agent message
type MessageType string

const (
	MessageTaskRequest MessageType = "task_request"
	MessageResult      MessageType = "result"
	MessageQuestion    MessageType = "question"
	MessageStatus      MessageType = "status"
)

// Workspace stores shared context between collaborating agents
type Workspace struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	ParentAgent string                 `json:"parent_agent"`
	Agents      []string               `json:"agents"`
	SharedData  map[string]interface{} `json:"shared_data"`
	Messages    []AgentMessage         `json:"messages"`
	Tasks       []Task                 `json:"tasks"`
	Workflows   map[string]Workflow    `json:"workflows,omitempty"`
	Status      WorkspaceStatus        `json:"status"`
	CreatedAt   time.Time              `json:"created_at"`
	UpdatedAt   time.Time              `json:"updated_at"`
	mu          sync.RWMutex           `json:"-"`
}

// AgentMessage represents a message passed between agents
type AgentMessage struct {
	ID        string                 `json:"id"`
	From      string                 `json:"from"`
	To        string                 `json:"to"` // empty = broadcast
	Type      MessageType            `json:"type"`
	Content   string                 `json:"content"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	Timestamp time.Time              `json:"timestamp"`
}

// Task represents a delegated task within a workspace
type Task struct {
	ID          string                 `json:"id"`
	WorkspaceID string                 `json:"workspace_id"`
	From        string                 `json:"from"`
	To          string                 `json:"to"`
	Description string                 `json:"description"`
	Priority    int                    `json:"priority"`
	Context     map[string]interface{} `json:"context"`
	Timeout     time.Duration          `json:"timeout"`
	Status      TaskStatus             `json:"status"`
	Result      string                 `json:"result,omitempty"`
	Error       string                 `json:"error,omitempty"`
	CreatedAt   time.Time              `json:"created_at"`
	StartedAt   *time.Time             `json:"started_at,omitempty"`
	CompletedAt *time.Time             `json:"completed_at,omitempty"`
}

// TaskStatus represents the current state of a task
type TaskStatus string

const (
	TaskStatusPending    TaskStatus = "pending"
	TaskStatusAssigned   TaskStatus = "assigned"
	TaskStatusInProgress TaskStatus = "in_progress"
	TaskStatusCompleted  TaskStatus = "completed"
	TaskStatusFailed     TaskStatus = "failed"
	TaskStatusCancelled  TaskStatus = "cancelled"
	TaskStatusTimeout    TaskStatus = "timeout"
)

// CreateWorkspaceParams contains parameters for creating a new workspace
type CreateWorkspaceParams struct {
	Name        string
	Description string
	ParentAgent string
	Agents      []string
	InitialData map[string]interface{}
}

// NewWorkspace creates a new workspace
func NewWorkspace(params CreateWorkspaceParams) *Workspace {
	now := time.Now()
	return &Workspace{
		ID:          uuid.New().String(),
		Name:        params.Name,
		Description: params.Description,
		ParentAgent: params.ParentAgent,
		Agents:      params.Agents,
		SharedData:  params.InitialData,
		Messages:    []AgentMessage{},
		Tasks:       []Task{},
		Status:      StatusActive,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

// AddMessage adds a message to the workspace
func (w *Workspace) AddMessage(msg AgentMessage) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Validate sender is part of workspace
	if !w.hasAgent(msg.From) && msg.From != w.ParentAgent {
		return fmt.Errorf("sender %s is not part of workspace", msg.From)
	}

	// Validate recipient if specified
	if msg.To != "" && !w.hasAgent(msg.To) && msg.To != w.ParentAgent {
		return fmt.Errorf("recipient %s is not part of workspace", msg.To)
	}

	// Set message ID and timestamp if not set
	if msg.ID == "" {
		msg.ID = uuid.New().String()
	}
	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now()
	}

	w.Messages = append(w.Messages, msg)
	w.UpdatedAt = time.Now()

	return nil
}

// GetMessagesForAgent returns all messages relevant to a specific agent
func (w *Workspace) GetMessagesForAgent(agentName string) []AgentMessage {
	w.mu.RLock()
	defer w.mu.RUnlock()

	var messages []AgentMessage
	for _, msg := range w.Messages {
		// Include messages sent to this agent, broadcast messages, or messages from this agent
		if msg.To == agentName || msg.To == "" || msg.From == agentName {
			messages = append(messages, msg)
		}
	}
	return messages
}

// GetMessagesSince returns messages added after the specified time
func (w *Workspace) GetMessagesSince(since time.Time) []AgentMessage {
	w.mu.RLock()
	defer w.mu.RUnlock()

	var messages []AgentMessage
	for _, msg := range w.Messages {
		if msg.Timestamp.After(since) {
			messages = append(messages, msg)
		}
	}
	return messages
}

// SetSharedData sets a value in the shared data store
func (w *Workspace) SetSharedData(key string, value interface{}) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.SharedData == nil {
		w.SharedData = make(map[string]interface{})
	}
	w.SharedData[key] = value
	w.UpdatedAt = time.Now()
}

// GetSharedData retrieves a value from the shared data store
func (w *Workspace) GetSharedData(key string) (interface{}, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	val, ok := w.SharedData[key]
	return val, ok
}

// AddAgent adds an agent to the workspace
func (w *Workspace) AddAgent(agentName string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.hasAgent(agentName) {
		return fmt.Errorf("agent %s already in workspace", agentName)
	}

	w.Agents = append(w.Agents, agentName)
	w.UpdatedAt = time.Now()
	return nil
}

// RemoveAgent removes an agent from the workspace
func (w *Workspace) RemoveAgent(agentName string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	for i, agent := range w.Agents {
		if agent == agentName {
			w.Agents = append(w.Agents[:i], w.Agents[i+1:]...)
			w.UpdatedAt = time.Now()
			return nil
		}
	}

	return fmt.Errorf("agent %s not found in workspace", agentName)
}

// SetStatus updates the workspace status
func (w *Workspace) SetStatus(status WorkspaceStatus) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.Status = status
	w.UpdatedAt = time.Now()
}

// GetStatus returns the current workspace status
func (w *Workspace) GetStatus() WorkspaceStatus {
	w.mu.RLock()
	defer w.mu.RUnlock()

	return w.Status
}

// hasAgent checks if an agent is part of the workspace (caller must hold lock)
func (w *Workspace) hasAgent(agentName string) bool {
	for _, agent := range w.Agents {
		if agent == agentName {
			return true
		}
	}
	return false
}

// HasAgent checks if an agent is part of the workspace (thread-safe)
func (w *Workspace) HasAgent(agentName string) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()

	return w.hasAgent(agentName)
}

// ToJSON serializes the workspace to JSON
func (w *Workspace) ToJSON() ([]byte, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	return json.MarshalIndent(w, "", "  ")
}

// FromJSON deserializes a workspace from JSON
func FromJSON(data []byte) (*Workspace, error) {
	var ws Workspace
	if err := json.Unmarshal(data, &ws); err != nil {
		return nil, err
	}
	return &ws, nil
}

// GetSummary returns a summary of the workspace
func (w *Workspace) GetSummary() map[string]interface{} {
	w.mu.RLock()
	defer w.mu.RUnlock()

	return map[string]interface{}{
		"id":            w.ID,
		"name":          w.Name,
		"description":   w.Description,
		"parent_agent":  w.ParentAgent,
		"agents":        w.Agents,
		"agent_count":   len(w.Agents),
		"message_count": len(w.Messages),
		"task_count":    len(w.Tasks),
		"status":        w.Status,
		"created_at":    w.CreatedAt,
		"updated_at":    w.UpdatedAt,
	}
}

// AddTask adds a task to the workspace
func (w *Workspace) AddTask(task Task) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Validate sender is part of workspace
	if !w.hasAgent(task.From) && task.From != w.ParentAgent {
		return fmt.Errorf("task delegator %s is not part of workspace", task.From)
	}

	// Validate recipient if specified
	if task.To != "" && !w.hasAgent(task.To) && task.To != w.ParentAgent {
		return fmt.Errorf("task recipient %s is not part of workspace", task.To)
	}

	// Set task ID and timestamp if not set
	if task.ID == "" {
		task.ID = uuid.New().String()
	}
	if task.CreatedAt.IsZero() {
		task.CreatedAt = time.Now()
	}

	// Ensure workspace ID matches
	task.WorkspaceID = w.ID

	w.Tasks = append(w.Tasks, task)
	w.UpdatedAt = time.Now()

	return nil
}

// GetTask retrieves a task by ID
func (w *Workspace) GetTask(taskID string) (*Task, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	for i := range w.Tasks {
		if w.Tasks[i].ID == taskID {
			return &w.Tasks[i], nil
		}
	}

	return nil, fmt.Errorf("task %s not found in workspace", taskID)
}

// UpdateTask updates an existing task in the workspace
func (w *Workspace) UpdateTask(task Task) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	for i := range w.Tasks {
		if w.Tasks[i].ID == task.ID {
			w.Tasks[i] = task
			w.UpdatedAt = time.Now()
			return nil
		}
	}

	return fmt.Errorf("task %s not found in workspace", task.ID)
}

// GetTasksForAgent returns all tasks assigned to a specific agent
func (w *Workspace) GetTasksForAgent(agentName string) []Task {
	w.mu.RLock()
	defer w.mu.RUnlock()

	var tasks []Task
	for _, task := range w.Tasks {
		if task.To == agentName {
			tasks = append(tasks, task)
		}
	}
	return tasks
}

// GetPendingTasksForAgent returns pending/assigned tasks for an agent
func (w *Workspace) GetPendingTasksForAgent(agentName string) []Task {
	w.mu.RLock()
	defer w.mu.RUnlock()

	var tasks []Task
	for _, task := range w.Tasks {
		if task.To == agentName &&
		   (task.Status == TaskStatusPending || task.Status == TaskStatusAssigned) {
			tasks = append(tasks, task)
		}
	}
	return tasks
}

// GetTaskStats returns statistics about tasks in the workspace
func (w *Workspace) GetTaskStats() map[string]int {
	w.mu.RLock()
	defer w.mu.RUnlock()

	stats := map[string]int{
		"total":       len(w.Tasks),
		"pending":     0,
		"assigned":    0,
		"in_progress": 0,
		"completed":   0,
		"failed":      0,
		"cancelled":   0,
		"timeout":     0,
	}

	for _, task := range w.Tasks {
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

	return stats
}
