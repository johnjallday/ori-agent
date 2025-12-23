package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/marketplace"
	internaltags "github.com/johnjallday/ori-agent/internal/tags"
	"github.com/johnjallday/ori-agent/internal/types"
	"gopkg.in/yaml.v3"
)

type pluginYAMLManifest struct {
	Name        string   `yaml:"name"`
	Version     string   `yaml:"version"`
	Description string   `yaml:"description"`
	Tags        []string `yaml:"tags"`
}

func normalizeTagsWithWarnings(pluginName string, rawTags []string) []string {
	if len(rawTags) == 0 {
		return nil
	}

	if _, errs := internaltags.ValidateTags(rawTags); len(errs) > 0 {
		fmt.Printf("Warning: plugin %q has invalid tags: %v\n", pluginName, errs)
	}
	if len(rawTags) > 5 {
		fmt.Printf("Warning: plugin %q has more than 5 tags; truncating to 5\n", pluginName)
	}

	return internaltags.NormalizeTags(rawTags)
}

func loadManifestForUploadedPlugin(pluginName, pluginPath string) (*pluginYAMLManifest, error) {
	candidates := []string{
		pluginPath + ".yaml",
		pluginPath + ".plugin.yaml",
		filepath.Join("..", "plugins", pluginName, "plugin.yaml"),
	}

	for _, p := range candidates {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var m pluginYAMLManifest
		if err := yaml.Unmarshal(b, &m); err != nil {
			return nil, fmt.Errorf("failed to parse manifest %q: %w", p, err)
		}
		return &m, nil
	}

	return nil, fmt.Errorf("no manifest found for %q", pluginName)
}

// Manager handles plugin registry operations
type Manager struct {
	localRegistryPath  string
	uploadedPluginsDir string
	lastFetchTime      time.Time
	fetchInterval      time.Duration
	mu                 sync.RWMutex // Protects concurrent access to local registry file
	marketplaceStore   *marketplace.Store
}

// NewManager creates a new registry manager
func NewManager() *Manager {
	return &Manager{
		localRegistryPath:  "local_plugin_registry.json",
		uploadedPluginsDir: "uploaded_plugins",
		fetchInterval:      12 * time.Hour, // Refresh every 12 hours
	}
}

// NewManagerWithMarketplaces creates a new registry manager with marketplace support
func NewManagerWithMarketplaces(mpStore *marketplace.Store) *Manager {
	return &Manager{
		localRegistryPath:  "local_plugin_registry.json",
		uploadedPluginsDir: "uploaded_plugins",
		fetchInterval:      12 * time.Hour,
		marketplaceStore:   mpStore,
	}
}

// SetMarketplaceStore sets the marketplace store for multi-marketplace support
func (m *Manager) SetMarketplaceStore(mpStore *marketplace.Store) {
	m.marketplaceStore = mpStore
}

// NormalizePluginName normalizes a plugin name for comparison
// Converts underscores to hyphens and lowercases the name
func NormalizePluginName(name string) string {
	return strings.ToLower(strings.ReplaceAll(name, "_", "-"))
}

var pluginVersionSuffixPattern = regexp.MustCompile(`^(.+)-v?\d+\.\d+\.\d+(?:[-+].+)?$`)

// NormalizePluginNameForLookup normalizes and strips version suffixes for name matching.
func NormalizePluginNameForLookup(name string) string {
	normalized := NormalizePluginName(name)
	matches := pluginVersionSuffixPattern.FindStringSubmatch(normalized)
	if len(matches) == 2 && matches[1] != "" {
		return matches[1]
	}
	return normalized
}

func findPluginIndexByName(plugins []types.PluginRegistryEntry, pluginName string) int {
	needle := NormalizePluginNameForLookup(pluginName)
	for i := range plugins {
		if NormalizePluginNameForLookup(plugins[i].Name) == needle {
			return i
		}
	}
	return -1
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

	// 2) Multi-marketplace support - if marketplace store is set, use it
	if m.marketplaceStore != nil {
		// Fetch from marketplaces dynamically - no file caching
		mpReg, err := m.FetchAllMarketplaces()
		if err == nil && len(mpReg.Plugins) > 0 {
			onlineReg = mpReg
		} else {
			if err != nil {
				fmt.Printf("Failed to load plugin registry from marketplaces: %v\n", err)
			}
			// Fallback to GitHub if marketplaces are empty or failed.
			if m.shouldFetchFromGitHub() {
				if githubReg, ghErr := m.fetchGitHubPluginRegistry(); ghErr == nil && len(githubReg.Plugins) > 0 {
					onlineReg = githubReg
				} else if ghErr != nil {
					fmt.Printf("Failed to load plugin registry from GitHub: %v\n", ghErr)
				}
			}
		}
	} else {
		// Fallback: Try to fetch from single GitHub source (legacy behavior)
		if m.shouldFetchFromGitHub() {
			if githubReg, err := m.fetchGitHubPluginRegistry(); err == nil {
				onlineReg = githubReg
			} else {
				fmt.Printf("Failed to load plugin registry from GitHub: %v\n", err)
			}
		}
	}

	// If online fetch failed, we'll still show locally installed plugins

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
