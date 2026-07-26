package downloadsjanitor

import (
	"strings"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
	workspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// Automation is the unattended half of the Janitor: a folder watcher and a
// daily catch-up, both of which end in exactly the same scan the user gets from
// "Scan now".
//
// Two properties shape everything here:
//
//   - Many events, one scan. A hundred files arriving at once is one piece of
//     news — "the folder changed" — and must produce one review batch, not a
//     hundred of anything (FR-35, FR-104).
//   - The watcher is an optimization, not the source of truth. Every automatic
//     run enumerates the folder through the same scanner as a manual run, so a
//     file the watcher missed is still found, and a file it reported that does
//     not qualify is still rejected (FR-37).

// DomainKey is the domain-scan handler key the Janitor registers under.
const DomainKey = "downloads_janitor"

// WatchTriggerName is the name of the trigger the Janitor installs. It is
// stable so setup can find and update its own trigger rather than creating a
// second one.
const WatchTriggerName = "Downloads Janitor folder watch"

// DefaultWatchDebounce is the collection window for folder activity when the
// template does not specify one (FR-34).
const DefaultWatchDebounce = 5 * time.Minute

// TriggerStore is the slice of trigger persistence the Janitor needs to install
// and remove its own watcher.
type TriggerStore interface {
	List(workspaceID string) ([]TriggerRecord, error)
	Upsert(record TriggerRecord) (TriggerRecord, error)
	Delete(workspaceID, triggerID string) error
}

// TriggerRecord is the Janitor's view of a file-watch trigger. The trigger
// package owns the real type; this is the narrow shape the Janitor sets, so the
// two packages do not depend on each other's internals.
type TriggerRecord struct {
	ID          string
	WorkspaceID string
	Name        string
	Enabled     bool
	// Path is the folder to watch, non-recursively.
	Path string
	// Events are the file events that wake the scan.
	Events []string
	// DebounceSeconds is the collection window.
	DebounceSeconds int
	// Domain routes the fire to an in-process handler instead of an agent.
	Domain string
}

// Automation installs, removes, and services the Janitor's unattended runs.
type Automation struct {
	service  *Service
	triggers TriggerStore
	now      func() time.Time
	// scan is the function one automatic run performs. It defaults to the
	// service's own ScanNow so automatic and manual runs share one path;
	// coalescing tests substitute a blocking implementation to hold a scan open
	// while more events arrive.
	scan func(workspaceID string, source ScanSource) (JanitorBatch, bool, error)

	mu sync.Mutex
	// running marks workspaces with a scan in flight; followUp marks those that
	// received events while one was running. Together they are the whole
	// coalescing rule: at most one active scan and one queued follow-up per
	// workspace (FR-36).
	running  map[string]bool
	followUp map[string]bool
	// lastCatchUp records the local date each workspace last ran its daily
	// catch-up, so a restart or a re-tick cannot run it twice in one day.
	lastCatchUp map[string]string

	stop chan struct{}
	done chan struct{}
}

// NewAutomation builds the automation service.
func NewAutomation(service *Service, triggers TriggerStore) *Automation {
	automation := &Automation{
		service:     service,
		triggers:    triggers,
		now:         time.Now,
		running:     map[string]bool{},
		followUp:    map[string]bool{},
		lastCatchUp: map[string]string{},
	}
	automation.scan = service.ScanNow
	return automation
}

func (a *Automation) clock() time.Time {
	if a == nil || a.now == nil {
		return time.Now()
	}
	return a.now()
}

// EnsureWatcher installs or updates the workspace's folder watcher from its
// template's automation recipe.
//
// It is idempotent by name: repeating setup updates the existing trigger rather
// than adding a second one, so a user who re-runs setup does not end up with
// two watchers on one folder (FR-24). A workspace that is not set up, or is
// paused, ends with the watcher disabled rather than removed — pausing is
// reversible and should not lose the configuration.
func (a *Automation) EnsureWatcher(workspaceID string) error {
	if a == nil || a.triggers == nil {
		return nil
	}
	settings, err := a.service.store.LoadSettings(workspaceID)
	if err != nil {
		return err
	}

	existing, err := a.findWatcher(workspaceID)
	if err != nil {
		return err
	}

	// Not set up, or paused: the watcher exists but does nothing.
	if !settings.IsSetUp() || settings.Paused {
		if existing == nil {
			return nil
		}
		existing.Enabled = false
		_, err := a.triggers.Upsert(*existing)
		return err
	}

	recipe := a.watchRecipe(workspaceID)
	events := recipe.Events
	if len(events) == 0 {
		// Creation and rename-into-the-folder are the two ways a completed
		// download appears. Modification is deliberately absent: a file being
		// written fires it constantly and settles nothing (FR-27).
		events = []string{"create", "rename"}
	}
	debounce := recipe.DebounceSeconds
	if debounce <= 0 {
		debounce = int(DefaultWatchDebounce / time.Second)
	}

	record := TriggerRecord{
		WorkspaceID:     workspaceID,
		Name:            WatchTriggerName,
		Enabled:         true,
		Path:            settings.RootPath,
		Events:          events,
		DebounceSeconds: debounce,
		Domain:          DomainKey,
	}
	if existing != nil {
		record.ID = existing.ID
	}
	_, err = a.triggers.Upsert(record)
	return err
}

// watchRecipe returns the template's requested watch settings, or an empty
// recipe when the workspace has none.
func (a *Automation) watchRecipe(workspaceID string) workspace.WatchRecipe {
	ws, err := a.service.readWorkspace(workspaceID)
	if err != nil || ws == nil {
		return workspace.WatchRecipe{}
	}
	recipe, ok := ws.TemplateAutomationRecipeFor(DirectoryRequirementKey)
	if !ok || recipe.Watch == nil {
		return workspace.WatchRecipe{}
	}
	return *recipe.Watch
}

func (a *Automation) findWatcher(workspaceID string) (*TriggerRecord, error) {
	records, err := a.triggers.List(workspaceID)
	if err != nil {
		return nil, err
	}
	for i := range records {
		if records[i].Name == WatchTriggerName || records[i].Domain == DomainKey {
			return &records[i], nil
		}
	}
	return nil, nil
}

// RemoveWatcher deletes the workspace's watcher. Used when folder access is
// revoked: the watcher must stop before the binding it depends on disappears
// (FR-117).
func (a *Automation) RemoveWatcher(workspaceID string) error {
	if a == nil || a.triggers == nil {
		return nil
	}
	existing, err := a.findWatcher(workspaceID)
	if err != nil || existing == nil {
		return err
	}
	return a.triggers.Delete(workspaceID, existing.ID)
}

// HandleDomainScan services one coalesced watcher fire. It implements the
// trigger package's DomainScanHandler.
//
// The event count and summary are for logging only. The filenames a fire
// carries are never used to decide what to act on — the scan enumerates the
// folder itself, so a watcher event cannot smuggle in a file that would not
// otherwise qualify (FR-53).
func (a *Automation) HandleDomainScan(workspaceID, fireID string, eventCount int, summary string) error {
	logger.Debug("Downloads Janitor watcher fire", logger.Fields{
		"workspace_id": workspaceID, "fire_id": fireID, "events": eventCount,
	})
	a.RunCoalescedScan(workspaceID, ScanSourceWatcher)
	return nil
}

// RunCoalescedScan runs a scan unless one is already running for this
// workspace, in which case it records that another is wanted.
//
// The follow-up is a single flag, not a queue: any number of fires arriving
// during a scan collapse into exactly one more scan afterwards. That is what
// turns a burst of a hundred events into one active scan and one follow-up.
func (a *Automation) RunCoalescedScan(workspaceID string, source ScanSource) {
	a.mu.Lock()
	if a.running[workspaceID] {
		a.followUp[workspaceID] = true
		a.mu.Unlock()
		return
	}
	a.running[workspaceID] = true
	a.mu.Unlock()

	go a.drainScans(workspaceID, source)
}

// drainScans runs the scan, then any single follow-up that accumulated while it
// was running, until no more are wanted.
func (a *Automation) drainScans(workspaceID string, source ScanSource) {
	defer func() {
		a.mu.Lock()
		delete(a.running, workspaceID)
		a.mu.Unlock()
	}()

	for {
		a.runOnce(workspaceID, source)

		a.mu.Lock()
		wanted := a.followUp[workspaceID]
		delete(a.followUp, workspaceID)
		if !wanted {
			a.mu.Unlock()
			return
		}
		a.mu.Unlock()
	}
}

// runOnce performs one scan through exactly the same service path a manual scan
// uses, so eligibility, settling, classification, fingerprints, and
// deduplication cannot diverge between automatic and manual runs (FR-42).
func (a *Automation) runOnce(workspaceID string, source ScanSource) {
	settings, err := a.service.store.LoadSettings(workspaceID)
	if err != nil || !settings.IsSetUp() {
		return
	}
	// Pause stops unattended work; it does not stop the user asking for a scan.
	if settings.Paused {
		return
	}

	batch, created, err := a.scan(workspaceID, source)
	if err != nil {
		logger.Warn("Downloads Janitor scan failed", logger.Fields{
			"workspace_id": workspaceID, "source": source, "error": err,
		})
		a.service.reportScanFailure(workspaceID, err)
		return
	}
	if !created {
		// Nothing new. No batch, no notification — a scan that found nothing is
		// not news (FR-105).
		return
	}
	a.service.notifyBatchReady(workspaceID, batch)
}

// ---------------------------------------------------------------- scheduler

// Start begins the daily catch-up loop. It runs an immediate pass so a
// workspace that missed its time while Ori was closed is caught up as soon as
// Ori is available, rather than waiting for tomorrow (FR-38).
func (a *Automation) Start(workspaces func() []string, interval time.Duration) {
	if a == nil {
		return
	}
	if interval <= 0 {
		interval = time.Minute
	}
	a.mu.Lock()
	if a.stop != nil {
		a.mu.Unlock()
		return
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	a.stop, a.done = stop, done
	a.mu.Unlock()

	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		a.tick(workspaces)
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				a.tick(workspaces)
			}
		}
	}()
}

// Stop halts the scheduler.
func (a *Automation) Stop() {
	if a == nil {
		return
	}
	a.mu.Lock()
	stop, done := a.stop, a.done
	a.stop, a.done = nil, nil
	a.mu.Unlock()
	if stop == nil {
		return
	}
	close(stop)
	<-done
}

// tick checks every configured workspace and runs the ones whose local catch-up
// time has arrived and which have not already run today.
func (a *Automation) tick(workspaces func() []string) {
	if workspaces == nil {
		return
	}
	now := a.clock()
	for _, workspaceID := range workspaces() {
		if a.dueForCatchUp(workspaceID, now) {
			a.RunCoalescedScan(workspaceID, ScanSourceDaily)
		}
	}
}

// dueForCatchUp reports whether the workspace's daily run should happen now,
// and claims the local date if so.
//
// The claim is per local date rather than per elapsed day, so a 23- or 25-hour
// DST day still gets exactly one catch-up. A workspace that was unavailable at
// its scheduled time runs as soon as it is available on that same local date —
// which is the whole point of a catch-up (FR-38).
func (a *Automation) dueForCatchUp(workspaceID string, now time.Time) bool {
	settings, err := a.service.store.LoadSettings(workspaceID)
	if err != nil || !settings.IsSetUp() || settings.Paused {
		return false
	}
	localTime := settings.DailyScanLocalTime
	if strings.TrimSpace(localTime) == "" {
		localTime = DefaultDailyScanLocalTime
	}
	today := workspace.LocalDateKey(settings.Timezone, now)

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.lastCatchUp[workspaceID] == today {
		return false
	}

	// Today's slot, not the next one: a schedule that has not come round yet
	// must not look overdue.
	occurrence, err := workspace.LocalOccurrenceOn(settings.Timezone, localTime, now)
	if err != nil {
		return false
	}
	if now.Before(occurrence) {
		return false
	}
	a.lastCatchUp[workspaceID] = today
	return true
}

// WatcherRegistered reports whether an enabled watcher exists for the
// workspace, satisfying AutomationStatus.
func (a *Automation) WatcherRegistered(workspaceID string) (bool, error) {
	if a == nil || a.triggers == nil {
		return false, nil
	}
	existing, err := a.findWatcher(workspaceID)
	if err != nil {
		return false, err
	}
	return existing != nil && existing.Enabled, nil
}

// SchedulerRegistered reports whether the daily catch-up loop is running.
// A stopped scheduler means no catch-up happens, which readiness must not
// paper over.
func (a *Automation) SchedulerRegistered(string) bool {
	if a == nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.stop != nil
}

// MarkCaughtUp records that a workspace already ran today, used when restoring
// scheduler state after a restart.
func (a *Automation) MarkCaughtUp(workspaceID, localDate string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.lastCatchUp[workspaceID] = localDate
}
