package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/model"
)

func TestStoreRoundTripsAtomicallyWithPrivatePermissions(t *testing.T) {
	t.Parallel()
	store := New(t.TempDir())
	want := model.NewBridgeState()
	want.Features["repo:feature"] = model.FeatureState{Feature: model.Feature{Name: "feature", Path: "/tmp/feature"}}
	if err := store.Save(want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("state mode = %o, want 0600", got)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Features["repo:feature"].Feature.Name != "feature" {
		t.Fatalf("Load() = %#v", got)
	}
}

func TestStoreRejectsCorruptOrFutureState(t *testing.T) {
	t.Parallel()
	store := New(t.TempDir())
	if err := os.MkdirAll(store.dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.Path(), []byte("not json"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("Load() accepted corrupt JSON")
	}
	if err := os.WriteFile(store.Path(), []byte(`{"version":99,"features":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("Load() accepted a future state version")
	}
	if err := os.WriteFile(store.Path(), []byte(`{"version":0,"features":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("Load() silently accepted an unsupported legacy state version")
	}
}

func TestStoreLockSerializesBridgeOperations(t *testing.T) {
	t.Parallel()
	store := New(t.TempDir())
	unlock, err := store.Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	_, err = store.Lock(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second Lock() error = %v, want deadline while first lock is held", err)
	}
	unlock()

	unlockAgain, err := store.Lock(context.Background())
	if err != nil {
		t.Fatalf("Lock() after release: %v", err)
	}
	unlockAgain()
	info, err := os.Stat(filepath.Join(store.dir, lockFileName))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("lock file = %#v, %v", info, err)
	}
}

// A state file written before tab-backed handoff has no tab_id and records the
// workspace_opened stage. Both must keep loading: the file is shared across
// repositories and is never migrated in place, so a missing TabID is normal
// data, not corruption. Cleanup reads that absence as "workspace-backed".
func TestStoreLoadsPreTabRecordsWithoutTabID(t *testing.T) {
	t.Parallel()
	store := New(t.TempDir())
	if err := os.MkdirAll(store.dir, 0700); err != nil {
		t.Fatal(err)
	}
	legacy := `{"version":1,"features":{"repo:legacy":{` +
		`"feature":{"repository_id":"repo","name":"legacy","branch":"feature/legacy","path":"/tmp/legacy"},` +
		`"workspace_id":"w12","handoff":{"stage":"workspace_opened","root_pane_id":"w12:p1","primary_role":"builder"}}}}`
	if err := os.WriteFile(store.Path(), []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() rejected a pre-tab record: %v", err)
	}
	feature := loaded.Features["repo:legacy"]
	if feature.WorkspaceID != "w12" || feature.TabID != "" {
		t.Fatalf("legacy record = %#v, want workspace w12 and no tab", feature)
	}
	if feature.Handoff.Stage != model.HandoffWorkspaceOpened {
		t.Fatalf("legacy handoff stage = %q, want the retired workspace_opened value preserved", feature.Handoff.Stage)
	}

	// Round-tripping must not invent a tab id for a workspace-backed feature.
	if err := store.Save(loaded); err != nil {
		t.Fatal(err)
	}
	// #nosec G304 -- the path is the test's own temporary state file.
	contents, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "tab_id") {
		t.Fatalf("re-saved legacy record gained a tab id: %s", contents)
	}
}

// TestLoadAcceptsAStateFileWrittenBeforeOvernightRuns is the compatibility
// contract: adding runs must not turn every existing installation's state file
// into a migration or an error.
func TestLoadAcceptsAStateFileWrittenBeforeOvernightRuns(t *testing.T) {
	dir := t.TempDir()
	legacy := `{"version":1,"features":{"repo:alpha":{"feature":{"repository_id":"repo","name":"alpha"},"workspace_id":"w1","updated_at":"2026-07-20T10:00:00Z"}}}`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	store := New(dir)
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("a state file without runs failed to load: %v", err)
	}
	if len(loaded.Features) != 1 {
		t.Fatalf("features = %#v, want the legacy record preserved", loaded.Features)
	}
	if loaded.Runs == nil {
		t.Fatal("runs was left nil, so a caller adding one would panic")
	}
	if len(loaded.Runs) != 0 {
		t.Fatalf("runs = %#v, want none invented", loaded.Runs)
	}
}

// TestRunsRoundTripThroughTheSharedStateFile keeps runs on the same atomic
// write and the same lock as every other bridge record, rather than inventing
// a second store that could disagree with this one.
func TestRunsRoundTripThroughTheSharedStateFile(t *testing.T) {
	store := New(t.TempDir())
	state := model.NewBridgeState()
	state.Runs["run-1"] = model.OvernightRun{
		Version: model.RunVersion, ID: "run-1", RepositoryID: "repo-1",
		State: model.RunScheduled, MaxResumes: 3,
		Participants: []model.RunParticipant{{ID: "p1", Position: 1, State: model.ParticipantQueued}},
	}
	if err := store.Save(state); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	run, ok := loaded.Runs["run-1"]
	if !ok || run.State != model.RunScheduled || len(run.Participants) != 1 {
		t.Fatalf("run = %#v, %v", run, ok)
	}
	if run.Participants[0].State != model.ParticipantQueued {
		t.Fatalf("participant state = %q, want queued", run.Participants[0].State)
	}

	info, err := os.Stat(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("state mode = %v, want 0600: a run names private sessions", info.Mode().Perm())
	}
}

// TestUnknownRunStateLoadsRatherThanBeingDropped keeps a record written by a
// newer helper inspectable. Silently dropping it would lose a run that may own
// a scheduled wake.
func TestUnknownRunStateLoadsRatherThanBeingDropped(t *testing.T) {
	dir := t.TempDir()
	future := `{"version":1,"features":{},"runs":{"run-9":{"version":1,"id":"run-9","state":"some_future_state","participants":[]}}}`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(future), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := New(dir).Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	run, ok := loaded.Runs["run-9"]
	if !ok {
		t.Fatal("a run with an unrecognized state was dropped")
	}
	if run.State.Terminal() {
		t.Fatalf("an unrecognized state = %q was treated as finished", run.State)
	}
}
