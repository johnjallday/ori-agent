package cliagent

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DirSnapshot captures the state of files in a directory for diff detection.
type DirSnapshot struct {
	WorkingDir string
	IsGitRepo  bool
	GitRef     string               // HEAD commit at snapshot time (if git)
	FileMTimes map[string]time.Time // relative path -> mtime (non-git fallback)
}

// DiffDetector detects file changes between snapshots.
type DiffDetector struct{}

// NewDiffDetector creates a new DiffDetector.
func NewDiffDetector() *DiffDetector {
	return &DiffDetector{}
}

// Snapshot captures the current file state for the working directory.
func (d *DiffDetector) Snapshot(workingDir string) (*DirSnapshot, error) {
	snap := &DirSnapshot{
		WorkingDir: workingDir,
	}

	// Check if it's a git repo
	if isGitRepo(workingDir) {
		snap.IsGitRepo = true
		ref, err := gitHeadRef(workingDir)
		if err == nil {
			snap.GitRef = ref
		}
		return snap, nil
	}

	// Non-git: snapshot file mtimes
	snap.FileMTimes = make(map[string]time.Time)
	err := filepath.Walk(workingDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip unreadable
		}
		if info.IsDir() {
			// Skip hidden directories
			if strings.HasPrefix(info.Name(), ".") && path != workingDir {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(workingDir, path)
		snap.FileMTimes[rel] = info.ModTime()
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk directory: %w", err)
	}

	return snap, nil
}

// Compare detects files that changed since the snapshot was taken.
func (d *DiffDetector) Compare(before *DirSnapshot, workingDir string) ([]FileChange, error) {
	if before.IsGitRepo {
		return d.compareGit(before, workingDir)
	}
	return d.compareMTime(before, workingDir)
}

// compareGit uses git diff to detect changes.
func (d *DiffDetector) compareGit(before *DirSnapshot, workingDir string) ([]FileChange, error) {
	// Use git diff to find changes (both staged and unstaged, plus untracked)
	var changes []FileChange

	// Tracked file changes
	cmd := exec.Command("git", "diff", "--name-status", "HEAD")
	cmd.Dir = workingDir
	out, err := cmd.Output()
	if err == nil {
		changes = append(changes, parseGitNameStatus(out)...)
	}

	// Untracked files
	cmd = exec.Command("git", "ls-files", "--others", "--exclude-standard")
	cmd.Dir = workingDir
	out, err = cmd.Output()
	if err == nil {
		scanner := bufio.NewScanner(bytes.NewReader(out))
		for scanner.Scan() {
			path := strings.TrimSpace(scanner.Text())
			if path != "" {
				changes = append(changes, FileChange{
					Path:       path,
					ChangeType: ChangeAdded,
				})
			}
		}
	}

	// Try to get numstat for line counts
	cmd = exec.Command("git", "diff", "--numstat", "HEAD")
	cmd.Dir = workingDir
	out, err = cmd.Output()
	if err == nil {
		enrichWithNumstat(changes, out)
	}

	return changes, nil
}

// compareMTime uses file modification times to detect changes.
func (d *DiffDetector) compareMTime(before *DirSnapshot, workingDir string) ([]FileChange, error) {
	var changes []FileChange
	afterFiles := make(map[string]time.Time)

	err := filepath.Walk(workingDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if strings.HasPrefix(info.Name(), ".") && path != workingDir {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(workingDir, path)
		afterFiles[rel] = info.ModTime()
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk directory: %w", err)
	}

	// Check for new and modified files
	for path, afterMTime := range afterFiles {
		beforeMTime, existed := before.FileMTimes[path]
		if !existed {
			changes = append(changes, FileChange{Path: path, ChangeType: ChangeAdded})
		} else if !afterMTime.Equal(beforeMTime) {
			changes = append(changes, FileChange{Path: path, ChangeType: ChangeModified})
		}
	}

	// Check for deleted files
	for path := range before.FileMTimes {
		if _, exists := afterFiles[path]; !exists {
			changes = append(changes, FileChange{Path: path, ChangeType: ChangeDeleted})
		}
	}

	return changes, nil
}

// isGitRepo checks if the directory is inside a git repository.
func isGitRepo(dir string) bool {
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = dir
	out, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// gitHeadRef returns the current HEAD commit hash.
func gitHeadRef(dir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// parseGitNameStatus parses the output of git diff --name-status.
func parseGitNameStatus(data []byte) []FileChange {
	var changes []FileChange
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) < 2 {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		status := parts[0]
		path := parts[len(parts)-1]

		var ct ChangeType
		switch {
		case strings.HasPrefix(status, "A"):
			ct = ChangeAdded
		case strings.HasPrefix(status, "D"):
			ct = ChangeDeleted
		default: // M, R, C, etc.
			ct = ChangeModified
		}

		changes = append(changes, FileChange{Path: path, ChangeType: ct})
	}
	return changes
}

// enrichWithNumstat adds line count data to existing changes from git diff --numstat output.
func enrichWithNumstat(changes []FileChange, data []byte) {
	stats := make(map[string][2]int) // path -> [added, removed]
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		parts := strings.Fields(scanner.Text())
		if len(parts) < 3 {
			continue
		}
		added, _ := strconv.Atoi(parts[0])
		removed, _ := strconv.Atoi(parts[1])
		stats[parts[2]] = [2]int{added, removed}
	}

	for i, c := range changes {
		if s, ok := stats[c.Path]; ok {
			changes[i].LinesAdded = s[0]
			changes[i].LinesRemoved = s[1]
		}
	}
}
