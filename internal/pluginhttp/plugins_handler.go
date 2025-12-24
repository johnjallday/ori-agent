package pluginhttp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/pluginloader"
	"github.com/johnjallday/ori-agent/internal/pluginmanager"
	"github.com/johnjallday/ori-agent/internal/pluginupdateservice"
	"github.com/johnjallday/ori-agent/internal/registry"
	"github.com/johnjallday/ori-agent/internal/store"
	internaltags "github.com/johnjallday/ori-agent/internal/tags"
	"github.com/johnjallday/ori-agent/internal/types"
)

// PluginsPageHandler handles endpoints for the dedicated plugins management page

type PluginsPageHandler struct {
	Store             store.Store
	RegistryManager   *registry.Manager
	CategoryManager   *pluginmanager.CategoryManager
	PermissionManager *pluginmanager.PermissionManager
	Loader            ToolLoader
	UpdateService     *pluginupdateservice.Service
}

// NewPluginsPageHandler creates a new handler for the plugins page
func NewPluginsPageHandler(
	st store.Store,
	regMgr *registry.Manager,
	catMgr *pluginmanager.CategoryManager,
	permMgr *pluginmanager.PermissionManager,
	loader ToolLoader,
) *PluginsPageHandler {
	return &PluginsPageHandler{
		Store:             st,
		RegistryManager:   regMgr,
		CategoryManager:   catMgr,
		PermissionManager: permMgr,
		Loader:            loader,
	}
}

// SetUpdateService injects the plugin update service.
func (h *PluginsPageHandler) SetUpdateService(svc *pluginupdateservice.Service) {
	h.UpdateService = svc
}

// HandleListPlugins returns a list of all plugins with their status, categories, and permissions
// GET /api/plugins
func (h *PluginsPageHandler) HandleListPlugins(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	tagFilter := strings.TrimSpace(r.URL.Query().Get("tag"))
	var normalizedTagFilter string
	if tagFilter != "" {
		normalizedTagFilter = internaltags.NormalizeTag(tagFilter)
		if err := internaltags.ValidateTag(normalizedTagFilter); err != nil {
			if err := orihttp.RespondBadRequest(w, fmt.Sprintf("Invalid tag filter: %v", err)); err != nil {
				logger.Error(

					// Load local registry to get all plugins
					"Failed to write response", logger.Fields{"error": err})
			}
			return
		}
	}

	localReg, err := h.RegistryManager.LoadLocal()
	if err != nil {
		if err := orihttp.RespondInternalError(w, fmt.Sprintf("Failed to load registry: %v", err)); err != nil {
			logger.

				// Get current agent to check enabled plugins
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	_, currentAgent := h.Store.ListAgents()
	agent, agentExists := h.Store.GetAgent(currentAgent)

	// Build response with extended plugin information
	plugins := make([]map[string]interface{}, 0, len(localReg.Plugins))

	for _, plugin := range localReg.Plugins {
		tags := pluginAllTags(&plugin)
		if normalizedTagFilter != "" && !pluginHasTag(&plugin, normalizedTagFilter) {
			continue
		}

		// Check if plugin is enabled
		isEnabled := false
		var loadedPlugin *types.LoadedPlugin
		if agentExists {
			if lp, exists := agent.Plugins[plugin.Name]; exists {
				isEnabled = true
				loadedPlugin = &lp
			}
		}

		// Determine plugin status
		status := h.getPluginStatus(&plugin, isEnabled)

		// Get plugin agents (plugins that provide agents)
		agents := h.getPluginAgents(&plugin, loadedPlugin)

		pluginInfo := map[string]interface{}{
			"name":                 plugin.Name,
			"description":          plugin.Description,
			"version":              plugin.Version,
			"tags":                 tags,
			"category":             plugin.Category,
			"status":               status,
			"enabled":              plugin.Enabled,
			"permissions":          plugin.Permissions,
			"permissions_approved": plugin.PermissionsApproved,
			"health_status":        plugin.HealthStatus,
			"last_used":            plugin.LastUsed,
			"agents":               agents,
			"metadata":             plugin.Metadata,
		}
		if h.UpdateService != nil {
			pluginInfo["needs_update"] = h.UpdateService.HasUpdateForPlugin(plugin.Name)
		} else {
			pluginInfo["needs_update"] = false
		}

		plugins = append(plugins, pluginInfo)
	}

	response := map[string]interface{}{
		"plugins": plugins,
	}

	w.Header().Set("Content-Type", "application/json")
	orihttp.WriteJSON(w, response)
}

// HandleListPluginTags lists all unique tags across plugins.
// GET /api/plugins/tags
func (h *PluginsPageHandler) HandleListPluginTags(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	localReg, err := h.RegistryManager.LoadLocal()
	if err != nil {
		if err := orihttp.RespondInternalError(w, fmt.Sprintf("Failed to load registry: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	seen := make(map[string]struct{})
	for _, plugin := range localReg.Plugins {
		for _, raw := range pluginAllTags(&plugin) {
			normalized := internaltags.NormalizeTag(raw)
			if err := internaltags.ValidateTag(normalized); err != nil {
				continue
			}
			seen[normalized] = struct{}{}
		}
	}

	out := make([]string, 0, len(seen))
	for tag := range seen {
		out = append(out, tag)
	}
	sort.Strings(out)

	w.Header().Set("Content-Type", "application/json")
	orihttp.WriteJSON(w, map[string]interface{}{"tags": out})
}

// HandleListPluginsByTag returns plugins filtered by tag.
// GET /api/plugins/tags/:tag
func (h *PluginsPageHandler) HandleListPluginsByTag(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	rawTag := strings.TrimPrefix(r.URL.Path, "/api/plugins/tags/")
	rawTag = strings.Split(rawTag, "/")[0]
	normalized := internaltags.NormalizeTag(rawTag)
	if err := internaltags.ValidateTag(normalized); err != nil {
		if err := orihttp.RespondBadRequest(w, fmt.Sprintf("Invalid tag: %v", err)); err != nil {
			logger.

				// Reuse list handler by injecting the tag filter via the query string.
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	q := r.URL.Query()
	q.Set("tag", normalized)
	r.URL.RawQuery = q.Encode()
	h.HandleListPlugins(w, r)
}

// HandleGetPluginDetails returns detailed information about a specific plugin
// GET /api/plugins/:name
func (h *PluginsPageHandler) HandleGetPluginDetails(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write method not allowed response", logger.Fields{"error": err})
		}
		return
	}

	// Extract plugin name from URL path
	pluginName := strings.TrimPrefix(r.URL.Path, "/api/plugins/")
	if pluginName == "" || pluginName == "/api/plugins/" {
		if err := orihttp.RespondBadRequest(w, "Plugin name required"); err != nil {
			logger.Error("Failed to write bad request response", logger.Fields{"error": err})
		}
		return
	}

	// Remove any trailing path components (like /details)
	pluginName = strings.Split(pluginName, "/")[0]

	// Get plugin from registry
	plugin, err := h.RegistryManager.GetPluginByName(pluginName)
	if err != nil {
		if encodeErr := orihttp.RespondNotFound(w, fmt.Sprintf("Plugin not found: %v", err)); encodeErr != nil {
			logger.Error("Failed to write not found response", logger.Fields{"error": encodeErr})
		}
		return
	}

	// Get current agent to check if plugin is loaded
	_, currentAgent := h.Store.ListAgents()
	agent, agentExists := h.Store.GetAgent(currentAgent)

	var loadedPlugin *types.LoadedPlugin
	var definition interface{}
	if agentExists {
		if lp, exists := agent.Plugins[pluginName]; exists {
			loadedPlugin = &lp
			definition = lp.Definition
		}
	}

	// Get permission details
	permissionEntry, _ := h.PermissionManager.GetPermissionEntry(pluginName)
	var permissions interface{}
	if permissionEntry != nil {
		permissions = permissionEntry
	}

	// Build detailed response
	details := map[string]interface{}{
		"name":                 plugin.Name,
		"description":          plugin.Description,
		"version":              plugin.Version,
		"tags":                 pluginAllTags(plugin),
		"category":             plugin.Category,
		"path":                 plugin.Path,
		"enabled":              plugin.Enabled,
		"permissions":          permissions,
		"permissions_approved": plugin.PermissionsApproved,
		"health_status":        plugin.HealthStatus,
		"last_used":            plugin.LastUsed,
		"version_history":      plugin.VersionHistory,
		"metadata":             plugin.Metadata,
		"definition":           definition,
		"agents":               h.getPluginAgents(plugin, loadedPlugin),
	}

	w.Header().Set("Content-Type", "application/json")
	orihttp.WriteJSON(w, details)
}

func pluginAllTags(plugin *types.PluginRegistryEntry) []string {
	if len(plugin.Tags) > 0 {
		return plugin.Tags
	}
	if plugin.Metadata != nil && len(plugin.Metadata.Tags) > 0 {
		return plugin.Metadata.Tags
	}
	return nil
}

func pluginHasTag(plugin *types.PluginRegistryEntry, normalizedTag string) bool {
	for _, raw := range pluginAllTags(plugin) {
		normalized := internaltags.NormalizeTag(raw)
		if normalized == normalizedTag {
			return true
		}
	}
	return false
}

// HandleEnablePlugin enables a plugin for the current agent
// POST /api/plugins/:name/enable
func (h *PluginsPageHandler) HandleEnablePlugin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	pluginName := h.extractPluginName(r.URL.Path)
	if pluginName == "" {
		if err := orihttp.RespondBadRequest(w, "Plugin name required"); err != nil {
			logger.

				// Get plugin from registry
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	plugin, err := h.RegistryManager.GetPluginByName(pluginName)
	if err != nil {
		if err := orihttp.RespondNotFound(w, fmt.Sprintf("Plugin not found: %v", err)); err != nil {
			logger.

				// Load the plugin
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}
	pluginKey := plugin.Name

	tool, err := h.Loader.Load(plugin.Path)
	if err != nil {
		if err := orihttp.RespondInternalError(w, fmt.Sprintf("Failed to load plugin: %v", err)); err != nil {
			logger.

				// Get current agent
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	_, currentAgent := h.Store.ListAgents()
	agent, ok := h.Store.GetAgent(currentAgent)
	if !ok {
		if err := orihttp.RespondInternalError(w, "Current agent not found"); err != nil {
			logger.

				// Add plugin to agent
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	agentSpecificStorePath := filepath.Join("agents", currentAgent, "config.json")
	if abs, err := filepath.Abs(agentSpecificStorePath); err == nil {
		agentSpecificStorePath = abs
	}
	pluginloader.SetAgentContext(tool, currentAgent, agentSpecificStorePath, "")

	if agent.Plugins == nil {
		agent.Plugins = make(map[string]types.LoadedPlugin)
	}

	supportsFiles, acceptedFileTypes := pluginloader.GetPluginFileSupport(tool)
	agent.Plugins[pluginKey] = types.LoadedPlugin{
		Tool:              tool,
		Definition:        tool.Definition(),
		Path:              plugin.Path,
		Version:           plugin.Version,
		SupportsFiles:     supportsFiles,
		AcceptedFileTypes: acceptedFileTypes,
	}

	// Save agent
	if err := h.Store.SetAgent(currentAgent, agent); err != nil {
		if err := orihttp.RespondInternalError(w, fmt.Sprintf("Failed to save agent: %v", err)); err != nil {
			logger.

				// Update registry status
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if err := h.RegistryManager.UpdatePluginStatus(pluginKey, true, "healthy"); err != nil {
		// Log error but don't fail the request
		fmt.Printf("Warning: Failed to update plugin status: %v\n", err)
	}

	// Update last used timestamp
	if err := h.RegistryManager.UpdatePluginLastUsed(pluginKey, time.Now()); err != nil {
		fmt.Printf("Warning: Failed to update last used: %v\n", err)
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Plugin %s enabled successfully", pluginKey),
	}); err != nil {
		fmt.Printf("Warning: Failed to encode response: %v\n", err)
	}
}

// HandleDisablePlugin disables a plugin for the current agent
// POST /api/plugins/:name/disable
func (h *PluginsPageHandler) HandleDisablePlugin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	pluginName := h.extractPluginName(r.URL.Path)
	if pluginName == "" {
		if err := orihttp.RespondBadRequest(w, "Plugin name required"); err != nil {
			logger.

				// Get current agent
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	_, currentAgent := h.Store.ListAgents()
	agent, ok := h.Store.GetAgent(currentAgent)
	if !ok {
		if err := orihttp.RespondInternalError(w, "Current agent not found"); err != nil {
			logger.

				// Remove plugin from agent
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	pluginKey := pluginName
	if entry, err := h.RegistryManager.GetPluginByName(pluginName); err == nil && entry != nil {
		pluginKey = entry.Name
	} else {
		normalized := registry.NormalizePluginNameForLookup(pluginName)
		for key := range agent.Plugins {
			if registry.NormalizePluginNameForLookup(key) == normalized {
				pluginKey = key
				break
			}
		}
	}

	delete(agent.Plugins, pluginKey)

	// Save agent
	if err := h.Store.SetAgent(currentAgent, agent); err != nil {
		if err := orihttp.RespondInternalError(w, fmt.Sprintf("Failed to save agent: %v", err)); err != nil {
			logger.

				// Update registry status
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if err := h.RegistryManager.UpdatePluginStatus(pluginKey, false, "inactive"); err != nil {
		fmt.Printf("Warning: Failed to update plugin status: %v\n", err)
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Plugin %s disabled successfully", pluginKey),
	}); err != nil {
		fmt.Printf("Warning: Failed to encode response: %v\n", err)
	}
}

// HandleUpdatePluginConfig updates configuration for a specific plugin
// PUT /api/plugins/:name/config
func (h *PluginsPageHandler) HandleUpdatePluginConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	pluginName := h.extractPluginName(r.URL.Path)
	if pluginName == "" {
		if err := orihttp.RespondBadRequest(w, "Plugin name required"); err != nil {
			logger.

				// Parse config from request body
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	var configReq struct {
		Config map[string]interface{} `json:"config"`
	}

	if err := json.NewDecoder(r.Body).Decode(&configReq); err != nil {
		if err := orihttp.RespondBadRequest(w, fmt.Sprintf("Invalid request body: %v", err)); err != nil {
			logger.

				// Get current agent
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	_, currentAgent := h.Store.ListAgents()

	// Create agent directory if it doesn't exist
	agentDir := filepath.Join("agents", currentAgent)
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		if err := orihttp.RespondInternalError(w, fmt.Sprintf("Failed to create agent directory: %v", err)); err != nil {
			logger.

				// Save config to plugin-specific settings file
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	settingsFileName := fmt.Sprintf("%s_settings.json", pluginName)
	settingsPath := filepath.Join(agentDir, settingsFileName)

	settingsData, err := json.MarshalIndent(configReq.Config, "", "  ")
	if err != nil {
		if err := orihttp.RespondInternalError(w, fmt.Sprintf("Failed to marshal config: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if err := os.WriteFile(settingsPath, settingsData, 0644); err != nil {
		if err := orihttp.RespondInternalError(w, fmt.Sprintf("Failed to write config: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	// Reload the plugin to pick up new config without server restart
	reloaded := false
	agent, ok := h.Store.GetAgent(currentAgent)
	if ok {
		if plugin, exists := agent.Plugins[pluginName]; exists {
			// Kill old plugin process if it's an RPC plugin
			if rpcPlugin, ok := plugin.Tool.(interface{ Kill() }); ok {
				rpcPlugin.Kill()
			}
			// Reload plugin from disk
			if newTool, err := h.Loader.Load(plugin.Path); err == nil {
				plugin.Tool = newTool
				plugin.Definition = newTool.Definition()
				agent.Plugins[pluginName] = plugin
				_ = h.Store.SetAgent(currentAgent, agent)
				reloaded = true
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"message":  fmt.Sprintf("Configuration updated for plugin %s", pluginName),
		"path":     settingsPath,
		"reloaded": reloaded,
	}); err != nil {
		fmt.Printf("Warning: Failed to encode response: %v\n", err)
	}
}

// HandleTestPlugin executes a test call to the plugin
// POST /api/plugins/:name/test
func (h *PluginsPageHandler) HandleTestPlugin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	pluginName := h.extractPluginName(r.URL.Path)
	if pluginName == "" {
		if err := orihttp.RespondBadRequest(w, "Plugin name required"); err != nil {
			logger.

				// Parse test arguments from request body
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	var testReq struct {
		Args string `json:"args"`
	}

	if err := json.NewDecoder(r.Body).Decode(&testReq); err != nil {
		if err := orihttp.RespondBadRequest(w, fmt.Sprintf("Invalid request body: %v", err)); err != nil {
			logger.

				// Get plugin from current agent
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	_, currentAgent := h.Store.ListAgents()
	agent, ok := h.Store.GetAgent(currentAgent)
	if !ok {
		if err := orihttp.RespondInternalError(w, "Current agent not found"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	plugin, exists := agent.Plugins[pluginName]
	if !exists {
		if err := orihttp.RespondBadRequest(w, "Plugin not enabled. Enable it first before testing."); err != nil {
			logger.

				// Execute test call
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	result, err := plugin.Tool.Call(r.Context(), testReq.Args)

	response := map[string]interface{}{
		"success": err == nil,
		"result":  result,
	}

	if err != nil {
		response["error"] = err.Error()
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		fmt.Printf("Error encoding response: %v\n", err)
	}
}

// HandleGetPluginLogs returns recent logs for a specific plugin
// GET /api/plugins/:name/logs
func (h *PluginsPageHandler) HandleGetPluginLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	pluginName := h.extractPluginName(r.URL.Path)
	if pluginName == "" {
		if err := orihttp.RespondBadRequest(w, "Plugin name required"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	// Placeholder logs - plugins currently don't emit structured logs.
	// To implement real log collection:
	// - Plugins would need to implement a LogProvider interface
	// - Server would capture plugin stdout/stderr during execution
	// - Logs would be stored with timestamps and aggregated here
	logs := []map[string]interface{}{
		{
			"timestamp": time.Now().Add(-5 * time.Minute).Format(time.RFC3339),
			"level":     "info",
			"message":   fmt.Sprintf("Plugin %s loaded successfully", pluginName),
		},
		{
			"timestamp": time.Now().Add(-2 * time.Minute).Format(time.RFC3339),
			"level":     "info",
			"message":   "Plugin initialized with config",
		},
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"logs": logs,
	}); err != nil {
		fmt.Printf("Error encoding logs response: %v\n", err)
	}
}

// HandleDeletePlugin removes a plugin from the system
// DELETE /api/plugins/:name
func (h *PluginsPageHandler) HandleDeletePlugin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	pluginName := h.extractPluginName(r.URL.Path)
	if pluginName == "" {
		if err := orihttp.RespondBadRequest(w, "Plugin name required"); err != nil {
			logger.

				// Get plugin to find its path
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	plugin, err := h.RegistryManager.GetPluginByName(pluginName)
	if err != nil {
		if err := orihttp.RespondNotFound(w, fmt.Sprintf("Plugin not found: %v", err)); err != nil {
			logger.

				// Remove plugin from all agents
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	agents, _ := h.Store.ListAgents()
	for _, agentName := range agents {
		agent, ok := h.Store.GetAgent(agentName)
		if ok {
			delete(agent.Plugins, pluginName)
			_ = h.Store.SetAgent(agentName, agent)
		}
	}

	// Remove plugin binary if it exists
	if plugin.Path != "" {
		if err := os.Remove(plugin.Path); err != nil && !os.IsNotExist(err) {
			fmt.Printf("Warning: Failed to remove plugin binary: %v\n", err)
		}
	}

	// Remove from registry
	if err := h.RegistryManager.RemovePlugin(pluginName); err != nil {
		if err := orihttp.RespondInternalError(w, fmt.Sprintf("Failed to remove plugin from registry: %v", err)); err != nil {
			logger.

				// Remove from category manager
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	h.CategoryManager.RemovePlugin(pluginName)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Plugin %s deleted successfully", pluginName),
	}); err != nil {
		fmt.Printf("Error encoding delete response: %v\n", err)
	}
}

// HandleReloadPlugin reloads a plugin (useful after updates)
// POST /api/plugins/:name/reload
func (h *PluginsPageHandler) HandleReloadPlugin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	pluginName := h.extractPluginName(r.URL.Path)
	if pluginName == "" {
		if err := orihttp.RespondBadRequest(w, "Plugin name required"); err != nil {
			logger.

				// Get current agent
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	_, currentAgent := h.Store.ListAgents()
	agent, ok := h.Store.GetAgent(currentAgent)
	if !ok {
		if err := orihttp.RespondInternalError(w, "Current agent not found"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	plugin, exists := agent.Plugins[pluginName]
	if !exists {
		if err := orihttp.RespondBadRequest(w, "Plugin not enabled"); err != nil {
			logger.

				// Kill old plugin process if it's an RPC plugin
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if rpcPlugin, ok := plugin.Tool.(interface{ Kill() }); ok {
		rpcPlugin.Kill()
	}

	// Reload plugin from disk
	newTool, err := h.Loader.Load(plugin.Path)
	if err != nil {
		if err := orihttp.RespondInternalError(w, fmt.Sprintf("Failed to reload plugin: %v", err)); err != nil {
			logger.

				// Update plugin in agent
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	plugin.Tool = newTool
	plugin.Definition = newTool.Definition()
	agent.Plugins[pluginName] = plugin

	// Save agent
	if err := h.Store.SetAgent(currentAgent, agent); err != nil {
		if err := orihttp.RespondInternalError(w, fmt.Sprintf("Failed to save agent: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Plugin %s reloaded successfully", pluginName),
	}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": err})
	}
}

// HandleGetPluginAgents returns list of agents provided by a plugin
// GET /api/plugins/:name/agents
func (h *PluginsPageHandler) HandleGetPluginAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	pluginName := h.extractPluginName(r.URL.Path)
	if pluginName == "" {
		if err := orihttp.RespondBadRequest(w, "Plugin name required"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	plugin, err := h.RegistryManager.GetPluginByName(pluginName)
	if err != nil {
		if err := orihttp.RespondNotFound(w, fmt.Sprintf("Plugin not found: %v", err)); err != nil {
			logger.

				// Get current agent to check if plugin is loaded
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	_, currentAgent := h.Store.ListAgents()
	agent, agentExists := h.Store.GetAgent(currentAgent)

	var loadedPlugin *types.LoadedPlugin
	if agentExists {
		if lp, exists := agent.Plugins[pluginName]; exists {
			loadedPlugin = &lp
		}
	}

	agents := h.getPluginAgents(plugin, loadedPlugin)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"agents": agents,
	}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": err})
	}
}

// Helper methods

func (h *PluginsPageHandler) extractPluginName(path string) string {
	// Remove /api/plugins/ prefix
	pluginPath := strings.TrimPrefix(path, "/api/plugins/")

	// Split by / and take first component (plugin name)
	parts := strings.Split(pluginPath, "/")
	if len(parts) > 0 {
		return parts[0]
	}
	return ""
}

func (h *PluginsPageHandler) getPluginStatus(plugin *types.PluginRegistryEntry, isEnabled bool) string {
	if !isEnabled {
		return "inactive"
	}
	if !plugin.PermissionsApproved {
		return "pending_approval"
	}
	if plugin.HealthStatus == "error" || plugin.HealthStatus == "failed" {
		return "error"
	}
	if plugin.HealthStatus == "healthy" {
		return "active"
	}
	return "inactive"
}

// getPluginAgents returns the list of agents that have this plugin enabled.
// Currently returns empty - would require iterating through all agents
// and checking their Plugins map for a matching plugin name.
func (h *PluginsPageHandler) getPluginAgents(plugin *types.PluginRegistryEntry, loadedPlugin *types.LoadedPlugin) []string {
	return []string{}
}
