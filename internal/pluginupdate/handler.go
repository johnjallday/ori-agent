package pluginupdate

import (
	"encoding/json"
	"fmt"

	"net/http"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/health"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/pluginupdateservice"
	"github.com/johnjallday/ori-agent/internal/registry"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/pluginapi"
)

// Handler handles plugin update HTTP requests
type Handler struct {
	store         store.Store
	updater       *Updater
	pluginReg     *types.PluginRegistry
	registryMgr   *registry.Manager
	healthChecker *health.Checker
	updateService *pluginupdateservice.Service
}

// NewHandler creates a new plugin update handler
func NewHandler(st store.Store, healthChecker *health.Checker) *Handler {
	return &Handler{
		store:         st,
		updater:       NewUpdater(healthChecker),
		healthChecker: healthChecker,
	}
}

// SetPluginRegistry sets the plugin registry for update lookups
func (h *Handler) SetPluginRegistry(reg *types.PluginRegistry) {
	h.pluginReg = reg
}

// SetRegistryManager sets the registry manager for refreshing registry data.
func (h *Handler) SetRegistryManager(mgr *registry.Manager) {
	h.registryMgr = mgr
}

// SetUpdateService sets the plugin update service.
func (h *Handler) SetUpdateService(svc *pluginupdateservice.Service) {
	h.updateService = svc
}

// HandleUpdatePlugin handles requests to update a specific plugin
// POST /api/plugins/{name}/update
func (h *Handler) HandleUpdatePlugin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.

				// Extract plugin name from URL path
				// Path format: /api/plugins/{name}/update
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) < 3 {
		orihttp.BadRequest(w, "Invalid URL path")
		return
	}
	pluginName := pathParts[2]

	logger.Debug("Update request for plugin", logger.Fields{"plugin": pluginName})

	// Get current agent (assuming single agent for now, or get from query param)
	agentNames, _ := h.store.ListAgents()
	if len(agentNames) == 0 {
		if err := orihttp.RespondInternalError(w, "No agents found"); err != nil {
			logger.

				// Find the plugin in agents
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	var currentPath string
	var currentVersion string
	var found bool

	for _, agentName := range agentNames {
		agent, ok := h.store.GetAgent(agentName)
		if !ok {
			continue
		}

		if lp, exists := agent.Plugins[pluginName]; exists {
			currentPath = lp.Path

			// Get current version from plugin
			if lp.Tool != nil {
				if versionedTool, ok := lp.Tool.(pluginapi.VersionedTool); ok {
					currentVersion = versionedTool.Version()
				}
			}
			found = true
			break
		}
	}

	if !found {
		if err := orihttp.RespondNotFound(w, fmt.Sprintf("Plugin %s not found in any agent", pluginName)); err != nil {
			logger.

				// Find plugin in registry
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if h.registryMgr != nil && h.pluginReg != nil {
		if reg, _, err := h.registryMgr.Load(); err == nil {
			*h.pluginReg = reg
		} else {
			logger.Warn("Failed to refresh plugin registry for update request", logger.Fields{"error": err})
		}
	}

	if h.pluginReg == nil {
		orihttp.InternalError(w, "Plugin registry not loaded")
		return
	}

	var registryEntry *types.PluginRegistryEntry
	normalizedName := registry.NormalizePluginNameForLookup(pluginName)
	for i, p := range h.pluginReg.Plugins {
		if registry.NormalizePluginNameForLookup(p.Name) == normalizedName {
			registryEntry = &h.pluginReg.Plugins[i]
			break
		}
	}

	if registryEntry == nil {
		if err := orihttp.RespondNotFound(w, fmt.Sprintf("Plugin %s not found in registry", pluginName)); err != nil {
			logger.

				// Check if update is needed
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if currentVersion == registryEntry.Version {
		if err := json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"message": fmt.Sprintf("Plugin %s is already at version %s", pluginName, currentVersion),
		}); err != nil {
			logger.Error("Failed to encode response", logger.Fields{"response": err})
		}
		return
	}

	// Perform update
	result := h.updater.UpdatePlugin(pluginName, currentPath, *registryEntry, currentVersion)
	if result.Success && h.updateService != nil {
		if err := h.updateService.CheckNow(); err != nil {
			logger.Warn("Failed to refresh plugin update cache after update", logger.Fields{"error": err})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if result.Success {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusInternalServerError)
	}
	if err := json.NewEncoder(w).Encode(result); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// HandleListBackups lists all plugin backups
// GET /api/plugins/backups
func (h *Handler) HandleListBackups(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	backups, err := h.updater.ListBackups()
	if err != nil {
		if err := orihttp.RespondInternalError(w, fmt.Sprintf("Failed to list backups: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"backups": backups,
		"count":   len(backups),
	}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// HandleCleanBackups cleans old plugin backups
// POST /api/plugins/backups/clean
func (h *Handler) HandleCleanBackups(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	var req struct {
		MaxAgeDays int `json:"max_age_days"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if err := orihttp.RespondBadRequest(w, "Invalid request body"); err != nil {
			logger.Error("Failed to write response", logger.Fields{

				// Default to 30 days
				"error": err})
		}
		return
	}

	if req.MaxAgeDays <= 0 {
		req.MaxAgeDays = 30
	}

	maxAge := time.Duration(req.MaxAgeDays) * 24 * time.Hour
	removed, err := h.updater.CleanOldBackups(maxAge)
	if err != nil {
		if err := orihttp.RespondInternalError(w, fmt.Sprintf("Failed to clean backups: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"removed": removed,
		"message": fmt.Sprintf("Removed %d old backup(s)", removed),
	}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// HandleRollbackPlugin rolls back a plugin to a previous backup
// POST /api/plugins/{name}/rollback
func (h *Handler) HandleRollbackPlugin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.

				// Extract plugin name from URL path
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) < 3 {
		orihttp.BadRequest(w, "Invalid URL path")
		return
	}
	pluginName := pathParts[2]

	var req struct {
		BackupPath string `json:"backup_path"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		orihttp.BadRequest(w, "Invalid request body")
		return
	}

	if req.BackupPath == "" {
		if err := orihttp.RespondBadRequest(w, "backup_path is required"); err != nil {
			logger.

				// Find current plugin path
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	agentNames, _ := h.store.ListAgents()
	if len(agentNames) == 0 {
		orihttp.InternalError(w, "No agents found")
		return
	}

	var currentPath string
	var found bool

	for _, agentName := range agentNames {
		agent, ok := h.store.GetAgent(agentName)
		if !ok {
			continue
		}

		if lp, exists := agent.Plugins[pluginName]; exists {
			currentPath = lp.Path
			found = true
			break
		}
	}

	if !found {
		if err := orihttp.RespondNotFound(w, fmt.Sprintf("Plugin %s not found", pluginName)); err != nil {
			logger.

				// Perform rollback
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if err := h.updater.rollbackPlugin(req.BackupPath, currentPath); err != nil {
		if err := orihttp.RespondInternalError(w, fmt.Sprintf("Rollback failed: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	logger.Info("Rolled back to backup", logger.Fields{"pluginName": pluginName, "backuppath": req.BackupPath})

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Successfully rolled back %s", pluginName),
	}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// HandleCheckUpdates checks for available updates for all plugins
// GET /api/plugins/check-updates
func (h *Handler) HandleCheckUpdates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	if h.registryMgr != nil && h.pluginReg != nil {
		if reg, _, err := h.registryMgr.Load(); err == nil {
			*h.pluginReg = reg
		} else {
			logger.Warn("Failed to refresh plugin registry for update check", logger.Fields{"error": err})
		}
	}

	if h.pluginReg == nil {
		if err := orihttp.RespondInternalError(w, "Plugin registry not loaded"); err != nil {
			logger.Error("Failed to write response", logger.

				// Get all agents and their plugins
				Fields{"error": err})
		}
		return
	}

	updates := []map[string]interface{}{}

	agentNames, _ := h.store.ListAgents()

	// Track which plugins we've checked (to avoid duplicates)
	checkedPlugins := make(map[string]bool)
	registryIndex := make(map[string]types.PluginRegistryEntry, len(h.pluginReg.Plugins))
	for _, entry := range h.pluginReg.Plugins {
		registryIndex[registry.NormalizePluginNameForLookup(entry.Name)] = entry
	}

	for _, agentName := range agentNames {
		agent, ok := h.store.GetAgent(agentName)
		if !ok {
			continue
		}

		for pluginName, lp := range agent.Plugins {
			if checkedPlugins[pluginName] {
				continue
			}
			checkedPlugins[pluginName] = true

			// Get current version
			var currentVersion string
			if lp.Tool != nil {
				if versionedTool, ok := lp.Tool.(pluginapi.VersionedTool); ok {
					currentVersion = versionedTool.Version()
				}
			}

			// Find in registry
			registryEntry, exists := registryIndex[registry.NormalizePluginNameForLookup(pluginName)]
			if !exists {
				continue
			}

			if currentVersion != registryEntry.Version {
				isOlder, _ := health.IsVersionOlder(currentVersion, registryEntry.Version)
				if isOlder {
					updates = append(updates, map[string]interface{}{
						"plugin_name":     pluginName,
						"current_version": currentVersion,
						"latest_version":  registryEntry.Version,
						"auto_update":     registryEntry.AutoUpdate,
						"download_url":    registryEntry.DownloadURL,
					})
				}
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"updates": updates,
		"count":   len(updates),
	}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// HandleGetUpdateStatus returns cached plugin update status
// GET /api/plugins/updates/status
func (h *Handler) HandleGetUpdateStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	if h.updateService == nil {
		orihttp.InternalError(w, "Plugin update service not initialized")
		return
	}

	updates := h.updateService.GetAvailableUpdates()
	lastChecked := h.updateService.LastChecked()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"success":     true,
		"updates":     updates,
		"count":       len(updates),
		"lastChecked": lastChecked,
	}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}
