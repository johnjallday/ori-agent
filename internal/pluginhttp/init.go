package pluginhttp

import (
	"encoding/json"
	"fmt"

	"net/http"
	"os"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/registry"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/oriagent/ori-pluginapi"
)

type InitHandler struct {
	store           store.Store
	registryManager *registry.Manager
	pluginHandler   *Handler
}

func NewInitHandler(store store.Store, registryManager *registry.Manager, pluginHandler *Handler) *InitHandler {
	return &InitHandler{
		store:           store,
		registryManager: registryManager,
		pluginHandler:   pluginHandler,
	}
}

// handlePluginDefaultSettings handles requests for plugin default settings
func (h *InitHandler) handlePluginDefaultSettings(w http.ResponseWriter, tool pluginapi.PluginTool, pluginName string) {
	w.Header().Set("Content-Type", "application/json")

	// Check if tool is nil
	if tool == nil {
		response := map[string]interface{}{
			"success": false,
			"message": "Plugin not loaded",
		}
		w.WriteHeader(http.StatusOK) // Return 200 with success:false instead of 500
		if encErr := json.NewEncoder(w).Encode(response); encErr != nil {
			logger.Error("Failed to encode response", logger.Fields{"error": encErr})
		}
		return
	}

	// Check if the tool supports GetDefaultSettings
	if defaultSettingsTool, ok := tool.(pluginapi.DefaultSettingsProvider); ok {
		defaultSettings, err := defaultSettingsTool.GetDefaultSettings()
		if err != nil {
			orihttp.InternalError(w, "Failed to get default settings")
			return
		}

		// Check if default settings is empty (plugin doesn't actually have settings)
		if defaultSettings == "" {
			response := map[string]interface{}{
				"success": false,
				"message": "Plugin does not provide default settings",
			}
			if encErr := json.NewEncoder(w).Encode(response); encErr != nil {
				logger.Error("Failed to encode response", logger.Fields{"error": encErr})
			}
			return
		}

		// Parse the JSON settings to ensure it's valid
		var settings map[string]interface{}
		if err := json.Unmarshal([]byte(defaultSettings), &settings); err != nil {
			orihttp.InternalError(w, "Invalid default settings format")
			return
		}

		// Return the default settings

		response := map[string]interface{}{
			"success":          true,
			"default_settings": settings,
		}
		if encErr := json.NewEncoder(w).Encode(response); encErr != nil {
			logger.Error("Failed to encode response", logger.Fields{"error": encErr})
		}
	} else {
		// Plugin doesn't support default settings
		response := map[string]interface{}{
			"success": false,
			"message": "Plugin does not support default settings",
		}
		if encErr := json.NewEncoder(w).Encode(response); encErr != nil {
			logger.Error("Failed to encode response", logger.Fields{"error": encErr})
		}
	}
}

// PluginInitHandler handles plugin config discovery and initialization
func (h *InitHandler) PluginInitHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Printf("🎯 PluginInitHandler called: %s %s\n", r.Method, r.URL.Path)

	// Parse URL path to extract plugin name and action
	// Expected paths: /api/plugins/{name}/config or /api/plugins/{name}/initialize
	pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/plugins/"), "/")
	fmt.Printf("📋 Path parts: %v (count: %d)\n", pathParts, len(pathParts))

	if len(pathParts) < 2 {
		fmt.Printf("❌ Invalid path format - not enough parts\n")
		orihttp.BadRequest(w, "invalid path format")
		return
	}

	pluginName := pathParts[0]
	action := pathParts[1]
	fmt.Printf("🔧 Plugin: %s, Action: %s\n", pluginName, action)

	if pluginName == "" {
		fmt.Printf("❌ Plugin name is empty\n")
		orihttp.BadRequest(w, "plugin name required")
		return
	}

	// Get current agent and its plugins
	_, current := h.store.ListAgents()
	fmt.Printf("📁 Current agent: %s\n", current)

	ag, ok := h.store.GetAgent(current)
	if !ok {
		fmt.Printf("❌ Agent '%s' not found\n", current)
		orihttp.InternalError(w, "current agent not found")
		return
	}

	// Find the plugin

	plugin, exists := ag.Plugins[pluginName]
	if !exists {
		normalized := registry.NormalizePluginNameForLookup(pluginName)
		for key, entry := range ag.Plugins {
			if registry.NormalizePluginNameForLookup(key) == normalized {
				pluginName = key
				plugin = entry
				exists = true
				break
			}
		}
	}

	// For default-settings and config, also check local registry if plugin not loaded in agent
	// OR if plugin exists but tool is nil (failed to load)
	if (!exists || (exists && plugin.Tool == nil)) && (action == "default-settings" || action == "config") {
		fmt.Printf("🔄 Plugin not in agent or tool is nil, trying local registry...\n")
		// Try to load plugin from local registry temporarily
		localReg, err := h.registryManager.LoadLocal()
		if err == nil {
			fmt.Printf("✓ Local registry has %d plugins\n", len(localReg.Plugins))
			normalizedName := registry.NormalizePluginNameForLookup(pluginName)
			for _, regPlugin := range localReg.Plugins {
				if registry.NormalizePluginNameForLookup(regPlugin.Name) == normalizedName {
					fmt.Printf("✓ Found '%s' in local registry at: %s\n", pluginName, regPlugin.Path)
					// For config action, try to load plugin to check InitializationProvider
					if action == "config" {
						if r.Method != http.MethodGet {
							w.WriteHeader(http.StatusMethodNotAllowed)
							return
						}
						// Load plugin temporarily to check InitializationProvider
						fmt.Printf("🔄 Loading plugin temporarily from: %s\n", regPlugin.Path)
						var tool pluginapi.PluginTool
						tool, loadErr := NativeLoader{}.Load(regPlugin.Path)
						if loadErr != nil {
							fmt.Printf("❌ Failed to load plugin: %v\n", loadErr)
						} else {
							fmt.Printf("✓ Plugin loaded successfully\n")
						}
						h.handlePluginConfigDiscovery(w, tool, pluginName, current)
						return
					}

					// For default-settings, we need to load the plugin
					tool, err := NativeLoader{}.Load(regPlugin.Path)
					if err == nil {
						if r.Method != http.MethodGet {
							w.WriteHeader(http.StatusMethodNotAllowed)
							return
						}
						h.handlePluginDefaultSettings(w, tool, pluginName)
						return
					}
					break
				}
			}
		}
	}

	if !exists {
		orihttp.NotFound(w, "plugin not found")
		return
	}

	switch action {
	case "config":
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		h.handlePluginConfigDiscovery(w, plugin.Tool, pluginName, current)

	case "initialize":
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		h.handlePluginInitialization(w, r, plugin.Tool, pluginName, current)

	case "default-settings":
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		h.handlePluginDefaultSettings(w, plugin.Tool, pluginName)

	default:
		orihttp.BadRequest(w, "invalid action")
	}
}

func (h *InitHandler) handlePluginConfigDiscovery(w http.ResponseWriter, tool pluginapi.PluginTool, pluginName, agentName string) {
	w.Header().Set("Content-Type", "application/json")

	// Get current agent
	_, current := h.store.ListAgents()
	if current == "" {
		current = agentName
	}

	// Always check for existing settings file first, regardless of plugin load status
	// Try multiple name variations (underscores vs hyphens) to handle naming inconsistencies
	normalizedName := registry.NormalizePluginName(pluginName)
	settingsFilePath := fmt.Sprintf("agents/%s/%s_settings.json", current, pluginName)
	var currentValues map[string]interface{}
	isInitialized := false

	if fileData, err := os.ReadFile(settingsFilePath); err == nil {
		// File exists, parse current values
		if err := json.Unmarshal(fileData, &currentValues); err == nil {
			isInitialized = true
		}
	} else if fileData, err := os.ReadFile(fmt.Sprintf("agents/%s/%s_settings.json", current, normalizedName)); err == nil {
		// Try normalized name (hyphens)
		if err := json.Unmarshal(fileData, &currentValues); err == nil {
			isInitialized = true
		}
	}

	// Use the pluginHandler's GetPluginConfig to get fresh config from loaded plugin
	fmt.Printf("📞 Calling GetPluginConfig for plugin: %s, pluginHandler is nil: %v\n", pluginName, h.pluginHandler == nil)
	if h.pluginHandler != nil {
		configVars, supportsInit, err := h.pluginHandler.GetPluginConfig(pluginName)
		fmt.Printf("📊 GetPluginConfig returned: supportsInit=%v, err=%v, configVars count=%d\n", supportsInit, err, len(configVars))
		if err == nil && supportsInit {
			response := map[string]any{
				"supports_initialization": true,
				"is_initialized":          isInitialized,
				"required_config":         configVars,
				"current_values":          currentValues,
			}
			fmt.Printf("✅ Sending config response: %+v\n", response)
			if encErr := json.NewEncoder(w).Encode(response); encErr != nil {
				logger.Error("Failed to encode response", logger.Fields{"error": encErr})
			}
			return
		}
		if err != nil {
			fmt.Printf("⚠️ GetPluginConfig error: %v\n", err)
		}
	}

	// Fallback: check tool directly if pluginHandler method failed
	if tool != nil {
		if initProvider, ok := tool.(pluginapi.InitializationProvider); ok {
			// Get required configuration variables
			configVars := initProvider.GetRequiredConfig()

			response := map[string]any{
				"supports_initialization": true,
				"is_initialized":          isInitialized,
				"required_config":         configVars,
				"current_values":          currentValues,
			}

			if encErr := json.NewEncoder(w).Encode(response); encErr != nil {

				logger.Error("Failed to encode response", logger.Fields{"error": encErr})

			}
			return
		}
	}

	// Plugin doesn't support initialization or tool is nil, but still show settings if they exist
	response := map[string]any{
		"supports_initialization": isInitialized, // If settings exist, we can at least show them
		"is_initialized":          isInitialized,
		"required_config":         []interface{}{}, // Empty config for unsupported plugins
		"current_values":          currentValues,
		"message":                 "Plugin configuration found in settings file",
	}

	if encErr := json.NewEncoder(w).Encode(response); encErr != nil {

		logger.Error("Failed to encode response", logger.Fields{"error": encErr})

	}
}

func (h *InitHandler) handlePluginInitialization(w http.ResponseWriter, r *http.Request, tool pluginapi.PluginTool, pluginName, agentName string) {
	w.Header().Set("Content-Type", "application/json")

	// Parse configuration from request body first
	var configData map[string]interface{}
	if !orihttp.ParseJSONBody(w, r, &configData) {
		return
	}

	// Simplified: Skip legacy SettingsProvider check

	// Check if plugin implements InitializationProvider (modern plugins)
	initProvider, ok := tool.(pluginapi.InitializationProvider)
	if !ok {
		if encErr := json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"message": "Plugin does not support automatic initialization",
		}); encErr != nil {
			logger.Error("Failed to encode response", logger.Fields{"error": encErr})
		}
		return
	}

	// Validate configuration
	if err := initProvider.ValidateConfig(configData); err != nil {
		if encErr := json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"message": "Configuration validation failed: " + err.Error(),
		}); encErr != nil {
			logger.Error("Failed to encode response", logger.Fields{"error": encErr})
		}
		return
	}

	// Initialize plugin with configuration
	if err := initProvider.InitializeWithConfig(configData); err != nil {
		if encErr := json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"message": "Plugin initialization failed: " + err.Error(),
		}); encErr != nil {
			logger.Error("Failed to encode response", logger.Fields{"error": encErr})
		}
		return
	}

	// Provide agent context if plugin supports it
	if agentAware, ok := tool.(pluginapi.AgentAwareTool); ok {
		agentDir := fmt.Sprintf("agents/%s", agentName)
		agentContext := pluginapi.AgentContext{
			Name:         agentName,
			ConfigPath:   fmt.Sprintf("%s/config.json", agentDir),
			SettingsPath: fmt.Sprintf("%s/agent_settings.json", agentDir),
			AgentDir:     agentDir,
		}
		agentAware.SetAgentContext(agentContext)
	}

	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"message": "Plugin initialized successfully",
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// PluginExecuteHandler directly executes plugin function calls
func (h *InitHandler) PluginExecuteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		PluginName string                 `json:"plugin_name"`
		Parameters map[string]interface{} `json:"parameters"`
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if req.PluginName == "" {
		orihttp.BadRequest(w, "plugin_name required")
		return
	}

	// Get current agent and its plugins
	_, current := h.store.ListAgents()
	ag, ok := h.store.GetAgent(current)
	if !ok {
		orihttp.InternalError(w, "current agent not found")
		return
	}

	// Find the plugin

	plugin, exists := ag.Plugins[req.PluginName]
	if !exists {
		orihttp.NotFound(w, "plugin not found")
		return
	}

	// Convert parameters to JSON string
	argsJSON, err := json.Marshal(req.Parameters)
	if err != nil {
		orihttp.BadRequest(w, fmt.Sprintf("failed to marshal parameters: %v", err))
		return
	}

	// Execute the plugin function
	result, err := plugin.Tool.Call(r.Context(), string(argsJSON))
	if err != nil {
		orihttp.InternalError(w, fmt.Sprintf("plugin execution error: %v", err))
		return
	}

	// Return the result

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"result":  result,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// PluginInitStatusHandler checks which plugins actually need configuration
func (h *InitHandler) PluginInitStatusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	// Get current agent
	_, current := h.store.ListAgents()
	if current == "" {
		names, _ := h.store.ListAgents()
		if len(names) > 0 {
			current = names[0]
		} else {
			current = "default"
		}
	}

	// Get active plugins for current agent
	ag, ok := h.store.GetAgent(current)
	if !ok {
		if encErr := json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"message": "current agent not found",
		}); encErr != nil {
			logger.Error("Failed to encode response", logger.Fields{"error": encErr})
		}
		return
	}

	// Check each active plugin - only include those that actually need configuration
	var uninitializedPlugins []map[string]any

	for pluginName, pluginEntry := range ag.Plugins {
		// First check if plugin actually supports/needs configuration
		needsConfig := false
		var requiredConfig []pluginapi.ConfigVariable

		if pluginEntry.Tool != nil {
			// Check if plugin implements InitializationProvider
			if initProvider, ok := pluginEntry.Tool.(pluginapi.InitializationProvider); ok {
				requiredConfig = initProvider.GetRequiredConfig()
				// Plugin needs config only if it has required config variables
				needsConfig = len(requiredConfig) > 0
			}
		}

		// Skip plugins that don't need configuration
		if !needsConfig {
			continue
		}

		// Plugin needs configuration - check if settings file exists
		settingsFilePath := fmt.Sprintf("agents/%s/%s_settings.json", current, pluginName)
		normalizedName := registry.NormalizePluginName(pluginName)

		// Check both original and normalized name paths
		settingsExist := false
		if _, err := os.Stat(settingsFilePath); err == nil {
			settingsExist = true
		} else if _, err := os.Stat(fmt.Sprintf("agents/%s/%s_settings.json", current, normalizedName)); err == nil {
			settingsExist = true
		}

		// Only add to uninitialized if plugin needs config AND settings don't exist
		if !settingsExist {
			uninitializedPlugins = append(uninitializedPlugins, map[string]any{
				"name":            pluginName,
				"description":     fmt.Sprintf("Plugin %s needs configuration", pluginName),
				"required_config": requiredConfig,
			})
		}
	}

	response := map[string]any{
		"success":                 true,
		"requires_initialization": len(uninitializedPlugins) > 0,
		"uninitialized_plugins":   uninitializedPlugins,
	}

	if encErr := json.NewEncoder(w).Encode(response); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}
