package session

import (
	"context"
	"errors"

	"github.com/johnjallday/ori-agent/internal/database"
)

// Common errors returned by store operations.
var (
	ErrSessionNotFound   = errors.New("session not found")
	ErrWorkspaceNotFound = errors.New("workspace not found")
	ErrMessageNotFound   = errors.New("message not found")
	ErrNoteNotFound      = errors.New("note not found")
	ErrTaskNotFound      = errors.New("task not found")
	ErrReminderNotFound  = errors.New("reminder not found")
	ErrInvalidID         = errors.New("invalid ID format")
	ErrDuplicateID       = errors.New("duplicate ID")
)

// SessionStore defines the interface for session persistence operations.
// Implementations may use memory, SQLite, or a hybrid approach.
type SessionStore interface {
	// CreateSession creates a new session.
	// The session ID must be pre-populated with a valid UUID.
	CreateSession(ctx context.Context, session *Session) error

	// GetSession retrieves a session by ID, including all messages.
	// Returns ErrSessionNotFound if the session doesn't exist.
	GetSession(ctx context.Context, id string) (*Session, error)

	// UpdateSession updates session metadata (title, folder, tags).
	// Does not modify messages - use AddMessage for that.
	// Returns ErrSessionNotFound if the session doesn't exist.
	UpdateSession(ctx context.Context, session *Session) error

	// DeleteSession removes a session and all its messages.
	// Returns ErrSessionNotFound if the session doesn't exist.
	DeleteSession(ctx context.Context, id string) error

	// DeleteSessionsByAgent removes every session whose agent_name matches the
	// given name, along with their messages and other dependents. Used when an
	// agent is permanently deleted so the UI cannot resolve stale references.
	// Returns the number of sessions removed.
	DeleteSessionsByAgent(ctx context.Context, agentName string) (int, error)

	// ListSessions returns sessions matching the filter with pagination.
	// Sessions are returned without full message content for efficiency.
	ListSessions(ctx context.Context, filter *SessionFilter, opts *ListOptions) (*ListResult, error)

	// AddMessage appends a message to a session.
	// Updates the session's UpdatedAt timestamp and MessageCount.
	// Returns ErrSessionNotFound if the session doesn't exist.
	AddMessage(ctx context.Context, sessionID string, message *Message) error

	// GetMessages retrieves all messages for a session.
	// Messages are ordered by creation time (oldest first).
	// Returns ErrSessionNotFound if the session doesn't exist.
	GetMessages(ctx context.Context, sessionID string) ([]Message, error)

	// Search performs full-text search across session titles and message content.
	// Returns results with matching text snippets for display.
	Search(ctx context.Context, query string, filter *SessionFilter, opts *ListOptions) ([]SearchResult, int, error)

	// UpdateTags replaces all tags for a session.
	// Tags are normalized (lowercase, trimmed) before storage.
	UpdateTags(ctx context.Context, sessionID string, tags []string) error

	// GetAllTags returns all unique tags with usage counts.
	// Useful for tag autocomplete and management.
	GetAllTags(ctx context.Context) ([]Tag, error)
}

// WorkspaceStore defines the interface for workspace persistence operations.
type WorkspaceStore interface {
	// CreateWorkspace creates a new workspace.
	// The workspace ID must be pre-populated with a valid UUID.
	CreateWorkspace(ctx context.Context, workspace *Workspace) error

	// GetWorkspace retrieves a workspace by ID.
	// Returns ErrWorkspaceNotFound if the workspace doesn't exist.
	GetWorkspace(ctx context.Context, id string) (*Workspace, error)

	// UpdateWorkspace updates workspace metadata (name, parent, color).
	// Returns ErrWorkspaceNotFound if the workspace doesn't exist.
	UpdateWorkspace(ctx context.Context, workspace *Workspace) error

	// DeleteWorkspace removes a workspace.
	// Sessions in the workspace are moved to root (workspace_id set to empty).
	// Subworkspaces are also moved to root (parent_id set to empty).
	// Returns ErrWorkspaceNotFound if the workspace doesn't exist.
	DeleteWorkspace(ctx context.Context, id string) error

	// ListWorkspaces returns all workspaces as a flat list.
	ListWorkspaces(ctx context.Context) ([]Workspace, error)

	// GetWorkspaceTree returns workspaces organized as a tree structure.
	// Root-level workspaces have Children populated with their subworkspaces.
	GetWorkspaceTree(ctx context.Context) ([]Workspace, error)

	// GetSubworkspaceIDs returns all descendant workspace IDs for a given workspace.
	// Useful for filtering sessions including subworkspaces.
	GetSubworkspaceIDs(ctx context.Context, workspaceID string) ([]string, error)

	// DeleteSessionsByWorkspace deletes all sessions belonging to a workspace.
	DeleteSessionsByWorkspace(ctx context.Context, workspaceID string) error

	// UnlinkSessionsFromWorkspace sets workspace_id to NULL for all sessions in a workspace.
	UnlinkSessionsFromWorkspace(ctx context.Context, workspaceID string) error
}

// NoteStore defines the interface for workspace note persistence operations.
type NoteStore interface {
	// CreateNote creates a new workspace note.
	// The note ID must be pre-populated with a valid UUID.
	CreateNote(ctx context.Context, note *WorkspaceNote) error

	// GetNote retrieves a note by ID.
	// Returns ErrNoteNotFound if the note doesn't exist.
	GetNote(ctx context.Context, id string) (*WorkspaceNote, error)

	// UpdateNote updates note metadata and content.
	// Returns ErrNoteNotFound if the note doesn't exist.
	UpdateNote(ctx context.Context, note *WorkspaceNote) error

	// DeleteNote removes a note.
	// Returns ErrNoteNotFound if the note doesn't exist.
	DeleteNote(ctx context.Context, id string) error

	// ListNotesByWorkspace returns all notes in a workspace.
	// Notes are returned without full content for efficiency.
	ListNotesByWorkspace(ctx context.Context, workspaceID string) ([]WorkspaceNoteListItem, error)

	// SearchNotes performs full-text search across note names and content.
	// Returns results with matching text snippets for display.
	SearchNotes(ctx context.Context, query string, limit int) ([]NoteSearchResult, error)

	// SearchHeadings performs full-text search across note headings across all workspaces.
	SearchHeadings(ctx context.Context, query string, limit int) ([]HeadingSearchResult, error)

	// SearchBacklinks returns notes that link to the given note via wikilinks.
	SearchBacklinks(ctx context.Context, noteID string, limit int) ([]BacklinkResult, error)
}

// HybridStore combines SessionStore, WorkspaceStore, and NoteStore with caching capabilities.
// It provides the primary interface for session management with:
//   - In-memory LRU cache for active sessions
//   - SQLite persistence for durability
//   - Automatic eviction and on-demand loading
type HybridStore interface {
	SessionStore
	WorkspaceStore
	NoteStore

	// TouchSession marks a session as recently accessed.
	// This updates its position in the LRU cache.
	TouchSession(ctx context.Context, id string) error

	// IsSessionCached returns true if the session is in memory.
	IsSessionCached(id string) bool

	// GetCacheStats returns cache statistics for monitoring.
	GetCacheStats() CacheStats

	// FlushToStorage forces all cached sessions to be written to SQLite.
	// Useful before shutdown or for durability guarantees.
	FlushToStorage(ctx context.Context) error

	// Close releases resources and flushes pending writes.
	Close() error

	// Cleanup removes sessions inactive for the specified duration.
	// Returns the number of sessions deleted.
	Cleanup(ctx context.Context, inactiveDuration int) (int, error)

	// GetInactiveSessions returns sessions not updated in the specified number of days.
	// Useful for showing cleanup warnings before actual deletion.
	GetInactiveSessions(ctx context.Context, inactiveDays int) ([]*Session, error)

	// GetStorageStats returns statistics about session storage.
	GetStorageStats(ctx context.Context) (*StorageStats, error)

	// ToolCallStore returns the tool call store for persisting tool execution data.
	ToolCallStore() ToolCallStore

	// DB returns the underlying database connection for use by other components.
	DB() *database.DB
}

// CacheStats provides information about the memory cache state.
type CacheStats struct {
	// Size is the current number of sessions in memory.
	Size int `json:"size"`

	// MaxSize is the maximum number of sessions the cache can hold.
	MaxSize int `json:"max_size"`

	// Hits is the number of cache hits since startup.
	Hits int64 `json:"hits"`

	// Misses is the number of cache misses since startup.
	Misses int64 `json:"misses"`

	// Evictions is the number of sessions evicted to make room.
	Evictions int64 `json:"evictions"`
}

// HybridStoreConfig configures the hybrid store behavior.
type HybridStoreConfig struct {
	// CacheSize is the maximum number of sessions to keep in memory.
	// Default is 50.
	CacheSize int

	// DatabasePath is the path to the SQLite database file.
	// If empty, uses the default data directory.
	DatabasePath string

	// FlushInterval is how often to sync cached data to SQLite.
	// Default is 30 seconds.
	FlushInterval int

	// EnableFTS enables full-text search using SQLite FTS5.
	// Default is true.
	EnableFTS bool

	// EnableAutoCleanup enables automatic cleanup of inactive sessions.
	// Default is true.
	EnableAutoCleanup bool

	// CleanupThresholdDays is the number of days of inactivity before
	// a session is eligible for cleanup. Default is 30.
	CleanupThresholdDays int

	// CleanupCheckInterval is how often to check for inactive sessions
	// to clean up, in seconds. Default is 3600 (1 hour).
	CleanupCheckInterval int

	// MaxTotalSessions is the maximum number of sessions to store in the database.
	// When exceeded, oldest inactive sessions are deleted.
	// 0 means no limit. Default is 1000.
	MaxTotalSessions int
}

// DefaultHybridStoreConfig returns the default configuration.
func DefaultHybridStoreConfig() *HybridStoreConfig {
	return &HybridStoreConfig{
		CacheSize:            50,
		FlushInterval:        30,
		EnableFTS:            true,
		EnableAutoCleanup:    true,
		CleanupThresholdDays: 30,
		CleanupCheckInterval: 3600, // 1 hour
		MaxTotalSessions:     1000,
	}
}
