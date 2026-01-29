package updatehttp

import (
	"net/http"
	"strconv"
	"time"

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

	response := map[string]interface{}{
		"releases": releases,
		"count":    len(releases),
	}

	orihttp.WriteJSON(w, response)
}

func (h *Handler) DownloadUpdateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	var request struct {
		Version     string `json:"version"`
		AutoRestart bool   `json:"autoRestart"`
	}

	if !orihttp.ParseJSONBody(w, r, &request) {
		return
	}

	if request.Version == "" {
		orihttp.BadRequest(w, "Version is required")
		return
	}

	filePath, err := h.updateManager.DownloadUpdate(request.Version)
	if err != nil {
		logger.Error("Error downloading update", logger.Fields{"error": err})
		orihttp.InternalError(w, "Failed to download update")
		return
	}

	message := "Update downloaded successfully. Please restart ori-agent to use the new version."
	if request.AutoRestart {
		message = "Update downloaded successfully. Restarting application..."
	}

	response := map[string]interface{}{
		"success":     true,
		"version":     request.Version,
		"filePath":    filePath,
		"message":     message,
		"autoRestart": request.AutoRestart,
	}

	orihttp.WriteJSON(w, response)

	if request.AutoRestart {
		go func() {
			// Wait a bit to ensure response is sent
			time.Sleep(1 * time.Second)
			h.updateManager.RestartApplication()
		}()
	}
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
