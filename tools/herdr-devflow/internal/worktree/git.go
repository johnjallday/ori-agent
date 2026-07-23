package worktree

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// GitWorktree is the canonical provenance required before Herdr can be asked
// to open an existing checkout. Ori remains the creator/remover of this Git
// worktree; the bridge only proves and opens it in Herdr.
type GitWorktree struct {
	Path      string
	Branch    string
	CommonDir string
}

// InspectLinkedGitWorktree verifies that path is the root of an existing,
// linked Git worktree and (when supplied) that it has the expected branch and
// repository common directory. It never mutates Git state.
func InspectLinkedGitWorktree(ctx context.Context, path, expectedBranch, expectedCommonDir string) (GitWorktree, error) {
	canonicalTarget, err := canonicalPath(path)
	if err != nil {
		return GitWorktree{}, fmt.Errorf("canonicalize worktree: %w", err)
	}
	topLevel, err := gitOutput(ctx, canonicalTarget, "rev-parse", "--show-toplevel")
	if err != nil {
		return GitWorktree{}, fmt.Errorf("verify Git worktree: %w", err)
	}
	topLevel, err = canonicalPath(topLevel)
	if err != nil {
		return GitWorktree{}, fmt.Errorf("canonicalize Git worktree root: %w", err)
	}
	if topLevel != canonicalTarget {
		return GitWorktree{}, fmt.Errorf("target must be the Git worktree root, not %s", topLevel)
	}
	branch, err := gitOutput(ctx, canonicalTarget, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return GitWorktree{}, fmt.Errorf("resolve Git branch: %w", err)
	}
	if branch == "HEAD" || branch == "" {
		return GitWorktree{}, fmt.Errorf("target worktree is detached")
	}
	if expectedBranch != "" && branch != expectedBranch {
		return GitWorktree{}, fmt.Errorf("target branch %q does not match expected branch %q", branch, expectedBranch)
	}
	commonDir, err := gitOutput(ctx, canonicalTarget, "rev-parse", "--git-common-dir")
	if err != nil {
		return GitWorktree{}, fmt.Errorf("resolve Git common directory: %w", err)
	}
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(canonicalTarget, commonDir)
	}
	commonDir, err = canonicalPath(commonDir)
	if err != nil {
		return GitWorktree{}, fmt.Errorf("canonicalize Git common directory: %w", err)
	}
	if expectedCommonDir != "" {
		expectedCommonDir, err = canonicalPath(expectedCommonDir)
		if err != nil {
			return GitWorktree{}, fmt.Errorf("canonicalize expected Git common directory: %w", err)
		}
		if commonDir != expectedCommonDir {
			return GitWorktree{}, fmt.Errorf("target belongs to a different Git repository")
		}
	}

	listed, err := gitOutput(ctx, canonicalTarget, "worktree", "list", "--porcelain")
	if err != nil {
		return GitWorktree{}, fmt.Errorf("list linked Git worktrees: %w", err)
	}
	if !listedWorktreeContains(listed, canonicalTarget, branch) {
		return GitWorktree{}, fmt.Errorf("target is not present in Git's linked worktree list")
	}
	return GitWorktree{Path: canonicalTarget, Branch: branch, CommonDir: commonDir}, nil
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	output, err := command.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func listedWorktreeContains(output, target, branch string) bool {
	var currentPath string
	var currentBranch string
	flush := func() bool {
		if currentPath == "" {
			return false
		}
		canonical, err := canonicalPath(currentPath)
		if err != nil {
			return false
		}
		return canonical == target && currentBranch == "refs/heads/"+branch
	}
	for _, line := range strings.Split(output, "\n") {
		if line == "" {
			if flush() {
				return true
			}
			currentPath, currentBranch = "", ""
			continue
		}
		switch {
		case strings.HasPrefix(line, "worktree "):
			currentPath = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "branch "):
			currentBranch = strings.TrimPrefix(line, "branch ")
		}
	}
	return flush()
}
