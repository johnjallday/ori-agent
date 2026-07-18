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

type locateAttachmentFileRequest struct {
	RelativePath string `json:"relative_path"`
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
	if workspaceFolderHasNestedMetadata(ws, folderID, folderPath) || workspaceFolderHasActiveAttachments(ws, folderPath) || workspaceFolderHasStorageReferences(ws, folderPath) {
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

	// Get returns a private deep clone, so reconcile and tree-building run on our
	// own copy with no shared state — no per-workspace lock needed. Deliberately
	// NOT holding the workspace lock here: this is a read-heavy endpoint, and
	// holding the exclusive lock across the directory scan serialized concurrent
	// loads (e.g. two browser tabs), making one hang on "Scanning directory...".
	// Only Save needs synchronization, which it handles internally. If a
	// concurrent move races our reconcile save, it self-heals on the next load
	// since the on-disk layout is the source of truth.
	ws, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	filesPath := h.store.GetFilesPath(workspaceID)

	// Reconcile attachment metadata with what is actually on disk before building
	// the tree, so files renamed/moved outside the app are re-bound (and genuine
	// deletions flagged). Failures here are non-fatal: fall back to current state.
	changed, syncEvents, syncErr := reconcileWorkspaceFiles(ws, filesPath)
	if syncErr != nil {
		logger.Warn("Workspace file reconcile failed", logger.Fields{
			"workspace_id": workspaceID,
			"error":        syncErr,
		})
	} else if changed {
		if saveErr := h.store.Save(ws); saveErr != nil {
			logger.Error("Failed to save workspace after file reconcile", logger.Fields{
				"workspace_id": workspaceID,
				"error":        saveErr,
			})
		}
	}

	files, err := buildWorkspaceFileTree(ws, filesPath)
	if err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to build file tree: %v", err))
		return
	}

	h.publishFileSyncEvents(workspaceID, syncEvents)

	writeJSON(w, http.StatusOK, map[string]any{
		"files":     files,
		"workspace": workspaceID,
	})
}

// publishFileSyncEvents emits a "file.moved" event for each attachment that
// reconciliation re-bound to a new on-disk path, mirroring the manual move flow
// so live UI clients refresh.
func (h *HTTPHandler) publishFileSyncEvents(workspaceID string, events []fileSyncEvent) {
	if h.eventBus == nil || len(events) == 0 {
		return
	}
	ws, err := h.store.Get(workspaceID)
	if err != nil {
		return
	}
	for _, ev := range events {
		for i := range ws.Attachments {
			if ws.Attachments[i].ID != ev.attachmentID {
				continue
			}
			h.publishWorkspaceFolderEvent(workspaceID, "file.moved", map[string]any{
				"attachment": HydrateAttachment(ws.Attachments[i], h.store),
				"old_path":   ev.oldPath,
				"new_path":   ev.newPath,
				"reason":     "external_sync",
			})
			break
		}
	}
}

func (h *HTTPHandler) MoveAttachmentFile(w http.ResponseWriter, r *http.Request) {
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

// LocateAttachmentFile handles PATCH /api/workspaces/:id/attachments/:attachment_id/locate
//
// It re-points a "missing" attachment at an existing on-disk file already inside
// the workspace files folder (typically an orphan whose content changed after a
// rename, so checksum reconciliation could not match it automatically). This is
// the DAW-style "Locate" action: unlike MoveAttachmentFile it does not move the
// file, and unlike RelinkAttachmentFile it does not upload a replacement — it
// adopts the file the user points to. JSON body: { "relative_path": "..." }.
func (h *HTTPHandler) LocateAttachmentFile(w http.ResponseWriter, r *http.Request) {
	workspaceID, attachmentID, ok := attachmentRouteParts(w, r)
	if !ok {
		return
	}
	var req locateAttachmentFileRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
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
	if attachment.File == nil {
		orihttp.BadRequest(w, "Attachment does not reference a workspace file")
		return
	}

	filesPath := h.store.GetFilesPath(workspaceID)
	targetAbs, cleanTarget, err := workspaceFilePathWithinRoot(filesPath, req.RelativePath)
	if err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}
	info, err := os.Stat(targetAbs)
	if err != nil {
		if os.IsNotExist(err) {
			orihttp.NotFound(w, "Target file not found")
			return
		}
		orihttp.InternalError(w, fmt.Sprintf("Failed to inspect target file: %v", err))
		return
	}
	if info.IsDir() {
		orihttp.BadRequest(w, "Target path is a directory")
		return
	}

	// Reject linking to a file another active attachment already owns.
	for i := range ws.Attachments {
		if i == attachmentIdx || ws.Attachments[i].DeletedAt != nil || ws.Attachments[i].File == nil {
			continue
		}
		if extractAttachmentRelativePath(workspaceID, ws.Attachments[i].File) == cleanTarget {
			orihttp.Conflict(w, "Another attachment already references that file")
			return
		}
	}

	oldRelativePath := extractAttachmentRelativePath(workspaceID, attachment.File)
	sum, modTime, size, err := hashFileSHA256(targetAbs)
	if err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to read target file: %v", err))
		return
	}

	attachment.File.RelativePath = cleanTarget
	attachment.File.Name = filepath.Base(cleanTarget)
	attachment.File.URL = workspaceFileURL(workspaceID, cleanTarget)
	attachment.File.Size = size
	attachment.File.Checksum = sum
	attachment.File.ChecksumModTime = modTime
	attachment.File.Status = ""
	attachment.UpdatedAt = time.Now()
	ws.UpdatedAt = attachment.UpdatedAt

	if err := h.store.Save(ws); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save workspace: %v", err))
		return
	}

	hydrated := HydrateAttachment(*attachment, h.store)
	h.publishWorkspaceFolderEvent(workspaceID, "file.moved", map[string]any{
		"attachment": hydrated,
		"old_path":   oldRelativePath,
		"new_path":   cleanTarget,
		"reason":     "locate",
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"message":    "Attachment file located successfully",
		"attachment": hydrated,
		"workspace":  workspaceID,
	})
}

func buildWorkspaceFileTree(ws *Workspace, filesPath string) ([]FileInfo, error) {
	items := make(map[string]FileInfo)
	managedFolders := make(map[string]Folder)
	trashedAttachmentPaths := make(map[string]bool)
	diskFilePaths := make(map[string]bool)
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
		if clean == "" || isHiddenWorkspacePath(clean) {
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
			if isHiddenWorkspacePath(clean) {
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
				diskFilePaths[clean] = true
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
		if relativePath == "" || isHiddenWorkspacePath(relativePath) {
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
		// Flag attachments whose backing file is absent on disk so the UI can
		// offer a Locate action instead of rendering a broken entry.
		if !diskFilePaths[relativePath] {
			info.Status = string(AttachmentFileStatusMissing)
		}
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

func ensureWorkspaceFileTreeAncestors(items map[string]FileInfo, managedFolders map[string]Folder, relativePath string) {
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
	workspaceID := r.PathValue("workspaceID")
	attachmentID := r.PathValue("attachmentId")
	if workspaceID == "" || strings.TrimSpace(attachmentID) == "" {
		orihttp.BadRequest(w, "Invalid URL format")
		return "", "", false
	}
	return workspaceID, attachmentID, true
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
