package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Where a ticket's result file is written. A ticket's destination lives on the
// ticket itself (its ResultStorage): either a folder under the workspace's own
// files root, or a path on disk. These helpers resolve the first kind and keep a
// resolved path inside its base directory.

const StorageTargetWorkspaceFolder = "workspace_folder"

func NormalizeStorageTarget(target string) string {
	switch strings.ToLower(strings.TrimSpace(target)) {
	case StorageTargetWorkspaceFolder:
		return StorageTargetWorkspaceFolder
	default:
		return ""
	}
}

func ResultStorageUsesWorkspaceFolder(storage *ResultStorageConfig) bool {
	if storage == nil {
		return false
	}
	return NormalizeStorageTarget(storage.StorageTarget) == StorageTargetWorkspaceFolder
}

func ResolveWorkspaceFolderBaseDir(resolver AttachmentFilePathResolver, workspaceID, folderPath string) (string, string, error) {
	if resolver == nil {
		return "", "", fmt.Errorf("workspace store is required")
	}
	return workspaceFolderPathWithinRoot(resolver.GetFilesPath(workspaceID), folderPath)
}

// BuildFinalPath safely joins base directory and file path, then validates the result
func BuildFinalPath(baseDir, filePath string) (string, error) {
	// Validate inputs first
	if baseDir == "" {
		return "", fmt.Errorf("base directory is empty")
	}
	if filePath == "" {
		return "", fmt.Errorf("file path is empty")
	}

	// Clean both paths
	cleanBase := filepath.Clean(baseDir)
	cleanFile := filepath.Clean(filePath)

	// Join paths
	finalPath := filepath.Join(cleanBase, cleanFile)

	// Critical security check: ensure final path is still within base directory
	// Get absolute paths for comparison
	absBase, err := filepath.Abs(cleanBase)
	if err != nil {
		return "", fmt.Errorf("failed to resolve base directory: %w", err)
	}

	absFinal, err := filepath.Abs(finalPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve final path: %w", err)
	}

	// Ensure final path is within the base directory. Use filepath.Rel instead
	// of prefix matching so sibling paths with similar names cannot pass.
	if !isPathWithin(absFinal, absBase) {
		return "", fmt.Errorf("file path escapes base directory: %s", filePath)
	}
	if _, statErr := os.Stat(absBase); statErr == nil {
		if !pathWithinRootAfterSymlinks(absFinal, absBase) {
			return "", fmt.Errorf("file path escapes base directory: %s", filePath)
		}
	} else if !os.IsNotExist(statErr) {
		return "", fmt.Errorf("failed to inspect base directory: %w", statErr)
	}

	return finalPath, nil
}
