package trigger

import (
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/filewatcher"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// watchValidateInterval is how often enabled watches are re-checked against
// the filesystem. fsnotify does not reliably report a watched directory's own
// deletion across platforms, so a cheap stat sweep is the portable way to
// satisfy PRD #16.
const watchValidateInterval = time.Minute

// WatchManager owns the file-watch side of triggers: it holds a dedicated
// filewatcher.Watcher instance (watch keys are trigger IDs, completely
// separate from the session watcher used by DirectorySyncManager), filters
// raw events per trigger, and feeds matches into the coalescer.
type WatchManager struct {
	store         *Store
	coalescer     *Coalescer
	watcher       *filewatcher.Watcher
	opportunities workspace.OpportunityStore // nil disables path-lost findings

	stopOnce sync.Once
	stop     chan struct{}
	wg       sync.WaitGroup
}

// NewWatchManager constructs a WatchManager with its own watcher instance.
func NewWatchManager(store *Store, coalescer *Coalescer, opps workspace.OpportunityStore) (*WatchManager, error) {
	w, err := filewatcher.NewWatcher(filewatcher.DefaultWatcherConfig())
	if err != nil {
		return nil, fmt.Errorf("create trigger file watcher: %w", err)
	}
	return &WatchManager{
		store:         store,
		coalescer:     coalescer,
		watcher:       w,
		opportunities: opps,
		stop:          make(chan struct{}),
	}, nil
}

// Start registers all enabled file-watch triggers and begins processing
// events. Call after Store.LoadAll and after the dispatcher is wired.
func (m *WatchManager) Start() {
	m.watcher.Start()

	for _, t := range m.store.ListAll() {
		if t.Type != TypeFileWatch || !t.Enabled {
			continue
		}
		if err := m.Add(t); err != nil {
			logger.Warn("trigger watch manager: startup registration failed", logger.Fields{
				"trigger_id": t.ID, "workspace_id": t.WorkspaceID, "error": err,
			})
		}
	}

	m.wg.Add(2)
	go m.eventLoop()
	go m.validateLoop()
}

// Add starts watching for one trigger. The path is live-validated first; on
// failure the trigger's tracking fields are updated and the error returned.
func (m *WatchManager) Add(t Trigger) error {
	if t.Type != TypeFileWatch || t.FileWatch == nil {
		return nil
	}
	if err := t.CheckWatchPath(); err != nil {
		m.markWatchBroken(t, err)
		return err
	}
	if err := m.watcher.Watch(t.ID, t.FileWatch.Path); err != nil {
		m.markWatchBroken(t, err)
		return err
	}
	return nil
}

// Remove stops watching for one trigger (disable or delete) and drops any
// open coalescing window.
func (m *WatchManager) Remove(triggerID string) {
	if err := m.watcher.Unwatch(triggerID); err != nil {
		logger.Debug("trigger watch manager: unwatch", logger.Fields{
			"trigger_id": triggerID, "error": err,
		})
	}
	m.coalescer.Drop(triggerID)
}

// Close tears down all watches and stops the loops.
func (m *WatchManager) Close() {
	m.stopOnce.Do(func() { close(m.stop) })
	_ = m.watcher.Close()
	m.wg.Wait()
}

// eventLoop forwards matching raw watch events into the coalescer.
func (m *WatchManager) eventLoop() {
	defer m.wg.Done()
	for {
		select {
		case <-m.stop:
			return
		case ev, ok := <-m.watcher.Events():
			if !ok {
				return
			}
			m.handleEvent(ev)
		}
	}
}

// handleEvent routes one raw event to every trigger watching its directory.
// Routing is by path, not by the watch key (filewatcher's reverse lookup
// returns one arbitrary key per path, which would starve a second trigger
// watching the same directory).
func (m *WatchManager) handleEvent(ev filewatcher.WatchEvent) {
	dir := filepath.Dir(ev.FilePath)
	for _, t := range m.store.ListAll() {
		if t.Type != TypeFileWatch || !t.Enabled || t.FileWatch == nil {
			continue
		}
		// Watches are non-recursive: the event's parent dir must be the
		// watched dir itself.
		if filepath.Clean(t.FileWatch.Path) != dir {
			continue
		}
		if !t.MatchesFileEvent(string(ev.Type), ev.FileName) {
			continue
		}
		m.coalescer.Observe(t, Event{
			Kind:      "file",
			Timestamp: ev.Timestamp,
			FileEvent: string(ev.Type),
			FilePath:  ev.FilePath,
			FileName:  ev.FileName,
		})
	}
}

// validateLoop periodically re-checks that watched directories still exist,
// breaking cleanly (no crash, no respin) when one disappears.
func (m *WatchManager) validateLoop() {
	defer m.wg.Done()
	ticker := time.NewTicker(watchValidateInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-ticker.C:
			m.validateWatches()
		}
	}
}

// validateWatches stats every enabled watch path once.
func (m *WatchManager) validateWatches() {
	for _, t := range m.store.ListAll() {
		if t.Type != TypeFileWatch || !t.Enabled {
			continue
		}
		if err := t.CheckWatchPath(); err != nil {
			logger.Warn("trigger watch manager: watched path lost", logger.Fields{
				"trigger_id": t.ID, "workspace_id": t.WorkspaceID, "path": t.FileWatch.Path, "error": err,
			})
			m.Remove(t.ID)
			m.markWatchBroken(t, err)
		}
	}
}

// markWatchBroken disables the trigger, records the failure on its tracking
// fields, and surfaces an Action Center finding (PRD #16, #24). Disabling —
// rather than leaving the trigger enabled-but-dead — keeps the validation
// sweep from re-reporting the same loss every minute; re-enabling re-runs
// the path check.
func (m *WatchManager) markWatchBroken(t Trigger, cause error) {
	msg := fmt.Sprintf("file watch stopped: %v", cause)
	_, err := m.store.Update(t.WorkspaceID, t.ID, func(tr *Trigger) error {
		tr.Enabled = false
		tr.FailureCount++
		tr.LastError = msg
		return nil
	})
	if err != nil {
		logger.Warn("trigger watch manager: record broken watch", logger.Fields{
			"trigger_id": t.ID, "workspace_id": t.WorkspaceID, "error": err,
		})
	}

	if m.opportunities == nil {
		return
	}
	now := time.Now()
	if _, _, err := m.opportunities.Upsert(workspace.Opportunity{
		WorkspaceID:       t.WorkspaceID,
		Title:             fmt.Sprintf("Event trigger %q is failing", t.Name),
		Summary:           fmt.Sprintf("The file-watch trigger %q was disabled because its watched directory is gone.", t.Name),
		Evidence:          msg,
		Priority:          triggerFindingPriority,
		Confidence:        triggerFindingConfidence,
		Status:            workspace.OpportunityNew,
		RecommendedAction: "Restore the watched directory (or point the trigger at a new one), then re-enable the trigger.",
		CreatedAt:         now,
		UpdatedAt:         now,
	}); err != nil {
		logger.Warn("trigger watch manager: file path-lost finding", logger.Fields{
			"trigger_id": t.ID, "workspace_id": t.WorkspaceID, "error": err,
		})
	}
}
