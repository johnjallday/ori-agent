package plugindownloader

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/johnjallday/dolphin-agent/internal/types"
)

// PluginDownloader handles downloading and caching of external plugins
type PluginDownloader struct {
	CacheDir   string
	HTTPClient *http.Client
}

// NewDownloader creates a new plugin downloader with a cache directory
func NewDownloader(cacheDir string) *PluginDownloader {
	// Ensure cache directory exists
	os.MkdirAll(cacheDir, 0755)

	return &PluginDownloader{
		CacheDir: cacheDir,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// GetPlugin downloads a plugin if needed and returns the local path
func (d *PluginDownloader) GetPlugin(entry types.PluginRegistryEntry) (string, error) {
	// If it's a local plugin, return the path as-is
	if entry.URL == "" && entry.Path != "" {
		return entry.Path, nil
	}

	// If it's a URL plugin, handle downloading/caching
	if entry.URL != "" {
		return d.downloadAndCache(entry)
	}

	return "", fmt.Errorf("plugin entry must have either path or url specified")
}

// downloadAndCache downloads a plugin from URL and caches it locally
func (d *PluginDownloader) downloadAndCache(entry types.PluginRegistryEntry) (string, error) {
	// Generate cache filename based on plugin name and version
	cacheFilename := fmt.Sprintf("%s-%s.so", entry.Name, entry.Version)
	if entry.Version == "" {
		cacheFilename = fmt.Sprintf("%s.so", entry.Name)
	}
	cachePath := filepath.Join(d.CacheDir, cacheFilename)

	// Check if plugin is already cached and valid
	if d.isCacheValid(cachePath, entry) {
		return cachePath, nil
	}

	// Download the plugin
	resp, err := d.HTTPClient.Get(entry.URL)
	if err != nil {
		return "", fmt.Errorf("failed to download plugin from %s: %w", entry.URL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download plugin: HTTP %d", resp.StatusCode)
	}

	// Create temporary file
	tempFile, err := os.CreateTemp(d.CacheDir, "plugin-*.so.tmp")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	defer tempFile.Close()

	// Download to temp file and calculate checksum
	hasher := sha256.New()
	writer := io.MultiWriter(tempFile, hasher)

	_, err = io.Copy(writer, resp.Body)
	if err != nil {
		os.Remove(tempFile.Name())
		return "", fmt.Errorf("failed to download plugin: %w", err)
	}

	// Verify checksum if provided
	if entry.Checksum != "" {
		calculatedChecksum := fmt.Sprintf("%x", hasher.Sum(nil))
		if calculatedChecksum != entry.Checksum {
			os.Remove(tempFile.Name())
			return "", fmt.Errorf("checksum mismatch: expected %s, got %s", entry.Checksum, calculatedChecksum)
		}
	}

	// Move temp file to final location
	tempFile.Close()
	err = os.Rename(tempFile.Name(), cachePath)
	if err != nil {
		os.Remove(tempFile.Name())
		return "", fmt.Errorf("failed to move plugin to cache: %w", err)
	}

	return cachePath, nil
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
			cacheFilename := fmt.Sprintf("%s-%s.so", entry.Name, entry.Version)
			if entry.Version == "" {
				cacheFilename = fmt.Sprintf("%s.so", entry.Name)
			}
			cachePath := filepath.Join(d.CacheDir, cacheFilename)

			if !d.isCacheValid(cachePath, entry) {
				needsUpdate = append(needsUpdate, entry)
			}
		}
	}

	return needsUpdate, nil
}

