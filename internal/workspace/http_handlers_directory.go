package workspace

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
)

// CreateDirectoryRequest represents the request to create a directory reference
type CreateDirectoryRequest struct {
	Name string  `json:"name"`
	Path string  `json:"path"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
}

// CreateDirectory handles POST /api/studios/:id/directories
func (h *HTTPHandler) CreateDirectory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	studioID := parts[0]

	var req CreateDirectoryRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if req.Name == "" {
		orihttp.BadRequest(w, "Directory name is required")
		return
	}
	if req.Path == "" {
		orihttp.BadRequest(w, "Directory path is required")
		return
	}

	studio, err := h.store.Get(studioID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Studio not found: %v", err))
		return
	}

	dir := DirectoryReference{
		ID:          uuid.New().String(),
		WorkspaceID: studioID,
		Name:        req.Name,
		Path:        req.Path,
		X:           req.X,
		Y:           req.Y,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	if err := studio.AddDirectoryReference(dir); err != nil {
		orihttp.BadRequest(w, fmt.Sprintf("Failed to add directory: %v", err))
		return
	}

	if err := h.store.Save(studio); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save studio: %v", err))
		return
	}

	createdDir, err := studio.GetDirectoryReference(dir.ID)
	if err != nil {
		logger.Debug("Could not retrieve created directory, using original", logger.Fields{"directory_id": dir.ID, "error": err})
		createdDir = &dir
	}

	if h.eventBus != nil {
		h.eventBus.Publish(Event{
			Type:        EventWorkspaceUpdated,
			WorkspaceID: studioID,
			Source:      "api",
			Data: map[string]interface{}{
				"action":    "directory_created",
				"directory": createdDir,
			},
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"message":   "Directory reference created successfully",
		"directory": createdDir,
		"studio":    studioID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// ListDirectories handles GET /api/studios/:id/directories
func (h *HTTPHandler) ListDirectories(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	studioID := parts[0]

	studio, err := h.store.Get(studioID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Studio not found: %v", err))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"directories": studio.DirectoryReferences,
		"count":       len(studio.DirectoryReferences),
		"studio":      studioID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// GetDirectory handles GET /api/studios/:id/directories/:dir_id
func (h *HTTPHandler) GetDirectory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	studioID := parts[0]
	dirID := parts[2]

	studio, err := h.store.Get(studioID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Studio not found: %v", err))
		return
	}

	dir, err := studio.GetDirectoryReference(dirID)
	if err != nil {
		orihttp.NotFound(w, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"directory": dir,
		"studio":    studioID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// UpdateDirectory handles PUT /api/studios/:id/directories/:dir_id
func (h *HTTPHandler) UpdateDirectory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPatch {
		orihttp.MethodNotAllowed(w)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	studioID := parts[0]
	dirID := parts[2]

	var req struct {
		Name *string  `json:"name,omitempty"`
		Path *string  `json:"path,omitempty"`
		X    *float64 `json:"x,omitempty"`
		Y    *float64 `json:"y,omitempty"`
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	studio, err := h.store.Get(studioID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Studio not found: %v", err))
		return
	}

	dir, err := studio.GetDirectoryReference(dirID)
	if err != nil {
		orihttp.NotFound(w, err.Error())
		return
	}

	// Apply updates
	if req.Name != nil {
		dir.Name = *req.Name
	}
	if req.Path != nil {
		dir.Path = *req.Path
	}
	if req.X != nil {
		dir.X = *req.X
	}
	if req.Y != nil {
		dir.Y = *req.Y
	}

	if err := studio.UpdateDirectoryReference(*dir); err != nil {
		orihttp.BadRequest(w, fmt.Sprintf("Failed to update directory: %v", err))
		return
	}

	if err := h.store.Save(studio); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save studio: %v", err))
		return
	}

	updatedDir, err := studio.GetDirectoryReference(dirID)
	if err != nil {
		logger.Debug("Could not retrieve updated directory, using original", logger.Fields{"directory_id": dirID, "error": err})
		updatedDir = dir
	}

	if h.eventBus != nil {
		h.eventBus.Publish(Event{
			Type:        EventWorkspaceUpdated,
			WorkspaceID: studioID,
			Source:      "api",
			Data: map[string]interface{}{
				"action":    "directory_updated",
				"directory": updatedDir,
			},
		})
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"message":   "Directory reference updated successfully",
		"directory": updatedDir,
		"studio":    studioID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// DeleteDirectory handles DELETE /api/studios/:id/directories/:dir_id
func (h *HTTPHandler) DeleteDirectory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		orihttp.MethodNotAllowed(w)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	studioID := parts[0]
	dirID := parts[2]

	studio, err := h.store.Get(studioID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Studio not found: %v", err))
		return
	}

	if err := studio.DeleteDirectoryReference(dirID); err != nil {
		orihttp.NotFound(w, err.Error())
		return
	}

	if err := h.store.Save(studio); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save studio: %v", err))
		return
	}

	if h.eventBus != nil {
		h.eventBus.Publish(Event{
			Type:        EventWorkspaceUpdated,
			WorkspaceID: studioID,
			Source:      "api",
			Data: map[string]interface{}{
				"action":       "directory_deleted",
				"directory_id": dirID,
			},
		})
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"message":      "Directory reference deleted successfully",
		"directory_id": dirID,
		"studio":       studioID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// ListDirectoryFiles handles GET /api/studios/:id/directories/:dir_id/files
func (h *HTTPHandler) ListDirectoryFiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 4 {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	studioID := parts[0]
	dirID := parts[2]

	studio, err := h.store.Get(studioID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Studio not found: %v", err))
		return
	}

	files, err := studio.ListDirectoryFiles(dirID)
	if err != nil {
		orihttp.BadRequest(w, fmt.Sprintf("Failed to list files: %v", err))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"files":        files,
		"count":        len(files),
		"directory_id": dirID,
		"studio":       studioID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// ReadDirectoryFile handles GET /api/studios/:id/directories/:dir_id/files/*
// The file path is everything after /files/
func (h *HTTPHandler) ReadDirectoryFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 5 {
		orihttp.BadRequest(w, "Invalid URL format - file path required")
		return
	}
	studioID := parts[0]
	dirID := parts[2]
	// Everything after "files/" is the relative file path
	filePath := strings.Join(parts[4:], "/")

	if filePath == "" {
		orihttp.BadRequest(w, "File path is required")
		return
	}

	studio, err := h.store.Get(studioID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Studio not found: %v", err))
		return
	}

	content, err := studio.ReadDirectoryFile(dirID, filePath)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			orihttp.NotFound(w, err.Error())
		} else if strings.Contains(err.Error(), "access denied") || strings.Contains(err.Error(), "not allowed") {
			orihttp.Forbidden(w, err.Error())
		} else {
			orihttp.BadRequest(w, fmt.Sprintf("Failed to read file: %v", err))
		}
		return
	}

	// Determine content type based on file extension
	contentType := "application/octet-stream"
	lowerPath := strings.ToLower(filePath)
	switch {
	case strings.HasSuffix(lowerPath, ".json"):
		contentType = "application/json"
	case strings.HasSuffix(lowerPath, ".txt"):
		contentType = "text/plain"
	case strings.HasSuffix(lowerPath, ".md"):
		contentType = "text/markdown"
	case strings.HasSuffix(lowerPath, ".html"), strings.HasSuffix(lowerPath, ".htm"):
		contentType = "text/html"
	case strings.HasSuffix(lowerPath, ".css"):
		contentType = "text/css"
	case strings.HasSuffix(lowerPath, ".js"):
		contentType = "application/javascript"
	case strings.HasSuffix(lowerPath, ".go"):
		contentType = "text/x-go"
	case strings.HasSuffix(lowerPath, ".py"):
		contentType = "text/x-python"
	case strings.HasSuffix(lowerPath, ".yaml"), strings.HasSuffix(lowerPath, ".yml"):
		contentType = "text/yaml"
	case strings.HasSuffix(lowerPath, ".xml"):
		contentType = "application/xml"
	case strings.HasSuffix(lowerPath, ".csv"):
		contentType = "text/csv"
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-File-Path", filePath)
	w.Header().Set("X-Directory-ID", dirID)
	if _, writeErr := w.Write(content); writeErr != nil {
		logger.Error("Failed to write response", logger.Fields{"error": writeErr})
	}
}
