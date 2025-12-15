package pluginmanager

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const (
	// MaxVersionHistory is the maximum number of previous versions to keep
	MaxVersionHistory = 3
)

// VersionManager manages plugin version history and rollback functionality.
type VersionManager struct {
	mu             sync.RWMutex
	versionsDir    string                   // base directory for storing versions
	versionHistory map[string][]VersionInfo // plugin name -> version history
}

// NewVersionManager creates a new VersionManager instance.
func NewVersionManager(versionsDir string) *VersionManager {
	return &VersionManager{
		versionsDir:    versionsDir,
		versionHistory: make(map[string][]VersionInfo),
	}
}

// StoreVersion backs up a plugin binary before an update.
// This should be called before replacing a plugin with a new version.
func (vm *VersionManager) StoreVersion(pluginName, version, currentBinaryPath string) error {
	if pluginName == "" {
		return fmt.Errorf("plugin name cannot be empty")
	}
	if version == "" {
		return fmt.Errorf("version cannot be empty")
	}
	if currentBinaryPath == "" {
		return fmt.Errorf("binary path cannot be empty")
	}

	// Check if binary exists
	if _, err := os.Stat(currentBinaryPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("binary not found at %s", currentBinaryPath)
		}
		return fmt.Errorf("failed to stat binary: %w", err)
	}

	vm.mu.Lock()
	defer vm.mu.Unlock()

	// Create plugin-specific version directory
	pluginVersionDir := filepath.Join(vm.versionsDir, pluginName)
	if err := os.MkdirAll(pluginVersionDir, 0755); err != nil {
		return fmt.Errorf("failed to create version directory: %w", err)
	}

	// Generate backup filename with version and timestamp
	timestamp := time.Now().Format("20060102-150405")
	backupFilename := fmt.Sprintf("%s-%s-%s", pluginName, version, timestamp)
	backupPath := filepath.Join(pluginVersionDir, backupFilename)

	// Copy current binary to backup location
	if err := copyFile(currentBinaryPath, backupPath); err != nil {
		return fmt.Errorf("failed to copy binary: %w", err)
	}

	// Make backup executable
	if err := os.Chmod(backupPath, 0755); err != nil {
		return fmt.Errorf("failed to set executable permission: %w", err)
	}

	// Add to version history
	versionInfo := VersionInfo{
		Version:     version,
		Path:        backupPath,
		InstalledAt: time.Now(),
	}

	if _, exists := vm.versionHistory[pluginName]; !exists {
		vm.versionHistory[pluginName] = []VersionInfo{}
	}
	vm.versionHistory[pluginName] = append(vm.versionHistory[pluginName], versionInfo)

	// Clean up old versions if we exceed the limit
	if err := vm.cleanupOldVersions(pluginName); err != nil {
		// Log error but don't fail the operation
		fmt.Printf("Warning: failed to cleanup old versions: %v\n", err)
	}

	return nil
}

// GetVersionHistory retrieves the version history for a plugin.
// Returns versions sorted by installation time (newest first).
func (vm *VersionManager) GetVersionHistory(pluginName string) []VersionInfo {
	vm.mu.RLock()
	defer vm.mu.RUnlock()

	history, exists := vm.versionHistory[pluginName]
	if !exists {
		return []VersionInfo{}
	}

	// Return a copy, sorted by installed time (newest first)
	result := make([]VersionInfo, len(history))
	copy(result, history)
	sort.Slice(result, func(i, j int) bool {
		return result[i].InstalledAt.After(result[j].InstalledAt)
	})

	return result
}

// RollbackToVersion restores a previous version of a plugin.
// currentBinaryPath is the path to the current plugin binary that will be replaced.
func (vm *VersionManager) RollbackToVersion(pluginName, targetVersion, currentBinaryPath string) error {
	if pluginName == "" {
		return fmt.Errorf("plugin name cannot be empty")
	}
	if targetVersion == "" {
		return fmt.Errorf("target version cannot be empty")
	}
	if currentBinaryPath == "" {
		return fmt.Errorf("current binary path cannot be empty")
	}

	vm.mu.Lock()
	defer vm.mu.Unlock()

	// Find the target version in history
	history, exists := vm.versionHistory[pluginName]
	if !exists || len(history) == 0 {
		return fmt.Errorf("no version history found for plugin %s", pluginName)
	}

	var targetVersionInfo *VersionInfo
	for i := range history {
		if history[i].Version == targetVersion {
			targetVersionInfo = &history[i]
			break
		}
	}

	if targetVersionInfo == nil {
		return fmt.Errorf("version %s not found in history for plugin %s", targetVersion, pluginName)
	}

	// Verify the backup file exists
	if _, err := os.Stat(targetVersionInfo.Path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("backup file not found at %s", targetVersionInfo.Path)
		}
		return fmt.Errorf("failed to stat backup file: %w", err)
	}

	// Backup current version before rollback (as a safety measure)
	currentBackupDir := filepath.Dir(currentBinaryPath)
	timestamp := time.Now().Format("20060102-150405")
	currentBackupPath := filepath.Join(currentBackupDir, fmt.Sprintf("%s-pre-rollback-%s", pluginName, timestamp))
	if err := copyFile(currentBinaryPath, currentBackupPath); err != nil {
		// Log warning but continue
		fmt.Printf("Warning: failed to backup current version: %v\n", err)
	}

	// Copy the target version to the current binary location
	if err := copyFile(targetVersionInfo.Path, currentBinaryPath); err != nil {
		return fmt.Errorf("failed to restore version: %w", err)
	}

	// Make restored binary executable
	if err := os.Chmod(currentBinaryPath, 0755); err != nil {
		return fmt.Errorf("failed to set executable permission: %w", err)
	}

	return nil
}

// GetAvailableVersions returns a list of version strings available for rollback.
func (vm *VersionManager) GetAvailableVersions(pluginName string) []string {
	history := vm.GetVersionHistory(pluginName)
	versions := make([]string, len(history))
	for i, v := range history {
		versions[i] = v.Version
	}
	return versions
}

// HasVersionHistory returns true if there are any previous versions stored for the plugin.
func (vm *VersionManager) HasVersionHistory(pluginName string) bool {
	vm.mu.RLock()
	defer vm.mu.RUnlock()

	history, exists := vm.versionHistory[pluginName]
	return exists && len(history) > 0
}

// RemoveVersionHistory removes all version history for a plugin.
// This should be called when a plugin is uninstalled.
func (vm *VersionManager) RemoveVersionHistory(pluginName string) error {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	// Remove from memory
	delete(vm.versionHistory, pluginName)

	// Remove directory from disk
	pluginVersionDir := filepath.Join(vm.versionsDir, pluginName)
	if err := os.RemoveAll(pluginVersionDir); err != nil {
		return fmt.Errorf("failed to remove version directory: %w", err)
	}

	return nil
}

// LoadVersionHistory loads version history from disk.
// This scans the versions directory and rebuilds the version history map.
func (vm *VersionManager) LoadVersionHistory() error {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	// Clear existing history
	vm.versionHistory = make(map[string][]VersionInfo)

	// Create versions dir if it doesn't exist
	if err := os.MkdirAll(vm.versionsDir, 0755); err != nil {
		return fmt.Errorf("failed to create versions directory: %w", err)
	}

	// Read plugin directories
	entries, err := os.ReadDir(vm.versionsDir)
	if err != nil {
		return fmt.Errorf("failed to read versions directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		pluginName := entry.Name()
		pluginVersionDir := filepath.Join(vm.versionsDir, pluginName)

		// Read version files
		versionFiles, err := os.ReadDir(pluginVersionDir)
		if err != nil {
			fmt.Printf("Warning: failed to read version directory for %s: %v\n", pluginName, err)
			continue
		}

		for _, versionFile := range versionFiles {
			if versionFile.IsDir() {
				continue
			}

			versionPath := filepath.Join(pluginVersionDir, versionFile.Name())

			// Get file info for timestamp
			info, err := versionFile.Info()
			if err != nil {
				continue
			}

			// Parse version from filename (format: pluginname-version-timestamp)
			// This is a simplified parser - we'll use the modification time as fallback
			version := parseVersionFromFilename(versionFile.Name())

			versionInfo := VersionInfo{
				Version:     version,
				Path:        versionPath,
				InstalledAt: info.ModTime(),
			}

			if _, exists := vm.versionHistory[pluginName]; !exists {
				vm.versionHistory[pluginName] = []VersionInfo{}
			}
			vm.versionHistory[pluginName] = append(vm.versionHistory[pluginName], versionInfo)
		}
	}

	return nil
}

// cleanupOldVersions removes old versions if we exceed MaxVersionHistory.
// This is an internal method and assumes the caller holds the lock.
func (vm *VersionManager) cleanupOldVersions(pluginName string) error {
	history, exists := vm.versionHistory[pluginName]
	if !exists || len(history) <= MaxVersionHistory {
		return nil
	}

	// Sort by installation time (oldest first for removal)
	sort.Slice(history, func(i, j int) bool {
		return history[i].InstalledAt.Before(history[j].InstalledAt)
	})

	// Remove oldest versions beyond the limit
	toRemove := len(history) - MaxVersionHistory
	for i := 0; i < toRemove; i++ {
		if err := os.Remove(history[i].Path); err != nil {
			if !os.IsNotExist(err) {
				return fmt.Errorf("failed to remove old version: %w", err)
			}
		}
	}

	// Update history (keep only the newest MaxVersionHistory entries)
	vm.versionHistory[pluginName] = history[toRemove:]

	return nil
}

// --- Helper functions ---

// copyFile copies a file from src to dst.
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = sourceFile.Close() }()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() { _ = destFile.Close() }()

	if _, err := io.Copy(destFile, sourceFile); err != nil {
		return err
	}

	return destFile.Sync()
}

// parseVersionFromFilename attempts to extract version from backup filename.
// Expected format: pluginname-version-timestamp
func parseVersionFromFilename(filename string) string {
	// Simple implementation: extract middle part
	// Format: pluginname-version-timestamp
	parts := filepath.Base(filename)
	// Remove extension if present
	ext := filepath.Ext(parts)
	if ext != "" {
		parts = parts[:len(parts)-len(ext)]
	}

	// Try to extract version (this is simplified)
	// In practice, you might want more sophisticated parsing
	// For now, we'll just return the filename without extension as version
	return parts
}
