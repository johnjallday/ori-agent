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
		currentPath := sanitizeWorkspaceRelativePath(ws.Folders[i].Path)
		if !workspaceFolderContainsPath(oldPath, currentPath) {
			continue
		}
		suffix := strings.TrimPrefix(currentPath, oldPath)
		suffix = strings.TrimPrefix(suffix, string(filepath.Separator))
		ws.Folders[i].Path = cleanNewPath
		if suffix != "" {
			ws.Folders[i].Path = filepath.Join(cleanNewPath, suffix)
		}
		ws.Folders[i].UpdatedAt = now
	}
	for i := range ws.Attachments {
		if ws.Attachments[i].File == nil || ws.Attachments[i].DeletedAt != nil {
			continue
		}
		currentPath := extractAttachmentRelativePath(ws.ID, ws.Attachments[i].File)
		if !workspaceFolderContainsPath(oldPath, currentPath) {
			continue
		}
		suffix := strings.TrimPrefix(currentPath, oldPath)
		suffix = strings.TrimPrefix(suffix, string(filepath.Separator))
		newRelativePath := cleanNewPath
		if suffix != "" {
			newRelativePath = filepath.Join(cleanNewPath, suffix)
		}
		ws.Attachments[i].File.RelativePath = newRelativePath
		ws.Attachments[i].File.URL = workspaceFileURL(ws.ID, newRelativePath)
		ws.Attachments[i].UpdatedAt = now
	}
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

func sortWorkspaceFileInfos(files []FileInfo) {
	sort.Slice(files, func(i, j int) bool {
		if files[i].RelativePath == files[j].RelativePath {
			return !files[i].IsDir && files[j].IsDir
		}
		return files[i].RelativePath < files[j].RelativePath
	})
}
