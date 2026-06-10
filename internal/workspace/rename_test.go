package workspace

import (
	"path/filepath"
	"testing"
)

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
