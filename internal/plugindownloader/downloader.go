package plugindownloader

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/types"
)

// PluginDownloader handles downloading and caching of external plugins
type PluginDownloader struct {
	CacheDir   string
	HTTPClient *http.Client
}

// NewDownloader creates a new plugin downloader with a cache directory
func NewDownloader(cacheDir string) *PluginDownloader {
	// Ensure cache directory exists
	_ = os.MkdirAll(cacheDir, 0755) // Ignore error, will fail later if needed

	return &PluginDownloader{
		CacheDir: cacheDir,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// GetPlugin downloads a plugin if needed and returns the local path and whether it was already cached
func (d *PluginDownloader) GetPlugin(entry types.PluginRegistryEntry) (string, bool, error) {
	// If it's a local plugin, return the path as-is
	if entry.URL == "" && entry.Path != "" {
		return entry.Path, false, nil
	}

	// If it's a remote plugin (URL or DownloadURL), handle downloading/caching
	if entry.URL != "" || entry.DownloadURL != "" {
		return d.downloadAndCache(entry)
	}

	return "", false, fmt.Errorf("plugin entry must have either path or url specified")
}

// getPlatformExtension returns the appropriate file extension for the current platform
func (d *PluginDownloader) getPlatformExtension() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// getPlatformFilename generates a platform-specific filename for a plugin
func (d *PluginDownloader) getPlatformFilename(entry types.PluginRegistryEntry) string {
	ext := d.getPlatformExtension()

	// Check if the URL already has platform info
	if strings.Contains(entry.URL, runtime.GOOS) {
		// URL likely points to a platform-specific binary
		if entry.Version != "" {
			return fmt.Sprintf("%s-%s-%s-%s%s", entry.Name, entry.Version, runtime.GOOS, runtime.GOARCH, ext)
		}
		return fmt.Sprintf("%s-%s-%s%s", entry.Name, runtime.GOOS, runtime.GOARCH, ext)
	}

	// Legacy .so file or generic URL
	if strings.HasSuffix(entry.URL, ".so") {
		if entry.Version != "" {
			return fmt.Sprintf("%s-%s.so", entry.Name, entry.Version)
		}
		return fmt.Sprintf("%s.so", entry.Name)
	}

	// Default to executable naming
	if entry.Version != "" {
		return fmt.Sprintf("%s-%s%s", entry.Name, entry.Version, ext)
	}
	return fmt.Sprintf("%s%s", entry.Name, ext)
}

// downloadAndCache downloads a plugin from URL and caches it locally
func (d *PluginDownloader) downloadAndCache(entry types.PluginRegistryEntry) (string, bool, error) {
	// Generate cache filename based on plugin name, version, and platform
	cacheFilename := d.getPlatformFilename(entry)
	cachePath := filepath.Join(d.CacheDir, cacheFilename)

	// Check if plugin is already cached and valid
	if d.isCacheValid(cachePath, entry) {
		return cachePath, true, nil
	}

	// Build platform-specific download URL if needed
	downloadURL := entry.URL
	if entry.DownloadURL != "" {
		// Use DownloadURL template with platform substitution
		downloadURL = strings.ReplaceAll(entry.DownloadURL, "{os}", runtime.GOOS)
		downloadURL = strings.ReplaceAll(downloadURL, "{arch}", runtime.GOARCH)
		downloadURL = strings.ReplaceAll(downloadURL, "{version}", entry.Version)
		downloadURL = strings.ReplaceAll(downloadURL, "{platform}", fmt.Sprintf("%s-%s", runtime.GOOS, runtime.GOARCH))
		downloadURL = strings.ReplaceAll(downloadURL, "{ext}", d.getPlatformExtension())
	}

	// Download the plugin
	resp, err := d.HTTPClient.Get(downloadURL)
	if err != nil {
		return "", false, fmt.Errorf("failed to download plugin from %s: %w", downloadURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", false, fmt.Errorf("failed to download plugin: HTTP %d", resp.StatusCode)
	}

	// Create temporary file
	tempFile, err := os.CreateTemp(d.CacheDir, "plugin-*.tmp")
	if err != nil {
		return "", false, fmt.Errorf("failed to create temp file: %w", err)
	}
	defer tempFile.Close()

	// Download to temp file and calculate checksum
	hasher := sha256.New()
	writer := io.MultiWriter(tempFile, hasher)

	_, err = io.Copy(writer, resp.Body)
	if err != nil {
		os.Remove(tempFile.Name())
		return "", false, fmt.Errorf("failed to download plugin: %w", err)
	}

	// Verify checksum if provided
	if entry.Checksum != "" {
		calculatedChecksum := fmt.Sprintf("%x", hasher.Sum(nil))
		if calculatedChecksum != entry.Checksum {
			os.Remove(tempFile.Name())
			return "", false, fmt.Errorf("checksum mismatch: expected %s, got %s", entry.Checksum, calculatedChecksum)
		}
	}

	// Move temp file to final location
	tempFile.Close()
	err = os.Rename(tempFile.Name(), cachePath)
	if err != nil {
		os.Remove(tempFile.Name())
		return "", false, fmt.Errorf("failed to move plugin to cache: %w", err)
	}

	// Make the file executable on Unix-like systems
	if runtime.GOOS != "windows" {
		if err := os.Chmod(cachePath, 0755); err != nil {
			return "", false, fmt.Errorf("failed to make plugin executable: %w", err)
		}
	}

	return cachePath, false, nil
}

// isCacheValid checks if the cached plugin is still valid
func (d *PluginDownloader) isCacheValid(cachePath string, entry types.PluginRegistryEntry) bool {
	// Check if file exists
	info, err := os.Stat(cachePath)
	if err != nil {
		return false
	}

	// If checksum is provided, verify it
	if entry.Checksum != "" {
		file, err := os.Open(cachePath)
		if err != nil {
			return false
		}
		defer file.Close()

		hasher := sha256.New()
		if _, err := io.Copy(hasher, file); err != nil {
			return false
		}

		calculatedChecksum := fmt.Sprintf("%x", hasher.Sum(nil))
		if calculatedChecksum != entry.Checksum {
			return false
		}
	}

	// If auto-update is enabled, check if file is older than 1 hour
	if entry.AutoUpdate && time.Since(info.ModTime()) > time.Hour {
		return false
	}

	return true
}

// CheckForUpdates checks if any plugins in the registry need updates
func (d *PluginDownloader) CheckForUpdates(registry types.PluginRegistry) ([]types.PluginRegistryEntry, error) {
	var needsUpdate []types.PluginRegistryEntry

	for _, entry := range registry.Plugins {
		if entry.URL != "" && entry.AutoUpdate {
			// Check if cached version exists and is old
			cacheFilename := d.getPlatformFilename(entry)
			cachePath := filepath.Join(d.CacheDir, cacheFilename)

			if !d.isCacheValid(cachePath, entry) {
				needsUpdate = append(needsUpdate, entry)
			}
		}
	}

	return needsUpdate, nil
}
