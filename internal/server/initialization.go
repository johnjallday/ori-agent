// Package server provides the HTTP server for the Ori Agent application.
// This file contains initialization helper functions used by the ServerBuilder
// to construct and configure server components.
package server

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/johnjallday/ori-agent/internal/agentstudio"
	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/location"
	"github.com/johnjallday/ori-agent/internal/plugindownloader"
	"github.com/johnjallday/ori-agent/internal/registry"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

// loadDefaultSettings returns the default server settings configuration.
func loadDefaultSettings() types.Settings {
	return types.Settings{
		Model:        "gpt-5-nano",
		Temperature:  1,
		SystemPrompt: "You are a helpful assistant with access to various tools. When a user request can be fulfilled by using an available tool, use the tool instead of providing general information. Be concise and direct in your responses.",
	}
}

// createConfigManager initializes and loads the configuration manager.
func createConfigManager(configPath string) (*config.Manager, error) {
	mgr := config.NewManager(configPath)
	if err := mgr.Load(); err != nil {
		return nil, fmt.Errorf("failed to load configuration: %w", err)
	}
	return mgr, nil
}

// createRegistryManager initializes the plugin registry manager and refreshes from GitHub.
func createRegistryManager() (*registry.Manager, error) {
	mgr := registry.NewManager()

	// Refresh plugin registry from GitHub on startup
	if err := mgr.RefreshFromGitHub(); err != nil {
		log.Printf("⚠️  Failed to refresh plugin registry from GitHub: %v", err)
		log.Printf("   Will use cached or local registry")
	}

	return mgr, nil
}

// createLLMFactory creates a new LLM factory instance.
func createLLMFactory() *llm.Factory {
	return llm.NewFactory()
}

// registerLLMProviders registers all available LLM providers (OpenAI, Claude, Ollama).
func registerLLMProviders(factory *llm.Factory, configMgr *config.Manager) error {
	verbose := os.Getenv("ORI_VERBOSE") == "true"

	// Register OpenAI provider if API key is available
	apiKey := configMgr.GetAPIKey()
	if apiKey != "" {
		openaiProvider := llm.NewOpenAIProvider(llm.ProviderConfig{
			APIKey: apiKey,
		})
		factory.Register("openai", openaiProvider)
		if verbose {
			log.Printf("✅ OpenAI provider registered")
		}
	} else {
		log.Printf("⚠️  OPENAI_API_KEY not set - OpenAI provider will be unavailable")
		log.Printf("   You can configure it later in the Settings page")
	}

	// Register Claude provider if API key is available
	claudeAPIKey := configMgr.GetAnthropicAPIKey()
	if claudeAPIKey != "" {
		claudeProvider := llm.NewClaudeProvider(llm.ProviderConfig{
			APIKey: claudeAPIKey,
		})
		factory.Register("claude", claudeProvider)
		if verbose {
			log.Printf("Claude provider registered")
		}
	}

	// Register Ollama provider (always available, no API key required)
	ollamaBaseURL := os.Getenv("OLLAMA_BASE_URL")
	if ollamaBaseURL == "" {
		ollamaBaseURL = "http://localhost:11434"
	}
	ollamaProvider := llm.NewOllamaProvider(llm.ProviderConfig{
		BaseURL: ollamaBaseURL,
	})
	factory.Register("ollama", ollamaProvider)
	if verbose {
		log.Printf("Ollama provider registered (base URL: %s)", ollamaBaseURL)
	}

	return nil
}

// resolveAgentStorePath determines the agent store path from environment or default.
func resolveAgentStorePath() (string, error) {
	agentStorePath := "agents.json"
	if p := os.Getenv("AGENT_STORE_PATH"); p != "" {
		agentStorePath = p
	} else if abs, err := filepath.Abs(agentStorePath); err == nil {
		agentStorePath = abs
	}

	verbose := os.Getenv("ORI_VERBOSE") == "true"
	if verbose {
		log.Printf("Using agent store: %s", agentStorePath)
	}

	return agentStorePath, nil
}

// createFileStore creates a new file-based storage system for agents.
func createFileStore(agentStorePath string, defaultConf types.Settings) (store.Store, error) {
	st, err := store.NewFileStore(agentStorePath, defaultConf)
	if err != nil {
		return nil, fmt.Errorf("failed to create file store: %w", err)
	}
	return st, nil
}

// resolvePluginCacheDir determines the plugin cache directory from environment or default.
func resolvePluginCacheDir() string {
	pluginCacheDir := "plugin_cache"
	if p := os.Getenv("PLUGIN_CACHE_DIR"); p != "" {
		pluginCacheDir = p
	} else if abs, err := filepath.Abs(pluginCacheDir); err == nil {
		pluginCacheDir = abs
	}

	verbose := os.Getenv("ORI_VERBOSE") == "true"
	if verbose {
		log.Printf("Using plugin cache: %s", pluginCacheDir)
	}

	return pluginCacheDir
}

// createPluginDownloader creates a new plugin downloader instance.
func createPluginDownloader(cacheDir string) *plugindownloader.PluginDownloader {
	return plugindownloader.NewDownloader(cacheDir)
}

// refreshLocalPluginRegistry refreshes the local plugin registry from uploaded_plugins directory.
func refreshLocalPluginRegistry(mgr *registry.Manager) error {
	verbose := os.Getenv("ORI_VERBOSE") == "true"
	if err := mgr.RefreshLocalRegistry(); err != nil {
		if verbose {
			log.Printf("Warning: failed to refresh local plugin registry: %v", err)
		}
		return err
	}
	return nil
}

// loadLocationZones loads location zones from the specified file path.
func loadLocationZones(zonesPath string) ([]location.Zone, error) {
	verbose := os.Getenv("ORI_VERBOSE") == "true"

	zones, err := location.LoadZones(zonesPath)
	if err != nil {
		if verbose {
			log.Printf("Warning: failed to load location zones: %v", err)
		}
		return []location.Zone{}, nil // Return empty zones instead of error
	}

	if verbose {
		log.Printf("📍 Loaded %d location zones", len(zones))
	}

	return zones, nil
}

// createLocationManager creates and starts the location manager with detectors.
func createLocationManager(zones []location.Zone, zonesFilePath string) *location.Manager {
	verbose := os.Getenv("ORI_VERBOSE") == "true"

	// Create detectors
	manualDetector := location.NewManualDetector()
	wifiDetector := location.NewWiFiDetector()
	detectors := []location.Detector{manualDetector, wifiDetector}

	// Initialize location manager
	mgr := location.NewManager(detectors, zones)

	// Set zones file path for persistence
	mgr.SetZonesFilePath(zonesFilePath)

	if verbose {
		log.Printf("📍 Location manager initialized and detection started")
	}

	return mgr
}

// resolveWorkspaceDir determines the workspace directory from environment or default.
func resolveWorkspaceDir() string {
	workspaceDir := "workspaces"
	if p := os.Getenv("WORKSPACE_DIR"); p != "" {
		workspaceDir = p
	} else if abs, err := filepath.Abs(workspaceDir); err == nil {
		workspaceDir = abs
	}

	verbose := os.Getenv("ORI_VERBOSE") == "true"
	if verbose {
		log.Printf("Using workspace directory: %s", workspaceDir)
	}

	return workspaceDir
}

// createWorkspaceStore creates a new file-based workspace storage system.
func createWorkspaceStore(workspaceDir string) (agentstudio.Store, error) {
	ws, err := agentstudio.NewFileStore(workspaceDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create workspace store: %w", err)
	}
	return ws, nil
}

// resolveCostTrackerDir determines the cost tracker data directory.
func resolveCostTrackerDir() string {
	return filepath.Join(os.Getenv("HOME"), ".ori-agent", "usage_data")
}

// resolveActivityLogDir determines the activity log directory.
func resolveActivityLogDir() string {
	activityLogDir := "activity_logs"
	if abs, err := filepath.Abs(activityLogDir); err == nil {
		activityLogDir = abs
	}
	return activityLogDir
}

// resolveLocationZonesPath determines the location zones file path.
func resolveLocationZonesPath() string {
	locationZonesPath := "locations.json"
	if abs, err := filepath.Abs(locationZonesPath); err == nil {
		locationZonesPath = abs
	}

	verbose := os.Getenv("ORI_VERBOSE") == "true"
	if verbose {
		log.Printf("Using location zones file: %s", locationZonesPath)
	}

	return locationZonesPath
}

// resolveWorkflowTemplatesDir determines the workflow templates directory.
func resolveWorkflowTemplatesDir() string {
	templatesDir := "workflow_templates"
	if p := os.Getenv("WORKFLOW_TEMPLATES_DIR"); p != "" {
		templatesDir = p
	} else if abs, err := filepath.Abs(templatesDir); err == nil {
		templatesDir = abs
	}
	return templatesDir
}
