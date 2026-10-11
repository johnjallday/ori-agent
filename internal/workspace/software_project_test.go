package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/testutil/softwarefixture"
)

// testWitness is a stand-in directory witness: two directories are the same
// when the operating system says they are.
func testWitness(t *testing.T, dir string) (string, func(os.FileInfo) (string, error)) {
	t.Helper()
	confirmed, err := os.Lstat(dir)
	if err != nil {
		t.Fatal(err)
	}
	return "confirmed", func(info os.FileInfo) (string, error) {
		if os.SameFile(confirmed, info) {
			return "confirmed", nil
		}
		return "another", nil
	}
}

func softwareWorkspace(t *testing.T, dir, identity string) (*Workspace, string) {
	t.Helper()
	ws := &Workspace{ID: "ws", Name: "Widget", SharedData: map[string]any{}}
	if err := ws.AddDirectoryReference(DirectoryReference{Name: "widget", Path: dir}); err != nil {
		t.Fatal(err)
	}
	id := ws.DirectoryReferences[0].ID
	if err := SetSoftwareProjectBinding(ws.SharedData, SoftwareProjectBinding{
		DirectoryID: id, DirectoryIdentity: identity, Recipe: "software_project", RecipeVersion: 1,
		BoundAt: time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}
	return ws, id
}

func TestSoftwareProjectBinding_RoundTripsThroughStoredJSON(t *testing.T) {
	dir := t.TempDir()
	ws, id := softwareWorkspace(t, dir, "confirmed")
	// A workspace read back from disk holds plain maps, not the typed value.
	encoded, err := json.Marshal(ws.SharedData)
	if err != nil {
		t.Fatal(err)
	}
	var stored map[string]any
	if err := json.Unmarshal(encoded, &stored); err != nil {
		t.Fatal(err)
	}
	binding, err := GetSoftwareProjectBinding(stored)
	if err != nil || binding == nil || binding.DirectoryID != id || binding.DirectoryIdentity != "confirmed" ||
		binding.Recipe != "software_project" || binding.RecipeVersion != 1 {
		t.Fatalf("binding = %+v err = %v", binding, err)
	}
	if none, err := GetSoftwareProjectBinding(map[string]any{}); none != nil || err != nil {
		t.Fatalf("a workspace with no binding = %+v %v", none, err)
	}
	if none, err := GetSoftwareProjectBinding(nil); none != nil || err != nil {
		t.Fatalf("nil shared data = %+v %v", none, err)
	}
}

func TestSoftwareProjectBinding_RefusesMalformedValues(t *testing.T) {
	valid := SoftwareProjectBinding{
		DirectoryID: "dir-1", DirectoryIdentity: "1:2", Recipe: "software_project", RecipeVersion: 1,
		BoundAt: time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC), Remote: "example.invalid/acme/widget.git",
	}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*SoftwareProjectBinding){
		"no directory":          func(b *SoftwareProjectBinding) { b.DirectoryID = "" },
		"a path as directory":   func(b *SoftwareProjectBinding) { b.DirectoryID = "/Users/someone/widget" },
		"no witness":            func(b *SoftwareProjectBinding) { b.DirectoryIdentity = " " },
		"no recipe":             func(b *SoftwareProjectBinding) { b.Recipe = "" },
		"no version":            func(b *SoftwareProjectBinding) { b.RecipeVersion = 0 },
		"no time":               func(b *SoftwareProjectBinding) { b.BoundAt = time.Time{} },
		"remote with a user":    func(b *SoftwareProjectBinding) { b.Remote = "deploy@example.invalid/acme/widget.git" },
		"remote with a query":   func(b *SoftwareProjectBinding) { b.Remote = "example.invalid/acme/widget.git?token=x" },
		"repository key a path": func(b *SoftwareProjectBinding) { b.RepositoryKey = "a/b" },
	} {
		binding := valid
		mutate(&binding)
		if err := binding.Validate(); !errors.Is(err, ErrInvalidSoftwareProject) {
			t.Errorf("%s: %v", name, err)
		}
		if err := SetSoftwareProjectBinding(map[string]any{}, binding); err == nil {
			t.Errorf("%s was stored", name)
		}
	}
	// Unknown fields and trailing shapes are not quietly accepted.
	for name, raw := range map[string]any{
		"unknown field": map[string]any{"directory_id": "d", "directory_identity": "i", "recipe": "r", "recipe_version": 1, "bound_at": "2026-10-11T09:00:00Z", "path": "/elsewhere"},
		"a string":      "software_project",
		"an oversized":  map[string]any{"directory_id": fmt.Sprintf("%05000d", 1)},
	} {
		if binding, err := GetSoftwareProjectBinding(map[string]any{SoftwareProjectKey: raw}); binding != nil || !errors.Is(err, ErrInvalidSoftwareProject) {
			t.Errorf("%s: %+v %v", name, binding, err)
		}
	}
}

func TestSoftwareProjectFolder_ResolvesTheConfirmedFolderOrNothing(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	identity, witness := testWitness(t, dir)

	// No binding: not this function's question.
	if folder, bound, err := SoftwareProjectFolder(&Workspace{ID: "plain", SharedData: map[string]any{}}, witness); folder != "" || bound || err != nil {
		t.Fatalf("unbound = %q %t %v", folder, bound, err)
	}
	if folder, bound, err := SoftwareProjectFolder(nil, witness); folder != "" || bound || err != nil {
		t.Fatalf("nil = %q %t %v", folder, bound, err)
	}

	ws, _ := softwareWorkspace(t, dir, identity)
	if folder, bound, err := SoftwareProjectFolder(ws, witness); folder != dir || !bound || err != nil {
		t.Fatalf("bound = %q %t %v, want the linked folder", folder, bound, err)
	}

	// Every way the binding can stop being true reports unavailable, with no
	// other folder offered in its place.
	unavailable := func(name string, ws *Workspace, witness func(os.FileInfo) (string, error)) {
		t.Helper()
		folder, bound, err := SoftwareProjectFolder(ws, witness)
		if folder != "" || !bound || !errors.Is(err, ErrSoftwareProjectUnavailable) {
			t.Errorf("%s = %q %t %v, want bound and unavailable", name, folder, bound, err)
		}
	}
	unavailable("no way to verify", ws, nil)

	repurposed, _ := softwareWorkspace(t, dir, identity)
	repurposed.DirectoryReferences[0].Purpose = "sample_library"
	unavailable("a capability-owned reference", repurposed, witness)

	unlinked, _ := softwareWorkspace(t, dir, identity)
	unlinked.DirectoryReferences = nil
	unavailable("the reference removed", unlinked, witness)

	// The folder is gone, and then another is made at the same path. The old
	// one is kept until the new one exists, so the two cannot share an identity
	// on a filesystem that reuses a deleted directory's inode number.
	discard, err := softwarefixture.Vacate(dir)
	if err != nil {
		t.Fatal(err)
	}
	unavailable("the folder missing", ws, witness)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	unavailable("a different folder at the path", ws, witness)
	if err := discard(); err != nil {
		t.Fatal(err)
	}

	malformed := &Workspace{ID: "bad", SharedData: map[string]any{SoftwareProjectKey: "not a binding"}}
	if folder, bound, err := SoftwareProjectFolder(malformed, witness); folder != "" || !bound || !errors.Is(err, ErrInvalidSoftwareProject) {
		t.Fatalf("malformed = %q %t %v", folder, bound, err)
	}
}
