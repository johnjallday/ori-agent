// Package server provides the HTTP server for the Ori Agent application.
// This file contains initialization helper functions used by the ServerBuilder
// to construct and configure server components.
package server

import (
	"fmt"

	"os"
	"path/filepath"

	"github.com/johnjallday/ori-agent/internal/authdiscovery"
	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/location"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/vault"
	"github.com/johnjallday/ori-agent/internal/workspace"
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
	mgr := config.NewManagerWithSecretStore(configPath, vault.NewDefaultSecretStoreForNamespace(configPath))
	if err := mgr.Load(); err != nil {
		return nil, fmt.Errorf("failed to load configuration: %w", err)
	}
	return mgr, nil
}

// createLLMFactory creates a new LLM factory instance.
func createLLMFactory() *llm.Factory {
	return llm.NewFactory()
}

// registerLLMProviders registers all available LLM providers.
func registerLLMProviders(factory *llm.Factory, configMgr *config.Manager) error {
	verbose := os.Getenv("ORI_VERBOSE") == "true"

	// Register OpenAI provider.
	apiKey := configMgr.GetAPIKey()
	if apiKey != "" {
		openaiProvider := llm.NewOpenAIProvider(llm.ProviderConfig{
			APIKey: apiKey,
		})
		factory.Register("openai", openaiProvider)
		if verbose {
			logger.Info("OpenAI provider registered", logger.Fields{})
		}
	} else {
		logger.Warn("OPENAI_API_KEY not set - OpenAI provider will be unavailable", logger.Fields{})
		logger.Debug("You can configure it later in the Settings page", logger.Fields{})
	}

	// Register Codex provider whenever Codex credentials are available.
	// This is independent of the "External Agents > Codex" toggle, which only
	// controls reading external Codex agents/skills into Ori.
	creds, source, err := authdiscovery.DiscoverCodexCredentialsWithSource()
	if err != nil {
		if verbose {
			logger.Debug("Codex credentials not found", logger.Fields{"error": err})
		}
	} else {
		refreshed, refreshErr := creds.RefreshIfNeeded()
		if refreshErr != nil {
			logger.Warn("Codex token refresh failed", logger.Fields{"error": refreshErr})
		} else if refreshed {
			if err := authdiscovery.PersistCodexCredentials(source, creds); err != nil {
				logger.Warn("Codex token refresh persisted failed", logger.Fields{"error": err})
			}
		}

		codexProvider, err := llm.NewCodexProvider()
		if err != nil {
			logger.Warn("Codex provider unavailable", logger.Fields{"error": err})
		} else {
			factory.Register("codex", codexProvider)
			if verbose {
				logger.Info("Codex provider registered", logger.Fields{})
			}
		}
	}

	// Register Claude Code provider if Claude CLI credentials are available
	if token, err := authdiscovery.DiscoverClaudeToken(); err != nil {
		if verbose {
			logger.Debug("Claude Code credentials not found", logger.Fields{"error": err})
		}
	} else if token != "" {
		claudeCodeProvider, err := llm.NewClaudeCodeProvider()
		if err != nil {
			logger.Warn("Claude Code provider unavailable", logger.Fields{"error": err})
		} else {
			factory.Register("claude_code", claudeCodeProvider)
			if verbose {
				logger.Info("Claude Code provider registered", logger.Fields{})
			}
		}
	}

	// Register Claude provider if API key is available
	claudeAPIKey := configMgr.GetAnthropicAPIKey()
	if verbose {
		logger.Debug("Checking for Claude API key", logger.Fields{"hasKey": claudeAPIKey != "", "keyLength": len(claudeAPIKey)})
	}
	if claudeAPIKey != "" {
		claudeProvider := llm.NewClaudeProvider(llm.ProviderConfig{
			APIKey: claudeAPIKey,
		})
		factory.Register("claude", claudeProvider)
		if verbose {
			logger.Debug("Claude provider registered", logger.Fields{})
		}
	} else if verbose {
		logger.Debug("Claude provider NOT registered (no key)", logger.Fields{})
	}

	// Register Gemini provider if API key is available
	geminiAPIKey := configMgr.GetGeminiAPIKey()
	if geminiAPIKey != "" {
		geminiProvider := llm.NewGeminiProvider(llm.ProviderConfig{
			APIKey: geminiAPIKey,
		})
		factory.Register("gemini", geminiProvider)
		if verbose {
			logger.Debug("Gemini provider registered", logger.Fields{})
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
		logger.Debug("Ollama provider registered", logger.Fields{"base_url": ollamaBaseURL})
	}

	// Register LM Studio provider (OpenAI-compatible local server)
	lmStudioBaseURL := os.Getenv("LM_STUDIO_BASE_URL")
	lmStudioModel := os.Getenv("LM_STUDIO_MODEL")
	lmStudioProvider := llm.NewLMStudioProvider(llm.ProviderConfig{
		BaseURL: lmStudioBaseURL,
		Model:   lmStudioModel,
	})
	factory.Register("lmstudio", lmStudioProvider)
	if verbose {
		logger.Debug("LM Studio provider registered", logger.Fields{
			"lmStudioBaseURL": lmStudioBaseURL,
			"lmStudioModel":   lmStudioModel,
		})
	}

	// Register MLX-LM provider (mlx_lm.server OpenAI-compatible endpoint)
	mlxLMBaseURL := os.Getenv("MLX_LM_BASE_URL")
	mlxLMModel := os.Getenv("MLX_LM_MODEL")
	mlxLMProvider := llm.NewMLXLMProvider(llm.ProviderConfig{
		BaseURL: mlxLMBaseURL,
		Model:   mlxLMModel,
	})
	factory.Register("mlx_lm", mlxLMProvider)
	if verbose {
		logger.Debug("MLX-LM provider registered", logger.Fields{
			"mlxLMBaseURL": mlxLMBaseURL,
			"mlxLMModel":   mlxLMModel,
		})
	}

	return nil
}

// resolveAgentStorePath determines the agent store path from environment or default.
func resolveAgentStorePath() string {
	agentStorePath := "agents.json"
	if p := os.Getenv("AGENT_STORE_PATH"); p != "" {
		agentStorePath = p
	} else if abs, err := filepath.Abs(agentStorePath); err == nil {
		agentStorePath = abs
	}

	verbose := os.Getenv("ORI_VERBOSE") == "true"
	if verbose {
		logger.Debug("Using agent store", logger.Fields{"path": agentStorePath})
	}

	return agentStorePath
}

// createFileStore creates a new file-based storage system for agents.
func createFileStore(agentStorePath string, defaultConf types.Settings) (store.Store, error) {
	st, err := store.NewFileStore(agentStorePath, defaultConf)
	if err != nil {
		return nil, fmt.Errorf("failed to create file store: %w", err)
	}
	return st, nil
}

// loadLocationZones loads location zones from the specified file path.
func loadLocationZones(zonesPath string) []location.Zone {
	verbose := os.Getenv("ORI_VERBOSE") == "true"

	zones, err := location.LoadZones(zonesPath)
	if err != nil {
		if verbose {
			logger.Error("failed to load location zones", logger.Fields{"err": err})
		}
		return []location.Zone{}
	}

	if verbose {
		logger.Debug("📍 Loaded location zones", logger.Fields{"zone_count": len(zones)})
	}

	return zones
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
		logger.Debug("📍 Location manager initialized and detection started", logger.Fields{})
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
		logger.Debug("Using workspace directory", logger.Fields{"path": workspaceDir})
	}

	return workspaceDir
}

// resolveWorkspaceRoot determines the root directory for workspace folders.
// Priority: 1) settings workspace_root, 2) WORKSPACE_DIR env, 3) ~/Ori Workspaces
func resolveWorkspaceRoot(configManager *config.Manager) string {
	// Check settings first
	if configManager != nil {
		if root := configManager.GetWorkspaceRoot(); root != "" {
			return root
		}
	}

	return config.ResolveWorkspaceRoot("")
}

// resolveVaultRoot determines the root directory for new managed vault files.
// Priority: 1) settings vault_root, 2) ORI_VAULT_DIR env, 3) current data dir + /vaults
func resolveVaultRoot(configManager *config.Manager) string {
	if configManager != nil {
		if root := configManager.GetVaultRoot(); root != "" {
			return root
		}
	}

	return config.ResolveVaultRoot("")
}

// resolveTemplatesRoot determines the directory holding project template folders.
// Priority: 1) settings templates_root, 2) ORI_TEMPLATES_DIR env, 3) current data dir + /templates
func resolveTemplatesRoot(configManager *config.Manager) string {
	if configManager != nil {
		if root := configManager.GetTemplatesRoot(); root != "" {
			return root
		}
	}

	return config.ResolveTemplatesRoot("")
}

// createWorkspaceStore creates a new file-based workspace storage system.
func createWorkspaceStore(workspaceDir string) (workspace.Store, error) {
	ws, err := workspace.NewFileStore(workspaceDir)
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
		logger.Debug("Using location zones file", logger.Fields{"locationZonesPath": locationZonesPath})
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
