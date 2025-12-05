package pluginhttp

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/pluginmanager"
	"github.com/johnjallday/ori-agent/internal/registry"
	"github.com/johnjallday/ori-agent/internal/store"
)

// RollbackHandler handles plugin version rollback operations
type RollbackHandler struct {
	Store           store.Store
	VersionManager  *pluginmanager.VersionManager
	RegistryManager *registry.Manager
	Loader          ToolLoader
}

// NewRollbackHandler creates a new rollback handler
func NewRollbackHandler(
	st store.Store,
	verMgr *pluginmanager.VersionManager,
	regMgr *registry.Manager,
	loader ToolLoader,
) *RollbackHandler {
	return &RollbackHandler{
		Store:           st,
		VersionManager:  verMgr,
		RegistryManager: regMgr,
		Loader:          loader,
	}
}

// HandleRollbackPlugin rolls back a plugin to a previous version
// POST /api/plugins/:name/rollback
// Request body: { "version": "1.0.0" }
func (h *RollbackHandler) HandleRollbackPlugin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract plugin name from URL
	pluginName := h.extractPluginName(r.URL.Path)
	if pluginName == "" {
		http.Error(w, "Plugin name required", http.StatusBadRequest)
		return
	}

	// Parse request body
	var rollbackReq struct {
		Version string `json:"version"`
	}

	if err := json.NewDecoder(r.Body).Decode(&rollbackReq); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if rollbackReq.Version == "" {
		http.Error(w, "Version required", http.StatusBadRequest)
		return
	}

	// Get plugin from registry to find current path
	plugin, err := h.RegistryManager.GetPluginByName(pluginName)
	if err != nil {
		http.Error(w, fmt.Sprintf("Plugin not found: %v", err), http.StatusNotFound)
		return
	}

	if plugin.Path == "" {
		http.Error(w, "Plugin path not found", http.StatusBadRequest)
		return
	}

	// Perform rollback
	err = h.VersionManager.RollbackToVersion(pluginName, rollbackReq.Version, plugin.Path)
	if err != nil {
		http.Error(w, fmt.Sprintf("Rollback failed: %v", err), http.StatusInternalServerError)
		return
	}

	// The version manager has already replaced the binary at plugin.Path

	// Reload plugin if it's enabled in current agent
	_, currentAgent := h.Store.ListAgents()
	agent, ok := h.Store.GetAgent(currentAgent)
	if ok {
		if loadedPlugin, exists := agent.Plugins[pluginName]; exists {
			// Kill old plugin process
			if rpcPlugin, ok := loadedPlugin.Tool.(interface{ Kill() }); ok {
				rpcPlugin.Kill()
			}

			// Reload from disk
			newTool, err := h.Loader.Load(plugin.Path)
			if err == nil {
				loadedPlugin.Tool = newTool
				loadedPlugin.Definition = newTool.Definition()
				loadedPlugin.Version = rollbackReq.Version
				agent.Plugins[pluginName] = loadedPlugin

				// Save agent
				_ = h.Store.SetAgent(currentAgent, agent)
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Plugin %s rolled back to version %s", pluginName, rollbackReq.Version),
		"version": rollbackReq.Version,
	}); err != nil {
		log.Printf("Failed to encode response: %v", err)
	}
}

func (h *RollbackHandler) extractPluginName(path string) string {
	// Remove /api/plugins/ prefix
	pluginPath := strings.TrimPrefix(path, "/api/plugins/")

	// Split by / and take first component (plugin name)
	parts := strings.Split(pluginPath, "/")
	if len(parts) > 0 {
		return parts[0]
	}
	return ""
}
