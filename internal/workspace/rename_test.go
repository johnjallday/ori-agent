package workspace

import (
	"path/filepath"
	"testing"
	"time"
)

// TestRenameWithSlug_PreservesInstalledCapabilities guards a non-obvious path:
// RenameWithSlug persists s.cache[id], which is the METADATA-ONLY cache copy
// (metadataCacheCopy detaches Messages/Tasks before cloning). A capability
// install therefore survives a rename only because it stays in that lean copy.
// If someone later trims more fields out of the metadata cache, this test fails
// rather than silently uninstalling File Janitor on rename (PRD FR-144).
//
// Both branches are covered: the display-name-only rename (which writes the
// cache copy directly and has no follow-up portable resync) and the
// slug-changing rename (which moves the folder first).
func TestRenameWithSlug_PreservesInstalledCapabilities(t *testing.T) {
	install := InstalledCapability{
		ID:          CapabilityFileJanitor,
		Version:     1,
		InstalledAt: time.Now(),
		Source:      InstallSourceInPlace,
	}

	tests := []struct {
		name     string
		newName  string
		wantSlug string
	}{
		{"display name only", "inbox", "inbox"},
		{"slug changes", "Sorted Inbox", "sorted-inbox"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			st, err := NewFileStore(dir)
			if err != nil {
				t.Fatalf("NewFileStore: %v", err)
			}

			ws := newTestWorkspace("ws-rename-capability", "Inbox")
			if _, err := ws.AddInstalledCapability(install); err != nil {
				t.Fatalf("AddInstalledCapability: %v", err)
			}
			if err := st.Save(ws); err != nil {
				t.Fatalf("Save: %v", err)
			}

			if _, err := st.RenameWithSlug(ws.ID, tc.newName, ""); err != nil {
				t.Fatalf("RenameWithSlug: %v", err)
			}

			got, err := st.Get(ws.ID)
			if err != nil {
				t.Fatalf("Get after rename: %v", err)
			}
			if got.FolderSlug != tc.wantSlug {
				t.Fatalf("slug = %q, want %q", got.FolderSlug, tc.wantSlug)
			}
			if !got.HasInstalledCapability(CapabilityFileJanitor) {
				t.Fatalf("capability install lost by rename: %+v", got.GetInstalledCapabilities())
			}
		})
	}
}

// TestRebindExistingFolder_PreservesInstalledCapabilitiesFromDisk covers the
// sync-recreate path (sessionhttp's handleWorkspaceSync), which rebinds a folder
// using a workspace built from the SQLite row. When that row predates the
// installed_capabilities_json column it carries no installs, and rebinding must
// take the canonical collection from the folder rather than write an erasure.
func TestRebindExistingFolder_PreservesInstalledCapabilitiesFromDisk(t *testing.T) {
	dir := t.TempDir()
	st, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	ws := newTestWorkspace("ws-rebind-capability", "Inbox")
	if _, err := ws.AddInstalledCapability(InstalledCapability{
		ID:          CapabilityFileJanitor,
		Version:     1,
		InstalledAt: time.Now(),
		Source:      InstallSourceLegacyMigration,
	}); err != nil {
		t.Fatalf("AddInstalledCapability: %v", err)
	}
	if err := st.Save(ws); err != nil {
		t.Fatalf("Save: %v", err)
	}
	folderPath := mustFolderPath(t, st, ws.ID)

	// A record with no capability data, as a pre-migration SQLite row yields.
	stale := newTestWorkspace(ws.ID, "Inbox")
	if err := st.RebindExistingFolder(stale, folderPath); err != nil {
		t.Fatalf("RebindExistingFolder: %v", err)
	}

	got, err := st.Get(ws.ID)
	if err != nil {
		t.Fatalf("Get after rebind: %v", err)
	}
	installed, ok := got.GetInstalledCapability(CapabilityFileJanitor)
	if !ok {
		t.Fatalf("capability install erased by rebind: %+v", got.GetInstalledCapabilities())
	}
	if installed.Source != InstallSourceLegacyMigration {
		t.Fatalf("install provenance not preserved: %+v", installed)
	}
}

// TestRenameWithSlug_RenamesGroupFolderAndRewritesMembers verifies that renaming
// a group folder (which physically contains a nested member) moves the folder on
// disk AND rewrites the member's path mapping, so members are not orphaned. This
// is the safety guarantee that lets the rename handler rename group folders.
func TestRenameWithSlug_RenamesGroupFolderAndRewritesMembers(t *testing.T) {
	dir := t.TempDir()
	st, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	saveWorkspaceUnder(t, st, "group-1", "Group", "")
	saveWorkspaceUnder(t, st, "alpha", "Alpha", "")
	if _, err := st.MoveWorkspaceFolder("alpha", "group-1"); err != nil {
		t.Fatalf("MoveWorkspaceFolder: %v", err)
	}
	// Sanity: alpha is nested under the group's original "group" slug.
	assertWorkspaceAt(t, st, "alpha", filepath.Join(dir, "group", SubWorkspacesDir, "alpha"))

	// Rename the group: its folder slug changes from "group" to "renamed-group".
	moved, err := st.RenameWithSlug("group-1", "Renamed Group", "")
	if err != nil {
		t.Fatalf("RenameWithSlug: %v", err)
	}
	// Both the group and its nested member must be reported so callers can fix
	// path-keyed references.
	movedIDs := make(map[string]bool, len(moved))
	for _, m := range moved {
		movedIDs[m.ID] = true
	}
	if !movedIDs["group-1"] || !movedIDs["alpha"] {
		t.Fatalf("expected moved list to include group-1 and alpha, got %#v", moved)
	}

	// The group folder moved, and the nested member moved with it (path rewritten).
	assertWorkspaceAt(t, st, "group-1", filepath.Join(dir, "renamed-group"))
	assertWorkspaceAt(t, st, "alpha", filepath.Join(dir, "renamed-group", SubWorkspacesDir, "alpha"))

	if ws, ok := st.cache["group-1"]; !ok || ws.Name != "Renamed Group" {
		t.Fatalf("group-1 name = %q, want %q", st.cache["group-1"].Name, "Renamed Group")
	}
}

// TestRenameWithSlug_DisplayNameOnlyKeepsMembers verifies that a rename which
// does not change the slug (e.g. only letter-case/display tweaks resolving to the
// same slug) leaves nested members in place.
func TestRenameWithSlug_DisplayNameOnlyKeepsMembers(t *testing.T) {
	dir := t.TempDir()
	st, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	saveWorkspaceUnder(t, st, "group-1", "Group", "")
	saveWorkspaceUnder(t, st, "alpha", "Alpha", "")
	if _, err := st.MoveWorkspaceFolder("alpha", "group-1"); err != nil {
		t.Fatalf("MoveWorkspaceFolder: %v", err)
	}

	// Keep the same slug ("group") while changing the display name.
	if _, err := st.RenameWithSlug("group-1", "Group", "group"); err != nil {
		t.Fatalf("RenameWithSlug: %v", err)
	}

	assertWorkspaceAt(t, st, "group-1", filepath.Join(dir, "group"))
	assertWorkspaceAt(t, st, "alpha", filepath.Join(dir, "group", SubWorkspacesDir, "alpha"))
}
