package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func newLinkedDirectoryWorkspace(t *testing.T) (*Workspace, string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "Linked")
	outside := filepath.Join(base, "Outside")
	for _, dir := range []string{filepath.Join(root, "sub"), outside} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "inside.txt"), []byte("inside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link-out")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "sub"), filepath.Join(root, "link-in")); err != nil {
		t.Fatal(err)
	}
	ws := &Workspace{ID: "ws", Name: "WS"}
	if err := ws.AddDirectoryReference(DirectoryReference{Name: "Linked", Path: root}); err != nil {
		t.Fatal(err)
	}
	return ws, root
}

func TestResolveDirectoryPath_ContainsSymlinksAndRefusesEscapes(t *testing.T) {
	ws, root := newLinkedDirectoryWorkspace(t)
	id := ws.DirectoryReferences[0].ID

	if _, abs, err := ws.ResolveDirectoryPath(id, ""); err != nil || abs != root {
		t.Fatalf("root: %q err=%v", abs, err)
	}
	if _, abs, err := ws.ResolveDirectoryPath(id, "sub/inside.txt"); err != nil || abs != filepath.Join(root, "sub", "inside.txt") {
		t.Fatalf("inside: %q err=%v", abs, err)
	}
	// A link that stays inside is fine.
	if _, _, err := ws.ResolveDirectoryPath(id, "link-in/inside.txt"); err != nil {
		t.Fatalf("inside link err=%v", err)
	}
	for _, scenario := range []struct {
		path string
		want error
	}{
		{"link-out/secret.txt", ErrDirectoryPathOutside},
		{"link-out", ErrDirectoryPathOutside},
	} {
		if _, _, err := ws.ResolveDirectoryPath(id, scenario.path); !errors.Is(err, scenario.want) {
			t.Errorf("%s: err=%v want %v", scenario.path, err, scenario.want)
		}
	}
	for _, path := range []string{"..", "../Outside/secret.txt", "sub/../../Outside", "/etc/passwd"} {
		if _, _, err := ws.ResolveDirectoryPath(id, path); err == nil {
			t.Errorf("%s: accepted", path)
		}
	}
	if _, _, err := ws.ResolveDirectoryPath("missing", "sub"); err == nil {
		t.Error("unknown directory accepted")
	}

	internal := &Workspace{ID: "ws2", Name: "WS2"}
	if err := internal.AddDirectoryReference(DirectoryReference{Name: "Samples", Path: root, Purpose: "sample_library"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := internal.ResolveDirectoryPath(internal.DirectoryReferences[0].ID, ""); !errors.Is(err, ErrDirectoryInternal) {
		t.Errorf("internal purpose err=%v", err)
	}
}

func TestOpenDirectoryFile_RequiresARegularFileInside(t *testing.T) {
	ws, root := newLinkedDirectoryWorkspace(t)
	id := ws.DirectoryReferences[0].ID
	_, path, info, err := ws.OpenDirectoryFile(id, "sub/inside.txt")
	if err != nil || path != filepath.Join(root, "sub", "inside.txt") || info.Size() != 6 {
		t.Fatalf("open: %q %v err=%v", path, info, err)
	}
	if _, _, _, err := ws.OpenDirectoryFile(id, "link-out/secret.txt"); !errors.Is(err, ErrDirectoryPathOutside) {
		t.Errorf("escape err=%v", err)
	}
	if _, _, _, err := ws.OpenDirectoryFile(id, "sub"); !errors.Is(err, ErrDirectoryNotFound) {
		t.Errorf("folder as file err=%v", err)
	}
	if _, _, _, err := ws.OpenDirectoryFile(id, ""); !errors.Is(err, ErrDirectoryNotFound) {
		t.Errorf("empty path err=%v", err)
	}
	if _, _, _, err := ws.OpenDirectoryFile(id, "nope.txt"); !errors.Is(err, ErrDirectoryNotFound) {
		t.Errorf("missing err=%v", err)
	}
	// The Files explorer's read shares the check.
	if _, err := ws.ReadDirectoryFile(id, "link-out/secret.txt"); err == nil {
		t.Error("ReadDirectoryFile followed a link outside the directory")
	}
	if data, err := ws.ReadDirectoryFile(id, "sub/inside.txt"); err != nil || string(data) != "inside" {
		t.Errorf("ReadDirectoryFile: %q err=%v", data, err)
	}
}

func TestListDirectoryEntries_CapsEntriesAndDepth(t *testing.T) {
	ws, root := newLinkedDirectoryWorkspace(t)
	id := ws.DirectoryReferences[0].ID
	// Sorted last, so the cap trips after the smaller entries are listed.
	many := filepath.Join(root, "zz-many")
	if err := os.MkdirAll(many, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := range DirectoryListMaxEntries + 20 {
		if err := os.WriteFile(filepath.Join(many, fmt.Sprintf("f%04d.txt", i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	listing, err := ws.ListDirectoryEntries(id, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !listing.Truncated || len(listing.Entries) != DirectoryListMaxEntries {
		t.Fatalf("entries=%d truncated=%v", len(listing.Entries), listing.Truncated)
	}
	kinds := map[string]string{}
	for _, entry := range listing.Entries {
		kinds[entry.RelativePath] = entry.Kind
	}
	if kinds["link-out"] != "link" || kinds["link-in"] != "link" {
		t.Errorf("links: %q %q", kinds["link-out"], kinds["link-in"])
	}
	if kinds["sub"] != "folder" || kinds["sub/inside.txt"] != "file" {
		t.Errorf("sub: %q %q", kinds["sub"], kinds["sub/inside.txt"])
	}

	shallow, err := ws.ListDirectoryEntries(id, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range shallow.Entries {
		if entry.RelativePath == "sub/inside.txt" {
			t.Error("depth 1 descended into sub")
		}
	}
	if _, err := ws.ListDirectoryEntries(id, "link-out", 0); !errors.Is(err, ErrDirectoryPathOutside) {
		t.Errorf("listing through a link err=%v", err)
	}
	if _, err := ws.ListDirectoryEntries(id, "sub/inside.txt", 0); !errors.Is(err, ErrDirectoryNotFound) {
		t.Errorf("listing a file err=%v", err)
	}
}
