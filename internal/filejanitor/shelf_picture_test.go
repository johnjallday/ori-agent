//go:build darwin || linux

package filejanitor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
