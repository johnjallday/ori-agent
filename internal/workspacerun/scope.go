package workspacerun

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func CanonicalizeScope(scope Scope, workspaceRoots []string) (Scope, error) {
	roots, err := canonicalizeRoots(workspaceRoots)
	if err != nil {
		return Scope{}, err
	}
	if len(roots) == 0 && scopeHasFilesystemPaths(scope) {
		return Scope{}, fmt.Errorf("workspace root is required for filesystem scope")
	}

	out := scope
	if out.RepoPath != "" {
		out.RepoPath, err = canonicalizeScopedPath(out.RepoPath, roots)
		if err != nil {
			return Scope{}, fmt.Errorf("repo_path: %w", err)
		}
	}
	if out.Folder != "" {
		out.Folder, err = canonicalizeScopedPath(out.Folder, roots)
		if err != nil {
			return Scope{}, fmt.Errorf("folder: %w", err)
		}
	}
	if len(out.FilesystemRoots) > 0 {
		out.FilesystemRoots = make([]string, len(scope.FilesystemRoots))
		for i, root := range scope.FilesystemRoots {
			out.FilesystemRoots[i], err = canonicalizeScopedPath(root, roots)
			if err != nil {
				return Scope{}, fmt.Errorf("filesystem_roots[%d]: %w", i, err)
			}
		}
	}
	out.NetworkAllowlist = cloneStrings(scope.NetworkAllowlist)
	return out, nil
}

func canonicalizeRoots(paths []string) ([]string, error) {
	out := make([]string, 0, len(paths))
	seen := map[string]bool{}
	for _, path := range paths {
		trimmed := strings.TrimSpace(path)
		if trimmed == "" {
			continue
		}
		canon, err := canonicalExistingPath(trimmed)
		if err != nil {
			return nil, fmt.Errorf("workspace root %q: %w", path, err)
		}
		if !seen[canon] {
			seen[canon] = true
			out = append(out, canon)
		}
	}
	return out, nil
}

func canonicalizeScopedPath(path string, roots []string) (string, error) {
	canon, err := canonicalExistingPath(path)
	if err != nil {
		return "", err
	}
	if !pathWithinAnyRoot(canon, roots) {
		return "", fmt.Errorf("%q is outside allowed workspace roots", canon)
	}
	return canon, nil
}

func canonicalExistingPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(abs); err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}

func pathWithinAnyRoot(path string, roots []string) bool {
	for _, root := range roots {
		if pathWithinRoot(path, root) {
			return true
		}
	}
	return false
}

func pathWithinRoot(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." && !filepath.IsAbs(rel))
}

func scopeHasFilesystemPaths(scope Scope) bool {
	return strings.TrimSpace(scope.RepoPath) != "" ||
		strings.TrimSpace(scope.Folder) != "" ||
		len(scope.FilesystemRoots) > 0
}
