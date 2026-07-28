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
