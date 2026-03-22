package workspace

import (
	"os"
	"path/filepath"
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
func ResolveProjectPath(workspaceFolderPath, projectPath string) (string, bool) {
	if projectPath == "" {
		return "", false
	}

	absPath := filepath.Join(workspaceFolderPath, projectPath)
	absPath = filepath.Clean(absPath)

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
