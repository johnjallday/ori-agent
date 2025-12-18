package pluginhttp

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
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
		orihttp.RespondMethodNotAllowed(w)
		return
	}

	// Extract plugin name from URL
	pluginName := h.extractPluginName(r.URL.Path)
	if pluginName == "" {
		orihttp.RespondBadRequest(w, "Plugin name required")
		return
	}

	// Parse request body
	var rollbackReq struct {
		Version string `json:"version"`
	}

	if err := json.NewDecoder(r.Body).Decode(&rollbackReq); err != nil {
		orihttp.RespondBadRequest(w, fmt.Sprintf("Invalid request body: %v", err))
		return
	}

	if rollbackReq.Version == "" {
		orihttp.RespondBadRequest(w, "Version required")
		return
	}

	// Get plugin from registry to find current path
	plugin, err := h.RegistryManager.GetPluginByName(pluginName)
	if err != nil {
		orihttp.RespondNotFound(w, fmt.Sprintf("Plugin not found: %v", err))
		return
	}

	if plugin.Path == "" {
		orihttp.RespondBadRequest(w, "Plugin path not found")
		return
	}

	// Perform rollback
	err = h.VersionManager.RollbackToVersion(pluginName, rollbackReq.Version, plugin.Path)
	if err != nil {
		orihttp.RespondInternalError(w, fmt.Sprintf("Rollback failed: %v", err))
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
