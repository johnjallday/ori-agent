package session

import (
	"context"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func newCapabilityTestWorkspace(id string, now time.Time) *workspace.Workspace {
	return &workspace.Workspace{
		ID:         id,
		Name:       "Inbox",
		Status:     workspace.StatusActive,
		SharedData: map[string]any{},
		CreatedAt:  now,
		UpdatedAt:  now,
	}
}

func fileJanitorInstall(now time.Time) workspace.InstalledCapability {
	return workspace.InstalledCapability{
		ID:          workspace.CapabilityFileJanitor,
		Version:     1,
		InstalledAt: now,
		Source:      workspace.InstallSourceInPlace,
	}
}

// TestWorkspaceInstalledCapabilityPersistence covers the SQLite mirror for
// built-in Workspace Capability installs (PRD FR-4, FR-5): create, read back,
// update, list, and survive a restart. The adapter previously had no field for
// this, so an install that only reached workspace.json would read back as
// "not installed" through every SQLite-primary code path.
func TestWorkspaceInstalledCapabilityPersistence(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{InMemory: true})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	store := NewHybridStoreWithDB(db, 50)
	defer func() { _ = store.Close() }()
	adapter := NewWorkspaceStoreAdapter(store)

	now := time.Now().UTC().Truncate(time.Second)
	wsID := "ws-capability-test"
	ws := newCapabilityTestWorkspace(wsID, now)
	if _, err := ws.AddInstalledCapability(fileJanitorInstall(now)); err != nil {
		t.Fatalf("install: %v", err)
	}

	if err := adapter.Save(ws); err != nil {
		t.Fatalf("save: %v", err)
	}

	// CREATE -> SELECT.
	got, err := adapter.Get(wsID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	installed, ok := got.GetInstalledCapability(workspace.CapabilityFileJanitor)
	if !ok {
		t.Fatalf("after Save/Get: install lost, got %+v", got.GetInstalledCapabilities())
	}
	if installed.Version != 1 || installed.Source != workspace.InstallSourceInPlace {
		t.Fatalf("install record not preserved: %+v", installed)
	}
	if !installed.InstalledAt.Equal(now) {
		t.Fatalf("installed_at not preserved: got %v want %v", installed.InstalledAt, now)
	}

	// UPDATE: a second Save of an existing workspace must persist the change,
	// not just the initial INSERT.
	got.RemoveInstalledCapability(workspace.CapabilityFileJanitor)
	bumped := fileJanitorInstall(now.Add(time.Hour))
	bumped.Version = 2
	bumped.Source = workspace.InstallSourceBlueprint
	if _, err := got.AddInstalledCapability(bumped); err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	if err := adapter.Save(got); err != nil {
		t.Fatalf("update save: %v", err)
	}

	updated, err := adapter.Get(wsID)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	installed, ok = updated.GetInstalledCapability(workspace.CapabilityFileJanitor)
	if !ok {
		t.Fatalf("install lost on update: %+v", updated.GetInstalledCapabilities())
	}
	if installed.Version != 2 || installed.Source != workspace.InstallSourceBlueprint {
		t.Fatalf("update did not persist: %+v", installed)
	}

	// LIST: ListActive goes through the metadata-only ListWorkspaces query. It
	// must still carry installs, because cross-workspace folder-overlap checks
	// (FR-49) scan this listing.
	active, err := adapter.ListActive()
	if err != nil {
		t.Fatalf("list active: %v", err)
	}
	found := false
	for _, item := range active {
		if item.ID != wsID {
			continue
		}
		found = true
		if !item.HasInstalledCapability(workspace.CapabilityFileJanitor) {
			t.Fatalf("ListActive dropped the install: %+v", item.GetInstalledCapabilities())
		}
	}
	if !found {
		t.Fatalf("workspace %s missing from ListActive", wsID)
	}

	// RELOAD: a fresh store over the same DB has an empty cache, so this Get is
	// served from SQLite. Proves the install reached disk.
	restarted := NewWorkspaceStoreAdapter(NewHybridStoreWithDB(db, 50))
	reloaded, err := restarted.Get(wsID)
	if err != nil {
		t.Fatalf("reload get: %v", err)
	}
	if !reloaded.HasInstalledCapability(workspace.CapabilityFileJanitor) {
		t.Fatalf("after restart: install not persisted, got %+v", reloaded.GetInstalledCapabilities())
	}
}

// TestWorkspaceInstalledCapability_AbsentReadsAsNoData proves the backward
// compatibility contract: a workspace that never installed anything must read
// back as nil (no data), never as a phantom install, and never as a non-nil
// empty slice — the merge and preservation guards distinguish those.
func TestWorkspaceInstalledCapability_AbsentReadsAsNoData(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{InMemory: true})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	store := NewHybridStoreWithDB(db, 50)
	defer func() { _ = store.Close() }()
	adapter := NewWorkspaceStoreAdapter(store)

	now := time.Now().UTC().Truncate(time.Second)
	wsID := "ws-no-capability"
	if err := adapter.Save(newCapabilityTestWorkspace(wsID, now)); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := adapter.Get(wsID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if caps := got.GetInstalledCapabilities(); caps != nil {
		t.Fatalf("expected nil collection for a workspace with no installs, got %+v", caps)
	}
	if got.HasInstalledCapability(workspace.CapabilityFileJanitor) {
		t.Fatal("workspace reports File Janitor installed without ever installing it")
	}
}

// TestConvertAgentWorkspace_CarriesInstalledCapabilities covers the folder ->
// SQLite import bridge used by workspace sync and the startup rescan
// (reconcileWorkspacesFromDisk). Importing a workspace folder that has an
// install must not produce a SQLite row that says nothing is installed.
func TestConvertAgentWorkspace_CarriesInstalledCapabilities(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	ws := newCapabilityTestWorkspace("ws-import", now)
	if _, err := ws.AddInstalledCapability(fileJanitorInstall(now)); err != nil {
		t.Fatalf("install: %v", err)
	}

	converted := ConvertAgentWorkspace(ws)
	if converted == nil {
		t.Fatal("ConvertAgentWorkspace returned nil")
	}
	if len(converted.InstalledCapabilitiesJSON) == 0 {
		t.Fatal("installed capabilities dropped on the folder -> SQLite import bridge")
	}

	// And back again, so the bridge is lossless in both directions.
	adapter := &WorkspaceStoreAdapter{}
	roundTripped := adapter.toAgentWorkspace(converted)
	installed, ok := roundTripped.GetInstalledCapability(workspace.CapabilityFileJanitor)
	if !ok {
		t.Fatalf("install lost on the return trip: %+v", roundTripped.GetInstalledCapabilities())
	}
	if installed.Source != workspace.InstallSourceInPlace || installed.Version != 1 {
		t.Fatalf("install record changed through conversion: %+v", installed)
	}
}

// TestToAgentWorkspace_NormalizesPersistedCapabilities proves a malformed or
// duplicated persisted collection cannot reach the rest of the system through
// the SQLite read path, mirroring the same guard on the workspace.json decode.
func TestToAgentWorkspace_NormalizesPersistedCapabilities(t *testing.T) {
	adapter := &WorkspaceStoreAdapter{}
	sessionWS := &Workspace{
		ID:   "ws-garbage",
		Name: "Garbage",
		InstalledCapabilitiesJSON: []byte(`[
			{"id":"File-Janitor","version":1,"installed_at":"2026-07-30T12:00:00Z","source":"in-place"},
			{"id":"file-janitor","version":9,"installed_at":"2026-07-31T12:00:00Z","source":"blueprint"},
			{"id":"","version":1,"installed_at":"2026-07-30T12:00:00Z","source":"in-place"}
		]`),
	}

	got := adapter.toAgentWorkspace(sessionWS)
	caps := got.GetInstalledCapabilities()
	if len(caps) != 1 {
		t.Fatalf("expected one canonicalized record, got %d: %+v", len(caps), caps)
	}
	if caps[0].ID != workspace.CapabilityFileJanitor || caps[0].Version != 1 {
		t.Fatalf("wrong record survived: %+v", caps[0])
	}
}
