// Package workspacepolicy holds the compiled adapters behind a workspace's
// enforced planning policy.
//
// It exists as its own package because of what it must NOT be mixed with.
// internal/workspacesettings computes what a policy says and stays free of the
// filesystem; internal/workspaceplan runs plans and stays free of opinions
// about repositories. This package is the one place that knows both, and
// keeping it separate is what stops either of those from growing a dependency
// on version control.
package workspacepolicy

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspacesettings"
)

// Inspect reports what a workspace folder can support.
//
// Repository detection reads the filesystem rather than shelling out to git.
// The check runs on every effective-policy read, and a subprocess per read
// would make an ordinary settings page wait on process spawn — for an answer
// that a directory entry already gives.
func Inspect(folderPath string) workspacesettings.WorkspaceCapabilities {
	folderPath = strings.TrimSpace(folderPath)
	if folderPath == "" {
		return workspacesettings.WorkspaceCapabilities{}
	}

	info, err := os.Stat(folderPath)
	if err != nil || !info.IsDir() {
		// A folder that is configured but missing is reported as absent rather
		// than as an error. The policy answer is the same — nothing
		// filesystem-backed can be enforced — and a settings page should not
		// fail to render because a folder moved.
		return workspacesettings.WorkspaceCapabilities{}
	}

	caps := workspacesettings.WorkspaceCapabilities{HasFolder: true}
	gitDir, isRepo := resolveGitDir(folderPath)
	if !isRepo {
		return caps
	}
	caps.IsRepository = true
	caps.CurrentBranch = currentBranch(gitDir)
	return caps
}

// resolveGitDir finds the .git directory for a folder.
//
// A worktree's .git is a FILE containing "gitdir: <path>", not a directory.
// Treating only the directory case as a repository would report every git
// worktree as unversioned — and worktrees are exactly where a branch
// precondition matters most.
func resolveGitDir(folderPath string) (string, bool) {
	gitPath := filepath.Join(folderPath, ".git")
	info, err := os.Stat(gitPath)
	if err != nil {
		return "", false
	}
	if info.IsDir() {
		return gitPath, true
	}

	contents, err := os.ReadFile(gitPath) // #nosec G304 -- path is derived from the workspace folder, not user input
	if err != nil {
		return "", false
	}
	line := strings.TrimSpace(string(contents))
	pointer, found := strings.CutPrefix(line, "gitdir:")
	if !found {
		return "", false
	}
	pointer = strings.TrimSpace(pointer)
	if pointer == "" {
		return "", false
	}
	if !filepath.IsAbs(pointer) {
		pointer = filepath.Join(folderPath, pointer)
	}
	// #nosec G703 -- read-only existence probe, no open and no write. pointer is
	// the gitdir: target the workspace's own .git file declares, which is how
	// git worktrees and submodules legitimately point elsewhere; the result is
	// used only to answer "is this a repository".
	if info, err := os.Stat(pointer); err != nil || !info.IsDir() {
		return "", false
	}
	return pointer, true
}

// currentBranch reads the checked-out branch name, or empty when the repository
// is in a detached HEAD.
//
// Empty is the honest answer for a detached HEAD: there is no branch, and
// inventing one ("HEAD", the commit sha) would make the preflight message name
// something the user cannot switch away from.
func currentBranch(gitDir string) string {
	head, err := os.ReadFile(filepath.Join(gitDir, "HEAD")) // #nosec G304 -- gitDir is resolved from the workspace folder
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(head))
	ref, found := strings.CutPrefix(line, "ref:")
	if !found {
		return ""
	}
	ref = strings.TrimSpace(ref)
	return strings.TrimPrefix(ref, "refs/heads/")
}
