package updatehttp

import (
	"net/http"
	"strconv"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/updatemanager"
)

// Handler handles update-related HTTP requests
type Handler struct {
	updateManager *updatemanager.Manager
}

// NewHandler creates a new update HTTP handler
func NewHandler(updateManager *updatemanager.Manager) *Handler {
	return &Handler{
		updateManager: updateManager,
	}
}

// CheckUpdatesHandler handles GET /api/updates/check
func (h *Handler) CheckUpdatesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	// Parse query parameters
	includePrerelease := r.URL.Query().Get("prerelease") == "true"

	updateInfo, err := h.updateManager.CheckUpdates(includePrerelease)
	if err != nil {
		logger.Warn("Update check unavailable", logger.Fields{"error": err})
		orihttp.ServiceUnavailable(w, "Update check unavailable. Provide GITHUB_TOKEN/GH_TOKEN to increase GitHub API limits.")
		return
	}

	orihttp.WriteJSON(w, updateInfo)
}

func (h *Handler) ListReleasesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		// Parse query parameters
		return
	}

	includePrerelease := r.URL.Query().Get("prerelease") == "true"

	limitStr := r.URL.Query().Get("limit")
	limit := 10 // default limit
	if limitStr != "" {
		if parsed, err := strconv.Atoi(limitStr); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	releases, err := h.updateManager.ListReleases(includePrerelease, limit)
	if err != nil {
		logger.Error("Error listing releases", logger.Fields{"error": err})
		orihttp.InternalError(w, "Failed to list releases")
		return
	}

	response := map[string]any{
		"releases": releases,
		"count":    len(releases),
	}

	orihttp.WriteJSON(w, response)
}

// GetVersionHandler handles GET /api/updates/version
func (h *Handler) GetVersionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	versionInfo := h.updateManager.GetCurrentVersion()

	orihttp.WriteJSON(w, versionInfo)
}
