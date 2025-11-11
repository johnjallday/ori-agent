package agentstudio

import (
	"encoding/json"
	"fmt"
	"log"
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
	ID             string                 `json:"id"`
	Name           string                 `json:"name"`
	Description    string                 `json:"description,omitempty"`
	Agents         []string               `json:"agents"`
	SharedData     map[string]interface{} `json:"shared_data"`
	Messages       []AgentMessage         `json:"messages"`
	Tasks          []Task                 `json:"tasks"`
	ScheduledTasks []ScheduledTask        `json:"scheduled_tasks,omitempty"`
	Workflows      map[string]Workflow    `json:"workflows,omitempty"`
	Status         WorkspaceStatus        `json:"status"`
	CreatedAt      time.Time              `json:"created_at"`
	UpdatedAt      time.Time              `json:"updated_at"`
	mu             sync.RWMutex           `json:"-"`
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
	WorkspaceID string                 `json:"studio_id"`
	From        string                 `json:"from"`
	To          string                 `json:"to"`
	Description string                 `json:"description"`
	Priority    int                    `json:"priority"`
	Context     map[string]interface{} `json:"context"`
	Timeout     time.Duration          `json:"timeout"`
	Status      TaskStatus             `json:"status"`
	Result      string                 `json:"result,omitempty"`
	Error       string                 `json:"error,omitempty"`
	Progress    *TaskProgress          `json:"progress,omitempty"`
	// InputTaskIDs specifies task IDs whose results should be included as input context
	InputTaskIDs []string `json:"input_task_ids,omitempty"`
	// ResultCombinationMode specifies how to combine results from input tasks with the new task
	ResultCombinationMode string `json:"result_combination_mode,omitempty"`
	// CombinationInstruction provides custom instructions for how to combine results (used when mode is "custom")
	CombinationInstruction string     `json:"combination_instruction,omitempty"`
	CreatedAt              time.Time  `json:"created_at"`
	StartedAt              *time.Time `json:"started_at,omitempty"`
	CompletedAt            *time.Time `json:"completed_at,omitempty"`
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

// ResultCombinationMode specifies how to combine results from input tasks
type ResultCombinationMode string

const (
	// CombineModeDefault - Simply include input task results as context (existing behavior)
	CombineModeDefault ResultCombinationMode = "default"
	// CombineModeAppend - Append input results to the new task prompt
	CombineModeAppend ResultCombinationMode = "append"
	// CombineModeMerge - Ask the agent to merge/synthesize all input results
	CombineModeMerge ResultCombinationMode = "merge"
	// CombineModeSummarize - Ask the agent to create a summary of all input results
	CombineModeSummarize ResultCombinationMode = "summarize"
	// CombineModeCompare - Ask the agent to compare and contrast input results
	CombineModeCompare ResultCombinationMode = "compare"
	// CombineModeCustom - Use custom combination instruction provided by user
	CombineModeCustom ResultCombinationMode = "custom"
)

// TaskProgress tracks the execution progress of a task
type TaskProgress struct {
	Percentage     int       `json:"percentage"`             // 0-100
	CurrentStep    string    `json:"current_step,omitempty"` // e.g. "Analyzing data..."
	TotalSteps     int       `json:"total_steps,omitempty"`
	CompletedSteps int       `json:"completed_steps,omitempty"`
	ElapsedTimeMs  float64   `json:"elapsed_time_ms,omitempty"`
	UpdatedAt      time.Time `json:"updated_at,omitempty"`
}

// ScheduleType represents the type of schedule
type ScheduleType string

const (
	ScheduleOnce     ScheduleType = "once"     // Execute once at specific time
	ScheduleInterval ScheduleType = "interval" // Every X duration
	ScheduleDaily    ScheduleType = "daily"    // Every day at specific time
	ScheduleWeekly   ScheduleType = "weekly"   // Every week on specific day/time
	ScheduleCron     ScheduleType = "cron"     // Cron expression (advanced)
)

// ScheduleConfig defines how a scheduled task should be executed
type ScheduleConfig struct {
	Type ScheduleType `json:"type"` // Type of schedule

	// For "once" type
	ExecuteAt *time.Time `json:"execute_at,omitempty"`

	// For "interval" type
	Interval time.Duration `json:"interval,omitempty"` // e.g., 5m, 1h, 24h

	// For "cron" type
	CronExpr string `json:"cron_expr,omitempty"` // e.g., "0 9 * * *"

	// For "daily" type
	TimeOfDay string `json:"time_of_day,omitempty"` // e.g., "09:00", "14:30"

	// For "weekly" type
	DayOfWeek int `json:"day_of_week,omitempty"` // 0=Sunday, 1=Monday, ..., 6=Saturday

	// Limits
	MaxRuns int        `json:"max_runs,omitempty"` // 0 = infinite
	EndDate *time.Time `json:"end_date,omitempty"` // nil = no end date
}

// TaskExecution represents a single execution of a scheduled task
type TaskExecution struct {
	TaskID     string    `json:"task_id"`            // ID of the created Task
	ExecutedAt time.Time `json:"executed_at"`        // When it was triggered
	Status     string    `json:"status"`             // "success" or "failed"
	Error      string    `json:"error,omitempty"`    // Error message if failed
	Duration   int64     `json:"duration,omitempty"` // Execution duration in milliseconds (future)
}

// ScheduledTask represents a recurring or one-time scheduled task template
type ScheduledTask struct {
	ID          string                 `json:"id"`
	WorkspaceID string                 `json:"studio_id"`
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	From        string                 `json:"from"`   // Sender agent
	To          string                 `json:"to"`     // Recipient agent
	Prompt      string                 `json:"prompt"` // Task description/prompt
	Priority    int                    `json:"priority"`
	Context     map[string]interface{} `json:"context"`

	// Scheduling configuration
	Schedule ScheduleConfig `json:"schedule"`
	NextRun  *time.Time     `json:"next_run"`
	LastRun  *time.Time     `json:"last_run"`
	Enabled  bool           `json:"enabled"`

	// Execution tracking
	ExecutionCount int    `json:"execution_count"`
	FailureCount   int    `json:"failure_count"`
	LastResult     string `json:"last_result,omitempty"`
	LastError      string `json:"last_error,omitempty"`

	// Execution history (last 20 executions)
	ExecutionHistory []TaskExecution `json:"execution_history,omitempty"`

	// Metadata
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CreateWorkspaceParams contains parameters for creating a new workspace
type CreateWorkspaceParams struct {
	Name        string
	Description string
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
	if !w.hasAgent(msg.From) {
		return fmt.Errorf("sender %s is not part of workspace", msg.From)
	}

	// Validate recipient if specified
	if msg.To != "" && !w.hasAgent(msg.To) {
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
		"agents":        w.Agents,
		"agent_count":   len(w.Agents),
		"message_count": len(w.Messages),
		"task_count":    len(w.Tasks),
		"status":        w.Status,
		"created_at":    w.CreatedAt,
		"updated_at":    w.UpdatedAt,
	}
}

// AgentStats holds statistics for a single agent
type AgentStats struct {
	Name            string    `json:"name"`
	Status          string    `json:"status"` // "idle", "active", "busy", "error"
	CurrentTasks    []string  `json:"current_tasks"`
	QueuedTasks     []string  `json:"queued_tasks"`
	CompletedTasks  int       `json:"completed_tasks"`
	FailedTasks     int       `json:"failed_tasks"`
	TotalExecutions int       `json:"total_executions"`
	LastActive      time.Time `json:"last_active,omitempty"`
}

// GetAgentStats returns statistics for all agents in the workspace
func (w *Workspace) GetAgentStats() map[string]AgentStats {
	w.mu.RLock()
	defer w.mu.RUnlock()

	stats := make(map[string]AgentStats)

	// Initialize stats for all agents
	for _, agentName := range w.Agents {
		stats[agentName] = AgentStats{
			Name:         agentName,
			Status:       "idle",
			CurrentTasks: []string{},
			QueuedTasks:  []string{},
		}
	}

	// Analyze tasks to calculate agent stats
	for _, task := range w.Tasks {
		if task.To == "" || task.To == "unassigned" {
			continue
		}

		agentStat, exists := stats[task.To]
		if !exists {
			continue // Skip tasks for agents not in this workspace
		}

		switch task.Status {
		case TaskStatusInProgress:
			agentStat.CurrentTasks = append(agentStat.CurrentTasks, task.ID)
			agentStat.Status = "active"
			if task.StartedAt != nil && task.StartedAt.After(agentStat.LastActive) {
				agentStat.LastActive = *task.StartedAt
			}

		case TaskStatusPending, TaskStatusAssigned:
			agentStat.QueuedTasks = append(agentStat.QueuedTasks, task.ID)
			if agentStat.Status == "idle" {
				agentStat.Status = "queued"
			}

		case TaskStatusCompleted:
			agentStat.CompletedTasks++
			agentStat.TotalExecutions++
			if task.CompletedAt != nil && task.CompletedAt.After(agentStat.LastActive) {
				agentStat.LastActive = *task.CompletedAt
			}

		case TaskStatusFailed, TaskStatusTimeout:
			agentStat.FailedTasks++
			agentStat.TotalExecutions++
			if agentStat.Status == "idle" || agentStat.Status == "queued" {
				agentStat.Status = "error"
			}
			if task.CompletedAt != nil && task.CompletedAt.After(agentStat.LastActive) {
				agentStat.LastActive = *task.CompletedAt
			}
		}

		stats[task.To] = agentStat
	}

	// Determine if agent is busy (multiple tasks queued)
	for agentName, agentStat := range stats {
		if len(agentStat.QueuedTasks) > 5 {
			agentStat.Status = "busy"
			stats[agentName] = agentStat
		}
	}

	return stats
}

// WorkspaceProgress represents overall workspace progress metrics
type WorkspaceProgress struct {
	TotalTasks      int       `json:"total_tasks"`
	CompletedTasks  int       `json:"completed_tasks"`
	InProgressTasks int       `json:"in_progress_tasks"`
	PendingTasks    int       `json:"pending_tasks"`
	FailedTasks     int       `json:"failed_tasks"`
	Percentage      int       `json:"percentage"`
	StartedAt       time.Time `json:"started_at,omitempty"`
	EstimatedEnd    time.Time `json:"estimated_end,omitempty"`
	ElapsedTimeMs   float64   `json:"elapsed_time_ms,omitempty"`
	RemainingTimeMs float64   `json:"remaining_time_ms,omitempty"`
	ActiveAgents    int       `json:"active_agents"`
	IdleAgents      int       `json:"idle_agents"`
	TotalAgents     int       `json:"total_agents"`
	AverageTaskTime float64   `json:"average_task_time_ms,omitempty"`
}

// GetWorkspaceProgress calculates overall workspace progress
func (w *Workspace) GetWorkspaceProgress() WorkspaceProgress {
	w.mu.RLock()
	defer w.mu.RUnlock()

	progress := WorkspaceProgress{
		TotalTasks:  len(w.Tasks),
		TotalAgents: len(w.Agents),
	}

	if progress.TotalTasks == 0 {
		return progress
	}

	// Count tasks by status
	var firstStartTime time.Time
	var totalDuration float64
	var completedCount int

	for _, task := range w.Tasks {
		switch task.Status {
		case "completed":
			progress.CompletedTasks++
			completedCount++

			// Calculate task duration if we have timestamps
			if task.StartedAt != nil && task.CompletedAt != nil && !task.StartedAt.IsZero() && !task.CompletedAt.IsZero() {
				duration := task.CompletedAt.Sub(*task.StartedAt).Milliseconds()
				totalDuration += float64(duration)
			}
		case "in_progress":
			progress.InProgressTasks++

			// Track earliest start time
			if task.StartedAt != nil && !task.StartedAt.IsZero() {
				if firstStartTime.IsZero() || task.StartedAt.Before(firstStartTime) {
					firstStartTime = *task.StartedAt
				}
			}
		case "pending":
			progress.PendingTasks++
		case "failed":
			progress.FailedTasks++
		}

		// Track earliest task creation time for workspace start
		if !task.CreatedAt.IsZero() {
			if firstStartTime.IsZero() || task.CreatedAt.Before(firstStartTime) {
				firstStartTime = task.CreatedAt
			}
		}
	}

	// Calculate percentage (completed / total)
	if progress.TotalTasks > 0 {
		progress.Percentage = (progress.CompletedTasks * 100) / progress.TotalTasks
	}

	// Calculate average task time
	if completedCount > 0 {
		progress.AverageTaskTime = totalDuration / float64(completedCount)
	}

	// Calculate elapsed time
	if !firstStartTime.IsZero() {
		progress.StartedAt = firstStartTime
		progress.ElapsedTimeMs = float64(time.Since(firstStartTime).Milliseconds())
	}

	// Estimate remaining time based on average task time and remaining tasks
	if progress.AverageTaskTime > 0 {
		remainingTasks := progress.InProgressTasks + progress.PendingTasks
		progress.RemainingTimeMs = progress.AverageTaskTime * float64(remainingTasks)

		// Estimate completion time
		if progress.RemainingTimeMs > 0 {
			progress.EstimatedEnd = time.Now().Add(time.Duration(progress.RemainingTimeMs) * time.Millisecond)
		}
	}

	// Count active vs idle agents using agent stats
	agentStats := w.getAgentStatsUnlocked() // Use unlocked version since we already have read lock
	for _, stats := range agentStats {
		if stats.Status == "active" || stats.Status == "busy" {
			progress.ActiveAgents++
		} else {
			progress.IdleAgents++
		}
	}

	return progress
}

// getAgentStatsUnlocked is the unlocked version of GetAgentStats for internal use
func (w *Workspace) getAgentStatsUnlocked() map[string]AgentStats {
	stats := make(map[string]AgentStats)

	// Initialize stats for all agents
	for _, agentName := range w.Agents {
		stats[agentName] = AgentStats{
			Name:            agentName,
			Status:          "idle",
			CurrentTasks:    []string{},
			QueuedTasks:     []string{},
			CompletedTasks:  0,
			FailedTasks:     0,
			TotalExecutions: 0,
		}
	}

	// Analyze tasks to populate stats
	for _, task := range w.Tasks {
		// Skip unassigned tasks
		if task.To == "unassigned" || task.To == "" {
			continue
		}

		agentStat, exists := stats[task.To]
		if !exists {
			continue
		}

		switch task.Status {
		case "in_progress":
			agentStat.CurrentTasks = append(agentStat.CurrentTasks, task.ID)
			agentStat.Status = "active"
			if task.StartedAt != nil {
				agentStat.LastActive = *task.StartedAt
			}
			agentStat.TotalExecutions++

		case "pending":
			agentStat.QueuedTasks = append(agentStat.QueuedTasks, task.ID)
			// Set status to queued if not already active
			if agentStat.Status == "idle" {
				agentStat.Status = "queued"
			} else if agentStat.Status == "active" {
				agentStat.Status = "busy" // Active with queued tasks
			}

		case "completed":
			agentStat.CompletedTasks++
			agentStat.TotalExecutions++
			if task.CompletedAt != nil && !task.CompletedAt.IsZero() && task.CompletedAt.After(agentStat.LastActive) {
				agentStat.LastActive = *task.CompletedAt
			}

		case "failed":
			agentStat.FailedTasks++
			agentStat.TotalExecutions++
			agentStat.Status = "error"
			if task.CompletedAt != nil && !task.CompletedAt.IsZero() && task.CompletedAt.After(agentStat.LastActive) {
				agentStat.LastActive = *task.CompletedAt
			}
		}

		stats[task.To] = agentStat
	}

	// Determine if agent is busy (multiple tasks queued)
	for agentName, agentStat := range stats {
		if len(agentStat.QueuedTasks) > 5 {
			agentStat.Status = "busy"
			stats[agentName] = agentStat
		}
	}

	return stats
}

// AddTask adds a task to the workspace
func (w *Workspace) AddTask(task Task) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	log.Printf("[DEBUG] AddTask - Workspace: %s, Agents: %v", w.ID, w.Agents)
	log.Printf("[DEBUG] AddTask - Task: From=%s, To=%s", task.From, task.To)
	log.Printf("[DEBUG] AddTask - hasAgent(From): %v", w.hasAgent(task.From))

	// Validate sender is part of workspace
	// Allow "user" and "system" as special senders for UI-created tasks
	if task.From != "user" && task.From != "system" && !w.hasAgent(task.From) {
		log.Printf("[DEBUG] AddTask - Validation FAILED: From agent not valid")
		return fmt.Errorf("task delegator %s is not part of workspace", task.From)
	}

	// Validate recipient if specified
	// Allow "unassigned" as a special value for tasks without a specific recipient
	if task.To != "" && task.To != "unassigned" && !w.hasAgent(task.To) {
		log.Printf("[DEBUG] AddTask - Validation FAILED: To agent not valid")
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

// AddScheduledTask adds a scheduled task to the workspace
func (w *Workspace) AddScheduledTask(st ScheduledTask) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Validate sender is part of workspace
	if !w.hasAgent(st.From) {
		return fmt.Errorf("task delegator %s is not part of workspace", st.From)
	}

	// Validate recipient
	if st.To != "" && !w.hasAgent(st.To) {
		return fmt.Errorf("task recipient %s is not part of workspace", st.To)
	}

	// Set ID and timestamps if not set
	if st.ID == "" {
		st.ID = uuid.New().String()
	}
	if st.CreatedAt.IsZero() {
		st.CreatedAt = time.Now()
	}
	st.UpdatedAt = time.Now()

	// Ensure workspace ID matches
	st.WorkspaceID = w.ID

	w.ScheduledTasks = append(w.ScheduledTasks, st)
	w.UpdatedAt = time.Now()

	return nil
}

// GetScheduledTask retrieves a scheduled task by ID
func (w *Workspace) GetScheduledTask(id string) (*ScheduledTask, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	for i := range w.ScheduledTasks {
		if w.ScheduledTasks[i].ID == id {
			return &w.ScheduledTasks[i], nil
		}
	}

	return nil, fmt.Errorf("scheduled task %s not found in workspace", id)
}

// UpdateScheduledTask updates an existing scheduled task
func (w *Workspace) UpdateScheduledTask(st ScheduledTask) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	for i := range w.ScheduledTasks {
		if w.ScheduledTasks[i].ID == st.ID {
			st.UpdatedAt = time.Now()
			w.ScheduledTasks[i] = st
			w.UpdatedAt = time.Now()
			return nil
		}
	}

	return fmt.Errorf("scheduled task %s not found in workspace", st.ID)
}

// DeleteScheduledTask removes a scheduled task from the workspace
func (w *Workspace) DeleteScheduledTask(id string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	for i := range w.ScheduledTasks {
		if w.ScheduledTasks[i].ID == id {
			w.ScheduledTasks = append(w.ScheduledTasks[:i], w.ScheduledTasks[i+1:]...)
			w.UpdatedAt = time.Now()
			return nil
		}
	}

	return fmt.Errorf("scheduled task %s not found in workspace", id)
}

// GetEnabledScheduledTasks returns all enabled scheduled tasks
func (w *Workspace) GetEnabledScheduledTasks() []ScheduledTask {
	w.mu.RLock()
	defer w.mu.RUnlock()

	var enabled []ScheduledTask
	for _, st := range w.ScheduledTasks {
		if st.Enabled {
			enabled = append(enabled, st)
		}
	}
	return enabled
}

// GetTaskResults retrieves results from multiple tasks by their IDs
// Returns a map of task ID to task result, skipping tasks that don't exist or have no result
func (w *Workspace) GetTaskResults(taskIDs []string) map[string]string {
	w.mu.RLock()
	defer w.mu.RUnlock()

	results := make(map[string]string)
	for _, taskID := range taskIDs {
		for _, task := range w.Tasks {
			if task.ID == taskID {
				if task.Result != "" {
					results[taskID] = task.Result
				}
				break
			}
		}
	}
	return results
}

// GetInputContext builds a context map that includes results from input tasks
func (w *Workspace) GetInputContext(task *Task) map[string]interface{} {
	context := make(map[string]interface{})

	// Copy existing context
	for k, v := range task.Context {
		context[k] = v
	}

	// Add input task results if any
	if len(task.InputTaskIDs) > 0 {
		inputResults := w.GetTaskResults(task.InputTaskIDs)
		if len(inputResults) > 0 {
			context["input_task_results"] = inputResults
		}
	}

	return context
}
