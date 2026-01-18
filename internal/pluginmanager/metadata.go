package pluginmanager

import (
	"time"

	"github.com/oriagent/ori-pluginapi"
)

// PluginMetadata represents extended metadata for a plugin in the management system.
// This extends the basic plugin information with categories, permissions, and version history.
type PluginMetadata struct {
	// Name is the plugin name
	Name string `json:"name"`
	// Version is the current plugin version
	Version string `json:"version"`
	// Description is the plugin description
	Description string `json:"description"`
	// Category is the plugin category (e.g., "System Tools", "AI/ML", "Data Processing")
	// Can be comma-separated for multiple categories
	Category string `json:"category,omitempty"`
	// Permissions describes what system permissions this plugin requires
	Permissions pluginapi.PluginPermissions `json:"permissions"`
	// VersionHistory tracks previous versions for rollback capability
	VersionHistory []VersionInfo `json:"version_history,omitempty"`
	// Source indicates where the plugin came from (uploaded, built-in, marketplace)
	Source string `json:"source,omitempty"`
	// Path is the file path to the plugin binary
	Path string `json:"path"`
	// Enabled indicates if the plugin is currently enabled
	Enabled bool `json:"enabled"`
	// LastUsed tracks when the plugin was last executed
	LastUsed *time.Time `json:"last_used,omitempty"`
	// HealthStatus indicates the current health status (healthy, degraded, failed)
	HealthStatus string `json:"health_status,omitempty"`
	// PermissionsApproved indicates if the user has approved the plugin's permissions
	PermissionsApproved bool `json:"permissions_approved"`
	// Author is the plugin author (from MetadataProvider if available)
	Author string `json:"author,omitempty"`
	// License is the plugin license (from MetadataProvider if available)
	License string `json:"license,omitempty"`
	// Repository is the plugin repository URL (from MetadataProvider if available)
	Repository string `json:"repository,omitempty"`
}

// VersionInfo tracks information about a specific plugin version for rollback.
type VersionInfo struct {
	// Version is the version string (e.g., "1.0.0", "1.2.3-beta")
	Version string `json:"version"`
	// Path is the file path to this version's binary (stored in plugin_versions/)
	Path string `json:"path"`
	// InstalledAt is when this version was installed
	InstalledAt time.Time `json:"installed_at"`
	// Changelog describes what changed in this version (if available)
	Changelog string `json:"changelog,omitempty"`
}

// PluginStatus represents the current operational status of a plugin.
type PluginStatus string

const (
	// StatusActive indicates the plugin is enabled and healthy
	StatusActive PluginStatus = "active"
	// StatusInactive indicates the plugin is disabled by the user
	StatusInactive PluginStatus = "inactive"
	// StatusError indicates the plugin has errors or failed health check
	StatusError PluginStatus = "error"
	// StatusNeedsUpdate indicates an update is available for this plugin
	StatusNeedsUpdate PluginStatus = "needs_update"
	// StatusNotConfigured indicates the plugin is missing required settings
	StatusNotConfigured PluginStatus = "not_configured"
	// StatusPendingApproval indicates the plugin needs permission approval
	StatusPendingApproval PluginStatus = "pending_approval"
)

// GetStatus returns the current status of a plugin based on its metadata.
func (m *PluginMetadata) GetStatus() PluginStatus {
	// Check if permissions need approval
	if m.Permissions.FileAccess || m.Permissions.NetworkAccess || m.Permissions.SystemCommands {
		if !m.PermissionsApproved {
			return StatusPendingApproval
		}
	}

	// Check if plugin is disabled
	if !m.Enabled {
		return StatusInactive
	}

	// Check health status
	if m.HealthStatus == "failed" || m.HealthStatus == "error" {
		return StatusError
	}

	// Plugin is active
	return StatusActive
}
