package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestReloadDerivesParentFromPhysicalLocation proves that grouping is
// disk-authoritative: a stale parent_id stored in workspace.json (e.g. carried
// over from another machine, or left behind by a manual folder move) is
// overridden by the workspace's physical location on reload.
func TestReloadDerivesParentFromPhysicalLocation(t *testing.T) {
	dir := t.TempDir()
	st, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	saveWorkspaceUnder(t, st, "group-1", "Group", "")
	saveWorkspaceUnder(t, st, "child", "Child", "group-1") // group/sub-workspaces/child

	// Overwrite the stored parent_id with a bogus value to simulate stale data.
	childCfg := filepath.Join(dir, "group", SubWorkspacesDir, "child", WorkspaceConfigFile)
	raw, err := os.ReadFile(childCfg)
	if err != nil {
		t.Fatalf("read child config: %v", err)
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("unmarshal child config: %v", err)
	}
	obj["parent_id"] = "stale-parent-from-another-machine"
	patched, _ := json.MarshalIndent(obj, "", "  ")
	if err := os.WriteFile(childCfg, patched, 0o644); err != nil {
		t.Fatalf("write child config: %v", err)
	}

	if err := st.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	ws, ok := st.cache["child"]
	if !ok {
		t.Fatalf("child missing from cache after reload")
	}
	if ws.ParentID != "group-1" {
		t.Fatalf("expected parent derived from physical location (group-1), got %q", ws.ParentID)
	}
}

// TestSelfContainedGroupSharePortsViaCopy proves the core portability promise:
// copying a single group folder into a fresh workspaces root reconstructs the
// whole group — its kind, members, and nesting — with no database involved.
func TestSelfContainedGroupSharePortsViaCopy(t *testing.T) {
	srcDir := t.TempDir()
	src, err := NewFileStore(srcDir)
	if err != nil {
		t.Fatalf("NewFileStore src: %v", err)
	}
	group := newTestWorkspace("group-1", "Group")
	group.Kind = "group"
	if err := src.Save(group); err != nil {
		t.Fatalf("save group: %v", err)
	}
	saveWorkspaceUnder(t, src, "child", "Child", "group-1") // group/sub-workspaces/child

	// "Share": copy just the group folder into a fresh, otherwise-empty root.
	destDir := t.TempDir()
	if err := copyDir(filepath.Join(srcDir, "group"), filepath.Join(destDir, "group")); err != nil {
		t.Fatalf("copy group folder: %v", err)
	}

	// A brand-new store over the destination reconstructs the structure from the
	// folder layout alone.
	dst, err := NewFileStore(destDir)
	if err != nil {
		t.Fatalf("NewFileStore dst: %v", err)
	}
	g, ok := dst.cache["group-1"]
	if !ok {
		t.Fatalf("group not reconstructed after copy")
	}
	if g.Kind != "group" {
		t.Fatalf("expected group kind to travel, got %q", g.Kind)
	}
	c, ok := dst.cache["child"]
	if !ok {
		t.Fatalf("member not reconstructed after copy")
	}
	if c.ParentID != "group-1" {
		t.Fatalf("expected member nested under group, got parent %q", c.ParentID)
	}
}
