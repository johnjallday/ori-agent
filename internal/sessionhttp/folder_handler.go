package sessionhttp

import (
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/session"
)

// HandleFolders routes requests to /api/folders.
func (h *Handler) HandleFolders(w http.ResponseWriter, r *http.Request) {
	// Check if there's an ID in the path (e.g., /api/folders/{id})
	path := strings.TrimPrefix(r.URL.Path, "/api/folders")
	path = strings.TrimPrefix(path, "/")

	if path != "" && !strings.Contains(path, "/") {
		// This is a request for a specific folder
		h.handleFolder(w, r, path)
		return
	}

	// Handle collection-level requests
	switch r.Method {
	case http.MethodGet:
		h.listFolders(w, r)
	case http.MethodPost:
		h.createFolder(w, r)
	default:
		_ = orihttp.RespondMethodNotAllowed(w)
	}
}

// handleFolder handles requests for a specific folder.
func (h *Handler) handleFolder(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		h.getFolder(w, r, id)
	case http.MethodPut:
		h.updateFolder(w, r, id)
	case http.MethodPatch:
		h.updateFolder(w, r, id)
	case http.MethodDelete:
		h.deleteFolder(w, r, id)
	default:
		_ = orihttp.RespondMethodNotAllowed(w)
	}
}

// createFolder handles POST /api/folders.
func (h *Handler) createFolder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		ParentID    string `json:"parent_id,omitempty"`
		Color       string `json:"color,omitempty"`
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if req.Name == "" {
		_ = orihttp.RespondBadRequest(w, "name is required")
		return
	}

	folder := &session.Folder{
		Name:        req.Name,
		Description: req.Description,
		ParentID:    req.ParentID,
		Color:       req.Color,
	}

	if err := h.store.CreateFolder(r.Context(), folder); err != nil {
		logger.Error("Failed to create folder", logger.Fields{"error": err})
		_ = orihttp.RespondInternalError(w, "Failed to create folder")
		return
	}

	logger.Info("Folder created", logger.Fields{"id": folder.ID, "name": req.Name})

	_ = orihttp.RespondCreated(w, map[string]interface{}{
		"success": true,
		"folder":  folder,
	})
}

// getFolder handles GET /api/folders/{id}.
func (h *Handler) getFolder(w http.ResponseWriter, r *http.Request, id string) {
	folder, err := h.store.GetFolder(r.Context(), id)
	if err == session.ErrFolderNotFound {
		_ = orihttp.RespondNotFound(w, "Folder not found")
		return
	}
	if err != nil {
		logger.Error("Failed to get folder", logger.Fields{"id": id, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to get folder")
		return
	}

	orihttp.WriteJSON(w, folder)
}

// updateFolder handles PUT/PATCH /api/folders/{id}.
func (h *Handler) updateFolder(w http.ResponseWriter, r *http.Request, id string) {
	folder, err := h.store.GetFolder(r.Context(), id)
	if err == session.ErrFolderNotFound {
		_ = orihttp.RespondNotFound(w, "Folder not found")
		return
	}
	if err != nil {
		_ = orihttp.RespondInternalError(w, "Failed to get folder")
		return
	}

	var req struct {
		Name        *string `json:"name,omitempty"`
		Description *string `json:"description,omitempty"`
		ParentID    *string `json:"parent_id,omitempty"`
		Color       *string `json:"color,omitempty"`
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	// Apply partial updates
	if req.Name != nil {
		folder.Name = *req.Name
	}
	if req.Description != nil {
		folder.Description = *req.Description
	}
	if req.ParentID != nil {
		// Check for circular reference
		if *req.ParentID == folder.ID {
			_ = orihttp.RespondBadRequest(w, "Folder cannot be its own parent")
			return
		}
		folder.ParentID = *req.ParentID
	}
	if req.Color != nil {
		folder.Color = *req.Color
	}

	if err := h.store.UpdateFolder(r.Context(), folder); err != nil {
		logger.Error("Failed to update folder", logger.Fields{"id": id, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to update folder")
		return
	}

	logger.Info("Folder updated", logger.Fields{"id": id})

	orihttp.WriteJSON(w, map[string]interface{}{
		"success": true,
		"folder":  folder,
	})
}

// deleteFolder handles DELETE /api/folders/{id}.
func (h *Handler) deleteFolder(w http.ResponseWriter, r *http.Request, id string) {
	err := h.store.DeleteFolder(r.Context(), id)
	if err == session.ErrFolderNotFound {
		_ = orihttp.RespondNotFound(w, "Folder not found")
		return
	}
	if err != nil {
		logger.Error("Failed to delete folder", logger.Fields{"id": id, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to delete folder")
		return
	}

	logger.Info("Folder deleted", logger.Fields{"id": id})

	orihttp.RespondNoContent(w)
}

// listFolders handles GET /api/folders.
func (h *Handler) listFolders(w http.ResponseWriter, r *http.Request) {
	tree := r.URL.Query().Get("tree") == "true"

	if tree {
		folders, err := h.store.GetFolderTree(r.Context())
		if err != nil {
			logger.Error("Failed to get folder tree", logger.Fields{"error": err})
			_ = orihttp.RespondInternalError(w, "Failed to get folders")
			return
		}

		orihttp.WriteJSON(w, map[string]interface{}{
			"folders": folders,
		})
		return
	}

	folders, err := h.store.ListFolders(r.Context())
	if err != nil {
		logger.Error("Failed to list folders", logger.Fields{"error": err})
		_ = orihttp.RespondInternalError(w, "Failed to list folders")
		return
	}

	orihttp.WriteJSON(w, map[string]interface{}{
		"folders": folders,
	})
}
