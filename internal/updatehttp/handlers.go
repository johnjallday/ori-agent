package updatehttp

import (
	"encoding/json"
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
		if encodeErr := orihttp.RespondMethodNotAllowed(w); encodeErr != nil {
			logger.Error("Failed to write method not allowed response", logger.Fields{"error": encodeErr})
		}
		return
	}

	// Parse query parameters
	includePrerelease := r.URL.Query().Get("prerelease") == "true"

	updateInfo, err := h.updateManager.CheckUpdates(includePrerelease)
	if err != nil {
		logger.Warn("Update check unavailable", logger.Fields{"error": err})
		if encodeErr := orihttp.RespondServiceUnavailable(w, "Update check unavailable. Provide GITHUB_TOKEN/GH_TOKEN to increase GitHub API limits."); encodeErr != nil {
			logger.Error("Failed to write service unavailable response", logger.Fields{"error": encodeErr})
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(updateInfo); err != nil {
		logger.Error("Error encoding update info", logger.Fields{"error": err})
		if err := orihttp.RespondInternalError(w, "Failed to encode response"); err != nil {
			logger.

				// ListReleasesHandler handles GET /api/updates/releases
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}
}

func (h *Handler) ListReleasesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.

				// Parse query parameters
				Error("Failed to write response", logger.Fields{"error": err})
		}
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
		if err := orihttp.RespondInternalError(w, "Failed to list releases"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	response := map[string]interface{}{
		"releases": releases,
		"count":    len(releases),
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		logger.Error("Error encoding releases", logger.Fields{"error": err})
		if err := orihttp.RespondInternalError(w, "Failed to encode response"); err != nil {
			logger.

				// DownloadUpdateHandler handles POST /api/updates/download
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}
}

func (h *Handler) DownloadUpdateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	var request struct {
		Version     string `json:"version"`
		AutoRestart bool   `json:"autoRestart"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		if err := orihttp.RespondBadRequest(w, "Invalid request body"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if request.Version == "" {
		if err := orihttp.RespondBadRequest(w, "Version is required"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	filePath, err := h.updateManager.DownloadUpdate(request.Version)
	if err != nil {
		logger.Error("Error downloading update", logger.Fields{"error": err})
		if err := orihttp.RespondInternalError(w, "Failed to download update"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
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

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		logger.Error("Error encoding download response", logger.Fields{"response": err})
		if err := orihttp.RespondInternalError(w, "Failed to encode response"); err != nil {
			logger.

				// If auto-restart is requested, trigger restart after response is sent
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

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
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	versionInfo := h.updateManager.GetCurrentVersion()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(versionInfo); err != nil {
		logger.Error("Error encoding version info", logger.Fields{"error": err})
		if err := orihttp.RespondInternalError(w, "Failed to encode response"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}
}
