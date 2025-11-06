package pluginupdate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/johnjallday/ori-agent/internal/health"
	"github.com/johnjallday/ori-agent/internal/types"
)

// UpdateResult represents the result of a plugin update
type UpdateResult struct {
	PluginName    string    `json:"plugin_name"`
	OldVersion    string    `json:"old_version"`
	NewVersion    string    `json:"new_version"`
	Success       bool      `json:"success"`
	Error         string    `json:"error,omitempty"`
	BackupPath    string    `json:"backup_path,omitempty"`
	UpdatedAt     time.Time `json:"updated_at"`
	RolledBack    bool      `json:"rolled_back"`
}

// Updater handles plugin updates
type Updater struct {
	backupDir     string
	uploadedPluginsDir string
	healthChecker *health.Checker
}

// NewUpdater creates a new plugin updater
func NewUpdater(healthChecker *health.Checker) *Updater {
	return &Updater{
		backupDir:     "plugin_backups",
		uploadedPluginsDir: "uploaded_plugins",
		healthChecker: healthChecker,
	}
}

// UpdatePlugin updates a plugin to the specified version
func (u *Updater) UpdatePlugin(pluginName, currentPath string, registryEntry types.PluginRegistryEntry, currentVersion string) UpdateResult {
	result := UpdateResult{
		PluginName: pluginName,
		OldVersion: currentVersion,
		NewVersion: registryEntry.Version,
		UpdatedAt:  time.Now(),
	}

	// Ensure backup directory exists
	if err := os.MkdirAll(u.backupDir, 0755); err != nil {
		result.Error = fmt.Sprintf("Failed to create backup directory: %v", err)
		return result
	}

	// Step 1: Backup current plugin
	backupPath, err := u.backupPlugin(currentPath, pluginName, currentVersion)
	if err != nil {
		result.Error = fmt.Sprintf("Failed to backup plugin: %v", err)
		return result
	}
	result.BackupPath = backupPath
	log.Printf("✅ Backed up %s v%s to %s", pluginName, currentVersion, backupPath)

	// Step 2: Download new plugin
	tempPath := filepath.Join(os.TempDir(), fmt.Sprintf("%s_update_%d", pluginName, time.Now().Unix()))
	if err := u.downloadPlugin(registryEntry, tempPath); err != nil {
		result.Error = fmt.Sprintf("Failed to download plugin: %v", err)
		return result
	}
	defer os.Remove(tempPath) // Clean up temp file
	log.Printf("✅ Downloaded %s v%s", pluginName, registryEntry.Version)

	// Step 3: Verify checksum if available
	if registryEntry.Checksum != "" {
		if err := u.verifyChecksum(tempPath, registryEntry.Checksum); err != nil {
			result.Error = fmt.Sprintf("Checksum verification failed: %v", err)
			return result
		}
		log.Printf("✅ Checksum verified for %s", pluginName)
	}

	// Step 4: Make executable
	if err := os.Chmod(tempPath, 0755); err != nil {
		result.Error = fmt.Sprintf("Failed to make plugin executable: %v", err)
		return result
	}

	// Step 5: Replace old plugin with new
	if err := os.Rename(tempPath, currentPath); err != nil {
		result.Error = fmt.Sprintf("Failed to install plugin: %v", err)
		return result
	}
	log.Printf("✅ Installed %s v%s", pluginName, registryEntry.Version)

	// Step 6: Verify new plugin loads and is healthy
	if u.healthChecker != nil {
		// We can't easily load the plugin here without restarting,
		// but we can at least verify the file exists and is executable
		if info, err := os.Stat(currentPath); err != nil || info.Mode().Perm()&0111 == 0 {
			// Rollback
			log.Printf("❌ New plugin failed verification, rolling back...")
			if rollbackErr := u.rollbackPlugin(backupPath, currentPath); rollbackErr != nil {
				result.Error = fmt.Sprintf("Plugin verification failed and rollback failed: %v (original error: %v)", rollbackErr, err)
			} else {
				result.Error = fmt.Sprintf("Plugin verification failed, rolled back to v%s", currentVersion)
				result.RolledBack = true
			}
			return result
		}
	}

	result.Success = true
	log.Printf("🎉 Successfully updated %s: v%s → v%s", pluginName, currentVersion, registryEntry.Version)
	return result
}

// backupPlugin creates a backup of the current plugin
func (u *Updater) backupPlugin(currentPath, pluginName, version string) (string, error) {
	timestamp := time.Now().Format("20060102_150405")
	backupFilename := fmt.Sprintf("%s_v%s_%s", pluginName, version, timestamp)
	backupPath := filepath.Join(u.backupDir, backupFilename)

	// Copy current plugin to backup location
	if err := copyFile(currentPath, backupPath); err != nil {
		return "", fmt.Errorf("failed to copy plugin to backup: %w", err)
	}

	return backupPath, nil
}

// downloadPlugin downloads a plugin from the registry
func (u *Updater) downloadPlugin(registryEntry types.PluginRegistryEntry, destPath string) error {
	downloadURL := registryEntry.DownloadURL
	if downloadURL == "" {
		return fmt.Errorf("no download URL provided in registry entry")
	}

	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: 5 * time.Minute,
	}

	// Download file
	resp, err := client.Get(downloadURL)
	if err != nil {
		return fmt.Errorf("failed to download from %s: %w", downloadURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed with status %d", resp.StatusCode)
	}

	// Create destination file
	out, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer out.Close()

	// Copy data
	if _, err := io.Copy(out, resp.Body); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}

// verifyChecksum verifies the SHA256 checksum of a file
func (u *Updater) verifyChecksum(filePath, expectedChecksum string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return fmt.Errorf("failed to compute hash: %w", err)
	}

	actualChecksum := hex.EncodeToString(hash.Sum(nil))
	if actualChecksum != expectedChecksum {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", expectedChecksum, actualChecksum)
	}

	return nil
}

// rollbackPlugin restores a plugin from backup
func (u *Updater) rollbackPlugin(backupPath, destPath string) error {
	return copyFile(backupPath, destPath)
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	if _, err := io.Copy(destFile, sourceFile); err != nil {
		return err
	}

	// Copy permissions
	sourceInfo, err := os.Stat(src)
	if err != nil {
		return err
	}
	return os.Chmod(dst, sourceInfo.Mode())
}

// ListBackups returns a list of all plugin backups
func (u *Updater) ListBackups() ([]BackupInfo, error) {
	backups := []BackupInfo{}

	if _, err := os.Stat(u.backupDir); os.IsNotExist(err) {
		return backups, nil // No backups directory
	}

	entries, err := os.ReadDir(u.backupDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read backup directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		backups = append(backups, BackupInfo{
			Name:      entry.Name(),
			Path:      filepath.Join(u.backupDir, entry.Name()),
			Size:      info.Size(),
			CreatedAt: info.ModTime(),
		})
	}

	return backups, nil
}

// BackupInfo represents information about a plugin backup
type BackupInfo struct {
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
}

// CleanOldBackups removes backups older than the specified duration
func (u *Updater) CleanOldBackups(maxAge time.Duration) (int, error) {
	backups, err := u.ListBackups()
	if err != nil {
		return 0, err
	}

	removed := 0
	cutoff := time.Now().Add(-maxAge)

	for _, backup := range backups {
		if backup.CreatedAt.Before(cutoff) {
			if err := os.Remove(backup.Path); err != nil {
				log.Printf("Failed to remove old backup %s: %v", backup.Name, err)
				continue
			}
			removed++
			log.Printf("Removed old backup: %s", backup.Name)
		}
	}

	return removed, nil
}

// AutoUpdate checks for updates and installs them if auto-update is enabled
func (u *Updater) AutoUpdate(pluginName, currentPath, currentVersion string, registryEntry types.PluginRegistryEntry) (UpdateResult, bool) {
	// Check if auto-update is enabled for this plugin
	if !registryEntry.AutoUpdate {
		return UpdateResult{}, false
	}

	// Check if update is available
	if currentVersion == registryEntry.Version {
		return UpdateResult{}, false // Already up to date
	}

	// Compare versions
	isOlder, err := health.IsVersionOlder(currentVersion, registryEntry.Version)
	if err != nil || !isOlder {
		return UpdateResult{}, false // Not older or can't compare
	}

	log.Printf("🔄 Auto-updating %s: v%s → v%s", pluginName, currentVersion, registryEntry.Version)
	result := u.UpdatePlugin(pluginName, currentPath, registryEntry, currentVersion)
	return result, true
}
