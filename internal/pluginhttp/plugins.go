package pluginhttp

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/johnjallday/dolphin-agent/pluginapi"

	"github.com/johnjallday/dolphin-agent/internal/pluginloader"
	"github.com/johnjallday/dolphin-agent/internal/store"
	"github.com/johnjallday/dolphin-agent/internal/types"
)

// ToolLoader abstracts loading a plugin Tool from a path.
type ToolLoader interface {
	Load(path string) (pluginapi.Tool, error)
}

// NativeLoader uses the Go "plugin" package to load .so files.
type NativeLoader struct{}

func (NativeLoader) Load(path string) (pluginapi.Tool, error) {
	return pluginloader.LoadWithCache(path)
}

// Handler serves /api/plugins (GET list, POST upload+load, DELETE unload).
type Handler struct {
	State  store.Store
	Loader ToolLoader
}

func New(state store.Store, loader ToolLoader) *Handler {
	return &Handler{State: state, Loader: loader}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	_, current := h.State.ListAgents()
	ag, ok := h.State.GetAgent(current)
	if !ok {
		http.Error(w, "current agent not found", http.StatusInternalServerError)
		return
	}
	plist := make([]map[string]any, 0, len(ag.Plugins))
	for name, pl := range ag.Plugins {
		plist = append(plist, map[string]any{
			"name":        name,
			"description": pl.Definition.Description.String(),
			"definition":  pl.Definition,
			"path":        pl.Path,
			"version":     pl.Version,
		})
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
	if err := h.addToRegistry(def.Name, def.Description.String(), pluginFile, version); err != nil {
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

func (h *Handler) addToRegistry(name, description, path, version string) error {
	// Read current local registry (user uploaded plugins)
	registryPath := "local_plugin_registry.json"
	var registry types.PluginRegistry

	if data, err := os.ReadFile(registryPath); err == nil {
		if err := json.Unmarshal(data, &registry); err != nil {
			return err
		}
	}

	// Check if plugin already exists in registry
	for i, plugin := range registry.Plugins {
		if plugin.Name == name {
			// Update existing entry
			registry.Plugins[i].Path = path
			registry.Plugins[i].Description = description
			registry.Plugins[i].Version = version
			return h.saveRegistry(registryPath, registry)
		}
	}

	// Add new entry
	newEntry := types.PluginRegistryEntry{
		Name:        name,
		Description: description,
		Path:        path,
		Version:     version,
	}
	registry.Plugins = append(registry.Plugins, newEntry)

	return h.saveRegistry(registryPath, registry)
}

func (h *Handler) saveRegistry(path string, registry types.PluginRegistry) error {
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
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
	
	ag.Plugins[name] = types.LoadedPlugin{
		Tool:       tool,
		Definition: def,
		Path:       path,
		Version:    version,
	}
	
	// Save updated agent
	if err := h.State.SetAgent(current, ag); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	
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
