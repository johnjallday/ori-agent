package filewatcher

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
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
	sessions  map[string]string // watchID -> path
	pathRefs  map[string]int

	legacyEvents       chan WatchEvent
	subscriptions      map[int64]watchSubscription
	nextSubscriptionID int64
	closeOutputsOnce   sync.Once

	mu      sync.RWMutex
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	started bool
	startMu sync.Mutex
}

// WatcherConfig holds configuration for the watcher
type WatcherConfig struct {
	DebounceDuration time.Duration
	EventBufferSize  int
}

type watchSubscription struct {
	watchID string
	events  chan WatchEvent
}

// Subscription receives watcher events for a single watch ID.
type Subscription struct {
	watcher *Watcher
	id      int64
	events  chan WatchEvent
	once    sync.Once
}

// Events returns the subscription's event channel.
func (s *Subscription) Events() <-chan WatchEvent {
	if s == nil {
		return nil
	}
	return s.events
}

// Close unsubscribes and closes the subscription event channel.
func (s *Subscription) Close() {
	if s == nil || s.watcher == nil {
		return
	}
	s.once.Do(func() {
		s.watcher.unsubscribe(s.id)
	})
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
	defaultConfig := DefaultWatcherConfig()
	if config.DebounceDuration <= 0 {
		config.DebounceDuration = defaultConfig.DebounceDuration
	}
	if config.EventBufferSize <= 0 {
		config.EventBufferSize = defaultConfig.EventBufferSize
	}

	fsWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())

	w := &Watcher{
		fsWatcher:     fsWatcher,
		debouncer:     NewEventDebouncer(config.DebounceDuration, config.EventBufferSize),
		sessions:      make(map[string]string),
		pathRefs:      make(map[string]int),
		legacyEvents:  make(chan WatchEvent, config.EventBufferSize),
		subscriptions: make(map[int64]watchSubscription),
		ctx:           ctx,
		cancel:        cancel,
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
	w.wg.Add(1)
	go w.dispatchEvents()
}

// Watch adds a session folder to be watched
func (w *Watcher) Watch(sessionID, path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("watch path is required")
	}
	path = cleanWatchPath(path)

	w.mu.Lock()
	defer w.mu.Unlock()

	// Check if already watching this session
	if existing, exists := w.sessions[sessionID]; exists {
		if existing != path {
			return errors.New("watch ID is already registered for a different path")
		}
		return nil
	}

	// Add the OS-level watch only once per path. Multiple logical watch IDs can
	// share a folder and receive independent events through subscriptions.
	if w.pathRefs[path] == 0 {
		if err := w.fsWatcher.Add(path); err != nil {
			return err
		}
	}

	w.sessions[sessionID] = path
	w.pathRefs[path]++

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
	remaining := w.pathRefs[path] - 1
	if remaining <= 0 {
		delete(w.pathRefs, path)
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
	} else {
		w.pathRefs[path] = remaining
	}

	logger.Info("Stopped watching filesystem path", logger.Fields{
		"watch_id": sessionID,
		"path":     path,
	})

	return nil
}

// Events returns the channel for receiving debounced file events
func (w *Watcher) Events() <-chan WatchEvent {
	return w.legacyEvents
}

// Subscribe returns an independent stream of events for one watch ID. Unlike
// Events, each subscription receives its own copy and cannot steal events from
// another consumer.
func (w *Watcher) Subscribe(watchID string, bufferSize int) *Subscription {
	if bufferSize <= 0 {
		bufferSize = cap(w.legacyEvents)
		if bufferSize <= 0 {
			bufferSize = 1
		}
	}
	sub := &Subscription{
		watcher: w,
		events:  make(chan WatchEvent, bufferSize),
	}

	w.mu.Lock()
	w.nextSubscriptionID++
	sub.id = w.nextSubscriptionID
	w.subscriptions[sub.id] = watchSubscription{
		watchID: watchID,
		events:  sub.events,
	}
	w.mu.Unlock()
	return sub
}

func (w *Watcher) unsubscribe(id int64) {
	w.mu.Lock()
	sub, ok := w.subscriptions[id]
	if ok {
		delete(w.subscriptions, id)
		close(sub.events)
	}
	w.mu.Unlock()
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
	w.closeEventOutputs()

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

func (w *Watcher) dispatchEvents() {
	defer w.wg.Done()
	defer w.closeEventOutputs()

	for {
		select {
		case <-w.ctx.Done():
			return
		case event, ok := <-w.debouncer.Events():
			if !ok {
				return
			}
			w.dispatchEvent(event)
		}
	}
}

func (w *Watcher) dispatchEvent(event WatchEvent) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	select {
	case w.legacyEvents <- event:
	default:
		logger.Warn("File watcher legacy event channel full; dropping event", logger.Fields{
			"watch_id":  event.SessionID,
			"file_path": event.FilePath,
		})
	}

	for _, sub := range w.subscriptions {
		if sub.watchID != "" && sub.watchID != event.SessionID {
			continue
		}
		select {
		case sub.events <- event:
		default:
			logger.Warn("File watcher subscription channel full; dropping event", logger.Fields{
				"watch_id":  event.SessionID,
				"file_path": event.FilePath,
			})
		}
	}
}

func (w *Watcher) closeEventOutputs() {
	w.closeOutputsOnce.Do(func() {
		w.mu.Lock()
		close(w.legacyEvents)
		for id, sub := range w.subscriptions {
			delete(w.subscriptions, id)
			close(sub.events)
		}
		w.mu.Unlock()
	})
}

// handleFSEvent processes a single fsnotify event
func (w *Watcher) handleFSEvent(event fsnotify.Event) {
	// Get file name
	fileName := filepath.Base(event.Name)

	// Filter out temporary and system files
	if shouldIgnoreFile(fileName) {
		return
	}

	// Convert fsnotify event to our event type
	eventType := convertEventType(event.Op)
	if eventType == "" {
		return
	}

	watchIDs := w.watchIDsForPath(event.Name)
	if len(watchIDs) == 0 {
		return
	}

	now := time.Now()
	for _, watchID := range watchIDs {
		w.debouncer.Add(WatchEvent{
			SessionID: watchID,
			Type:      eventType,
			FilePath:  event.Name,
			FileName:  fileName,
			Timestamp: now,
		})
	}
}

// findSessionForPath finds the session ID for a given file path
func (w *Watcher) findSessionForPath(filePath string) string {
	watchIDs := w.watchIDsForPath(filePath)
	if len(watchIDs) == 0 {
		return ""
	}
	return watchIDs[0]
}

func (w *Watcher) watchIDsForPath(filePath string) []string {
	w.mu.RLock()
	defer w.mu.RUnlock()

	filePath = cleanWatchPath(filePath)
	longest := -1
	ids := make([]string, 0, 1)
	for sessionID, sessionPath := range w.sessions {
		if !pathContains(sessionPath, filePath) {
			continue
		}
		if len(sessionPath) > longest {
			longest = len(sessionPath)
			ids = ids[:0]
		}
		if len(sessionPath) == longest {
			ids = append(ids, sessionID)
		}
	}

	sort.Strings(ids)
	return ids
}

func cleanWatchPath(path string) string {
	return filepath.Clean(strings.TrimSpace(path))
}

func pathContains(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
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
