package workspace

import (
	"os"
	"path/filepath"
	"strings"
)

// ProjectPathInfo contains resolved project path information for a workspace.
type ProjectPathInfo struct {
	// RelativePath is the stored relative path from workspace.json.
	RelativePath string `json:"relative_path"`
	// AbsolutePath is the resolved absolute path on the current machine.
	AbsolutePath string `json:"absolute_path,omitempty"`
	// Resolved is true if the absolute path exists on disk.
	Resolved bool `json:"resolved"`
}

// ResolveProjectPath resolves a workspace's project_path relative to its folder.
// Returns the absolute path and whether it exists on disk.
// The resolved path must remain within the workspace folder to prevent traversal.
func ResolveProjectPath(workspaceFolderPath, projectPath string) (string, bool) {
	if projectPath == "" {
		return "", false
	}

	absPath := filepath.Clean(filepath.Join(workspaceFolderPath, projectPath))
	absFolder := filepath.Clean(workspaceFolderPath)

	// Ensure the resolved path stays within the workspace folder
	if !strings.HasPrefix(absPath, absFolder+string(filepath.Separator)) && absPath != absFolder {
		return "", false
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return absPath, false
	}

	return absPath, info.IsDir()
}

// GetProjectPathInfo returns resolved project path info for a workspace.
func (s *FileStore) GetProjectPathInfo(workspaceID string) (*ProjectPathInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ws, ok := s.cache[workspaceID]
	if !ok {
		return nil, nil
	}

	if ws.ProjectPath == "" {
		return nil, nil
	}

	relPath, ok := s.idToPath[workspaceID]
	if !ok {
		return &ProjectPathInfo{RelativePath: ws.ProjectPath}, nil
	}

	folderPath := filepath.Join(s.basePath, relPath)
	absPath, resolved := ResolveProjectPath(folderPath, ws.ProjectPath)

	return &ProjectPathInfo{
		RelativePath: ws.ProjectPath,
		AbsolutePath: absPath,
		Resolved:     resolved,
	}, nil
}
