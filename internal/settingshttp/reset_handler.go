package settingshttp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/onboarding"
)

const (
	// maxResetRequestSize limits the request body to prevent DoS
	maxResetRequestSize = 1024 // 1KB
)

// ResetRequest defines the selective reset options
type ResetRequest struct {
	Settings     bool   `json:"settings"`     // Reset settings.json
	Agents       bool   `json:"agents"`       // Reset agents.json and agents/ directory
	Sessions     bool   `json:"sessions"`     // Reset sessions.db
	Plugins      bool   `json:"plugins"`      // Reset local_plugin_registry.json and uploaded_plugins/
	Onboarding   bool   `json:"onboarding"`   // Reset app_state.json (onboarding)
	Confirmation string `json:"confirmation"` // Must be "RESET" to confirm
}

// ResetResponse contains the result of the reset operation
type ResetResponse struct {
	Success         bool     `json:"success"`
	ResetItems      []string `json:"reset_items"`
	Errors          []string `json:"errors,omitempty"`
	RequiresRestart bool     `json:"requires_restart"`
}

// ResetHandler handles application reset operations
type ResetHandler struct {
	onboardingMgr *onboarding.Manager
	dataDir       string
}

// NewResetHandler creates a new ResetHandler
func NewResetHandler(onboardingMgr *onboarding.Manager, dataDir string) *ResetHandler {
	return &ResetHandler{
		onboardingMgr: onboardingMgr,
		dataDir:       dataDir,
	}
}

// HandleReset handles POST /api/reset for selective app reset.
// NOTE: This endpoint is intentionally accessible without additional authentication
// as the server runs locally. If the server is ever exposed to a network, add
// authentication middleware.
func (h *ResetHandler) HandleReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.RespondMethodNotAllowed(w)
		return
	}

	// CSRF protection: require XMLHttpRequest header
	if r.Header.Get("X-Requested-With") != "XMLHttpRequest" {
		orihttp.RespondBadRequest(w, "Missing required header")
		return
	}

	// Limit request body size to prevent DoS
	r.Body = http.MaxBytesReader(w, r.Body, maxResetRequestSize)

	var req ResetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		orihttp.RespondBadRequest(w, "Invalid request body: "+err.Error())
		return
	}

	// Server-side confirmation validation (defense in depth)
	if req.Confirmation != "RESET" {
		orihttp.RespondBadRequest(w, "Confirmation required: type RESET to confirm")
		return
	}

	// Validate that at least one option is selected
	if !req.Settings && !req.Agents && !req.Sessions && !req.Plugins && !req.Onboarding {
		orihttp.RespondBadRequest(w, "At least one reset option must be selected")
		return
	}

	response := ResetResponse{
		Success:         true,
		ResetItems:      []string{},
		Errors:          []string{},
		RequiresRestart: true, // Reset always requires restart
	}

	// Reset settings
	if req.Settings {
		if err := h.resetSettings(); err != nil {
			response.Errors = append(response.Errors, "settings: "+err.Error())
			logger.Error("Failed to reset settings", logger.Fields{"error": err})
		} else {
			response.ResetItems = append(response.ResetItems, "settings")
		}
	}

	// Reset agents
	if req.Agents {
		if err := h.resetAgents(); err != nil {
			response.Errors = append(response.Errors, "agents: "+err.Error())
			logger.Error("Failed to reset agents", logger.Fields{"error": err})
		} else {
			response.ResetItems = append(response.ResetItems, "agents")
		}
	}

	// Reset sessions
	if req.Sessions {
		if err := h.resetSessions(); err != nil {
			response.Errors = append(response.Errors, "sessions: "+err.Error())
			logger.Error("Failed to reset sessions", logger.Fields{"error": err})
		} else {
			response.ResetItems = append(response.ResetItems, "sessions")
		}
	}

	// Reset plugins
	if req.Plugins {
		if err := h.resetPlugins(); err != nil {
			response.Errors = append(response.Errors, "plugins: "+err.Error())
			logger.Error("Failed to reset plugins", logger.Fields{"error": err})
		} else {
			response.ResetItems = append(response.ResetItems, "plugins")
		}
	}

	// Reset onboarding (must be last so user goes through setup again)
	if req.Onboarding {
		if err := h.onboardingMgr.ResetOnboarding(); err != nil {
			response.Errors = append(response.Errors, "onboarding: "+err.Error())
			logger.Error("Failed to reset onboarding", logger.Fields{"error": err})
		} else {
			response.ResetItems = append(response.ResetItems, "onboarding")
		}
	}

	// If there were errors, mark as partial success
	if len(response.Errors) > 0 {
		response.Success = len(response.ResetItems) > 0 // At least some items reset
	}

	logger.Info("App reset completed", logger.Fields{
		"reset_items": response.ResetItems,
		"errors":      response.Errors,
	})

	orihttp.WriteJSON(w, response)
}

// validatePath ensures the target path is within the data directory
func (h *ResetHandler) validatePath(targetPath string) error {
	absTarget, err := filepath.Abs(targetPath)
	if err != nil {
		return err
	}
	absDataDir, err := filepath.Abs(h.dataDir)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(absTarget, absDataDir+string(filepath.Separator)) && absTarget != absDataDir {
		return fmt.Errorf("path escapes data directory: %s", targetPath)
	}
	return nil
}

// resetSettings removes settings.json
func (h *ResetHandler) resetSettings() error {
	settingsPath := filepath.Join(h.dataDir, "settings.json")
	if err := h.validatePath(settingsPath); err != nil {
		return err
	}
	if err := os.Remove(settingsPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// resetAgents removes agents.json and the agents/ directory
func (h *ResetHandler) resetAgents() error {
	// Remove agents.json
	agentsJsonPath := filepath.Join(h.dataDir, "agents.json")
	if err := h.validatePath(agentsJsonPath); err != nil {
		return err
	}
	if err := os.Remove(agentsJsonPath); err != nil && !os.IsNotExist(err) {
		return err
	}

	// Remove agents/ directory
	agentsDir := filepath.Join(h.dataDir, "agents")
	if err := h.validatePath(agentsDir); err != nil {
		return err
	}
	if err := os.RemoveAll(agentsDir); err != nil {
		return err
	}

	return nil
}

// resetSessions removes sessions.db and related SQLite files.
// NOTE: The database file may be in use by the running server. The reset will
// mark files for deletion, but full cleanup requires a server restart.
// On Windows, this may fail if the database is locked.
func (h *ResetHandler) resetSessions() error {
	// Remove sessions.db
	sessionsPath := filepath.Join(h.dataDir, "sessions.db")
	if err := h.validatePath(sessionsPath); err != nil {
		return err
	}
	if err := os.Remove(sessionsPath); err != nil && !os.IsNotExist(err) {
		return err
	}

	// Remove SQLite WAL files (best effort - may be locked)
	walPath := filepath.Join(h.dataDir, "sessions.db-wal")
	if err := os.Remove(walPath); err != nil && !os.IsNotExist(err) {
		logger.Debug("WAL file may be in use", logger.Fields{"path": walPath, "error": err})
	}

	shmPath := filepath.Join(h.dataDir, "sessions.db-shm")
	if err := os.Remove(shmPath); err != nil && !os.IsNotExist(err) {
		logger.Debug("SHM file may be in use", logger.Fields{"path": shmPath, "error": err})
	}

	return nil
}

// resetPlugins removes local_plugin_registry.json and uploaded_plugins/ directory
func (h *ResetHandler) resetPlugins() error {
	// Remove local_plugin_registry.json
	registryPath := filepath.Join(h.dataDir, "local_plugin_registry.json")
	if err := h.validatePath(registryPath); err != nil {
		return err
	}
	if err := os.Remove(registryPath); err != nil && !os.IsNotExist(err) {
		return err
	}

	// Remove uploaded_plugins/ directory
	uploadedPluginsDir := filepath.Join(h.dataDir, "uploaded_plugins")
	if err := h.validatePath(uploadedPluginsDir); err != nil {
		return err
	}
	if err := os.RemoveAll(uploadedPluginsDir); err != nil {
		return err
	}

	// Also remove plugin_cache/ directory for a clean slate
	pluginCacheDir := filepath.Join(h.dataDir, "plugin_cache")
	if err := h.validatePath(pluginCacheDir); err != nil {
		return err
	}
	if err := os.RemoveAll(pluginCacheDir); err != nil && !os.IsNotExist(err) {
		return err
	}

	return nil
}

// GetResetPreview returns information about what would be reset
func (h *ResetHandler) GetResetPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.RespondMethodNotAllowed(w)
		return
	}

	preview := map[string]interface{}{
		"settings": map[string]interface{}{
			"description": "API keys and global configuration",
			"files":       []string{"settings.json"},
		},
		"agents": map[string]interface{}{
			"description": "All agents and their configurations",
			"files":       []string{"agents.json", "agents/"},
		},
		"sessions": map[string]interface{}{
			"description": "All chat sessions and message history",
			"files":       []string{"sessions.db"},
		},
		"plugins": map[string]interface{}{
			"description": "Uploaded plugins and plugin registry",
			"files":       []string{"local_plugin_registry.json", "uploaded_plugins/", "plugin_cache/"},
		},
		"onboarding": map[string]interface{}{
			"description": "Onboarding state and app preferences",
			"files":       []string{"app_state.json"},
		},
	}

	orihttp.WriteJSON(w, preview)
}
