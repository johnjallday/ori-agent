package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var (
	errProjectEntryTargetMissing = errors.New("project entry target is missing")
	ErrProjectEntryUnavailable   = errors.New("project entry is unavailable")
	ErrProjectEntryUnsafe        = errors.New("project entry failed containment validation")
)

// ResolvedProjectEntry is trusted process-local context. AbsolutePath must
// never be persisted to journey state or accepted from a browser/plugin.
type ResolvedProjectEntry struct {
	Locator      ProjectEntryLocator
	AbsolutePath string
}

// ResolveProjectEntry resolves one persisted typed locator against canonical
// workspace ownership and the current filesystem. Every call rechecks the
// exact reference, all relative components, file type, symlinks, and final
// containment; it never searches for a replacement file or directory.
func ResolveProjectEntry(ws *Workspace, workspaceRoot string) (*ResolvedProjectEntry, error) {
	if ws == nil || strings.TrimSpace(ws.ID) == "" || !filepath.IsAbs(workspaceRoot) {
		return nil, ErrProjectEntryUnavailable
	}
	locator, err := GetProjectEntryLocator(ws.SharedData)
	if err != nil {
		return nil, ErrProjectEntryUnsafe
	}
	if locator == nil {
		return nil, ErrProjectEntryUnavailable
	}
	var target string
	switch locator.Kind {
	case ProjectEntryManagedWorkspace:
		if strings.TrimSpace(ws.ProjectPath) == "" {
			return nil, ErrProjectEntryUnavailable
		}
		projectPath, validationErr := validatePersistedProjectPath(ws.ProjectPath)
		if validationErr != nil {
			return nil, ErrProjectEntryUnsafe
		}
		target, err = verifiedProjectEntryTarget(workspaceRoot, projectPath, locator.RelativePath)
		if err != nil && filepath.Base(filepath.Clean(workspaceRoot)) == filepath.Base(filepath.FromSlash(projectPath)) {
			// Some canonical folder stores resolve directly to ProjectPath. Retry
			// only that exact shape, never a search or same-named fallback.
			target, err = verifiedProjectEntryTarget(filepath.Dir(filepath.Clean(workspaceRoot)), projectPath, locator.RelativePath)
		}
	case ProjectEntryDirectoryReference:
		ref, referenceErr := ws.GetDirectoryReference(locator.DirectoryReferenceID)
		if referenceErr != nil || ref == nil || ref.ID != locator.DirectoryReferenceID || ref.WorkspaceID != ws.ID || ref.Purpose == "sample_library" {
			return nil, ErrProjectEntryUnavailable
		}
		target, err = verifiedDirectoryReferenceEntry(ref.Path, locator.RelativePath)
	default:
		return nil, ErrProjectEntryUnsafe
	}
	if err != nil {
		if errors.Is(err, errProjectEntryTargetMissing) {
			return nil, ErrProjectEntryUnavailable
		}
		return nil, ErrProjectEntryUnsafe
	}
	return &ResolvedProjectEntry{Locator: *locator, AbsolutePath: filepath.Clean(target)}, nil
}

func validatePersistedProjectPath(value string) (string, error) {
	return validateResolvedProjectEntryPath(value)
}

func verifiedProjectEntryTarget(workspaceRoot, projectPath, entryPath string) (string, error) {
	root, err := filepath.Abs(filepath.Clean(workspaceRoot))
	if err != nil {
		return "", fmt.Errorf("failed to resolve workspace root: %w", err)
	}
	rootInfo, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return "", errProjectEntryTargetMissing
		}
		return "", fmt.Errorf("failed to inspect workspace root: %w", err)
	}
	if !rootInfo.IsDir() {
		return "", fmt.Errorf("workspace root is not a directory")
	}

	projectRoot, err := inspectProjectRelativePath(root, projectPath, true)
	if err != nil {
		return "", err
	}
	target, err := inspectProjectRelativePath(projectRoot, entryPath, false)
	if err != nil {
		return "", err
	}
	if !pathWithinRootAfterSymlinks(projectRoot, root) ||
		!pathWithinRootAfterSymlinks(target, projectRoot) {
		return "", fmt.Errorf("resolved path escapes the workspace project")
	}
	return target, nil
}

func inspectProjectRelativePath(root, portablePath string, wantDirectory bool) (string, error) {
	current := filepath.Clean(root)
	segments := strings.Split(portablePath, "/")
	for index, segment := range segments {
		current = filepath.Join(current, filepath.FromSlash(segment))
		if !isPathWithin(current, root) || current == root {
			return "", fmt.Errorf("path escapes its allowed root")
		}
		info, err := os.Lstat(current) // #nosec G304 G703 -- each relative component remains under the canonical root
		if err != nil {
			if os.IsNotExist(err) {
				return "", errProjectEntryTargetMissing
			}
			return "", fmt.Errorf("failed to inspect path: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("path contains a symlink")
		}
		last := index == len(segments)-1
		if !last && !info.IsDir() {
			return "", fmt.Errorf("path has a non-directory parent")
		}
		if last && wantDirectory && !info.IsDir() {
			return "", fmt.Errorf("project_path is not a directory")
		}
		if last && !wantDirectory && !info.Mode().IsRegular() {
			return "", fmt.Errorf("project entry is not a regular file")
		}
	}
	return current, nil
}

func verifiedDirectoryReferenceEntry(referenceRoot, relativePath string) (string, error) {
	if !filepath.IsAbs(referenceRoot) {
		return "", ErrProjectEntryUnsafe
	}
	root := filepath.Clean(referenceRoot)
	rootInfo, err := os.Lstat(root) // #nosec G304 -- exact canonical directory reference resolved by ID
	if err != nil {
		return "", errProjectEntryTargetMissing
	}
	if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return "", ErrProjectEntryUnsafe
	}
	target, err := inspectProjectRelativePath(root, relativePath, false)
	if err != nil {
		return "", err
	}
	if !pathWithinRootAfterSymlinks(target, root) {
		return "", ErrProjectEntryUnsafe
	}
	return target, nil
}
