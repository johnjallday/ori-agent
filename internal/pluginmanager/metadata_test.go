package pluginmanager

import (
	"testing"
	"time"

	"github.com/oriagent/ori-pluginapi"
)

func TestPluginMetadata_GetStatus(t *testing.T) {
	tests := []struct {
		name     string
		metadata *PluginMetadata
		want     PluginStatus
	}{
		{
			name: "pending approval - file access not approved",
			metadata: &PluginMetadata{
				Name:    "test-plugin",
				Enabled: true,
				Permissions: pluginapi.PluginPermissions{
					FileAccess: true,
				},
				PermissionsApproved: false,
			},
			want: StatusPendingApproval,
		},
		{
			name: "pending approval - network access not approved",
			metadata: &PluginMetadata{
				Name:    "test-plugin",
				Enabled: true,
				Permissions: pluginapi.PluginPermissions{
					NetworkAccess: true,
				},
				PermissionsApproved: false,
			},
			want: StatusPendingApproval,
		},
		{
			name: "inactive - plugin disabled",
			metadata: &PluginMetadata{
				Name:    "test-plugin",
				Enabled: false,
				Permissions: pluginapi.PluginPermissions{
					FileAccess: true,
				},
				PermissionsApproved: true,
			},
			want: StatusInactive,
		},
		{
			name: "error - health check failed",
			metadata: &PluginMetadata{
				Name:                "test-plugin",
				Enabled:             true,
				HealthStatus:        "failed",
				PermissionsApproved: true,
			},
			want: StatusError,
		},
		{
			name: "error - health check error",
			metadata: &PluginMetadata{
				Name:                "test-plugin",
				Enabled:             true,
				HealthStatus:        "error",
				PermissionsApproved: true,
			},
			want: StatusError,
		},
		{
			name: "active - all good",
			metadata: &PluginMetadata{
				Name:                "test-plugin",
				Enabled:             true,
				HealthStatus:        "healthy",
				PermissionsApproved: true,
			},
			want: StatusActive,
		},
		{
			name: "active - no permissions needed",
			metadata: &PluginMetadata{
				Name:         "test-plugin",
				Enabled:      true,
				HealthStatus: "healthy",
				Permissions: pluginapi.PluginPermissions{
					FileAccess:     false,
					NetworkAccess:  false,
					SystemCommands: false,
				},
				PermissionsApproved: false, // Not needed
			},
			want: StatusActive,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.metadata.GetStatus()
			if got != tt.want {
				t.Errorf("PluginMetadata.GetStatus() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestVersionInfo(t *testing.T) {
	now := time.Now()
	vi := VersionInfo{
		Version:     "1.0.0",
		Path:        "/path/to/plugin",
		InstalledAt: now,
		Changelog:   "Initial release",
	}

	if vi.Version != "1.0.0" {
		t.Errorf("Expected version 1.0.0, got %s", vi.Version)
	}
	if vi.Path != "/path/to/plugin" {
		t.Errorf("Expected path /path/to/plugin, got %s", vi.Path)
	}
	if !vi.InstalledAt.Equal(now) {
		t.Errorf("Expected InstalledAt to be %v, got %v", now, vi.InstalledAt)
	}
	if vi.Changelog != "Initial release" {
		t.Errorf("Expected changelog 'Initial release', got %s", vi.Changelog)
	}
}

func TestPluginMetadata_Fields(t *testing.T) {
	now := time.Now()
	metadata := &PluginMetadata{
		Name:        "test-plugin",
		Version:     "1.0.0",
		Description: "A test plugin",
		Category:    "System Tools",
		Permissions: pluginapi.PluginPermissions{
			FileAccess:  true,
			Description: "Needs file access",
		},
		VersionHistory: []VersionInfo{
			{
				Version:     "0.9.0",
				Path:        "/old/path",
				InstalledAt: now.Add(-24 * time.Hour),
			},
		},
		Source:              "uploaded",
		Path:                "/path/to/plugin",
		Enabled:             true,
		LastUsed:            &now,
		HealthStatus:        "healthy",
		PermissionsApproved: true,
		Author:              "Test Author",
		License:             "MIT",
		Repository:          "https://github.com/test/plugin",
	}

	if metadata.Name != "test-plugin" {
		t.Errorf("Expected name 'test-plugin', got %s", metadata.Name)
	}
	if metadata.Category != "System Tools" {
		t.Errorf("Expected category 'System Tools', got %s", metadata.Category)
	}
	if !metadata.Permissions.FileAccess {
		t.Error("Expected FileAccess to be true")
	}
	if len(metadata.VersionHistory) != 1 {
		t.Errorf("Expected 1 version history entry, got %d", len(metadata.VersionHistory))
	}
	if metadata.Source != "uploaded" {
		t.Errorf("Expected source 'uploaded', got %s", metadata.Source)
	}
	if !metadata.Enabled {
		t.Error("Expected plugin to be enabled")
	}
	if metadata.LastUsed == nil {
		t.Error("Expected LastUsed to be set")
	}
	if metadata.HealthStatus != "healthy" {
		t.Errorf("Expected HealthStatus 'healthy', got %s", metadata.HealthStatus)
	}
	if !metadata.PermissionsApproved {
		t.Error("Expected PermissionsApproved to be true")
	}
}
