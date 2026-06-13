package trigger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestWatchSetup(t *testing.T, watchDir, glob string) (*WatchManager, *Store, *dispatchRecorder, Trigger) {
	t.Helper()
	store, _ := newTestStore(t, "ws1")
	trg, err := store.Create(Trigger{
		WorkspaceID: "ws1",
		Name:        "drop-folder",
		Type:        TypeFileWatch,
		Enabled:     true,
		Action:      Action{Kind: ActionTaskPrompt, Agent: "filer", Prompt: "Handle the file."},
		FileWatch:   &FileWatchConfig{Path: watchDir, Glob: glob},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	rec := newDispatchRecorder(false)
	c := NewCoalescer(store, rec.dispatch)
	c.debounceFor = func(Trigger) time.Duration { return testDebounce }
	t.Cleanup(func() {
		c.Close()
		c.WaitIdle()
	})

	m, err := NewWatchManager(store, c, &fakeOppStore{})
	if err != nil {
		t.Fatalf("NewWatchManager: %v", err)
	}
	m.Start()
	t.Cleanup(m.Close)
	return m, store, rec, trg
}

func TestWatchFiresOnMatchingCreate(t *testing.T) {
	dir := t.TempDir()
	_, _, rec, _ := newTestWatchSetup(t, dir, "*.txt")

	// fsnotify needs a beat to arm the watch on some platforms.
	time.Sleep(100 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec.waitDispatch(t)
	fire := rec.fire(0)
	if len(fire.Events) == 0 || fire.Events[0].FileName != "hello.txt" {
		t.Errorf("unexpected fire events: %+v", fire.Events)
	}
	if fire.Events[0].FileEvent != "create" {
		t.Errorf("event type = %q, want create", fire.Events[0].FileEvent)
	}
}

func TestWatchGlobExcludesNonMatching(t *testing.T) {
	dir := t.TempDir()
	_, _, rec, _ := newTestWatchSetup(t, dir, "*.pdf")

	time.Sleep(100 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Give the pipeline ample time to (not) fire.
	time.Sleep(1 * time.Second)
	if rec.count() != 0 {
		t.Errorf("glob-excluded file dispatched %d fires, want 0", rec.count())
	}
}

func TestWatchPathLostDisablesTriggerAndFilesFinding(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "drop")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	m, store, _, trg := newTestWatchSetup(t, dir, "")

	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	// Invoke the sweep directly instead of waiting out the minute ticker.
	m.validateWatches()

	got, _ := store.Get("ws1", trg.ID)
	if got.Enabled {
		t.Error("trigger should be disabled after its watch dir vanished")
	}
	if !strings.Contains(got.LastError, "file watch stopped") {
		t.Errorf("LastError = %q", got.LastError)
	}
	opps := m.opportunities.(*fakeOppStore)
	if len(opps.opps) != 1 {
		t.Errorf("findings filed = %d, want 1", len(opps.opps))
	}
}

func TestWatchCreateOnMissingDirFailsValidation(t *testing.T) {
	store, _ := newTestStore(t, "ws1")
	_, err := store.Create(Trigger{
		WorkspaceID: "ws1",
		Name:        "bad-watch",
		Type:        TypeFileWatch,
		Enabled:     true,
		Action:      Action{Kind: ActionMissionRun},
		FileWatch:   &FileWatchConfig{Path: "/nonexistent-ori-test-dir"},
	})
	// Structural validation passes (absolute path), but the watch manager's
	// Add must reject it.
	if err != nil {
		t.Fatalf("Create should pass structural validation: %v", err)
	}

	rec := newDispatchRecorder(false)
	c := NewCoalescer(store, rec.dispatch)
	t.Cleanup(c.Close)
	m, err := NewWatchManager(store, c, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)

	triggers := store.List("ws1")
	if addErr := m.Add(triggers[0]); addErr == nil {
		t.Error("Add should fail for a missing directory")
	}
	got := store.List("ws1")[0]
	if got.Enabled {
		t.Error("trigger should be disabled after failed Add")
	}
}
