package pluginhttp

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/pluginloader"
	"github.com/johnjallday/ori-agent/internal/pluginmanager"
	"github.com/johnjallday/ori-agent/internal/registry"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

// PluginsPageHandler handles endpoints for the dedicated plugins management page
type PluginsPageHandler struct {
	Store             store.Store
	RegistryManager   *registry.Manager
	CategoryManager   *pluginmanager.CategoryManager
	PermissionManager *pluginmanager.PermissionManager
	Loader            ToolLoader
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

// HandleListPlugins returns a list of all plugins with their status, categories, and permissions
// GET /api/plugins
func (h *PluginsPageHandler) HandleListPlugins(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Load local registry to get all plugins
	localReg, err := h.RegistryManager.LoadLocal()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load registry: %v", err), http.StatusInternalServerError)
		return
	}

	// Get current agent to check enabled plugins
	_, currentAgent := h.Store.ListAgents()
	agent, agentExists := h.Store.GetAgent(currentAgent)

	// Build response with extended plugin information
	plugins := make([]map[string]interface{}, 0, len(localReg.Plugins))

	for _, plugin := range localReg.Plugins {
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

		plugins = append(plugins, pluginInfo)
	}

	response := map[string]interface{}{
		"plugins": plugins,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

// HandleGetPluginDetails returns detailed information about a specific plugin
// GET /api/plugins/:name
func (h *PluginsPageHandler) HandleGetPluginDetails(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract plugin name from URL path
	pluginName := strings.TrimPrefix(r.URL.Path, "/api/plugins/")
	if pluginName == "" || pluginName == "/api/plugins/" {
		http.Error(w, "Plugin name required", http.StatusBadRequest)
		return
	}

	// Remove any trailing path components (like /details)
	pluginName = strings.Split(pluginName, "/")[0]

	// Get plugin from registry
	plugin, err := h.RegistryManager.GetPluginByName(pluginName)
	if err != nil {
		http.Error(w, fmt.Sprintf("Plugin not found: %v", err), http.StatusNotFound)
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
	_ = json.NewEncoder(w).Encode(details)
}

// HandleEnablePlugin enables a plugin for the current agent
// POST /api/plugins/:name/enable
func (h *PluginsPageHandler) HandleEnablePlugin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	pluginName := h.extractPluginName(r.URL.Path)
	if pluginName == "" {
		http.Error(w, "Plugin name required", http.StatusBadRequest)
		return
	}

	// Get plugin from registry
	plugin, err := h.RegistryManager.GetPluginByName(pluginName)
	if err != nil {
		http.Error(w, fmt.Sprintf("Plugin not found: %v", err), http.StatusNotFound)
		return
	}

	// Load the plugin
	tool, err := h.Loader.Load(plugin.Path)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load plugin: %v", err), http.StatusInternalServerError)
		return
	}

	// Get current agent
	_, currentAgent := h.Store.ListAgents()
	agent, ok := h.Store.GetAgent(currentAgent)
	if !ok {
		http.Error(w, "Current agent not found", http.StatusInternalServerError)
		return
	}

	// Add plugin to agent
	if agent.Plugins == nil {
		agent.Plugins = make(map[string]types.LoadedPlugin)
	}

	supportsFiles, acceptedFileTypes := pluginloader.GetPluginFileSupport(tool)
	agent.Plugins[pluginName] = types.LoadedPlugin{
		Tool:              tool,
		Definition:        tool.Definition(),
		Path:              plugin.Path,
		Version:           plugin.Version,
		SupportsFiles:     supportsFiles,
		AcceptedFileTypes: acceptedFileTypes,
	}

	// Save agent
	if err := h.Store.SetAgent(currentAgent, agent); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save agent: %v", err), http.StatusInternalServerError)
		return
	}

	// Update registry status
	if err := h.RegistryManager.UpdatePluginStatus(pluginName, true, "healthy"); err != nil {
		// Log error but don't fail the request
		fmt.Printf("Warning: Failed to update plugin status: %v\n", err)
	}

	// Update last used timestamp
	if err := h.RegistryManager.UpdatePluginLastUsed(pluginName, time.Now()); err != nil {
		fmt.Printf("Warning: Failed to update last used: %v\n", err)
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Plugin %s enabled successfully", pluginName),
	}); err != nil {
		fmt.Printf("Warning: Failed to encode response: %v\n", err)
	}
}

// HandleDisablePlugin disables a plugin for the current agent
// POST /api/plugins/:name/disable
func (h *PluginsPageHandler) HandleDisablePlugin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	pluginName := h.extractPluginName(r.URL.Path)
	if pluginName == "" {
		http.Error(w, "Plugin name required", http.StatusBadRequest)
		return
	}

	// Get current agent
	_, currentAgent := h.Store.ListAgents()
	agent, ok := h.Store.GetAgent(currentAgent)
	if !ok {
		http.Error(w, "Current agent not found", http.StatusInternalServerError)
		return
	}

	// Remove plugin from agent
	delete(agent.Plugins, pluginName)

	// Save agent
	if err := h.Store.SetAgent(currentAgent, agent); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save agent: %v", err), http.StatusInternalServerError)
		return
	}

	// Update registry status
	if err := h.RegistryManager.UpdatePluginStatus(pluginName, false, "inactive"); err != nil {
		fmt.Printf("Warning: Failed to update plugin status: %v\n", err)
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Plugin %s disabled successfully", pluginName),
	}); err != nil {
		fmt.Printf("Warning: Failed to encode response: %v\n", err)
	}
}

// HandleUpdatePluginConfig updates configuration for a specific plugin
// PUT /api/plugins/:name/config
func (h *PluginsPageHandler) HandleUpdatePluginConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	pluginName := h.extractPluginName(r.URL.Path)
	if pluginName == "" {
		http.Error(w, "Plugin name required", http.StatusBadRequest)
		return
	}

	// Parse config from request body
	var configReq struct {
		Config map[string]interface{} `json:"config"`
	}

	if err := json.NewDecoder(r.Body).Decode(&configReq); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	// Get current agent
	_, currentAgent := h.Store.ListAgents()

	// Create agent directory if it doesn't exist
	agentDir := filepath.Join("agents", currentAgent)
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		http.Error(w, fmt.Sprintf("Failed to create agent directory: %v", err), http.StatusInternalServerError)
		return
	}

	// Save config to plugin-specific settings file
	settingsFileName := fmt.Sprintf("%s_settings.json", pluginName)
	settingsPath := filepath.Join(agentDir, settingsFileName)

	settingsData, err := json.MarshalIndent(configReq.Config, "", "  ")
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to marshal config: %v", err), http.StatusInternalServerError)
		return
	}

	if err := os.WriteFile(settingsPath, settingsData, 0644); err != nil {
		http.Error(w, fmt.Sprintf("Failed to write config: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Configuration updated for plugin %s", pluginName),
		"path":    settingsPath,
	}); err != nil {
		fmt.Printf("Warning: Failed to encode response: %v\n", err)
	}
}

// HandleTestPlugin executes a test call to the plugin
// POST /api/plugins/:name/test
func (h *PluginsPageHandler) HandleTestPlugin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	pluginName := h.extractPluginName(r.URL.Path)
	if pluginName == "" {
		http.Error(w, "Plugin name required", http.StatusBadRequest)
		return
	}

	// Parse test arguments from request body
	var testReq struct {
		Args string `json:"args"`
	}

	if err := json.NewDecoder(r.Body).Decode(&testReq); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	// Get plugin from current agent
	_, currentAgent := h.Store.ListAgents()
	agent, ok := h.Store.GetAgent(currentAgent)
	if !ok {
		http.Error(w, "Current agent not found", http.StatusInternalServerError)
		return
	}

	plugin, exists := agent.Plugins[pluginName]
	if !exists {
		http.Error(w, "Plugin not enabled. Enable it first before testing.", http.StatusBadRequest)
		return
	}

	// Execute test call
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
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	pluginName := h.extractPluginName(r.URL.Path)
	if pluginName == "" {
		http.Error(w, "Plugin name required", http.StatusBadRequest)
		return
	}

	// For now, return placeholder logs
	// TODO: Implement actual log collection from plugin execution
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
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	pluginName := h.extractPluginName(r.URL.Path)
	if pluginName == "" {
		http.Error(w, "Plugin name required", http.StatusBadRequest)
		return
	}

	// Get plugin to find its path
	plugin, err := h.RegistryManager.GetPluginByName(pluginName)
	if err != nil {
		http.Error(w, fmt.Sprintf("Plugin not found: %v", err), http.StatusNotFound)
		return
	}

	// Remove plugin from all agents
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
		http.Error(w, fmt.Sprintf("Failed to remove plugin from registry: %v", err), http.StatusInternalServerError)
		return
	}

	// Remove from category manager
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
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	pluginName := h.extractPluginName(r.URL.Path)
	if pluginName == "" {
		http.Error(w, "Plugin name required", http.StatusBadRequest)
		return
	}

	// Get current agent
	_, currentAgent := h.Store.ListAgents()
	agent, ok := h.Store.GetAgent(currentAgent)
	if !ok {
		http.Error(w, "Current agent not found", http.StatusInternalServerError)
		return
	}

	plugin, exists := agent.Plugins[pluginName]
	if !exists {
		http.Error(w, "Plugin not enabled", http.StatusBadRequest)
		return
	}

	// Kill old plugin process if it's an RPC plugin
	if rpcPlugin, ok := plugin.Tool.(interface{ Kill() }); ok {
		rpcPlugin.Kill()
	}

	// Reload plugin from disk
	newTool, err := h.Loader.Load(plugin.Path)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to reload plugin: %v", err), http.StatusInternalServerError)
		return
	}

	// Update plugin in agent
	plugin.Tool = newTool
	plugin.Definition = newTool.Definition()
	agent.Plugins[pluginName] = plugin

	// Save agent
	if err := h.Store.SetAgent(currentAgent, agent); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save agent: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Plugin %s reloaded successfully", pluginName),
	}); err != nil {
		log.Printf("Failed to encode response: %v", err)
	}
}

// HandleGetPluginAgents returns list of agents provided by a plugin
// GET /api/plugins/:name/agents
func (h *PluginsPageHandler) HandleGetPluginAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	pluginName := h.extractPluginName(r.URL.Path)
	if pluginName == "" {
		http.Error(w, "Plugin name required", http.StatusBadRequest)
		return
	}

	// Get plugin from registry
	plugin, err := h.RegistryManager.GetPluginByName(pluginName)
	if err != nil {
		http.Error(w, fmt.Sprintf("Plugin not found: %v", err), http.StatusNotFound)
		return
	}

	// Get current agent to check if plugin is loaded
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
		log.Printf("Failed to encode response: %v", err)
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

func (h *PluginsPageHandler) getPluginAgents(plugin *types.PluginRegistryEntry, loadedPlugin *types.LoadedPlugin) []string {
	// For now, return empty array
	// TODO: Implement agent discovery from plugin metadata or interfaces
	return []string{}
}
