package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestLinkedWorktreesResolveOneRepositoryIdentity characterizes the resolution
// every repository-scoped command already depends on, and which the GitHub
// Issue backlog is about to depend on for a different reason.
//
// A source checkout and each linked worktree are separate directories with
// separate working trees, but they are one repository: they share a common Git
// directory, and therefore one stable repository identity. `./scripts/backlog.sh` must
// select the same GitHub repository from any of them, so the property is
// pinned here before the backlog command starts relying on it.
func TestLinkedWorktreesResolveOneRepositoryIdentity(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	root := t.TempDir()
	source := filepath.Join(root, "repo")
	runGit(t, "", "init", "-b", "dev", source)
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "add", "README.md")
	runGit(t, source, "-c", "user.name=Ori Test", "-c", "user.email=ori@example.test", "commit", "-m", "fixture")
	feature := filepath.Join(root, "feature")
	runGit(t, source, "worktree", "add", "-b", "feature/backlog", feature)

	home := filepath.Join(t.TempDir(), "runtime")
	lookup := func(key string) (string, bool) {
		if key == HomeOverrideEnv {
			return home, true
		}
		return "", false
	}

	sourcePaths, err := Resolve(source, lookup)
	if err != nil {
		t.Fatalf("Resolve(source): %v", err)
	}
	featurePaths, err := Resolve(feature, lookup)
	if err != nil {
		t.Fatalf("Resolve(feature): %v", err)
	}

	if sourcePaths.RepoRoot == featurePaths.RepoRoot {
		t.Fatalf("both checkouts resolved to one root %q; the fixture is not testing two worktrees", sourcePaths.RepoRoot)
	}
	if sourcePaths.GitCommonDir != featurePaths.GitCommonDir {
		t.Fatalf("common dir source=%q feature=%q, want one shared Git directory",
			sourcePaths.GitCommonDir, featurePaths.GitCommonDir)
	}
	if sourcePaths.RepositoryID != featurePaths.RepositoryID {
		t.Fatalf("repository id source=%q feature=%q, want one identity for every linked checkout",
			sourcePaths.RepositoryID, featurePaths.RepositoryID)
	}

	// Sharing an identity must not blur the checkouts themselves: work started
	// inside a linked worktree stays anchored to that worktree, never silently
	// redirected to the source checkout.
	nested := filepath.Join(feature, "tools", "herdr-devflow")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal(err)
	}
	got, err := FindRepoRoot(nested)
	if err != nil {
		t.Fatalf("FindRepoRoot(nested): %v", err)
	}
	if got != featurePaths.RepoRoot {
		t.Fatalf("FindRepoRoot(%q) = %q, want the linked worktree %q", nested, got, featurePaths.RepoRoot)
	}
}
