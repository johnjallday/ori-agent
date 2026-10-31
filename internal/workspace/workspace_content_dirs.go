package workspace

import (
	"fmt"
	"os"
	"path/filepath"
)

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
