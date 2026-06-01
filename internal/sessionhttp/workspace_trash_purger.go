package sessionhttp

import (
	"context"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// defaultTrashPurgeInterval is how often the auto-purger scans the Trash. Scans
// are cheap (a single indexed query plus teardown of expired rows), so running a
// few times a day keeps workspaces from lingering long past their purge time
// without meaningful cost.
const defaultTrashPurgeInterval = 6 * time.Hour

// TrashPurger is a background service that permanently deletes workspaces left in
// Trash longer than TrashRetention. It is the automatic counterpart to the manual
// "Delete permanently" action and shares the same WorkspacePurger teardown.
type TrashPurger struct {
	store     session.HybridStore
	purger    *WorkspacePurger
	interval  time.Duration
	retention time.Duration

	stopOnce sync.Once
	stopCh   chan struct{}
	doneCh   chan struct{}
}

// NewTrashPurger builds an auto-purger over the given stores. workspaceStore and
// agentStore may be nil (folder/entry-agent cleanup is then skipped).
func NewTrashPurger(s session.HybridStore, workspaceStore *workspace.FileStore, agentStore store.Store) *TrashPurger {
	return &TrashPurger{
		store:     s,
		purger:    NewWorkspacePurger(s, workspaceStore, agentStore),
		interval:  defaultTrashPurgeInterval,
		retention: TrashRetention,
		stopCh:    make(chan struct{}),
		doneCh:    make(chan struct{}),
	}
}

// Start launches the background loop. It is a no-op when the purger or its store
// is nil, so callers don't need to guard optional wiring.
func (p *TrashPurger) Start() {
	if p == nil || p.store == nil {
		return
	}
	go p.run()
}

func (p *TrashPurger) run() {
	defer close(p.doneCh)

	// Run once shortly after startup so already-expired items don't wait a whole
	// interval for their first scan.
	initial := time.NewTimer(time.Minute)
	defer initial.Stop()

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopCh:
			return
		case <-initial.C:
			p.purgeExpired()
		case <-ticker.C:
			p.purgeExpired()
		}
	}
}

// Stop signals the loop to exit and waits briefly for it to finish.
func (p *TrashPurger) Stop() {
	if p == nil {
		return
	}
	p.stopOnce.Do(func() { close(p.stopCh) })
	select {
	case <-p.doneCh:
	case <-time.After(5 * time.Second):
	}
}

// purgeExpired permanently deletes every trashed workspace whose deletion time is
// older than the retention window. Sessions are deleted too: the workspace is
// being removed entirely, so nothing should be left orphaned at root.
func (p *TrashPurger) purgeExpired() {
	ctx := context.Background()

	workspaces, err := p.store.ListTrashedWorkspaces(ctx)
	if err != nil {
		logger.Warn("Trash purge: failed to list trashed workspaces", logger.Fields{"error": err})
		return
	}

	cutoff := time.Now().Add(-p.retention)
	purged := 0
	for i := range workspaces {
		ws := &workspaces[i]
		if ws.DeletedAt == nil || ws.DeletedAt.After(cutoff) {
			continue
		}
		if err := p.purger.Purge(ctx, ws, true); err != nil {
			logger.Warn("Trash purge: failed to purge workspace", logger.Fields{"id": ws.ID, "error": err})
			continue
		}
		purged++
	}

	if purged > 0 {
		logger.Info("Trash purge complete", logger.Fields{"purged": purged, "retention": p.retention.String()})
	}
}
