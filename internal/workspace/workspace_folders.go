package workspace

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

const workspaceFileSource = "workspace"

func normalizeManagedWorkspaceFolderPath(raw string) (string, error) {
	clean := sanitizeWorkspaceRelativePath(raw)
	if clean == "" {
		return "", fmt.Errorf("folder path is required")
	}
	return clean, nil
}

func workspaceFolderContainsPath(folderPath, relativePath string) bool {
	folderPath = sanitizeWorkspaceRelativePath(folderPath)
	relativePath = sanitizeWorkspaceRelativePath(relativePath)
	if folderPath == "" || relativePath == "" {
		return false
	}
	if relativePath == folderPath {
		return true
	}
	return strings.HasPrefix(relativePath, folderPath+string(filepath.Separator))
}

func rebaseWorkspaceFolderPath(oldPath, newPath, currentPath string) (string, bool) {
	oldPath = sanitizeWorkspaceRelativePath(oldPath)
	newPath = sanitizeWorkspaceRelativePath(newPath)
	currentPath = sanitizeWorkspaceRelativePath(currentPath)
	if oldPath == "" || newPath == "" || !workspaceFolderContainsPath(oldPath, currentPath) {
		return "", false
	}
	suffix := strings.TrimPrefix(currentPath, oldPath)
	suffix = strings.TrimPrefix(suffix, string(filepath.Separator))
	if suffix == "" {
		return newPath, true
	}
	return filepath.Join(newPath, suffix), true
}

func findWorkspaceFolderIndexByID(folders []WorkspaceFolder, id string) int {
	for i := range folders {
		if folders[i].ID == id {
			return i
		}
	}
	return -1
}

func findWorkspaceFolderIndexByPath(folders []WorkspaceFolder, path string) int {
	clean := sanitizeWorkspaceRelativePath(path)
	for i := range folders {
		if sanitizeWorkspaceRelativePath(folders[i].Path) == clean {
			return i
		}
	}
	return -1
}

func addWorkspaceFolderMetadata(ws *Workspace, folderPath string) (WorkspaceFolder, error) {
	clean, err := normalizeManagedWorkspaceFolderPath(folderPath)
	if err != nil {
		return WorkspaceFolder{}, err
	}
	if findWorkspaceFolderIndexByPath(ws.Folders, clean) >= 0 {
		return WorkspaceFolder{}, fmt.Errorf("folder already exists: %s", clean)
	}

	now := time.Now()
	folder := WorkspaceFolder{
		ID:        uuid.New().String(),
		Path:      clean,
		CreatedAt: now,
		UpdatedAt: now,
	}
	ws.Folders = append(ws.Folders, folder)
	ws.UpdatedAt = now
	return folder, nil
}

func renameWorkspaceFolderMetadata(ws *Workspace, folderID, newPath string) (WorkspaceFolder, string, error) {
	cleanNewPath, err := normalizeManagedWorkspaceFolderPath(newPath)
	if err != nil {
		return WorkspaceFolder{}, "", err
	}
	idx := findWorkspaceFolderIndexByID(ws.Folders, folderID)
	if idx < 0 {
		return WorkspaceFolder{}, "", fmt.Errorf("folder %s not found", folderID)
	}
	oldPath := sanitizeWorkspaceRelativePath(ws.Folders[idx].Path)
	if oldPath == "" {
		return WorkspaceFolder{}, "", fmt.Errorf("folder %s has invalid path", folderID)
	}
	if oldPath == cleanNewPath {
		return ws.Folders[idx], oldPath, nil
	}
	if duplicateIdx := findWorkspaceFolderIndexByPath(ws.Folders, cleanNewPath); duplicateIdx >= 0 && duplicateIdx != idx {
		return WorkspaceFolder{}, "", fmt.Errorf("folder already exists: %s", cleanNewPath)
	}

	now := time.Now()
	for i := range ws.Folders {
		if rebasedPath, ok := rebaseWorkspaceFolderPath(oldPath, cleanNewPath, ws.Folders[i].Path); ok {
			ws.Folders[i].Path = rebasedPath
			ws.Folders[i].UpdatedAt = now
		}
	}
	for i := range ws.Attachments {
		if ws.Attachments[i].File == nil || ws.Attachments[i].DeletedAt != nil {
			continue
		}
		if newRelativePath, ok := rebaseWorkspaceFolderPath(oldPath, cleanNewPath, extractAttachmentRelativePath(ws.ID, ws.Attachments[i].File)); ok {
			ws.Attachments[i].File.RelativePath = newRelativePath
			ws.Attachments[i].File.URL = workspaceFileURL(ws.ID, newRelativePath)
			ws.Attachments[i].UpdatedAt = now
		}
	}
	rebaseWorkspaceFolderStorageReferences(ws, oldPath, cleanNewPath, now)
	ws.UpdatedAt = now
	return ws.Folders[idx], oldPath, nil
}

func deleteWorkspaceFolderMetadata(ws *Workspace, folderID string) (WorkspaceFolder, error) {
	idx := findWorkspaceFolderIndexByID(ws.Folders, folderID)
	if idx < 0 {
		return WorkspaceFolder{}, fmt.Errorf("folder %s not found", folderID)
	}
	folder := ws.Folders[idx]
	ws.Folders = append(ws.Folders[:idx], ws.Folders[idx+1:]...)
	ws.UpdatedAt = time.Now()
	return folder, nil
}

func rebaseWorkspaceFolderStorageReferences(ws *Workspace, oldPath, newPath string, now time.Time) {
	if ws == nil {
		return
	}
	for i := range ws.StoreNodes {
		if !StoreNodeUsesWorkspaceFolder(&ws.StoreNodes[i]) {
			continue
		}
		if rebasedPath, ok := rebaseWorkspaceFolderPath(oldPath, newPath, ws.StoreNodes[i].WorkspaceFolder); ok {
			ws.StoreNodes[i].WorkspaceFolder = rebasedPath
			ws.StoreNodes[i].BaseDir = rebasedPath
			ws.StoreNodes[i].UpdatedAt = now
		}
	}
	for i := range ws.Tasks {
		if !ResultStorageUsesWorkspaceFolder(ws.Tasks[i].ResultStorage) {
			continue
		}
		if rebasedPath, ok := rebaseWorkspaceFolderPath(oldPath, newPath, ws.Tasks[i].ResultStorage.WorkspaceFolder); ok {
			ws.Tasks[i].ResultStorage.WorkspaceFolder = rebasedPath
		}
	}
}

func workspaceFolderHasStorageReferences(ws *Workspace, folderPath string) bool {
	if ws == nil {
		return false
	}
	for i := range ws.StoreNodes {
		if StoreNodeUsesWorkspaceFolder(&ws.StoreNodes[i]) && workspaceFolderContainsPath(folderPath, ws.StoreNodes[i].WorkspaceFolder) {
			return true
		}
	}
	for i := range ws.Tasks {
		if ResultStorageUsesWorkspaceFolder(ws.Tasks[i].ResultStorage) && workspaceFolderContainsPath(folderPath, ws.Tasks[i].ResultStorage.WorkspaceFolder) {
			return true
		}
	}
	return false
}

func sortWorkspaceFileInfos(files []FileInfo) {
	sort.Slice(files, func(i, j int) bool {
		if files[i].RelativePath == files[j].RelativePath {
			return !files[i].IsDir && files[j].IsDir
		}
		return files[i].RelativePath < files[j].RelativePath
	})
}
