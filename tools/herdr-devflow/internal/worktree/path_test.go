package worktree

import (
	"os"
	"path/filepath"
	"testing"
)

// canonicalTempDir returns a temp directory already canonicalized, because on
// macOS TempDir hands back /var/... which is a symlink to /private/var.
func canonicalTempDir(t *testing.T) string {
	t.Helper()
	dir, err := canonicalPath(t.TempDir())
	if err != nil {
		t.Fatalf("canonicalize temp dir: %v", err)
	}
	return dir
}

func TestContainsMatchesTheRootAndPathsInsideIt(t *testing.T) {
	root := canonicalTempDir(t)
	inside := filepath.Join(root, "internal", "overview")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name      string
		candidate string
		want      bool
	}{
		{"the root itself", root, true},
		{"a directory inside", inside, true},
		{"the root with a trailing separator", root + string(filepath.Separator), true},
		{"a sibling", filepath.Dir(root), false},
		{"an unrelated absolute path", "/definitely/not/here", false},
		{"empty", "", false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := Contains(root, testCase.candidate); got != testCase.want {
				t.Fatalf("Contains(%q, %q) = %v, want %v", root, testCase.candidate, got, testCase.want)
			}
		})
	}
}

// TestContainsRejectsPrefixCollisions is the case a naive strings.HasPrefix
// gets wrong: two sibling worktrees whose names share a prefix.
func TestContainsRejectsPrefixCollisions(t *testing.T) {
	parent := canonicalTempDir(t)
	shortRoot := filepath.Join(parent, "feature-a")
	longRoot := filepath.Join(parent, "feature-abc")
	for _, dir := range []string{shortRoot, longRoot} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	if Contains(shortRoot, longRoot) {
		t.Fatalf("Contains(%q, %q) matched across a directory boundary", shortRoot, longRoot)
	}
	if Contains(shortRoot, filepath.Join(longRoot, "internal")) {
		t.Fatal("a path inside the longer sibling matched the shorter root")
	}
	// The reverse direction must be equally unforgiving.
	if Contains(longRoot, shortRoot) {
		t.Fatal("the shorter sibling matched the longer root")
	}
}

func TestContainsResolvesSymlinkedPaths(t *testing.T) {
	// A pane may report /var/... while the inventory canonicalized to
	// /private/var/..., or vice versa. Both must resolve to one answer.
	root := canonicalTempDir(t)
	inside := filepath.Join(root, "tasks")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(canonicalTempDir(t), "link-to-root")
	if err := os.Symlink(root, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if !Contains(root, filepath.Join(link, "tasks")) {
		t.Fatal("a path reached through a symlink did not resolve into the root")
	}
	if !Contains(link, inside) {
		t.Fatal("a symlinked root did not contain its real child")
	}
}

func TestContainsIgnoresRelativeAndDirtyInput(t *testing.T) {
	root := canonicalTempDir(t)
	inside := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}

	// A traversal that climbs back out must not match.
	if Contains(root, filepath.Join(inside, "..", "..", "..")) {
		t.Fatal("a path that traversed out of the root matched")
	}
	// A path that traverses but stays inside must still match.
	if !Contains(root, filepath.Join(inside, "..")) {
		t.Fatal("a path that stayed inside the root after traversal did not match")
	}
	// An empty root can never contain anything.
	if Contains("", inside) {
		t.Fatal("an empty root matched")
	}
}

func TestContainsRejectsControlCharacters(t *testing.T) {
	root := canonicalTempDir(t)
	// cwd values arrive from a terminal; a control character means the value
	// is not trustworthy, so it must never resolve.
	if Contains(root, filepath.Join(root, "sub\x00dir")) {
		t.Fatal("a path containing a NUL byte matched")
	}
	if Contains(root+"\x1b[31m", root) {
		t.Fatal("a root containing an escape sequence matched")
	}
}

func TestSameRepositoryComparesCanonicalRoots(t *testing.T) {
	repo := canonicalTempDir(t)
	other := canonicalTempDir(t)

	if !SameRepository(repo, repo) {
		t.Fatal("a repository did not match itself")
	}
	if !SameRepository(repo, repo+string(filepath.Separator)) {
		t.Fatal("a trailing separator defeated the comparison")
	}
	if SameRepository(repo, other) {
		t.Fatal("two different repositories matched")
	}
	// An unknown repository is never a match: a same-named worktree in another
	// clone must not be attributed to this one.
	if SameRepository(repo, "") || SameRepository("", repo) {
		t.Fatal("an empty repository root matched")
	}
}

func TestContainsIsUsableWithPathsThatDoNotExist(t *testing.T) {
	// Herdr may report a cwd for a directory that has since been removed —
	// exactly what happens after a worktree is deleted. Matching must still
	// give a defined answer rather than erroring.
	root := canonicalTempDir(t)
	removed := filepath.Join(root, "already-gone")

	if !Contains(root, removed) {
		t.Fatal("a non-existent path inside the root did not match")
	}
	if Contains(filepath.Join(root, "elsewhere"), removed) {
		t.Fatal("a non-existent path matched an unrelated root")
	}
}
