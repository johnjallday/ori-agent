package pluginhttp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/pluginapi"

	"github.com/johnjallday/ori-agent/internal/pluginloader"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

// ToolLoader abstracts loading a plugin Tool from a path.
type ToolLoader interface {
	Load(path string) (pluginapi.Tool, error)
}

// NativeLoader uses the unified plugin loader to load both .so files and RPC executables.
type NativeLoader struct{}

func (NativeLoader) Load(path string) (pluginapi.Tool, error) {
	return pluginloader.LoadPluginUnified(path)
}

// Handler serves /api/plugins (GET list, POST upload+load, DELETE unload).
type Handler struct {
	State         store.Store
	Loader        ToolLoader
	LocalRegistry *LocalRegistry
	EnumExtractor *EnumExtractor
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

				plist = append(plist, map[string]any{
					"name":                    name,
					"description":             pl.Definition.Description.String(),
					"definition":              pl.Definition,
					"path":                    pl.Path,
					"version":                 pl.Version,
					"enabled":                 true,
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
				"definition":              loadedPlugin.Definition,
				"supports_initialization": requiresSettings,
				"requires_settings":       requiresSettings,
				"setting_variables":       settingVariables,
			}
			plist = append(plist, pluginInfo)
		} else {
			// Plugin not enabled - temporarily load to check if it supports initialization
			var requiresSettings bool
			var settingVariables []pluginapi.ConfigVariable

			fmt.Printf("🔄 Temporarily loading plugin '%s' from path: %s\n", registryPlugin.Name, registryPlugin.Path)
			if tool, err := h.Loader.Load(registryPlugin.Path); err == nil {
				fmt.Printf("✓ Plugin loaded, type: %T\n", tool)
				fmt.Printf("✓ Checking InitializationProvider interface...\n")
				if initProvider, ok := tool.(pluginapi.InitializationProvider); ok {
					fmt.Printf("✅ Plugin implements InitializationProvider!\n")
					settingVariables = initProvider.GetRequiredConfig()
					fmt.Printf("✅ Got %d setting variables\n", len(settingVariables))
					for i, sv := range settingVariables {
						fmt.Printf("  [%d] %s (%s): %s\n", i, sv.Key, sv.Type, sv.Description)
					}
					requiresSettings = len(settingVariables) > 0
				} else {
					fmt.Printf("❌ Plugin does NOT implement InitializationProvider (type: %T)\n", tool)
				}
			} else {
				fmt.Printf("❌ Failed to load plugin: %v\n", err)
			}

			pluginInfo := map[string]any{
				"name":                    registryPlugin.Name,
				"description":             registryPlugin.Description,
				"path":                    registryPlugin.Path,
				"version":                 registryPlugin.Version,
				"enabled":                 false,
				"supports_initialization": requiresSettings,
				"requires_settings":       requiresSettings,
				"setting_variables":       settingVariables,
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
	if err := h.LocalRegistry.AddToRegistry(def.Name, def.Description.String(), pluginFile, version); err != nil {
		// Clean up the file if registry update fails
		os.Remove(pluginFile)
		http.Error(w, "Failed to register plugin: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Return success with plugin info
	response := map[string]any{
		"success":     true,
		"name":        def.Name,
		"description": def.Description.String(),
		"path":        pluginFile,
		"version":     version,
		"message":     "Plugin uploaded and registered successfully. You can now load it from the registry.",
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
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

	fmt.Printf("💾 Adding plugin '%s' to agent '%s' (current plugins: %d)\n", name, current, len(ag.Plugins))

	ag.Plugins[name] = types.LoadedPlugin{
		Tool:       tool,
		Definition: def,
		Path:       path,
		Version:    version,
	}

	fmt.Printf("💾 Agent now has %d plugins\n", len(ag.Plugins))

	// Save updated agent
	fmt.Printf("💾 Saving agent '%s' to state...\n", current)
	if err := h.State.SetAgent(current, ag); err != nil {
		fmt.Printf("❌ Failed to save agent: %v\n", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	fmt.Printf("✅ Agent saved successfully\n")
	
	// Return success response
	response := map[string]any{
		"success":     true,
		"name":        name,
		"description": def.Description.String(),
		"path":        path,
		"version":     version,
		"message":     "Plugin loaded successfully from registry",
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
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
	fmt.Printf("📋 Available agents: %v, current: %s\n", names, current)

	ag, ok := h.State.GetAgent(current)
	if !ok {
		fmt.Printf("❌ Current agent '%s' not found\n", current)
		return nil, false, fmt.Errorf("current agent not found")
	}

	fmt.Printf("📦 Agent has %d plugins: %v\n", len(ag.Plugins), func() []string {
		keys := make([]string, 0, len(ag.Plugins))
		for k := range ag.Plugins {
			keys = append(keys, k)
		}
		return keys
	}())

	plugin, exists := ag.Plugins[pluginName]
	if !exists {
		fmt.Printf("❌ Plugin '%s' not found in agent. Available: %v\n", pluginName, func() []string {
			keys := make([]string, 0, len(ag.Plugins))
			for k := range ag.Plugins {
				keys = append(keys, k)
			}
			return keys
		}())
		return nil, false, fmt.Errorf("plugin %s not found", pluginName)
	}

	fmt.Printf("✓ Plugin found: %s, Path: %s, Tool: %v\n", pluginName, plugin.Path, plugin.Tool != nil)

	// If Tool is not loaded, load it now
	var tool pluginapi.Tool
	if plugin.Tool != nil {
		fmt.Printf("✓ Using already loaded tool\n")
		tool = plugin.Tool
	} else if plugin.Path != "" {
		fmt.Printf("🔄 Loading plugin from path: %s\n", plugin.Path)
		// Load plugin from path
		loadedTool, err := h.Loader.Load(plugin.Path)
		if err != nil {
			fmt.Printf("❌ Failed to load plugin: %v\n", err)
			return nil, false, fmt.Errorf("failed to load plugin: %w", err)
		}
		tool = loadedTool
		fmt.Printf("✓ Plugin loaded successfully\n")
	} else {
		fmt.Printf("❌ Plugin has no tool instance or path\n")
		return nil, false, fmt.Errorf("plugin has no tool instance or path")
	}

	// Check if plugin implements InitializationProvider
	if initProvider, ok := tool.(pluginapi.InitializationProvider); ok {
		fmt.Printf("✓ Plugin implements InitializationProvider\n")
		configVars := initProvider.GetRequiredConfig()
		fmt.Printf("✓ Got %d config variables\n", len(configVars))
		return configVars, true, nil
	}

	fmt.Printf("❌ Plugin does NOT implement InitializationProvider\n")
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

	// Get current agent
	_, current := h.State.ListAgents()

	// Create agent directory if it doesn't exist
	agentDir := filepath.Join("agents", current)
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		http.Error(w, fmt.Sprintf("Failed to create agent directory: %v", err), http.StatusInternalServerError)
		return
	}

	// Save settings to plugin-specific file
	settingsFileName := fmt.Sprintf("%s_settings.json", req.PluginName)
	settingsPath := filepath.Join(agentDir, settingsFileName)

	// Convert user settings to JSON
	settingsData, err := json.MarshalIndent(req.Settings, "", "  ")
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to marshal settings: %v", err), http.StatusInternalServerError)
		return
	}

	if err := os.WriteFile(settingsPath, settingsData, 0644); err != nil {
		http.Error(w, fmt.Sprintf("Failed to write settings file: %v", err), http.StatusInternalServerError)
		return
	}

	fmt.Printf("Saved plugin settings for %s to %s\n", req.PluginName, settingsPath)

	// Return success response
	response := map[string]any{
		"success": true,
		"message": fmt.Sprintf("Settings saved for plugin %s", req.PluginName),
		"path":    settingsPath,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
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

	fmt.Printf("Uploaded config file %s for plugin %s to %s\n", req.Filename, req.PluginName, configPath)

	// Return success response
	response := map[string]any{
		"success":    true,
		"message":    fmt.Sprintf("Config file %s uploaded successfully for plugin %s", req.Filename, req.PluginName),
		"saved_path": configPath,
		"filename":   req.Filename,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
