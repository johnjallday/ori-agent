package updatehttp

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"

	"github.com/johnjallday/dolphin-agent/internal/updatemanager"
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
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse query parameters
	includePrerelease := r.URL.Query().Get("prerelease") == "true"

	updateInfo, err := h.updateManager.CheckUpdates(includePrerelease)
	if err != nil {
		log.Printf("Error checking for updates: %v", err)
		http.Error(w, "Failed to check for updates", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(updateInfo); err != nil {
		log.Printf("Error encoding update info: %v", err)
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}
}

// ListReleasesHandler handles GET /api/updates/releases
func (h *Handler) ListReleasesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse query parameters
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
		log.Printf("Error listing releases: %v", err)
		http.Error(w, "Failed to list releases", http.StatusInternalServerError)
		return
	}

	response := map[string]interface{}{
		"releases": releases,
		"count":    len(releases),
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("Error encoding releases: %v", err)
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}
}

// DownloadUpdateHandler handles POST /api/updates/download
func (h *Handler) DownloadUpdateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var request struct {
		Version string `json:"version"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if request.Version == "" {
		http.Error(w, "Version is required", http.StatusBadRequest)
		return
	}

	filePath, err := h.updateManager.DownloadUpdate(request.Version)
	if err != nil {
		log.Printf("Error downloading update: %v", err)
		http.Error(w, "Failed to download update", http.StatusInternalServerError)
		return
	}

	response := map[string]interface{}{
		"success":  true,
		"version":  request.Version,
		"filePath": filePath,
		"message":  "Update downloaded successfully. Manual installation required.",
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("Error encoding download response: %v", err)
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}
}

// GetVersionHandler handles GET /api/updates/version
func (h *Handler) GetVersionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	versionInfo := h.updateManager.GetCurrentVersion()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(versionInfo); err != nil {
		log.Printf("Error encoding version info: %v", err)
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}
}