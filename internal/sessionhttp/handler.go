// Package sessionhttp provides HTTP handlers for session management.
package sessionhttp

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Handler handles session-related HTTP requests.
type Handler struct {
	store                 session.HybridStore
	workspaceStore        *workspace.FileStore // optional folder-based workspace store
	workspaceRootResolver func() string
	agentStore            store.Store
	workspaceAllowlist    *workspace.Allowlist
}

// New creates a new session handler.
func New(store session.HybridStore) *Handler {
	return &Handler{store: store}
}

// SetWorkspaceStore sets the folder-based workspace store for enhanced workspace operations.
func (h *Handler) SetWorkspaceStore(ws *workspace.FileStore) {
	h.workspaceStore = ws
}

// SetWorkspaceRootResolver sets the resolver used to determine the default
// directory for newly created workspace folders.
func (h *Handler) SetWorkspaceRootResolver(fn func() string) {
	h.workspaceRootResolver = fn
}

// SetAgentStore sets the agent store used for workspace entry-agent provisioning.
func (h *Handler) SetAgentStore(agentStore store.Store) {
	h.agentStore = agentStore
}

// SetWorkspaceAllowlist sets the per-data-dir allowlist that gates which
// workspaces from the shared ~/Ori Workspaces/ tree are allowed to hydrate
// their agent snapshots into this data directory. When the workspace import
// endpoint succeeds it appends the imported IDs to this allowlist.
func (h *Handler) SetWorkspaceAllowlist(a *workspace.Allowlist) {
	h.workspaceAllowlist = a
}

// handleSessions routes requests to /api/sessions.
func (h *Handler) HandleSessions(w http.ResponseWriter, r *http.Request) {
	// Check if there's an ID in the path (e.g., /api/sessions/{id})
	path := strings.TrimPrefix(r.URL.Path, "/api/sessions")
	path = strings.TrimPrefix(path, "/")

	if path != "" && !strings.Contains(path, "/") {
		// This is a request for a specific session
		h.handleSession(w, r, path)
		return
	}

	if strings.HasSuffix(path, "/messages") {
		// This is a message request
		sessionID := strings.TrimSuffix(path, "/messages")
		h.handleMessages(w, r, sessionID)
		return
	}

	// Handle collection-level requests
	switch r.Method {
	case http.MethodGet:
		h.listSessions(w, r)
	case http.MethodPost:
		h.createSession(w, r)
	default:
		_ = orihttp.RespondMethodNotAllowed(w)
	}
}

// handleSession handles requests for a specific session.
func (h *Handler) handleSession(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		h.getSession(w, r, id)
	case http.MethodPut:
		h.updateSession(w, r, id)
	case http.MethodPatch:
		h.updateSession(w, r, id)
	case http.MethodDelete:
		h.deleteSession(w, r, id)
	default:
		_ = orihttp.RespondMethodNotAllowed(w)
	}
}

// handleMessages handles requests for session messages.
func (h *Handler) handleMessages(w http.ResponseWriter, r *http.Request, sessionID string) {
	switch r.Method {
	case http.MethodGet:
		h.getMessages(w, r, sessionID)
	case http.MethodPost:
		h.addMessage(w, r, sessionID)
	default:
		_ = orihttp.RespondMethodNotAllowed(w)
	}
}

// createSession handles POST /api/sessions.
func (h *Handler) createSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title     string   `json:"title,omitempty"`
		AgentName string   `json:"agent_name"`
		FolderID  string   `json:"folder_id,omitempty"`
		Tags      []string `json:"tags,omitempty"`
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	// Default title
	if req.Title == "" {
		req.Title = "New Session"
	}
	if _, err := h.requireConcreteWorkspace(r.Context(), req.FolderID); err != nil {
		switch {
		case errors.Is(err, session.ErrWorkspaceNotFound):
			_ = orihttp.RespondNotFound(w, "Workspace not found")
		case errors.Is(err, errWorkspaceDisallowsDirectUse):
			_ = orihttp.RespondBadRequest(w, err.Error())
		default:
			_ = orihttp.RespondInternalError(w, "Failed to validate workspace")
		}
		return
	}

	if strings.TrimSpace(req.AgentName) == "" && strings.TrimSpace(req.FolderID) != "" {
		req.AgentName = h.defaultSessionAgentNameForWorkspace(r.Context(), req.FolderID)
	}

	sess := &session.Session{
		Title:     req.Title,
		AgentName: req.AgentName,
		FolderID:  req.FolderID,
		Tags:      req.Tags,
	}

	if err := h.store.CreateSession(r.Context(), sess); err != nil {
		logger.Error("Failed to create session", logger.Fields{"error": err})
		_ = orihttp.RespondInternalError(w, "Failed to create session")
		return
	}

	logger.Info("Session created", logger.Fields{"id": sess.ID, "agent": req.AgentName})

	_ = orihttp.RespondCreated(w, map[string]interface{}{
		"success": true,
		"session": sess,
	})
}

// getSession handles GET /api/sessions/{id}.
func (h *Handler) getSession(w http.ResponseWriter, r *http.Request, id string) {
	sess, err := h.store.GetSession(r.Context(), id)
	if err == session.ErrSessionNotFound {
		_ = orihttp.RespondNotFound(w, "Session not found")
		return
	}
	if err != nil {
		logger.Error("Failed to get session", logger.Fields{"id": id, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to get session")
		return
	}

	orihttp.WriteJSON(w, sess)
}

// updateSession handles PUT/PATCH /api/sessions/{id}.
func (h *Handler) updateSession(w http.ResponseWriter, r *http.Request, id string) {
	sess, err := h.store.GetSession(r.Context(), id)
	if err == session.ErrSessionNotFound {
		_ = orihttp.RespondNotFound(w, "Session not found")
		return
	}
	if err != nil {
		_ = orihttp.RespondInternalError(w, "Failed to get session")
		return
	}

	var req struct {
		Title     *string   `json:"title,omitempty"`
		FolderID  *string   `json:"folder_id,omitempty"`
		Tags      *[]string `json:"tags,omitempty"`
		AgentName *string   `json:"agent_name,omitempty"`
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	// Apply partial updates
	if req.Title != nil {
		sess.Title = *req.Title
	}
	if req.FolderID != nil {
		if _, err := h.requireConcreteWorkspace(r.Context(), *req.FolderID); err != nil {
			switch {
			case errors.Is(err, session.ErrWorkspaceNotFound):
				_ = orihttp.RespondNotFound(w, "Workspace not found")
			case errors.Is(err, errWorkspaceDisallowsDirectUse):
				_ = orihttp.RespondBadRequest(w, err.Error())
			default:
				_ = orihttp.RespondInternalError(w, "Failed to validate workspace")
			}
			return
		}
		sess.FolderID = *req.FolderID
	}
	if req.Tags != nil {
		sess.Tags = *req.Tags
	}
	if req.AgentName != nil && *req.AgentName != "" {
		sess.AgentName = *req.AgentName
	}

	if err := h.store.UpdateSession(r.Context(), sess); err != nil {
		logger.Error("Failed to update session", logger.Fields{"id": id, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to update session")
		return
	}

	logger.Info("Session updated", logger.Fields{"id": id})

	orihttp.WriteJSON(w, map[string]interface{}{
		"success": true,
		"session": sess,
	})
}

// deleteSession handles DELETE /api/sessions/{id}.
func (h *Handler) deleteSession(w http.ResponseWriter, r *http.Request, id string) {
	err := h.store.DeleteSession(r.Context(), id)
	if err == session.ErrSessionNotFound {
		_ = orihttp.RespondNotFound(w, "Session not found")
		return
	}
	if err != nil {
		logger.Error("Failed to delete session", logger.Fields{"id": id, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to delete session")
		return
	}

	logger.Info("Session deleted", logger.Fields{"id": id})

	orihttp.RespondNoContent(w)
}

// listSessions handles GET /api/sessions.
func (h *Handler) listSessions(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	// Build filter
	filter := &session.SessionFilter{}

	if agent := query.Get("agent_name"); agent != "" {
		filter.AgentName = agent
	}

	if folderID := query.Get("folder_id"); folderID != "" {
		filter.FolderID = &folderID
	}

	if tags := query.Get("tags"); tags != "" {
		filter.Tags = strings.Split(tags, ",")
	}

	if anyTags := query.Get("any_tags"); anyTags != "" {
		filter.AnyTags = strings.Split(anyTags, ",")
	}

	// Parse date filters
	if createdAfter := query.Get("created_after"); createdAfter != "" {
		if t, err := time.Parse(time.RFC3339, createdAfter); err == nil {
			filter.CreatedAfter = &t
		}
	}
	if createdBefore := query.Get("created_before"); createdBefore != "" {
		if t, err := time.Parse(time.RFC3339, createdBefore); err == nil {
			filter.CreatedBefore = &t
		}
	}
	if updatedAfter := query.Get("updated_after"); updatedAfter != "" {
		if t, err := time.Parse(time.RFC3339, updatedAfter); err == nil {
			filter.UpdatedAfter = &t
		}
	}
	if updatedBefore := query.Get("updated_before"); updatedBefore != "" {
		if t, err := time.Parse(time.RFC3339, updatedBefore); err == nil {
			filter.UpdatedBefore = &t
		}
	}

	// Build list options
	opts := &session.ListOptions{
		Limit: 50,
		Sort:  session.SortByUpdatedDesc,
	}

	if limit := query.Get("limit"); limit != "" {
		if l, err := strconv.Atoi(limit); err == nil && l > 0 {
			opts.Limit = l
		}
	}

	if offset := query.Get("offset"); offset != "" {
		if o, err := strconv.Atoi(offset); err == nil && o >= 0 {
			opts.Offset = o
		}
	}

	if sort := query.Get("sort"); sort != "" {
		opts.Sort = session.SessionSort(sort)
	}

	// Check for search query
	searchQuery := query.Get("q")
	if searchQuery != "" {
		results, total, err := h.store.Search(r.Context(), searchQuery, filter, opts)
		if err != nil {
			logger.Error("Failed to search sessions", logger.Fields{"error": err})
			_ = orihttp.RespondInternalError(w, "Failed to search sessions")
			return
		}

		orihttp.WriteJSON(w, map[string]interface{}{
			"sessions": results,
			"total":    total,
			"has_more": opts.Offset+len(results) < total,
		})
		return
	}

	// Normal list
	result, err := h.store.ListSessions(r.Context(), filter, opts)
	if err != nil {
		// Don't log context canceled errors - these are normal when requests are aborted
		if !errors.Is(err, context.Canceled) {
			logger.Error("Failed to list sessions", logger.Fields{"error": err})
		}
		_ = orihttp.RespondInternalError(w, "Failed to list sessions")
		return
	}

	orihttp.WriteJSON(w, result)
}

// getMessages handles GET /api/sessions/{id}/messages.
func (h *Handler) getMessages(w http.ResponseWriter, r *http.Request, sessionID string) {
	messages, err := h.store.GetMessages(r.Context(), sessionID)
	if err == session.ErrSessionNotFound {
		_ = orihttp.RespondNotFound(w, "Session not found")
		return
	}
	if err != nil {
		logger.Error("Failed to get messages", logger.Fields{"session_id": sessionID, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to get messages")
		return
	}

	orihttp.WriteJSON(w, map[string]interface{}{
		"messages": messages,
		"count":    len(messages),
	})
}

// addMessage handles POST /api/sessions/{id}/messages.
func (h *Handler) addMessage(w http.ResponseWriter, r *http.Request, sessionID string) {
	var req struct {
		Role       session.MessageRole `json:"role"`
		Content    string              `json:"content"`
		Model      string              `json:"model,omitempty"`
		TokensUsed int                 `json:"tokens_used,omitempty"`
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if req.Role == "" {
		_ = orihttp.RespondBadRequest(w, "role is required")
		return
	}
	if req.Content == "" {
		_ = orihttp.RespondBadRequest(w, "content is required")
		return
	}

	msg := &session.Message{
		Role:       req.Role,
		Content:    req.Content,
		Model:      req.Model,
		TokensUsed: req.TokensUsed,
	}

	if err := h.store.AddMessage(r.Context(), sessionID, msg); err != nil {
		if err == session.ErrSessionNotFound {
			_ = orihttp.RespondNotFound(w, "Session not found")
			return
		}
		logger.Error("Failed to add message", logger.Fields{"session_id": sessionID, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to add message")
		return
	}

	_ = orihttp.RespondCreated(w, map[string]interface{}{
		"success": true,
		"message": msg,
	})
}

// HandleTags handles GET /api/tags for tag listing.
func (h *Handler) HandleTags(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}

	tags, err := h.store.GetAllTags(r.Context())
	if err != nil {
		// Don't log context canceled errors - these are normal when requests are aborted
		if !errors.Is(err, context.Canceled) {
			logger.Error("Failed to get tags", logger.Fields{"error": err})
		}
		_ = orihttp.RespondInternalError(w, "Failed to get tags")
		return
	}

	orihttp.WriteJSON(w, map[string]interface{}{
		"tags": tags,
	})
}

// HandleSessionTags handles PUT /api/sessions/{id}/tags for updating session tags.
func (h *Handler) HandleSessionTags(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}

	// Extract session ID from path
	path := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
	sessionID := strings.TrimSuffix(path, "/tags")

	var req struct {
		Tags []string `json:"tags"`
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if err := h.store.UpdateTags(r.Context(), sessionID, req.Tags); err != nil {
		if err == session.ErrSessionNotFound {
			_ = orihttp.RespondNotFound(w, "Session not found")
			return
		}
		logger.Error("Failed to update tags", logger.Fields{"session_id": sessionID, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to update tags")
		return
	}

	logger.Info("Session tags updated", logger.Fields{"id": sessionID, "tags": req.Tags})

	orihttp.WriteJSON(w, map[string]interface{}{
		"success": true,
		"tags":    req.Tags,
	})
}

// GetCacheStats returns cache statistics for monitoring.
func (h *Handler) HandleCacheStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}

	stats := h.store.GetCacheStats()
	orihttp.WriteJSON(w, stats)
}

// HandleBulkDeleteSessions handles DELETE /api/sessions/bulk
// Deletes multiple sessions at once (cascades to delete all messages)
func (h *Handler) HandleBulkDeleteSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}

	var req struct {
		SessionIDs []string `json:"session_ids"`
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if len(req.SessionIDs) == 0 {
		_ = orihttp.RespondBadRequest(w, "session_ids is required")
		return
	}

	successCount := 0
	failedCount := 0
	var errors []string

	for _, sessionID := range req.SessionIDs {
		err := h.store.DeleteSession(r.Context(), sessionID)
		if err != nil {
			failedCount++
			errors = append(errors, sessionID+": "+err.Error())
			continue
		}
		successCount++
	}

	logger.Info("Bulk delete sessions completed", logger.Fields{
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
