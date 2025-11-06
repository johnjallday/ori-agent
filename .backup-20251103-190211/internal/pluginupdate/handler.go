package pluginupdate

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/health"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/pluginapi"
)

// Handler handles plugin update HTTP requests
type Handler struct {
	store         store.Store
	updater       *Updater
	pluginReg     *types.PluginRegistry
	healthChecker *health.Checker
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

// HandleUpdatePlugin handles requests to update a specific plugin
// POST /api/plugins/{name}/update
func (h *Handler) HandleUpdatePlugin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract plugin name from URL path
	// Path format: /api/plugins/{name}/update
	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) < 3 {
		http.Error(w, "Invalid URL path", http.StatusBadRequest)
		return
	}
	pluginName := pathParts[2]

	log.Printf("Update request for plugin: %s", pluginName)

	// Get current agent (assuming single agent for now, or get from query param)
	agentNames, _ := h.store.ListAgents()
	if len(agentNames) == 0 {
		http.Error(w, "No agents found", http.StatusInternalServerError)
		return
	}

	// Find the plugin in agents
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
		http.Error(w, fmt.Sprintf("Plugin %s not found in any agent", pluginName), http.StatusNotFound)
		return
	}

	// Find plugin in registry
	if h.pluginReg == nil {
		http.Error(w, "Plugin registry not loaded", http.StatusInternalServerError)
		return
	}

	var registryEntry *types.PluginRegistryEntry
	for i, p := range h.pluginReg.Plugins {
		if p.Name == pluginName {
			registryEntry = &h.pluginReg.Plugins[i]
			break
		}
	}

	if registryEntry == nil {
		http.Error(w, fmt.Sprintf("Plugin %s not found in registry", pluginName), http.StatusNotFound)
		return
	}

	// Check if update is needed
	if currentVersion == registryEntry.Version {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"message": fmt.Sprintf("Plugin %s is already at version %s", pluginName, currentVersion),
		})
		return
	}

	// Perform update
	result := h.updater.UpdatePlugin(pluginName, currentPath, *registryEntry, currentVersion)

	w.Header().Set("Content-Type", "application/json")
	if result.Success {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusInternalServerError)
	}
	json.NewEncoder(w).Encode(result)
}

// HandleListBackups lists all plugin backups
// GET /api/plugins/backups
func (h *Handler) HandleListBackups(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	backups, err := h.updater.ListBackups()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to list backups: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"backups": backups,
		"count":   len(backups),
	})
}

// HandleCleanBackups cleans old plugin backups
// POST /api/plugins/backups/clean
func (h *Handler) HandleCleanBackups(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		MaxAgeDays int `json:"max_age_days"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.MaxAgeDays <= 0 {
		req.MaxAgeDays = 30 // Default to 30 days
	}

	maxAge := time.Duration(req.MaxAgeDays) * 24 * time.Hour
	removed, err := h.updater.CleanOldBackups(maxAge)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to clean backups: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"removed": removed,
		"message": fmt.Sprintf("Removed %d old backup(s)", removed),
	})
}

// HandleRollbackPlugin rolls back a plugin to a previous backup
// POST /api/plugins/{name}/rollback
func (h *Handler) HandleRollbackPlugin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract plugin name from URL path
	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) < 3 {
		http.Error(w, "Invalid URL path", http.StatusBadRequest)
		return
	}
	pluginName := pathParts[2]

	var req struct {
		BackupPath string `json:"backup_path"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.BackupPath == "" {
		http.Error(w, "backup_path is required", http.StatusBadRequest)
		return
	}

	// Find current plugin path
	agentNames, _ := h.store.ListAgents()
	if len(agentNames) == 0 {
		http.Error(w, "No agents found", http.StatusInternalServerError)
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
		http.Error(w, fmt.Sprintf("Plugin %s not found", pluginName), http.StatusNotFound)
		return
	}

	// Perform rollback
	if err := h.updater.rollbackPlugin(req.BackupPath, currentPath); err != nil {
		http.Error(w, fmt.Sprintf("Rollback failed: %v", err), http.StatusInternalServerError)
		return
	}

	log.Printf("✅ Rolled back %s to backup: %s", pluginName, req.BackupPath)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Successfully rolled back %s", pluginName),
	})
}

// HandleCheckUpdates checks for available updates for all plugins
// GET /api/plugins/check-updates
func (h *Handler) HandleCheckUpdates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.pluginReg == nil {
		http.Error(w, "Plugin registry not loaded", http.StatusInternalServerError)
		return
	}

	updates := []map[string]interface{}{}

	// Get all agents and their plugins
	agentNames, _ := h.store.ListAgents()

	// Track which plugins we've checked (to avoid duplicates)
	checkedPlugins := make(map[string]bool)

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
			for _, registryEntry := range h.pluginReg.Plugins {
				if registryEntry.Name == pluginName {
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
					break
				}
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"updates": updates,
		"count":   len(updates),
	})
}
