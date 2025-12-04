package registry

import (
	"os"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/types"
)

func setupTestManager(t *testing.T) (*Manager, string) {
	t.Helper()

	// Create temporary directory for test files
	tmpDir, err := os.MkdirTemp("", "registry-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	manager := NewManager()
	// Override paths to use temp directory
	manager.localRegistryPath = tmpDir + "/local_plugin_registry.json"
	manager.cachePath = tmpDir + "/plugin_registry_cache.json"
	manager.uploadedPluginsDir = tmpDir + "/uploaded_plugins"

	return manager, tmpDir
}

func cleanupTestManager(t *testing.T, tmpDir string) {
	t.Helper()
	if err := os.RemoveAll(tmpDir); err != nil {
		t.Errorf("Failed to cleanup temp dir: %v", err)
	}
}

func createTestRegistry(t *testing.T, manager *Manager) {
	t.Helper()

	reg := types.PluginRegistry{
		Plugins: []types.PluginRegistryEntry{
			{
				Name:        "test-plugin-1",
				Description: "Test plugin 1",
				Version:     "1.0.0",
				Path:        "/path/to/plugin1",
				Category:    "System Tools",
				Enabled:     true,
				Permissions: map[string]interface{}{
					"file_access":     true,
					"network_access":  false,
					"system_commands": false,
				},
				PermissionsApproved: true,
				HealthStatus:        "healthy",
			},
			{
				Name:        "test-plugin-2",
				Description: "Test plugin 2",
				Version:     "2.0.0",
				Path:        "/path/to/plugin2",
				Category:    "AI/ML",
				Enabled:     false,
			},
		},
	}

	if err := manager.SaveLocal(reg); err != nil {
		t.Fatalf("Failed to save test registry: %v", err)
	}
}

func TestManager_GetPluginByName(t *testing.T) {
	manager, tmpDir := setupTestManager(t)
	defer cleanupTestManager(t, tmpDir)

	createTestRegistry(t, manager)

	tests := []struct {
		name       string
		pluginName string
		wantName   string
		wantErr    bool
	}{
		{
			name:       "existing plugin",
			pluginName: "test-plugin-1",
			wantName:   "test-plugin-1",
			wantErr:    false,
		},
		{
			name:       "second plugin",
			pluginName: "test-plugin-2",
			wantName:   "test-plugin-2",
			wantErr:    false,
		},
		{
			name:       "non-existent plugin",
			pluginName: "non-existent",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plugin, err := manager.GetPluginByName(tt.pluginName)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetPluginByName() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && plugin.Name != tt.wantName {
				t.Errorf("GetPluginByName() got name = %v, want %v", plugin.Name, tt.wantName)
			}
		})
	}
}

func TestManager_UpdatePluginCategory(t *testing.T) {
	manager, tmpDir := setupTestManager(t)
	defer cleanupTestManager(t, tmpDir)

	createTestRegistry(t, manager)

	tests := []struct {
		name        string
		pluginName  string
		newCategory string
		wantErr     bool
	}{
		{
			name:        "update existing plugin",
			pluginName:  "test-plugin-1",
			newCategory: "Development",
			wantErr:     false,
		},
		{
			name:        "update to custom category",
			pluginName:  "test-plugin-2",
			newCategory: "Custom",
			wantErr:     false,
		},
		{
			name:        "non-existent plugin",
			pluginName:  "non-existent",
			newCategory: "Utilities",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := manager.UpdatePluginCategory(tt.pluginName, tt.newCategory)
			if (err != nil) != tt.wantErr {
				t.Errorf("UpdatePluginCategory() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				// Verify update
				plugin, err := manager.GetPluginByName(tt.pluginName)
				if err != nil {
					t.Fatalf("Failed to get plugin after update: %v", err)
				}
				if plugin.Category != tt.newCategory {
					t.Errorf("Category not updated: got %v, want %v", plugin.Category, tt.newCategory)
				}
			}
		})
	}
}

func TestManager_UpdatePluginPermissions(t *testing.T) {
	manager, tmpDir := setupTestManager(t)
	defer cleanupTestManager(t, tmpDir)

	createTestRegistry(t, manager)

	tests := []struct {
		name        string
		pluginName  string
		permissions map[string]interface{}
		approved    bool
		wantErr     bool
	}{
		{
			name:       "update permissions",
			pluginName: "test-plugin-1",
			permissions: map[string]interface{}{
				"file_access":     false,
				"network_access":  true,
				"system_commands": true,
			},
			approved: true,
			wantErr:  false,
		},
		{
			name:       "update permissions not approved",
			pluginName: "test-plugin-2",
			permissions: map[string]interface{}{
				"file_access": true,
			},
			approved: false,
			wantErr:  false,
		},
		{
			name:       "non-existent plugin",
			pluginName: "non-existent",
			permissions: map[string]interface{}{
				"file_access": true,
			},
			approved: false,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := manager.UpdatePluginPermissions(tt.pluginName, tt.permissions, tt.approved)
			if (err != nil) != tt.wantErr {
				t.Errorf("UpdatePluginPermissions() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				// Verify update
				plugin, err := manager.GetPluginByName(tt.pluginName)
				if err != nil {
					t.Fatalf("Failed to get plugin after update: %v", err)
				}
				if plugin.PermissionsApproved != tt.approved {
					t.Errorf("PermissionsApproved not updated: got %v, want %v", plugin.PermissionsApproved, tt.approved)
				}
			}
		})
	}
}

func TestManager_UpdatePluginStatus(t *testing.T) {
	manager, tmpDir := setupTestManager(t)
	defer cleanupTestManager(t, tmpDir)

	createTestRegistry(t, manager)

	tests := []struct {
		name         string
		pluginName   string
		enabled      bool
		healthStatus string
		wantErr      bool
	}{
		{
			name:         "enable plugin",
			pluginName:   "test-plugin-2",
			enabled:      true,
			healthStatus: "healthy",
			wantErr:      false,
		},
		{
			name:         "disable plugin",
			pluginName:   "test-plugin-1",
			enabled:      false,
			healthStatus: "inactive",
			wantErr:      false,
		},
		{
			name:         "set error status",
			pluginName:   "test-plugin-1",
			enabled:      false,
			healthStatus: "error",
			wantErr:      false,
		},
		{
			name:         "non-existent plugin",
			pluginName:   "non-existent",
			enabled:      true,
			healthStatus: "healthy",
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := manager.UpdatePluginStatus(tt.pluginName, tt.enabled, tt.healthStatus)
			if (err != nil) != tt.wantErr {
				t.Errorf("UpdatePluginStatus() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				// Verify update
				plugin, err := manager.GetPluginByName(tt.pluginName)
				if err != nil {
					t.Fatalf("Failed to get plugin after update: %v", err)
				}
				if plugin.Enabled != tt.enabled {
					t.Errorf("Enabled not updated: got %v, want %v", plugin.Enabled, tt.enabled)
				}
				if plugin.HealthStatus != tt.healthStatus {
					t.Errorf("HealthStatus not updated: got %v, want %v", plugin.HealthStatus, tt.healthStatus)
				}
			}
		})
	}
}

func TestManager_UpdatePluginLastUsed(t *testing.T) {
	manager, tmpDir := setupTestManager(t)
	defer cleanupTestManager(t, tmpDir)

	createTestRegistry(t, manager)

	now := time.Now()

	tests := []struct {
		name       string
		pluginName string
		lastUsed   time.Time
		wantErr    bool
	}{
		{
			name:       "update last used",
			pluginName: "test-plugin-1",
			lastUsed:   now,
			wantErr:    false,
		},
		{
			name:       "update with past time",
			pluginName: "test-plugin-2",
			lastUsed:   now.Add(-24 * time.Hour),
			wantErr:    false,
		},
		{
			name:       "non-existent plugin",
			pluginName: "non-existent",
			lastUsed:   now,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := manager.UpdatePluginLastUsed(tt.pluginName, tt.lastUsed)
			if (err != nil) != tt.wantErr {
				t.Errorf("UpdatePluginLastUsed() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				// Verify update
				plugin, err := manager.GetPluginByName(tt.pluginName)
				if err != nil {
					t.Fatalf("Failed to get plugin after update: %v", err)
				}
				if plugin.LastUsed == nil {
					t.Error("LastUsed is nil after update")
				} else if !plugin.LastUsed.Equal(tt.lastUsed) {
					t.Errorf("LastUsed not updated correctly: got %v, want %v", plugin.LastUsed, tt.lastUsed)
				}
			}
		})
	}
}

func TestManager_AddVersionToHistory(t *testing.T) {
	manager, tmpDir := setupTestManager(t)
	defer cleanupTestManager(t, tmpDir)

	createTestRegistry(t, manager)

	tests := []struct {
		name       string
		pluginName string
		version    types.VersionHistoryEntry
		wantErr    bool
	}{
		{
			name:       "add version",
			pluginName: "test-plugin-1",
			version: types.VersionHistoryEntry{
				Version:     "1.1.0",
				Path:        "/path/to/plugin1-v1.1.0",
				InstalledAt: time.Now(),
				Changelog:   "Bug fixes",
			},
			wantErr: false,
		},
		{
			name:       "add another version",
			pluginName: "test-plugin-2",
			version: types.VersionHistoryEntry{
				Version:     "2.1.0",
				Path:        "/path/to/plugin2-v2.1.0",
				InstalledAt: time.Now(),
			},
			wantErr: false,
		},
		{
			name:       "non-existent plugin",
			pluginName: "non-existent",
			version: types.VersionHistoryEntry{
				Version: "1.0.0",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := manager.AddVersionToHistory(tt.pluginName, tt.version)
			if (err != nil) != tt.wantErr {
				t.Errorf("AddVersionToHistory() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				// Verify update
				plugin, err := manager.GetPluginByName(tt.pluginName)
				if err != nil {
					t.Fatalf("Failed to get plugin after update: %v", err)
				}
				if len(plugin.VersionHistory) == 0 {
					t.Error("VersionHistory is empty after adding version")
				} else {
					found := false
					for _, v := range plugin.VersionHistory {
						if v.Version == tt.version.Version {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("Version %s not found in history", tt.version.Version)
					}
				}
			}
		})
	}
}

func TestManager_RemovePlugin(t *testing.T) {
	manager, tmpDir := setupTestManager(t)
	defer cleanupTestManager(t, tmpDir)

	createTestRegistry(t, manager)

	tests := []struct {
		name       string
		pluginName string
		wantErr    bool
	}{
		{
			name:       "remove existing plugin",
			pluginName: "test-plugin-1",
			wantErr:    false,
		},
		{
			name:       "remove non-existent plugin",
			pluginName: "non-existent",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := manager.RemovePlugin(tt.pluginName)
			if (err != nil) != tt.wantErr {
				t.Errorf("RemovePlugin() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				// Verify removal
				_, err := manager.GetPluginByName(tt.pluginName)
				if err == nil {
					t.Error("Plugin still exists after removal")
				}
			}
		})
	}
}

func TestManager_MigrateExistingPlugins(t *testing.T) {
	manager, tmpDir := setupTestManager(t)
	defer cleanupTestManager(t, tmpDir)

	// Create registry with plugins missing new metadata fields
	reg := types.PluginRegistry{
		Plugins: []types.PluginRegistryEntry{
			{
				Name:        "old-plugin-1",
				Description: "Old plugin without metadata",
				Version:     "1.0.0",
				Path:        "/path/to/old-plugin",
				// Missing: Category, Permissions, VersionHistory, Enabled, HealthStatus
			},
			{
				Name:        "old-plugin-2",
				Description: "Another old plugin",
				Version:     "2.0.0",
				Path:        "/path/to/old-plugin2",
				Category:    "AI/ML", // Has category
				// Missing: Permissions, VersionHistory, Enabled, HealthStatus
			},
		},
	}

	if err := manager.SaveLocal(reg); err != nil {
		t.Fatalf("Failed to save test registry: %v", err)
	}

	// Run migration
	err := manager.MigrateExistingPlugins()
	if err != nil {
		t.Fatalf("MigrateExistingPlugins() error = %v", err)
	}

	// Verify migration
	plugin1, err := manager.GetPluginByName("old-plugin-1")
	if err != nil {
		t.Fatalf("Failed to get plugin1 after migration: %v", err)
	}

	// Check defaults were applied
	if plugin1.Category == "" {
		t.Error("Category not set after migration")
	}
	if plugin1.Category != "Custom" {
		t.Errorf("Default category should be 'Custom', got %s", plugin1.Category)
	}

	// Permissions should be initialized with default values
	if plugin1.Permissions == nil {
		t.Error("Permissions not initialized after migration")
	} else {
		// Check that default permissions were set
		if _, hasFileAccess := plugin1.Permissions["file_access"]; !hasFileAccess {
			t.Error("file_access permission not set after migration")
		}
		if _, hasNetworkAccess := plugin1.Permissions["network_access"]; !hasNetworkAccess {
			t.Error("network_access permission not set after migration")
		}
		if _, hasSystemCommands := plugin1.Permissions["system_commands"]; !hasSystemCommands {
			t.Error("system_commands permission not set after migration")
		}
	}

	// VersionHistory should be initialized (may be empty slice, which is fine)
	// Note: empty slices with omitempty may be nil after JSON round-trip, which is acceptable
	// We just verify it doesn't cause errors when accessed
	_ = len(plugin1.VersionHistory) // Should not panic
	if !plugin1.Enabled {
		t.Error("Enabled should be true by default after migration")
	}
	if plugin1.HealthStatus != "healthy" {
		t.Errorf("HealthStatus should be 'healthy', got %s", plugin1.HealthStatus)
	}

	// Check plugin with existing category keeps it
	plugin2, err := manager.GetPluginByName("old-plugin-2")
	if err != nil {
		t.Fatalf("Failed to get plugin2 after migration: %v", err)
	}
	if plugin2.Category != "AI/ML" {
		t.Errorf("Existing category should be preserved, got %s", plugin2.Category)
	}
}

func TestManager_MigrateExistingPlugins_EmptyRegistry(t *testing.T) {
	manager, tmpDir := setupTestManager(t)
	defer cleanupTestManager(t, tmpDir)

	// Create empty registry
	reg := types.PluginRegistry{
		Plugins: []types.PluginRegistryEntry{},
	}

	if err := manager.SaveLocal(reg); err != nil {
		t.Fatalf("Failed to save test registry: %v", err)
	}

	// Migration should succeed without error
	err := manager.MigrateExistingPlugins()
	if err != nil {
		t.Errorf("MigrateExistingPlugins() with empty registry error = %v", err)
	}
}

func TestManager_MigrateExistingPlugins_NoRegistryFile(t *testing.T) {
	manager, tmpDir := setupTestManager(t)
	defer cleanupTestManager(t, tmpDir)

	// Don't create registry file - migration should handle gracefully
	err := manager.MigrateExistingPlugins()
	// Should NOT error - LoadLocal returns empty registry when file doesn't exist
	if err != nil {
		t.Errorf("MigrateExistingPlugins() should not error when no registry file exists, got: %v", err)
	}
}

func TestManager_ConcurrentAccess(t *testing.T) {
	manager, tmpDir := setupTestManager(t)
	defer cleanupTestManager(t, tmpDir)

	createTestRegistry(t, manager)

	// Test concurrent reads
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			_, err := manager.GetPluginByName("test-plugin-1")
			if err != nil {
				t.Errorf("Concurrent read failed: %v", err)
			}
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	// Test concurrent writes
	for i := 0; i < 5; i++ {
		go func(index int) {
			err := manager.UpdatePluginStatus("test-plugin-1", index%2 == 0, "healthy")
			if err != nil {
				t.Errorf("Concurrent write failed: %v", err)
			}
			done <- true
		}(i)
	}

	for i := 0; i < 5; i++ {
		<-done
	}
}
