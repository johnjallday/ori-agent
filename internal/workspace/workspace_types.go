package workspace

import (
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/types"
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

// AgentInstance represents a specific instance of an agent with a stable identifier
type AgentInstance struct {
	ID             string    `json:"id"`              // Stable UUID for this agent instance
	Name           string    `json:"name"`            // Agent type name (e.g., "default", "writer")
	InstanceNumber int       `json:"instance_number"` // Instance number for display (e.g., 1, 2, 3)
	NodeID         string    `json:"node_id"`         // Stable node ID (e.g., "default-node-1")
	CreatedAt      time.Time `json:"created_at"`      // When this instance was added
}

// Workspace stores shared context between collaborating agents
type Workspace struct {
	ID                   string                      `json:"id"`
	Name                 string                      `json:"name"`
	Description          string                      `json:"description,omitempty"`
	Agents               []string                    `json:"agents,omitempty"`          // Deprecated: Use AgentInstances instead. Auto-migrated by MigrateToAgentInstances().
	AgentInstances       []AgentInstance             `json:"agent_instances,omitempty"` // NEW: Stable agent instances with persistent IDs
	SharedData           map[string]interface{}      `json:"shared_data"`
	Messages             []AgentMessage              `json:"messages"`
	Tasks                []Task                      `json:"tasks"`
	PlannerDecision      *types.PlannerDecision      `json:"planner_decision,omitempty"`
	PendingPlan          *types.PendingPlan          `json:"pending_plan,omitempty"`
	DynamicAgentRequests []types.DynamicAgentRequest `json:"dynamic_agent_requests,omitempty"`
	Attachments          []Attachment                `json:"attachments,omitempty"`
	ScheduledTasks       []ScheduledTask             `json:"scheduled_tasks,omitempty"`
	StoreNodes           []StoreNode                 `json:"store_nodes,omitempty"`
	DirectoryReferences  []DirectoryReference        `json:"directory_references,omitempty"`
	MCPBindings          []WorkspaceMCPBinding       `json:"mcp_bindings,omitempty"`
	AgentMCPAccess       []WorkspaceAgentMCPAccess   `json:"agent_mcp_access,omitempty"`
	Workflows            map[string]Workflow         `json:"workflows,omitempty"`
	Layout               *CanvasLayout               `json:"layout,omitempty"` // Canvas layout (positions of tasks and agents)
	Status               WorkspaceStatus             `json:"status"`
	CreatedAt            time.Time                   `json:"created_at"`
	UpdatedAt            time.Time                   `json:"updated_at"`
	mu                   sync.RWMutex                `json:"-"`
	taskIndex            map[string]int              `json:"-"` // Index for O(1) task lookups by ID
}

// CanvasLayout stores positions of tasks and agents on the canvas
type CanvasLayout struct {
	TaskPositions       map[string]Position        `json:"task_positions,omitempty"`       // task ID -> position
	AgentPositions      map[string]Position        `json:"agent_positions,omitempty"`      // agent node ID -> position (falls back to name for legacy layouts)
	AttachmentPositions map[string]Position        `json:"attachment_positions,omitempty"` // attachment ID -> position
	SchedulerPositions  map[string]Position        `json:"scheduler_positions,omitempty"`  // scheduler node ID -> position
	StorePositions      map[string]Position        `json:"store_positions,omitempty"`      // store node ID -> position
	DirectoryPositions  map[string]Position        `json:"directory_positions,omitempty"`  // directory reference ID -> position
	WorkflowConnections []WorkflowConnectionLayout `json:"workflow_connections,omitempty"` // connections between tasks/agents
	Scale               float64                    `json:"scale,omitempty"`                // zoom level
	OffsetX             float64                    `json:"offset_x,omitempty"`             // pan offset X
	OffsetY             float64                    `json:"offset_y,omitempty"`             // pan offset Y
}

// Position represents a 2D position on the canvas
type Position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// WorkflowConnectionLayout represents a connection between nodes (task/agent)
type WorkflowConnectionLayout struct {
	ID       string `json:"id"`
	From     string `json:"from"`
	FromPort string `json:"fromPort"`
	To       string `json:"to"`
	ToPort   string `json:"toPort"`
	Color    string `json:"color,omitempty"`
	Animated bool   `json:"animated,omitempty"`
}

// AttachmentType represents the attachment content type
type AttachmentType string

const (
	AttachmentTypeDoc   AttachmentType = "doc"
	AttachmentTypeImage AttachmentType = "image"
	AttachmentTypeOther AttachmentType = "other"
)

// AttachmentFileMeta captures optional file information
type AttachmentFileMeta struct {
	Name string `json:"name,omitempty"`
	Size int64  `json:"size,omitempty"`
	Mime string `json:"mime,omitempty"`
	URL  string `json:"url,omitempty"`
}

// Attachment represents a note/file/link pinned to the workspace canvas
type Attachment struct {
	ID          string              `json:"id"`
	WorkspaceID string              `json:"workspace_id"`
	Title       string              `json:"title"`
	Body        string              `json:"body,omitempty"`
	Type        AttachmentType      `json:"type"`
	Color       string              `json:"color,omitempty"`
	LinkURL     string              `json:"link_url,omitempty"`
	File        *AttachmentFileMeta `json:"file_meta,omitempty"`
	X           float64             `json:"x"`
	Y           float64             `json:"y"`
	CreatedAt   time.Time           `json:"created_at"`
	UpdatedAt   time.Time           `json:"updated_at"`
	DeletedAt   *time.Time          `json:"deleted_at,omitempty"` // Soft-delete timestamp (nil = active, set = in trash)
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
	ID             string                 `json:"id"`
	WorkspaceID    string                 `json:"studio_id"`
	From           string                 `json:"from"`
	To             string                 `json:"to"`
	AssignedNodeID string                 `json:"assigned_node_id,omitempty"` // Specific agent instance (node) when multiple share a name
	Description    string                 `json:"description"`
	Details        string                 `json:"details,omitempty"`
	Priority       int                    `json:"priority"`
	Context        map[string]interface{} `json:"context"`
	Timeout        time.Duration          `json:"timeout"`
	Status         TaskStatus             `json:"status"`
	Result         string                 `json:"result,omitempty"`
	Error          string                 `json:"error,omitempty"`
	Progress       *TaskProgress          `json:"progress,omitempty"`
	ExecutionMode  TaskExecutionMode      `json:"execution_mode,omitempty"`
	ExecutionSteps []TaskExecutionStep    `json:"execution_steps,omitempty"`
	// InputTaskIDs specifies task IDs whose results should be included as input context
	InputTaskIDs []string `json:"input_task_ids,omitempty"`
	// ParentTaskID groups this task under a parent workflow task when set.
	ParentTaskID string `json:"parent_task_id,omitempty"`
	// SubtaskIndex is a 1-based ordering hint within the parent workflow.
	SubtaskIndex int        `json:"subtask_index,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`

	// Schedule fields - enables task to run on a schedule (re-runs same task)
	Schedule         *ScheduleConfig `json:"schedule,omitempty"`
	ScheduleEnabled  bool            `json:"schedule_enabled,omitempty"`
	ScheduleName     string          `json:"schedule_name,omitempty"`
	NextRun          *time.Time      `json:"next_run,omitempty"`
	LastRun          *time.Time      `json:"last_run,omitempty"`
	ExecutionCount   int             `json:"execution_count,omitempty"`
	FailureCount     int             `json:"failure_count,omitempty"`
	ExecutionHistory []TaskExecution `json:"execution_history,omitempty"`

	// Result storage configuration - auto-save results on completion
	ResultStorage *ResultStorageConfig `json:"result_storage,omitempty"`
}

// ResultStorageConfig specifies how task results should be automatically stored
type ResultStorageConfig struct {
	Enabled     bool   `json:"enabled"`                 // Enable auto-save on completion
	StoreNodeID string `json:"store_node_id,omitempty"` // Save to specific store node (if set)
	FilePath    string `json:"file_path,omitempty"`     // Custom file path (if no store node)
	Format      string `json:"format,omitempty"`        // Output format: text, json, markdown
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

// TaskProgress tracks the execution progress of a task
type TaskProgress struct {
	Percentage     int       `json:"percentage"`             // 0-100
	CurrentStep    string    `json:"current_step,omitempty"` // e.g. "Analyzing data..."
	TotalSteps     int       `json:"total_steps,omitempty"`
	CompletedSteps int       `json:"completed_steps,omitempty"`
	ElapsedTimeMs  float64   `json:"elapsed_time_ms,omitempty"`
	UpdatedAt      time.Time `json:"updated_at,omitempty"`
}

// TaskExecutionMode controls how internal execution steps advance.
type TaskExecutionMode string

const (
	TaskExecutionModeAuto        TaskExecutionMode = "auto"
	TaskExecutionModeStepThrough TaskExecutionMode = "step_through"
)

// TaskExecutionStepStatus tracks a single internal execution step.
type TaskExecutionStepStatus string

const (
	TaskExecutionStepPending    TaskExecutionStepStatus = "pending"
	TaskExecutionStepInProgress TaskExecutionStepStatus = "in_progress"
	TaskExecutionStepCompleted  TaskExecutionStepStatus = "completed"
	TaskExecutionStepFailed     TaskExecutionStepStatus = "failed"
	TaskExecutionStepBlocked    TaskExecutionStepStatus = "blocked"
	TaskExecutionStepSkipped    TaskExecutionStepStatus = "skipped"
)

// TaskExecutionStep represents a persisted internal step for a task.
type TaskExecutionStep struct {
	ID          string                  `json:"id"`
	Index       int                     `json:"index"`
	Title       string                  `json:"title"`
	Detail      string                  `json:"detail,omitempty"`
	Tag         string                  `json:"tag,omitempty"`
	Status      TaskExecutionStepStatus `json:"status"`
	Result      string                  `json:"result,omitempty"`
	Error       string                  `json:"error,omitempty"`
	StartedAt   *time.Time              `json:"started_at,omitempty"`
	CompletedAt *time.Time              `json:"completed_at,omitempty"`
}

// ScheduleType represents the type of schedule
type ScheduleType string

const (
	ScheduleOnce          ScheduleType = "once"           // Execute once at specific time
	ScheduleInterval      ScheduleType = "interval"       // Every X duration
	ScheduleDaily         ScheduleType = "daily"          // Every day at specific time
	ScheduleWeekly        ScheduleType = "weekly"         // Every week on specific day/time
	ScheduleCron          ScheduleType = "cron"           // Cron expression (advanced)
	ScheduleRelativeDelay ScheduleType = "relative_delay" // Delay from trigger/enable time
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

	// For "relative_delay" type
	DelayDuration time.Duration `json:"delay_duration,omitempty"` // e.g., 30s, 5m, 1h
	TriggerOnce   bool          `json:"trigger_once,omitempty"`   // true = one-time after delay, false = repeat after each delay

	// Limits
	MaxRuns int        `json:"max_runs,omitempty"` // 0 = infinite
	EndDate *time.Time `json:"end_date,omitempty"` // nil = no end date
}

// TaskExecution represents a single recorded run of a task.
type TaskExecution struct {
	TaskID     string    `json:"task_id"`            // ID of the executed task
	ExecutedAt time.Time `json:"executed_at"`        // When the run started
	Status     string    `json:"status"`             // "success", "failed", or "blocked"
	Summary    string    `json:"summary,omitempty"`  // Short result or failure summary
	Error      string    `json:"error,omitempty"`    // Full error message if failed or blocked
	Duration   int64     `json:"duration,omitempty"` // Execution duration in milliseconds
}

// ScheduledTask represents a recurring or one-time scheduled task template
type ScheduledTask struct {
	ID           string                 `json:"id"`
	WorkspaceID  string                 `json:"studio_id"`
	CanvasNodeID string                 `json:"canvas_node_id,omitempty"` // Links to canvas scheduler node (empty for dashboard-created tasks)
	TargetTaskID string                 `json:"target_task_id,omitempty"` // Links to a canvas task node to execute on schedule
	Name         string                 `json:"name"`
	Description  string                 `json:"description"`
	From         string                 `json:"from"`   // Sender agent
	To           string                 `json:"to"`     // Recipient agent
	Prompt       string                 `json:"prompt"` // Task description/prompt
	Priority     int                    `json:"priority"`
	Context      map[string]interface{} `json:"context"`

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

// StoreNode represents a file storage node on the canvas
type StoreNode struct {
	ID            string    `json:"id"`
	CanvasNodeID  string    `json:"canvas_node_id"`
	AgentNodeID   string    `json:"agent_node_id"` // Agent instance this store is connected to
	WorkspaceID   string    `json:"workspace_id"`
	Name          string    `json:"name"`
	BaseDir       string    `json:"base_dir"`   // Base directory (e.g., "reports/")
	Format        string    `json:"format"`     // "json", "text", "markdown", "binary"
	WriteMode     string    `json:"write_mode"` // "overwrite", "append"
	AutoCreateDir bool      `json:"auto_create_dir"`
	AutoStore     bool      `json:"auto_store"` // Automatically store task results on completion
	LastWriteTime time.Time `json:"last_write_time"`
	WriteCount    int       `json:"write_count"`
	LastError     string    `json:"last_error"`
	LastFilePath  string    `json:"last_file_path"` // Last written file (relative to base_dir)
	X             float64   `json:"x"`
	Y             float64   `json:"y"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// WorkspaceMCPBinding represents a concrete MCP binding owned by the workspace.
// ServerName maps to the globally configured MCP server template/definition.
type WorkspaceMCPBinding struct {
	ID         string                 `json:"id"`
	ServerName string                 `json:"server_name"`
	Alias      string                 `json:"alias,omitempty"`
	Enabled    bool                   `json:"enabled"`
	Scope      map[string]interface{} `json:"scope,omitempty"`
	Config     map[string]interface{} `json:"config,omitempty"`
	CreatedAt  time.Time              `json:"created_at,omitempty"`
	UpdatedAt  time.Time              `json:"updated_at,omitempty"`
}

// WorkspaceAgentMCPAccess narrows which workspace MCP bindings an agent instance
// may use. When no access entry exists for an instance, all enabled bindings are allowed.
type WorkspaceAgentMCPAccess struct {
	AgentInstanceID   string    `json:"agent_instance_id"`
	EnabledBindingIDs []string  `json:"enabled_binding_ids,omitempty"`
	UpdatedAt         time.Time `json:"updated_at,omitempty"`
}

// DirectoryReference represents a reference to a filesystem directory for reading files
type DirectoryReference struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Name        string    `json:"name"` // Display name for the directory
	Path        string    `json:"path"` // Absolute filesystem path to the directory
	X           float64   `json:"x"`    // Canvas position X
	Y           float64   `json:"y"`    // Canvas position Y
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// FileInfo represents information about a file in a directory
type FileInfo struct {
	Name         string    `json:"name"`          // File name
	RelativePath string    `json:"relative_path"` // Path relative to directory root
	Size         int64     `json:"size"`          // File size in bytes
	IsDir        bool      `json:"is_dir"`        // True if this is a directory
	ModTime      time.Time `json:"mod_time"`      // Last modification time
}

// CreateWorkspaceParams contains parameters for creating a new workspace
type CreateWorkspaceParams struct {
	Name        string
	Description string
	Agents      []string
	InitialData map[string]interface{}
}

// AgentStats holds statistics for a single agent
type AgentStats struct {
	Name            string    `json:"name"`
	Status          string    `json:"status"` // "idle", "active", "busy", "error", "queued"
	CurrentTasks    []string  `json:"current_tasks"`
	QueuedTasks     []string  `json:"queued_tasks"`
	CompletedTasks  int       `json:"completed_tasks"`
	FailedTasks     int       `json:"failed_tasks"`
	TotalExecutions int       `json:"total_executions"`
	LastActive      time.Time `json:"last_active,omitempty"`
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
