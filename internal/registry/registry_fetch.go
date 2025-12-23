package registry

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/types"
)

// shouldFetchFromGitHub checks if enough time has passed since the last fetch
// Uses in-memory tracking only - no file-based caching
func (m *Manager) shouldFetchFromGitHub() bool {
	if m.lastFetchTime.IsZero() {
		return true // Never fetched this session
	}
	return time.Since(m.lastFetchTime) >= m.fetchInterval
}

// RefreshFromGitHub forces a refresh from GitHub on startup
func (m *Manager) RefreshFromGitHub() error {
	fmt.Println("🔄 Refreshing plugin registry from GitHub...")

	_, err := m.fetchGitHubPluginRegistry()
	if err != nil {
		return fmt.Errorf("failed to fetch from GitHub: %w", err)
	}

	fmt.Println("✅ Plugin registry refreshed successfully")
	return nil
}

// fetchGitHubPluginRegistry fetches the plugin registry from GitHub
func (m *Manager) fetchGitHubPluginRegistry() (types.PluginRegistry, error) {
	var reg types.PluginRegistry

	resp, err := http.Get("https://raw.githubusercontent.com/johnjallday/ori-plugin-registry/main/plugin_registry.json")
	if err != nil {
		return reg, fmt.Errorf("failed to fetch from GitHub: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

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
			meta.Tags = normalizeTagsWithWarnings(meta.Name, meta.Tags)
			entry := types.PluginRegistryEntry{
				Name:        meta.Name,
				Description: meta.Description,
				Tags:        meta.Tags,
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

// FetchFromMarketplace fetches plugins from a single marketplace
func (m *Manager) FetchFromMarketplace(mp types.Marketplace) (types.PluginRegistry, error) {
	var reg types.PluginRegistry

	url := mp.ResolveURL()
	var data []byte
	if mp.SourceType == "file" || types.DetectMarketplaceSourceType(mp.Source) == "file" {
		path, err := types.ResolveLocalMarketplacePath(mp.Source)
		if err != nil {
			return reg, fmt.Errorf("failed to resolve local marketplace %s: %w", mp.Name, err)
		}
		data, err = os.ReadFile(path)
		if err != nil {
			return reg, fmt.Errorf("failed to read local marketplace %s: %w", mp.Name, err)
		}
	} else {
		resp, err := http.Get(url)
		if err != nil {
			return reg, fmt.Errorf("failed to fetch from %s: %w", mp.Name, err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			return reg, fmt.Errorf("marketplace %s returned status %d", mp.Name, resp.StatusCode)
		}

		data, err = io.ReadAll(resp.Body)
		if err != nil {
			return reg, fmt.Errorf("failed to read response from %s: %w", mp.Name, err)
		}
	}

	// Try to parse as the new metadata format first
	var metadataReg struct {
		Plugins []types.PluginMetadata `json:"plugins"`
	}
	if err := json.Unmarshal(data, &metadataReg); err == nil && len(metadataReg.Plugins) > 0 {
		// Convert metadata format to registry entry format
		reg.Plugins = make([]types.PluginRegistryEntry, len(metadataReg.Plugins))
		for i, meta := range metadataReg.Plugins {
			meta.Tags = normalizeTagsWithWarnings(meta.Name, meta.Tags)
			entry := types.PluginRegistryEntry{
				Name:              meta.Name,
				Description:       meta.Description,
				Tags:              meta.Tags,
				Version:           meta.Version,
				Metadata:          &meta,
				SourceMarketplace: mp.ID,
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
				repoURL := strings.TrimSuffix(meta.Repository, ".git")
				repoURL = strings.TrimSuffix(repoURL, "/")
				if repoURL == "" {
					repoURL = meta.Repository
				}
				entry.GitHubRepo = repoURL

				repoName := strings.TrimSuffix(filepath.Base(repoURL), ".git")

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
			return reg, fmt.Errorf("failed to parse plugin registry JSON from %s: %w", mp.Name, err)
		}
		// Tag plugins with source marketplace
		for i := range reg.Plugins {
			reg.Plugins[i].SourceMarketplace = mp.ID
		}
	}

	return reg, nil
}

// FetchAllMarketplaces fetches from all enabled marketplaces and merges results
func (m *Manager) FetchAllMarketplaces() (types.PluginRegistry, error) {
	if m.marketplaceStore == nil {
		// Fall back to single GitHub registry
		return m.fetchGitHubPluginRegistry()
	}

	enabledMarketplaces := m.marketplaceStore.GetEnabled()
	if len(enabledMarketplaces) == 0 {
		return types.PluginRegistry{}, nil
	}

	var allRegistries []types.PluginRegistry
	var marketplaceOrder []types.Marketplace

	for _, mp := range enabledMarketplaces {
		reg, err := m.FetchFromMarketplace(mp)
		if err != nil {
			fmt.Printf("Warning: failed to fetch from marketplace %s: %v\n", mp.Name, err)
			// Update marketplace with error
			now := time.Now()
			mp.LastFetched = &now
			mp.LastError = err.Error()
			_ = m.marketplaceStore.SetLastFetched(mp.ID, &mp)
			continue
		}

		// Update marketplace with success
		now := time.Now()
		mp.LastFetched = &now
		mp.LastError = ""
		_ = m.marketplaceStore.SetLastFetched(mp.ID, &mp)

		allRegistries = append(allRegistries, reg)
		marketplaceOrder = append(marketplaceOrder, mp)
	}

	return m.MergeWithPriority(allRegistries, marketplaceOrder), nil
}

// MergeWithPriority merges multiple registries respecting marketplace priority
// First match wins for duplicate plugin names, but tracks all source marketplaces
func (m *Manager) MergeWithPriority(registries []types.PluginRegistry, marketplaces []types.Marketplace) types.PluginRegistry {
	merged := types.PluginRegistry{}
	seen := make(map[string]int) // Maps normalized plugin name to index in merged.Plugins

	// Sort marketplaces by order (should already be sorted, but ensure it)
	sort.Slice(marketplaces, func(i, j int) bool {
		return marketplaces[i].Order < marketplaces[j].Order
	})

	// Process registries in order - first match wins for data, but track all sources
	for i, reg := range registries {
		for _, plugin := range reg.Plugins {
			// Ensure source marketplace is set
			if plugin.SourceMarketplace == "" && i < len(marketplaces) {
				plugin.SourceMarketplace = marketplaces[i].ID
			}

			// Normalize name for comparison (handles underscore vs hyphen variations)
			normalizedName := NormalizePluginName(plugin.Name)

			if idx, exists := seen[normalizedName]; exists {
				// Plugin already exists - add this marketplace to SourceMarketplaces if not already present
				existingPlugin := &merged.Plugins[idx]
				alreadyTracked := false
				for _, mp := range existingPlugin.SourceMarketplaces {
					if mp == plugin.SourceMarketplace {
						alreadyTracked = true
						break
					}
				}
				if !alreadyTracked && plugin.SourceMarketplace != "" {
					existingPlugin.SourceMarketplaces = append(existingPlugin.SourceMarketplaces, plugin.SourceMarketplace)
				}
			} else {
				// First occurrence - initialize SourceMarketplaces with current marketplace
				if plugin.SourceMarketplace != "" {
					plugin.SourceMarketplaces = []string{plugin.SourceMarketplace}
				}
				merged.Plugins = append(merged.Plugins, plugin)
				seen[normalizedName] = len(merged.Plugins) - 1
			}
		}
	}

	return merged
}

// Merge combines online and local plugin registries
// Online plugins are preserved even if a local version exists, so the marketplace
// can still show them (marked as installed). Local-only plugins are also included.
// When a plugin exists in both registries, the local path is preserved so installed
// plugins appear in the sidebar.
func (m *Manager) Merge(online, local types.PluginRegistry) types.PluginRegistry {
	merged := types.PluginRegistry{}

	// Create maps to track plugins using normalized names
	onlineMap := make(map[string]types.PluginRegistryEntry)
	localMap := make(map[string]types.PluginRegistryEntry)

	// Index online plugins by normalized name
	for _, plugin := range online.Plugins {
		onlineMap[NormalizePluginName(plugin.Name)] = plugin
	}

	// Index local plugins by normalized name
	for _, plugin := range local.Plugins {
		localMap[NormalizePluginName(plugin.Name)] = plugin
	}

	// Add online plugins, merging with local path if installed
	for _, plugin := range online.Plugins {
		normalizedName := NormalizePluginName(plugin.Name)
		if localPlugin, existsLocal := localMap[normalizedName]; existsLocal {
			// Plugin exists locally - copy the path so it shows as installed
			plugin.Path = localPlugin.Path
		}
		merged.Plugins = append(merged.Plugins, plugin)
	}

	// Add local-only plugins (user uploads that aren't in the online registry)
	for _, plugin := range local.Plugins {
		normalizedName := NormalizePluginName(plugin.Name)
		if _, existsOnline := onlineMap[normalizedName]; !existsOnline {
			merged.Plugins = append(merged.Plugins, plugin)
		}
	}

	return merged
}
