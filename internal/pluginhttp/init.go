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
	"github.com/johnjallday/ori-agent/pluginapi"
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
		if err := json.NewEncoder(w).Encode(response); err != nil {
			logger.Error("Failed to encode response", logger.Fields{"response": err})
		}
		return
	}

	// Check if the tool supports GetDefaultSettings
	if defaultSettingsTool, ok := tool.(pluginapi.DefaultSettingsProvider); ok {
		defaultSettings, err := defaultSettingsTool.GetDefaultSettings()
		if err != nil {
			if err := orihttp.RespondInternalError(w, "Failed to get default settings"); err != nil {
				logger.Error(

					// Check if default settings is empty (plugin doesn't actually have settings)
					"Failed to write response", logger.Fields{"error": err})
			}
			return
		}

		if defaultSettings == "" {
			response := map[string]interface{}{
				"success": false,
				"message": "Plugin does not provide default settings",
			}
			if err := json.NewEncoder(w).Encode(response); err != nil {
				logger.Error("Failed to encode response", logger.Fields{"response": err})
			}
			return
		}

		// Parse the JSON settings to ensure it's valid
		var settings map[string]interface{}
		if err := json.Unmarshal([]byte(defaultSettings), &settings); err != nil {
			if err := orihttp.RespondInternalError(w, "Invalid default settings format"); err != nil {
				logger.Error(

					// Return the default settings
					"Failed to write response", logger.Fields{"error": err})
			}
			return
		}

		response := map[string]interface{}{
			"success":          true,
			"default_settings": settings,
		}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			logger.Error("Failed to encode response", logger.Fields{"response": err})
		}
	} else {
		// Plugin doesn't support default settings
		response := map[string]interface{}{
			"success": false,
			"message": "Plugin does not support default settings",
		}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			logger.Error("Failed to encode response", logger.Fields{"response": err})
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
		if err := orihttp.RespondBadRequest(w, "invalid path format"); err != nil {
			logger.Error("Failed to write bad request response", logger.Fields{"error": err})
		}
		return
	}

	pluginName := pathParts[0]
	action := pathParts[1]
	fmt.Printf("🔧 Plugin: %s, Action: %s\n", pluginName, action)

	if pluginName == "" {
		fmt.Printf("❌ Plugin name is empty\n")
		if err := orihttp.RespondBadRequest(w, "plugin name required"); err != nil {
			logger.

				// Get current agent and its plugins
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	_, current := h.store.ListAgents()
	fmt.Printf("📁 Current agent: %s\n", current)

	ag, ok := h.store.GetAgent(current)
	if !ok {
		fmt.Printf("❌ Agent '%s' not found\n", current)
		if err := orihttp.RespondInternalError(w, "current agent not found"); err != nil {
			logger.

				// Find the plugin
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	plugin, exists := ag.Plugins[pluginName]

	// For default-settings and config, also check local registry if plugin not loaded in agent
	// OR if plugin exists but tool is nil (failed to load)
	if (!exists || (exists && plugin.Tool == nil)) && (action == "default-settings" || action == "config") {
		fmt.Printf("🔄 Plugin not in agent or tool is nil, trying local registry...\n")
		// Try to load plugin from local registry temporarily
		localReg, err := h.registryManager.LoadLocal()
		if err == nil {
			fmt.Printf("✓ Local registry has %d plugins\n", len(localReg.Plugins))
			for _, regPlugin := range localReg.Plugins {
				if regPlugin.Name == pluginName {
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
		if err := orihttp.RespondNotFound(w, "plugin not found"); err != nil {
			logger.Error("Failed to write not found response", logger.Fields{"error": err})
		}
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
		if err := orihttp.RespondBadRequest(w, "invalid action"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
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
	settingsFilePath := fmt.Sprintf("agents/%s/%s_settings.json", current, pluginName)
	var currentValues map[string]interface{}
	isInitialized := false

	if fileData, err := os.ReadFile(settingsFilePath); err == nil {
		// File exists, parse current values
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
			if err := json.NewEncoder(w).Encode(response); err != nil {
				logger.Error("Failed to encode response", logger.Fields{"response": err})
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

			if err := json.NewEncoder(w).Encode(response); err != nil {

				logger.Error("Failed to encode response", logger.Fields{"response": err})

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

	if err := json.NewEncoder(w).Encode(response); err != nil {

		logger.Error("Failed to encode response", logger.Fields{"response": err})

	}
}

func (h *InitHandler) handlePluginInitialization(w http.ResponseWriter, r *http.Request, tool pluginapi.PluginTool, pluginName, agentName string) {
	w.Header().Set("Content-Type", "application/json")

	// Parse configuration from request body first
	var configData map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&configData); err != nil {
		if err := json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"message": "Invalid JSON in request body: " + err.Error(),
		}); err != nil {
			logger.Error("Failed to encode response", logger.Fields{"response": err})
		}
		return
	}

	// Simplified: Skip legacy SettingsProvider check

	// Check if plugin implements InitializationProvider (modern plugins)
	initProvider, ok := tool.(pluginapi.InitializationProvider)
	if !ok {
		if err := json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"message": "Plugin does not support automatic initialization",
		}); err != nil {
			logger.Error("Failed to encode response", logger.Fields{"response": err})
		}
		return
	}

	// Validate configuration
	if err := initProvider.ValidateConfig(configData); err != nil {
		if err := json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"message": "Configuration validation failed: " + err.Error(),
		}); err != nil {
			logger.Error("Failed to encode response", logger.Fields{"response": err})
		}
		return
	}

	// Initialize plugin with configuration
	if err := initProvider.InitializeWithConfig(configData); err != nil {
		if err := json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"message": "Plugin initialization failed: " + err.Error(),
		}); err != nil {
			logger.Error("Failed to encode response", logger.Fields{"response": err})
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

	if err := json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"message": "Plugin initialized successfully",
	}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
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

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if err := orihttp.RespondBadRequest(w, "invalid JSON"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if req.PluginName == "" {
		if err := orihttp.RespondBadRequest(w, "plugin_name required"); err != nil {
			logger.

				// Get current agent and its plugins
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	_, current := h.store.ListAgents()
	ag, ok := h.store.GetAgent(current)
	if !ok {
		if err := orihttp.RespondInternalError(w, "current agent not found"); err != nil {
			logger.

				// Find the plugin
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	plugin, exists := ag.Plugins[req.PluginName]
	if !exists {
		if err := orihttp.RespondNotFound(w, "plugin not found"); err != nil {
			logger.Error("Failed to write not found response", logger.Fields{"error": err})
		}
		return
	}

	// Convert parameters to JSON string
	argsJSON, err := json.Marshal(req.Parameters)
	if err != nil {
		if err := orihttp.RespondBadRequest(w, fmt.Sprintf("failed to marshal parameters: %v", err)); err != nil {
			logger.

				// Execute the plugin function
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	result, err := plugin.Tool.Call(r.Context(), string(argsJSON))
	if err != nil {
		if err := orihttp.RespondInternalError(w, fmt.Sprintf("plugin execution error: %v", err)); err != nil {
			logger.

				// Return the result
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"result":  result,
	}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// PluginInitStatusHandler checks filesystem for settings files
func (h *InitHandler) PluginInitStatusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write method not allowed response", logger.Fields{"error": err})
		}
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
		if err := json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"message": "current agent not found",
		}); err != nil {
			logger.Error("Failed to encode response", logger.Fields{"response": err})
		}
		return
	}

	// Check each active plugin for settings file
	var uninitializedPlugins []map[string]any

	for pluginName := range ag.Plugins {
		settingsFilePath := fmt.Sprintf("agents/%s/%s_settings.json", current, pluginName)

		// Check if settings file exists
		if _, err := os.Stat(settingsFilePath); os.IsNotExist(err) {
			// Settings file doesn't exist, plugin needs initialization
			uninitializedPlugins = append(uninitializedPlugins, map[string]any{
				"name":            pluginName,
				"description":     fmt.Sprintf("Plugin %s needs configuration", pluginName),
				"required_config": []map[string]any{}, // Empty for now, could be expanded later
				"legacy_plugin":   true,
			})
		}
	}

	response := map[string]any{
		"success":                 true,
		"requires_initialization": len(uninitializedPlugins) > 0,
		"uninitialized_plugins":   uninitializedPlugins,
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {

		logger.Error("Failed to encode response", logger.Fields{"response": err})

	}
}
