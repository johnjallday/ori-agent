package secondbrain

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestListProjects_ListsOnlyRealFoldersDirectlyUnderProjectsByName(t *testing.T) {
	tree := newWriterTree(t)
	for _, folder := range []string{"Projects/Launch", "Projects/album 2", "Projects/Zine", "Projects/.hidden", "Projects/node_modules", "Projects/Launch/Deep"} {
		if err := os.MkdirAll(filepath.Join(tree.vault, folder), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	writeNote(t, tree.vault, "Projects/loose-note.md", "not a folder")
	if err := os.Symlink(tree.outside, filepath.Join(tree.vault, "Projects", "Linked")); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, tree.base)

	projects, err := ListProjects(tree.vault)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, project := range projects {
		names = append(names, project.Name)
		if project.Rel != "Projects/"+project.Name || project.Path != filepath.Join(tree.vault, "Projects", project.Name) {
			t.Errorf("project = %+v", project)
		}
	}
	if fmt.Sprint(names) != "[album 2 Launch Zine]" {
		t.Fatalf("names = %v", names)
	}
	if after := snapshot(t, tree.base); after != before {
		t.Fatalf("listing changed the tree.\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestListProjects_ABoundAMissingFolderAndALinkedProjectsFolder(t *testing.T) {
	tree := newWriterTree(t)
	if got, err := ListProjects(filepath.Join(tree.base, "gone")); err == nil {
		t.Fatalf("a missing notes folder listed %v", got)
	}
	if err := os.RemoveAll(filepath.Join(tree.vault, "Projects")); err != nil {
		t.Fatal(err)
	}
	if got, err := ListProjects(tree.vault); err != nil || got != nil {
		t.Fatalf("no Projects = %v, %v", got, err)
	}
	// A Projects that is itself a link to another folder lists nothing.
	if err := os.MkdirAll(filepath.Join(tree.outside, "Elsewhere"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(tree.outside, filepath.Join(tree.vault, "Projects")); err != nil {
		t.Fatal(err)
	}
	if got, err := ListProjects(tree.vault); err != nil || got != nil {
		t.Fatalf("a linked Projects = %v, %v", got, err)
	}
	if err := os.Remove(filepath.Join(tree.vault, "Projects")); err != nil {
		t.Fatal(err)
	}
	for index := range MaxProjects + 20 {
		if err := os.MkdirAll(filepath.Join(tree.vault, "Projects", fmt.Sprintf("p%03d", index)), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	got, err := ListProjects(tree.vault)
	if err != nil || len(got) != MaxProjects || got[0].Name != "p000" {
		t.Fatalf("bounded = %d, %v", len(got), err)
	}
}
