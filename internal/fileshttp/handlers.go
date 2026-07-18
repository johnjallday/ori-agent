package fileshttp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/johnjallday/ori-agent/internal/filewatcher"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/sessionfiles"
)

const (
	// MaxUploadSize is the maximum file upload size (100 MB)
	MaxUploadSize = 100 << 20 // 100 MB
)

// Handler provides HTTP handlers for session file operations
type Handler struct {
	store   *sessionfiles.Store
	watcher *filewatcher.Watcher
}

// Watcher returns the file watcher
func (h *Handler) Watcher() *filewatcher.Watcher {
	return h.watcher
}

// NewHandler creates a new file handler
func NewHandler(store *sessionfiles.Store, watcher *filewatcher.Watcher) *Handler {
	return &Handler{
		store:   store,
		watcher: watcher,
	}
}

// UploadFile handles POST /api/sessions/{id}/files/upload
func (h *Handler) UploadFile(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")

	logger.Debug("Processing file upload", logger.Fields{"session_id": sessionID})

	// Limit upload size
	r.Body = http.MaxBytesReader(w, r.Body, MaxUploadSize)

	// Parse multipart form
	if err := r.ParseMultipartForm(MaxUploadSize); err != nil {
		logger.Warn("Failed to parse multipart form", logger.Fields{"error": err.Error()})
		if strings.Contains(err.Error(), "request body too large") {
			orihttp.BadRequest(w, fmt.Sprintf("File too large. Maximum size is %d MB", MaxUploadSize/(1<<20)))
			return
		}
		orihttp.BadRequest(w, "Failed to parse upload: "+err.Error())
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		logger.Warn("No file in form", logger.Fields{"error": err.Error()})
		orihttp.BadRequest(w, "File is required: "+err.Error())
		return
	}
	defer func() { _ = file.Close() }()

	// Validate filename
	filename, ok := orihttp.ValidateUploadFilename(w, header.Filename)
	if !ok {
		return
	}

	// Add file to store
	entry, err := h.store.AddFileFromReader(sessionID, file, filename, header.Size)
	if err != nil {
		if strings.Contains(err.Error(), "maximum file limit") {
			orihttp.BadRequest(w, err.Error())
			return
		}
		orihttp.InternalError(w, "Failed to save file: "+err.Error())
		return
	}

	logger.Info("File uploaded", logger.Fields{
		"session_id": sessionID,
		"file_id":    entry.ID,
		"filename":   filename,
		"size":       entry.Size,
	})

	_ = orihttp.RespondCreated(w, map[string]any{
		"message": "File uploaded successfully",
		"file":    entry,
	})
}

// LinkFileRequest represents the request to link a file
type LinkFileRequest struct {
	Path string `json:"path"`
	Name string `json:"name,omitempty"`
}

// LinkFile handles POST /api/sessions/{id}/files/link
func (h *Handler) LinkFile(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")

	var req LinkFileRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if req.Path == "" {
		orihttp.BadRequest(w, "Path is required")
		return
	}

	// SECURITY: Validate the source path
	// Clean the path to resolve any .. components
	cleanPath := filepath.Clean(req.Path)

	// Ensure it's an absolute path (prevents relative path attacks)
	if !filepath.IsAbs(cleanPath) {
		orihttp.BadRequest(w, "Path must be an absolute path")
		return
	}

	// Use filename from path if not provided
	name := req.Name
	if name == "" {
		name = filepath.Base(cleanPath)
	}

	// SECURITY: Validate the name to prevent path traversal in the destination
	// This prevents symlinks being created outside the session folder
	safeName, ok := orihttp.ValidateUploadFilename(w, name)
	if !ok {
		return // Error already sent
	}

	// Link file using the validated path and name
	entry, err := h.store.LinkFile(sessionID, cleanPath, safeName)
	if err != nil {
		if strings.Contains(err.Error(), "maximum file limit") {
			orihttp.BadRequest(w, err.Error())
			return
		}
		if strings.Contains(err.Error(), "not accessible") {
			orihttp.BadRequest(w, "File not accessible: "+req.Path)
			return
		}
		orihttp.InternalError(w, "Failed to link file: "+err.Error())
		return
	}

	logger.Info("File linked", logger.Fields{
		"session_id":    sessionID,
		"file_id":       entry.ID,
		"original_path": req.Path,
	})

	_ = orihttp.RespondCreated(w, map[string]any{
		"message": "File linked successfully",
		"file":    entry,
	})
}

// ListFiles handles GET /api/sessions/{id}/files
func (h *Handler) ListFiles(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")

	files, err := h.store.ListFiles(sessionID)
	if err != nil {
		orihttp.InternalError(w, "Failed to list files: "+err.Error())
		return
	}

	_ = orihttp.RespondSuccess(w, map[string]any{
		"files": files,
		"count": len(files),
	})
}

// GetFile handles GET /api/sessions/{id}/files/{fileId}
func (h *Handler) GetFile(w http.ResponseWriter, r *http.Request) {
	sessionID, fileID := r.PathValue("id"), r.PathValue("fileId")

	entry, err := h.store.GetFile(sessionID, fileID)
	if err != nil {
		orihttp.NotFound(w, "File not found")
		return
	}

	_ = orihttp.RespondSuccess(w, entry)
}

// DownloadFile handles GET /api/sessions/{id}/files/{fileId}/download
func (h *Handler) DownloadFile(w http.ResponseWriter, r *http.Request) {
	sessionID, fileID := r.PathValue("id"), r.PathValue("fileId")

	// Get file entry
	entry, err := h.store.GetFile(sessionID, fileID)
	if err != nil {
		orihttp.NotFound(w, "File not found")
		return
	}

	// Get file path
	filePath, err := h.store.GetFilePath(sessionID, fileID)
	if err != nil {
		if strings.Contains(err.Error(), "not accessible") {
			orihttp.BadRequest(w, "File not accessible (broken link)")
			return
		}
		orihttp.InternalError(w, "Failed to get file path")
		return
	}

	// Set headers for download
	w.Header().Set("Content-Type", entry.MimeType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, entry.Name))

	// Serve file
	http.ServeFile(w, r, filePath)
}

// DeleteFile handles DELETE /api/sessions/{id}/files/{fileId}
func (h *Handler) DeleteFile(w http.ResponseWriter, r *http.Request) {
	sessionID, fileID := r.PathValue("id"), r.PathValue("fileId")

	if err := h.store.RemoveFile(sessionID, fileID); err != nil {
		if strings.Contains(err.Error(), "not found") {
			orihttp.NotFound(w, "File not found")
			return
		}
		orihttp.InternalError(w, "Failed to delete file: "+err.Error())
		return
	}

	logger.Info("File deleted", logger.Fields{
		"session_id": sessionID,
		"file_id":    fileID,
	})

	_ = orihttp.RespondSuccess(w, map[string]any{
		"message": "File deleted successfully",
	})
}

// RelinkFileRequest represents the request to relink a file
type RelinkFileRequest struct {
	NewPath string `json:"new_path"`
}

// RelinkFile handles POST /api/sessions/{id}/files/{fileId}/relink
func (h *Handler) RelinkFile(w http.ResponseWriter, r *http.Request) {
	sessionID, fileID := r.PathValue("id"), r.PathValue("fileId")

	var req RelinkFileRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if req.NewPath == "" {
		orihttp.BadRequest(w, "New path is required")
		return
	}

	// SECURITY: Validate the new path
	cleanPath := filepath.Clean(req.NewPath)
	if !filepath.IsAbs(cleanPath) {
		orihttp.BadRequest(w, "New path must be an absolute path")
		return
	}

	if err := h.store.RelinkFile(sessionID, fileID, cleanPath); err != nil {
		if strings.Contains(err.Error(), "not a link") {
			orihttp.BadRequest(w, "File is not a link")
			return
		}
		if strings.Contains(err.Error(), "not accessible") {
			orihttp.BadRequest(w, "New path not accessible")
			return
		}
		orihttp.InternalError(w, "Failed to relink file: "+err.Error())
		return
	}

	// Get updated entry
	entry, _ := h.store.GetFile(sessionID, fileID)

	logger.Info("File relinked", logger.Fields{
		"session_id": sessionID,
		"file_id":    fileID,
		"new_path":   req.NewPath,
	})

	_ = orihttp.RespondSuccess(w, map[string]any{
		"message": "File relinked successfully",
		"file":    entry,
	})
}

// OpenFolder handles POST /api/sessions/{id}/folder/open
func (h *Handler) OpenFolder(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")

	// Get session files path
	folderPath := h.store.GetSessionFilesPath(sessionID)

	// Open folder in native file manager
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", folderPath)
	case "windows":
		cmd = exec.Command("explorer", folderPath)
	case "linux":
		cmd = exec.Command("xdg-open", folderPath)
	default:
		orihttp.BadRequest(w, "Unsupported operating system")
		return
	}

	if err := cmd.Start(); err != nil {
		orihttp.InternalError(w, "Failed to open folder: "+err.Error())
		return
	}

	logger.Info("Opened session folder", logger.Fields{
		"session_id": sessionID,
		"path":       folderPath,
	})

	_ = orihttp.RespondSuccess(w, map[string]any{
		"message": "Folder opened",
		"path":    folderPath,
	})
}

// ValidateLinks handles POST /api/sessions/{id}/files/validate
func (h *Handler) ValidateLinks(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")

	brokenLinks, err := h.store.ValidateLinks(sessionID)
	if err != nil {
		orihttp.InternalError(w, "Failed to validate links: "+err.Error())
		return
	}

	_ = orihttp.RespondSuccess(w, map[string]any{
		"broken_links": brokenLinks,
		"count":        len(brokenLinks),
	})
}

// FileEvents handles GET /api/sessions/{id}/files/events (SSE endpoint)
func (h *Handler) FileEvents(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Get flusher
	flusher, ok := w.(http.Flusher)
	if !ok {
		orihttp.InternalError(w, "Streaming not supported")
		return
	}

	// Note: We no longer auto-start watching here. The client should
	// explicitly call POST /api/sessions/{id}/files/watch when file
	// monitoring is needed, to avoid watching large directories on connect.

	// Send initial connection event
	_, _ = fmt.Fprintf(w, "event: connected\ndata: {\"session_id\": \"%s\"}\n\n", sessionID)
	flusher.Flush()

	// Listen for events
	ctx := r.Context()
	if h.watcher != nil {
		sub := h.watcher.Subscribe(sessionID, 32)
		defer sub.Close()

		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-sub.Events():
				if !ok {
					return
				}
				data, _ := json.Marshal(event)
				_, _ = fmt.Fprintf(w, "event: file_change\ndata: %s\n\n", data)
				flusher.Flush()
			}
		}
	} else {
		// No watcher, just keep connection alive
		<-ctx.Done()
	}
}

// StartWatching handles POST /api/sessions/{id}/files/watch
func (h *Handler) StartWatching(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")

	if h.watcher == nil {
		orihttp.InternalError(w, "File watcher not available")
		return
	}

	filesPath := h.store.GetSessionFilesPath(sessionID)
	if err := h.watcher.Watch(sessionID, filesPath); err != nil {
		orihttp.InternalError(w, "Failed to start watching: "+err.Error())
		return
	}

	_ = orihttp.RespondSuccess(w, map[string]any{
		"message": "Started watching session folder",
		"path":    filesPath,
	})
}

// StopWatching handles DELETE /api/sessions/{id}/files/watch
func (h *Handler) StopWatching(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")

	if h.watcher == nil {
		orihttp.InternalError(w, "File watcher not available")
		return
	}

	if err := h.watcher.Unwatch(sessionID); err != nil {
		orihttp.InternalError(w, "Failed to stop watching: "+err.Error())
		return
	}

	_ = orihttp.RespondSuccess(w, map[string]any{
		"message": "Stopped watching session folder",
	})
}

// ReadFile reads file content - for use by agent plugins
func (h *Handler) ReadFile(sessionID, fileID string) (io.ReadCloser, *sessionfiles.FileEntry, error) {
	entry, err := h.store.GetFile(sessionID, fileID)
	if err != nil {
		return nil, nil, err
	}

	filePath, err := h.store.GetFilePath(sessionID, fileID)
	if err != nil {
		return nil, nil, err
	}

	file, err := openFile(filePath)
	if err != nil {
		return nil, nil, err
	}

	return file, entry, nil
}
