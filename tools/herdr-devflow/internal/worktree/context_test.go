package worktree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindRepoRootRecognizesLinkedWorktreeGitfile(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, ".git"), []byte("gitdir: /tmp/not-used\n"), 0600); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(repo, "nested", "feature")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal(err)
	}
	got, err := FindRepoRoot(nested)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("FindRepoRoot() = %q, want %q", got, want)
	}
}

func TestResolveUsesUserLocalRuntimeAndRejectsCheckoutState(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	home := filepath.Join(t.TempDir(), "runtime")
	paths, err := Resolve(repo, func(key string) (string, bool) {
		if key == HomeOverrideEnv {
			return home, true
		}
		return "", false
	})
	if err != nil {
		t.Fatal(err)
	}
	canonicalHome, err := canonicalPath(home)
	if err != nil {
		t.Fatal(err)
	}
	if paths.RuntimeRoot != canonicalHome || !strings.HasSuffix(paths.HelperPath, "bin/herdr-devflow") {
		t.Fatalf("unexpected resolved paths: %#v", paths)
	}
	if !strings.Contains(paths.RepositoryID, "-") {
		t.Fatalf("RepositoryID = %q, want stable suffix", paths.RepositoryID)
	}

	_, err = Resolve(repo, func(key string) (string, bool) {
		if key == HomeOverrideEnv {
			return filepath.Join(repo, ".runtime"), true
		}
		return "", false
	})
	if err == nil || !strings.Contains(err.Error(), "outside the Git checkout") {
		t.Fatalf("Resolve() error = %v, want checkout safety error", err)
	}
}
