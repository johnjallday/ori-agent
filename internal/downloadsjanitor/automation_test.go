package downloadsjanitor

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	workspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// fakeTriggers is an in-memory trigger store.
type fakeTriggers struct {
	mu      sync.Mutex
	records map[string][]TriggerRecord
	nextID  int
}

func newFakeTriggers() *fakeTriggers {
	return &fakeTriggers{records: map[string][]TriggerRecord{}}
}

func (f *fakeTriggers) List(workspaceID string) ([]TriggerRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]TriggerRecord(nil), f.records[workspaceID]...), nil
}

func (f *fakeTriggers) Upsert(record TriggerRecord) (TriggerRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if record.ID == "" {
		f.nextID++
		record.ID = "trigger-" + string(rune('a'+f.nextID))
		f.records[record.WorkspaceID] = append(f.records[record.WorkspaceID], record)
		return record, nil
	}
	for i, existing := range f.records[record.WorkspaceID] {
		if existing.ID == record.ID {
			f.records[record.WorkspaceID][i] = record
			return record, nil
		}
	}
	f.records[record.WorkspaceID] = append(f.records[record.WorkspaceID], record)
	return record, nil
}

func (f *fakeTriggers) Delete(workspaceID, triggerID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	kept := f.records[workspaceID][:0:0]
	for _, existing := range f.records[workspaceID] {
		if existing.ID != triggerID {
			kept = append(kept, existing)
		}
	}
	f.records[workspaceID] = kept
	return nil
}

// fakeNotifier records Action Center entries by title, mirroring the real
// store's dedup-by-title behavior.
type fakeNotifier struct {
	mu      sync.Mutex
	byTitle map[string]workspace.Opportunity
	upserts int
}

func newFakeNotifier() *fakeNotifier {
	return &fakeNotifier{byTitle: map[string]workspace.Opportunity{}}
}

func (f *fakeNotifier) Upsert(opp workspace.Opportunity) (workspace.Opportunity, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upserts++
	_, merged := f.byTitle[opp.Title]
	f.byTitle[opp.Title] = opp
	return opp, merged, nil
}

func (f *fakeNotifier) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.byTitle)
}

func automationFixture(t *testing.T) (*Automation, *Service, string, *fakeTriggers, *fakeNotifier) {
	t.Helper()
	service, root := configuredService(t)
	service.SetMover(&realMover{})
	triggers := newFakeTriggers()
	notifier := newFakeNotifier()
	service.SetNotifier(notifier)
	return NewAutomation(service, triggers), service, root, triggers, notifier
}

func TestEnsureWatcher_InstallsOneNonRecursiveWatcherOnTheApprovedFolder(t *testing.T) {
	automation, _, root, triggers, _ := automationFixture(t)

	if err := automation.EnsureWatcher("ws-1"); err != nil {
		t.Fatalf("EnsureWatcher: %v", err)
	}
	records, _ := triggers.List("ws-1")
	if len(records) != 1 {
		t.Fatalf("expected one watcher, got %d", len(records))
	}
	watcher := records[0]
	if watcher.Path != root {
		t.Fatalf("watcher path = %q, want the approved folder", watcher.Path)
	}
	if watcher.Domain != DomainKey {
		t.Fatalf("the fire must route to the Janitor, not an agent: %q", watcher.Domain)
	}
	// Creation and rename-into-the-folder only: modification fires constantly
	// while a file is being written and settles nothing.
	if len(watcher.Events) != 2 {
		t.Fatalf("watcher events = %v", watcher.Events)
	}
	for _, event := range watcher.Events {
		if event != "create" && event != "rename" {
			t.Fatalf("unexpected watch event %q", event)
		}
	}
	if watcher.DebounceSeconds != int(DefaultWatchDebounce/time.Second) {
		t.Fatalf("debounce = %ds, want the 5-minute window", watcher.DebounceSeconds)
	}

	// Repeating setup updates the same watcher rather than adding a second.
	if err := automation.EnsureWatcher("ws-1"); err != nil {
		t.Fatalf("EnsureWatcher (repeat): %v", err)
	}
	records, _ = triggers.List("ws-1")
	if len(records) != 1 {
		t.Fatalf("repeating setup must not add a second watcher: %d", len(records))
	}
}

func TestEnsureWatcher_StaysDisabledUntilSetupAndWhilePaused(t *testing.T) {
	store, _ := newTestStore(t)
	service := NewService(store, newFakeWorkspaceStore("ws-1"))
	triggers := newFakeTriggers()
	automation := NewAutomation(service, triggers)

	// Not set up: nothing to watch.
	if err := automation.EnsureWatcher("ws-1"); err != nil {
		t.Fatalf("EnsureWatcher: %v", err)
	}
	if records, _ := triggers.List("ws-1"); len(records) != 0 {
		t.Fatalf("an unconfigured workspace must have no watcher: %+v", records)
	}

	// Configure, then pause: the watcher is kept but disabled, so resuming does
	// not have to rebuild it.
	automation2, service2, _, triggers2, _ := automationFixture(t)
	if err := automation2.EnsureWatcher("ws-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := service2.store.UpdateSettings("ws-1", func(s *JanitorSettings) error {
		s.Paused = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := automation2.EnsureWatcher("ws-1"); err != nil {
		t.Fatal(err)
	}
	records, _ := triggers2.List("ws-1")
	if len(records) != 1 || records[0].Enabled {
		t.Fatalf("a paused workspace keeps a disabled watcher: %+v", records)
	}
}

func TestRemoveWatcher_StopsWatchingBeforeAccessGoesAway(t *testing.T) {
	automation, _, _, triggers, _ := automationFixture(t)
	if err := automation.EnsureWatcher("ws-1"); err != nil {
		t.Fatal(err)
	}
	if err := automation.RemoveWatcher("ws-1"); err != nil {
		t.Fatalf("RemoveWatcher: %v", err)
	}
	if records, _ := triggers.List("ws-1"); len(records) != 0 {
		t.Fatalf("the watcher must be gone: %+v", records)
	}
}

// The coalescing rule, stated as the PRD states it: a hundred events produce
// one active scan and at most one follow-up.
func TestCoalescing_AHundredEventsProduceOneScanAndOneFollowUp(t *testing.T) {
	automation, service, root, _, _ := automationFixture(t)

	var mu sync.Mutex
	scans := 0
	release := make(chan struct{})
	// Hold the first scan open so every other event arrives while it is running.
	realScan := service.ScanNow
	automation.scan = func(workspaceID string, source ScanSource) (JanitorBatch, bool, error) {
		mu.Lock()
		scans++
		count := scans
		mu.Unlock()
		if count == 1 {
			<-release
		}
		return realScan(workspaceID, source)
	}
	agedFile(t, root, "report.pdf", 100)

	automation.RunCoalescedScan("ws-1", ScanSourceWatcher)
	// A hundred more fires arrive while the first scan is in flight.
	for range 100 {
		automation.RunCoalescedScan("ws-1", ScanSourceWatcher)
	}
	close(release)

	// Wait for the drain to finish.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		automation.mu.Lock()
		running := automation.running["ws-1"]
		automation.mu.Unlock()
		if !running {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if scans != 2 {
		t.Fatalf("100 events should yield one scan plus one follow-up, got %d scans", scans)
	}
}

func TestAutomaticScans_CreateNoTasksAndOneNotificationPerBatch(t *testing.T) {
	automation, _, root, _, notifier := automationFixture(t)
	agedFile(t, root, "a.pdf", 10)
	agedFile(t, root, "b.pdf", 10)
	agedFile(t, root, "c.pdf", 10)

	automation.runOnce("ws-1", ScanSourceWatcher)

	// Three files, one entry — not one per file.
	if notifier.count() != 1 {
		t.Fatalf("expected exactly one Action Center entry, got %d", notifier.count())
	}
	entry := notifier.byTitle[readyBatchTitle]
	if entry.Summary == "" {
		t.Fatal("the entry should say how many files are waiting")
	}
	if entry.WorkspaceID != "ws-1" {
		t.Fatalf("entry workspace = %q", entry.WorkspaceID)
	}
}

// A scan that finds nothing is not news.
func TestAutomaticScans_SayNothingWhenThereIsNothingToSay(t *testing.T) {
	automation, _, root, _, notifier := automationFixture(t)
	agedFile(t, root, "a.pdf", 10)

	automation.runOnce("ws-1", ScanSourceWatcher)
	first := notifier.upserts

	// Nothing changed since; the next run proposes nothing.
	automation.runOnce("ws-1", ScanSourceDaily)
	if notifier.upserts != first {
		t.Fatal("a scan with nothing new must not create or update a notification")
	}
}

// Repeated failures update one entry rather than flooding.
func TestAutomaticScans_RepeatedFailuresUpdateOneEntry(t *testing.T) {
	automation, service, root, _, notifier := automationFixture(t)
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}

	for range 5 {
		automation.runOnce("ws-1", ScanSourceDaily)
	}
	if notifier.count() != 1 {
		t.Fatalf("five failures should produce one entry, got %d", notifier.count())
	}
	entry := notifier.byTitle[needsAttentionText]
	if entry.RecommendedAction == "" {
		t.Fatal("a needs-attention entry must offer a repair")
	}
	// The service is still usable once the folder comes back.
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.ScanNow("ws-1", ScanSourceManual); err != nil {
		t.Fatalf("scanning should recover: %v", err)
	}
}

// Pausing stops unattended work but not the user's own scans.
func TestPause_StopsAutomaticRunsButNotManualOnes(t *testing.T) {
	automation, service, root, _, notifier := automationFixture(t)
	agedFile(t, root, "a.pdf", 10)
	if _, err := service.store.UpdateSettings("ws-1", func(s *JanitorSettings) error {
		s.Paused = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	automation.runOnce("ws-1", ScanSourceWatcher)
	if notifier.upserts != 0 {
		t.Fatal("a paused workspace must not run unattended scans")
	}
	batches, _ := service.ListBatches("ws-1")
	if len(batches) != 0 {
		t.Fatalf("a paused workspace must produce no automatic batches: %d", len(batches))
	}

	// The user can still ask for one explicitly.
	if _, created, err := service.ScanNow("ws-1", ScanSourceManual); err != nil || !created {
		t.Fatalf("a manual scan must still work while paused: created=%v err=%v", created, err)
	}
}

// ---------------------------------------------------------------- scheduler

func TestDailyCatchUp_RunsOncePerLocalDate(t *testing.T) {
	automation, service, _, _, _ := automationFixture(t)
	if _, err := service.store.UpdateSettings("ws-1", func(s *JanitorSettings) error {
		s.DailyScanLocalTime = "09:00"
		s.Timezone = "America/New_York"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("America/New_York")

	// Before the scheduled time: not due.
	automation.now = func() time.Time { return time.Date(2026, 7, 24, 8, 30, 0, 0, loc) }
	if automation.dueForCatchUp("ws-1", automation.clock()) {
		t.Fatal("a catch-up must not run before its local time")
	}

	// After it: due, once.
	automation.now = func() time.Time { return time.Date(2026, 7, 24, 9, 30, 0, 0, loc) }
	if !automation.dueForCatchUp("ws-1", automation.clock()) {
		t.Fatal("the catch-up should be due after its local time")
	}
	if automation.dueForCatchUp("ws-1", automation.clock()) {
		t.Fatal("the catch-up must not run twice on the same local date")
	}

	// The next local date is a fresh run.
	automation.now = func() time.Time { return time.Date(2026, 7, 25, 9, 30, 0, 0, loc) }
	if !automation.dueForCatchUp("ws-1", automation.clock()) {
		t.Fatal("a new local date should be due again")
	}
}

// A workspace that was unavailable at its scheduled time runs as soon as Ori is
// available on that same local date — that is what a catch-up is for.
func TestDailyCatchUp_RunsAfterDowntimeOnTheSameDate(t *testing.T) {
	automation, service, _, _, _ := automationFixture(t)
	if _, err := service.store.UpdateSettings("ws-1", func(s *JanitorSettings) error {
		s.DailyScanLocalTime = "09:00"
		s.Timezone = "UTC"
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Ori starts at 4pm, long after the 9am slot it missed.
	automation.now = func() time.Time { return time.Date(2026, 7, 24, 16, 0, 0, 0, time.UTC) }
	if !automation.dueForCatchUp("ws-1", automation.clock()) {
		t.Fatal("a missed slot must be caught up when Ori becomes available")
	}
}

// A day that is 23 or 25 hours long still gets exactly one catch-up, because
// the claim is per local date rather than per elapsed 24 hours.
func TestDailyCatchUp_HandlesDaylightSavingTransitions(t *testing.T) {
	automation, service, _, _, _ := automationFixture(t)
	if _, err := service.store.UpdateSettings("ws-1", func(s *JanitorSettings) error {
		s.DailyScanLocalTime = "09:00"
		s.Timezone = "America/New_York"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("America/New_York")

	// Spring forward: 2026-03-08 is a 23-hour day in New York.
	for _, hour := range []int{10, 14, 20} {
		automation.now = func() time.Time { return time.Date(2026, 3, 8, hour, 0, 0, 0, loc) }
		automation.dueForCatchUp("ws-1", automation.clock())
	}
	automation.mu.Lock()
	claimed := automation.lastCatchUp["ws-1"]
	automation.mu.Unlock()
	if claimed != "2026-03-08" {
		t.Fatalf("claimed date = %q, want the local date", claimed)
	}

	// Autumn back: 2026-11-01 is a 25-hour day, and still one run.
	runs := 0
	for _, hour := range []int{10, 14, 20, 23} {
		automation.now = func() time.Time { return time.Date(2026, 11, 1, hour, 0, 0, 0, loc) }
		if automation.dueForCatchUp("ws-1", automation.clock()) {
			runs++
		}
	}
	if runs != 1 {
		t.Fatalf("a 25-hour day must still get exactly one catch-up, got %d", runs)
	}
}

// A timezone or time change is picked up on the next tick, with no stored
// "next run" to migrate.
func TestDailyCatchUp_ConfigChangesApplyOnTheNextTick(t *testing.T) {
	automation, service, _, _, _ := automationFixture(t)
	loc, _ := time.LoadLocation("America/New_York")
	automation.now = func() time.Time { return time.Date(2026, 7, 24, 12, 0, 0, 0, loc) }

	if _, err := service.store.UpdateSettings("ws-1", func(s *JanitorSettings) error {
		s.DailyScanLocalTime = "23:00"
		s.Timezone = "America/New_York"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if automation.dueForCatchUp("ws-1", automation.clock()) {
		t.Fatal("11pm has not arrived at noon")
	}

	// The user moves it earlier; the very next check honours it.
	if _, err := service.store.UpdateSettings("ws-1", func(s *JanitorSettings) error {
		s.DailyScanLocalTime = "08:00"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !automation.dueForCatchUp("ws-1", automation.clock()) {
		t.Fatal("a changed schedule must apply without a restart")
	}
}

// Scheduler state survives a restart: a workspace already caught up today is
// not run again just because the process bounced.
func TestDailyCatchUp_RestartDoesNotRepeatTodaysRun(t *testing.T) {
	automation, service, _, _, _ := automationFixture(t)
	if _, err := service.store.UpdateSettings("ws-1", func(s *JanitorSettings) error {
		s.DailyScanLocalTime = "09:00"
		s.Timezone = "UTC"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)
	automation.now = func() time.Time { return now }

	// A fresh process, told what already happened today.
	restarted := NewAutomation(service, newFakeTriggers())
	restarted.now = automation.now
	restarted.MarkCaughtUp("ws-1", workspace.LocalDateKey("UTC", now))
	if restarted.dueForCatchUp("ws-1", now) {
		t.Fatal("a workspace already caught up today must not run again after a restart")
	}
}

func TestScheduler_StartsAndStopsCleanly(t *testing.T) {
	automation, service, root, _, notifier := automationFixture(t)
	agedFile(t, root, "a.pdf", 10)
	if _, err := service.store.UpdateSettings("ws-1", func(s *JanitorSettings) error {
		s.DailyScanLocalTime = "00:00"
		s.Timezone = "UTC"
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	automation.Start(func() []string { return []string{"ws-1"} }, 20*time.Millisecond)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && notifier.count() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	automation.Stop()

	if notifier.count() == 0 {
		t.Fatal("the scheduler should have run the due catch-up")
	}
	// Stopping twice is harmless.
	automation.Stop()
}

// The watcher's event payload is never the source of truth: the scan
// enumerates the folder itself.
func TestDomainScanHandler_IgnoresEventPayloadAndScansTheFolder(t *testing.T) {
	automation, service, root, _, _ := automationFixture(t)
	agedFile(t, root, "real.pdf", 10)

	// The fire claims something entirely different happened.
	if err := automation.HandleDomainScan("ws-1", "fire-1", 3, "created: ../../etc/passwd"); err != nil {
		t.Fatalf("HandleDomainScan: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	var candidates []JanitorCandidate
	for time.Now().Before(deadline) {
		_, candidates, _, _ = service.LatestPendingBatch("ws-1")
		if len(candidates) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(candidates) != 1 || candidates[0].Name != "real.pdf" {
		t.Fatalf("the scan must enumerate the folder itself, got %+v", candidates)
	}
}

func TestNextDailyOccurrence_MatchesTheDailyBriefBehaviour(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	after := time.Date(2026, 7, 24, 8, 0, 0, 0, loc)

	next, err := workspace.NextDailyOccurrence("America/New_York", "09:00", after)
	if err != nil {
		t.Fatalf("NextDailyOccurrence: %v", err)
	}
	if next.Hour() != 9 || next.Day() != 24 {
		t.Fatalf("next = %v, want 09:00 the same day", next)
	}

	// After the time, it rolls to tomorrow.
	next, err = workspace.NextDailyOccurrence("America/New_York", "09:00", time.Date(2026, 7, 24, 10, 0, 0, 0, loc))
	if err != nil {
		t.Fatal(err)
	}
	if next.Day() != 25 {
		t.Fatalf("next = %v, want the following day", next)
	}

	// A bad zone or time is an error, not a silent fallback to some other hour.
	if _, err := workspace.NextDailyOccurrence("Mars/Olympus", "09:00", after); err == nil {
		t.Fatal("an unknown timezone must be an error")
	}
	if _, err := workspace.NextDailyOccurrence("UTC", "nope", after); err == nil {
		t.Fatal("an unparseable time must be an error")
	}
}

func TestAutomation_IsWorkspaceScoped(t *testing.T) {
	automation, service, root, _, notifier := automationFixture(t)
	agedFile(t, root, "a.pdf", 10)

	automation.runOnce("ws-2", ScanSourceWatcher)
	if notifier.upserts != 0 {
		t.Fatal("an unconfigured workspace must produce nothing")
	}
	if batches, _ := service.ListBatches("ws-2"); len(batches) != 0 {
		t.Fatalf("ws-2 must have no batches: %d", len(batches))
	}

	var unavailable *SetupError
	if _, _, err := service.ScanNow("ws-2", ScanSourceDaily); !errors.As(err, &unavailable) {
		t.Fatalf("expected a setup error for an unconfigured workspace, got %v", err)
	}
	_ = filepath.Join(root, "unused")
}
