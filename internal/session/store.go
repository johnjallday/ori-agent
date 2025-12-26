package session

import (
	"context"
	"errors"
)

// Common errors returned by store operations.
var (
	ErrSessionNotFound = errors.New("session not found")
	ErrFolderNotFound  = errors.New("folder not found")
	ErrMessageNotFound = errors.New("message not found")
	ErrNoteNotFound    = errors.New("note not found")
	ErrInvalidID       = errors.New("invalid ID format")
	ErrDuplicateID     = errors.New("duplicate ID")
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

// FolderStore defines the interface for folder persistence operations.
type FolderStore interface {
	// CreateFolder creates a new folder.
	// The folder ID must be pre-populated with a valid UUID.
	CreateFolder(ctx context.Context, folder *Folder) error

	// GetFolder retrieves a folder by ID.
	// Returns ErrFolderNotFound if the folder doesn't exist.
	GetFolder(ctx context.Context, id string) (*Folder, error)

	// UpdateFolder updates folder metadata (name, parent, color).
	// Returns ErrFolderNotFound if the folder doesn't exist.
	UpdateFolder(ctx context.Context, folder *Folder) error

	// DeleteFolder removes a folder.
	// Sessions in the folder are moved to root (folder_id set to empty).
	// Subfolders are also moved to root (parent_id set to empty).
	// Returns ErrFolderNotFound if the folder doesn't exist.
	DeleteFolder(ctx context.Context, id string) error

	// ListFolders returns all folders as a flat list.
	ListFolders(ctx context.Context) ([]Folder, error)

	// GetFolderTree returns folders organized as a tree structure.
	// Root-level folders have Children populated with their subfolders.
	GetFolderTree(ctx context.Context) ([]Folder, error)

	// GetSubfolderIDs returns all descendant folder IDs for a given folder.
	// Useful for filtering sessions including subfolders.
	GetSubfolderIDs(ctx context.Context, folderID string) ([]string, error)
}

// NoteStore defines the interface for folder note persistence operations.
type NoteStore interface {
	// CreateNote creates a new folder note.
	// The note ID must be pre-populated with a valid UUID.
	CreateNote(ctx context.Context, note *FolderNote) error

	// GetNote retrieves a note by ID.
	// Returns ErrNoteNotFound if the note doesn't exist.
	GetNote(ctx context.Context, id string) (*FolderNote, error)

	// UpdateNote updates note metadata and content.
	// Returns ErrNoteNotFound if the note doesn't exist.
	UpdateNote(ctx context.Context, note *FolderNote) error

	// DeleteNote removes a note.
	// Returns ErrNoteNotFound if the note doesn't exist.
	DeleteNote(ctx context.Context, id string) error

	// ListNotesByFolder returns all notes in a folder.
	// Notes are returned without full content for efficiency.
	ListNotesByFolder(ctx context.Context, folderID string) ([]FolderNoteListItem, error)

	// SearchNotes performs full-text search across note names and content.
	// Returns results with matching text snippets for display.
	SearchNotes(ctx context.Context, query string, limit int) ([]NoteSearchResult, error)
}

// HybridStore combines SessionStore, FolderStore, and NoteStore with caching capabilities.
// It provides the primary interface for session management with:
//   - In-memory LRU cache for active sessions
//   - SQLite persistence for durability
//   - Automatic eviction and on-demand loading
type HybridStore interface {
	SessionStore
	FolderStore
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
