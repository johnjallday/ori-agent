package workspace

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

var ErrManagedTrashUnavailable = errors.New("the managed workspace folder cannot be safely moved to Trash")

// ReviewManagedTrash resolves a single top-level managed workspace for a
// trash-only action. Imported, anchored, nested and multi-workspace folders
// must use their own lifecycle, not the Settings shortcut.
func (s *FileStore) ReviewManagedTrash(id string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.managedTrashPathLocked(id)
}

// TrashReviewedManaged never falls back to unregistering an external folder
// or permanently deleting anything. Recheck the reviewed path under the same
// lock as the ordinary workspace Trash operation.
func (s *FileStore) TrashReviewedManaged(id, path string) (string, string, error) {
	if path == "" {
		return "", "", ErrManagedTrashUnavailable
	}
	return s.trashWorkspaceFolder(id, "", path)
}

func (s *FileStore) managedTrashPathLocked(id string) (string, error) {
	// Only use the rename-based implementations. The Windows shell API
	// requests recycling but may delete if the volume cannot recycle.
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return "", ErrManagedTrashUnavailable
	}
	ws, rel := s.cache[id], s.idToPath[id]
	if ws == nil || ws.Shape() != ShapeManaged ||
		rel == "" || rel == "." || rel == ".." || rel != filepath.Base(rel) {
		return "", ErrManagedTrashUnavailable
	}
	root, err := filepath.EvalSymlinks(s.basePath)
	if err != nil || !filepath.IsAbs(root) {
		return "", ErrManagedTrashUnavailable
	}
	path := filepath.Join(root, rel)
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() {
		return "", ErrManagedTrashUnavailable
	}
	for childID, child := range s.idToPath {
		if childID == id {
			continue
		}
		childPath := s.resolveFolder(child)
		if managedTrashContains(path, childPath) || managedTrashContains(s.resolveFolder(rel), childPath) {
			return "", ErrManagedTrashUnavailable
		}
	}
	data, err := readNativeWorkspaceFile(path)
	var disk struct {
		ID         string         `json:"id"`
		SharedData map[string]any `json:"shared_data"`
	}
	if err != nil || json.Unmarshal(data, &disk) != nil || disk.ID != id || ShapeFromSharedData(disk.SharedData) != ShapeManaged {
		return "", ErrManagedTrashUnavailable
	}
	// A path swap during the disk check must not select a different directory.
	if current, err := os.Lstat(path); err != nil || !os.SameFile(info, current) {
		return "", ErrManagedTrashUnavailable
	}
	return path, nil
}

func managedTrashContains(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && filepath.IsLocal(rel)
}
