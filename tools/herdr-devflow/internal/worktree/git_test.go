package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestInspectLinkedGitWorktreeProvesPathBranchAndRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	repo := filepath.Join(t.TempDir(), "repo")
	runGit(t, "", "init", "-b", "dev", repo)
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "-c", "user.name=Ori Test", "-c", "user.email=ori@example.test", "commit", "-m", "fixture")
	feature := filepath.Join(filepath.Dir(repo), "feature")
	runGit(t, repo, "worktree", "add", "-b", "feature/bridge", feature)

	paths, err := Resolve(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := InspectLinkedGitWorktree(context.Background(), feature, "feature/bridge", paths.GitCommonDir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Branch != "feature/bridge" || got.CommonDir != paths.GitCommonDir {
		t.Fatalf("InspectLinkedGitWorktree() = %#v, want branch/common dir", got)
	}
	if _, err := InspectLinkedGitWorktree(context.Background(), feature, "feature/other", paths.GitCommonDir); err == nil {
		t.Fatal("InspectLinkedGitWorktree accepted the wrong branch")
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	command := exec.Command("git", args...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}
