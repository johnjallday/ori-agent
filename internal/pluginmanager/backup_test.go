package pluginmanager

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestNewBackupManager(t *testing.T) {
	bm := NewBackupManager("/tmp/backups")
	if bm == nil {
		t.Fatal("NewBackupManager returned nil")
	}
	if bm.configStore == nil {
		t.Error("configStore map not initialized")
	}
	if bm.backupsDir != "/tmp/backups" {
		t.Errorf("Expected backupsDir /tmp/backups, got %s", bm.backupsDir)
	}
}

func TestBackupManager_SetGetPluginConfig(t *testing.T) {
	bm := NewBackupManager("/tmp/backups")

	config := map[string]interface{}{
		"api_key":  "test-key",
		"timeout":  30,
		"enabled":  true,
		"version":  "1.0.0",
		"category": "System Tools",
	}

	// Set config
	bm.SetPluginConfig("test-plugin", config)

	// Get config
	retrieved, err := bm.GetPluginConfig("test-plugin")
	if err != nil {
		t.Errorf("GetPluginConfig() error = %v", err)
	}

	// Verify values
	if retrieved["api_key"] != "test-key" {
		t.Errorf("Expected api_key 'test-key', got %v", retrieved["api_key"])
	}
	if retrieved["timeout"] != 30 {
		t.Errorf("Expected timeout 30, got %v", retrieved["timeout"])
	}
	if retrieved["enabled"] != true {
		t.Errorf("Expected enabled true, got %v", retrieved["enabled"])
	}

	// Test getting non-existent config
	_, err = bm.GetPluginConfig("nonexistent")
	if err == nil {
		t.Error("Expected error for non-existent plugin")
	}
}

func TestBackupManager_ExportPluginConfig(t *testing.T) {
	bm := NewBackupManager("/tmp/backups")

	config := map[string]interface{}{
		"api_key": "test-key",
		"version": "1.0.0",
	}
	bm.SetPluginConfig("test-plugin", config)

	// Export config
	data, err := bm.ExportPluginConfig("test-plugin")
	if err != nil {
		t.Errorf("ExportPluginConfig() error = %v", err)
	}

	if len(data) == 0 {
		t.Error("Expected non-empty export data")
	}

	// Verify it's valid JSON
	var export PluginConfigExport
	if err := json.Unmarshal(data, &export); err != nil {
		t.Errorf("Export data is not valid JSON: %v", err)
	}

	if export.PluginName != "test-plugin" {
		t.Errorf("Expected plugin name 'test-plugin', got %s", export.PluginName)
	}
	if export.Version != "1.0.0" {
		t.Errorf("Expected version '1.0.0', got %s", export.Version)
	}

	// Test exporting non-existent plugin
	_, err = bm.ExportPluginConfig("nonexistent")
	if err == nil {
		t.Error("Expected error for non-existent plugin")
	}

	// Test empty plugin name
	_, err = bm.ExportPluginConfig("")
	if err == nil {
		t.Error("Expected error for empty plugin name")
	}
}

func TestBackupManager_ExportAllPluginConfigs(t *testing.T) {
	bm := NewBackupManager("/tmp/backups")

	// Add multiple configs
	bm.SetPluginConfig("plugin1", map[string]interface{}{
		"version": "1.0.0",
		"enabled": true,
	})
	bm.SetPluginConfig("plugin2", map[string]interface{}{
		"version":  "2.0.0",
		"category": "AI/ML",
	})
	bm.SetPluginConfig("plugin3", map[string]interface{}{
		"version": "3.0.0",
	})

	// Export all
	data, err := bm.ExportAllPluginConfigs()
	if err != nil {
		t.Errorf("ExportAllPluginConfigs() error = %v", err)
	}

	// Verify it's valid JSON array
	var exports []PluginConfigExport
	if err := json.Unmarshal(data, &exports); err != nil {
		t.Errorf("Export data is not valid JSON: %v", err)
	}

	if len(exports) != 3 {
		t.Errorf("Expected 3 exports, got %d", len(exports))
	}

	// Test with no configs
	bm2 := NewBackupManager("/tmp/backups2")
	_, err = bm2.ExportAllPluginConfigs()
	if err == nil {
		t.Error("Expected error when no configs to export")
	}
}

func TestBackupManager_ImportPluginConfig(t *testing.T) {
	bm := NewBackupManager("/tmp/backups")

	configJSON := `{
		"plugin_name": "test-plugin",
		"version": "1.0.0",
		"category": "System Tools",
		"settings": {
			"api_key": "test-key",
			"timeout": 30
		},
		"exported_at": "2024-01-01T00:00:00Z"
	}`

	// Import config
	err := bm.ImportPluginConfig([]byte(configJSON))
	if err != nil {
		t.Errorf("ImportPluginConfig() error = %v", err)
	}

	// Verify config was imported
	config, err := bm.GetPluginConfig("test-plugin")
	if err != nil {
		t.Errorf("GetPluginConfig() error = %v", err)
	}

	if config["api_key"] != "test-key" {
		t.Errorf("Expected api_key 'test-key', got %v", config["api_key"])
	}
	if config["timeout"] != float64(30) { // JSON unmarshals numbers as float64
		t.Errorf("Expected timeout 30, got %v", config["timeout"])
	}

	// Test invalid JSON
	err = bm.ImportPluginConfig([]byte("invalid json"))
	if err == nil {
		t.Error("Expected error for invalid JSON")
	}

	// Test missing plugin name
	invalidConfig := `{
		"version": "1.0.0",
		"settings": {}
	}`
	err = bm.ImportPluginConfig([]byte(invalidConfig))
	if err == nil {
		t.Error("Expected error for missing plugin name")
	}
}

func TestBackupManager_ImportMultipleConfigs(t *testing.T) {
	bm := NewBackupManager("/tmp/backups")

	configsJSON := `[
		{
			"plugin_name": "plugin1",
			"version": "1.0.0",
			"settings": {"key": "value1"}
		},
		{
			"plugin_name": "plugin2",
			"version": "2.0.0",
			"settings": {"key": "value2"}
		}
	]`

	// Import multiple configs
	err := bm.ImportMultipleConfigs([]byte(configsJSON))
	if err != nil {
		t.Errorf("ImportMultipleConfigs() error = %v", err)
	}

	// Verify both configs were imported
	config1, err := bm.GetPluginConfig("plugin1")
	if err != nil {
		t.Error("plugin1 should be imported")
	}
	if config1["key"] != "value1" {
		t.Error("plugin1 config not imported correctly")
	}

	config2, err := bm.GetPluginConfig("plugin2")
	if err != nil {
		t.Error("plugin2 should be imported")
	}
	if config2["key"] != "value2" {
		t.Error("plugin2 config not imported correctly")
	}
}

func TestBackupManager_ValidateImportedConfig(t *testing.T) {
	bm := NewBackupManager("/tmp/backups")

	tests := []struct {
		name    string
		config  string
		wantErr bool
	}{
		{
			name: "valid single config",
			config: `{
				"plugin_name": "test-plugin",
				"settings": {}
			}`,
			wantErr: false,
		},
		{
			name: "valid multiple configs",
			config: `[
				{"plugin_name": "plugin1", "settings": {}},
				{"plugin_name": "plugin2", "settings": {}}
			]`,
			wantErr: false,
		},
		{
			name:    "missing plugin name",
			config:  `{"settings": {}}`,
			wantErr: true,
		},
		{
			name:    "invalid JSON",
			config:  `invalid json`,
			wantErr: true,
		},
		{
			name:    "empty array",
			config:  `[]`,
			wantErr: true,
		},
		{
			name: "array with missing plugin name",
			config: `[
				{"plugin_name": "plugin1", "settings": {}},
				{"settings": {}}
			]`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := bm.ValidateImportedConfig([]byte(tt.config))
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateImportedConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestBackupManager_RemovePluginConfig(t *testing.T) {
	bm := NewBackupManager("/tmp/backups")

	bm.SetPluginConfig("plugin1", map[string]interface{}{"key": "value"})
	bm.SetPluginConfig("plugin2", map[string]interface{}{"key": "value"})

	// Remove plugin1
	bm.RemovePluginConfig("plugin1")

	// Verify plugin1 is removed
	_, err := bm.GetPluginConfig("plugin1")
	if err == nil {
		t.Error("Expected error for removed plugin")
	}

	// Verify plugin2 still exists
	_, err = bm.GetPluginConfig("plugin2")
	if err != nil {
		t.Error("plugin2 should still exist")
	}
}

func TestBackupManager_SaveExportToFile(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "backup-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	bm := NewBackupManager(tmpDir)

	exportData := []byte(`{"plugin_name": "test-plugin", "settings": {}}`)

	// Save export to file
	filepath, err := bm.SaveExportToFile("test-plugin", exportData)
	if err != nil {
		t.Errorf("SaveExportToFile() error = %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(filepath); os.IsNotExist(err) {
		t.Error("Export file was not created")
	}

	// Verify file content
	content, err := os.ReadFile(filepath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != string(exportData) {
		t.Error("File content doesn't match export data")
	}
}

func TestBackupManager_CreateBackupArchive(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "backup-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	bm := NewBackupManager(tmpDir)

	// Add some configs
	bm.SetPluginConfig("plugin1", map[string]interface{}{"key": "value1"})
	bm.SetPluginConfig("plugin2", map[string]interface{}{"key": "value2"})

	// Create backup archive
	archivePath, err := bm.CreateBackupArchive("Test backup")
	if err != nil {
		t.Errorf("CreateBackupArchive() error = %v", err)
	}

	// Verify archive exists
	if _, err := os.Stat(archivePath); os.IsNotExist(err) {
		t.Error("Backup archive was not created")
	}

	// Verify it's a valid zip file
	zipReader, err := zip.OpenReader(archivePath)
	if err != nil {
		t.Errorf("Archive is not a valid zip file: %v", err)
	}
	defer zipReader.Close()

	// Verify expected files in archive
	files := make(map[string]bool)
	for _, file := range zipReader.File {
		files[file.Name] = true
	}

	if !files["manifest.json"] {
		t.Error("manifest.json not found in archive")
	}
	if !files["plugin_configs.json"] {
		t.Error("plugin_configs.json not found in archive")
	}
}

func TestBackupManager_ExtractBackupArchive(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "backup-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create backup manager and add configs
	bm1 := NewBackupManager(tmpDir)
	bm1.SetPluginConfig("plugin1", map[string]interface{}{
		"api_key": "test-key",
		"version": "1.0.0",
	})
	bm1.SetPluginConfig("plugin2", map[string]interface{}{
		"timeout": 30,
		"version": "2.0.0",
	})

	// Create backup archive
	archivePath, err := bm1.CreateBackupArchive("Test backup")
	if err != nil {
		t.Fatal(err)
	}

	// Create new backup manager and extract archive
	bm2 := NewBackupManager(tmpDir)
	err = bm2.ExtractBackupArchive(archivePath)
	if err != nil {
		t.Errorf("ExtractBackupArchive() error = %v", err)
	}

	// Verify configs were imported
	config1, err := bm2.GetPluginConfig("plugin1")
	if err != nil {
		t.Error("plugin1 should be imported")
	}
	if config1["api_key"] != "test-key" {
		t.Error("plugin1 config not imported correctly")
	}

	config2, err := bm2.GetPluginConfig("plugin2")
	if err != nil {
		t.Error("plugin2 should be imported")
	}
	if config2["timeout"] != float64(30) {
		t.Error("plugin2 config not imported correctly")
	}

	// Test extracting invalid archive
	invalidPath := filepath.Join(tmpDir, "invalid.zip")
	err = bm2.ExtractBackupArchive(invalidPath)
	if err == nil {
		t.Error("Expected error for invalid archive path")
	}
}

func TestBackupManager_ListBackups(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "backup-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	bm := NewBackupManager(tmpDir)

	// Create some backup files
	_ = os.WriteFile(filepath.Join(tmpDir, "backup1.json"), []byte("{}"), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "backup2.zip"), []byte("test"), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "readme.txt"), []byte("text"), 0644) // Should be ignored

	// List backups
	backups, err := bm.ListBackups()
	if err != nil {
		t.Errorf("ListBackups() error = %v", err)
	}

	if len(backups) != 2 {
		t.Errorf("Expected 2 backups, got %d", len(backups))
	}

	// Verify backup files are included
	found := make(map[string]bool)
	for _, backup := range backups {
		found[backup] = true
	}
	if !found["backup1.json"] || !found["backup2.zip"] {
		t.Error("Expected backup files not found")
	}
	if found["readme.txt"] {
		t.Error("Non-backup file should not be listed")
	}

	// Test with non-existent directory
	bm2 := NewBackupManager("/nonexistent/path")
	backups, err = bm2.ListBackups()
	if err != nil {
		t.Errorf("ListBackups() should not error for non-existent directory, got: %v", err)
	}
	if len(backups) != 0 {
		t.Errorf("Expected 0 backups for non-existent directory, got %d", len(backups))
	}
}

func TestBackupManager_RoundTrip(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "backup-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	bm := NewBackupManager(tmpDir)

	// Set original configs
	originalConfigs := map[string]map[string]interface{}{
		"plugin1": {
			"api_key":  "key1",
			"version":  "1.0.0",
			"category": "System Tools",
		},
		"plugin2": {
			"timeout": 60,
			"version": "2.0.0",
		},
	}

	for name, config := range originalConfigs {
		bm.SetPluginConfig(name, config)
	}

	// Export all configs
	exportData, err := bm.ExportAllPluginConfigs()
	if err != nil {
		t.Fatal(err)
	}

	// Create new backup manager and import
	bm2 := NewBackupManager(tmpDir)
	if err := bm2.ImportMultipleConfigs(exportData); err != nil {
		t.Fatal(err)
	}

	// Verify all configs match
	for name, originalConfig := range originalConfigs {
		imported, err := bm2.GetPluginConfig(name)
		if err != nil {
			t.Errorf("Failed to get config for %s: %v", name, err)
			continue
		}

		for key, value := range originalConfig {
			importedValue := imported[key]

			// Handle numeric comparison (JSON unmarshals numbers as float64)
			if numValue, ok := value.(int); ok {
				if importedFloat, ok := importedValue.(float64); ok {
					if float64(numValue) != importedFloat {
						t.Errorf("Config mismatch for %s.%s: expected %v, got %v", name, key, value, importedValue)
					}
					continue
				}
			}

			if importedValue != value {
				t.Errorf("Config mismatch for %s.%s: expected %v, got %v", name, key, value, importedValue)
			}
		}
	}
}
