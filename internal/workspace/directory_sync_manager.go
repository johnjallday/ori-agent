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
	PollInterval  time.Duration
	WatcherConfig filewatcher.WatcherConfig
}

// DefaultDirectorySyncConfig returns the default sync manager configuration.
func DefaultDirectorySyncConfig() DirectorySyncConfig {
	return DirectorySyncConfig{
		PollInterval:  30 * time.Second,
		WatcherConfig: filewatcher.DefaultWatcherConfig(),
	}
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

	mu      sync.RWMutex
	watched map[string]directoryWatchTarget

	started bool
	startMu sync.Mutex
}

// NewDirectorySyncManager creates a new directory sync manager.
func NewDirectorySyncManager(store Store, eventBus *EventBus, config DirectorySyncConfig) (*DirectorySyncManager, error) {
	if config.PollInterval <= 0 {
		config.PollInterval = 30 * time.Second
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
		store:    store,
		eventBus: eventBus,
		watcher:  watcher,
		config:   config,
		ctx:      ctx,
		cancel:   cancel,
		watched:  make(map[string]directoryWatchTarget),
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
	m.syncWatchedDirectories()

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
		Data: map[string]interface{}{
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
	workspaceIDs, err := m.store.List()
	if err != nil {
		logger.Warn("Directory sync: failed to list workspaces", logger.Fields{"error": err})
		return
	}

	desired := make(map[string]directoryWatchTarget)

	for _, workspaceID := range workspaceIDs {
		ws, err := m.store.Get(workspaceID)
		if err != nil {
			continue
		}
		for _, dir := range ws.DirectoryReferences {
			path := strings.TrimSpace(dir.Path)
			if path == "" {
				continue
			}
			info, err := os.Stat(path)
			if err != nil || !info.IsDir() {
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
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Remove stale watchers.
	for watchKey, existing := range m.watched {
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
			continue
		}
		if err := m.watcher.Watch(watchKey, target.Path); err != nil {
			logger.Warn("Directory sync: failed to watch directory", logger.Fields{
				"watch_key": watchKey,
				"path":      target.Path,
				"error":     err,
			})
			continue
		}
		m.watched[watchKey] = target
	}
}

func buildDirectoryWatchKey(workspaceID, directoryID string) string {
	return workspaceID + "::" + directoryID
}
