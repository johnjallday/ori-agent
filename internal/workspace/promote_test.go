package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPromoteWorkspaceToGroup_InPlaceAndIdempotent(t *testing.T) {
	dir := t.TempDir()
	st, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	saveWorkspaceUnder(t, st, "drafts", "Three Drafts", "")
	folder := mustFolderPath(t, st, "drafts")
	before, _ := st.Get("drafts")
	before.AgentInstances = []AgentInstance{{ID: "i-1", Name: "Three Drafts Manager", NodeID: "n-1", InstanceNumber: 1}}
	if err := before.SetEntryAgentName("Three Drafts Manager"); err != nil {
		t.Fatal(err)
	}
	if err := st.Save(before); err != nil {
		t.Fatal(err)
	}

	result, err := PromoteWorkspaceToGroup(st, "drafts")
	if err != nil || !result.Promoted {
		t.Fatalf("promote = %+v %v", result, err)
	}
	after, _ := st.Get("drafts")
	if after.Kind != groupWorkspaceKind || after.ID != "drafts" || after.Name != "Three Drafts" || after.EntryAgentName() != "Three Drafts Manager" {
		t.Fatalf("after = kind %q id %q name %q entry %q", after.Kind, after.ID, after.Name, after.EntryAgentName())
	}
	if got := mustFolderPath(t, st, "drafts"); got != folder {
		t.Fatalf("the folder moved: %q → %q", folder, got)
	}
	if info, err := os.Stat(filepath.Join(folder, SubWorkspacesDir)); err != nil || !info.IsDir() {
		t.Fatalf("sub-workspaces/ missing: %v", err)
	}
	// A member can now be moved in.
	saveWorkspaceUnder(t, st, "night", "Night Train", "")
	if _, err := st.MoveWorkspaceFolder("night", "drafts"); err != nil {
		t.Fatal(err)
	}
	assertWorkspaceAt(t, st, "night", filepath.Join(folder, SubWorkspacesDir, "night-train"))

	// A second run of the same plan finds the group and changes nothing.
	again, err := PromoteWorkspaceToGroup(st, "drafts")
	if err != nil || again.Promoted {
		t.Fatalf("second run = %+v %v", again, err)
	}
	// It survives a reload from disk.
	reloaded, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if ws, _ := reloaded.Get("drafts"); ws == nil || ws.Kind != groupWorkspaceKind {
		t.Fatal("the kind did not persist in workspace.json")
	}
}

// failingUpdate simulates a crash between making sub-workspaces/ and the
// kind change.
type failingUpdate struct {
	*FileStore
	fail bool
}

func (f *failingUpdate) Update(id string, fn func(*Workspace) error) error {
	if f.fail {
		return errors.New("crash")
	}
	return f.FileStore.Update(id, fn)
}

func TestPromoteWorkspaceToGroup_ResumesAfterACrash(t *testing.T) {
	st, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	saveWorkspaceUnder(t, st, "songs", "Songs", "")
	crashing := &failingUpdate{FileStore: st, fail: true}
	if _, err := PromoteWorkspaceToGroup(crashing, "songs"); err == nil {
		t.Fatal("the simulated crash must surface")
	}
	if ws, _ := st.Get("songs"); ws.Kind == groupWorkspaceKind {
		t.Fatal("the kind changed despite the crash")
	}
	crashing.fail = false
	result, err := PromoteWorkspaceToGroup(crashing, "songs")
	if err != nil || !result.Promoted {
		t.Fatalf("resume = %+v %v", result, err)
	}
}

func TestPromoteWorkspaceToGroup_Refusals(t *testing.T) {
	st, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	saveWorkspaceUnder(t, st, "busy", "Busy", "")
	busy, _ := st.Get("busy")
	busy.Tasks = []Task{{ID: "t1", Status: TaskStatusInProgress}}
	if err := st.Save(busy); err != nil {
		t.Fatal(err)
	}
	if _, err := PromoteWorkspaceToGroup(st, "busy"); !errors.Is(err, ErrPromoteActiveWork) {
		t.Fatalf("running work = %v", err)
	}

	// A chain at the maximum depth cannot hold members below it.
	parent := ""
	for i := range MaxNestingDepth {
		id := string(rune('a' + i))
		saveWorkspaceUnder(t, st, id, "Level "+id, parent)
		if parent != "" {
			if err := st.Update(parent, func(w *Workspace) error { w.Kind = groupWorkspaceKind; return nil }); err != nil {
				t.Fatal(err)
			}
		}
		parent = id
	}
	if _, err := PromoteWorkspaceToGroup(st, parent); !errors.Is(err, ErrMaxNestingDepthExceeded) {
		t.Fatalf("too deep = %v", err)
	}
	if _, err := PromoteWorkspaceToGroup(st, "missing"); err == nil {
		t.Fatal("a missing workspace must be refused")
	}
}
