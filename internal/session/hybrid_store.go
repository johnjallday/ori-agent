package session

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/logger"
)

// hybridStore implements HybridStore using an LRU memory cache backed by SQLite.
type hybridStore struct {
	cache         *MemoryCache
	sqlite        *SQLiteStore
	toolCallStore *SQLiteToolCallStore
	taskStore     *SQLiteTaskStore
	db            *database.DB

	mu     sync.RWMutex
	stopCh chan struct{}
	config *HybridStoreConfig
}

// NewHybridStore creates a new hybrid store with the given configuration.
func NewHybridStore(ctx context.Context, cfg *HybridStoreConfig) (HybridStore, error) {
	if cfg == nil {
		cfg = DefaultHybridStoreConfig()
	}

	// Open database
	dbCfg := &database.Config{
		Path:    cfg.DatabasePath,
		WALMode: true,
	}

	db, err := database.Open(ctx, dbCfg)
	if err != nil {
		return nil, err
	}

	store := &hybridStore{
		cache:         NewMemoryCache(cfg.CacheSize),
		sqlite:        NewSQLiteStore(db),
		toolCallStore: NewSQLiteToolCallStore(db),
		taskStore:     NewSQLiteTaskStore(db),
		db:            db,
		stopCh:        make(chan struct{}),
		config:        cfg,
	}

	// Enforce memory limits on startup
	if err := store.enforceStorageLimits(ctx); err != nil {
		logger.Warn("Failed to enforce storage limits on startup", logger.Fields{"error": err})
	}

	// Start periodic flush
	if cfg.FlushInterval > 0 {
		store.startPeriodicFlush(time.Duration(cfg.FlushInterval) * time.Second)
	}

	// Start periodic cleanup if enabled
	if cfg.EnableAutoCleanup && cfg.CleanupCheckInterval > 0 {
		store.startPeriodicCleanup(time.Duration(cfg.CleanupCheckInterval) * time.Second)
	}

	return store, nil
}

// NewHybridStoreWithDB creates a hybrid store with an existing database connection.
// This is useful for testing or when the database is managed externally.
func NewHybridStoreWithDB(db *database.DB, cacheSize int) HybridStore {
	if cacheSize <= 0 {
		cacheSize = 50
	}

	return &hybridStore{
		cache:         NewMemoryCache(cacheSize),
		sqlite:        NewSQLiteStore(db),
		toolCallStore: NewSQLiteToolCallStore(db),
		taskStore:     NewSQLiteTaskStore(db),
		db:            db,
		stopCh:        make(chan struct{}),
		config:        &HybridStoreConfig{CacheSize: cacheSize},
	}
}

// CreateSession creates a new session.
func (h *hybridStore) CreateSession(ctx context.Context, session *Session) error {
	// Generate ID if not set
	if session.ID == "" {
		session.ID = uuid.New().String()
	}

	// Set timestamps
	now := time.Now()
	if session.CreatedAt.IsZero() {
		session.CreatedAt = now
	}
	session.UpdatedAt = now

	// Save to SQLite first for durability
	if err := h.sqlite.CreateSession(ctx, session); err != nil {
		return err
	}

	// Add to cache
	h.cache.Put(session.ID, session)

	logger.Debug("Session created", logger.Fields{"id": session.ID, "title": session.Title})

	return nil
}

// GetSession retrieves a session by ID.
func (h *hybridStore) GetSession(ctx context.Context, id string) (*Session, error) {
	// Try cache first
	if session := h.cache.Get(id); session != nil {
		return session, nil
	}

	// Load from SQLite
	session, err := h.sqlite.GetSession(ctx, id)
	if err != nil {
		return nil, err
	}

	// Add to cache (may evict oldest)
	evicted := h.cache.Put(id, session)
	if evicted != nil {
		// The evicted session is already in SQLite, just log it
		logger.Debug("Session evicted from cache", logger.Fields{"id": evicted.ID})
	}

	return session, nil
}

// UpdateSession updates session metadata.
func (h *hybridStore) UpdateSession(ctx context.Context, session *Session) error {
	session.UpdatedAt = time.Now()

	// Update in SQLite
	if err := h.sqlite.UpdateSession(ctx, session); err != nil {
		return err
	}

	// Update in cache if present
	if h.cache.Contains(session.ID) {
		h.cache.Put(session.ID, session)
	}

	return nil
}

// DeleteSession removes a session.
func (h *hybridStore) DeleteSession(ctx context.Context, id string) error {
	// Remove from cache
	h.cache.Remove(id)

	// Remove from SQLite
	return h.sqlite.DeleteSession(ctx, id)
}

// ListSessions returns sessions matching the filter.
func (h *hybridStore) ListSessions(ctx context.Context, filter *SessionFilter, opts *ListOptions) (*ListResult, error) {
	// Always use SQLite for listing to ensure completeness
	return h.sqlite.ListSessions(ctx, filter, opts)
}

// AddMessage appends a message to a session.
func (h *hybridStore) AddMessage(ctx context.Context, sessionID string, message *Message) error {
	// Generate ID if not set
	if message.ID == "" {
		message.ID = uuid.New().String()
	}
	message.SessionID = sessionID

	if message.CreatedAt.IsZero() {
		message.CreatedAt = time.Now()
	}

	// Add to SQLite
	if err := h.sqlite.AddMessage(ctx, sessionID, message); err != nil {
		return err
	}

	// Update cache if session is cached (protected by mutex for concurrent access)
	h.mu.Lock()
	if session := h.cache.Get(sessionID); session != nil {
		session.Messages = append(session.Messages, *message)
		session.MessageCount++
		session.UpdatedAt = time.Now()

		// Auto-generate title from first message
		if session.Title == "" || session.Title == "New Session" {
			session.Title = generateTitle(message.Content)
			// Update title in SQLite too
			_ = h.sqlite.UpdateSession(ctx, session)
		}
	}
	h.mu.Unlock()

	return nil
}

// GetMessages retrieves all messages for a session.
func (h *hybridStore) GetMessages(ctx context.Context, sessionID string) ([]Message, error) {
	// Try cache first
	if session := h.cache.Get(sessionID); session != nil && len(session.Messages) > 0 {
		return session.Messages, nil
	}

	return h.sqlite.GetMessages(ctx, sessionID)
}

// Search performs full-text search.
func (h *hybridStore) Search(ctx context.Context, query string, filter *SessionFilter, opts *ListOptions) ([]SearchResult, int, error) {
	return h.sqlite.Search(ctx, query, filter, opts)
}

// UpdateTags replaces all tags for a session.
func (h *hybridStore) UpdateTags(ctx context.Context, sessionID string, tags []string) error {
	if err := h.sqlite.UpdateTags(ctx, sessionID, tags); err != nil {
		return err
	}

	// Update cache if present (protected by mutex for concurrent access)
	h.mu.Lock()
	if session := h.cache.Get(sessionID); session != nil {
		session.Tags = tags
	}
	h.mu.Unlock()

	return nil
}

// GetAllTags returns all unique tags.
func (h *hybridStore) GetAllTags(ctx context.Context) ([]Tag, error) {
	return h.sqlite.GetAllTags(ctx)
}

// CreateWorkspace creates a new workspace.
func (h *hybridStore) CreateWorkspace(ctx context.Context, workspace *Workspace) error {
	if workspace.ID == "" {
		workspace.ID = uuid.New().String()
	}

	now := time.Now()
	if workspace.CreatedAt.IsZero() {
		workspace.CreatedAt = now
	}
	workspace.UpdatedAt = now

	return h.sqlite.CreateWorkspace(ctx, workspace)
}

// GetWorkspace retrieves a workspace by ID.
func (h *hybridStore) GetWorkspace(ctx context.Context, id string) (*Workspace, error) {
	return h.sqlite.GetWorkspace(ctx, id)
}

// UpdateWorkspace updates workspace metadata.
func (h *hybridStore) UpdateWorkspace(ctx context.Context, workspace *Workspace) error {
	workspace.UpdatedAt = time.Now()
	return h.sqlite.UpdateWorkspace(ctx, workspace)
}

// DeleteWorkspace removes a workspace.
func (h *hybridStore) DeleteWorkspace(ctx context.Context, id string) error {
	return h.sqlite.DeleteWorkspace(ctx, id)
}

// ListWorkspaces returns all workspaces.
func (h *hybridStore) ListWorkspaces(ctx context.Context) ([]Workspace, error) {
	return h.sqlite.ListWorkspaces(ctx)
}

// GetWorkspaceTree returns workspaces as a tree.
func (h *hybridStore) GetWorkspaceTree(ctx context.Context) ([]Workspace, error) {
	return h.sqlite.GetWorkspaceTree(ctx)
}

// GetSubworkspaceIDs returns all descendant workspace IDs.
func (h *hybridStore) GetSubworkspaceIDs(ctx context.Context, workspaceID string) ([]string, error) {
	return h.sqlite.GetSubworkspaceIDs(ctx, workspaceID)
}

// TouchSession marks a session as recently accessed.
func (h *hybridStore) TouchSession(ctx context.Context, id string) error {
	// If in cache, just touch it
	if h.cache.Touch(id) {
		return nil
	}

	// Load into cache if not present
	_, err := h.GetSession(ctx, id)
	return err
}

// IsSessionCached returns true if the session is in memory.
func (h *hybridStore) IsSessionCached(id string) bool {
	return h.cache.Contains(id)
}

// GetCacheStats returns cache statistics.
func (h *hybridStore) GetCacheStats() CacheStats {
	return h.cache.Stats()
}

// FlushToStorage forces all cached sessions to be written to SQLite.
func (h *hybridStore) FlushToStorage(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	sessions := h.cache.GetAll()
	for _, session := range sessions {
		if err := h.sqlite.UpdateSession(ctx, session); err != nil {
			logger.Warn("Failed to flush session to storage", logger.Fields{
				"id":    session.ID,
				"error": err,
			})
		}
	}

	logger.Debug("Flushed sessions to storage", logger.Fields{"count": len(sessions)})
	return nil
}

// Close releases resources.
func (h *hybridStore) Close() error {
	// Stop periodic flush
	if h.stopCh != nil {
		close(h.stopCh)
	}

	// Final flush
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = h.FlushToStorage(ctx)

	// Close database
	return h.db.Close()
}

// Cleanup removes sessions inactive for the specified number of days.
func (h *hybridStore) Cleanup(ctx context.Context, inactiveDays int) (int, error) {
	cutoff := time.Now().AddDate(0, 0, -inactiveDays)

	// First, get IDs of sessions to be deleted so we can remove from cache
	rows, err := h.db.QueryContext(ctx, "SELECT id FROM sessions WHERE updated_at < ?", cutoff)
	if err != nil {
		return 0, err
	}
	var idsToDelete []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			idsToDelete = append(idsToDelete, id)
		}
	}
	_ = rows.Close()

	// Remove from cache first
	for _, id := range idsToDelete {
		h.cache.Remove(id)
	}

	// Delete from database
	result, err := h.db.ExecContext(ctx, `
		DELETE FROM sessions WHERE updated_at < ?
	`, cutoff)

	if err != nil {
		return 0, err
	}

	deleted, _ := result.RowsAffected()

	logger.Info("Session cleanup completed", logger.Fields{
		"deleted":     deleted,
		"cutoff_days": inactiveDays,
		"cutoff_date": cutoff.Format("2006-01-02"),
	})

	return int(deleted), nil
}

// GetInactiveSessions returns sessions that haven't been updated in the specified number of days.
// This is useful for showing cleanup warnings before actual deletion.
func (h *hybridStore) GetInactiveSessions(ctx context.Context, inactiveDays int) ([]*Session, error) {
	cutoff := time.Now().AddDate(0, 0, -inactiveDays)

	rows, err := h.db.QueryContext(ctx, `
		SELECT id, title, agent_name, workspace_id, message_count, created_at, updated_at
		FROM sessions
		WHERE updated_at < ?
		ORDER BY updated_at ASC
	`, cutoff)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var sessions []*Session
	for rows.Next() {
		var s Session
		if err := rows.Scan(&s.ID, &s.Title, &s.AgentName, &s.FolderID,
			&s.MessageCount, &s.CreatedAt, &s.UpdatedAt); err != nil {
			continue
		}
		sessions = append(sessions, &s)
	}

	return sessions, nil
}

// GetStorageStats returns statistics about session storage.
func (h *hybridStore) GetStorageStats(ctx context.Context) (*StorageStats, error) {
	stats := &StorageStats{}

	// Get total session count
	err := h.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions").Scan(&stats.TotalSessions)
	if err != nil {
		return nil, err
	}

	// Get total message count
	err = h.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM messages").Scan(&stats.TotalMessages)
	if err != nil {
		return nil, err
	}

	// Get oldest session
	var oldestStr sql.NullString
	err = h.db.QueryRowContext(ctx, "SELECT MIN(updated_at) FROM sessions").Scan(&oldestStr)
	if err == nil && oldestStr.Valid {
		stats.OldestSessionDate, _ = time.Parse(time.RFC3339, oldestStr.String)
	}

	// Get sessions inactive for 30+ days
	cutoff30 := time.Now().AddDate(0, 0, -30)
	err = h.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions WHERE updated_at < ?", cutoff30).Scan(&stats.InactiveSessions30Days)
	if err != nil {
		stats.InactiveSessions30Days = 0
	}

	// Get database file size (approximate)
	stats.DatabaseSizeBytes = h.getDatabaseSize()

	// Get cache stats
	stats.CachedSessions = h.cache.Len()

	return stats, nil
}

// ToolCallStore returns the tool call store for persisting tool execution data.
func (h *hybridStore) ToolCallStore() ToolCallStore {
	return h.toolCallStore
}

// TaskStore returns the task store for session/workspace tasks.
func (h *hybridStore) TaskStore() TaskStore {
	return h.taskStore
}

// DB returns the underlying database connection.
func (h *hybridStore) DB() *database.DB {
	return h.db
}

// getDatabaseSize returns the approximate database size in bytes.
func (h *hybridStore) getDatabaseSize() int64 {
	var pageCount, pageSize int64
	_ = h.db.QueryRow("PRAGMA page_count").Scan(&pageCount)
	_ = h.db.QueryRow("PRAGMA page_size").Scan(&pageSize)
	return pageCount * pageSize
}

// startPeriodicFlush starts a background goroutine to flush cached data.
func (h *hybridStore) startPeriodicFlush(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				_ = h.FlushToStorage(ctx)
				cancel()
			case <-h.stopCh:
				return
			}
		}
	}()
}

// startPeriodicCleanup starts a background goroutine to clean up inactive sessions.
func (h *hybridStore) startPeriodicCleanup(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				if err := h.enforceStorageLimits(ctx); err != nil {
					logger.Warn("Periodic cleanup failed", logger.Fields{"error": err})
				}
				cancel()
			case <-h.stopCh:
				return
			}
		}
	}()
}

// enforceStorageLimits ensures storage limits are respected.
// It cleans up inactive sessions and enforces max session count.
func (h *hybridStore) enforceStorageLimits(ctx context.Context) error {
	if h.config == nil {
		return nil
	}

	// Clean up old inactive sessions
	if h.config.CleanupThresholdDays > 0 {
		deleted, err := h.Cleanup(ctx, h.config.CleanupThresholdDays)
		if err != nil {
			logger.Warn("Failed to cleanup inactive sessions", logger.Fields{"error": err})
		} else if deleted > 0 {
			logger.Info("Cleaned up inactive sessions", logger.Fields{
				"deleted":        deleted,
				"threshold_days": h.config.CleanupThresholdDays,
			})
		}
	}

	// Enforce max total sessions
	if h.config.MaxTotalSessions > 0 {
		if err := h.enforceMaxSessions(ctx); err != nil {
			return err
		}
	}

	return nil
}

// enforceMaxSessions deletes oldest inactive sessions when over the limit.
func (h *hybridStore) enforceMaxSessions(ctx context.Context) error {
	// Count total sessions
	var count int
	err := h.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions").Scan(&count)
	if err != nil {
		return err
	}

	if count <= h.config.MaxTotalSessions {
		return nil
	}

	// Delete oldest sessions to get under the limit
	excess := count - h.config.MaxTotalSessions

	// Get IDs of oldest sessions to delete
	rows, err := h.db.QueryContext(ctx, `
		SELECT id FROM sessions
		ORDER BY updated_at ASC
		LIMIT ?
	`, excess)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	var idsToDelete []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			idsToDelete = append(idsToDelete, id)
		}
	}

	// Remove from cache and delete from database
	for _, id := range idsToDelete {
		h.cache.Remove(id)
	}

	// Delete in batches using placeholders
	if len(idsToDelete) > 0 {
		placeholders := strings.Repeat("?,", len(idsToDelete))
		placeholders = placeholders[:len(placeholders)-1] // Remove trailing comma

		args := make([]interface{}, len(idsToDelete))
		for i, id := range idsToDelete {
			args[i] = id
		}

		_, err = h.db.ExecContext(ctx, "DELETE FROM sessions WHERE id IN ("+placeholders+")", args...)
		if err != nil {
			return err
		}

		logger.Info("Enforced max sessions limit", logger.Fields{
			"deleted":      len(idsToDelete),
			"max_sessions": h.config.MaxTotalSessions,
			"total_before": count,
		})
	}

	return nil
}

// generateTitle creates a session title from the first message content.
func generateTitle(content string) string {
	// Clean up the content
	content = strings.TrimSpace(content)

	// Take first line or first 50 chars
	lines := strings.SplitN(content, "\n", 2)
	title := lines[0]

	// Truncate if too long
	if len(title) > 50 {
		title = title[:47] + "..."
	}

	// Default if empty
	if title == "" {
		title = "New Session"
	}

	return title
}

// ============================================================================
// Workspace Note Operations (Passthrough to SQLite)
// ============================================================================

// CreateNote creates a new workspace note.
func (h *hybridStore) CreateNote(ctx context.Context, note *WorkspaceNote) error {
	return h.sqlite.CreateNote(ctx, note)
}

// GetNote retrieves a note by ID.
func (h *hybridStore) GetNote(ctx context.Context, id string) (*WorkspaceNote, error) {
	return h.sqlite.GetNote(ctx, id)
}

// UpdateNote updates note metadata and content.
func (h *hybridStore) UpdateNote(ctx context.Context, note *WorkspaceNote) error {
	return h.sqlite.UpdateNote(ctx, note)
}

// DeleteNote removes a note.
func (h *hybridStore) DeleteNote(ctx context.Context, id string) error {
	return h.sqlite.DeleteNote(ctx, id)
}

// ListNotesByWorkspace returns all notes in a workspace.
func (h *hybridStore) ListNotesByWorkspace(ctx context.Context, workspaceID string) ([]WorkspaceNoteListItem, error) {
	return h.sqlite.ListNotesByWorkspace(ctx, workspaceID)
}

// SearchNotes performs full-text search across note names and content.
func (h *hybridStore) SearchNotes(ctx context.Context, query string, limit int) ([]NoteSearchResult, error) {
	return h.sqlite.SearchNotes(ctx, query, limit)
}
