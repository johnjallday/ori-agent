// Package session provides session management with hybrid memory + SQLite storage.
//
// Architecture Overview:
//
// The session system uses a hybrid storage approach combining in-memory caching
// with SQLite persistence. This provides fast access to active sessions while
// maintaining durability for all data.
//
// Key Components:
//   - Session: Represents a chat conversation with an agent
//   - Message: Individual messages within a session (user or assistant)
//   - Workspace: Hierarchical organization for sessions
//   - Tag: Labels for categorizing and filtering sessions
//
// Storage Strategy:
//   - Active sessions are kept in an LRU memory cache (default 50 sessions)
//   - When cache is full, least recently used sessions are evicted to SQLite
//   - On-demand loading brings sessions back to memory when accessed
//   - All writes are persisted to SQLite for durability
//
// Multi-Tab Support:
//   - Each browser tab gets a unique session automatically
//   - Session ID is passed via X-Session-ID header
//   - Multiple tabs can have different active sessions
package session

import (
	"time"
)

// MessageRole indicates whether a message is from the user or assistant.
type MessageRole string

const (
	RoleUser      MessageRole = "user"
	RoleAssistant MessageRole = "assistant"
	RoleSystem    MessageRole = "system"
)

// Session represents a chat conversation with an agent.
// Sessions contain messages and can be organized into folders with tags.
type Session struct {
	// ID is a unique identifier for the session (UUID format).
	ID string `json:"id"`

	// Title is a human-readable name for the session.
	// Auto-generated from the first message if not explicitly set.
	Title string `json:"title"`

	// AgentName is the name of the agent this session is associated with.
	AgentName string `json:"agent_name"`

	// FolderID is the optional folder this session belongs to.
	// Empty string means the session is in the root (uncategorized).
	FolderID string `json:"folder_id,omitempty"`

	// Tags are labels assigned to this session for filtering.
	Tags []string `json:"tags,omitempty"`

	// MessageCount is the total number of messages in the session.
	// This is denormalized for efficient listing without loading messages.
	MessageCount int `json:"message_count"`

	// CreatedAt is when the session was first created.
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt is when the session was last modified.
	// Updated on each new message or metadata change.
	UpdatedAt time.Time `json:"updated_at"`

	// Messages contains all messages in this session.
	// Only populated when the full session is loaded.
	Messages []Message `json:"messages,omitempty"`
}

// ToolCall represents a tool/function call made during a conversation.
// Tool calls are stored separately from messages to enable analysis of tool usage patterns.
type ToolCall struct {
	// ID is a unique identifier for the tool call (UUID format).
	ID string `json:"id"`

	// MessageID is the message that triggered this tool call.
	MessageID string `json:"message_id"`

	// SessionID is the session this tool call belongs to.
	SessionID string `json:"session_id"`

	// ToolName is the name of the tool/function that was called.
	ToolName string `json:"tool_name"`

	// Arguments is the JSON-encoded arguments passed to the tool.
	Arguments string `json:"arguments,omitempty"`

	// Result is the output returned by the tool (if successful).
	Result string `json:"result,omitempty"`

	// Error is the error message if the tool call failed.
	Error string `json:"error,omitempty"`

	// DurationMs is how long the tool execution took in milliseconds.
	DurationMs int `json:"duration_ms,omitempty"`

	// CreatedAt is when the tool call was made.
	CreatedAt time.Time `json:"created_at"`
}

// Message represents a single message in a session.
type Message struct {
	// ID is a unique identifier for the message (UUID format).
	ID string `json:"id"`

	// SessionID is the session this message belongs to.
	SessionID string `json:"session_id"`

	// Role indicates whether this is a user, assistant, or system message.
	Role MessageRole `json:"role"`

	// Content is the text content of the message.
	Content string `json:"content"`

	// Model is the LLM model used for assistant responses (empty for user messages).
	Model string `json:"model,omitempty"`

	// TokensUsed is the number of tokens consumed by this message.
	// Useful for cost tracking and analytics.
	TokensUsed int `json:"tokens_used,omitempty"`

	// CreatedAt is when the message was created.
	CreatedAt time.Time `json:"created_at"`
}

// WorkspaceStatus represents the current state of a workspace.
type WorkspaceStatus string

const (
	WorkspaceStatusActive    WorkspaceStatus = "active"
	WorkspaceStatusCompleted WorkspaceStatus = "completed"
	WorkspaceStatusFailed    WorkspaceStatus = "failed"
	WorkspaceStatusCancelled WorkspaceStatus = "cancelled"
)

// AgentInstance represents a specific instance of an agent in a workspace.
// This allows multiple instances of the same agent type with stable IDs.
type AgentInstance struct {
	// ID is a stable UUID for this agent instance.
	ID string `json:"id"`

	// Name is the agent type name (e.g., "default", "writer").
	Name string `json:"name"`

	// InstanceNumber is the sequential number for display (e.g., 1, 2, 3).
	InstanceNumber int `json:"instance_number"`

	// NodeID is a stable node ID (e.g., "default-node-1").
	NodeID string `json:"node_id"`

	// CreatedAt is when this instance was added.
	CreatedAt time.Time `json:"created_at"`
}

// Position represents a 2D position on the canvas.
type Position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// WorkflowConnectionLayout represents a connection between nodes on the canvas.
type WorkflowConnectionLayout struct {
	ID       string `json:"id"`
	From     string `json:"from"`
	FromPort string `json:"from_port,omitempty"`
	To       string `json:"to"`
	ToPort   string `json:"to_port,omitempty"`
	Color    string `json:"color,omitempty"`
	Animated bool   `json:"animated,omitempty"`
}

// CanvasLayout stores the visual arrangement of workspace elements.
type CanvasLayout struct {
	TaskPositions       map[string]Position        `json:"task_positions,omitempty"`
	AgentPositions      map[string]Position        `json:"agent_positions,omitempty"`
	AttachmentPositions map[string]Position        `json:"attachment_positions,omitempty"`
	SchedulerPositions  map[string]Position        `json:"scheduler_positions,omitempty"`
	StorePositions      map[string]Position        `json:"store_positions,omitempty"`
	WorkflowConnections []WorkflowConnectionLayout `json:"workflow_connections,omitempty"`
	Scale               float64                    `json:"scale,omitempty"`
	OffsetX             float64                    `json:"offset_x,omitempty"`
	OffsetY             float64                    `json:"offset_y,omitempty"`
}

// Workspace represents a hierarchical workspace for organizing sessions.
// Workspaces can be nested to create a tree structure.
// Workspaces also serve as unified containers for multi-agent orchestration.
type Workspace struct {
	// ID is a unique identifier for the workspace (UUID format).
	ID string `json:"id"`

	// Name is the display name of the workspace.
	Name string `json:"name"`

	// Description is an optional short description of the workspace's purpose.
	Description string `json:"description,omitempty"`

	// ParentID is the ID of the parent workspace, or empty for root-level workspaces.
	ParentID string `json:"parent_id,omitempty"`

	// Color is an optional hex color code for the workspace icon.
	Color string `json:"color,omitempty"`

	// SessionCount is the number of sessions in this workspace (not including subworkspaces).
	// This is denormalized for efficient display.
	SessionCount int `json:"session_count"`

	// CreatedAt is when the workspace was created.
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt is when the workspace was last modified.
	UpdatedAt time.Time `json:"updated_at"`

	// Children contains nested subworkspaces.
	// Only populated when building a tree structure for display.
	Children []Workspace `json:"children,omitempty"`

	// ==========================================================================
	// Orchestration Fields (for unified workspace functionality)
	// ==========================================================================

	// Agents is a list of agent names participating in this workspace.
	// Deprecated: Use AgentInstances for new code.
	Agents []string `json:"agents,omitempty"`

	// AgentInstances contains stable agent instances with persistent IDs.
	AgentInstances []AgentInstance `json:"agent_instances,omitempty"`

	// SharedData is a key-value store for inter-agent data sharing.
	SharedData map[string]interface{} `json:"shared_data,omitempty"`

	// Status is the current state of the workspace.
	Status WorkspaceStatus `json:"status,omitempty"`

	// Layout stores the visual arrangement of workspace elements on the canvas.
	Layout *CanvasLayout `json:"layout,omitempty"`
}

// Tag represents a unique tag used across sessions.
// Tags are stored normalized (lowercase, trimmed) for consistent matching.
type Tag struct {
	// Name is the normalized tag name.
	Name string `json:"name"`

	// UsageCount is how many sessions use this tag.
	// Useful for displaying popular tags and cleanup of unused tags.
	UsageCount int `json:"usage_count"`
}

// WorkspaceNote represents a markdown note attached to a workspace.
// Notes provide context and documentation that can be accessed
// by all sessions within the workspace.
type WorkspaceNote struct {
	// ID is a unique identifier for the note (UUID format).
	ID string `json:"id"`

	// WorkspaceID is the workspace this note belongs to.
	WorkspaceID string `json:"workspace_id"`

	// Name is the display name of the note.
	Name string `json:"name"`

	// Content is the markdown content of the note.
	Content string `json:"content"`

	// CreatedAt is when the note was created.
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt is when the note was last modified.
	UpdatedAt time.Time `json:"updated_at"`
}

// WorkspaceNoteListItem is a lightweight representation of a note for list views.
// It omits the full content to reduce payload size.
type WorkspaceNoteListItem struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Name        string    `json:"name"`
	Preview     string    `json:"preview,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// NoteSearchResult contains a note with search match context.
type NoteSearchResult struct {
	WorkspaceNoteListItem

	// WorkspaceName is the name of the workspace containing this note.
	WorkspaceName string `json:"workspace_name,omitempty"`

	// Snippets are text excerpts showing where the search matched.
	Snippets []string `json:"snippets,omitempty"`
}

// SessionListItem is a lightweight representation of a session for list views.
// It omits the full message content to reduce payload size.
type SessionListItem struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	AgentName    string    `json:"agent_name"`
	FolderID     string    `json:"folder_id,omitempty"`
	Tags         []string  `json:"tags,omitempty"`
	MessageCount int       `json:"message_count"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`

	// Preview is a snippet from the first message for display.
	Preview string `json:"preview,omitempty"`
}

// SessionFilter defines criteria for filtering and searching sessions.
type SessionFilter struct {
	// Query is a full-text search query matching title and message content.
	Query string `json:"query,omitempty"`

	// AgentName filters sessions by agent.
	AgentName string `json:"agent_name,omitempty"`

	// FolderID filters sessions by folder.
	// Use empty string for root sessions, or a specific folder ID.
	FolderID *string `json:"folder_id,omitempty"`

	// IncludeSubfolders when true includes sessions from nested folders.
	IncludeSubfolders bool `json:"include_subfolders,omitempty"`

	// Tags filters sessions that have ALL of the specified tags (AND logic).
	Tags []string `json:"tags,omitempty"`

	// AnyTags filters sessions that have ANY of the specified tags (OR logic).
	AnyTags []string `json:"any_tags,omitempty"`

	// CreatedAfter filters sessions created after this time.
	CreatedAfter *time.Time `json:"created_after,omitempty"`

	// CreatedBefore filters sessions created before this time.
	CreatedBefore *time.Time `json:"created_before,omitempty"`

	// UpdatedAfter filters sessions updated after this time.
	UpdatedAfter *time.Time `json:"updated_after,omitempty"`

	// UpdatedBefore filters sessions updated before this time.
	UpdatedBefore *time.Time `json:"updated_before,omitempty"`
}

// SessionSort defines sorting options for session lists.
type SessionSort string

const (
	SortByUpdatedDesc SessionSort = "updated_desc" // Most recently updated first (default)
	SortByUpdatedAsc  SessionSort = "updated_asc"  // Oldest updated first
	SortByCreatedDesc SessionSort = "created_desc" // Most recently created first
	SortByCreatedAsc  SessionSort = "created_asc"  // Oldest created first
	SortByTitleAsc    SessionSort = "title_asc"    // Alphabetical A-Z
	SortByTitleDesc   SessionSort = "title_desc"   // Alphabetical Z-A
)

// ListOptions defines pagination and sorting for list operations.
type ListOptions struct {
	// Limit is the maximum number of results to return.
	// Default is 50, max is 100.
	Limit int `json:"limit,omitempty"`

	// Offset is the number of results to skip for pagination.
	Offset int `json:"offset,omitempty"`

	// Sort defines the sort order.
	Sort SessionSort `json:"sort,omitempty"`
}

// ListResult contains paginated results with metadata.
type ListResult struct {
	// Sessions is the list of sessions matching the filter.
	Sessions []SessionListItem `json:"sessions"`

	// Total is the total number of sessions matching the filter (before pagination).
	Total int `json:"total"`

	// HasMore indicates if there are more results after this page.
	HasMore bool `json:"has_more"`
}

// SearchResult contains a session with search match context.
type SearchResult struct {
	SessionListItem

	// Snippets are text excerpts showing where the search matched.
	Snippets []string `json:"snippets,omitempty"`
}

// StorageStats contains statistics about session storage.
type StorageStats struct {
	// TotalSessions is the total number of sessions in storage.
	TotalSessions int `json:"total_sessions"`

	// TotalMessages is the total number of messages across all sessions.
	TotalMessages int `json:"total_messages"`

	// InactiveSessions30Days is the number of sessions not updated in 30+ days.
	InactiveSessions30Days int `json:"inactive_sessions_30_days"`

	// OldestSessionDate is when the oldest session was last updated.
	OldestSessionDate time.Time `json:"oldest_session_date,omitempty"`

	// DatabaseSizeBytes is the approximate size of the SQLite database.
	DatabaseSizeBytes int64 `json:"database_size_bytes"`

	// CachedSessions is the number of sessions currently in memory cache.
	CachedSessions int `json:"cached_sessions"`
}

// =============================================================================
// Session Tasks
// =============================================================================

// TaskStatus represents the current state of a task.
type TaskStatus string

const (
	TaskStatusPending    TaskStatus = "pending"
	TaskStatusInProgress TaskStatus = "in_progress"
	TaskStatusCompleted  TaskStatus = "completed"
	TaskStatusCancelled  TaskStatus = "cancelled"
)

// SessionTask represents a todo item or task attached to a workspace.
// Tasks are workspace-scoped and visible from any session within that workspace.
type SessionTask struct {
	// ID is a unique identifier for the task (UUID format).
	ID string `json:"id"`

	// WorkspaceID is the workspace (folder) this task belongs to.
	// Tasks are workspace-scoped and can be viewed/executed from any session in that workspace.
	WorkspaceID string `json:"workspace_id"`

	// Description is the task title/summary.
	Description string `json:"description"`

	// Details contains additional information about the task.
	Details string `json:"details,omitempty"`

	// Status is the current state of the task.
	Status TaskStatus `json:"status"`

	// Priority is the task priority (1-5, higher = more important).
	Priority int `json:"priority"`

	// CreatedAt is when the task was created.
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt is when the task was last modified.
	UpdatedAt time.Time `json:"updated_at"`

	// CompletedAt is when the task was marked complete.
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// SessionTaskListItem is a lightweight representation of a task for list views.
type SessionTaskListItem struct {
	ID          string     `json:"id"`
	WorkspaceID string     `json:"workspace_id"`
	Description string     `json:"description"`
	Details     string     `json:"details,omitempty"`
	Status      TaskStatus `json:"status"`
	Priority    int        `json:"priority"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// TaskCounts contains aggregated task statistics for a session or workspace.
type TaskCounts struct {
	Total     int `json:"total"`
	Pending   int `json:"pending"`
	Completed int `json:"completed"`
}

// =============================================================================
// Scheduled Task Reminders
// =============================================================================

// ReminderScheduleType represents the type of schedule for a reminder.
type ReminderScheduleType string

const (
	ReminderOnce   ReminderScheduleType = "once"   // Execute once at specific time
	ReminderDaily  ReminderScheduleType = "daily"  // Every day at specific time
	ReminderWeekly ReminderScheduleType = "weekly" // Every week on specific day/time
)

// ScheduledTaskReminder represents a recurring or one-time reminder.
// Unlike agentstudio scheduled tasks, these are reminders only (no agent execution).
type ScheduledTaskReminder struct {
	// ID is a unique identifier for the reminder (UUID format).
	ID string `json:"id"`

	// WorkspaceID is the workspace this reminder belongs to.
	WorkspaceID string `json:"workspace_id"`

	// Name is a short title for the reminder.
	Name string `json:"name"`

	// Description is additional details about the reminder.
	Description string `json:"description,omitempty"`

	// ScheduleType defines how the reminder repeats.
	ScheduleType ReminderScheduleType `json:"schedule_type"`

	// ExecuteAt is for "once" type - when to trigger the reminder.
	ExecuteAt *time.Time `json:"execute_at,omitempty"`

	// TimeOfDay is for "daily" type - time to trigger (e.g., "09:00").
	TimeOfDay string `json:"time_of_day,omitempty"`

	// DayOfWeek is for "weekly" type - 0=Sunday, 1=Monday, ..., 6=Saturday.
	DayOfWeek int `json:"day_of_week,omitempty"`

	// NextRun is the calculated next trigger time.
	NextRun *time.Time `json:"next_run,omitempty"`

	// LastRun is when the reminder last triggered.
	LastRun *time.Time `json:"last_run,omitempty"`

	// Enabled indicates if the reminder is active.
	Enabled bool `json:"enabled"`

	// CreatedAt is when the reminder was created.
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt is when the reminder was last modified.
	UpdatedAt time.Time `json:"updated_at"`
}
