package registry

import (
	"fmt"
	"time"

	"github.com/johnjallday/ori-agent/internal/types"
)

// GetPluginByName retrieves a plugin entry by name from the local registry
func (m *Manager) GetPluginByName(pluginName string) (*types.PluginRegistryEntry, error) {
	localReg, err := m.LoadLocal()
	if err != nil {
		return nil, fmt.Errorf("failed to load local registry: %w", err)
	}

	if idx := findPluginIndexByName(localReg.Plugins, pluginName); idx >= 0 {
		return &localReg.Plugins[idx], nil
	}

	return nil, fmt.Errorf("plugin not found: %s", pluginName)
}

// UpdatePluginCategory updates the category for a plugin in the local registry
func (m *Manager) UpdatePluginCategory(pluginName, category string) error {
	localReg, err := m.LoadLocal()
	if err != nil {
		return fmt.Errorf("failed to load local registry: %w", err)
	}

	idx := findPluginIndexByName(localReg.Plugins, pluginName)
	if idx < 0 {
		return fmt.Errorf("plugin not found: %s", pluginName)
	}

	localReg.Plugins[idx].Category = category
	return m.SaveLocal(localReg)
}

// UpdatePluginPermissions updates the permissions for a plugin in the local registry
func (m *Manager) UpdatePluginPermissions(pluginName string, perms map[string]interface{}, approved bool) error {
	localReg, err := m.LoadLocal()
	if err != nil {
		return fmt.Errorf("failed to load local registry: %w", err)
	}

	idx := findPluginIndexByName(localReg.Plugins, pluginName)
	if idx < 0 {
		return fmt.Errorf("plugin not found: %s", pluginName)
	}

	localReg.Plugins[idx].Permissions = perms
	localReg.Plugins[idx].PermissionsApproved = approved
	return m.SaveLocal(localReg)
}

// UpdatePluginStatus updates the enabled status and health for a plugin
func (m *Manager) UpdatePluginStatus(pluginName string, enabled bool, healthStatus string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	localReg, err := m.loadLocalUnlocked()
	if err != nil {
		return fmt.Errorf("failed to load local registry: %w", err)
	}

	idx := findPluginIndexByName(localReg.Plugins, pluginName)
	if idx < 0 {
		return fmt.Errorf("plugin not found: %s", pluginName)
	}

	localReg.Plugins[idx].Enabled = enabled
	localReg.Plugins[idx].HealthStatus = healthStatus
	return m.saveLocalUnlocked(localReg)
}

// UpdatePluginLastUsed updates the last used timestamp for a plugin
func (m *Manager) UpdatePluginLastUsed(pluginName string, lastUsed time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	localReg, err := m.loadLocalUnlocked()
	if err != nil {
		return fmt.Errorf("failed to load local registry: %w", err)
	}

	idx := findPluginIndexByName(localReg.Plugins, pluginName)
	if idx < 0 {
		return fmt.Errorf("plugin not found: %s", pluginName)
	}

	localReg.Plugins[idx].LastUsed = &lastUsed
	return m.saveLocalUnlocked(localReg)
}

// AddVersionToHistory adds a version entry to a plugin's version history
func (m *Manager) AddVersionToHistory(pluginName string, versionEntry types.VersionHistoryEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	localReg, err := m.loadLocalUnlocked()
	if err != nil {
		return fmt.Errorf("failed to load local registry: %w", err)
	}

	idx := findPluginIndexByName(localReg.Plugins, pluginName)
	if idx < 0 {
		return fmt.Errorf("plugin not found: %s", pluginName)
	}

	localReg.Plugins[idx].VersionHistory = append(localReg.Plugins[idx].VersionHistory, versionEntry)
	return m.saveLocalUnlocked(localReg)
}

// RemovePlugin removes a plugin from the local registry
func (m *Manager) RemovePlugin(pluginName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	localReg, err := m.loadLocalUnlocked()
	if err != nil {
		return fmt.Errorf("failed to load local registry: %w", err)
	}

	normalized := NormalizePluginNameForLookup(pluginName)
	var updatedPlugins []types.PluginRegistryEntry
	found := false
	for _, plugin := range localReg.Plugins {
		if NormalizePluginNameForLookup(plugin.Name) != normalized {
			updatedPlugins = append(updatedPlugins, plugin)
		} else {
			found = true
		}
	}

	if !found {
		return fmt.Errorf("plugin not found: %s", pluginName)
	}

	localReg.Plugins = updatedPlugins
	return m.saveLocalUnlocked(localReg)
}

// MigrateExistingPlugins adds default values for new metadata fields to existing plugins
func (m *Manager) MigrateExistingPlugins() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	localReg, err := m.loadLocalUnlocked()
	if err != nil {
		return fmt.Errorf("failed to load local registry: %w", err)
	}

	if len(localReg.Plugins) == 0 {
		return nil // Nothing to migrate
	}

	updated := false
	for i := range localReg.Plugins {
		plugin := &localReg.Plugins[i]

		// Set default category if empty
		if plugin.Category == "" {
			plugin.Category = "Custom" // Default category
			updated = true
		}

		// Initialize permissions if nil with default values (all false)
		// Note: We set actual values instead of empty map to avoid omitempty removing it
		if plugin.Permissions == nil {
			plugin.Permissions = map[string]interface{}{
				"file_access":     false,
				"network_access":  false,
				"system_commands": false,
			}
			updated = true
		}

		// Version history can remain nil/empty as it will be populated over time
		// Initialize only if nil to ensure it's not nil
		if plugin.VersionHistory == nil {
			plugin.VersionHistory = make([]types.VersionHistoryEntry, 0)
			updated = true
		}

		// Set default enabled status if not set
		if !plugin.Enabled && plugin.HealthStatus == "" {
			plugin.Enabled = true // Enable by default
			plugin.HealthStatus = "healthy"
			updated = true
		}
	}

	if updated {
		if err := m.saveLocalUnlocked(localReg); err != nil {
			return fmt.Errorf("failed to save migrated registry: %w", err)
		}
		fmt.Println("✅ Migrated local plugin registry with new metadata fields")
	}

	return nil
}
