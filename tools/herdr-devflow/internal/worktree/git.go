package worktree

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// GitWorktree is the canonical provenance required before Herdr can be asked
// to open an existing checkout. Ori remains the creator/remover of this Git
// worktree; the bridge only proves and opens it in Herdr.
type GitWorktree struct {
	Path       string
	Branch     string
	CommonDir  string
	SourcePath string
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
	gitEntry, err := os.Stat(filepath.Join(canonicalTarget, ".git"))
	if err != nil {
		return GitWorktree{}, fmt.Errorf("inspect Git worktree metadata: %w", err)
	}
	if gitEntry.IsDir() {
		return GitWorktree{}, fmt.Errorf("target is the repository source checkout, not a linked Git worktree")
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
	sourcePath, err := sourceCheckoutPath(listed, canonicalTarget)
	if err != nil {
		return GitWorktree{}, err
	}
	return GitWorktree{Path: canonicalTarget, Branch: branch, CommonDir: commonDir, SourcePath: sourcePath}, nil
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	// #nosec G204 -- callers validate the canonical linked worktree and use fixed Git plumbing arguments.
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

// sourceCheckoutPath finds the one normal source checkout in Git's linked
// worktree listing. Herdr 0.7.5 requires that checkout as the parent context
// when it opens an existing linked worktree. A normal checkout owns a .git
// directory; linked worktrees have a .git file that points to common metadata.
func sourceCheckoutPath(output, target string) (string, error) {
	candidates := make([]string, 0, 1)
	seen := make(map[string]struct{})
	var currentPath string
	flush := func() {
		if currentPath == "" {
			return
		}
		canonical, err := canonicalPath(currentPath)
		if err != nil || canonical == target {
			return
		}
		entry, err := os.Stat(filepath.Join(canonical, ".git"))
		if err != nil || !entry.IsDir() {
			return
		}
		if _, ok := seen[canonical]; ok {
			return
		}
		seen[canonical] = struct{}{}
		candidates = append(candidates, canonical)
	}
	for _, line := range strings.Split(output, "\n") {
		if line == "" {
			flush()
			currentPath = ""
			continue
		}
		if strings.HasPrefix(line, "worktree ") {
			currentPath = strings.TrimPrefix(line, "worktree ")
		}
	}
	flush()
	if len(candidates) != 1 {
		return "", fmt.Errorf("could not resolve exactly one repository source checkout for the linked worktree")
	}
	return candidates[0], nil
}
