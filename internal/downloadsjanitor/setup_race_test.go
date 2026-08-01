package downloadsjanitor

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecapability"
)

// TestConcurrentSetup_OnlyOneWorkspaceClaimsAFolder covers the race the
// ownership check exists for.
//
// The conflict lookup reads other workspaces' settings and then writes this
// one's, so two setups running at the same instant can both observe an
// unclaimed folder. That window is narrow but real — two browser tabs, or a
// blueprint setup racing an in-place one — and the consequence is two File
// Janitors proposing and acting on the same files.
//
// Whatever the interleaving, the folder must end up owned by exactly one
// workspace, and the loser must be left unconfigured rather than half-granted.
func TestConcurrentSetup_OnlyOneWorkspaceClaimsAFolder(t *testing.T) {
	store, _ := newTestStore(t)
	ids := []string{"ws-1", "ws-2", "ws-3", "ws-4"}
	service := NewService(store, listableFakeStore{newFakeWorkspaceStore(ids...)})
	shared := mkdir(t, filepath.Join(tempDirCanonical(t), "Downloads"))

	var wg sync.WaitGroup
	errs := make([]error, len(ids))
	for i, id := range ids {
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			_, errs[i] = service.ConfirmSetup(SetupRequest{WorkspaceID: id, Path: shared})
		}(i, id)
	}
	wg.Wait()

	configured := make([]string, 0, len(ids))
	for _, id := range ids {
		settings, err := service.store.LoadSettings(id)
		if err != nil {
			t.Fatalf("LoadSettings(%s): %v", id, err)
		}
		if settings.IsSetUp() {
			configured = append(configured, id)
			continue
		}
		// A workspace that did not win must hold nothing at all: no root, no
		// directory reference, no half-written grant to clean up later.
		if settings.RootPath != "" || settings.DirectoryReferenceID != "" {
			t.Fatalf("%s lost the race but kept partial state: %+v", id, settings)
		}
	}

	if len(configured) != 1 {
		t.Fatalf("expected exactly one workspace to own the folder, got %v (errors: %v)", configured, errs)
	}

	// Reconciliation agrees: there is nothing left to resolve.
	if result := service.ReconcileOverlappingRoots(ids); result.Paused != 0 {
		t.Fatalf("reconciliation found %d overlapping workspaces after the race", result.Paused)
	}
}

// TestRevoke_ReturnsTheCapabilityToSetupNeeded is FR-59: revoking access is not
// an uninstall. The capability stays installed and its station stays on the
// Map, but it reports Setup needed until a folder is approved again.
func TestRevoke_ReturnsTheCapabilityToSetupNeeded(t *testing.T) {
	store, _ := newTestStore(t)
	workspaces := newFakeWorkspaceStore("ws-1")
	service := NewService(store, workspaces)

	if err := workspaces.Update("ws-1", func(ws *workspace.Workspace) error {
		_, err := ws.AddInstalledCapability(workspace.InstalledCapability{
			ID:          workspace.CapabilityFileJanitor,
			Version:     1,
			InstalledAt: time.Now(),
			Source:      workspace.InstallSourceInPlace,
		})
		return err
	}); err != nil {
		t.Fatalf("install: %v", err)
	}

	root := mkdir(t, filepath.Join(tempDirCanonical(t), "Downloads"))
	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root}); err != nil {
		t.Fatalf("ConfirmSetup: %v", err)
	}

	runtime := NewCapabilityRuntime(service)
	status, err := runtime.CapabilityStatus("ws-1")
	if err != nil {
		t.Fatalf("CapabilityStatus: %v", err)
	}
	if !status.Configured {
		t.Fatalf("precondition: a set-up workspace should report configured, got %+v", status)
	}

	if _, err := service.RevokeAccess(nil, "ws-1"); err != nil {
		t.Fatalf("RevokeAccess: %v", err)
	}

	status, err = runtime.CapabilityStatus("ws-1")
	if err != nil {
		t.Fatalf("CapabilityStatus after revoke: %v", err)
	}
	if status.State != workspacecapability.StatusSetupNeeded {
		t.Fatalf("state = %q, want setup_needed", status.State)
	}
	if status.Configured {
		t.Fatal("a revoked capability must not report itself configured")
	}

	// The capability itself is still installed — revoking access is not
	// uninstalling (FR-59).
	ws, err := workspaces.Get("ws-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ws.HasInstalledCapability(workspace.CapabilityFileJanitor) {
		t.Fatal("revoking folder access uninstalled the capability")
	}
}

// TestRevoke_ThenSetupAgainClaimsTheFolderCleanly closes the loop: a released
// folder is claimable again, by this workspace or another.
func TestRevoke_ThenSetupAgainClaimsTheFolderCleanly(t *testing.T) {
	store, _ := newTestStore(t)
	service := NewService(store, listableFakeStore{newFakeWorkspaceStore("ws-1", "ws-2")})
	root := mkdir(t, filepath.Join(tempDirCanonical(t), "Downloads"))

	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root}); err != nil {
		t.Fatalf("first setup: %v", err)
	}
	if _, err := service.RevokeAccess(nil, "ws-1"); err != nil {
		t.Fatalf("RevokeAccess: %v", err)
	}
	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root}); err != nil {
		t.Fatalf("re-setup after revoke: %v", err)
	}

	settings, err := service.store.LoadSettings("ws-1")
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if !settings.IsSetUp() || settings.RootPath != root {
		t.Fatalf("re-setup did not restore the grant: %+v", settings)
	}
}
