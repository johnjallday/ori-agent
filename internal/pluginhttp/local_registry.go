package pluginhttp

import (
	"encoding/json"
	"os"

	"github.com/johnjallday/ori-agent/internal/types"
)

// LocalRegistry manages the local plugin registry for uploaded plugins
type LocalRegistry struct{}

// NewLocalRegistry creates a new local registry manager
func NewLocalRegistry() *LocalRegistry {
	return &LocalRegistry{}
}

// AddToRegistry adds a plugin to the local registry
func (lr *LocalRegistry) AddToRegistry(name, description, path, version string) error {
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
			return lr.saveRegistry(registryPath, registry)
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

	return lr.saveRegistry(registryPath, registry)
}

// saveRegistry saves the registry to the specified path
func (lr *LocalRegistry) saveRegistry(path string, registry types.PluginRegistry) error {
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
