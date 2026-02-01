package pluginhttp

import (
	"context"
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
	"github.com/oriagent/ori-pluginapi"
)

// PluginTestTimeout is the maximum duration allowed for plugin test execution
const PluginTestTimeout = 30 * time.Second

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
		orihttp.MethodNotAllowed(w)
		return
	}

	tagFilter := strings.TrimSpace(r.URL.Query().Get("tag"))
	var normalizedTagFilter string
	if tagFilter != "" {
		normalizedTagFilter = internaltags.NormalizeTag(tagFilter)
		if err := internaltags.ValidateTag(normalizedTagFilter); err != nil {
			orihttp.BadRequest(w, fmt.Sprintf("Invalid tag filter: %v", err))
			return
		}
	}

	// Load local registry to get all plugins
	localReg, err := h.RegistryManager.LoadLocal()
	if err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to load registry: %v", err))
		return
	}

	// Get current agent to check enabled plugins
	agent, currentAgent, agentExists := store.GetCurrentAgent(h.Store)

	// Get all agents to see which plugins they are using
	allAgentNames, _ := h.Store.ListAgents()
	agentPluginMap := make(map[string][]string) // plugin lookup name -> list of agent names
	for _, name := range allAgentNames {
		if ag, ok := h.Store.GetAgent(name); ok {
			for pName := range ag.Plugins {
				normPName := registry.NormalizePluginNameForLookup(pName)
				agentPluginMap[normPName] = append(agentPluginMap[normPName], name)
			}
		}
	}

	// Build response with extended plugin information
	plugins := make([]map[string]interface{}, 0, len(localReg.Plugins))

	for _, plugin := range localReg.Plugins {
		tags := pluginAllTags(&plugin)
		if normalizedTagFilter != "" && !pluginHasTag(&plugin, normalizedTagFilter) {
			continue
		}

		// Check if plugin is enabled using lookup normalization
		isEnabled := false
		var loadedPlugin *types.LoadedPlugin
		if agentExists {
			normalized := registry.NormalizePluginNameForLookup(plugin.Name)
			for name, lp := range agent.Plugins {
				if registry.NormalizePluginNameForLookup(name) == normalized {
					isEnabled = true
					loadedPlugin = &lp
					break
				}
			}
		}

		// Check if plugin is installed (binary exists on disk)
		isInstalled := false
		if plugin.Path != "" {
			if _, err := os.Stat(plugin.Path); err == nil {
				isInstalled = true
			}
		}

		// Get agents using this plugin
		normName := registry.NormalizePluginNameForLookup(plugin.Name)
		usingAgents := agentPluginMap[normName]
		if usingAgents == nil {
			usingAgents = []string{}
		}

		// Check if plugin supports initialization and get config variables
		var supportsInit bool
		var requiredConfig []pluginapi.ConfigVariable

		// Try to check initialization support
		if loadedPlugin != nil && loadedPlugin.Tool != nil {
			if initProvider, ok := loadedPlugin.Tool.(pluginapi.InitializationProvider); ok {
				requiredConfig = initProvider.GetRequiredConfig()
				supportsInit = true
			}
		} else if plugin.InitSupportChecked {
			// Use cached init support from registry to avoid spawning plugin process
			supportsInit = plugin.SupportsInitialization
			requiredConfig = plugin.RequiredConfig
		}

		// Check if settings file exists for this plugin
		isConfigured := false
		if currentAgent != "" {
			lookupName := registry.NormalizePluginNameForLookup(plugin.Name)
			// Settings files are named after the lookup name (without version)
			settingsFilePath := fmt.Sprintf("agents/%s/%s_settings.json", currentAgent, lookupName)
			if _, err := os.Stat(settingsFilePath); err == nil {
				isConfigured = true
			}
		}

		// Determine plugin status (now focused on health)
		status := h.getPluginStatus(&plugin, isEnabled)

		pluginInfo := map[string]interface{}{
			"name":                    plugin.Name,
			"description":             plugin.Description,
			"version":                 plugin.Version,
			"path":                    plugin.Path,
			"tags":                    tags,
			"category":                plugin.Category,
			"status":                  status,
			"enabled":                 isEnabled,
			"installed":               isInstalled,
			"agents":                  usingAgents, // List of agent names using this plugin
			"permissions":             plugin.Permissions,
			"permissions_approved":    plugin.PermissionsApproved,
			"health_status":           plugin.HealthStatus,
			"last_used":               plugin.LastUsed,
			"metadata":                plugin.Metadata,
			"supports_initialization": supportsInit,
			"required_config":         requiredConfig,
			"is_configured":           isConfigured,
		}

		if h.UpdateService != nil {
			pluginInfo["needs_update"] = h.UpdateService.HasUpdateForPlugin(plugin.Name)
		} else {
			pluginInfo["needs_update"] = false
		}

		plugins = append(plugins, pluginInfo)
	}

	response := map[string]interface{}{
		"plugins":       plugins,
		"current_agent": currentAgent,
	}

	w.Header().Set("Content-Type", "application/json")
	orihttp.WriteJSON(w, response)
}

// HandleListPluginTags lists all unique tags across plugins.
// GET /api/plugins/tags
func (h *PluginsPageHandler) HandleListPluginTags(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	localReg, err := h.RegistryManager.LoadLocal()
	if err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to load registry: %v", err))
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
		orihttp.MethodNotAllowed(w)
		return
	}

	rawTag := strings.TrimPrefix(r.URL.Path, "/api/plugins/tags/")
	rawTag = strings.Split(rawTag, "/")[0]
	normalized := internaltags.NormalizeTag(rawTag)
	if err := internaltags.ValidateTag(normalized); err != nil {
		orihttp.BadRequest(w, fmt.Sprintf("Invalid tag: %v", err))
		return
	}

	// Reuse list handler by injecting the tag filter via the query string.

	q := r.URL.Query()
	q.Set("tag", normalized)
	r.URL.RawQuery = q.Encode()
	h.HandleListPlugins(w, r)
}

// HandleGetPluginDetails returns detailed information about a specific plugin
// GET /api/plugins/:name
func (h *PluginsPageHandler) HandleGetPluginDetails(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	// Extract plugin name from URL path
	pluginName := strings.TrimPrefix(r.URL.Path, "/api/plugins/")
	if pluginName == "" || pluginName == "/api/plugins/" {
		orihttp.BadRequest(w, "Plugin name required")
		return
	}

	// Remove any trailing path components (like /details)
	pluginName = strings.Split(pluginName, "/")[0]

	// Get plugin from registry
	plugin, err := h.RegistryManager.GetPluginByName(pluginName)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Plugin not found: %v", err))
		return
	}

	// Get current agent to check if plugin is loaded
	agent, currentAgent, agentExists := store.GetCurrentAgent(h.Store)

	var loadedPlugin *types.LoadedPlugin
	var definition interface{}
	var operations []pluginapi.OperationInfo
	lpExists := false
	if agentExists {
		normalized := registry.NormalizePluginNameForLookup(pluginName)
		for name, lp := range agent.Plugins {
			if registry.NormalizePluginNameForLookup(name) == normalized {
				loadedPlugin = &lp
				definition = lp.Definition
				lpExists = true

				// Extract operations if tool is loaded
				if lp.Tool != nil {
					if opsProvider, ok := lp.Tool.(pluginapi.OperationsProvider); ok {
						operations = opsProvider.GetOperations()
					}
				}
				break
			}
		}
	}

	// If definition is not found in loaded plugins, try loading it from the binary
	if (definition == nil || len(operations) == 0) && plugin.Path != "" {
		if tool, err := h.Loader.Load(plugin.Path); err == nil {
			defer pluginloader.CloseRPCPlugin(tool)
			if definition == nil {
				definition = tool.Definition()
			}
			// Also check for operations
			if opsProvider, ok := tool.(pluginapi.OperationsProvider); ok {
				operations = opsProvider.GetOperations()
			}
		}
	}

	// Fallback: If no operations were found via interface, try extracting from schema
	if len(operations) == 0 && definition != nil {
		// Attempt to extract operations from the tool definition's JSON schema
		extractedOps := h.extractOperationsFromSchema(definition)
		if len(extractedOps) > 0 {
			operations = extractedOps
		}
	}

	// Get permission details
	permissionEntry, _ := h.PermissionManager.GetPermissionEntry(pluginName)
	var permissions interface{}
	if permissionEntry != nil {
		permissions = permissionEntry
	}

	// Check if plugin supports initialization and get config variables
	var supportsInit bool
	var requiredConfig []pluginapi.ConfigVariable

	if loadedPlugin != nil && loadedPlugin.Tool != nil {
		if initProvider, ok := loadedPlugin.Tool.(pluginapi.InitializationProvider); ok {
			requiredConfig = initProvider.GetRequiredConfig()
			supportsInit = true
		}
	} else if plugin.Path != "" {
		if tool, err := h.Loader.Load(plugin.Path); err == nil {
			defer pluginloader.CloseRPCPlugin(tool)
			if initProvider, ok := tool.(pluginapi.InitializationProvider); ok {
				requiredConfig = initProvider.GetRequiredConfig()
				supportsInit = true
			}
		}
	}

	// Check if settings file exists
	isConfigured := false
	if currentAgent != "" {
		lookupName := registry.NormalizePluginNameForLookup(plugin.Name)
		settingsFilePath := fmt.Sprintf("agents/%s/%s_settings.json", currentAgent, lookupName)
		if _, err := os.Stat(settingsFilePath); err == nil {
			isConfigured = true
		}
	}

	// Check if plugin is installed (binary exists on disk)
	isInstalled := false
	if plugin.Path != "" {
		if _, err := os.Stat(plugin.Path); err == nil {
			isInstalled = true
		}
	}

	// Build detailed response
	details := map[string]interface{}{
		"name":                    plugin.Name,
		"description":             plugin.Description,
		"version":                 plugin.Version,
		"tags":                    pluginAllTags(plugin),
		"category":                plugin.Category,
		"path":                    plugin.Path,
		"github_repo":             plugin.GitHubRepo,
		"enabled":                 lpExists,
		"installed":               isInstalled,
		"permissions":             permissions,
		"permissions_approved":    plugin.PermissionsApproved,
		"health_status":           plugin.HealthStatus,
		"status":                  h.getPluginStatus(plugin, lpExists),
		"last_used":               plugin.LastUsed,
		"version_history":         plugin.VersionHistory,
		"metadata":                plugin.Metadata,
		"definition":              definition,
		"operations":              operations,
		"agents":                  h.getPluginAgents(plugin, loadedPlugin),
		"supports_initialization": supportsInit,
		"required_config":         requiredConfig,
		"is_configured":           isConfigured,
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
		orihttp.MethodNotAllowed(w)
		return
	}

	pluginName := h.extractPluginName(r.URL.Path)
	if pluginName == "" {
		orihttp.BadRequest(w, "Plugin name required")
		return
	}

	// Get plugin from registry
	plugin, err := h.RegistryManager.GetPluginByName(pluginName)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Plugin not found: %v", err))
		return
	}
	pluginKey := plugin.Name

	// Load the plugin
	tool, err := h.Loader.Load(plugin.Path)
	if err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to load plugin: %v", err))
		return
	}

	// Get current agent
	agent, currentAgent, ok := store.GetCurrentAgent(h.Store)
	if !ok {
		orihttp.InternalError(w, "Current agent not found")
		return
	}

	// Add plugin to agent
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
		orihttp.InternalError(w, fmt.Sprintf("Failed to save agent: %v", err))
		return
	}

	// Update registry status
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
		orihttp.MethodNotAllowed(w)
		return
	}

	pluginName := h.extractPluginName(r.URL.Path)
	if pluginName == "" {
		orihttp.BadRequest(w, "Plugin name required")
		return
	}

	// Get current agent
	agent, currentAgent, ok := store.GetCurrentAgent(h.Store)
	if !ok {
		orihttp.InternalError(w, "Current agent not found")
		return
	}

	// Remove plugin from agent
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
		orihttp.InternalError(w, fmt.Sprintf("Failed to save agent: %v", err))
		return
	}

	// Update registry status
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
		orihttp.MethodNotAllowed(w)
		return
	}

	pluginName := h.extractPluginName(r.URL.Path)
	if pluginName == "" {
		orihttp.BadRequest(w, "Plugin name required")
		return
	}

	// Parse config from request body
	var configReq struct {
		Config map[string]interface{} `json:"config"`
	}

	if !orihttp.ParseJSONBody(w, r, &configReq) {
		return
	}

	// Get current agent
	_, currentAgent, _ := store.GetCurrentAgent(h.Store)

	// Create agent directory if it doesn't exist
	agentDir := filepath.Join("agents", currentAgent)
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to create agent directory: %v", err))
		return
	}

	// Save config to plugin-specific settings file

	// Use normalized name (without version) to ensure consistent settings file naming
	normalizedPluginName := registry.NormalizePluginNameForLookup(pluginName)
	settingsFileName := fmt.Sprintf("%s_settings.json", normalizedPluginName)
	settingsPath := filepath.Join(agentDir, settingsFileName)

	settingsData, err := json.MarshalIndent(configReq.Config, "", "  ")
	if err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to marshal config: %v", err))
		return
	}

	if err := os.WriteFile(settingsPath, settingsData, 0644); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to write config: %v", err))
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
		orihttp.MethodNotAllowed(w)
		return
	}

	pluginName := h.extractPluginName(r.URL.Path)
	if pluginName == "" {
		orihttp.BadRequest(w, "Plugin name required")
		return
	}

	// Parse test arguments from request body
	var testReq struct {
		Args string `json:"args"`
	}

	if !orihttp.ParseJSONBody(w, r, &testReq) {
		return
	}

	// Get plugin from current agent
	agent, _, ok := store.GetCurrentAgent(h.Store)
	if !ok {
		orihttp.InternalError(w, "Current agent not found")
		return
	}

	plugin, exists := agent.Plugins[pluginName]
	if !exists {
		orihttp.BadRequest(w, "Plugin not enabled. Enable it first before testing.")
		return
	}

	// Execute test call with timeout to prevent plugins from blocking indefinitely
	ctx, cancel := context.WithTimeout(r.Context(), PluginTestTimeout)
	defer cancel()

	result, err := plugin.Tool.Call(ctx, testReq.Args)

	response := map[string]interface{}{
		"success": err == nil,
		"result":  result,
	}

	if err != nil {
		response["error"] = err.Error()
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(response); encErr != nil {
		fmt.Printf("Error encoding response: %v\n", err)
	}
}

// HandleGetPluginLogs returns recent logs for a specific plugin
// GET /api/plugins/:name/logs
func (h *PluginsPageHandler) HandleGetPluginLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	pluginName := h.extractPluginName(r.URL.Path)
	if pluginName == "" {
		orihttp.BadRequest(w, "Plugin name required")
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
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"logs": logs,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// HandleDeletePlugin removes a plugin from the system
// DELETE /api/plugins/:name
func (h *PluginsPageHandler) HandleDeletePlugin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		orihttp.MethodNotAllowed(w)
		return
	}

	pluginName := h.extractPluginName(r.URL.Path)
	if pluginName == "" {
		orihttp.BadRequest(w, "Plugin name required")
		return
	}

	// Get plugin to find its path
	plugin, err := h.RegistryManager.GetPluginByName(pluginName)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Plugin not found: %v", err))
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
		orihttp.InternalError(w, fmt.Sprintf("Failed to remove plugin from registry: %v", err))
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
		orihttp.MethodNotAllowed(w)
		return
	}

	pluginName := h.extractPluginName(r.URL.Path)
	if pluginName == "" {
		orihttp.BadRequest(w, "Plugin name required")
		return
	}

	// Get current agent
	agent, currentAgent, ok := store.GetCurrentAgent(h.Store)
	if !ok {
		orihttp.InternalError(w, "Current agent not found")
		return
	}

	plugin, exists := agent.Plugins[pluginName]
	if !exists {
		orihttp.BadRequest(w, "Plugin not enabled")
		return
	}

	// Kill old plugin process if it's an RPC plugin
	if rpcPlugin, ok := plugin.Tool.(interface{ Kill() }); ok {
		rpcPlugin.Kill()
	}

	// Reload plugin from disk
	newTool, err := h.Loader.Load(plugin.Path)
	if err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to reload plugin: %v", err))
		return
	}

	// Update plugin in agent
	plugin.Tool = newTool
	plugin.Definition = newTool.Definition()
	agent.Plugins[pluginName] = plugin

	// Save agent
	if err := h.Store.SetAgent(currentAgent, agent); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save agent: %v", err))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Plugin %s reloaded successfully", pluginName),
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// HandleGetPluginAgents returns list of agents provided by a plugin
// GET /api/plugins/:name/agents
func (h *PluginsPageHandler) HandleGetPluginAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	pluginName := h.extractPluginName(r.URL.Path)
	if pluginName == "" {
		orihttp.BadRequest(w, "Plugin name required")
		return
	}

	plugin, err := h.RegistryManager.GetPluginByName(pluginName)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Plugin not found: %v", err))
		return
	}

	// Get current agent to check if plugin is loaded
	agent, _, agentExists := store.GetCurrentAgent(h.Store)

	var loadedPlugin *types.LoadedPlugin
	if agentExists {
		if lp, exists := agent.Plugins[pluginName]; exists {
			loadedPlugin = &lp
		}
	}

	agents := h.getPluginAgents(plugin, loadedPlugin)

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"agents": agents,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
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

// extractOperationsFromSchema attempts to parse operations and their parameters from a tool definition
func (h *PluginsPageHandler) extractOperationsFromSchema(toolDef interface{}) []pluginapi.OperationInfo {
	// Convert to map if possible
	defMap, ok := toolDef.(map[string]interface{})
	if !ok {
		// Try to see if it's a pluginapi.Tool
		if t, ok := toolDef.(pluginapi.Tool); ok {
			defMap = map[string]interface{}{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.Parameters,
			}
		} else {
			return nil
		}
	}

	params, ok := defMap["parameters"].(map[string]interface{})
	if !ok || params == nil {
		return nil
	}

	var result []pluginapi.OperationInfo

	// Pattern 1: oneOf (multiple sub-schemas)
	if oneOf, ok := params["oneOf"].([]interface{}); ok {
		for _, opt := range oneOf {
			optSchema, ok := opt.(map[string]interface{})
			if !ok {
				continue
			}

			props, ok := optSchema["properties"].(map[string]interface{})
			if !ok {
				continue
			}

			// Find the operation name from the enum value of the "operation" property
			opName := ""
			if opProp, ok := props["operation"].(map[string]interface{}); ok {
				if enum, ok := opProp["enum"].([]interface{}); ok && len(enum) > 0 {
					if str, ok := enum[0].(string); ok {
						opName = str
					}
				} else if enum, ok := opProp["enum"].([]string); ok && len(enum) > 0 {
					opName = enum[0]
				}
			}

			if opName == "" {
				continue
			}

			// Get required parameters
			var req []string
			if r, ok := optSchema["required"].([]interface{}); ok {
				for _, val := range r {
					if s, ok := val.(string); ok && s != "operation" {
						req = append(req, s)
					}
				}
			}

			// Get all parameters (excluding "operation")
			var pList []string
			for p := range props {
				if p != "operation" {
					pList = append(pList, p)
				}
			}
			sort.Strings(pList)

			result = append(result, pluginapi.OperationInfo{
				Name:               opName,
				Parameters:         pList,
				RequiredParameters: req,
			})
		}
	}

	// Pattern 2: simple enum on "operation" property in flat schema
	if len(result) == 0 {
		props, ok := params["properties"].(map[string]interface{})
		if ok {
			if opProp, ok := props["operation"].(map[string]interface{}); ok {
				var enumValues []string
				if enum, ok := opProp["enum"].([]interface{}); ok {
					for _, val := range enum {
						if s, ok := val.(string); ok {
							enumValues = append(enumValues, s)
						}
					}
				} else if enum, ok := opProp["enum"].([]string); ok {
					enumValues = enum
				}

				if len(enumValues) > 0 {
					// Get required fields for the whole tool
					var globalReq []string
					if r, ok := params["required"].([]interface{}); ok {
						for _, val := range r {
							if s, ok := val.(string); ok && s != "operation" {
								globalReq = append(globalReq, s)
							}
						}
					}

					// Get all parameters (excluding "operation")
					var allParams []string
					for p := range props {
						if p != "operation" {
							allParams = append(allParams, p)
						}
					}
					sort.Strings(allParams)

					for _, opName := range enumValues {
						result = append(result, pluginapi.OperationInfo{
							Name:               opName,
							Parameters:         allParams,
							RequiredParameters: globalReq,
						})
					}
				}
			}
		}
	}

	return result
}
