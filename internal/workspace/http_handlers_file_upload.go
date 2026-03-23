package workspace

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
)

const (
	// MaxWorkspaceFileSize is the maximum file upload size (100 MB)
	MaxWorkspaceFileSize = 100 << 20 // 100 MB
)

// UploadFile handles POST /api/workspaces/:id/files
// Accepts multipart form data with a file and creates an attachment with file metadata.
func (h *HTTPHandler) UploadFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	// Extract studio ID from path
	path := strings.TrimPrefix(r.URL.Path, "/api/workspaces/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	studioID := parts[0]

	logger.Debug("Processing workspace file upload", logger.Fields{"workspace_id": studioID})

	// Limit upload size
	r.Body = http.MaxBytesReader(w, r.Body, MaxWorkspaceFileSize)

	// Parse multipart form
	if err := r.ParseMultipartForm(MaxWorkspaceFileSize); err != nil {
		logger.Warn("Failed to parse multipart form", logger.Fields{"error": err.Error()})
		if strings.Contains(err.Error(), "request body too large") {
			orihttp.BadRequest(w, fmt.Sprintf("File too large. Maximum size is %d MB", MaxWorkspaceFileSize/(1<<20)))
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

	// Validate and sanitize filename
	filename, ok := orihttp.ValidateUploadFilename(w, header.Filename)
	if !ok {
		return
	}

	// Get optional form fields
	title := r.FormValue("title")
	if title == "" {
		title = filename
	}
	notes := r.FormValue("notes")

	// Get the workspace
	studio, err := h.store.Get(studioID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	// Create files directory for this workspace
	filesPath := h.store.GetFilesPath(studioID)
	if err := os.MkdirAll(filesPath, 0755); err != nil {
		logger.Error("Failed to create files directory", logger.Fields{"error": err, "path": filesPath})
		orihttp.InternalError(w, "Failed to create files directory")
		return
	}

	// Generate unique file ID and destination path
	fileID := uuid.New().String()
	// Use fileID prefix to ensure uniqueness even with same filenames
	destFilename := fileID[:8] + "_" + filename
	destPath := filepath.Join(filesPath, destFilename)

	// Save the file
	destFile, err := os.Create(destPath)
	if err != nil {
		logger.Error("Failed to create destination file", logger.Fields{"error": err, "path": destPath})
		orihttp.InternalError(w, "Failed to save file")
		return
	}
	defer func() { _ = destFile.Close() }()

	written, err := io.Copy(destFile, file)
	if err != nil {
		_ = os.Remove(destPath) // Cleanup on failure
		logger.Error("Failed to write file", logger.Fields{"error": err})
		orihttp.InternalError(w, "Failed to write file")
		return
	}

	// Detect MIME type
	mimeType := detectMimeType(filename)

	// Create attachment with file metadata
	attachment := Attachment{
		ID:          uuid.New().String(),
		WorkspaceID: studioID,
		Title:       title,
		Body:        notes,
		Type:        inferTypeFromMime(mimeType),
		File: &AttachmentFileMeta{
			Name: filename,
			Size: written,
			Mime: mimeType,
			URL:  fmt.Sprintf("/api/workspaces/%s/files/%s", studioID, destFilename),
		},
		X:         0,
		Y:         0,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := studio.AddAttachment(attachment); err != nil {
		_ = os.Remove(destPath) // Cleanup on failure
		orihttp.BadRequest(w, fmt.Sprintf("Failed to add attachment: %v", err))
		return
	}

	if err := h.store.Save(studio); err != nil {
		_ = os.Remove(destPath) // Cleanup on failure
		orihttp.InternalError(w, fmt.Sprintf("Failed to save workspace: %v", err))
		return
	}

	// Publish event for live updates
	if h.eventBus != nil {
		h.eventBus.Publish(Event{
			Type:        EventAttachmentCreated,
			WorkspaceID: studioID,
			Source:      "api",
			Data: map[string]interface{}{
				"attachment": attachment,
			},
		})
	}

	logger.Info("File uploaded to workspace", logger.Fields{
		"workspace_id":  studioID,
		"attachment_id": attachment.ID,
		"filename":      filename,
		"size":          written,
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"message":    "File uploaded successfully",
		"attachment": attachment,
		"studio":     studioID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// ServeFile handles GET /api/workspaces/:id/files/:filename
// Serves uploaded files from the workspace files directory.
func (h *HTTPHandler) ServeFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	// Extract studio ID and filename from path
	path := strings.TrimPrefix(r.URL.Path, "/api/workspaces/")
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	studioID := parts[0]
	filename := parts[2]

	// Validate the workspace exists
	_, err := h.store.Get(studioID)
	if err != nil {
		orihttp.NotFound(w, "Workspace not found")
		return
	}

	// Construct file path
	filesPath := h.store.GetFilesPath(studioID)
	filePath := filepath.Join(filesPath, filename)

	// Security: Ensure the resolved path is within the files directory
	absFilesPath, _ := filepath.Abs(filesPath)
	absFilePath, _ := filepath.Abs(filePath)
	if !strings.HasPrefix(absFilePath, absFilesPath) {
		orihttp.BadRequest(w, "Invalid file path")
		return
	}

	// Check if file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		orihttp.NotFound(w, "File not found")
		return
	}

	// Set content type based on file extension
	contentType := detectMimeType(filename)
	w.Header().Set("Content-Type", contentType)

	// Serve the file
	http.ServeFile(w, r, filePath)
}

// detectMimeType detects MIME type from filename extension
func detectMimeType(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	mimeType := mime.TypeByExtension(ext)
	if mimeType == "" {
		// Default to octet-stream for unknown types
		return "application/octet-stream"
	}
	return mimeType
}

// inferTypeFromMime infers attachment type from MIME type
func inferTypeFromMime(mimeType string) AttachmentType {
	lower := strings.ToLower(mimeType)
	if strings.HasPrefix(lower, "image/") {
		return AttachmentTypeImage
	}
	if strings.HasPrefix(lower, "text/") ||
		strings.Contains(lower, "pdf") ||
		strings.Contains(lower, "document") ||
		strings.Contains(lower, "spreadsheet") {
		return AttachmentTypeDoc
	}
	return AttachmentTypeOther
}
