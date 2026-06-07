package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Move validation errors. Callers (e.g. HTTP handlers) can map these onto
// appropriate status codes with errors.Is.
var (
	// ErrSelfParent is returned when a workspace is moved under itself.
	ErrSelfParent = errors.New("workspace cannot be its own parent")
	// ErrMoveCreatesCycle is returned when a workspace is moved under one of
	// its own descendants.
	ErrMoveCreatesCycle = errors.New("workspace cannot be moved under its own descendant")
	// ErrMaxNestingDepthExceeded is returned when a move would nest a workspace
	// deeper than MaxNestingDepth.
	ErrMaxNestingDepthExceeded = errors.New("maximum nesting depth exceeded")
)

// MovedWorkspace describes a workspace whose folder changed location as the
// result of a move. The moved node is the first entry; its descendants follow.
// Paths are absolute so callers can update path-keyed references.
type MovedWorkspace struct {
	ID      string
	OldPath string
	NewPath string
}

// MoveWorkspaceFolder relocates a workspace folder (and its entire subtree) so
// that it becomes a child of newParentID. An empty newParentID moves the
// workspace back to the top-level workspaces root (ungroup).
//
// Physical folder location is the source of truth for grouping, so this updates
// the on-disk layout, the in-memory cache, the id→path mapping, and the index
// for the moved node and every descendant. It returns the old/new absolute
// paths of every affected workspace so callers can fix up path-keyed references
// (directory references, MCP roots, project_path) that live outside the folder.
//
// The move is validated (cycle, nesting depth, slug uniqueness) before any
// filesystem mutation, and uses an atomic rename with a cross-device
// copy-then-delete fallback so a workspace is never left partially moved.
func (s *FileStore) MoveWorkspaceFolder(id, newParentID string) ([]MovedWorkspace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	oldRelPath, ok := s.idToPath[id]
	if !ok {
		return nil, fmt.Errorf("workspace %s not found", id)
	}

	// Validate the destination parent.
	if newParentID != "" {
		if newParentID == id {
			return nil, ErrSelfParent
		}
		if _, ok := s.idToPath[newParentID]; !ok {
			return nil, fmt.Errorf("parent workspace %s not found", newParentID)
		}
	}

	// Cycle check: the destination must not be the workspace itself or any of
	// its descendants.
	descendants := s.collectDescendantsLocked(id)
	if newParentID != "" {
		if _, isDescendant := descendants[newParentID]; isDescendant {
			return nil, ErrMoveCreatesCycle
		}
	}

	// Depth check: the deepest node in the moved subtree must not exceed the
	// maximum nesting depth at its new location.
	newRootDepth := s.getNestingDepth(newParentID) + 1
	if deepest := newRootDepth + s.subtreeHeightLocked(id); deepest > MaxNestingDepth {
		return nil, fmt.Errorf("%w: limit is %d", ErrMaxNestingDepthExceeded, MaxNestingDepth)
	}

	oldFolderPath := s.resolveFolder(oldRelPath)
	slug := filepath.Base(oldFolderPath)

	// Resolve the destination parent directory.
	var destParentDir string
	if newParentID == "" {
		destParentDir = s.basePath
	} else {
		destParentDir = filepath.Join(s.resolveFolder(s.idToPath[newParentID]), SubWorkspacesDir)
	}
	destFolderPath := filepath.Join(destParentDir, slug)

	// No-op if the workspace is already in the destination.
	if filepath.Clean(destFolderPath) == filepath.Clean(oldFolderPath) {
		return nil, nil
	}

	// Slug uniqueness: never overwrite an existing folder in the destination.
	if existsOnDisk, err := pathExists(destFolderPath); err != nil {
		return nil, fmt.Errorf("failed to check destination path: %w", err)
	} else if existsOnDisk {
		return nil, &FolderSlugConflictError{
			Slug:          slug,
			SuggestedSlug: nextAvailableWorkspaceSlug(destParentDir, slug),
			ParentDir:     destParentDir,
		}
	}

	// Ensure the destination parent directory (e.g. an empty group's
	// sub-workspaces/) exists before the move.
	if err := os.MkdirAll(destParentDir, 0o750); err != nil {
		return nil, fmt.Errorf("failed to create destination directory: %w", err)
	}

	// Move the folder tree on disk (atomic rename, cross-device fallback).
	if err := moveDir(oldFolderPath, destFolderPath); err != nil {
		return nil, fmt.Errorf("failed to move workspace folder: %w", err)
	}

	// Compute the moved node's new relative path (preserve absolute if the
	// workspace was tracked by an absolute path).
	newRelPath := destFolderPath
	if !filepath.IsAbs(oldRelPath) {
		if rel, err := filepath.Rel(s.basePath, destFolderPath); err == nil {
			newRelPath = rel
		} else {
			newRelPath = filepath.Join(filepath.Base(destParentDir), slug)
		}
	}

	moved := make([]MovedWorkspace, 0, len(descendants)+1)

	// Update the moved node: path mapping, ParentID, persisted workspace.json,
	// and index entry.
	s.idToPath[id] = newRelPath
	ws, ok := s.cache[id]
	if ok {
		ws.ParentID = newParentID
		ws.UpdatedAt = time.Now()
		if err := s.persistWorkspaceLocked(ws); err != nil {
			return nil, fmt.Errorf("failed to persist moved workspace: %w", err)
		}
		s.registerIndexLocked(ws, newRelPath)
	}
	moved = append(moved, MovedWorkspace{ID: id, OldPath: oldFolderPath, NewPath: s.resolveFolder(newRelPath)})

	// Update every descendant: their position within the subtree is unchanged,
	// so rewrite their path prefix from the old root to the new root.
	for descID := range descendants {
		oldDescRel, ok := s.idToPath[descID]
		if !ok {
			continue
		}
		oldDescAbs := s.resolveFolder(oldDescRel)
		rel, err := filepath.Rel(oldRelPath, oldDescRel)
		if err != nil {
			continue
		}
		newDescRel := filepath.Join(newRelPath, rel)
		s.idToPath[descID] = newDescRel
		if dws, ok := s.cache[descID]; ok {
			s.registerIndexLocked(dws, newDescRel)
		}
		moved = append(moved, MovedWorkspace{ID: descID, OldPath: oldDescAbs, NewPath: s.resolveFolder(newDescRel)})
	}

	return moved, nil
}

// collectDescendantsLocked returns the set of all descendant IDs of id (not
// including id itself). Caller must hold s.mu.
func (s *FileStore) collectDescendantsLocked(id string) map[string]struct{} {
	// Build a parent → children adjacency from the cache once.
	childrenByParent := make(map[string][]string, len(s.cache))
	for cid, ws := range s.cache {
		if ws.ParentID != "" {
			childrenByParent[ws.ParentID] = append(childrenByParent[ws.ParentID], cid)
		}
	}

	descendants := make(map[string]struct{})
	stack := append([]string{}, childrenByParent[id]...)
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if _, seen := descendants[current]; seen {
			continue
		}
		descendants[current] = struct{}{}
		stack = append(stack, childrenByParent[current]...)
	}
	return descendants
}

// subtreeHeightLocked returns the height of the subtree rooted at id: 0 for a
// leaf, 1 if it has direct children only, etc. Caller must hold s.mu.
func (s *FileStore) subtreeHeightLocked(id string) int {
	height := 0
	for cid, ws := range s.cache {
		if ws.ParentID == id {
			if h := 1 + s.subtreeHeightLocked(cid); h > height {
				height = h
			}
		}
	}
	return height
}

// registerIndexLocked re-registers a workspace in the global index with a new
// folder path. Caller must hold s.mu.
func (s *FileStore) registerIndexLocked(ws *Workspace, relPath string) {
	if s.index == nil {
		return
	}
	_ = s.index.Register(IndexEntry{
		ID:         ws.ID,
		Name:       ws.Name,
		FolderPath: relPath,
		ParentID:   ws.ParentID,
		UpdatedAt:  ws.UpdatedAt,
	})
}

// moveDir moves a directory tree from src to dst. It first attempts an atomic
// os.Rename; if that fails because the move crosses a filesystem boundary
// (EXDEV), it falls back to a recursive copy followed by removal of the source.
// On a failed cross-device copy the partial destination is cleaned up so the
// source remains the single intact copy.
func moveDir(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	} else if !isCrossDeviceErr(err) {
		return err
	}
	// Cross-device move: rename can't span filesystems, so fall back to a
	// copy-then-delete.
	return copyThenRemove(src, dst)
}

// copyThenRemove implements moveDir's cross-device fallback: copy the tree to
// dst, then remove the source. On a failed copy the partial destination is
// cleaned up so the source stays the single intact copy. If the copy succeeds
// but the source can't be removed, no data is lost (the destination is
// complete) — the error is surfaced so the stale source can be cleaned up.
func copyThenRemove(src, dst string) error {
	if err := copyDir(src, dst); err != nil {
		_ = os.RemoveAll(dst) // roll back partial copy; source is untouched
		return err
	}
	if err := os.RemoveAll(src); err != nil {
		return fmt.Errorf("copied workspace to new location but failed to remove old folder %q: %w", src, err)
	}
	return nil
}

// isCrossDeviceErr reports whether err is an EXDEV (cross-device link) error
// from os.Rename.
func isCrossDeviceErr(err error) bool {
	return errors.Is(err, syscall.EXDEV)
}
