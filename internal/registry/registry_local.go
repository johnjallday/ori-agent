package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/pluginloader"
	"github.com/johnjallday/ori-agent/internal/types"
)

// loadLocalUnlocked loads the user's local plugin registry without locking (internal use only)
func (m *Manager) loadLocalUnlocked() (types.PluginRegistry, error) {
	var reg types.PluginRegistry

	if b, err := os.ReadFile(m.localRegistryPath); err == nil {
		if err := json.Unmarshal(b, &reg); err != nil {
			return reg, fmt.Errorf("failed to parse local plugin registry: %w", err)
		}
	}
	return reg, nil
}

// saveLocalUnlocked saves the local plugin registry to file without locking (internal use only)
func (m *Manager) saveLocalUnlocked(reg types.PluginRegistry) error {
	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal local registry: %w", err)
	}

	if err := os.WriteFile(m.localRegistryPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write local registry: %w", err)
	}

	return nil
}

// LoadLocal loads the user's local plugin registry
func (m *Manager) LoadLocal() (types.PluginRegistry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.loadLocalUnlocked()
}

// SaveLocal saves the local plugin registry to file
func (m *Manager) SaveLocal(reg types.PluginRegistry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.saveLocalUnlocked(reg)
}

// ScanUploadedPlugins scans the uploaded_plugins directory and adds any new plugins to local registry
func (m *Manager) ScanUploadedPlugins() error {
	// Check if uploaded_plugins directory exists
	if _, err := os.Stat(m.uploadedPluginsDir); os.IsNotExist(err) {
		return nil // No uploaded plugins directory, nothing to scan
	}

	// Load current local registry
	localReg, err := m.LoadLocal()
	if err != nil {
		return fmt.Errorf("failed to load local registry: %w", err)
	}

	// Create map of existing plugins for quick lookup
	existingPlugins := make(map[string]bool)
	for _, plugin := range localReg.Plugins {
		existingPlugins[plugin.Path] = true
	}

	// Read uploaded_plugins directory
	entries, err := os.ReadDir(m.uploadedPluginsDir)
	if err != nil {
		return fmt.Errorf("failed to read uploaded_plugins directory: %w", err)
	}

	var newPluginsAdded bool
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		filename := entry.Name()
		// Skip hidden files and non-plugin files
		if strings.HasPrefix(filename, ".") {
			continue
		}

		pluginPath := filepath.Join(m.uploadedPluginsDir, filename)

		// Skip if plugin is already in registry
		if existingPlugins[pluginPath] {
			continue
		}

		// Plugin name is the filename (RPC executables don't have extensions)
		pluginName := filename

		// Try to load the plugin to get better information (using unified loader)
		var description, version string
		var metadata *types.PluginMetadata
		if tool, loadErr := pluginloader.LoadPluginUnified(pluginPath); loadErr == nil {
			def := tool.Definition()
			description = def.Description
			version = pluginloader.GetPluginVersion(tool)

			// Extract metadata if available
			if protoMeta, metaErr := pluginloader.GetPluginMetadata(tool); metaErr == nil && protoMeta != nil {
				// Convert pluginapi.PluginMetadata (proto) to types.PluginMetadata
				maintainers := make([]types.Maintainer, len(protoMeta.Maintainers))
				for i, m := range protoMeta.Maintainers {
					maintainers[i] = types.Maintainer{
						Name:         m.Name,
						Email:        m.Email,
						Organization: m.Organization,
						Website:      m.Website,
						Role:         m.Role,
						Primary:      m.Primary,
					}
				}

				// Convert platforms
				platforms := make([]types.Platform, len(protoMeta.Platforms))
				for i, p := range protoMeta.Platforms {
					platforms[i] = types.Platform{
						Os:            p.Os,
						Architectures: p.Architectures,
					}
				}

				// Convert requirements
				requirements := types.Requirements{
					MinOriVersion: protoMeta.Requirements.GetMinOriVersion(),
					Dependencies:  protoMeta.Requirements.GetDependencies(),
				}

				metadata = &types.PluginMetadata{
					Name:         protoMeta.Name,
					Version:      protoMeta.Version,
					Description:  protoMeta.Description,
					Tags:         normalizeTagsWithWarnings(pluginName, protoMeta.Tags),
					Maintainers:  maintainers,
					License:      protoMeta.License,
					Repository:   protoMeta.Repository,
					Platforms:    platforms,
					Requirements: requirements,
				}
			}

			// Clean up RPC plugins after getting metadata
			pluginloader.CloseRPCPlugin(tool)
		} else {
			// Fall back to parsing plugin.yaml (useful in dev when external plugins are checked out under ../plugins/)
			if manifest, err := loadManifestForUploadedPlugin(pluginName, pluginPath); err == nil {
				if description == "" {
					description = manifest.Description
				}
				if version == "" {
					version = manifest.Version
				}
				metadata = &types.PluginMetadata{
					Name:        manifest.Name,
					Version:     manifest.Version,
					Description: manifest.Description,
					Tags:        normalizeTagsWithWarnings(pluginName, manifest.Tags),
				}
			}
		}

		// Fallback values if loading failed
		if description == "" {
			description = fmt.Sprintf("Plugin: %s", pluginName)
		}
		if version == "" {
			version = "unknown"
		}

		var pluginTags []string
		if metadata != nil {
			pluginTags = metadata.Tags
		}

		// Add to registry
		newPlugin := types.PluginRegistryEntry{
			Name:        pluginName,
			Description: description,
			Tags:        pluginTags,
			Path:        pluginPath,
			Version:     version,
			Metadata:    metadata,
		}

		localReg.Plugins = append(localReg.Plugins, newPlugin)
		newPluginsAdded = true

		fmt.Printf("Auto-registered plugin: %s (%s) from %s\n", pluginName, version, pluginPath)
	}

	// Save updated registry if changes were made
	if newPluginsAdded {
		if err := m.SaveLocal(localReg); err != nil {
			return fmt.Errorf("failed to save updated local registry: %w", err)
		}
		fmt.Printf("Updated local plugin registry with new plugins from uploaded_plugins/\n")
	}

	return nil
}

// RefreshLocalRegistry completely rebuilds the local registry from uploaded_plugins directory
// This refreshes all metadata (version, description) for all plugins
func (m *Manager) RefreshLocalRegistry() error {
	// Check if uploaded_plugins directory exists
	if _, err := os.Stat(m.uploadedPluginsDir); os.IsNotExist(err) {
		// No uploaded plugins directory - create empty registry
		emptyReg := types.PluginRegistry{Plugins: []types.PluginRegistryEntry{}}
		return m.SaveLocal(emptyReg)
	}

	// Create new registry from scratch
	newReg := types.PluginRegistry{
		Plugins: []types.PluginRegistryEntry{},
	}

	// Read uploaded_plugins directory
	entries, err := os.ReadDir(m.uploadedPluginsDir)
	if err != nil {
		return fmt.Errorf("failed to read uploaded_plugins directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		filename := entry.Name()
		// Skip hidden files
		if strings.HasPrefix(filename, ".") {
			continue
		}

		pluginPath := filepath.Join(m.uploadedPluginsDir, filename)
		pluginName := filename

		// Try to load the plugin to get metadata
		var description, version string
		var metadata *types.PluginMetadata
		if tool, loadErr := pluginloader.LoadPluginUnified(pluginPath); loadErr == nil {
			def := tool.Definition()
			description = def.Description
			version = pluginloader.GetPluginVersion(tool)

			// Extract metadata if available
			if protoMeta, metaErr := pluginloader.GetPluginMetadata(tool); metaErr == nil && protoMeta != nil {
				// Convert pluginapi.PluginMetadata (proto) to types.PluginMetadata
				maintainers := make([]types.Maintainer, len(protoMeta.Maintainers))
				for i, m := range protoMeta.Maintainers {
					maintainers[i] = types.Maintainer{
						Name:         m.Name,
						Email:        m.Email,
						Organization: m.Organization,
						Website:      m.Website,
						Role:         m.Role,
						Primary:      m.Primary,
					}
				}

				// Convert platforms
				platforms := make([]types.Platform, len(protoMeta.Platforms))
				for i, p := range protoMeta.Platforms {
					platforms[i] = types.Platform{
						Os:            p.Os,
						Architectures: p.Architectures,
					}
				}

				// Convert requirements
				requirements := types.Requirements{
					MinOriVersion: protoMeta.Requirements.GetMinOriVersion(),
					Dependencies:  protoMeta.Requirements.GetDependencies(),
				}

				metadata = &types.PluginMetadata{
					Name:         protoMeta.Name,
					Version:      protoMeta.Version,
					Description:  protoMeta.Description,
					Tags:         normalizeTagsWithWarnings(pluginName, protoMeta.Tags),
					Maintainers:  maintainers,
					License:      protoMeta.License,
					Repository:   protoMeta.Repository,
					Platforms:    platforms,
					Requirements: requirements,
				}
			}

			// Clean up RPC plugins after getting metadata
			pluginloader.CloseRPCPlugin(tool)
		} else {
			// Fall back to parsing plugin.yaml (useful in dev when external plugins are checked out under ../plugins/)
			if manifest, err := loadManifestForUploadedPlugin(pluginName, pluginPath); err == nil {
				if description == "" {
					description = manifest.Description
				}
				if version == "" {
					version = manifest.Version
				}
				metadata = &types.PluginMetadata{
					Name:        manifest.Name,
					Version:     manifest.Version,
					Description: manifest.Description,
					Tags:        normalizeTagsWithWarnings(pluginName, manifest.Tags),
				}
			}
		}

		// Fallback values if loading failed
		if description == "" {
			description = fmt.Sprintf("Plugin: %s", pluginName)
		}
		if version == "" {
			version = "unknown"
		}

		var pluginTags []string
		if metadata != nil {
			pluginTags = metadata.Tags
		}

		// Add to new registry
		newPlugin := types.PluginRegistryEntry{
			Name:        pluginName,
			Description: description,
			Tags:        pluginTags,
			Path:        pluginPath,
			Version:     version,
			Metadata:    metadata,
		}

		newReg.Plugins = append(newReg.Plugins, newPlugin)
	}

	// Save refreshed registry
	if err := m.SaveLocal(newReg); err != nil {
		return fmt.Errorf("failed to save refreshed local registry: %w", err)
	}

	return nil
}

// ValidateAndUpdateLocal checks that plugins in local registry exist and updates paths if needed
func (m *Manager) ValidateAndUpdateLocal() error {
	// Load current local registry
	localReg, err := m.LoadLocal()
	if err != nil {
		return fmt.Errorf("failed to load local registry: %w", err)
	}

	if len(localReg.Plugins) == 0 {
		return nil // Nothing to validate
	}

	var validPlugins []types.PluginRegistryEntry
	var updated bool

	// Common search locations for plugins
	searchDirs := []string{
		"plugins",
		"uploaded_plugins",
		"example_plugins",
		"../plugins",
		"../uploaded_plugins",
	}

	for _, plugin := range localReg.Plugins {
		// Check if plugin exists at its current path
		if _, err := os.Stat(plugin.Path); err == nil {
			validPlugins = append(validPlugins, plugin)
			continue
		}

		// Plugin doesn't exist at specified path, try to find it
		pluginName := plugin.Name
		found := false
		var newPath string

		// Try each search directory
		for _, dir := range searchDirs {
			// Try with plugin name only
			possiblePath := filepath.Join(dir, pluginName, pluginName)
			if _, err := os.Stat(possiblePath); err == nil {
				newPath = possiblePath
				found = true
				break
			}

			// Try with plugin name directly in directory
			possiblePath = filepath.Join(dir, pluginName)
			if _, err := os.Stat(possiblePath); err == nil {
				newPath = possiblePath
				found = true
				break
			}
		}

		if found {
			fmt.Printf("Updated plugin path: %s -> %s\n", plugin.Path, newPath)
			plugin.Path = newPath
			validPlugins = append(validPlugins, plugin)
			updated = true
		} else {
			fmt.Printf("Plugin not found, removing from registry: %s (was at %s)\n", plugin.Name, plugin.Path)
			updated = true
		}
	}

	// Save updated registry if changes were made
	if updated {
		localReg.Plugins = validPlugins
		if err := m.SaveLocal(localReg); err != nil {
			return fmt.Errorf("failed to save updated local registry: %w", err)
		}
		fmt.Printf("Updated local plugin registry (validated %d plugins, %d valid)\n", len(localReg.Plugins), len(validPlugins))
	}

	return nil
}
