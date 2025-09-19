package pluginhttp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/johnjallday/dolphin-agent/internal/registry"
	"github.com/johnjallday/dolphin-agent/internal/store"
	"github.com/johnjallday/dolphin-agent/pluginapi"
)

type InitHandler struct {
	store           store.Store
	registryManager *registry.Manager
}

func NewInitHandler(store store.Store, registryManager *registry.Manager) *InitHandler {
	return &InitHandler{
		store:           store,
		registryManager: registryManager,
	}
}

// handlePluginDefaultSettings handles requests for plugin default settings
func (h *InitHandler) handlePluginDefaultSettings(w http.ResponseWriter, tool pluginapi.Tool, pluginName string) {
	w.Header().Set("Content-Type", "application/json")

	// Check if the tool supports GetDefaultSettings
	if defaultSettingsTool, ok := tool.(interface{ GetDefaultSettings() (string, error) }); ok {
		defaultSettings, err := defaultSettingsTool.GetDefaultSettings()
		if err != nil {
			http.Error(w, "Failed to get default settings", http.StatusInternalServerError)
			return
		}

		// Parse the JSON settings to ensure it's valid
		var settings map[string]interface{}
		if err := json.Unmarshal([]byte(defaultSettings), &settings); err != nil {
			http.Error(w, "Invalid default settings format", http.StatusInternalServerError)
			return
		}

		// Return the default settings
		response := map[string]interface{}{
			"success":          true,
			"default_settings": settings,
		}
		json.NewEncoder(w).Encode(response)
	} else {
		// Plugin doesn't support default settings
		response := map[string]interface{}{
			"success": false,
			"message": "Plugin does not support default settings",
		}
		json.NewEncoder(w).Encode(response)
	}
}

// PluginInitHandler handles plugin config discovery and initialization
func (h *InitHandler) PluginInitHandler(w http.ResponseWriter, r *http.Request) {
	// Parse URL path to extract plugin name and action
	// Expected paths: /api/plugins/{name}/config or /api/plugins/{name}/initialize
	pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/plugins/"), "/")
	if len(pathParts) < 2 {
		http.Error(w, "invalid path format", http.StatusBadRequest)
		return
	}

	pluginName := pathParts[0]
	action := pathParts[1]

	if pluginName == "" {
		http.Error(w, "plugin name required", http.StatusBadRequest)
		return
	}

	// Get current agent and its plugins
	_, current := h.store.ListAgents()
	ag, ok := h.store.GetAgent(current)
	if !ok {
		http.Error(w, "current agent not found", http.StatusInternalServerError)
		return
	}

	// Find the plugin
	plugin, exists := ag.Plugins[pluginName]

	// For default-settings, also check local registry if plugin not loaded in agent
	if !exists && action == "default-settings" {
		// Try to load plugin from local registry temporarily
		localReg, err := h.registryManager.LoadLocal()
		if err == nil {
			for _, regPlugin := range localReg.Plugins {
				if regPlugin.Name == pluginName {
					// Load the plugin temporarily just to get default settings
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
		http.Error(w, "plugin not found", http.StatusNotFound)
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
		http.Error(w, "invalid action", http.StatusBadRequest)
	}
}

func (h *InitHandler) handlePluginConfigDiscovery(w http.ResponseWriter, tool pluginapi.Tool, pluginName, agentName string) {
	// Check if we should return the raw JSON settings file
	// Look for the plugin settings file in the agent directory
	_, current := h.store.ListAgents()
	if current == "" {
		current = agentName
	}

	settingsFilePath := fmt.Sprintf("agents/%s/%s_settings.json", current, pluginName)
	if fileData, err := os.ReadFile(settingsFilePath); err == nil {
		// File exists, return it directly as JSON
		w.Header().Set("Content-Type", "application/json")
		w.Write(fileData)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	// Check if plugin implements SettingsProvider (for legacy plugins)
	if settingsProvider, ok := tool.(pluginapi.SettingsProvider); ok {
		isInitialized := settingsProvider.IsInitialized()

		// Get current settings if the plugin supports it
		var currentSettings map[string]interface{}
		if settingsJSON, err := settingsProvider.GetSettings(); err == nil {
			json.Unmarshal([]byte(settingsJSON), &currentSettings)
		}

		response := map[string]any{
			"supports_initialization": true,
			"is_initialized":          isInitialized,
			"is_legacy_plugin":        true,
			"current_settings":        currentSettings,
		}

		json.NewEncoder(w).Encode(response)
		return
	}

	// Check if plugin implements InitializationProvider (for modern plugins)
	initProvider, ok := tool.(pluginapi.InitializationProvider)
	if !ok {
		// Plugin doesn't support any kind of initialization
		json.NewEncoder(w).Encode(map[string]any{
			"supports_initialization": false,
			"message":                 "Plugin does not support automatic initialization",
		})
		return
	}

	// Get required configuration variables
	configVars := initProvider.GetRequiredConfig()

	// Check if plugin is already initialized (if it supports SettingsProvider)
	isInitialized := false
	if settingsProvider, ok := tool.(pluginapi.SettingsProvider); ok {
		isInitialized = settingsProvider.IsInitialized()
	}

	response := map[string]any{
		"supports_initialization": true,
		"is_initialized":          isInitialized,
		"required_config":         configVars,
	}

	json.NewEncoder(w).Encode(response)
}

func (h *InitHandler) handlePluginInitialization(w http.ResponseWriter, r *http.Request, tool pluginapi.Tool, pluginName, agentName string) {
	w.Header().Set("Content-Type", "application/json")

	// Parse configuration from request body first
	var configData map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&configData); err != nil {
		json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"message": "Invalid JSON in request body: " + err.Error(),
		})
		return
	}

	// Check if plugin implements SettingsProvider (legacy plugins)
	if settingsProvider, ok := tool.(pluginapi.SettingsProvider); ok {
		// For legacy plugins, update settings directly using SetSettings
		settingsJSON, err := json.Marshal(configData)
		if err != nil {
			json.NewEncoder(w).Encode(map[string]any{
				"success": false,
				"message": "Failed to encode settings: " + err.Error(),
			})
			return
		}

		if err := settingsProvider.SetSettings(string(settingsJSON)); err != nil {
			json.NewEncoder(w).Encode(map[string]any{
				"success": false,
				"message": "Failed to update settings: " + err.Error(),
			})
			return
		}

		json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"message": "Plugin settings updated successfully",
		})
		return
	}

	// Check if plugin implements InitializationProvider (modern plugins)
	initProvider, ok := tool.(pluginapi.InitializationProvider)
	if !ok {
		json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"message": "Plugin does not support automatic initialization",
		})
		return
	}

	// Validate configuration
	if err := initProvider.ValidateConfig(configData); err != nil {
		json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"message": "Configuration validation failed: " + err.Error(),
		})
		return
	}

	// Initialize plugin with configuration
	if err := initProvider.InitializeWithConfig(configData); err != nil {
		json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"message": "Plugin initialization failed: " + err.Error(),
		})
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

	json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"message": "Plugin initialized successfully",
	})
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
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if req.PluginName == "" {
		http.Error(w, "plugin_name required", http.StatusBadRequest)
		return
	}

	// Get current agent and its plugins
	_, current := h.store.ListAgents()
	ag, ok := h.store.GetAgent(current)
	if !ok {
		http.Error(w, "current agent not found", http.StatusInternalServerError)
		return
	}

	// Find the plugin
	plugin, exists := ag.Plugins[req.PluginName]
	if !exists {
		http.Error(w, "plugin not found", http.StatusNotFound)
		return
	}

	// Convert parameters to JSON string
	argsJSON, err := json.Marshal(req.Parameters)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to marshal parameters: %v", err), http.StatusBadRequest)
		return
	}

	// Execute the plugin function
	result, err := plugin.Tool.Call(r.Context(), string(argsJSON))
	if err != nil {
		http.Error(w, fmt.Sprintf("plugin execution error: %v", err), http.StatusInternalServerError)
		return
	}

	// Return the result
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"result":  result,
	})
}

// PluginInitStatusHandler checks filesystem for settings files
func (h *InitHandler) PluginInitStatusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
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
		json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"message": "current agent not found",
		})
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

	json.NewEncoder(w).Encode(response)
}