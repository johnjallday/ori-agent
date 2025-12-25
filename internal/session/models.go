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
//   - Folder: Hierarchical organization for sessions
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

// Folder represents a hierarchical folder for organizing sessions.
// Folders can be nested to create a tree structure.
type Folder struct {
	// ID is a unique identifier for the folder (UUID format).
	ID string `json:"id"`

	// Name is the display name of the folder.
	Name string `json:"name"`

	// ParentID is the ID of the parent folder, or empty for root-level folders.
	ParentID string `json:"parent_id,omitempty"`

	// Color is an optional hex color code for the folder icon.
	Color string `json:"color,omitempty"`

	// SessionCount is the number of sessions in this folder (not including subfolders).
	// This is denormalized for efficient display.
	SessionCount int `json:"session_count"`

	// CreatedAt is when the folder was created.
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt is when the folder was last modified.
	UpdatedAt time.Time `json:"updated_at"`

	// Children contains nested subfolders.
	// Only populated when building a tree structure for display.
	Children []Folder `json:"children,omitempty"`
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
