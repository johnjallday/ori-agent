package workspace

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

type testLinkedPathChecker struct{}

func (testLinkedPathChecker) Allow(*http.Request) error { return nil }
func (testLinkedPathChecker) Check(p string) (TypedFolderPath, error) {
	return TypedFolderPath{Path: canonicalWorkspaceRoot(p)}, nil
}

func TestLinkedMoveAndFolderTrashRequireExactItemsAndDestinations(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("requires exclusive Mac rename")
	}
	f := newLinkedOpenFixture(t, "downloads")
	home := t.TempDir()
	t.Setenv("HOME", home)
	f.h.typedFolderPaths = testLinkedPathChecker{}
	dst := filepath.Join(f.root, "Albums")
	if err := os.Mkdir(dst, 0700); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(f.root, "Bundle.rpp")
	if err := os.WriteFile(filepath.Join(src, "nested.txt"), []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	rows := f.listActions(t, false)
	item, dest := rows["Bundle.rpp"], rows["Albums"]
	move := func(name, token, into, intoToken string) int {
		return f.request(t, f.h.MoveLinkedItem, map[string]string{"path": name, "selection": token, "destination": into, "destination_selection": intoToken}, nil).Code
	}
	if item.TrashSelection == "" || item.MoveSelection == "" || dest.DestinationSelection == "" {
		t.Fatal("missing folder capability")
	}
	if move("Bundle.rpp", item.MoveSelection, "Albums", item.DestinationSelection) != 409 || move("Bundle.rpp", item.TrashSelection, "Albums", dest.DestinationSelection) != 409 || move("Bundle.rpp", item.MoveSelection, "Bundle.rpp", item.DestinationSelection) != 409 {
		t.Fatal("accepted wrong destination, wrong operation or self move")
	}
	if f.action(t, f.h.TrashLinkedFile, "Bundle.rpp", item.TrashSelection, nil).Code != 409 || f.action(t, f.h.TrashLinkedFolder, ".", item.TrashSelection, nil).Code != 409 {
		t.Fatal("folder token admitted as file or root")
	}
	if err := os.Mkdir(filepath.Join(dst, "Bundle.rpp"), 0700); err != nil {
		t.Fatal(err)
	}
	if move("Bundle.rpp", item.MoveSelection, "Albums", dest.DestinationSelection) != 409 {
		t.Fatal("replaced collision")
	}
	if err := os.Remove(filepath.Join(dst, "Bundle.rpp")); err != nil {
		t.Fatal(err)
	}
	if got := move("Bundle.rpp", item.MoveSelection, "Albums", dest.DestinationSelection); got != 200 {
		t.Fatalf("move: %d", got)
	}
	if got := move("Song.rpp", rows["Song.rpp"].MoveSelection, "Albums", dest.DestinationSelection); got != 200 {
		t.Fatalf("second move: %d", got)
	}
	if _, err := os.Lstat(src); !os.IsNotExist(err) {
		t.Fatal("original still present")
	}
	fresh := f.listActions(t, false)["Albums"]
	if got := f.action(t, f.h.TrashLinkedFolder, "Albums", dest.TrashSelection, nil).Code; got != 409 {
		t.Fatal("accepted changed folder")
	}
	if got := f.action(t, f.h.TrashLinkedFolder, "Albums", fresh.TrashSelection, nil).Code; got != 200 {
		t.Fatalf("trash folder %d", got)
	}
	if body, err := os.ReadFile(filepath.Join(home, ".Trash", "Albums", "Bundle.rpp", "nested.txt")); err != nil || string(body) != "untouched" {
		t.Fatal("nested content not preserved", err)
	}
}

func TestLinkedFolderHoldingAnotherReferenceCannotMove(t *testing.T) {
	f := newLinkedOpenFixture(t, "downloads")
	t.Setenv("HOME", t.TempDir())
	f.h.typedFolderPaths = testLinkedPathChecker{}
	path := filepath.Join(f.root, "Bundle.rpp", "project")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Update(f.id, func(ws *Workspace) error {
		return ws.AddDirectoryReference(DirectoryReference{ID: "nested", Name: "Project", Path: path})
	}); err != nil {
		t.Fatal(err)
	}
	a := f.listActions(t, false)["Bundle.rpp"]
	if got := f.action(t, f.h.TrashLinkedFolder, "Bundle.rpp", a.TrashSelection, nil).Code; got != 403 {
		t.Fatalf("moved linked subtree: %d", got)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}
