package workspace

import (
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/vaultref"
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

	// Extract workspace ID from path
	path := strings.TrimPrefix(r.URL.Path, "/api/workspaces/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	workspaceID := parts[0]

	logger.Debug("Processing workspace file upload", logger.Fields{"workspace_id": workspaceID})

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
	vaultRef, ok := parseWorkspaceUploadVaultReference(w, r.FormValue("vault_reference"))
	if !ok {
		return
	}

	// Get the workspace
	workspace, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	filesPath := h.store.GetFilesPath(workspaceID)
	storedFile, err := storeWorkspaceFile(filesPath, file, filename)
	if err != nil {
		logger.Error("Failed to store workspace file", logger.Fields{"error": err, "path": filesPath})
		orihttp.InternalError(w, "Failed to save file")
		return
	}

	// Create attachment with file metadata
	attachment := Attachment{
		ID:          uuid.New().String(),
		WorkspaceID: workspaceID,
		Title:       title,
		Body:        notes,
		Type:        inferTypeFromMime(storedFile.MimeType),
		File:        buildWorkspaceOwnedAttachmentFileMeta(workspaceID, *storedFile, ""),
		VaultRef:    vaultRef,
		X:           0,
		Y:           0,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	if err := workspace.AddAttachment(attachment); err != nil {
		removeWorkspaceOwnedAttachmentFile(h.store, workspaceID, attachment.File, "")
		orihttp.BadRequest(w, fmt.Sprintf("Failed to add attachment: %v", err))
		return
	}

	if err := h.store.Save(workspace); err != nil {
		removeWorkspaceOwnedAttachmentFile(h.store, workspaceID, attachment.File, "")
		orihttp.InternalError(w, fmt.Sprintf("Failed to save workspace: %v", err))
		return
	}

	hydratedAttachment := HydrateAttachment(attachment, h.store)

	// Publish event for live updates
	if h.eventBus != nil {
		h.eventBus.Publish(Event{
			Type:        EventAttachmentCreated,
			WorkspaceID: workspaceID,
			Source:      "api",
			Data: map[string]interface{}{
				"attachment": hydratedAttachment,
			},
		})
	}

	logger.Info("File uploaded to workspace", logger.Fields{
		"workspace_id":  workspaceID,
		"attachment_id": attachment.ID,
		"filename":      filename,
		"size":          storedFile.Size,
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"message":    "File uploaded successfully",
		"attachment": hydratedAttachment,
		"workspace":  workspaceID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

func parseWorkspaceUploadVaultReference(w http.ResponseWriter, raw string) (*vaultref.Reference, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, true
	}
	var ref vaultref.Reference
	if err := json.Unmarshal([]byte(raw), &ref); err != nil {
		orihttp.BadRequest(w, "Invalid vault reference")
		return nil, false
	}
	return vaultref.Normalize(&ref), true
}

// ServeFile handles GET /api/workspaces/:id/files/:filename
// Serves uploaded files from the workspace files directory.
func (h *HTTPHandler) ServeFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	// Extract workspace ID and filename from path
	path := strings.TrimPrefix(r.URL.Path, "/api/workspaces/")
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	workspaceID := parts[0]
	filename := parts[2]

	// Validate the workspace exists
	_, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, "Workspace not found")
		return
	}

	// Construct file path
	filesPath := h.store.GetFilesPath(workspaceID)
	filePath := filepath.Join(filesPath, filename)

	// Security: Ensure the resolved path is within the files directory
	absFilesPath, _ := filepath.Abs(filesPath)
	absFilePath, _ := filepath.Abs(filePath)
	if !isPathWithin(absFilePath, absFilesPath) {
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
