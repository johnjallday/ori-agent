package workspacecapability

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Reinstalling creates a FRESH record. It must not resurrect the folder grant,
// the watcher, or the schedule the previous install had: an uninstall the user
// performed to stop Ori touching a folder cannot be undone by an install that
// silently picks the folder back up (FR-29, FR-30).
func TestReinstall_StartsFromNothing(t *testing.T) {
	service, _, store := removalFixture(t,
		workspace.CapabilityResource{Kind: workspace.ResourceDirectoryReference, ID: "ref-1"},
		workspace.CapabilityResource{Kind: workspace.ResourceMCPBinding, ID: "binding-1"})

	original, ok := store.workspaces["ws-1"].GetInstalledCapability(workspace.CapabilityFileJanitor)
	if !ok {
		t.Fatal("fixture has no install record")
	}
	if _, err := service.Remove("ws-1", workspace.CapabilityFileJanitor, RemoveOptions{}); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	result, err := service.Install(InstallRequest{
		WorkspaceID:  "ws-1",
		CapabilityID: workspace.CapabilityFileJanitor,
		Source:       workspace.InstallSourceInPlace,
	})
	if err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	if result.AlreadyInstalled {
		t.Fatal("a reinstall after removal must create a new record, not report the old one")
	}

	fresh := result.Record
	if len(fresh.OwnedResources) != 0 {
		t.Errorf("the new record inherited resources from the old install: %v", fresh.OwnedResources)
	}
	if fresh.InstalledAt.Before(original.InstalledAt) {
		t.Error("the new record should carry its own install time")
	}
	if !fresh.Active() {
		t.Error("the reinstalled record must not still be a tombstone")
	}
}

// Removing and reinstalling repeatedly must not accumulate records: the
// per-workspace install limit is a real constraint, not a first-time-only one.
func TestReinstall_LeavesExactlyOneRecord(t *testing.T) {
	service, _, store := removalFixture(t)

	for range 3 {
		if _, err := service.Remove("ws-1", workspace.CapabilityFileJanitor, RemoveOptions{}); err != nil {
			t.Fatalf("Remove: %v", err)
		}
		if _, err := service.Install(InstallRequest{
			WorkspaceID:  "ws-1",
			CapabilityID: workspace.CapabilityFileJanitor,
			Source:       workspace.InstallSourceInPlace,
		}); err != nil {
			t.Fatalf("Install: %v", err)
		}
	}

	count := 0
	for _, record := range store.workspaces["ws-1"].InstalledCapabilities {
		if workspace.NormalizeCapabilityID(record.ID) == workspace.CapabilityFileJanitor {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("file-janitor records = %d, want exactly 1", count)
	}
}

// alwaysConfigured reports legacy Downloads Janitor state for every workspace,
// which is the strongest migration signal there is.
type alwaysConfigured struct{}

func (alwaysConfigured) HasConfiguredJanitorState(string) bool { return true }

// neverConfigured reports no legacy state, so template provenance is the only
// signal left — which is the case that actually matters here.
type neverConfigured struct{}

func (neverConfigured) HasConfiguredJanitorState(string) bool { return false }

// Template provenance is the signal that SURVIVES an uninstall.
//
// Removal clears the settings and the pending setup requirement, so those two
// migration signals disappear on their own. Which blueprint a workspace was
// created from cannot be undone — so this is the one that would have
// re-installed the capability on every restart, forever, with no way for the
// user to make their removal stick (FR-30).
func TestBackfill_DoesNotReinstallARemovedTemplateWorkspace(t *testing.T) {
	registry, err := NewBuiltinRegistry()
	if err != nil {
		t.Fatalf("NewBuiltinRegistry: %v", err)
	}

	ws := &workspace.Workspace{ID: "ws-1", Name: "Downloads"}
	ws.SetTemplateProvenance(&workspace.TemplateProvenance{TemplateID: LegacyDownloadsTemplateID})
	if !ws.IsFromTemplate(LegacyDownloadsTemplateID) {
		t.Fatal("fixture does not carry the template provenance the test is about")
	}
	if _, err := ws.AddInstalledCapability(workspace.InstalledCapability{
		ID:      workspace.CapabilityFileJanitor,
		Version: 1,
		Source:  workspace.InstallSourceLegacyMigration,
	}); err != nil {
		t.Fatalf("AddInstalledCapability: %v", err)
	}
	if !ws.RemoveInstalledCapability(workspace.CapabilityFileJanitor) {
		t.Fatal("fixture removal did not happen")
	}

	store := newMigrationStore(ws)
	// Two boots: the marker has to keep working, not just survive one restart.
	for range 2 {
		if result := NewMigrator(registry, store, neverConfigured{}).Run(); result.Failed > 0 {
			t.Fatalf("migration reported %d failures", result.Failed)
		}
	}

	current, err := store.Get("ws-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if current.HasInstalledCapability(workspace.CapabilityFileJanitor) {
		t.Fatal("template provenance re-installed a capability the user removed")
	}
}

// The same workspace that was never uninstalled must still migrate on
// provenance alone, so the guard above cannot be hiding a broken migration.
func TestBackfill_MigratesATemplateWorkspaceThatWasNeverRemoved(t *testing.T) {
	registry, err := NewBuiltinRegistry()
	if err != nil {
		t.Fatalf("NewBuiltinRegistry: %v", err)
	}
	ws := &workspace.Workspace{ID: "ws-1", Name: "Downloads"}
	ws.SetTemplateProvenance(&workspace.TemplateProvenance{TemplateID: LegacyDownloadsTemplateID})
	store := newMigrationStore(ws)

	if result := NewMigrator(registry, store, neverConfigured{}).Run(); result.Failed > 0 {
		t.Fatalf("migration reported %d failures", result.Failed)
	}
	current, err := store.Get("ws-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !current.HasInstalledCapability(workspace.CapabilityFileJanitor) {
		t.Fatal("a Downloads Janitor template workspace must still migrate")
	}
}

// Retained legacy state is evidence of what happened, never authorization for
// what happens next.
//
// The workspace below still carries the built-in template it was created from,
// which no uninstall can change. Without the removal marker the startup
// backfill would re-install the capability the user deliberately removed — on
// every restart, with no way to make the removal stick (FR-30, FR-126).
func TestBackfill_DoesNotReinstallWhatTheUserRemoved(t *testing.T) {
	registry, err := NewBuiltinRegistry()
	if err != nil {
		t.Fatalf("NewBuiltinRegistry: %v", err)
	}

	ws := &workspace.Workspace{ID: "ws-1", Name: "Files"}
	if _, err := ws.AddInstalledCapability(workspace.InstalledCapability{
		ID:      workspace.CapabilityFileJanitor,
		Version: 1,
		Source:  workspace.InstallSourceLegacyMigration,
	}); err != nil {
		t.Fatalf("AddInstalledCapability: %v", err)
	}
	if !ws.RemoveInstalledCapability(workspace.CapabilityFileJanitor) {
		t.Fatal("fixture removal did not happen")
	}

	store := newMigrationStore(ws)
	result := NewMigrator(registry, store, alwaysConfigured{}).Run()
	if result.Failed > 0 {
		t.Fatalf("migration reported %d failures", result.Failed)
	}

	current, err := store.Get("ws-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if current.HasInstalledCapability(workspace.CapabilityFileJanitor) {
		t.Fatal("the backfill re-installed a capability the user deliberately removed")
	}
	if !current.CapabilityWasRemoved(workspace.CapabilityFileJanitor) {
		t.Fatal("the removal marker must survive the backfill")
	}
}

// The counterpart: a workspace that was never uninstalled still migrates, so
// the guard above cannot silently disable migration for everyone.
func TestBackfill_StillMigratesAWorkspaceThatWasNeverRemoved(t *testing.T) {
	registry, err := NewBuiltinRegistry()
	if err != nil {
		t.Fatalf("NewBuiltinRegistry: %v", err)
	}
	store := newMigrationStore(&workspace.Workspace{ID: "ws-1", Name: "Files"})

	if result := NewMigrator(registry, store, alwaysConfigured{}).Run(); result.Failed > 0 {
		t.Fatalf("migration reported %d failures", result.Failed)
	}

	current, err := store.Get("ws-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !current.HasInstalledCapability(workspace.CapabilityFileJanitor) {
		t.Fatal("a legacy workspace that was never uninstalled must still migrate")
	}
}

// A removal marker must not outlive an explicit reinstall: the user changing
// their mind is exactly the case it must not block.
func TestRemovalMarker_IsClearedByAnExplicitInstall(t *testing.T) {
	service, _, store := removalFixture(t)

	if _, err := service.Remove("ws-1", workspace.CapabilityFileJanitor, RemoveOptions{}); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !store.workspaces["ws-1"].CapabilityWasRemoved(workspace.CapabilityFileJanitor) {
		t.Fatal("removal should be recorded so the backfill leaves it alone")
	}

	if _, err := service.Install(InstallRequest{
		WorkspaceID:  "ws-1",
		CapabilityID: workspace.CapabilityFileJanitor,
		Source:       workspace.InstallSourceInPlace,
	}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if store.workspaces["ws-1"].CapabilityWasRemoved(workspace.CapabilityFileJanitor) {
		t.Error("an explicit reinstall must clear the removal marker")
	}
}
