package workspace

import (
	"context"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/filewatcher"
	"github.com/johnjallday/ori-agent/internal/logger"
)

// DirectorySyncConfig controls how frequently workspace directories are re-synced.
type DirectorySyncConfig struct {
	PollInterval          time.Duration
	WatcherConfig         filewatcher.WatcherConfig
	MaxWatchedDirectories int
}

// DefaultDirectorySyncConfig returns the default sync manager configuration.
func DefaultDirectorySyncConfig() DirectorySyncConfig {
	return DirectorySyncConfig{
		PollInterval:          30 * time.Second,
		WatcherConfig:         filewatcher.DefaultWatcherConfig(),
		MaxWatchedDirectories: 128,
	}
}

type DirectorySyncWatchResult struct {
	WorkspaceID      string `json:"workspace_id"`
	Watched          int    `json:"watched"`
	AlreadyWatching  int    `json:"already_watching"`
	SkippedInvalid   int    `json:"skipped_invalid"`
	SkippedOverLimit int    `json:"skipped_over_limit"`
	TotalDirectories int    `json:"total_directories"`
}

type directoryWatchTarget struct {
	WorkspaceID string
	DirectoryID string
	Name        string
	Path        string
}

// DirectorySyncManager keeps directory references in sync by watching filesystem changes.
type DirectorySyncManager struct {
	store    Store
	eventBus *EventBus
	watcher  *filewatcher.Watcher
	config   DirectorySyncConfig

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu                sync.RWMutex
	watched           map[string]directoryWatchTarget
	workspaceAccessed map[string]time.Time

	started bool
	startMu sync.Mutex
}

// NewDirectorySyncManager creates a new directory sync manager.
func NewDirectorySyncManager(store Store, eventBus *EventBus, config DirectorySyncConfig) (*DirectorySyncManager, error) {
	if config.PollInterval <= 0 {
		config.PollInterval = 30 * time.Second
	}
	if config.MaxWatchedDirectories <= 0 {
		config.MaxWatchedDirectories = DefaultDirectorySyncConfig().MaxWatchedDirectories
	}

	watcherCfg := config.WatcherConfig
	defaultCfg := filewatcher.DefaultWatcherConfig()
	if watcherCfg.DebounceDuration <= 0 {
		watcherCfg.DebounceDuration = defaultCfg.DebounceDuration
	}
	if watcherCfg.EventBufferSize <= 0 {
		watcherCfg.EventBufferSize = defaultCfg.EventBufferSize
	}
	config.WatcherConfig = watcherCfg

	watcher, err := filewatcher.NewWatcher(config.WatcherConfig)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &DirectorySyncManager{
		store:             store,
		eventBus:          eventBus,
		watcher:           watcher,
		config:            config,
		ctx:               ctx,
		cancel:            cancel,
		watched:           make(map[string]directoryWatchTarget),
		workspaceAccessed: make(map[string]time.Time),
	}, nil
}

// Start starts filesystem watching and periodic sync.
func (m *DirectorySyncManager) Start() {
	m.startMu.Lock()
	if m.started {
		m.startMu.Unlock()
		return
	}
	m.started = true
	m.startMu.Unlock()

	m.watcher.Start()

	m.wg.Add(2)
	go m.runEventLoop()
	go m.runSyncLoop()
}

// Stop gracefully stops filesystem watching.
func (m *DirectorySyncManager) Stop() {
	m.startMu.Lock()
	if !m.started {
		m.startMu.Unlock()
		return
	}
	m.started = false
	m.startMu.Unlock()

	m.cancel()
	m.wg.Wait()

	if err := m.watcher.Close(); err != nil {
		logger.Warn("Failed to close directory sync watcher", logger.Fields{"error": err})
	}
}

func (m *DirectorySyncManager) runSyncLoop() {
	defer m.wg.Done()

	ticker := time.NewTicker(m.config.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.syncWatchedDirectories()
		}
	}
}

func (m *DirectorySyncManager) runEventLoop() {
	defer m.wg.Done()

	events := m.watcher.Events()
	for {
		select {
		case <-m.ctx.Done():
			return
		case evt, ok := <-events:
			if !ok {
				return
			}
			m.handleWatchEvent(evt)
		}
	}
}

func (m *DirectorySyncManager) handleWatchEvent(evt filewatcher.WatchEvent) {
	if m.eventBus == nil {
		return
	}

	m.mu.RLock()
	target, ok := m.watched[evt.SessionID]
	m.mu.RUnlock()
	if !ok {
		return
	}

	m.eventBus.Publish(Event{
		Type:        EventWorkspaceUpdated,
		WorkspaceID: target.WorkspaceID,
		Source:      "directory-sync",
		Data: map[string]any{
			"action":         "directory_synced",
			"directory_id":   target.DirectoryID,
			"directory_name": target.Name,
			"change_type":    string(evt.Type),
			"file_path":      evt.FilePath,
			"file_name":      evt.FileName,
			"changed_at":     evt.Timestamp,
		},
	})
}

func (m *DirectorySyncManager) syncWatchedDirectories() {
	workspaceIDs := m.watchedWorkspaceIDs()
	for _, workspaceID := range workspaceIDs {
		if _, err := m.watchWorkspace(workspaceID, false); err != nil {
			logger.Debug("Directory sync: failed to refresh watched workspace", logger.Fields{
				"workspace_id": workspaceID,
				"error":        err,
			})
		}
	}
}

func (m *DirectorySyncManager) watchedWorkspaceIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	seen := make(map[string]struct{}, len(m.watched))
	ids := make([]string, 0, len(m.watched))
	for _, target := range m.watched {
		if target.WorkspaceID == "" {
			continue
		}
		if _, ok := seen[target.WorkspaceID]; ok {
			continue
		}
		seen[target.WorkspaceID] = struct{}{}
		ids = append(ids, target.WorkspaceID)
	}
	return ids
}

// WatchWorkspace starts or refreshes watches only for the requested workspace.
func (m *DirectorySyncManager) WatchWorkspace(workspaceID string) (DirectorySyncWatchResult, error) {
	return m.watchWorkspace(workspaceID, true)
}

func (m *DirectorySyncManager) watchWorkspace(workspaceID string, touchAccess bool) (DirectorySyncWatchResult, error) {
	result := DirectorySyncWatchResult{WorkspaceID: workspaceID}
	if strings.TrimSpace(workspaceID) == "" {
		return result, nil
	}

	ws, err := m.store.Get(workspaceID)
	if err != nil || ws == nil {
		m.unwatchWorkspace(workspaceID)
		return result, err
	}

	result.TotalDirectories = len(ws.DirectoryReferences)
	if ws.Status != "" && ws.Status != StatusActive {
		m.unwatchWorkspace(workspaceID)
		return result, nil
	}

	desired := make(map[string]directoryWatchTarget)
	for _, dir := range ws.DirectoryReferences {
		path := strings.TrimSpace(dir.Path)
		if path == "" {
			result.SkippedInvalid++
			continue
		}
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			result.SkippedInvalid++
			continue
		}

		watchKey := buildDirectoryWatchKey(ws.ID, dir.ID)
		desired[watchKey] = directoryWatchTarget{
			WorkspaceID: ws.ID,
			DirectoryID: dir.ID,
			Name:        dir.Name,
			Path:        path,
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if touchAccess {
		m.workspaceAccessed[workspaceID] = time.Now()
	}

	// Remove stale watchers for this workspace only.
	for watchKey, existing := range m.watched {
		if existing.WorkspaceID != workspaceID {
			continue
		}
		target, ok := desired[watchKey]
		if !ok || target.Path != existing.Path {
			if err := m.watcher.Unwatch(watchKey); err != nil {
				logger.Warn("Directory sync: failed to unwatch directory", logger.Fields{
					"watch_key": watchKey,
					"path":      existing.Path,
					"error":     err,
				})
			}
			delete(m.watched, watchKey)
		}
	}

	// Add missing watchers.
	for watchKey, target := range desired {
		if _, alreadyWatching := m.watched[watchKey]; alreadyWatching {
			result.AlreadyWatching++
			result.Watched++
			continue
		}
		if m.config.MaxWatchedDirectories > 0 && len(m.watched) >= m.config.MaxWatchedDirectories {
			m.evictLeastRecentlyUsedWorkspaceLocked(workspaceID)
		}
		if m.config.MaxWatchedDirectories > 0 && len(m.watched) >= m.config.MaxWatchedDirectories {
			result.SkippedOverLimit++
			continue
		}
		if err := m.watcher.Watch(watchKey, target.Path); err != nil {
			logger.Warn("Directory sync: failed to watch directory", logger.Fields{
				"watch_key": watchKey,
				"path":      target.Path,
				"error":     err,
			})
			result.SkippedInvalid++
			continue
		}
		m.watched[watchKey] = target
		result.Watched++
	}

	return result, nil
}

func (m *DirectorySyncManager) unwatchWorkspace(workspaceID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.unwatchWorkspaceLocked(workspaceID)
}

func (m *DirectorySyncManager) unwatchWorkspaceLocked(workspaceID string) {
	for watchKey, existing := range m.watched {
		if existing.WorkspaceID != workspaceID {
			continue
		}
		if err := m.watcher.Unwatch(watchKey); err != nil {
			logger.Warn("Directory sync: failed to unwatch workspace directory", logger.Fields{
				"watch_key":    watchKey,
				"workspace_id": workspaceID,
				"path":         existing.Path,
				"error":        err,
			})
		}
		delete(m.watched, watchKey)
	}
	delete(m.workspaceAccessed, workspaceID)
}

func (m *DirectorySyncManager) evictLeastRecentlyUsedWorkspaceLocked(protectedWorkspaceID string) bool {
	var candidate string
	var candidateLastAccessed time.Time
	for _, target := range m.watched {
		if target.WorkspaceID == "" || target.WorkspaceID == protectedWorkspaceID {
			continue
		}
		lastAccessed := m.workspaceAccessed[target.WorkspaceID]
		if candidate == "" || lastAccessed.Before(candidateLastAccessed) {
			candidate = target.WorkspaceID
			candidateLastAccessed = lastAccessed
		}
	}
	if candidate == "" {
		return false
	}

	logger.Info("Directory sync: evicting least recently used workspace watches", logger.Fields{
		"workspace_id": candidate,
		"protected":    protectedWorkspaceID,
	})
	m.unwatchWorkspaceLocked(candidate)
	return true
}

func buildDirectoryWatchKey(workspaceID, directoryID string) string {
	return workspaceID + "::" + directoryID
}
