package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func saveWorkspaceUnder(t *testing.T, st *FileStore, id, name, parentID string) {
	t.Helper()
	ws := newTestWorkspace(id, name)
	ws.ParentID = parentID
	if err := st.Save(ws); err != nil {
		t.Fatalf("Save %s: %v", id, err)
	}
}

func mustFolderPath(t *testing.T, st *FileStore, id string) string {
	t.Helper()
	p, err := st.GetFolderPath(id)
	if err != nil {
		t.Fatalf("GetFolderPath %s: %v", id, err)
	}
	return p
}

func assertWorkspaceAt(t *testing.T, st *FileStore, id, wantPath string) {
	t.Helper()
	got := mustFolderPath(t, st, id)
	if filepath.Clean(got) != filepath.Clean(wantPath) {
		t.Fatalf("workspace %s: path = %q, want %q", id, got, wantPath)
	}
	if _, err := os.Stat(filepath.Join(got, WorkspaceConfigFile)); err != nil {
		t.Fatalf("workspace %s: expected workspace.json at %q: %v", id, got, err)
	}
}

func TestMoveWorkspaceFolder_IntoAndOutOfGroup(t *testing.T) {
	dir := t.TempDir()
	st, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	saveWorkspaceUnder(t, st, "group-1", "Group", "")
	saveWorkspaceUnder(t, st, "alpha", "Alpha", "")

	// Move Alpha into the group.
	moved, err := st.MoveWorkspaceFolder("alpha", "group-1")
	if err != nil {
		t.Fatalf("MoveWorkspaceFolder into group: %v", err)
	}
	if len(moved) != 1 || moved[0].ID != "alpha" {
		t.Fatalf("expected 1 moved (alpha), got %+v", moved)
	}
	wantInGroup := filepath.Join(dir, "group", SubWorkspacesDir, "alpha")
	assertWorkspaceAt(t, st, "alpha", wantInGroup)
	if ws, ok := st.cache["alpha"]; !ok || ws.ParentID != "group-1" {
		t.Fatalf("alpha ParentID = %q, want group-1", st.cache["alpha"].ParentID)
	}

	// Ungroup: move Alpha back to the root.
	if _, err := st.MoveWorkspaceFolder("alpha", ""); err != nil {
		t.Fatalf("MoveWorkspaceFolder to root: %v", err)
	}
	assertWorkspaceAt(t, st, "alpha", filepath.Join(dir, "alpha"))
	if ws := st.cache["alpha"]; ws.ParentID != "" {
		t.Fatalf("alpha ParentID = %q, want empty after ungroup", ws.ParentID)
	}
}

func TestMoveWorkspaceFolder_RewritesDescendantPaths(t *testing.T) {
	dir := t.TempDir()
	st, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	saveWorkspaceUnder(t, st, "group-1", "Group", "")
	saveWorkspaceUnder(t, st, "alpha", "Alpha", "")
	saveWorkspaceUnder(t, st, "child", "Child", "alpha") // alpha/sub-workspaces/child

	moved, err := st.MoveWorkspaceFolder("alpha", "group-1")
	if err != nil {
		t.Fatalf("MoveWorkspaceFolder: %v", err)
	}
	if len(moved) != 2 {
		t.Fatalf("expected 2 moved (alpha + child), got %d: %+v", len(moved), moved)
	}

	wantAlpha := filepath.Join(dir, "group", SubWorkspacesDir, "alpha")
	wantChild := filepath.Join(wantAlpha, SubWorkspacesDir, "child")
	assertWorkspaceAt(t, st, "alpha", wantAlpha)
	assertWorkspaceAt(t, st, "child", wantChild)

	// The child's ParentID is unchanged (still alpha); only its location moved.
	if ws := st.cache["child"]; ws.ParentID != "alpha" {
		t.Fatalf("child ParentID = %q, want alpha", ws.ParentID)
	}
}

func TestMoveWorkspaceFolder_RejectsCycle(t *testing.T) {
	dir := t.TempDir()
	st, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	saveWorkspaceUnder(t, st, "g1", "G1", "")
	saveWorkspaceUnder(t, st, "g2", "G2", "g1") // g2 nested under g1

	if _, err := st.MoveWorkspaceFolder("g1", "g2"); err == nil {
		t.Fatalf("expected cycle rejection moving g1 under its descendant g2")
	}
}

func TestMoveWorkspaceFolder_RejectsDepthOverflow(t *testing.T) {
	dir := t.TempDir()
	st, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	// Build a chain at the maximum nesting depth: a1 > a2 > ... > a5.
	parent := ""
	for i := 1; i <= MaxNestingDepth; i++ {
		id := "a" + string(rune('0'+i))
		saveWorkspaceUnder(t, st, id, "A"+string(rune('0'+i)), parent)
		parent = id
	}
	deepest := "a" + string(rune('0'+MaxNestingDepth))

	saveWorkspaceUnder(t, st, "b", "B", "")
	if _, err := st.MoveWorkspaceFolder("b", deepest); err == nil {
		t.Fatalf("expected depth rejection moving b under depth-%d node", MaxNestingDepth)
	}
}

func TestMoveWorkspaceFolder_RejectsSlugCollision(t *testing.T) {
	dir := t.TempDir()
	st, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	saveWorkspaceUnder(t, st, "group-1", "Group", "")
	saveWorkspaceUnder(t, st, "dup-inside", "Dup", "group-1") // group/sub-workspaces/dup
	saveWorkspaceUnder(t, st, "dup-root", "Dup", "")          // root/dup

	_, err = st.MoveWorkspaceFolder("dup-root", "group-1")
	if err == nil {
		t.Fatalf("expected slug-collision rejection")
	}
	var conflict *FolderSlugConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected FolderSlugConflictError, got %T: %v", err, err)
	}
}
