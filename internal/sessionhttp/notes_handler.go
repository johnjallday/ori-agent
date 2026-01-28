package sessionhttp

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/session"
)

// HandleNotes routes requests to /api/notes.
func (h *Handler) HandleNotes(w http.ResponseWriter, r *http.Request) {
	// Check if there's an ID in the path (e.g., /api/notes/{id})
	path := strings.TrimPrefix(r.URL.Path, "/api/notes")
	path = strings.TrimPrefix(path, "/")

	// Handle search endpoint
	if path == "search" {
		h.searchNotes(w, r)
		return
	}

	if path != "" && !strings.Contains(path, "/") {
		// This is a request for a specific note
		h.handleNote(w, r, path)
		return
	}

	// Handle collection-level requests
	switch r.Method {
	case http.MethodPost:
		h.createNote(w, r)
	default:
		_ = orihttp.RespondMethodNotAllowed(w)
	}
}

// HandleWorkspaceNotes routes requests to /api/workspaces/{id}/notes.
func (h *Handler) HandleWorkspaceNotes(w http.ResponseWriter, r *http.Request) {
	// Extract workspace ID from path
	path := r.URL.Path
	path = strings.TrimPrefix(path, "/api/workspaces/")
	path = strings.TrimPrefix(path, "/api/folders/") // Legacy support

	// Path should be "{workspace_id}/notes" or "{workspace_id}/notes/{note_id}"
	parts := strings.Split(path, "/")
	if len(parts) < 2 || parts[1] != "notes" {
		_ = orihttp.RespondBadRequest(w, "Invalid path")
		return
	}

	workspaceID := parts[0]

	// If there's a note ID, route to specific note handler
	if len(parts) > 2 && parts[2] != "" {
		h.handleNote(w, r, parts[2])
		return
	}

	// Handle workspace notes collection
	switch r.Method {
	case http.MethodGet:
		h.listNotesByWorkspace(w, r, workspaceID)
	case http.MethodPost:
		h.createNoteInWorkspace(w, r, workspaceID)
	default:
		_ = orihttp.RespondMethodNotAllowed(w)
	}
}

// handleNote handles requests for a specific note.
func (h *Handler) handleNote(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		h.getNote(w, r, id)
	case http.MethodPut:
		h.updateNote(w, r, id)
	case http.MethodPatch:
		h.updateNote(w, r, id)
	case http.MethodDelete:
		h.deleteNote(w, r, id)
	default:
		_ = orihttp.RespondMethodNotAllowed(w)
	}
}

// createNote handles POST /api/notes.
func (h *Handler) createNote(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkspaceID string `json:"workspace_id"`
		Name        string `json:"name"`
		Content     string `json:"content"`
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if req.WorkspaceID == "" {
		_ = orihttp.RespondBadRequest(w, "workspace_id is required")
		return
	}

	if req.Name == "" {
		req.Name = "Untitled Note"
	}

	now := time.Now()
	note := &session.WorkspaceNote{
		ID:          uuid.New().String(),
		WorkspaceID: req.WorkspaceID,
		Name:        req.Name,
		Content:     req.Content,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := h.store.CreateNote(r.Context(), note); err != nil {
		if err == session.ErrWorkspaceNotFound {
			_ = orihttp.RespondNotFound(w, "Workspace not found")
			return
		}
		logger.Error("Failed to create note", logger.Fields{"error": err})
		_ = orihttp.RespondInternalError(w, "Failed to create note")
		return
	}

	logger.Info("Note created", logger.Fields{"id": note.ID, "workspace_id": req.WorkspaceID, "name": req.Name})

	_ = orihttp.RespondCreated(w, map[string]interface{}{
		"success": true,
		"note":    note,
	})
}

// createNoteInWorkspace handles POST /api/workspaces/{id}/notes.
func (h *Handler) createNoteInWorkspace(w http.ResponseWriter, r *http.Request, workspaceID string) {
	var req struct {
		Name    string `json:"name"`
		Content string `json:"content"`
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if req.Name == "" {
		req.Name = "Untitled Note"
	}

	now := time.Now()
	note := &session.WorkspaceNote{
		ID:          uuid.New().String(),
		WorkspaceID: workspaceID,
		Name:        req.Name,
		Content:     req.Content,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := h.store.CreateNote(r.Context(), note); err != nil {
		if err == session.ErrWorkspaceNotFound {
			_ = orihttp.RespondNotFound(w, "Workspace not found")
			return
		}
		logger.Error("Failed to create note", logger.Fields{"error": err})
		_ = orihttp.RespondInternalError(w, "Failed to create note")
		return
	}

	logger.Info("Note created", logger.Fields{"id": note.ID, "workspace_id": workspaceID, "name": req.Name})

	_ = orihttp.RespondCreated(w, map[string]interface{}{
		"success": true,
		"note":    note,
	})
}

// getNote handles GET /api/notes/{id}.
func (h *Handler) getNote(w http.ResponseWriter, r *http.Request, id string) {
	note, err := h.store.GetNote(r.Context(), id)
	if err == session.ErrNoteNotFound {
		_ = orihttp.RespondNotFound(w, "Note not found")
		return
	}
	if err != nil {
		// Don't log context canceled errors - these are normal when requests are aborted
		if !errors.Is(err, context.Canceled) {
			logger.Error("Failed to get note", logger.Fields{"id": id, "error": err})
		}
		_ = orihttp.RespondInternalError(w, "Failed to get note")
		return
	}

	orihttp.WriteJSON(w, note)
}

// updateNote handles PUT/PATCH /api/notes/{id}.
func (h *Handler) updateNote(w http.ResponseWriter, r *http.Request, id string) {
	note, err := h.store.GetNote(r.Context(), id)
	if err == session.ErrNoteNotFound {
		_ = orihttp.RespondNotFound(w, "Note not found")
		return
	}
	if err != nil {
		_ = orihttp.RespondInternalError(w, "Failed to get note")
		return
	}

	var req struct {
		Name        *string `json:"name,omitempty"`
		Content     *string `json:"content,omitempty"`
		WorkspaceID *string `json:"workspace_id,omitempty"`
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	// Apply partial updates
	if req.Name != nil {
		note.Name = *req.Name
	}
	if req.Content != nil {
		note.Content = *req.Content
	}
	if req.WorkspaceID != nil {
		note.WorkspaceID = *req.WorkspaceID
	}
	note.UpdatedAt = time.Now()

	if err := h.store.UpdateNote(r.Context(), note); err != nil {
		logger.Error("Failed to update note", logger.Fields{"id": id, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to update note")
		return
	}

	logger.Info("Note updated", logger.Fields{"id": id})

	orihttp.WriteJSON(w, map[string]interface{}{
		"success": true,
		"note":    note,
	})
}

// deleteNote handles DELETE /api/notes/{id}.
func (h *Handler) deleteNote(w http.ResponseWriter, r *http.Request, id string) {
	err := h.store.DeleteNote(r.Context(), id)
	if err == session.ErrNoteNotFound {
		_ = orihttp.RespondNotFound(w, "Note not found")
		return
	}
	if err != nil {
		logger.Error("Failed to delete note", logger.Fields{"id": id, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to delete note")
		return
	}

	logger.Info("Note deleted", logger.Fields{"id": id})

	orihttp.RespondNoContent(w)
}

// listNotesByWorkspace handles GET /api/workspaces/{id}/notes.
func (h *Handler) listNotesByWorkspace(w http.ResponseWriter, r *http.Request, workspaceID string) {
	notes, err := h.store.ListNotesByWorkspace(r.Context(), workspaceID)
	if err != nil {
		// Don't log context canceled - it's normal when client disconnects
		if errors.Is(err, context.Canceled) {
			return
		}
		logger.Error("Failed to list notes", logger.Fields{"workspace_id": workspaceID, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to list notes")
		return
	}

	orihttp.WriteJSON(w, map[string]interface{}{
		"notes": notes,
	})
}

// searchNotes handles GET /api/notes/search.
func (h *Handler) searchNotes(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		orihttp.WriteJSON(w, map[string]interface{}{
			"notes": []session.NoteSearchResult{},
		})
		return
	}

	notes, err := h.store.SearchNotes(r.Context(), query, 50)
	if err != nil {
		logger.Error("Failed to search notes", logger.Fields{"query": query, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to search notes")
		return
	}

	orihttp.WriteJSON(w, map[string]interface{}{
		"notes": notes,
	})
}

// HandleBulkDeleteNotes handles DELETE /api/notes/bulk
// Deletes multiple notes at once
func (h *Handler) HandleBulkDeleteNotes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}

	var req struct {
		NoteIDs []string `json:"note_ids"`
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if len(req.NoteIDs) == 0 {
		_ = orihttp.RespondBadRequest(w, "note_ids is required")
		return
	}

	successCount := 0
	failedCount := 0
	var errors []string

	for _, noteID := range req.NoteIDs {
		err := h.store.DeleteNote(r.Context(), noteID)
		if err != nil {
			failedCount++
			errors = append(errors, noteID+": "+err.Error())
			continue
		}
		successCount++
	}

	logger.Info("Bulk delete notes completed", logger.Fields{
		"success_count": successCount,
		"failed_count":  failedCount,
	})

	orihttp.WriteJSON(w, map[string]interface{}{
		"success":       true,
		"message":       "Bulk delete completed",
		"success_count": successCount,
		"failed_count":  failedCount,
		"errors":        errors,
	})
}
