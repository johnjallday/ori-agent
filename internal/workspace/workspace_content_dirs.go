package workspace

import (
	"fmt"
	"os"
	"path/filepath"
)

// makeNewWorkspaceFolder claims the leaf rather than using MkdirAll on it,
// which would accept a directory created by somebody else since a preflight.
func makeNewWorkspaceFolder(folderPath string) error {
	if err := os.MkdirAll(filepath.Dir(folderPath), 0750); err != nil { // #nosec G703 -- canonical local store destination.
		return err
	}
	return os.Mkdir(folderPath, 0750) // #nosec G703 -- canonical local store destination.
}

// The caller supplies only the store's canonical slug and locally registered
// destination. Anchored workspaces keep Ori's state here, not the user's work.
func ensureWorkspaceContentDirs(folderPath string, anchored bool) error {
	dirs := []string{folderPath}
	if !anchored {
		dirs = append(dirs, filepath.Join(folderPath, FilesDir), filepath.Join(folderPath, NotesDir))
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0750); err != nil { // #nosec G703 -- canonical local store destination, never a portable reference path.
			return fmt.Errorf("failed to create workspace folder: %w", err)
		}
	}
	return nil
}
