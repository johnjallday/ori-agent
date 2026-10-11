package softwarefixture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/sensitive"
)

func buildSet(t *testing.T) Set {
	t.Helper()
	root := t.TempDir()
	set, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { Restore(set.Unreadable) })
	return set
}

func TestBuild_LaysOutEveryFixture(t *testing.T) {
	set := buildSet(t)
	for name, dir := range map[string]string{
		"GoNode": set.GoNode, "RustPython": set.RustPython, "Worktree": set.Worktree,
		"SiblingWorktree": set.SiblingWorktree, "Clone": set.Clone, "NonGit": set.NonGit,
		"Container": set.Container, "ContainerAPI": set.ContainerAPI, "ContainerSite": set.ContainerSite,
		"SymlinkEscape": set.SymlinkEscape, "Outside": set.Outside, "Unreadable": set.Unreadable, "Hostile": set.Hostile,
	} {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			t.Errorf("%s is not a directory: %v", name, err)
		}
		if !strings.HasPrefix(dir, set.Root+string(filepath.Separator)) {
			t.Errorf("%s = %q is outside the fixture root", name, dir)
		}
	}
}

func TestBuild_WorktreePointsIntoTheMainCheckoutAndBack(t *testing.T) {
	set := buildSet(t)
	marker, err := os.Lstat(filepath.Join(set.Worktree, ".git"))
	if err != nil || !marker.Mode().IsRegular() {
		t.Fatalf("a worktree's .git must be a regular file: %v", err)
	}
	pointer, err := os.ReadFile(filepath.Join(set.Worktree, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	admin := strings.TrimSpace(strings.TrimPrefix(string(pointer), "gitdir:"))
	if !strings.HasPrefix(admin, filepath.Join(set.GoNode, ".git", "worktrees")) {
		t.Fatalf("worktree points at %q, want a directory inside the main checkout", admin)
	}
	back, err := os.ReadFile(filepath.Join(admin, "gitdir"))
	if err != nil || strings.TrimSpace(string(back)) != filepath.Join(set.Worktree, ".git") {
		t.Fatalf("the admin directory does not point back at the worktree: %q %v", back, err)
	}
	if main, err := os.Stat(filepath.Join(set.GoNode, ".git")); err != nil || !main.IsDir() {
		t.Fatalf("an ordinary checkout's .git must be a directory: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(set.NonGit, ".git")); !os.IsNotExist(err) {
		t.Fatalf("the non-Git folder has a .git entry: %v", err)
	}
}

func TestHash_ChangesOnlyWhenTheFolderDoes(t *testing.T) {
	set := buildSet(t)
	for _, dir := range []string{set.GoNode, set.Worktree, set.SymlinkEscape, set.Unreadable} {
		before, err := Hash(dir)
		if err != nil {
			t.Fatal(err)
		}
		again, err := Hash(dir)
		if err != nil || again != before {
			t.Fatalf("hash of %s is not stable: %v", filepath.Base(dir), err)
		}
	}
	before, _ := Hash(set.GoNode)
	if err := os.WriteFile(filepath.Join(set.GoNode, "added.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if after, _ := Hash(set.GoNode); after == before {
		t.Fatal("an added file did not change the hash")
	}
}

func TestReplace_KeepsThePathAndChangesTheDirectory(t *testing.T) {
	set := buildSet(t)
	before, err := os.Stat(set.Clone)
	if err != nil {
		t.Fatal(err)
	}
	if err := Replace(set.Clone); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(set.Clone)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(before, after) {
		t.Fatal("the replaced directory is still the same directory")
	}
}

// The hostile fixture must trip the protections it exists to exercise, and the
// ordinary fixtures must not.
func TestFixtures_CredentialShapedTextIsOnlyWhereItIsMeantToBe(t *testing.T) {
	set := buildSet(t)
	if !sensitive.ContainsSecretLikeText(CredentialShapedValue()) {
		t.Fatal("the credential-shaped value is not recognized as one")
	}
	hostile, err := os.ReadFile(filepath.Join(set.Hostile, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !sensitive.ContainsSecretLikeText(string(hostile)) || !strings.Contains(string(hostile), InjectedInstruction) {
		t.Fatal("the hostile README lost its credential-shaped text or its injected instruction")
	}
	for _, name := range []string{"README.md", "AGENTS.md", "Makefile", "package.json", "go.mod"} {
		data, err := os.ReadFile(filepath.Join(set.GoNode, name))
		if err != nil {
			t.Fatal(err)
		}
		if sensitive.ContainsSecretLikeText(string(data)) {
			t.Errorf("%s in the ordinary checkout looks like a credential", name)
		}
	}
}
