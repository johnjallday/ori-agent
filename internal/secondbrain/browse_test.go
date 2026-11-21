package secondbrain

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestBrowseListsOnlyImmediateAllowedNames(t *testing.T) {
	root := canonicalTemp(t)
	for _, dir := range []string{"Field Notes", ".obsidian", "node_modules"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"note.MD", ".hidden.md", "photo.png", "Field Notes/nested.md"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("private fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	unreadable := filepath.Join(root, "note.MD")
	if err := os.Chmod(unreadable, 0); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "Field Notes"), filepath.Join(root, "shortcut")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(unreadable, filepath.Join(root, "shortcut.md")); err != nil {
		t.Fatal(err)
	}
	got, err := ListDirectory(t.Context(), root, nil)
	want := Directory{Entries: []BrowseEntry{{Name: "Field Notes", Folder: true}, {Name: "note.MD"}}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("list = %+v, %v; want %+v", got, err, want)
	}
	info, err := os.Stat(unreadable)
	if err != nil || info.Mode().Perm() != 0 {
		t.Fatalf("file permissions changed: %v, %v", info, err)
	}
	if err := os.Chmod(unreadable, 0o600); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(unreadable) // #nosec G304 -- this test reads its own fixture.
	if err != nil || string(body) != "private fixture" {
		t.Fatalf("fixture bytes changed: %q, %v", body, err)
	}
	got, err = ListDirectory(t.Context(), root, []string{"Field Notes"})
	if err != nil || !reflect.DeepEqual(got.Entries, []BrowseEntry{{Name: "nested.md"}}) {
		t.Fatalf("nested list = %+v, %v", got, err)
	}
	for _, part := range []string{".", "..", "", "node_modules", ".obsidian", "/tmp", "a/b", `a\b`, "C:work", "a\n", strings.Repeat("a", 256)} {
		if _, err := ListDirectory(t.Context(), root, []string{part}); !errors.Is(err, ErrBrowsePath) {
			t.Errorf("part %q = %v", part, err)
		}
	}
	if _, err := ListDirectory(t.Context(), root, []string{"shortcut"}); !errors.Is(err, ErrOutsideOwnedFolders) {
		t.Errorf("linked folder = %v", err)
	}
	if _, err := ListDirectory(t.Context(), root, []string{"gone"}); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing folder = %v", err)
	}
}

func TestBrowseReportsWorkEntryAndDepthBounds(t *testing.T) {
	root := canonicalTemp(t)
	for _, name := range []string{"one.md", "two.md", "three.md"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, limits := range [][2]int{{2, 200}, {200, 2}} {
		got, err := listDirectory(t.Context(), root, nil, limits[0], limits[1])
		if err != nil || !got.Partial || len(got.Entries) != 2 {
			t.Fatalf("bounded list = %+v, %v", got, err)
		}
	}
	if _, err := ListDirectory(t.Context(), root, make([]string, BrowseMaxDepth+1)); !errors.Is(err, ErrBrowsePath) {
		t.Fatalf("over depth = %v", err)
	}
}
