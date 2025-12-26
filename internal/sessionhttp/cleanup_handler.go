// Package sessionhttp provides HTTP handlers for session management.
package sessionhttp

import (
	"encoding/json"
	"net/http"
	"strconv"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
)

// HandleCleanup handles requests to /api/sessions/cleanup.
func (h *Handler) HandleCleanup(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.getCleanupPreview(w, r)
	case http.MethodPost:
		h.runCleanup(w, r)
	default:
		_ = orihttp.RespondMethodNotAllowed(w)
	}
}

// HandleStorageStats handles GET /api/sessions/stats.
func (h *Handler) HandleStorageStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}

	stats, err := h.store.GetStorageStats(r.Context())
	if err != nil {
		logger.Error("Failed to get storage stats", logger.Fields{"error": err})
		_ = orihttp.RespondInternalError(w, "Failed to get storage statistics")
		return
	}

	orihttp.WriteJSON(w, stats)
}

// getCleanupPreview returns sessions that would be deleted by cleanup.
func (h *Handler) getCleanupPreview(w http.ResponseWriter, r *http.Request) {
	// Get inactive days from query (default 30)
	daysStr := r.URL.Query().Get("days")
	days := 30
	if daysStr != "" {
		if d, err := strconv.Atoi(daysStr); err == nil && d > 0 {
			days = d
		}
	}

	sessions, err := h.store.GetInactiveSessions(r.Context(), days)
	if err != nil {
		logger.Error("Failed to get inactive sessions", logger.Fields{"error": err})
		_ = orihttp.RespondInternalError(w, "Failed to get inactive sessions")
		return
	}

	orihttp.WriteJSON(w, map[string]any{
		"inactive_days": days,
		"sessions":      sessions,
		"count":         len(sessions),
	})
}

// runCleanup performs the actual cleanup operation.
func (h *Handler) runCleanup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Days int `json:"days"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		_ = orihttp.RespondBadRequest(w, "Invalid request body")
		return
	}

	// Default to 30 days if not specified
	if req.Days <= 0 {
		req.Days = 30
	}

	// Perform cleanup
	deleted, err := h.store.Cleanup(r.Context(), req.Days)
	if err != nil {
		logger.Error("Cleanup failed", logger.Fields{"error": err, "days": req.Days})
		_ = orihttp.RespondInternalError(w, "Cleanup failed")
		return
	}

	logger.Info("Session cleanup completed", logger.Fields{
		"deleted":       deleted,
		"inactive_days": req.Days,
	})

	orihttp.WriteJSON(w, map[string]any{
		"deleted":       deleted,
		"inactive_days": req.Days,
	})
}
