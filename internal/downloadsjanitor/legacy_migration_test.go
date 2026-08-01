package downloadsjanitor

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecapability"
)

// listableWorkspaceStore adds List to the package's fake workspace store so the
// capability backfill can walk it, matching the real store's contract.
type listableWorkspaceStore struct {
	*fakeWorkspaceStore
}

func (s listableWorkspaceStore) List() ([]string, error) {
	ids := make([]string, 0, len(s.workspaces))
	for id := range s.workspaces {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

// stateSnapshot is every byte of a workspace's on-disk Janitor state, keyed by
// path relative to the state directory.
type stateSnapshot map[string][]byte

func snapshotJanitorState(t *testing.T, store *Store, workspaceID string) stateSnapshot {
	t.Helper()
	dir, err := store.StateDir(workspaceID)
	if err != nil {
		t.Fatalf("stateDir: %v", err)
	}
	snapshot := stateSnapshot{}
	err = filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		data, readErr := os.ReadFile(path) // #nosec G304 -- test fixture path under a temp dir
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return relErr
		}
		snapshot[rel] = data
		return nil
	})
	if err != nil {
		t.Fatalf("walk state dir: %v", err)
	}
	if len(snapshot) == 0 {
		t.Fatal("precondition: the fixture wrote no Janitor state")
	}
	return snapshot
}

// legacyFixture builds a workspace that looks exactly like one an earlier Ori
// version left behind: a completed setup, tuned settings, a scanned batch with
// per-candidate decisions, a skipped item, and a journaled action.
func legacyFixture(t *testing.T) (*Service, *Store, listableWorkspaceStore, string) {
	t.Helper()

	store, _ := newTestStore(t)
	workspaces := listableWorkspaceStore{newFakeWorkspaceStore("ws-legacy", "ws-unrelated")}
	service := NewService(store, workspaces)
	service.SetMover(&realMover{})
	service.SetTrash(newFakeTrash(t))

	root := filepath.Join(tempDirCanonical(t), "Downloads")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ConfirmSetup(SetupRequest{
		WorkspaceID:        "ws-legacy",
		Path:               root,
		DailyScanLocalTime: "07:30",
		Timezone:           "America/New_York",
	}); err != nil {
		t.Fatalf("ConfirmSetup: %v", err)
	}

	// Real scan state: a batch with candidates, one decided, one skipped.
	agentFile := "notes.pdf"
	agedFile(t, root, agentFile, 120)
	agedFile(t, root, "photo.png", 90)
	batch, _, err := service.ScanNow("ws-legacy", ScanSourceManual)
	if err != nil {
		t.Fatalf("ScanNow: %v", err)
	}
	if len(batch.CandidateIDs) < 2 {
		t.Fatalf("fixture produced %d candidates, want at least 2", len(batch.CandidateIDs))
	}

	if _, err := service.ApplyDecisions("ws-legacy", []DecisionUpdate{
		{CandidateID: batch.CandidateIDs[0], Decision: DecisionMove},
		{CandidateID: batch.CandidateIDs[1], Decision: DecisionSkip},
	}); err != nil {
		t.Fatalf("UpdateDecisions: %v", err)
	}

	return service, store, workspaces, root
}

// TestLegacyMigration_PreservesEveryStateByte is the FR-128 guarantee in its
// strongest form.
//
// Rather than checking a list of fields, it snapshots every byte of the
// workspace's on-disk Janitor state before the backfill and compares after.
// Selected root, directory reference ID, filing-root name, classification and
// privacy settings, watcher state, the local daily schedule, scan timestamps,
// batches, per-candidate decisions, skipped items, the action journal, and undo
// eligibility all live in those files — so any change to any of them fails
// this, including ones nobody thought to enumerate.
//
// It also demonstrates FR-131: the state stays in its legacy namespace and is
// read in place. No rename happens, so there is nothing to recover from.
func TestLegacyMigration_PreservesEveryStateByte(t *testing.T) {
	service, store, workspaces, root := legacyFixture(t)

	before := snapshotJanitorState(t, store, "ws-legacy")

	registry, err := workspacecapability.NewBuiltinRegistry()
	if err != nil {
		t.Fatalf("NewBuiltinRegistry: %v", err)
	}
	probe := NewCapabilityRuntime(service)
	result := workspacecapability.NewMigrator(registry, workspaces, probe).Run()

	if result.Migrated != 1 {
		t.Fatalf("migrated = %d, want exactly the configured workspace (%+v)", result.Migrated, result)
	}

	after := snapshotJanitorState(t, store, "ws-legacy")
	if len(after) != len(before) {
		t.Fatalf("state files changed: %d before, %d after", len(before), len(after))
	}
	for name, want := range before {
		got, ok := after[name]
		if !ok {
			t.Fatalf("state file %q disappeared during migration", name)
		}
		if string(got) != string(want) {
			t.Fatalf("state file %q was rewritten by migration:\n before %s\n after  %s", name, want, got)
		}
	}

	// The state directory is still the legacy one — no physical rename (FR-131).
	dir, err := store.StateDir("ws-legacy")
	if err != nil {
		t.Fatalf("stateDir: %v", err)
	}
	if filepath.Base(dir) != StateDirName {
		t.Fatalf("state directory moved to %q", dir)
	}

	// And the capability is now visible, reading that same untouched state.
	ws, err := workspaces.Get("ws-legacy")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ws.HasInstalledCapability(workspace.CapabilityFileJanitor) {
		t.Fatal("the configured workspace did not gain an install record")
	}

	status, err := service.Status("ws-legacy")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Settings.RootPath != filepath.Clean(root) {
		t.Fatalf("selected root changed: %q", status.Settings.RootPath)
	}
	if status.Settings.DailyScanLocalTime != "07:30" || status.Settings.Timezone != "America/New_York" {
		t.Fatalf("schedule settings changed: %+v", status.Settings)
	}
	if status.Settings.DirectoryReferenceID == "" {
		t.Fatal("the directory reference was lost")
	}
}

// TestLegacyMigration_GrantsNoNewAccessAndStartsNoAutomation is FR-130 measured
// on the workspace record rather than on intent: after the backfill the
// workspace has exactly the directory references and MCP bindings it had
// before, and the watcher is in exactly the state it was in.
func TestLegacyMigration_GrantsNoNewAccessAndStartsNoAutomation(t *testing.T) {
	service, _, workspaces, _ := legacyFixture(t)

	before, err := workspaces.Get("ws-legacy")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	dirCount := len(before.DirectoryReferences)
	bindingCount := len(before.MCPBindings)
	pausedBefore, err := service.Status("ws-legacy")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}

	registry, err := workspacecapability.NewBuiltinRegistry()
	if err != nil {
		t.Fatalf("NewBuiltinRegistry: %v", err)
	}
	workspacecapability.NewMigrator(registry, workspaces, NewCapabilityRuntime(service)).Run()

	after, err := workspaces.Get("ws-legacy")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(after.DirectoryReferences) != dirCount {
		t.Fatalf("migration changed directory references: %d -> %d", dirCount, len(after.DirectoryReferences))
	}
	if len(after.MCPBindings) != bindingCount {
		t.Fatalf("migration changed MCP bindings: %d -> %d", bindingCount, len(after.MCPBindings))
	}

	pausedAfter, err := service.Status("ws-legacy")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if pausedAfter.Settings.Paused != pausedBefore.Settings.Paused {
		t.Fatal("migration changed the paused state")
	}
	if !pausedAfter.Settings.AutomationApprovedAt.Equal(pausedBefore.Settings.AutomationApprovedAt) {
		t.Fatal("migration changed the automation approval")
	}
}

// TestLegacyMigration_LeavesUnconfiguredWorkspacesAlone pins the negative case
// against real state: a workspace sharing the same store, with no completed
// setup and no Downloads provenance, gains nothing.
func TestLegacyMigration_LeavesUnconfiguredWorkspacesAlone(t *testing.T) {
	service, _, workspaces, _ := legacyFixture(t)

	registry, err := workspacecapability.NewBuiltinRegistry()
	if err != nil {
		t.Fatalf("NewBuiltinRegistry: %v", err)
	}
	workspacecapability.NewMigrator(registry, workspaces, NewCapabilityRuntime(service)).Run()

	unrelated, err := workspaces.Get("ws-unrelated")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if unrelated.HasInstalledCapability(workspace.CapabilityFileJanitor) {
		t.Fatal("an unconfigured workspace sharing the store was migrated")
	}
}

// TestCapabilityRuntime_ProbeRequiresACompletedSetup guards the probe itself:
// a state file that exists but records no approved folder is not evidence the
// capability was in use.
func TestCapabilityRuntime_ProbeRequiresACompletedSetup(t *testing.T) {
	store, _ := newTestStore(t)
	workspaces := listableWorkspaceStore{newFakeWorkspaceStore("ws-1")}
	service := NewService(store, workspaces)
	probe := NewCapabilityRuntime(service)

	// Never set up: no settings file at all.
	if probe.HasConfiguredJanitorState("ws-1") {
		t.Fatal("an unconfigured workspace reported configured state")
	}

	// Settings written, but no folder approved.
	if _, err := store.UpdateSettings("ws-1", func(s *JanitorSettings) error {
		s.DailyScanLocalTime = "09:00"
		return nil
	}); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
	if probe.HasConfiguredJanitorState("ws-1") {
		t.Fatal("settings without an approved root reported configured state")
	}

	// A completed setup does count.
	root := filepath.Join(tempDirCanonical(t), "Inbox")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root}); err != nil {
		t.Fatalf("ConfirmSetup: %v", err)
	}
	if !probe.HasConfiguredJanitorState("ws-1") {
		t.Fatal("a completed setup did not report configured state")
	}
}
