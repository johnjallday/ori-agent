package workspacerun

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalizeScopeAllowsPathsInsideWorkspaceRoot(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}

	scope, err := CanonicalizeScope(Scope{RepoPath: repo}, []string{root})
	if err != nil {
		t.Fatalf("canonicalize scope: %v", err)
	}
	if scope.RepoPath == "" || !filepath.IsAbs(scope.RepoPath) {
		t.Fatalf("RepoPath = %q, want absolute path", scope.RepoPath)
	}
}

func TestCanonicalizeScopeRejectsPathsOutsideWorkspaceRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	if _, err := CanonicalizeScope(Scope{RepoPath: outside}, []string{root}); err == nil {
		t.Fatal("expected outside path to be rejected")
	}
}

func TestCanonicalizeScopeResolvesSymlinkBeforeBoundaryCheck(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "link-out")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	if _, err := CanonicalizeScope(Scope{RepoPath: link}, []string{root}); err == nil {
		t.Fatal("expected symlink escaping workspace root to be rejected")
	}
}
