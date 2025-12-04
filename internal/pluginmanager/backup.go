package pluginmanager

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// PluginConfigExport represents an exported plugin configuration.
type PluginConfigExport struct {
	PluginName  string                 `json:"plugin_name"`
	Version     string                 `json:"version"`
	Category    string                 `json:"category,omitempty"`
	Permissions map[string]interface{} `json:"permissions,omitempty"`
	Settings    map[string]interface{} `json:"settings,omitempty"`
	ExportedAt  time.Time              `json:"exported_at"`
}

// BackupArchiveManifest represents metadata for a backup archive.
type BackupArchiveManifest struct {
	CreatedAt   time.Time `json:"created_at"`
	PluginCount int       `json:"plugin_count"`
	OriVersion  string    `json:"ori_version,omitempty"`
	Description string    `json:"description,omitempty"`
}

// BackupManager manages plugin configuration backup and export functionality.
type BackupManager struct {
	mu          sync.RWMutex
	backupsDir  string                            // directory for storing backups
	configStore map[string]map[string]interface{} // plugin name -> config data
}

// NewBackupManager creates a new BackupManager instance.
func NewBackupManager(backupsDir string) *BackupManager {
	return &BackupManager{
		backupsDir:  backupsDir,
		configStore: make(map[string]map[string]interface{}),
	}
}

// ExportPluginConfig exports a single plugin's configuration as JSON.
func (bm *BackupManager) ExportPluginConfig(pluginName string) ([]byte, error) {
	if pluginName == "" {
		return nil, fmt.Errorf("plugin name cannot be empty")
	}

	bm.mu.RLock()
	defer bm.mu.RUnlock()

	config, exists := bm.configStore[pluginName]
	if !exists {
		return nil, fmt.Errorf("no configuration found for plugin %s", pluginName)
	}

	export := PluginConfigExport{
		PluginName: pluginName,
		Settings:   config,
		ExportedAt: time.Now(),
	}

	// Add version, category, permissions if available
	if version, ok := config["version"].(string); ok {
		export.Version = version
	}
	if category, ok := config["category"].(string); ok {
		export.Category = category
	}
	if permissions, ok := config["permissions"].(map[string]interface{}); ok {
		export.Permissions = permissions
	}

	data, err := json.MarshalIndent(export, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal config: %w", err)
	}

	return data, nil
}

// ExportAllPluginConfigs exports all plugin configurations as a single JSON array.
func (bm *BackupManager) ExportAllPluginConfigs() ([]byte, error) {
	bm.mu.RLock()
	defer bm.mu.RUnlock()

	if len(bm.configStore) == 0 {
		return nil, fmt.Errorf("no plugin configurations to export")
	}

	exports := make([]PluginConfigExport, 0, len(bm.configStore))

	for pluginName, config := range bm.configStore {
		export := PluginConfigExport{
			PluginName: pluginName,
			Settings:   config,
			ExportedAt: time.Now(),
		}

		// Add optional metadata
		if version, ok := config["version"].(string); ok {
			export.Version = version
		}
		if category, ok := config["category"].(string); ok {
			export.Category = category
		}
		if permissions, ok := config["permissions"].(map[string]interface{}); ok {
			export.Permissions = permissions
		}

		exports = append(exports, export)
	}

	data, err := json.MarshalIndent(exports, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal configs: %w", err)
	}

	return data, nil
}

// SaveExportToFile saves exported configuration to a file.
func (bm *BackupManager) SaveExportToFile(pluginName string, data []byte) (string, error) {
	if err := os.MkdirAll(bm.backupsDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create backups directory: %w", err)
	}

	timestamp := time.Now().Format("20060102-150405")
	filename := fmt.Sprintf("%s-config-%s.json", pluginName, timestamp)
	filepath := filepath.Join(bm.backupsDir, filename)

	if err := os.WriteFile(filepath, data, 0644); err != nil {
		return "", fmt.Errorf("failed to write export file: %w", err)
	}

	return filepath, nil
}

// ImportPluginConfig imports a plugin configuration from JSON data.
func (bm *BackupManager) ImportPluginConfig(configData []byte) error {
	var export PluginConfigExport
	if err := json.Unmarshal(configData, &export); err != nil {
		return fmt.Errorf("failed to unmarshal config: %w", err)
	}

	if export.PluginName == "" {
		return fmt.Errorf("plugin name is required in configuration")
	}

	bm.mu.Lock()
	defer bm.mu.Unlock()

	// Store the configuration
	bm.configStore[export.PluginName] = export.Settings

	return nil
}

// ImportMultipleConfigs imports multiple plugin configurations from JSON array.
func (bm *BackupManager) ImportMultipleConfigs(configData []byte) error {
	var exports []PluginConfigExport
	if err := json.Unmarshal(configData, &exports); err != nil {
		return fmt.Errorf("failed to unmarshal configs: %w", err)
	}

	bm.mu.Lock()
	defer bm.mu.Unlock()

	for _, export := range exports {
		if export.PluginName == "" {
			continue // Skip invalid entries
		}
		bm.configStore[export.PluginName] = export.Settings
	}

	return nil
}

// ValidateImportedConfig validates configuration data before import.
func (bm *BackupManager) ValidateImportedConfig(configData []byte) error {
	// Try to unmarshal as single config
	var singleExport PluginConfigExport
	if err := json.Unmarshal(configData, &singleExport); err == nil {
		if singleExport.PluginName == "" {
			return fmt.Errorf("plugin name is required")
		}
		return nil
	}

	// Try to unmarshal as multiple configs
	var multipleExports []PluginConfigExport
	if err := json.Unmarshal(configData, &multipleExports); err != nil {
		return fmt.Errorf("invalid configuration format: must be single config object or array of configs")
	}

	if len(multipleExports) == 0 {
		return fmt.Errorf("no configurations found in import data")
	}

	// Validate each config
	for i, export := range multipleExports {
		if export.PluginName == "" {
			return fmt.Errorf("configuration at index %d is missing plugin name", i)
		}
	}

	return nil
}

// CreateBackupArchive creates a zip archive of all plugin configurations and optionally binaries.
func (bm *BackupManager) CreateBackupArchive(includeDescription string) (string, error) {
	if err := os.MkdirAll(bm.backupsDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create backups directory: %w", err)
	}

	timestamp := time.Now().Format("20060102-150405")
	archiveName := fmt.Sprintf("plugin-backup-%s.zip", timestamp)
	archivePath := filepath.Join(bm.backupsDir, archiveName)

	// Create zip file
	zipFile, err := os.Create(archivePath)
	if err != nil {
		return "", fmt.Errorf("failed to create archive: %w", err)
	}
	defer zipFile.Close()

	zipWriter := zip.NewWriter(zipFile)
	defer zipWriter.Close()

	// Create manifest
	manifest := BackupArchiveManifest{
		CreatedAt:   time.Now(),
		PluginCount: len(bm.configStore),
		Description: includeDescription,
	}

	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to create manifest: %w", err)
	}

	// Add manifest to archive
	manifestWriter, err := zipWriter.Create("manifest.json")
	if err != nil {
		return "", fmt.Errorf("failed to add manifest to archive: %w", err)
	}
	if _, err := manifestWriter.Write(manifestData); err != nil {
		return "", fmt.Errorf("failed to write manifest: %w", err)
	}

	// Export all configs and add to archive
	configsData, err := bm.ExportAllPluginConfigs()
	if err != nil {
		return "", fmt.Errorf("failed to export configs: %w", err)
	}

	configsWriter, err := zipWriter.Create("plugin_configs.json")
	if err != nil {
		return "", fmt.Errorf("failed to add configs to archive: %w", err)
	}
	if _, err := configsWriter.Write(configsData); err != nil {
		return "", fmt.Errorf("failed to write configs: %w", err)
	}

	return archivePath, nil
}

// ExtractBackupArchive extracts and imports configurations from a backup archive.
func (bm *BackupManager) ExtractBackupArchive(archivePath string) error {
	// Open zip file
	zipReader, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("failed to open archive: %w", err)
	}
	defer zipReader.Close()

	// Find and read plugin_configs.json
	var configsData []byte
	for _, file := range zipReader.File {
		if file.Name == "plugin_configs.json" {
			fileReader, err := file.Open()
			if err != nil {
				return fmt.Errorf("failed to open configs file in archive: %w", err)
			}
			defer fileReader.Close()

			configsData, err = io.ReadAll(fileReader)
			if err != nil {
				return fmt.Errorf("failed to read configs from archive: %w", err)
			}
			break
		}
	}

	if configsData == nil {
		return fmt.Errorf("plugin_configs.json not found in archive")
	}

	// Validate before import
	if err := bm.ValidateImportedConfig(configsData); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	// Import configurations
	if err := bm.ImportMultipleConfigs(configsData); err != nil {
		return fmt.Errorf("import failed: %w", err)
	}

	return nil
}

// SetPluginConfig sets the configuration for a plugin (used by registry manager).
func (bm *BackupManager) SetPluginConfig(pluginName string, config map[string]interface{}) {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	bm.configStore[pluginName] = config
}

// GetPluginConfig retrieves the configuration for a plugin.
func (bm *BackupManager) GetPluginConfig(pluginName string) (map[string]interface{}, error) {
	bm.mu.RLock()
	defer bm.mu.RUnlock()

	config, exists := bm.configStore[pluginName]
	if !exists {
		return nil, fmt.Errorf("no configuration found for plugin %s", pluginName)
	}

	// Return a copy
	result := make(map[string]interface{}, len(config))
	for k, v := range config {
		result[k] = v
	}

	return result, nil
}

// RemovePluginConfig removes a plugin's configuration.
func (bm *BackupManager) RemovePluginConfig(pluginName string) {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	delete(bm.configStore, pluginName)
}

// ListBackups lists all backup files in the backups directory.
func (bm *BackupManager) ListBackups() ([]string, error) {
	entries, err := os.ReadDir(bm.backupsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("failed to read backups directory: %w", err)
	}

	backups := make([]string, 0)
	for _, entry := range entries {
		if !entry.IsDir() && (filepath.Ext(entry.Name()) == ".json" || filepath.Ext(entry.Name()) == ".zip") {
			backups = append(backups, entry.Name())
		}
	}

	return backups, nil
}
