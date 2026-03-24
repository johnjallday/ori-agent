// Package server provides HTTP handler initialization methods for the ServerBuilder.
// This file contains the method for initializing all HTTP handlers.
package server

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	agenthttp "github.com/johnjallday/ori-agent/internal/agenthttp"
	"github.com/johnjallday/ori-agent/internal/chathttp"
	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/devicehttp"
	"github.com/johnjallday/ori-agent/internal/evolution"
	"github.com/johnjallday/ori-agent/internal/evolutionhttp"
	"github.com/johnjallday/ori-agent/internal/externalagents"
	"github.com/johnjallday/ori-agent/internal/externalagentshttp"
	"github.com/johnjallday/ori-agent/internal/featureflags"
	"github.com/johnjallday/ori-agent/internal/fileshttp"
	"github.com/johnjallday/ori-agent/internal/filewatcher"
	"github.com/johnjallday/ori-agent/internal/locationhttp"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/mcphttp"
	"github.com/johnjallday/ori-agent/internal/modelcategoryhttp"
	"github.com/johnjallday/ori-agent/internal/notehttp"
	"github.com/johnjallday/ori-agent/internal/onboardinghttp"
	"github.com/johnjallday/ori-agent/internal/review"
	"github.com/johnjallday/ori-agent/internal/reviewhttp"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/sessionfiles"
	"github.com/johnjallday/ori-agent/internal/sessionhttp"
	"github.com/johnjallday/ori-agent/internal/settingshttp"
	"github.com/johnjallday/ori-agent/internal/skills"
	"github.com/johnjallday/ori-agent/internal/skillshttp"
	"github.com/johnjallday/ori-agent/internal/speechhttp"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/usagehttp"
	"github.com/johnjallday/ori-agent/internal/vault"
	"github.com/johnjallday/ori-agent/internal/vaulthttp"
)

// initializeHandlers creates all HTTP handlers and wires up dependencies.
func (b *ServerBuilder) initializeHandlers() error {
	b.locationHandler = locationhttp.NewHandler(b.locationManager)
	b.usageHandler = usagehttp.NewHandler(b.costTracker)
	b.mcpHandler = mcphttp.NewHandler(b.mcpRegistry, b.mcpConfigManager)
	b.settingsHandler = settingshttp.NewHandler(b.st, b.configManager, b.clientFactory, b.llmFactory)
	b.speechHandler = speechhttp.NewHandler(b.configManager)

	b.chatHandler = chathttp.NewHandler(b.st, b.clientFactory)
	b.chatHandler.SetLLMFactory(b.llmFactory)
	b.chatHandler.SetCostTracker(b.costTracker)
	b.chatHandler.SetMCPRegistry(b.mcpRegistry)
	b.chatHandler.SetMCPConfigManager(b.mcpConfigManager)
	b.chatHandler.SetWorkspaceStore(b.workspaceStore) // Will be set later

	applyUtilitySettings := func() {
		cfg := b.configManager.Get()
		b.chatHandler.SetUtilityToolRegistry(buildUtilityToolRegistry(cfg.Utility))
		b.chatHandler.SetBrowserMCPPreference(cfg.Utility.BrowserControlProvider)
		b.syncPlaywrightBrowserSettings(cfg.Utility)
	}
	applyUtilitySettings()
	b.settingsHandler.SetUtilitySettingsReloader(applyUtilitySettings)

	b.usageHandler.SetUtilityTelemetry(b.chatHandler.UtilityTelemetry())
	if featureflags.EvolutionEnabled() {
		b.evolutionService = evolution.NewService(b.st, b.onboardingMgr, nil)
		b.evolutionService.SetActivityLogger(b.activityLogger)
		b.chatHandler.SetEvolutionService(b.evolutionService)
		logger.Info("Evolution feature enabled", logger.Fields{})
	} else {
		b.evolutionService = nil
		b.chatHandler.SetEvolutionService(nil)
		logger.Info("Evolution feature disabled via ORI_EVOLUTION_ENABLED", logger.Fields{})
	}
	b.chatHandler.SetShutdownFunc(func() {
		logger.Info("Shutting down ori-agent server", logger.Fields{})
		b.server.Shutdown()
		logger.Info("Server shut down complete, exiting", logger.Fields{})
		os.Exit(0)
	})

	if b.evolutionService != nil {
		b.evolutionHandler = evolutionhttp.NewHandler(b.st, b.onboardingMgr, b.evolutionService)
	} else {
		b.evolutionHandler = nil
	}
	b.onboardingHandler = onboardinghttp.NewHandler(b.onboardingMgr)
	b.deviceHandler = devicehttp.NewHandler(b.onboardingMgr)
	b.resetHandler = settingshttp.NewResetHandler(b.onboardingMgr, b.st, ".")

	// Initialize auto-config handler for agent creation
	b.autoConfigHandler = agenthttp.NewAutoConfigHandler(b.llmFactory, b.configManager)

	// Initialize smart onboarding handler
	systemProvider, systemModel := b.configManager.GetSystemModel()
	b.smartOnboardingHandler = onboardinghttp.NewSmartOnboardingHandler(b.st, b.llmFactory, b.onboardingMgr, systemProvider, systemModel)

	// Initialize model category store and handler
	modelCategoryStore, err := store.NewFileModelCategoryStore("model_categories.json")
	if err != nil {
		logger.Error("Failed to create model category store", logger.Fields{"error": err})
		// Non-fatal: continue without model categories
	} else {
		b.modelCategoryStore = modelCategoryStore
		b.modelCategoryHandler = modelcategoryhttp.NewHandler(modelCategoryStore)
		b.autoCategorizeHandler = modelcategoryhttp.NewAutoCategorizeHandler(modelCategoryStore, b.llmFactory, b.configManager)
	}

	// Initialize session store and handler
	ctx := context.Background()
	sessionStore, err := session.NewHybridStore(ctx, session.DefaultHybridStoreConfig())
	if err != nil {
		logger.Error("Failed to create session store", logger.Fields{"error": err})
		// Non-fatal: continue without session management
	} else {
		b.sessionStore = sessionStore
		b.sessionHandler = sessionhttp.New(sessionStore)
		b.sessionHandler.SetWorkspaceRootResolver(func() string {
			return resolveWorkspaceRoot(b.configManager)
		})
		b.sessionHandler.SetAgentStore(b.st)
		// Initialize auto-classify handler for session classification
		b.autoClassifyHandler = sessionhttp.NewAutoClassifyHandler(sessionStore, b.st, b.llmFactory, b.configManager)
		// Initialize smart input handler for Workspace Hub classification
		b.smartInputHandler = sessionhttp.NewSmartInputHandler(sessionStore, b.llmFactory, b.configManager)
		// Initialize note generation handler
		b.noteHandler = notehttp.NewHandler(b.llmFactory, b.configManager, b.st)
		// Wire session store to chat handler for multi-tab support
		b.chatHandler.SetSessionStore(sessionStore)
		// Wire tool call store for conversation review
		b.chatHandler.SetToolCallStore(sessionStore.ToolCallStore())
	}

	// Initialize session files store and handler
	sessionFilesPath := filepath.Join(".", "session_files")
	sessionFilesStore, err := sessionfiles.NewStore(sessionFilesPath)
	if err != nil {
		logger.Error("Failed to create session files store", logger.Fields{"error": err})
		// Non-fatal: continue without session files management
	} else {
		b.sessionFilesStore = sessionFilesStore

		// Create file watcher
		watcher, err := filewatcher.NewWatcher(filewatcher.DefaultWatcherConfig())
		if err != nil {
			logger.Error("Failed to create file watcher", logger.Fields{"error": err})
		} else {
			b.sessionFilesWatcher = watcher
			watcher.Start()
		}

		// Create files HTTP handler
		b.sessionFilesHandler = fileshttp.NewHandler(sessionFilesStore, b.sessionFilesWatcher)
		logger.Info("Session files management initialized", logger.Fields{"path": sessionFilesPath})
	}

	// Initialize review system
	if b.sessionStore != nil {
		reviewStore := review.NewSQLiteStore(b.sessionStore.DB())
		reviewRunner := review.NewRunner(
			reviewStore,
			b.sessionStore,
			b.sessionStore.ToolCallStore(),
			review.DefaultDetectionConfig(),
		)
		// Wire up agent store for per-agent review settings
		if b.st != nil {
			reviewRunner.SetAgentStore(b.st)
		}
		b.reviewHandler = reviewhttp.NewHandler(reviewRunner, reviewStore)
		logger.Info("Review system initialized", logger.Fields{})

		vaultStore := vault.NewStore(b.sessionStore.DB(), vault.StoreOptions{
			SecretStore: b.configManager.SecretStore(),
		})
		b.vaultHandler = vaulthttp.NewHandler(vaultStore)
		logger.Info("Vault system initialized", logger.Fields{})
	}

	// Initialize external agents (Claude Code, Codex)
	claudeReader := externalagents.NewClaudeReader("")
	codexReader := externalagents.NewCodexReader("")
	b.externalAgentsCache = externalagents.NewCache(claudeReader, codexReader)
	if err := b.externalAgentsCache.Load(); err != nil {
		logger.Warn("Failed to load external agents cache", logger.Fields{"error": err})
		// Non-fatal: continue without external agents
	}
	b.externalAgentsHandler = externalagentshttp.New(b.externalAgentsCache, b.configManager)
	logger.Info("External agents support initialized", logger.Fields{})

	// Initialize skills manager and handler (local + external)
	personalSkillsDir := ""
	if homeDir, err := os.UserHomeDir(); err == nil {
		personalSkillsDir = filepath.Join(homeDir, ".agents", "skills")
	}
	b.skillsManager = skills.NewManager(skills.ManagerConfig{
		AgentStorePath:    b.agentStorePath,
		PersonalSkillsDir: personalSkillsDir,
		ExternalAgents:    b.externalAgentsCache,
		ConfigManager:     b.configManager,
	})
	b.skillsHandler = skillshttp.New(b.skillsManager, b.st, b.llmFactory, b.configManager)
	b.chatHandler.SetSkillsManager(b.skillsManager)

	return nil
}

func (b *ServerBuilder) syncPlaywrightBrowserSettings(utility config.UtilitySettings) {
	if b == nil || b.mcpConfigManager == nil || b.mcpRegistry == nil {
		return
	}

	current, err := b.mcpConfigManager.GetServer("playwright")
	if err != nil || current == nil {
		return // Playwright MCP is not configured.
	}

	desired := *current
	desired.Env = resolvePlaywrightEnv(desired.Env, utility)
	if stringMapEqual(current.Env, desired.Env) {
		return
	}

	if err := b.mcpConfigManager.UpdateServer(desired); err != nil {
		logger.Warn("failed to persist Playwright MCP browser settings", logger.Fields{"error": err})
		return
	}

	wasRunning := false
	status, statusErr := b.mcpRegistry.GetServerStatus("playwright")
	if statusErr == nil {
		wasRunning = status == mcp.StatusRunning || status == mcp.StatusStarting || status == mcp.StatusRestarting
	}

	if err := b.mcpRegistry.RemoveServer("playwright"); err != nil && !strings.Contains(strings.ToLower(err.Error()), "not found") {
		logger.Warn("failed to reload Playwright MCP server after settings update", logger.Fields{"error": err})
		return
	}

	if err := b.mcpRegistry.AddServer(desired); err != nil {
		logger.Warn("failed to re-register Playwright MCP server after settings update", logger.Fields{"error": err})
		return
	}

	if wasRunning {
		if err := b.mcpRegistry.StartServer("playwright"); err != nil {
			logger.Warn("failed to restart Playwright MCP server after settings update", logger.Fields{"error": err})
		}
	}
}

func resolvePlaywrightEnv(existing map[string]string, utility config.UtilitySettings) map[string]string {
	next := make(map[string]string, len(existing))
	for k, v := range existing {
		next[k] = v
	}

	browserChoice := normalizePlaywrightBrowserChoice(utility.PlaywrightBrowser)
	executablePath := strings.TrimSpace(utility.PlaywrightExecutable)
	if browserChoice == "auto" && executablePath == "" {
		// No explicit override requested; preserve existing server-level configuration.
		return next
	}

	if browserChoice == "brave" {
		if executablePath == "" {
			executablePath = detectDefaultBraveExecutablePath()
		}
		browserChoice = "chrome"
	}

	delete(next, "PLAYWRIGHT_MCP_BROWSER")
	delete(next, "PLAYWRIGHT_MCP_EXECUTABLE_PATH")

	if browserChoice != "auto" {
		next["PLAYWRIGHT_MCP_BROWSER"] = browserChoice
	}
	if executablePath != "" {
		next["PLAYWRIGHT_MCP_EXECUTABLE_PATH"] = executablePath
	}

	return next
}

func normalizePlaywrightBrowserChoice(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "chrome", "firefox", "webkit", "msedge", "brave":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return "auto"
	}
}

func detectDefaultBraveExecutablePath() string {
	candidates := []string{}

	switch runtime.GOOS {
	case "darwin":
		candidates = append(candidates, "/Applications/Brave Browser.app/Contents/MacOS/Brave Browser")
	case "linux":
		candidates = append(candidates,
			"/usr/bin/brave-browser",
			"/usr/bin/brave-browser-stable",
			"/snap/bin/brave",
		)
	case "windows":
		programFiles := strings.TrimSpace(os.Getenv("ProgramFiles"))
		programFilesX86 := strings.TrimSpace(os.Getenv("ProgramFiles(x86)"))
		if programFiles != "" {
			candidates = append(candidates, filepath.Join(programFiles, "BraveSoftware", "Brave-Browser", "Application", "brave.exe"))
		}
		if programFilesX86 != "" {
			candidates = append(candidates, filepath.Join(programFilesX86, "BraveSoftware", "Brave-Browser", "Application", "brave.exe"))
		}
	}

	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	return ""
}

func stringMapEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}
