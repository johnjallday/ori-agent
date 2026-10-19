package externalmemory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeClock is a settable clock whose timer requests the test answers.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	waits  chan time.Duration
	fire   chan time.Time
	frozen bool
}

func newFakeClock(now time.Time) *fakeClock {
	return &fakeClock{now: now, waits: make(chan time.Duration, 8), fire: make(chan time.Time)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.waits <- d
	return c.fire
}

type fakeSource struct {
	id        string
	available bool
	entries   []Entry
	problems  []ParseError
	err       error

	mu    sync.Mutex
	reads int
}

func (f *fakeSource) ID() string       { return f.id }
func (f *fakeSource) Location() string { return "/fake/" + f.id }
func (f *fakeSource) Available() bool  { return f.available }
func (f *fakeSource) Read(context.Context) ([]Entry, []ParseError, error) {
	f.mu.Lock()
	f.reads++
	f.mu.Unlock()
	return f.entries, f.problems, f.err
}

func (f *fakeSource) Reads() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads
}

func enable(t *testing.T, store *Store, ids ...string) {
	t.Helper()
	if _, err := store.UpdateSources(func(doc *SourcesDocument) error {
		for _, id := range ids {
			state := doc.Source(id)
			state.Enabled = true
			doc.Sources[id] = state
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func codexRunner(t *testing.T, root string) (*Runner, *Store) {
	t.Helper()
	store := NewStore(t.TempDir())
	enable(t, store, SourceCodex)
	return NewRunner(store, NewRegistry(NewCodexSource(root)), WithHome("/Users/example")), store
}

func onlyResult(t *testing.T, result RunResult) SourceResult {
	t.Helper()
	if len(result.Sources) != 1 {
		t.Fatalf("sources synced = %+v, want one", result.Sources)
	}
	return result.Sources[0]
}

func TestRun_FirstSyncAddsEveryEntry(t *testing.T) {
	runner, store := codexRunner(t, codexFixtureRoot(t))
	result, err := runner.Run(context.Background(), ReasonManual)
	if err != nil {
		t.Fatal(err)
	}
	sr := onlyResult(t, result)
	if sr.Result != ResultOK || sr.Counts.Added != 23 || sr.Counts.Entries != 23 || sr.Counts.Folders != 2 {
		t.Fatalf("first sync = %+v", sr)
	}
	doc := store.LoadSources()
	state := doc.Source(SourceCodex)
	if state.LastResult != ResultOK || state.Counts.Entries != 23 || state.LastSyncFinished == nil || doc.LastCompletedSync == nil {
		t.Fatalf("source state not recorded: %+v", state)
	}
}

func TestRun_UnchangedResyncTouchesNothing(t *testing.T) {
	runner, store := codexRunner(t, codexFixtureRoot(t))
	if _, err := runner.Run(context.Background(), ReasonManual); err != nil {
		t.Fatal(err)
	}
	before := NewStore(store.Dir()).LoadEntries(SourceCodex)
	path, _ := store.entriesPath(SourceCodex)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(10 * time.Millisecond)
	result, err := runner.Run(context.Background(), ReasonManual)
	if err != nil {
		t.Fatal(err)
	}
	sr := onlyResult(t, result)
	if sr.Counts.Added+sr.Counts.Updated+sr.Counts.Removed != 0 {
		t.Fatalf("unchanged re-sync changed entries: %+v", sr.Counts)
	}
	if after := NewStore(store.Dir()).LoadEntries(SourceCodex); !reflect.DeepEqual(after, before) {
		t.Fatal("unchanged re-sync altered stored entries")
	}
	if again, _ := os.Stat(path); !again.ModTime().Equal(info.ModTime()) {
		t.Fatal("unchanged re-sync rewrote the entries file")
	}
}

func TestRun_EditingOneFileUpdatesExactlyOneEntry(t *testing.T) {
	root := copyFixture(t, codexFixtureRoot(t))
	runner, store := codexRunner(t, root)
	if _, err := runner.Run(context.Background(), ReasonManual); err != nil {
		t.Fatal(err)
	}
	rollout := filepath.Join(root, "memories", "rollout_summaries", "2026-08-02T10-00-00-IjKl-notes_release.md")
	appendTo(t, rollout, "\n- Added a crash reporter before the next release.\n")

	result, err := runner.Run(context.Background(), ReasonManual)
	if err != nil {
		t.Fatal(err)
	}
	if counts := onlyResult(t, result).Counts; counts.Updated != 1 || counts.Added != 0 || counts.Removed != 0 {
		t.Fatalf("after one edit: %+v, want exactly one update", counts)
	}
	edited := find(t, entriesFrom(store.LoadEntries(SourceCodex), "notes_release.md"), "Notes app 2.3 release")
	if !strings.Contains(edited.Body, "crash reporter") {
		t.Fatal("update not stored")
	}
}

func TestRun_DeletingOneFileMarksOneRemoved(t *testing.T) {
	root := copyFixture(t, codexFixtureRoot(t))
	runner, store := codexRunner(t, root)
	if _, err := runner.Run(context.Background(), ReasonManual); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(root, "memories", "rollout_summaries", "2026-08-02T10-00-00-IjKl-notes_release.md")
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	result, err := runner.Run(context.Background(), ReasonManual)
	if err != nil {
		t.Fatal(err)
	}
	sr := onlyResult(t, result)
	if sr.Counts.Removed != 1 || sr.Counts.Updated != 0 || sr.Counts.Added != 0 || sr.Counts.Entries != 22 {
		t.Fatalf("after one delete: %+v", sr.Counts)
	}
	removed := entriesFrom(store.LoadEntries(SourceCodex), "notes_release.md")
	if len(removed) != 1 || removed[0].State != StateRemoved || removed[0].RemovedAt == nil {
		t.Fatalf("deleted file's entry should be kept as removed: %+v", removed)
	}
}

// TestRun_NeverChangesTheSourceTree is the FR12 integrity check: a sync of the
// checked-in fixture tree leaves every file and folder exactly as it was.
func TestRun_NeverChangesTheSourceTree(t *testing.T) {
	root := codexFixtureRoot(t)
	before := snapshotTree(t, root)
	runner, _ := codexRunner(t, root)
	for range 2 {
		if _, err := runner.Run(context.Background(), ReasonManual); err != nil {
			t.Fatal(err)
		}
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("sync changed the source tree:\nbefore %v\nafter  %v", before, after)
	}
}

func TestRun_HonorsExclusionPrefixes(t *testing.T) {
	runner, store := codexRunner(t, codexFixtureRoot(t))
	if _, err := runner.Run(context.Background(), ReasonManual); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateSources(func(doc *SourcesDocument) error {
		state := doc.Source(SourceCodex)
		state.Exclusions = []string{"~/Projects/notes-app"}
		doc.Sources[SourceCodex] = state
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	result, err := runner.Run(context.Background(), ReasonManual)
	if err != nil {
		t.Fatal(err)
	}
	sr := onlyResult(t, result)
	if sr.Counts.Removed == 0 || sr.Counts.Folders != 1 {
		t.Fatalf("exclusion did not remove the folder's entries: %+v", sr.Counts)
	}
	for _, e := range store.LoadEntries(SourceCodex) {
		if strings.HasPrefix(e.Folder, "/Users/example/Projects/notes-app") && e.Active() {
			t.Fatalf("excluded entry still active: %q", e.Title)
		}
	}
	if stored := store.LoadSources().Source(SourceCodex).Exclusions; !reflect.DeepEqual(stored, []string{"~/Projects/notes-app"}) {
		t.Fatalf("sync rewrote the user's exclusions: %v", stored)
	}
}

func TestRun_SkipsDisabledSourcesAndRecordsFailuresPerSource(t *testing.T) {
	store := NewStore(t.TempDir())
	good := &fakeSource{id: SourceCodex, available: true, entries: []Entry{
		newEntry(SourceCodex, "/f", "a", Entry{Title: "a", Body: "a"}),
	}, problems: []ParseError{{Path: "/f/bad1.md", Reason: "unreadable"}, {Path: "/f/bad2.md", Reason: "unreadable"}}}
	broken := &fakeSource{id: SourceClaudeCode, available: true, err: errors.New("claude code memory folder not found")}
	off := &fakeSource{id: SourceGeminiCLI, available: true}
	enable(t, store, SourceCodex, SourceClaudeCode)
	runner := NewRunner(store, NewRegistry(good, broken, off))

	result, err := runner.Run(context.Background(), ReasonManual)
	if err != nil {
		t.Fatal(err)
	}
	if off.Reads() != 0 {
		t.Fatal("a disabled source was read")
	}
	if len(result.Sources) != 2 || result.Sources[0].Result != ResultPartial || result.Sources[1].Result != ResultError {
		t.Fatalf("results = %+v", result.Sources)
	}
	doc := store.LoadSources()
	if s := doc.Source(SourceCodex); s.SkippedFiles != 2 || s.Counts.Entries != 1 {
		t.Fatalf("partial source state: %+v", s)
	}
	if s := doc.Source(SourceClaudeCode); s.LastError == "" || s.LastResult != ResultError {
		t.Fatalf("failed source state: %+v", s)
	}
}

func TestRun_UnavailableSourceMarksItsEntriesRemoved(t *testing.T) {
	store := NewStore(t.TempDir())
	source := &fakeSource{id: SourceCodex, available: true, entries: []Entry{newEntry(SourceCodex, "/f", "a", Entry{Title: "a"})}}
	enable(t, store, SourceCodex)
	runner := NewRunner(store, NewRegistry(source))
	if _, err := runner.Run(context.Background(), ReasonManual); err != nil {
		t.Fatal(err)
	}
	source.available = false
	result, err := runner.Run(context.Background(), ReasonManual)
	if err != nil {
		t.Fatal(err)
	}
	if sr := onlyResult(t, result); sr.Result != ResultUnavailable || sr.Counts.Removed != 1 || sr.Counts.Entries != 0 {
		t.Fatalf("unavailable source result: %+v", sr)
	}
	source.available = true
	result, _ = runner.Run(context.Background(), ReasonManual)
	if sr := onlyResult(t, result); sr.Counts.Updated != 1 || sr.Counts.Entries != 1 {
		t.Fatalf("reappearing source should reactivate its entry: %+v", sr.Counts)
	}
}

func TestRun_KeepsPromotedLinkThroughUpdateAndRemoval(t *testing.T) {
	store := NewStore(t.TempDir())
	entry := newEntry(SourceCodex, "/f", "a", Entry{Title: "a", Body: "v1"})
	source := &fakeSource{id: SourceCodex, available: true, entries: []Entry{entry}}
	enable(t, store, SourceCodex)
	runner := NewRunner(store, NewRegistry(source))
	if _, err := runner.Run(context.Background(), ReasonManual); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.UpdateEntry(SourceCodex, entry.ID, func(e *Entry) { e.PromotedItemID = "item-1" }); err != nil || !ok {
		t.Fatalf("UpdateEntry: ok=%v err=%v", ok, err)
	}
	source.entries = []Entry{newEntry(SourceCodex, "/f", "a", Entry{Title: "a", Body: "v2"})}
	if _, err := runner.Run(context.Background(), ReasonManual); err != nil {
		t.Fatal(err)
	}
	if got := store.LoadEntries(SourceCodex)[0]; got.Body != "v2" || got.PromotedItemID != "item-1" {
		t.Fatalf("update lost the promoted link: %+v", got)
	}
	source.entries = nil
	if _, err := runner.Run(context.Background(), ReasonManual); err != nil {
		t.Fatal(err)
	}
	if got := store.LoadEntries(SourceCodex)[0]; got.State != StateRemoved || got.PromotedItemID != "item-1" {
		t.Fatalf("removal lost the promoted link: %+v", got)
	}
}

func TestMergeEntries_PrunesOldUnpromotedRemovals(t *testing.T) {
	now := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	longAgo, recent := now.Add(-removedRetention-time.Hour), now.Add(-time.Hour)
	prev := []Entry{
		{ID: "old", State: StateRemoved, RemovedAt: &longAgo, Body: "x"},
		{ID: "old-promoted", State: StateRemoved, RemovedAt: &longAgo, PromotedItemID: "item"},
		{ID: "recent", State: StateRemoved, RemovedAt: &recent},
	}
	total := 1
	out, delta, _ := mergeEntries(prev, nil, now, &total)
	var ids []string
	for _, e := range out {
		ids = append(ids, e.ID)
	}
	if !reflect.DeepEqual(ids, []string{"old-promoted", "recent"}) || delta.pruned != 1 || total != 0 {
		t.Fatalf("kept %v (pruned %d, total %d)", ids, delta.pruned, total)
	}
}

func TestMergeEntries_StopsAddingAtTheStoreCap(t *testing.T) {
	total := MaxStoreBytes - 5
	next := []Entry{{ID: "big", Body: "0123456789"}, {ID: "small", Body: "abc"}}
	out, delta, full := mergeEntries(nil, next, time.Now(), &total)
	if !full || delta.Added != 1 || len(out) != 1 || out[0].ID != "small" {
		t.Fatalf("cap: full=%v added=%d out=%+v", full, delta.Added, out)
	}
}

func TestTryRun_ReportsASyncInFlight(t *testing.T) {
	store := NewStore(t.TempDir())
	enable(t, store, SourceCodex)
	gate := make(chan struct{})
	blocking := &blockingSource{fakeSource: fakeSource{id: SourceCodex, available: true}, gate: gate, entered: make(chan struct{})}
	runner := NewRunner(store, NewRegistry(blocking))
	t.Cleanup(runner.WaitIdle)

	go func() { _, _ = runner.Run(context.Background(), ReasonSchedule) }()
	<-blocking.entered
	if status := runner.Status(); !status.Running || status.Reason != ReasonSchedule || status.StartedAt == nil {
		t.Fatalf("status during sync = %+v", status)
	}
	if _, started, err := runner.TryRun(context.Background(), ReasonManual); started || err != nil {
		t.Fatalf("TryRun during a sync: started=%v err=%v", started, err)
	}
	close(gate)
	waitFor(t, func() bool { return !runner.Status().Running })
}

type blockingSource struct {
	fakeSource
	gate    chan struct{}
	entered chan struct{}
	once    sync.Once
}

func (b *blockingSource) Read(ctx context.Context) ([]Entry, []ParseError, error) {
	b.once.Do(func() { close(b.entered) })
	<-b.gate
	return b.fakeSource.Read(ctx)
}

func TestRunInBackground_SyncsAndCoalesces(t *testing.T) {
	store := NewStore(t.TempDir())
	enable(t, store, SourceCodex)
	source := &fakeSource{id: SourceCodex, available: true, entries: []Entry{newEntry(SourceCodex, "/f", "a", Entry{Title: "a"})}}
	runner := NewRunner(store, NewRegistry(source))
	runner.RunInBackground(ReasonEnabled)
	runner.RunInBackground(ReasonEnabled)
	runner.WaitIdle()
	if store.LoadSources().Source(SourceCodex).Counts.Entries != 1 {
		t.Fatal("background sync did not run")
	}
	if reads := source.Reads(); reads < 1 || reads > 2 {
		t.Fatalf("two quick requests ran %d syncs, want one or two", reads)
	}
}

func TestOnSynced_RunsAfterEachCompletedSync(t *testing.T) {
	runner, _ := codexRunner(t, codexFixtureRoot(t))
	var reasons []string
	runner.OnSynced(func(result RunResult) { reasons = append(reasons, result.Reason) })
	_, _ = runner.Run(context.Background(), ReasonManual)
	_, _ = runner.Run(context.Background(), ReasonSchedule)
	if !reflect.DeepEqual(reasons, []string{ReasonManual, ReasonSchedule}) {
		t.Fatalf("hook reasons = %v", reasons)
	}
}

func TestSchedule_SyncsAtStartWhenNeverSyncedThenDaily(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC))
	store := NewStore(t.TempDir())
	enable(t, store, SourceCodex)
	source := &fakeSource{id: SourceCodex, available: true}
	runner := NewRunner(store, NewRegistry(source), WithClock(clock.Now, clock.After))

	runner.Start()
	defer runner.Stop()
	if wait := <-clock.waits; wait != SyncInterval {
		t.Fatalf("after the startup sync the timer waits %v, want %v", wait, SyncInterval)
	}
	if source.Reads() != 1 {
		t.Fatalf("startup reads = %d, want 1", source.Reads())
	}
	if due := runner.NextDue(); !due.Equal(clock.Now().Add(SyncInterval)) {
		t.Fatalf("next due = %v", due)
	}

	clock.Advance(SyncInterval)
	clock.fire <- clock.Now()
	if wait := <-clock.waits; wait != SyncInterval {
		t.Fatalf("after the daily sync the timer waits %v", wait)
	}
	if source.Reads() != 2 {
		t.Fatalf("daily sync reads = %d, want 2", source.Reads())
	}
}

func TestSchedule_RecentSyncArmsTheTimerForTheRemainder(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC))
	store := NewStore(t.TempDir())
	last := clock.Now().Add(-2 * time.Hour)
	if _, err := store.UpdateSources(func(doc *SourcesDocument) error {
		doc.LastCompletedSync = &last
		doc.Sources[SourceCodex] = SourceState{Enabled: true}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	source := &fakeSource{id: SourceCodex, available: true}
	runner := NewRunner(store, NewRegistry(source), WithClock(clock.Now, clock.After))

	runner.Start()
	if wait := <-clock.waits; wait != 22*time.Hour {
		t.Fatalf("timer waits %v, want the 22h remainder", wait)
	}
	runner.Stop()
	if source.Reads() != 0 {
		t.Fatal("a restart soon after a sync re-read the source")
	}
	// Stop then Start again works (the menubar restarts the server in-process).
	runner.Start()
	<-clock.waits
	runner.Stop()
}

func TestStop_WithoutStartIsANoop(t *testing.T) {
	NewRunner(NewStore(t.TempDir()), NewRegistry()).Stop()
}

// --- helpers ---

// snapshotTree records every path under root with its type, size, mode,
// modification time, and content hash.
func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		record := info.Mode().String() + " " + info.ModTime().UTC().Format(time.RFC3339Nano)
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(data)
			record += " " + hex.EncodeToString(sum[:])
		}
		out[rel] = record
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func appendTo(t *testing.T, path, text string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(text); err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within 5s")
}
