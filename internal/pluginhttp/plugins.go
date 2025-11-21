package pluginhttp

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/pluginapi"

	"github.com/johnjallday/ori-agent/internal/healthhttp"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/pluginloader"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

// ToolLoader abstracts loading a plugin Tool from a path.
type ToolLoader interface {
	Load(path string) (pluginapi.PluginTool, error)
}

// NativeLoader uses the unified plugin loader to load both .so files and RPC executables.
type NativeLoader struct{}

func (NativeLoader) Load(path string) (pluginapi.PluginTool, error) {
	return pluginloader.LoadPluginUnified(path)
}

// Handler serves /api/plugins (GET list, POST upload+load, DELETE unload).
type Handler struct {
	State         store.Store
	Loader        ToolLoader
	LocalRegistry *LocalRegistry
	EnumExtractor *EnumExtractor
	HealthManager *healthhttp.Manager
}

func New(state store.Store, loader ToolLoader) *Handler {
	return &Handler{
		State:         state,
		Loader:        loader,
		LocalRegistry: NewLocalRegistry(),
		EnumExtractor: NewEnumExtractor(),
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Handle special save-settings endpoint
	if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/save-settings") {
		h.saveSettings(w, r)
		return
	}

	// Handle special upload-config endpoint
	if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/upload-config") {
		h.uploadConfig(w, r)
		return
	}

	switch r.Method {

	case http.MethodGet:
		h.list(w, r)

	case http.MethodPost:
		// Parse multipart form to check if it contains actual file uploads
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			// If parsing multipart fails, try regular form parsing for registry loading
			h.loadFromRegistry(w, r)
			return
		}

		// Check if this request contains actual file uploads
		if _, _, err := r.FormFile("plugin"); err == nil {
			// Has file upload - use upload handler
			h.uploadAndRegister(w, r)
		} else {
			// No file upload, just form data - use registry loader
			h.loadFromRegistry(w, r)
		}

	case http.MethodDelete:
		h.unload(w, r)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *Handler) list(w http.ResponseWriter, _ *http.Request) {
	// Get enabled plugins for current agent
	_, current := h.State.ListAgents()

	// Create map of enabled plugins (empty if agent doesn't exist yet)
	enabledPlugins := make(map[string]bool)
	if ag, ok := h.State.GetAgent(current); ok {
		for name := range ag.Plugins {
			enabledPlugins[name] = true
		}
	}

	// Load local registry to get all available plugins
	registryPath := "local_plugin_registry.json"
	var localReg types.PluginRegistry

	data, err := os.ReadFile(registryPath)
	if err != nil || json.Unmarshal(data, &localReg) != nil {
		// Fallback to just enabled plugins if registry fails
		plist := make([]map[string]any, 0)
		if ag, ok := h.State.GetAgent(current); ok {
			for name, pl := range ag.Plugins {
				// Check if plugin supports initialization
				_, supportsInit := pl.Tool.(pluginapi.InitializationProvider)

				// Check if plugin binary exists in uploaded_plugins
				isInstalled := h.isPluginInstalled(name, pl.Path)

				plist = append(plist, map[string]any{
					"name":                    name,
					"description":             pl.Definition.Description,
					"definition":              pl.Definition,
					"path":                    pl.Path,
					"version":                 pl.Version,
					"enabled":                 true,
					"installed":               isInstalled,
					"supports_initialization": supportsInit,
				})
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"plugins": plist})
		return
	}

	// Build list of all available plugins from registry
	plist := make([]map[string]any, 0, len(localReg.Plugins))

	// Get current agent for checking loaded plugins
	ag, agentExists := h.State.GetAgent(current)

	for _, registryPlugin := range localReg.Plugins {
		// Check if plugin binary is installed (exists in uploaded_plugins/)
		isInstalled := h.isPluginInstalled(registryPlugin.Name, registryPlugin.Path)

		// Check if plugin is enabled (only if agent exists)
		var loadedPlugin *types.LoadedPlugin
		isEnabled := false
		if agentExists {
			if lp, exists := ag.Plugins[registryPlugin.Name]; exists {
				loadedPlugin = &lp
				isEnabled = true
			}
		}

		// If plugin is enabled, include its full definition
		if isEnabled && loadedPlugin != nil {
			// Check if plugin supports initialization and get config variables
			var requiresSettings bool
			var settingVariables []pluginapi.ConfigVariable

			if loadedPlugin.Tool != nil {
				if initProvider, ok := loadedPlugin.Tool.(pluginapi.InitializationProvider); ok {
					settingVariables = initProvider.GetRequiredConfig()
					requiresSettings = len(settingVariables) > 0
				}
			}

			pluginInfo := map[string]any{
				"name":                    registryPlugin.Name,
				"description":             registryPlugin.Description,
				"path":                    registryPlugin.Path,
				"version":                 registryPlugin.Version,
				"enabled":                 true,
				"installed":               isInstalled,
				"definition":              loadedPlugin.Definition,
				"supports_initialization": requiresSettings,
				"requires_settings":       requiresSettings,
				"setting_variables":       settingVariables,
				"metadata":                registryPlugin.Metadata,
			}
			plist = append(plist, pluginInfo)
		} else {
			// Plugin not enabled - temporarily load to check if it supports initialization
			var requiresSettings bool
			var settingVariables []pluginapi.ConfigVariable

			logger.Verbosef("🔄 Temporarily loading plugin '%s' from path: %s", registryPlugin.Name, registryPlugin.Path)
			if tool, err := h.Loader.Load(registryPlugin.Path); err == nil {
				logger.Verbosef("✓ Plugin loaded, type: %T", tool)
				logger.Verbosef("✓ Checking InitializationProvider interface...")
				if initProvider, ok := tool.(pluginapi.InitializationProvider); ok {
					logger.Verbosef("✅ Plugin implements InitializationProvider!")
					settingVariables = initProvider.GetRequiredConfig()
					logger.Verbosef("✅ Got %d setting variables", len(settingVariables))
					for i, sv := range settingVariables {
						fmt.Printf("  [%d] %s (%s): %s", i, sv.Key, sv.Type, sv.Description)
					}
					requiresSettings = len(settingVariables) > 0
				} else {
					logger.Verbosef("❌ Plugin does NOT implement InitializationProvider (type: %T)", tool)
				}
			} else {
				logger.Verbosef("❌ Failed to load plugin: %v", err)
			}

			pluginInfo := map[string]any{
				"name":                    registryPlugin.Name,
				"description":             registryPlugin.Description,
				"path":                    registryPlugin.Path,
				"version":                 registryPlugin.Version,
				"enabled":                 false,
				"installed":               isInstalled,
				"supports_initialization": requiresSettings,
				"requires_settings":       requiresSettings,
				"setting_variables":       settingVariables,
				"metadata":                registryPlugin.Metadata,
			}
			plist = append(plist, pluginInfo)
		}
	}

	_ = json.NewEncoder(w).Encode(map[string]any{"plugins": plist})
}

func (h *Handler) uploadAndRegister(w http.ResponseWriter, r *http.Request) {
	// Form should already be parsed in ServeHTTP, but ensure it's parsed
	if r.MultipartForm == nil {
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	file, header, err := r.FormFile("plugin")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Create a permanent directory for uploaded plugins
	uploadsDir := "uploaded_plugins"
	if err := os.MkdirAll(uploadsDir, 0755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	pluginFile := filepath.Join(uploadsDir, header.Filename)
	out, err := os.Create(pluginFile)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := io.Copy(out, file); err != nil {
		out.Close()
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out.Close()

	// Make the plugin executable (required for RPC plugins)
	if err := os.Chmod(pluginFile, 0755); err != nil {
		os.Remove(pluginFile)
		http.Error(w, "Failed to set plugin permissions: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Load plugin to get its definition and validate it
	tool, err := h.Loader.Load(pluginFile)
	if err != nil {
		// Clean up the file if plugin is invalid
		os.Remove(pluginFile)
		http.Error(w, "Invalid plugin: "+err.Error(), http.StatusBadRequest)
		return
	}
	def := tool.Definition()

	// Extract version information if available
	version := pluginloader.GetPluginVersion(tool)

	// Add to plugin registry
	if err := h.LocalRegistry.AddToRegistry(def.Name, def.Description, pluginFile, version); err != nil {
		// Clean up the file if registry update fails
		os.Remove(pluginFile)
		http.Error(w, "Failed to register plugin: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Return success with plugin info
	response := map[string]any{
		"success":     true,
		"name":        def.Name,
		"description": def.Description,
		"path":        pluginFile,
		"version":     version,
		"message":     "Plugin uploaded and registered successfully. You can now load it from the registry.",
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("Failed to encode response: %v", err)
	}
}

func (h *Handler) loadFromRegistry(w http.ResponseWriter, r *http.Request) {
	// Try to parse multipart form first, then regular form
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}

	name := r.FormValue("name")
	path := r.FormValue("path")

	if name == "" || path == "" {
		http.Error(w, "name and path required", http.StatusBadRequest)
		return
	}

	// Load plugin from the specified path
	tool, err := h.Loader.Load(path)
	if err != nil {
		http.Error(w, "Failed to load plugin: "+err.Error(), http.StatusInternalServerError)
		return
	}

	def := tool.Definition()
	version := pluginloader.GetPluginVersion(tool)

	// Get current agent
	_, current := h.State.ListAgents()
	ag, ok := h.State.GetAgent(current)
	if !ok {
		http.Error(w, "current agent not found", http.StatusInternalServerError)
		return
	}

	// Add plugin to current agent
	if ag.Plugins == nil {
		ag.Plugins = make(map[string]types.LoadedPlugin)
	}

	logger.Verbosef("💾 Adding plugin '%s' to agent '%s' (current plugins: %d)", name, current, len(ag.Plugins))

	ag.Plugins[name] = types.LoadedPlugin{
		Tool:       tool,
		Definition: def,
		Path:       path,
		Version:    version,
	}

	logger.Verbosef("💾 Agent now has %d plugins", len(ag.Plugins))

	// Save updated agent
	logger.Verbosef("💾 Saving agent '%s' to state...", current)
	if err := h.State.SetAgent(current, ag); err != nil {
		logger.Verbosef("❌ Failed to save agent: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	logger.Verbosef("✅ Agent saved successfully")

	// Check if plugin has required configuration fields
	showConfigModal := false
	if initProvider, ok := tool.(pluginapi.InitializationProvider); ok {
		configVars := initProvider.GetRequiredConfig()
		for _, cv := range configVars {
			if cv.Required {
				showConfigModal = true
				break
			}
		}
	}

	// Return success response
	response := map[string]any{
		"success":           true,
		"name":              name,
		"description":       def.Description,
		"path":              path,
		"version":           version,
		"message":           "Plugin loaded successfully from registry",
		"show_config_modal": showConfigModal,
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("Failed to encode response: %v", err)
	}
}

func (h *Handler) unload(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}
	_, current := h.State.ListAgents()
	ag, ok := h.State.GetAgent(current)
	if !ok {
		http.Error(w, "current agent not found", http.StatusInternalServerError)
		return
	}
	delete(ag.Plugins, name)
	if err := h.State.SetAgent(current, ag); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// GetPluginEnums extracts enum values from a specific plugin's function definition
func (h *Handler) GetPluginEnums(pluginName string) (map[string][]string, error) {
	_, current := h.State.ListAgents()
	ag, ok := h.State.GetAgent(current)
	if !ok {
		return nil, fmt.Errorf("current agent not found")
	}

	plugin, exists := ag.Plugins[pluginName]
	if !exists {
		return nil, fmt.Errorf("plugin %s not found", pluginName)
	}

	// Get fresh definition to return latest dynamic enums
	def := plugin.Definition
	if plugin.Tool != nil {
		def = plugin.Tool.Definition()
	}

	return h.EnumExtractor.GetAllEnumsFromParameter(def)
}

// GetPluginConfig gets configuration requirements from a specific plugin
func (h *Handler) GetPluginConfig(pluginName string) ([]pluginapi.ConfigVariable, bool, error) {
	fmt.Printf("🔍 GetPluginConfig called for plugin: %s\n", pluginName)

	names, current := h.State.ListAgents()
	logger.Verbosef("📋 Available agents: %v, current: %s", names, current)

	ag, ok := h.State.GetAgent(current)
	if !ok {
		logger.Verbosef("❌ Current agent '%s' not found", current)
		return nil, false, fmt.Errorf("current agent not found")
	}

	logger.Verbosef("📦 Agent has %d plugins: %v", len(ag.Plugins), func() []string {
		keys := make([]string, 0, len(ag.Plugins))
		for k := range ag.Plugins {
			keys = append(keys, k)
		}
		return keys
	}())

	plugin, exists := ag.Plugins[pluginName]
	if !exists {
		logger.Verbosef("❌ Plugin '%s' not found in agent. Available: %v", pluginName, func() []string {
			keys := make([]string, 0, len(ag.Plugins))
			for k := range ag.Plugins {
				keys = append(keys, k)
			}
			return keys
		}())
		return nil, false, fmt.Errorf("plugin %s not found", pluginName)
	}

	logger.Verbosef("✓ Plugin found: %s, Path: %s, Tool: %v", pluginName, plugin.Path, plugin.Tool != nil)

	// If Tool is not loaded, load it now
	var tool pluginapi.PluginTool
	if plugin.Tool != nil {
		logger.Verbosef("✓ Using already loaded tool")
		tool = plugin.Tool
	} else if plugin.Path != "" {
		logger.Verbosef("🔄 Loading plugin from path: %s", plugin.Path)
		// Load plugin from path
		loadedTool, err := h.Loader.Load(plugin.Path)
		if err != nil {
			logger.Verbosef("❌ Failed to load plugin: %v", err)
			return nil, false, fmt.Errorf("failed to load plugin: %w", err)
		}
		tool = loadedTool
		logger.Verbosef("✓ Plugin loaded successfully")
	} else {
		logger.Verbosef("❌ Plugin has no tool instance or path")
		return nil, false, fmt.Errorf("plugin has no tool instance or path")
	}

	// Check if plugin implements InitializationProvider
	if initProvider, ok := tool.(pluginapi.InitializationProvider); ok {
		logger.Verbosef("✓ Plugin implements InitializationProvider")
		configVars := initProvider.GetRequiredConfig()
		logger.Verbosef("✓ Got %d config variables", len(configVars))
		return configVars, true, nil
	}

	logger.Verbosef("❌ Plugin does NOT implement InitializationProvider")
	// Plugin doesn't support initialization
	return nil, false, nil
}

// ValidatePluginEnumValue validates an enum value for a specific plugin and property
func (h *Handler) ValidatePluginEnumValue(pluginName, propertyName, value string) (bool, error) {
	_, current := h.State.ListAgents()
	ag, ok := h.State.GetAgent(current)
	if !ok {
		return false, fmt.Errorf("current agent not found")
	}

	plugin, exists := ag.Plugins[pluginName]
	if !exists {
		return false, fmt.Errorf("plugin %s not found", pluginName)
	}

	// Get fresh definition to validate against latest dynamic enums
	def := plugin.Definition
	if plugin.Tool != nil {
		def = plugin.Tool.Definition()
	}

	return h.EnumExtractor.ValidateEnumValue(def, propertyName, value)
}

func (h *Handler) saveSettings(w http.ResponseWriter, r *http.Request) {
	// Parse JSON body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	var req struct {
		PluginName string            `json:"plugin_name"`
		Settings   map[string]string `json:"settings"`
	}

	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "Failed to parse JSON", http.StatusBadRequest)
		return
	}

	if req.PluginName == "" {
		http.Error(w, "plugin_name required", http.StatusBadRequest)
		return
	}

	// Normalize plugin name: OpenAI function names use underscores, but plugin names use hyphens
	// Convert ori_reaper -> ori-reaper
	pluginName := strings.ReplaceAll(req.PluginName, "_", "-")

	// Get current agent
	_, current := h.State.ListAgents()

	// Normalize settings keys: Frontend may send display names, but we need to save config keys
	// Get the plugin's config schema to build a mapping
	normalizedSettings := make(map[string]string)
	if configVars, supportsInit, err := h.GetPluginConfig(pluginName); err == nil && supportsInit {
		// Build a mapping from display names to keys, and store default values
		nameToKey := make(map[string]string)
		keyToDefault := make(map[string]string)
		for _, cv := range configVars {
			nameToKey[cv.Name] = cv.Key
			nameToKey[cv.Key] = cv.Key // Also map key to itself for idempotency

			// Store default value for this key
			if cv.DefaultValue != nil {
				if defaultStr, ok := cv.DefaultValue.(string); ok {
					keyToDefault[cv.Key] = defaultStr
				}
			}
		}

		// Transform the settings to use correct keys
		for k, v := range req.Settings {
			if correctKey, ok := nameToKey[k]; ok {
				normalizedSettings[correctKey] = v
			} else {
				// If no mapping found, use the key as-is (might be already correct)
				normalizedSettings[k] = v
			}
		}

		// Fill in default values for empty fields
		for key, defaultValue := range keyToDefault {
			if normalizedSettings[key] == "" {
				normalizedSettings[key] = defaultValue
				logger.Verbosef("Using default value for %s: %s", key, defaultValue)
			}
		}
	} else {
		// Plugin doesn't support initialization, just use settings as-is
		normalizedSettings = req.Settings
	}

	// Create agent directory if it doesn't exist
	agentDir := filepath.Join("agents", current)
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		http.Error(w, fmt.Sprintf("Failed to create agent directory: %v", err), http.StatusInternalServerError)
		return
	}

	// Save settings to plugin-specific file using normalized name
	settingsFileName := fmt.Sprintf("%s_settings.json", pluginName)
	settingsPath := filepath.Join(agentDir, settingsFileName)

	// Convert normalized settings to JSON
	settingsData, err := json.MarshalIndent(normalizedSettings, "", "  ")
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to marshal settings: %v", err), http.StatusInternalServerError)
		return
	}

	if err := os.WriteFile(settingsPath, settingsData, 0644); err != nil {
		http.Error(w, fmt.Sprintf("Failed to write settings file: %v", err), http.StatusInternalServerError)
		return
	}

	logger.Verbosef("Saved plugin settings for %s to %s", pluginName, settingsPath)

	// Reload the plugin completely to regenerate tool definition with new settings
	// This ensures the script enum in Definition() reflects the new scripts directory
	ag, ok := h.State.GetAgent(current)
	if ok {
		if plugin, exists := ag.Plugins[pluginName]; exists && plugin.Path != "" {
			logger.Verbosef("🔄 Reloading plugin %s to apply new settings...", pluginName)

			// Kill old plugin process if it's an RPC plugin
			if rpcPlugin, ok := plugin.Tool.(interface{ Kill() }); ok {
				rpcPlugin.Kill()
			}

			// Reload the plugin from disk
			newTool, err := h.Loader.Load(plugin.Path)
			if err != nil {
				logger.Verbosef("⚠️ Warning: Failed to reload plugin: %v", err)
				// Don't fail the request, settings file was saved successfully
			} else {
				// Update the tool in the plugin
				plugin.Tool = newTool
				plugin.Definition = newTool.Definition()

				// Convert normalizedSettings from map[string]string to map[string]interface{}
				configMap := make(map[string]interface{})
				for k, v := range normalizedSettings {
					configMap[k] = v
				}

				// Initialize the new plugin instance with the saved settings
				if initProvider, ok := newTool.(pluginapi.InitializationProvider); ok {
					if err := initProvider.InitializeWithConfig(configMap); err != nil {
						logger.Verbosef("⚠️ Warning: Failed to initialize reloaded plugin: %v", err)
					}
				}

				// Set agent context if plugin supports it
				if agentAware, ok := newTool.(pluginapi.AgentAwareTool); ok {
					agentDir := filepath.Join("agents", current)
					agentContext := pluginapi.AgentContext{
						Name:         current,
						ConfigPath:   filepath.Join(agentDir, "config.json"),
						SettingsPath: filepath.Join(agentDir, "agent_settings.json"),
						AgentDir:     agentDir,
					}
					agentAware.SetAgentContext(agentContext)
				}

				// Update agent state
				ag.Plugins[pluginName] = plugin
				if err := h.State.SetAgent(current, ag); err != nil {
					logger.Verbosef("⚠️ Warning: Failed to save agent state: %v", err)
				} else {
					logger.Verbosef("✅ Successfully reloaded plugin %s with new settings", pluginName)
				}
			}
		}
	}

	// Return success response
	response := map[string]any{
		"success": true,
		"message": fmt.Sprintf("Settings saved for plugin %s", pluginName),
		"path":    settingsPath,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("Failed to encode response: %v", err)
	}
}

func (h *Handler) uploadConfig(w http.ResponseWriter, r *http.Request) {
	// Parse JSON body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	var req struct {
		PluginName string                 `json:"plugin_name"`
		Config     map[string]interface{} `json:"config"`
		Filename   string                 `json:"filename"`
		FieldName  string                 `json:"field_name"`
	}

	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "Failed to parse JSON", http.StatusBadRequest)
		return
	}

	if req.PluginName == "" {
		http.Error(w, "plugin_name required", http.StatusBadRequest)
		return
	}

	if req.Filename == "" {
		http.Error(w, "filename required", http.StatusBadRequest)
		return
	}

	// Get current agent
	_, current := h.State.ListAgents()

	// Create agent directory if it doesn't exist
	agentDir := filepath.Join("agents", current)
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		http.Error(w, fmt.Sprintf("Failed to create agent directory: %v", err), http.StatusInternalServerError)
		return
	}

	// Save uploaded config to file
	configPath := filepath.Join(agentDir, req.Filename)

	// Convert config to JSON
	configData, err := json.MarshalIndent(req.Config, "", "  ")
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to marshal config: %v", err), http.StatusInternalServerError)
		return
	}

	if err := os.WriteFile(configPath, configData, 0644); err != nil {
		http.Error(w, fmt.Sprintf("Failed to write config file: %v", err), http.StatusInternalServerError)
		return
	}

	logger.Verbosef("Uploaded config file %s for plugin %s to %s", req.Filename, req.PluginName, configPath)

	// Return success response
	response := map[string]any{
		"success":    true,
		"message":    fmt.Sprintf("Config file %s uploaded successfully for plugin %s", req.Filename, req.PluginName),
		"saved_path": configPath,
		"filename":   req.Filename,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("Failed to encode response: %v", err)
	}
}

// isPluginInstalled checks if a plugin binary exists in the uploaded_plugins directory
func (h *Handler) isPluginInstalled(pluginName, pluginPath string) bool {
	// If path is empty, check by plugin name in uploaded_plugins
	if pluginPath == "" {
		uploadedPath := filepath.Join("uploaded_plugins", pluginName)
		if _, err := os.Stat(uploadedPath); err == nil {
			return true
		}
		return false
	}

	// Check if the plugin path contains uploaded_plugins directory
	if strings.Contains(pluginPath, "uploaded_plugins/") || strings.Contains(pluginPath, "uploaded_plugins"+string(filepath.Separator)) {
		// Check if file exists at the specified path
		if _, err := os.Stat(pluginPath); err == nil {
			return true
		}
	}

	// Also check by plugin name in uploaded_plugins directory
	// This handles cases where the plugin might have been moved
	uploadedPath := filepath.Join("uploaded_plugins", pluginName)
	if _, err := os.Stat(uploadedPath); err == nil {
		return true
	}

	return false
}
