//go:build darwin || linux

package filejanitor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestFindExistingShelfBeyondBoundedList(t *testing.T) {
	f := newDocsFixture(t, "one.txt")
	names := []string{}
	for i := range workspace.DirectoryListMaxEntries + 3 {
		name := fmt.Sprintf("folder-%03d", i)
		f.mkdir(name)
		names = append(names, name)
	}
	p, err := f.s.ReadShelfPicture(t.Context(), "local", "ws-1", f.batch.ID, "")
	if err != nil || !p.Partial {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, sh := range p.Folders {
		listed[sh.Name] = true
	}
	var parent string
	for _, n := range names {
		if !listed[n] {
			parent = n
			break
		}
	}
	if parent == "" {
		t.Fatal("expected a folder beyond the page")
	}
	f.mkdir(parent + "/Winter")
	f.mkdir("Internal")
	f.write("Internal/workspace.json", "{}")
	if err := os.Symlink(t.TempDir(), f.abs("Alias")); err != nil {
		t.Fatal(err)
	}
	p, err = f.s.ReadShelfPicture(t.Context(), "local", "ws-1", f.batch.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	find := func(name string) (PictureShelf, error) {
		return f.s.FindPictureShelf(t.Context(), "local", "ws-1", f.batch.ID, p.Revision, name)
	}
	for _, bad := range []string{"../outside", "Internal", "Alias", ".hidden", "Filed", "one.txt", parent + "/Winter/Deeper"} {
		if _, err := find(bad); err == nil {
			t.Fatal("accepted", bad)
		}
	}
	sh, err := find(parent + "/Winter")
	if err != nil || sh.Parent == "" {
		t.Fatal(sh, err)
	}
	row := ShelfPlacement{File: p.Files[0].ID, Located: sh.Name, Shelf: sh.Key, Parent: sh.Parent}
	file := func(row ShelfPlacement) (DocumentsFilingResult, error) {
		return f.s.FileShelfPicture(t.Context(), "local", "ws-1", f.batch.ID, p.Revision, keyA, []ShelfPlacement{row})
	}
	wrong := row
	wrong.Located = parent
	if _, err := file(wrong); err == nil {
		t.Fatal("name hint overrode reviewed shelf")
	}
	r, err := file(row)
	if err != nil || r.Filed != 1 || !f.exists(sh.Name+"/one.txt") {
		t.Fatal(r, err)
	}
}

func TestShelfPictureListsOnlyFoldersInReviewedRoot(t *testing.T) {
	f := newDocsFixture(t, "receipt-01.pdf", "tax-receipt.pdf", "loose.png")
	for _, d := range []string{"Receipts/2026", "Records", ".hidden", "NestedWorkspace"} {
		f.mkdir(d)
	}
	f.write("NestedWorkspace/workspace.json", "{}")
	if err := os.Symlink(t.TempDir(), filepath.Join(f.root, "Outside")); err != nil {
		t.Fatal(err)
	}
	settings, err := f.s.store.LoadSettings("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if settings.ApprovedReference == nil || settings.ApprovedReference.Witness == "" {
		t.Fatal("explicit folder grant did not record witness")
	}
	before := f.tree()
	p, err := f.s.ReadShelfPicture(t.Context(), "local", "ws-1", f.batch.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Folders) != 2 || p.Folders[0].Name != "Receipts" || p.Folders[1].Name != "Records" || !p.Supported || p.Partial || len(p.Files) != 3 {
		t.Fatalf("picture: %+v", p)
	}
	for _, file := range p.Files {
		if (file.Name == "receipt-01.pdf") != (file.Suggested == p.Folders[0].Key) {
			t.Fatalf("wrong suggestion: %+v", file)
		}
	}
	child, err := f.s.ReadShelfPicture(t.Context(), "local", "ws-1", f.batch.ID, p.Folders[0].Key)
	if err != nil || len(child.Folders) != 1 || child.Folders[0].Name != "Receipts/2026" || child.Revision != p.Revision {
		t.Fatalf("second level: %+v %v", child, err)
	}
	if _, err = f.s.ReadShelfPicture(t.Context(), "local", "ws-1", f.batch.ID, child.Folders[0].Key); err == nil {
		t.Fatal("allowed a third level")
	}
	data, err := json.Marshal(child)
	if err != nil || strings.Contains(string(data), f.rootID) || strings.Contains(string(data), f.root) {
		t.Fatal("picture disclosed folder identity/path")
	}
	after := f.tree()
	if len(before) != len(after) {
		t.Fatal("picture changed the directory")
	}
	for name, body := range before {
		if after[name] != body {
			t.Fatal("picture changed file")
		}
	}
	if _, err = f.s.ReadShelfPicture(t.Context(), "another", "ws-1", f.batch.ID, ""); err == nil {
		t.Fatal("foreign owner read picture")
	}
	if err = os.Rename(f.root, f.root+"-old"); err != nil {
		t.Fatal(err)
	}
	mkdir(t, f.root)
	if _, err = f.s.ReadShelfPicture(t.Context(), "local", "ws-1", f.batch.ID, ""); err == nil {
		t.Fatal("accepted replacement root")
	}
}
