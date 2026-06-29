package filewatcher

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/johnjallday/ori-agent/internal/logger"
)

// EventType represents the type of file system event
type EventType string

const (
	EventCreate EventType = "create"
	EventModify EventType = "modify"
	EventRemove EventType = "remove"
	EventRename EventType = "rename"
)

// WatchEvent represents a file system change event
type WatchEvent struct {
	SessionID string    `json:"session_id"`
	Type      EventType `json:"type"`
	FilePath  string    `json:"file_path"`
	FileName  string    `json:"file_name"`
	Timestamp time.Time `json:"timestamp"`
}

// Watcher monitors session folders for file changes
type Watcher struct {
	fsWatcher *fsnotify.Watcher
	debouncer *EventDebouncer
	sessions  map[string]string // sessionID -> path
	mu        sync.RWMutex
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	started   bool
	startMu   sync.Mutex
}

// WatcherConfig holds configuration for the watcher
type WatcherConfig struct {
	DebounceDuration time.Duration
	EventBufferSize  int
}

// DefaultWatcherConfig returns default configuration
func DefaultWatcherConfig() WatcherConfig {
	return WatcherConfig{
		DebounceDuration: 500 * time.Millisecond,
		EventBufferSize:  100,
	}
}

// NewWatcher creates a new file watcher
func NewWatcher(config WatcherConfig) (*Watcher, error) {
	fsWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())

	w := &Watcher{
		fsWatcher: fsWatcher,
		debouncer: NewEventDebouncer(config.DebounceDuration, config.EventBufferSize),
		sessions:  make(map[string]string),
		ctx:       ctx,
		cancel:    cancel,
	}

	return w, nil
}

// Start begins processing file system events
func (w *Watcher) Start() {
	w.startMu.Lock()
	if w.started {
		w.startMu.Unlock()
		return
	}
	w.started = true
	w.startMu.Unlock()

	w.wg.Add(1)
	go w.processEvents()
}

// Watch adds a session folder to be watched
func (w *Watcher) Watch(sessionID, path string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Check if already watching this session
	if _, exists := w.sessions[sessionID]; exists {
		return nil
	}

	// Add to fsnotify
	if err := w.fsWatcher.Add(path); err != nil {
		return err
	}

	w.sessions[sessionID] = path

	logger.Info("Started watching filesystem path", logger.Fields{
		"watch_id": sessionID,
		"path":     path,
	})

	return nil
}

// Unwatch removes a session folder from watching
func (w *Watcher) Unwatch(sessionID string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	path, exists := w.sessions[sessionID]
	if !exists {
		return nil
	}

	delete(w.sessions, sessionID)

	// Multiple sessions can watch the same folder. Only remove the OS-level watch
	// once the last session for this path is gone, otherwise the second Unwatch
	// would try to remove an already-removed watch.
	stillWatched := false
	for _, p := range w.sessions {
		if p == path {
			stillWatched = true
			break
		}
	}

	if !stillWatched {
		if err := w.fsWatcher.Remove(path); err != nil {
			// fsnotify auto-removes a watch when its directory is deleted, so a
			// "non-existent watch" here is expected and harmless — the watch is
			// already gone, which is the desired end state. Surface other errors.
			if errors.Is(err, fsnotify.ErrNonExistentWatch) {
				logger.Debug("Watch already removed before Unwatch", logger.Fields{
					"session_id": sessionID,
					"path":       path,
				})
			} else {
				logger.Warn("Failed to remove watcher", logger.Fields{
					"session_id": sessionID,
					"path":       path,
					"error":      err,
				})
			}
		}
	}

	logger.Info("Stopped watching filesystem path", logger.Fields{
		"watch_id": sessionID,
		"path":     path,
	})

	return nil
}

// Events returns the channel for receiving debounced file events
func (w *Watcher) Events() <-chan WatchEvent {
	return w.debouncer.Events()
}

// Close stops the watcher and releases resources
func (w *Watcher) Close() error {
	w.cancel()

	// Close debouncer first to stop accepting events
	w.debouncer.Close()

	// Close fsnotify watcher
	if err := w.fsWatcher.Close(); err != nil {
		logger.Error("Error closing fsnotify watcher", logger.Fields{"error": err})
	}

	// Wait for event processor to finish
	w.wg.Wait()

	logger.Info("File watcher closed", nil)

	return nil
}

// processEvents handles incoming fsnotify events
func (w *Watcher) processEvents() {
	defer w.wg.Done()

	for {
		select {
		case <-w.ctx.Done():
			return

		case event, ok := <-w.fsWatcher.Events:
			if !ok {
				return
			}
			w.handleFSEvent(event)

		case err, ok := <-w.fsWatcher.Errors:
			if !ok {
				return
			}
			logger.Error("File watcher error", logger.Fields{"error": err})
		}
	}
}

// handleFSEvent processes a single fsnotify event
func (w *Watcher) handleFSEvent(event fsnotify.Event) {
	// Get file name
	fileName := filepath.Base(event.Name)

	// Filter out temporary and system files
	if shouldIgnoreFile(fileName) {
		return
	}

	// Find which session this event belongs to
	sessionID := w.findSessionForPath(event.Name)
	if sessionID == "" {
		return
	}

	// Convert fsnotify event to our event type
	eventType := convertEventType(event.Op)
	if eventType == "" {
		return
	}

	// Create watch event
	watchEvent := WatchEvent{
		SessionID: sessionID,
		Type:      eventType,
		FilePath:  event.Name,
		FileName:  fileName,
		Timestamp: time.Now(),
	}

	// Add to debouncer
	w.debouncer.Add(watchEvent)
}

// findSessionForPath finds the session ID for a given file path
func (w *Watcher) findSessionForPath(filePath string) string {
	w.mu.RLock()
	defer w.mu.RUnlock()

	for sessionID, sessionPath := range w.sessions {
		// Check if filePath is under sessionPath
		rel, err := filepath.Rel(sessionPath, filePath)
		if err == nil && !strings.HasPrefix(rel, "..") {
			return sessionID
		}
	}

	return ""
}

// convertEventType converts fsnotify.Op to our EventType
func convertEventType(op fsnotify.Op) EventType {
	switch {
	case op&fsnotify.Create == fsnotify.Create:
		return EventCreate
	case op&fsnotify.Write == fsnotify.Write:
		return EventModify
	case op&fsnotify.Remove == fsnotify.Remove:
		return EventRemove
	case op&fsnotify.Rename == fsnotify.Rename:
		return EventRename
	default:
		return ""
	}
}

// shouldIgnoreFile returns true if the file should be ignored
func shouldIgnoreFile(fileName string) bool {
	// Ignore hidden files (starting with .)
	if strings.HasPrefix(fileName, ".") {
		return true
	}

	// Ignore common temporary files
	ignoredPatterns := []string{
		"~$",       // Office temp files
		".tmp",     // Temporary files
		".swp",     // Vim swap files
		".swo",     // Vim swap files
		"~",        // Backup files ending with ~
		".bak",     // Backup files
		".partial", // Partial downloads
	}

	lowerName := strings.ToLower(fileName)
	for _, pattern := range ignoredPatterns {
		if strings.HasPrefix(lowerName, pattern) || strings.HasSuffix(lowerName, pattern) {
			return true
		}
	}

	// Ignore specific system files
	ignoredFiles := []string{
		"thumbs.db",
		"desktop.ini",
		".ds_store",
		"icon\r",
	}

	for _, ignored := range ignoredFiles {
		if lowerName == ignored {
			return true
		}
	}

	return false
}

// IsWatching returns true if the session is being watched
func (w *Watcher) IsWatching(sessionID string) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	_, exists := w.sessions[sessionID]
	return exists
}

// WatchedSessions returns a list of all watched session IDs
func (w *Watcher) WatchedSessions() []string {
	w.mu.RLock()
	defer w.mu.RUnlock()

	sessions := make([]string, 0, len(w.sessions))
	for sessionID := range w.sessions {
		sessions = append(sessions, sessionID)
	}
	return sessions
}
