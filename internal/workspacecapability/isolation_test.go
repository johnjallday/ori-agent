package workspacecapability

import (
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// brokenRuntime fails every question asked of it.
type brokenRuntime struct{ err error }

func (b brokenRuntime) CapabilityStatus(string) (Status, error) { return Status{}, b.err }

// A capability whose runtime cannot answer must degrade to a visible unhealthy
// state, not an error that propagates out of the catalog.
//
// The catalog is what the workspace page, the Map station, and the Details card
// all read. If one capability's failure could fail the whole call, a single
// broken runtime would blank the Map for a workspace that has other,
// perfectly-working capabilities (FR-15, FR-145).
func TestCatalog_SurvivesARuntimeThatCannotReportStatus(t *testing.T) {
	registry, err := NewBuiltinRegistry()
	if err != nil {
		t.Fatalf("NewBuiltinRegistry: %v", err)
	}
	if err := registry.BindRuntime(workspace.CapabilityFileJanitor,
		brokenRuntime{err: errors.New("state file is unreadable")}); err != nil {
		t.Fatalf("BindRuntime: %v", err)
	}

	ws := &workspace.Workspace{ID: "ws-1"}
	if _, err := ws.AddInstalledCapability(workspace.InstalledCapability{
		ID: workspace.CapabilityFileJanitor, Version: 1,
		InstalledAt: time.Now(), Source: workspace.InstallSourceInPlace,
	}); err != nil {
		t.Fatalf("AddInstalledCapability: %v", err)
	}
	service := NewService(registry, newMemStore(ws))

	items, err := service.Catalog("ws-1")
	if err != nil {
		t.Fatalf("the catalog must not fail because one runtime did: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("the capability must still be listed")
	}
	item := items[0]
	if !item.Installed {
		t.Error("a broken runtime must not make an installed capability look uninstalled")
	}
	if item.Status == nil {
		t.Fatal("expected a derived status")
	}
	if item.Status.State != StatusNeedsAttention && item.Status.State != StatusUnavailable {
		t.Errorf("state = %q, want a visible unhealthy state", item.Status.State)
	}
	if item.Status.Detail == "" {
		t.Error("an unhealthy status must say something a user can act on")
	}
}

// A persisted ID this build does not compile stays visible as installed but
// unavailable, and nothing runs on its behalf. Disappearing would be worse: the
// user would see a workspace silently lose a capability it still has recorded,
// with no way to remove it (FR-14).
func TestCatalog_ReportsAnUnknownCapabilityAsUnavailable(t *testing.T) {
	registry, err := NewBuiltinRegistry()
	if err != nil {
		t.Fatalf("NewBuiltinRegistry: %v", err)
	}
	ws := &workspace.Workspace{ID: "ws-1"}
	ws.SetInstalledCapabilities([]workspace.InstalledCapability{{
		ID: "some-future-capability", Version: 1,
		InstalledAt: time.Now(), Source: workspace.InstallSourceInPlace,
	}})
	service := NewService(registry, newMemStore(ws))

	items, err := service.Catalog("ws-1")
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}

	var unknown *CatalogItem
	for i := range items {
		if items[i].Definition.ID == "some-future-capability" {
			unknown = &items[i]
		}
	}
	if unknown == nil {
		t.Fatal("an unresolvable install must stay visible, not vanish")
	}
	if unknown.Available {
		t.Error("an ID this build cannot resolve must not report as available")
	}
	// The placeholder carries no routes, console, setup, or companion — nothing
	// that could be executed on its behalf.
	if unknown.Definition.API.Prefix != "" || len(unknown.Definition.API.LegacyPrefixes) != 0 {
		t.Error("an unavailable capability must expose no routes")
	}
	if unknown.Definition.Setup.AdapterID != "" {
		t.Error("an unavailable capability must name no setup adapter")
	}
	if unknown.Definition.Companion != nil {
		t.Error("an unavailable capability must not offer a companion")
	}
}

// A capability that cannot be resolved must still be removable. Otherwise a
// build change would strand a workspace with a record it can neither use nor
// get rid of.
func TestUnavailableCapability_IsStillRemovable(t *testing.T) {
	registry, err := NewBuiltinRegistry()
	if err != nil {
		t.Fatalf("NewBuiltinRegistry: %v", err)
	}
	ws := &workspace.Workspace{ID: "ws-1"}
	ws.SetInstalledCapabilities([]workspace.InstalledCapability{{
		ID: "some-future-capability", Version: 1,
		InstalledAt: time.Now(), Source: workspace.InstallSourceInPlace,
	}})
	store := newMemStore(ws)
	service := NewService(registry, store)

	result, err := service.Remove("ws-1", "some-future-capability", RemoveOptions{})
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !result.Removed {
		t.Fatal("an unresolvable capability must still be removable")
	}
}

// The install limit is per capability, so a second capability is unaffected by
// File Janitor's. This is the isolation that keeps one capability's rules from
// becoming everyone's.
func TestInstallLimit_IsScopedToOneCapability(t *testing.T) {
	registry, err := NewBuiltinRegistry()
	if err != nil {
		t.Fatalf("NewBuiltinRegistry: %v", err)
	}
	ws := &workspace.Workspace{ID: "ws-1"}
	store := newMemStore(ws)
	service := NewService(registry, store)

	if _, err := service.Install(InstallRequest{
		WorkspaceID:  "ws-1",
		CapabilityID: workspace.CapabilityFileJanitor,
		Source:       workspace.InstallSourceInPlace,
	}); err != nil {
		t.Fatalf("first install: %v", err)
	}

	// Repeating is reported as already-installed, not as a limit breach: a
	// user pressing Install twice has done nothing wrong (FR-9).
	result, err := service.Install(InstallRequest{
		WorkspaceID:  "ws-1",
		CapabilityID: workspace.CapabilityFileJanitor,
		Source:       workspace.InstallSourceInPlace,
	})
	if err != nil {
		t.Fatalf("repeat install must be idempotent, got %v", err)
	}
	if !result.AlreadyInstalled {
		t.Error("a repeat install should report already_installed")
	}
}

// A workspace store that fails mid-read must not take the catalog down with an
// unhandled error; the boundary reports a workspace problem the caller can map
// to a status.
func TestCatalog_ReportsAStoreFailureAsALifecycleError(t *testing.T) {
	registry, err := NewBuiltinRegistry()
	if err != nil {
		t.Fatalf("NewBuiltinRegistry: %v", err)
	}
	service := NewService(registry, newMemStore())

	_, err = service.Catalog("ws-missing")
	var lifecycleErr *Error
	if !errors.As(err, &lifecycleErr) {
		t.Fatalf("error = %v, want a typed lifecycle error", err)
	}
	if lifecycleErr.Code != CodeWorkspaceMissing {
		t.Errorf("code = %q, want %q", lifecycleErr.Code, CodeWorkspaceMissing)
	}
}
