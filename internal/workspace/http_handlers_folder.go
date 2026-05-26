package workspace

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
)

type workspaceFolderRequest struct {
	Path string `json:"path"`
}

type moveAttachmentFileRequest struct {
	TargetFolder string `json:"target_folder"`
	FolderPath   string `json:"folder_path"`
}

func (h *HTTPHandler) CreateWorkspaceFolder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	workspaceID, _, ok := workspaceFolderRouteParts(w, r)
	if !ok {
		return
	}
	var req workspaceFolderRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	cleanPath, err := normalizeManagedWorkspaceFolderPath(req.Path)
	if err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}

	unlock := h.store.Lock(workspaceID)
	defer unlock()

	ws, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}
	filesPath := h.store.GetFilesPath(workspaceID)
	targetPath, _, err := workspaceFolderPathWithinRoot(filesPath, cleanPath)
	if err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}
	if workspaceFolderConflictsWithAttachment(ws, cleanPath) {
		orihttp.Conflict(w, "folder path conflicts with an existing file")
		return
	}

	createdOnDisk := false
	if info, err := os.Stat(targetPath); err == nil {
		if !info.IsDir() {
			orihttp.Conflict(w, "folder path conflicts with an existing file")
			return
		}
	} else if os.IsNotExist(err) {
		if err := os.MkdirAll(targetPath, 0755); err != nil {
			orihttp.InternalError(w, fmt.Sprintf("Failed to create folder: %v", err))
			return
		}
		createdOnDisk = true
	} else {
		orihttp.InternalError(w, fmt.Sprintf("Failed to inspect folder path: %v", err))
		return
	}

	folder, err := addWorkspaceFolderMetadata(ws, cleanPath)
	if err != nil {
		if createdOnDisk {
			_ = os.Remove(targetPath)
		}
		orihttp.Conflict(w, err.Error())
		return
	}

	if err := h.store.Save(ws); err != nil {
		if createdOnDisk {
			_ = os.Remove(targetPath)
		}
		orihttp.InternalError(w, fmt.Sprintf("Failed to save workspace: %v", err))
		return
	}

	h.publishWorkspaceFolderEvent(workspaceID, "folder.created", map[string]any{"folder": folder})
	writeJSON(w, http.StatusCreated, map[string]any{
		"message":   "Folder created successfully",
		"folder":    folder,
		"workspace": workspaceID,
	})
}

func (h *HTTPHandler) UpdateWorkspaceFolder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch && r.Method != http.MethodPut {
		orihttp.MethodNotAllowed(w)
		return
	}

	workspaceID, folderID, ok := workspaceFolderRouteParts(w, r)
	if !ok {
		return
	}
	var req workspaceFolderRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	newPath, err := normalizeManagedWorkspaceFolderPath(req.Path)
	if err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}

	unlock := h.store.Lock(workspaceID)
	defer unlock()

	ws, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}
	idx := findWorkspaceFolderIndexByID(ws.Folders, folderID)
	if idx < 0 {
		orihttp.NotFound(w, "Folder not found")
		return
	}
	oldPath := sanitizeWorkspaceRelativePath(ws.Folders[idx].Path)
	if oldPath == "" {
		orihttp.BadRequest(w, "Folder has invalid path")
		return
	}
	if duplicateIdx := findWorkspaceFolderIndexByPath(ws.Folders, newPath); duplicateIdx >= 0 && duplicateIdx != idx {
		orihttp.Conflict(w, "Folder path already exists")
		return
	}
	if workspaceFolderConflictsWithAttachment(ws, newPath) {
		orihttp.Conflict(w, "folder path conflicts with an existing file")
		return
	}
	if oldPath == newPath {
		writeJSON(w, http.StatusOK, map[string]any{
			"message":   "Folder updated successfully",
			"folder":    ws.Folders[idx],
			"workspace": workspaceID,
		})
		return
	}

	filesPath := h.store.GetFilesPath(workspaceID)
	oldAbs, _, err := workspaceFolderPathWithinRoot(filesPath, oldPath)
	if err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}
	newAbs, _, err := workspaceFolderPathWithinRoot(filesPath, newPath)
	if err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}
	if info, err := os.Stat(newAbs); err == nil {
		if info.IsDir() {
			orihttp.Conflict(w, "Folder path already exists")
		} else {
			orihttp.Conflict(w, "folder path conflicts with an existing file")
		}
		return
	} else if !os.IsNotExist(err) {
		orihttp.InternalError(w, fmt.Sprintf("Failed to inspect folder path: %v", err))
		return
	}
	if info, err := os.Stat(oldAbs); err != nil {
		if os.IsNotExist(err) {
			orihttp.NotFound(w, "Folder path not found on disk")
			return
		}
		orihttp.InternalError(w, fmt.Sprintf("Failed to inspect folder path: %v", err))
		return
	} else if !info.IsDir() {
		orihttp.Conflict(w, "Existing folder path is not a directory")
		return
	}
	if err := os.MkdirAll(filepath.Dir(newAbs), 0755); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to create destination folder parent: %v", err))
		return
	}
	if err := os.Rename(oldAbs, newAbs); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to rename folder: %v", err))
		return
	}

	folder, _, err := renameWorkspaceFolderMetadata(ws, folderID, newPath)
	if err != nil {
		_ = os.Rename(newAbs, oldAbs)
		orihttp.Conflict(w, err.Error())
		return
	}
	if err := h.store.Save(ws); err != nil {
		_ = os.Rename(newAbs, oldAbs)
		orihttp.InternalError(w, fmt.Sprintf("Failed to save workspace: %v", err))
		return
	}

	h.publishWorkspaceFolderEvent(workspaceID, "folder.renamed", map[string]any{
		"folder":   folder,
		"old_path": oldPath,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"message":   "Folder updated successfully",
		"folder":    folder,
		"workspace": workspaceID,
	})
}

func (h *HTTPHandler) DeleteWorkspaceFolder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		orihttp.MethodNotAllowed(w)
		return
	}

	workspaceID, folderID, ok := workspaceFolderRouteParts(w, r)
	if !ok {
		return
	}

	unlock := h.store.Lock(workspaceID)
	defer unlock()

	ws, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}
	idx := findWorkspaceFolderIndexByID(ws.Folders, folderID)
	if idx < 0 {
		orihttp.NotFound(w, "Folder not found")
		return
	}
	folder := ws.Folders[idx]
	folderPath := sanitizeWorkspaceRelativePath(folder.Path)
	if folderPath == "" {
		orihttp.BadRequest(w, "Folder has invalid path")
		return
	}
	if workspaceFolderHasNestedMetadata(ws, folderID, folderPath) || workspaceFolderHasActiveAttachments(ws, folderPath) {
		orihttp.Conflict(w, "Folder is not empty")
		return
	}

	filesPath := h.store.GetFilesPath(workspaceID)
	absFolderPath, _, err := workspaceFolderPathWithinRoot(filesPath, folderPath)
	if err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}
	removedFromDisk := false
	if entries, err := os.ReadDir(absFolderPath); err == nil {
		if len(entries) > 0 {
			orihttp.Conflict(w, "Folder is not empty")
			return
		}
		if err := os.Remove(absFolderPath); err != nil {
			orihttp.InternalError(w, fmt.Sprintf("Failed to delete folder: %v", err))
			return
		}
		removedFromDisk = true
	} else if !os.IsNotExist(err) {
		orihttp.InternalError(w, fmt.Sprintf("Failed to inspect folder: %v", err))
		return
	}

	deletedFolder, err := deleteWorkspaceFolderMetadata(ws, folderID)
	if err != nil {
		if removedFromDisk {
			_ = os.MkdirAll(absFolderPath, 0755)
		}
		orihttp.NotFound(w, err.Error())
		return
	}
	if err := h.store.Save(ws); err != nil {
		if removedFromDisk {
			_ = os.MkdirAll(absFolderPath, 0755)
		}
		orihttp.InternalError(w, fmt.Sprintf("Failed to save workspace: %v", err))
		return
	}

	h.publishWorkspaceFolderEvent(workspaceID, "folder.deleted", map[string]any{"folder": deletedFolder})
	writeJSON(w, http.StatusOK, map[string]any{
		"message":   "Folder deleted successfully",
		"folder":    deletedFolder,
		"workspace": workspaceID,
	})
}

func (h *HTTPHandler) GetWorkspaceFilesTree(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	workspaceID, ok := workspaceIDFromWorkspacePath(w, r)
	if !ok {
		return
	}
	ws, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	files, err := buildWorkspaceFileTree(ws, h.store.GetFilesPath(workspaceID))
	if err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to build file tree: %v", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"files":     files,
		"workspace": workspaceID,
	})
}

func (h *HTTPHandler) MoveAttachmentFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		orihttp.MethodNotAllowed(w)
		return
	}

	workspaceID, attachmentID, ok := attachmentRouteParts(w, r)
	if !ok {
		return
	}
	var req moveAttachmentFileRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	targetFolder := req.TargetFolder
	if strings.TrimSpace(targetFolder) == "" {
		targetFolder = req.FolderPath
	}

	unlock := h.store.Lock(workspaceID)
	defer unlock()

	ws, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}
	attachmentIdx := -1
	for i := range ws.Attachments {
		if ws.Attachments[i].ID == attachmentID {
			attachmentIdx = i
			break
		}
	}
	if attachmentIdx < 0 {
		orihttp.NotFound(w, "Attachment not found")
		return
	}
	attachment := &ws.Attachments[attachmentIdx]
	oldRelativePath := extractAttachmentRelativePath(workspaceID, attachment.File)
	if oldRelativePath == "" {
		orihttp.BadRequest(w, "Attachment does not reference a workspace file")
		return
	}

	filesPath := h.store.GetFilesPath(workspaceID)
	oldAbs, _, err := workspaceFilePathWithinRoot(filesPath, oldRelativePath)
	if err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}
	if info, err := os.Stat(oldAbs); err != nil {
		if os.IsNotExist(err) {
			orihttp.NotFound(w, "Attachment file not found on disk")
			return
		}
		orihttp.InternalError(w, fmt.Sprintf("Failed to inspect attachment file: %v", err))
		return
	} else if info.IsDir() {
		orihttp.BadRequest(w, "Attachment file path is a directory")
		return
	}

	targetDir, cleanTargetFolder, err := workspaceFolderPathWithinRoot(filesPath, targetFolder)
	if err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}
	newRelativePath := filepath.Base(oldRelativePath)
	if cleanTargetFolder != "" {
		newRelativePath = filepath.Join(cleanTargetFolder, newRelativePath)
	}
	if newRelativePath == oldRelativePath {
		hydrated := HydrateAttachment(*attachment, h.store)
		writeJSON(w, http.StatusOK, map[string]any{
			"message":    "Attachment file moved successfully",
			"attachment": hydrated,
			"workspace":  workspaceID,
		})
		return
	}

	newAbs, cleanNewRelativePath, err := workspaceFilePathWithinRoot(filesPath, newRelativePath)
	if err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}
	if _, err := os.Stat(newAbs); err == nil {
		orihttp.Conflict(w, "Destination file already exists")
		return
	} else if !os.IsNotExist(err) {
		orihttp.InternalError(w, fmt.Sprintf("Failed to inspect destination file: %v", err))
		return
	}
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to create destination folder: %v", err))
		return
	}
	if err := os.Rename(oldAbs, newAbs); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to move attachment file: %v", err))
		return
	}

	attachment.File.RelativePath = cleanNewRelativePath
	attachment.File.URL = workspaceFileURL(workspaceID, cleanNewRelativePath)
	attachment.UpdatedAt = time.Now()
	ws.UpdatedAt = attachment.UpdatedAt
	if err := h.store.Save(ws); err != nil {
		_ = os.Rename(newAbs, oldAbs)
		orihttp.InternalError(w, fmt.Sprintf("Failed to save workspace: %v", err))
		return
	}

	hydrated := HydrateAttachment(*attachment, h.store)
	h.publishWorkspaceFolderEvent(workspaceID, "file.moved", map[string]any{
		"attachment": hydrated,
		"old_path":   oldRelativePath,
		"new_path":   cleanNewRelativePath,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"message":    "Attachment file moved successfully",
		"attachment": hydrated,
		"workspace":  workspaceID,
	})
}

func buildWorkspaceFileTree(ws *Workspace, filesPath string) ([]FileInfo, error) {
	items := make(map[string]FileInfo)
	managedFolders := make(map[string]WorkspaceFolder)
	trashedAttachmentPaths := make(map[string]bool)
	for _, attachment := range ws.Attachments {
		if attachment.DeletedAt == nil || attachment.File == nil {
			continue
		}
		if relativePath := extractAttachmentRelativePath(ws.ID, attachment.File); relativePath != "" {
			trashedAttachmentPaths[relativePath] = true
		}
	}
	for _, folder := range ws.Folders {
		clean := sanitizeWorkspaceRelativePath(folder.Path)
		if clean == "" {
			continue
		}
		managedFolders[clean] = folder
		items[clean] = FileInfo{
			ID:           folder.ID,
			FolderID:     folder.ID,
			Source:       workspaceFileSource,
			Name:         filepath.Base(clean),
			RelativePath: clean,
			IsDir:        true,
			ModTime:      folder.UpdatedAt,
		}
	}

	if strings.TrimSpace(filesPath) != "" {
		if err := filepath.WalkDir(filesPath, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.Type()&os.ModeSymlink != 0 {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			rel, err := filepath.Rel(filesPath, path)
			if err != nil {
				return err
			}
			if rel == "." {
				return nil
			}
			clean := sanitizeWorkspaceRelativePath(rel)
			if clean == "" {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if _, _, err := workspaceFilePathWithinRoot(filesPath, clean); err != nil {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if !entry.IsDir() && trashedAttachmentPaths[clean] {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			fileInfo := FileInfo{
				ID:           "file:" + clean,
				Source:       workspaceFileSource,
				Name:         info.Name(),
				RelativePath: clean,
				Size:         info.Size(),
				IsDir:        info.IsDir(),
				ModTime:      info.ModTime(),
			}
			if fileInfo.IsDir {
				fileInfo.ID = "folder:" + clean
				if folder, ok := managedFolders[clean]; ok {
					fileInfo.ID = folder.ID
					fileInfo.FolderID = folder.ID
				}
			} else {
				fileInfo.URL = workspaceFileURL(ws.ID, clean)
			}
			items[clean] = mergeWorkspaceFileInfo(items[clean], fileInfo)
			return nil
		}); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}

	for _, attachment := range ws.Attachments {
		if attachment.DeletedAt != nil || attachment.File == nil {
			continue
		}
		relativePath := extractAttachmentRelativePath(ws.ID, attachment.File)
		if relativePath == "" {
			continue
		}
		ensureWorkspaceFileTreeAncestors(items, managedFolders, relativePath)
		info := items[relativePath]
		if info.Name == "" {
			info.Name = attachment.File.Name
			if info.Name == "" {
				info.Name = filepath.Base(relativePath)
			}
		}
		info.ID = attachment.ID
		info.AttachmentID = attachment.ID
		info.Source = workspaceFileSource
		info.RelativePath = relativePath
		info.URL = workspaceFileURL(ws.ID, relativePath)
		info.Size = attachment.File.Size
		info.IsDir = false
		info.DeletedAt = attachment.DeletedAt
		items[relativePath] = info
	}

	files := make([]FileInfo, 0, len(items))
	for _, item := range items {
		files = append(files, item)
	}
	sortWorkspaceFileInfos(files)
	return files, nil
}

func mergeWorkspaceFileInfo(existing, incoming FileInfo) FileInfo {
	if existing.ID == "" {
		return incoming
	}
	if existing.FolderID != "" {
		incoming.ID = existing.ID
		incoming.FolderID = existing.FolderID
	}
	if !existing.ModTime.IsZero() && incoming.ModTime.IsZero() {
		incoming.ModTime = existing.ModTime
	}
	return incoming
}

func ensureWorkspaceFileTreeAncestors(items map[string]FileInfo, managedFolders map[string]WorkspaceFolder, relativePath string) {
	dir := filepath.Dir(relativePath)
	for dir != "." && dir != string(filepath.Separator) && dir != "" {
		if _, ok := items[dir]; !ok {
			item := FileInfo{
				ID:           "folder:" + dir,
				Source:       workspaceFileSource,
				Name:         filepath.Base(dir),
				RelativePath: dir,
				IsDir:        true,
			}
			if folder, managed := managedFolders[dir]; managed {
				item.ID = folder.ID
				item.FolderID = folder.ID
				item.ModTime = folder.UpdatedAt
			}
			items[dir] = item
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
}

func workspaceFolderConflictsWithAttachment(ws *Workspace, folderPath string) bool {
	clean := sanitizeWorkspaceRelativePath(folderPath)
	for _, attachment := range ws.Attachments {
		if attachment.DeletedAt != nil || attachment.File == nil {
			continue
		}
		if extractAttachmentRelativePath(ws.ID, attachment.File) == clean {
			return true
		}
	}
	return false
}

func workspaceFolderHasNestedMetadata(ws *Workspace, folderID, folderPath string) bool {
	for _, folder := range ws.Folders {
		if folder.ID == folderID {
			continue
		}
		if workspaceFolderContainsPath(folderPath, folder.Path) {
			return true
		}
	}
	return false
}

func workspaceFolderHasActiveAttachments(ws *Workspace, folderPath string) bool {
	for _, attachment := range ws.Attachments {
		if attachment.DeletedAt != nil || attachment.File == nil {
			continue
		}
		if workspaceFolderContainsPath(folderPath, extractAttachmentRelativePath(ws.ID, attachment.File)) {
			return true
		}
	}
	return false
}

func workspaceIDFromWorkspacePath(w http.ResponseWriter, r *http.Request) (string, bool) {
	trimmed := strings.TrimPrefix(r.URL.Path, "/api/workspaces/")
	parts := strings.Split(trimmed, "/")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		orihttp.BadRequest(w, "Invalid URL format")
		return "", false
	}
	return parts[0], true
}

func workspaceFolderRouteParts(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	trimmed := strings.TrimPrefix(r.URL.Path, "/api/workspaces/")
	parts := strings.Split(trimmed, "/")
	if len(parts) < 2 || parts[1] != "folders" {
		orihttp.BadRequest(w, "Invalid URL format")
		return "", "", false
	}
	folderID := ""
	if len(parts) >= 3 {
		folderID = strings.TrimSpace(parts[2])
	}
	if r.Method != http.MethodPost && folderID == "" {
		orihttp.BadRequest(w, "Folder ID is required")
		return "", "", false
	}
	return parts[0], folderID, true
}

func attachmentRouteParts(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	trimmed := strings.TrimPrefix(r.URL.Path, "/api/workspaces/")
	parts := strings.Split(trimmed, "/")
	if len(parts) < 3 || parts[1] != "attachments" || strings.TrimSpace(parts[2]) == "" {
		orihttp.BadRequest(w, "Invalid URL format")
		return "", "", false
	}
	return parts[0], parts[2], true
}

func (h *HTTPHandler) publishWorkspaceFolderEvent(workspaceID, action string, data map[string]any) {
	if h.eventBus == nil {
		return
	}
	if data == nil {
		data = make(map[string]any)
	}
	data["action"] = action
	h.eventBus.Publish(NewWorkspaceEvent(EventWorkspaceUpdated, workspaceID, "workspace-files", data))
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": err})
	}
}
