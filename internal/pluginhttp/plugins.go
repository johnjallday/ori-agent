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
		h.uploadAndRegister(w, r)

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
	plist := make([]map[string]string, 0, len(ag.Plugins))
	for name, pl := range ag.Plugins {
		plist = append(plist, map[string]string{
			"name":        name,
			"description": pl.Definition.Description.String(),
		})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"plugins": plist})
}

func (h *Handler) uploadAndRegister(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
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

	// Add to plugin registry
	if err := h.addToRegistry(def.Name, def.Description.String(), pluginFile); err != nil {
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
		"message":     "Plugin uploaded and registered successfully. You can now load it from the registry.",
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (h *Handler) addToRegistry(name, description, path string) error {
	// Read current registry
	registryPath := "plugin_registry.json"
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
			return h.saveRegistry(registryPath, registry)
		}
	}

	// Add new entry
	newEntry := types.PluginRegistryEntry{
		Name:        name,
		Description: description,
		Path:        path,
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
