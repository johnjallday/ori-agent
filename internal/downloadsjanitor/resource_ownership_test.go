package downloadsjanitor

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// installedService returns a service whose workspace already has the File
// Janitor capability recorded, which is the state setup runs in after Group 1's
// install step.
func installedService(t *testing.T) (*Service, *fakeWorkspaceStore, string) {
	t.Helper()
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
		t.Fatalf("install capability: %v", err)
	}
	return service, workspaces, tempDirCanonical(t)
}

// TestSetup_RecordsCapabilityOwnedResources proves setup writes the ownership
// metadata uninstall will later need (FR-27). Without it, removal would have to
// guess from display names — and a user who renamed a folder link, or another
// feature that happened to pick a similar alias, would make that guess wrong.
func TestSetup_RecordsCapabilityOwnedResources(t *testing.T) {
	service, workspaces, base := installedService(t)
	root := mkdir(t, filepath.Join(base, "Downloads"))

	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root}); err != nil {
		t.Fatalf("ConfirmSetup: %v", err)
	}

	ws, err := workspaces.Get("ws-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	record, ok := ws.GetInstalledCapability(workspace.CapabilityFileJanitor)
	if !ok {
		t.Fatal("the install record disappeared during setup")
	}

	// The directory reference setup created is exclusively ours.
	refs := record.ResourcesOfKind(workspace.ResourceDirectoryReference)
	if len(refs) != 1 {
		t.Fatalf("expected one recorded directory reference, got %+v", refs)
	}
	if refs[0].Shared {
		t.Fatal("a directory reference File Janitor created must be exclusively owned")
	}
	// ...and it is the one the settings point at, by ID rather than by name.
	settings, err := service.store.LoadSettings("ws-1")
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if refs[0].ID != settings.DirectoryReferenceID {
		t.Fatalf("recorded reference %q is not the configured one %q", refs[0].ID, settings.DirectoryReferenceID)
	}

	// The root-scoped MCP binding is likewise ours.
	bindings := record.ResourcesOfKind(workspace.ResourceMCPBinding)
	if len(bindings) != 1 {
		t.Fatalf("expected one recorded MCP binding, got %+v", bindings)
	}
	if bindings[0].Shared {
		t.Fatal("the root-scoped binding must be exclusively owned")
	}
	found := false
	for _, binding := range ws.MCPBindings {
		if binding.ID == bindings[0].ID {
			found = true
			if binding.Alias != JanitorBindingAlias {
				t.Fatalf("recorded binding is not the Janitor's: alias %q", binding.Alias)
			}
		}
	}
	if !found {
		t.Fatalf("recorded binding id %q matches no binding on the workspace", bindings[0].ID)
	}
}

// TestSetup_MarksAPreExistingDirectoryReferenceAsShared is the case that makes
// the distinction matter. The user already linked this folder for their own
// reasons; File Janitor adopts the existing reference rather than creating a
// second one, and must record it as shared so uninstall never deletes it.
func TestSetup_MarksAPreExistingDirectoryReferenceAsShared(t *testing.T) {
	service, workspaces, base := installedService(t)
	root := mkdir(t, filepath.Join(base, "Downloads"))

	// The user linked this folder before File Janitor existed here.
	var preExistingID string
	if err := workspaces.Update("ws-1", func(ws *workspace.Workspace) error {
		if err := ws.AddDirectoryReference(workspace.DirectoryReference{
			Name: "My downloads",
			Path: root,
		}); err != nil {
			return err
		}
		preExistingID = ws.DirectoryReferences[len(ws.DirectoryReferences)-1].ID
		return nil
	}); err != nil {
		t.Fatalf("seed reference: %v", err)
	}

	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root}); err != nil {
		t.Fatalf("ConfirmSetup: %v", err)
	}

	ws, err := workspaces.Get("ws-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	// No duplicate link was created.
	if len(ws.DirectoryReferences) != 1 {
		t.Fatalf("setup duplicated the folder link: %+v", ws.DirectoryReferences)
	}

	record, _ := ws.GetInstalledCapability(workspace.CapabilityFileJanitor)
	exclusive, recorded := record.Owns(workspace.ResourceDirectoryReference, preExistingID)
	if !recorded {
		t.Fatalf("the adopted reference was not recorded: %+v", record.OwnedResources)
	}
	if exclusive {
		t.Fatal("a reference the user already had must be recorded as shared, not exclusively owned")
	}
}

// TestSetup_RepeatedSetupDoesNotDuplicateOwnershipRecords keeps the metadata
// clean across the re-confirmations a real user performs.
func TestSetup_RepeatedSetupDoesNotDuplicateOwnershipRecords(t *testing.T) {
	service, workspaces, base := installedService(t)
	root := mkdir(t, filepath.Join(base, "Downloads"))

	for range 3 {
		if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root}); err != nil {
			t.Fatalf("ConfirmSetup: %v", err)
		}
	}

	ws, _ := workspaces.Get("ws-1")
	record, _ := ws.GetInstalledCapability(workspace.CapabilityFileJanitor)
	if got := len(record.ResourcesOfKind(workspace.ResourceDirectoryReference)); got != 1 {
		t.Fatalf("directory reference recorded %d times", got)
	}
	if got := len(record.ResourcesOfKind(workspace.ResourceMCPBinding)); got != 1 {
		t.Fatalf("MCP binding recorded %d times", got)
	}
}

// TestSetup_WithoutAnInstallRecordStillWorks covers the legacy path: a workspace
// configured before capabilities existed has nothing to attach ownership to,
// and setup must keep working for it rather than failing on a missing record.
func TestSetup_WithoutAnInstallRecordStillWorks(t *testing.T) {
	store, _ := newTestStore(t)
	workspaces := newFakeWorkspaceStore("ws-1")
	service := NewService(store, workspaces)
	root := mkdir(t, filepath.Join(tempDirCanonical(t), "Downloads"))

	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root}); err != nil {
		t.Fatalf("setup must work without an install record: %v", err)
	}

	ws, _ := workspaces.Get("ws-1")
	if len(ws.GetInstalledCapabilities()) != 0 {
		t.Fatal("setup invented an install record")
	}
	// The grant itself still happened.
	settings, err := service.store.LoadSettings("ws-1")
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if !settings.IsSetUp() {
		t.Fatal("setup did not complete")
	}
}
