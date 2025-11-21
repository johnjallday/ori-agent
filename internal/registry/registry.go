package registry

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/pluginloader"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/web"
)

// Manager handles plugin registry operations
type Manager struct {
	cachePath          string
	localRegistryPath  string
	uploadedPluginsDir string
	lastFetchTime      time.Time
	fetchInterval      time.Duration
}

// NewManager creates a new registry manager
func NewManager() *Manager {
	return &Manager{
		cachePath:          "plugin_registry_cache.json",
		localRegistryPath:  "local_plugin_registry.json",
		uploadedPluginsDir: "uploaded_plugins",
		fetchInterval:      12 * time.Hour, // Refresh every 12 hours
	}
}

// shouldFetchFromGitHub checks if enough time has passed since the last fetch
func (m *Manager) shouldFetchFromGitHub() bool {
	// If we've never fetched, check if cache file exists and is recent
	if m.lastFetchTime.IsZero() {
		// Check cache file modification time
		if info, err := os.Stat(m.cachePath); err == nil {
			cacheAge := time.Since(info.ModTime())
			if cacheAge < m.fetchInterval {
				// Cache is fresh, use it instead of fetching
				m.lastFetchTime = info.ModTime()
				return false
			}
		}
		// No cache or cache is stale, fetch from GitHub
		return true
	}

	// Check if enough time has passed since last fetch
	return time.Since(m.lastFetchTime) >= m.fetchInterval
}

// RefreshFromGitHub forces a refresh from GitHub on startup
func (m *Manager) RefreshFromGitHub() error {
	fmt.Println("🔄 Refreshing plugin registry from GitHub...")

	githubReg, err := m.fetchGitHubPluginRegistry()
	if err != nil {
		return fmt.Errorf("failed to fetch from GitHub: %w", err)
	}

	// Cache it for offline use
	if data, marshalErr := json.MarshalIndent(githubReg, "", "  "); marshalErr == nil {
		if writeErr := os.WriteFile(m.cachePath, data, 0644); writeErr != nil {
			fmt.Printf("Warning: failed to cache registry: %v\n", writeErr)
		} else {
			fmt.Println("✅ Plugin registry cache updated successfully")
		}
	}

	return nil
}

// fetchGitHubPluginRegistry fetches the plugin registry from GitHub
func (m *Manager) fetchGitHubPluginRegistry() (types.PluginRegistry, error) {
	var reg types.PluginRegistry

	resp, err := http.Get("https://raw.githubusercontent.com/johnjallday/ori-plugin-registry/main/plugin_registry.json")
	if err != nil {
		return reg, fmt.Errorf("failed to fetch from GitHub: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return reg, fmt.Errorf("GitHub returned status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return reg, fmt.Errorf("failed to read GitHub response: %w", err)
	}

	// Try to parse as the new metadata format first
	var metadataReg struct {
		Plugins []types.PluginMetadata `json:"plugins"`
	}
	if err := json.Unmarshal(data, &metadataReg); err == nil && len(metadataReg.Plugins) > 0 {
		// Convert metadata format to registry entry format
		reg.Plugins = make([]types.PluginRegistryEntry, len(metadataReg.Plugins))
		for i, meta := range metadataReg.Plugins {
			entry := types.PluginRegistryEntry{
				Name:        meta.Name,
				Description: meta.Description,
				Version:     meta.Version,
				Metadata:    &meta,
			}

			// Extract supported OS, arch, and explicit platform combos
			if len(meta.Platforms) > 0 {
				osSet := make(map[string]bool)
				archSet := make(map[string]bool)
				var platformCombos []string

				for _, platform := range meta.Platforms {
					osSet[platform.Os] = true
					for _, arch := range platform.Architectures {
						archSet[arch] = true
						platformCombos = append(platformCombos, fmt.Sprintf("%s-%s", platform.Os, arch))
					}
				}

				entry.SupportedOS = make([]string, 0, len(osSet))
				for os := range osSet {
					entry.SupportedOS = append(entry.SupportedOS, os)
				}

				entry.SupportedArch = make([]string, 0, len(archSet))
				for arch := range archSet {
					entry.SupportedArch = append(entry.SupportedArch, arch)
				}

				entry.Platforms = platformCombos
			}

			// Set GitHub repo and download URL if available in repository field
			if meta.Repository != "" {
				// Normalize repository URL (drop trailing slash or .git suffix)
				repoURL := strings.TrimSuffix(meta.Repository, ".git")
				repoURL = strings.TrimSuffix(repoURL, "/")
				if repoURL == "" {
					repoURL = meta.Repository
				}
				entry.GitHubRepo = repoURL

				repoName := strings.TrimSuffix(filepath.Base(repoURL), ".git")

				// Default GitHub release asset pattern supports platform-specific binaries
				if strings.Contains(repoURL, "github.com") {
					entry.DownloadURL = fmt.Sprintf("%s/releases/latest/download/%s-{os}-{arch}%s", repoURL, repoName, "{ext}")
				} else {
					entry.DownloadURL = fmt.Sprintf("%s/releases/latest/download/%s", repoURL, repoName)
				}
			}

			reg.Plugins[i] = entry
		}
	} else {
		// Fall back to old format
		if err := json.Unmarshal(data, &reg); err != nil {
			return reg, fmt.Errorf("failed to parse GitHub plugin registry JSON: %w", err)
		}
	}

	// Update last fetch time on successful fetch
	m.lastFetchTime = time.Now()

	return reg, nil
}

// LoadLocal loads the user's local plugin registry
func (m *Manager) LoadLocal() (types.PluginRegistry, error) {
	var reg types.PluginRegistry

	if b, err := os.ReadFile(m.localRegistryPath); err == nil {
		if err := json.Unmarshal(b, &reg); err != nil {
			return reg, fmt.Errorf("failed to parse local plugin registry: %w", err)
		}
	}
	return reg, nil
}

// SaveLocal saves the local plugin registry to file
func (m *Manager) SaveLocal(reg types.PluginRegistry) error {
	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal local registry: %w", err)
	}

	if err := os.WriteFile(m.localRegistryPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write local registry: %w", err)
	}

	return nil
}

// ScanUploadedPlugins scans the uploaded_plugins directory and adds any new plugins to local registry
func (m *Manager) ScanUploadedPlugins() error {
	// Check if uploaded_plugins directory exists
	if _, err := os.Stat(m.uploadedPluginsDir); os.IsNotExist(err) {
		return nil // No uploaded plugins directory, nothing to scan
	}

	// Load current local registry
	localReg, err := m.LoadLocal()
	if err != nil {
		return fmt.Errorf("failed to load local registry: %w", err)
	}

	// Create map of existing plugins for quick lookup
	existingPlugins := make(map[string]bool)
	for _, plugin := range localReg.Plugins {
		existingPlugins[plugin.Path] = true
	}

	// Read uploaded_plugins directory
	entries, err := os.ReadDir(m.uploadedPluginsDir)
	if err != nil {
		return fmt.Errorf("failed to read uploaded_plugins directory: %w", err)
	}

	var newPluginsAdded bool
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		filename := entry.Name()
		// Skip hidden files and non-plugin files
		if strings.HasPrefix(filename, ".") {
			continue
		}

		pluginPath := filepath.Join(m.uploadedPluginsDir, filename)

		// Skip if plugin is already in registry
		if existingPlugins[pluginPath] {
			continue
		}

		// Plugin name is the filename (RPC executables don't have extensions)
		pluginName := filename

		// Try to load the plugin to get better information (using unified loader)
		var description, version string
		var metadata *types.PluginMetadata
		if tool, loadErr := pluginloader.LoadPluginUnified(pluginPath); loadErr == nil {
			def := tool.Definition()
			description = def.Description
			version = pluginloader.GetPluginVersion(tool)

			// Extract metadata if available
			if protoMeta, metaErr := pluginloader.GetPluginMetadata(tool); metaErr == nil && protoMeta != nil {
				// Convert pluginapi.PluginMetadata (proto) to types.PluginMetadata
				maintainers := make([]types.Maintainer, len(protoMeta.Maintainers))
				for i, m := range protoMeta.Maintainers {
					maintainers[i] = types.Maintainer{
						Name:         m.Name,
						Email:        m.Email,
						Organization: m.Organization,
						Website:      m.Website,
						Role:         m.Role,
						Primary:      m.Primary,
					}
				}

				// Convert platforms
				platforms := make([]types.Platform, len(protoMeta.Platforms))
				for i, p := range protoMeta.Platforms {
					platforms[i] = types.Platform{
						Os:            p.Os,
						Architectures: p.Architectures,
					}
				}

				// Convert requirements
				requirements := types.Requirements{
					MinOriVersion: protoMeta.Requirements.GetMinOriVersion(),
					Dependencies:  protoMeta.Requirements.GetDependencies(),
				}

				metadata = &types.PluginMetadata{
					Name:         protoMeta.Name,
					Version:      protoMeta.Version,
					Description:  protoMeta.Description,
					Maintainers:  maintainers,
					License:      protoMeta.License,
					Repository:   protoMeta.Repository,
					Platforms:    platforms,
					Requirements: requirements,
				}
			}

			// Clean up RPC plugins after getting metadata
			pluginloader.CloseRPCPlugin(tool)
		}

		// Fallback values if loading failed
		if description == "" {
			description = fmt.Sprintf("Plugin: %s", pluginName)
		}
		if version == "" {
			version = "unknown"
		}

		// Add to registry
		newPlugin := types.PluginRegistryEntry{
			Name:        pluginName,
			Description: description,
			Path:        pluginPath,
			Version:     version,
			Metadata:    metadata,
		}

		localReg.Plugins = append(localReg.Plugins, newPlugin)
		newPluginsAdded = true

		fmt.Printf("Auto-registered plugin: %s (%s) from %s\n", pluginName, version, pluginPath)
	}

	// Save updated registry if changes were made
	if newPluginsAdded {
		if err := m.SaveLocal(localReg); err != nil {
			return fmt.Errorf("failed to save updated local registry: %w", err)
		}
		fmt.Printf("Updated local plugin registry with new plugins from uploaded_plugins/\n")
	}

	return nil
}

// RefreshLocalRegistry completely rebuilds the local registry from uploaded_plugins directory
// This refreshes all metadata (version, description) for all plugins
func (m *Manager) RefreshLocalRegistry() error {
	// Check if uploaded_plugins directory exists
	if _, err := os.Stat(m.uploadedPluginsDir); os.IsNotExist(err) {
		// No uploaded plugins directory - create empty registry
		emptyReg := types.PluginRegistry{Plugins: []types.PluginRegistryEntry{}}
		return m.SaveLocal(emptyReg)
	}

	// Create new registry from scratch
	newReg := types.PluginRegistry{
		Plugins: []types.PluginRegistryEntry{},
	}

	// Read uploaded_plugins directory
	entries, err := os.ReadDir(m.uploadedPluginsDir)
	if err != nil {
		return fmt.Errorf("failed to read uploaded_plugins directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		filename := entry.Name()
		// Skip hidden files
		if strings.HasPrefix(filename, ".") {
			continue
		}

		pluginPath := filepath.Join(m.uploadedPluginsDir, filename)
		pluginName := filename

		// Try to load the plugin to get metadata
		var description, version string
		var metadata *types.PluginMetadata
		if tool, loadErr := pluginloader.LoadPluginUnified(pluginPath); loadErr == nil {
			def := tool.Definition()
			description = def.Description
			version = pluginloader.GetPluginVersion(tool)

			// Extract metadata if available
			if protoMeta, metaErr := pluginloader.GetPluginMetadata(tool); metaErr == nil && protoMeta != nil {
				// Convert pluginapi.PluginMetadata (proto) to types.PluginMetadata
				maintainers := make([]types.Maintainer, len(protoMeta.Maintainers))
				for i, m := range protoMeta.Maintainers {
					maintainers[i] = types.Maintainer{
						Name:         m.Name,
						Email:        m.Email,
						Organization: m.Organization,
						Website:      m.Website,
						Role:         m.Role,
						Primary:      m.Primary,
					}
				}

				// Convert platforms
				platforms := make([]types.Platform, len(protoMeta.Platforms))
				for i, p := range protoMeta.Platforms {
					platforms[i] = types.Platform{
						Os:            p.Os,
						Architectures: p.Architectures,
					}
				}

				// Convert requirements
				requirements := types.Requirements{
					MinOriVersion: protoMeta.Requirements.GetMinOriVersion(),
					Dependencies:  protoMeta.Requirements.GetDependencies(),
				}

				metadata = &types.PluginMetadata{
					Name:         protoMeta.Name,
					Version:      protoMeta.Version,
					Description:  protoMeta.Description,
					Maintainers:  maintainers,
					License:      protoMeta.License,
					Repository:   protoMeta.Repository,
					Platforms:    platforms,
					Requirements: requirements,
				}
			}

			// Clean up RPC plugins after getting metadata
			pluginloader.CloseRPCPlugin(tool)
		}

		// Fallback values if loading failed
		if description == "" {
			description = fmt.Sprintf("Plugin: %s", pluginName)
		}
		if version == "" {
			version = "unknown"
		}

		// Add to new registry
		newPlugin := types.PluginRegistryEntry{
			Name:        pluginName,
			Description: description,
			Path:        pluginPath,
			Version:     version,
			Metadata:    metadata,
		}

		newReg.Plugins = append(newReg.Plugins, newPlugin)
	}

	// Save refreshed registry
	if err := m.SaveLocal(newReg); err != nil {
		return fmt.Errorf("failed to save refreshed local registry: %w", err)
	}

	fmt.Printf("✅ Refreshed local plugin registry with %d plugin(s) from uploaded_plugins/\n", len(newReg.Plugins))
	return nil
}

// Merge combines online and local plugin registries
func (m *Manager) Merge(online, local types.PluginRegistry) types.PluginRegistry {
	merged := types.PluginRegistry{}

	// Create a map to track plugin names and avoid duplicates
	pluginMap := make(map[string]types.PluginRegistryEntry)

	// Add online plugins first
	for _, plugin := range online.Plugins {
		pluginMap[plugin.Name] = plugin
	}

	// Add local plugins (they override online plugins with same name)
	for _, plugin := range local.Plugins {
		pluginMap[plugin.Name] = plugin
	}

	// Convert map back to slice
	for _, plugin := range pluginMap {
		merged.Plugins = append(merged.Plugins, plugin)
	}

	return merged
}

// ValidateAndUpdateLocal checks that plugins in local registry exist and updates paths if needed
func (m *Manager) ValidateAndUpdateLocal() error {
	// Load current local registry
	localReg, err := m.LoadLocal()
	if err != nil {
		return fmt.Errorf("failed to load local registry: %w", err)
	}

	if len(localReg.Plugins) == 0 {
		return nil // Nothing to validate
	}

	var validPlugins []types.PluginRegistryEntry
	var updated bool

	// Common search locations for plugins
	searchDirs := []string{
		"plugins",
		"uploaded_plugins",
		"example_plugins",
		"../plugins",
		"../uploaded_plugins",
	}

	for _, plugin := range localReg.Plugins {
		// Check if plugin exists at its current path
		if _, err := os.Stat(plugin.Path); err == nil {
			validPlugins = append(validPlugins, plugin)
			continue
		}

		// Plugin doesn't exist at specified path, try to find it
		pluginName := plugin.Name
		found := false
		var newPath string

		// Try each search directory
		for _, dir := range searchDirs {
			// Try with plugin name only
			possiblePath := filepath.Join(dir, pluginName, pluginName)
			if _, err := os.Stat(possiblePath); err == nil {
				newPath = possiblePath
				found = true
				break
			}

			// Try with plugin name directly in directory
			possiblePath = filepath.Join(dir, pluginName)
			if _, err := os.Stat(possiblePath); err == nil {
				newPath = possiblePath
				found = true
				break
			}
		}

		if found {
			fmt.Printf("Updated plugin path: %s -> %s\n", plugin.Path, newPath)
			plugin.Path = newPath
			validPlugins = append(validPlugins, plugin)
			updated = true
		} else {
			fmt.Printf("Plugin not found, removing from registry: %s (was at %s)\n", plugin.Name, plugin.Path)
			updated = true
		}
	}

	// Save updated registry if changes were made
	if updated {
		localReg.Plugins = validPlugins
		if err := m.SaveLocal(localReg); err != nil {
			return fmt.Errorf("failed to save updated local registry: %w", err)
		}
		fmt.Printf("Updated local plugin registry (validated %d plugins, %d valid)\n", len(localReg.Plugins), len(validPlugins))
	}

	return nil
}

// Load reads the registry dynamically with fallbacks, merging online and local registries.
// Returns: registry, baseDir (for resolving relative plugin paths), error.
func (m *Manager) Load() (types.PluginRegistry, string, error) {
	var onlineReg types.PluginRegistry

	// 1) Env override (highest priority) - if set, use only this and merge with local
	if p := os.Getenv("PLUGIN_REGISTRY_PATH"); p != "" {
		if b, err := os.ReadFile(p); err == nil {
			if err := json.Unmarshal(b, &onlineReg); err != nil {
				return onlineReg, "", fmt.Errorf("parse %s: %w", p, err)
			}
			// Merge with local registry
			if localReg, err := m.LoadLocal(); err == nil {
				merged := m.Merge(onlineReg, localReg)
				return merged, p, nil
			}
			return onlineReg, p, nil
		}
	}

	// 2) Try to fetch from GitHub (primary online source) - with time-based caching
	if m.shouldFetchFromGitHub() {
		if githubReg, err := m.fetchGitHubPluginRegistry(); err == nil {
			// Success! Cache it for offline use
			if data, marshalErr := json.MarshalIndent(githubReg, "", "  "); marshalErr == nil {
				_ = os.WriteFile(m.cachePath, data, 0644) // Ignore error - caching is optional
			}
			onlineReg = githubReg
			fmt.Println("plugin registry loaded from GitHub")
		} else {
			fmt.Printf("Failed to load plugin registry from GitHub: %v\n", err)
		}
	}

	// If GitHub fetch was skipped or failed, try other fallback sources
	if len(onlineReg.Plugins) == 0 {
		// 3) Try cached version (use if fresh enough or as offline fallback)
		if b, err := os.ReadFile(m.cachePath); err == nil {
			if err := json.Unmarshal(b, &onlineReg); err == nil {
				fmt.Println("plugin registry loaded from cache")
			} else {
				fmt.Printf("Failed to parse cached plugin registry: %v\n", err)
			}
		}

		// 4) Local files (legacy fallback)
		if len(onlineReg.Plugins) == 0 {
			for _, p := range []string{
				"plugin_registry.json",
				filepath.Join("internal", "web", "static", "plugin_registry.json"),
			} {
				if b, err := os.ReadFile(p); err == nil {
					if err := json.Unmarshal(b, &onlineReg); err == nil {
						fmt.Printf("plugin registry loaded from local file: %s\n", p)
						break
					}
				}
			}
		}

		// 5) Embedded fallback (last resort)
		if len(onlineReg.Plugins) == 0 {
			if b, err := web.Static.ReadFile("static/plugin_registry.json"); err == nil {
				if err := json.Unmarshal(b, &onlineReg); err == nil {
					fmt.Println("plugin registry loaded from embedded file")
				}
			}
		}
	}

	// Merge with local registry
	localReg, _ := m.LoadLocal() // Ignore error - local registry is optional
	merged := m.Merge(onlineReg, localReg)

	// If environment variable was used, return that path as base directory
	if p := os.Getenv("PLUGIN_REGISTRY_PATH"); p != "" {
		return merged, filepath.Dir(p), nil
	}

	// Otherwise return current directory as base
	return merged, ".", nil
}
