package reapersetup

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

var (
	ErrAuthoritativeProjectMissing = errors.New("authoritative REAPER project is missing")
	ErrAuthoritativeProjectUnsafe  = errors.New("authoritative REAPER project is unsafe")
)

// RuntimeWorkspaceSource is the canonical folder-backed workspace view used by
// the REAPER adapter. The project path and runtime grants live in
// workspace.json, so a SQLite-only projection is not sufficient.
type RuntimeWorkspaceSource interface {
	GetFolderWorkspace(string) (*workspace.Workspace, error)
	GetFolderPath(string) (string, error)
}

// AuthoritativeProject resolves the one persisted .rpp entry without accepting
// a caller-supplied path. Every segment is checked with Lstat, symlinks are
// rejected, and the resulting file must stay inside the persisted project
// directory and workspace folder.
func AuthoritativeProject(source RuntimeWorkspaceSource, workspaceID string) (string, error) {
	if source == nil || strings.TrimSpace(workspaceID) == "" {
		return "", ErrAuthoritativeProjectMissing
	}
	ws, err := source.GetFolderWorkspace(strings.TrimSpace(workspaceID))
	if err != nil || ws == nil {
		return "", ErrAuthoritativeProjectMissing
	}
	workspaceRoot, err := source.GetFolderPath(ws.ID)
	if err != nil || strings.TrimSpace(workspaceRoot) == "" {
		return "", ErrAuthoritativeProjectMissing
	}
	entry, err := workspace.GetProjectEntryPath(ws.SharedData)
	if err != nil || !strings.HasSuffix(strings.ToLower(entry), ".rpp") {
		return "", ErrAuthoritativeProjectMissing
	}

	projectRoot, err := inspectRelativePath(workspaceRoot, ws.ProjectPath, true)
	if err != nil {
		return "", err
	}
	target, err := inspectRelativePath(projectRoot, entry, false)
	if err != nil {
		return "", err
	}
	if !pathInside(projectRoot, workspaceRoot) || !pathInside(target, projectRoot) {
		return "", ErrAuthoritativeProjectUnsafe
	}
	return filepath.Clean(target), nil
}

func inspectRelativePath(root, portable string, wantDirectory bool) (string, error) {
	root, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", ErrAuthoritativeProjectUnsafe
	}
	portable = strings.TrimSpace(portable)
	if portable == "" || filepath.IsAbs(portable) || strings.ContainsRune(portable, '\x00') || strings.Contains(portable, `\`) {
		return "", ErrAuthoritativeProjectUnsafe
	}

	current := root
	segments := strings.Split(filepath.ToSlash(portable), "/")
	for index, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return "", ErrAuthoritativeProjectUnsafe
		}
		current = filepath.Join(current, filepath.FromSlash(segment))
		if !pathInsideLexically(current, root) || current == root {
			return "", ErrAuthoritativeProjectUnsafe
		}
		// The path is assembled from validated portable segments and checked
		// against root before this trusted filesystem read.
		info, statErr := os.Lstat(current) // #nosec G304 -- trusted workspace metadata, segment-checked above
		if statErr != nil {
			if os.IsNotExist(statErr) {
				return "", ErrAuthoritativeProjectMissing
			}
			return "", ErrAuthoritativeProjectUnsafe
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", ErrAuthoritativeProjectUnsafe
		}
		last := index == len(segments)-1
		if !last && !info.IsDir() {
			return "", ErrAuthoritativeProjectUnsafe
		}
		if last && wantDirectory && !info.IsDir() {
			return "", ErrAuthoritativeProjectUnsafe
		}
		if last && !wantDirectory && !info.Mode().IsRegular() {
			return "", ErrAuthoritativeProjectUnsafe
		}
	}
	return filepath.Clean(current), nil
}

func pathInsideLexically(path, root string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func pathInside(path, root string) bool {
	canonicalPath, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return false
	}
	canonicalRoot, err := filepath.EvalSymlinks(filepath.Clean(root))
	if err != nil {
		return false
	}
	return pathInsideLexically(canonicalPath, canonicalRoot)
}

func sameProjectPath(current, expected string) bool {
	currentCanonical, currentErr := filepath.EvalSymlinks(filepath.Clean(current))
	expectedCanonical, expectedErr := filepath.EvalSymlinks(filepath.Clean(expected))
	if currentErr != nil || expectedErr != nil {
		return false
	}
	if runtime.GOOS == "darwin" {
		return strings.EqualFold(currentCanonical, expectedCanonical)
	}
	return currentCanonical == expectedCanonical
}
