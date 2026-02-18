// Package server provides HTTP handler initialization methods for the ServerBuilder.
// This file contains the method for initializing all HTTP handlers.
package server

import (
	"context"
	"os"
	"path/filepath"

	agenthttp "github.com/johnjallday/ori-agent/internal/agenthttp"
	"github.com/johnjallday/ori-agent/internal/chathttp"
	"github.com/johnjallday/ori-agent/internal/devicehttp"
	"github.com/johnjallday/ori-agent/internal/evolution"
	"github.com/johnjallday/ori-agent/internal/evolutionhttp"
	"github.com/johnjallday/ori-agent/internal/externalagents"
	"github.com/johnjallday/ori-agent/internal/externalagentshttp"
	"github.com/johnjallday/ori-agent/internal/featureflags"
	"github.com/johnjallday/ori-agent/internal/fileshttp"
	"github.com/johnjallday/ori-agent/internal/filewatcher"
	"github.com/johnjallday/ori-agent/internal/healthhttp"
	"github.com/johnjallday/ori-agent/internal/locationhttp"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/mcphttp"
	"github.com/johnjallday/ori-agent/internal/modelcategoryhttp"
	"github.com/johnjallday/ori-agent/internal/notehttp"
	"github.com/johnjallday/ori-agent/internal/onboardinghttp"
	pluginhttp "github.com/johnjallday/ori-agent/internal/pluginhttp"
	"github.com/johnjallday/ori-agent/internal/pluginmanager"
	"github.com/johnjallday/ori-agent/internal/pluginupdate"
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
)

// initializeHandlers creates all HTTP handlers and wires up dependencies.
func (b *ServerBuilder) initializeHandlers() error {
	b.locationHandler = locationhttp.NewHandler(b.locationManager)
	b.usageHandler = usagehttp.NewHandler(b.costTracker)
	b.mcpHandler = mcphttp.NewHandler(b.mcpRegistry, b.mcpConfigManager, b.st)
	b.settingsHandler = settingshttp.NewHandler(b.st, b.configManager, b.clientFactory, b.llmFactory)
	b.speechHandler = speechhttp.NewHandler(b.configManager)

	b.chatHandler = chathttp.NewHandler(b.st, b.clientFactory)
	b.chatHandler.SetLLMFactory(b.llmFactory)
	b.chatHandler.SetHealthManager(b.healthManager)
	b.chatHandler.SetCostTracker(b.costTracker)
	b.chatHandler.SetMCPRegistry(b.mcpRegistry)
	b.chatHandler.SetMCPConfigManager(b.mcpConfigManager)
	b.chatHandler.SetWorkspaceStore(b.workspaceStore) // Will be set later
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

	b.pluginRegistryHandler = pluginhttp.NewRegistryHandler(b.st, b.registryManager, b.pluginDownloader, b.agentStorePath)
	b.pluginHandler = pluginhttp.New(b.st, pluginhttp.NativeLoader{})
	b.pluginHandler.HealthManager = b.healthManager
	b.pluginInitHandler = pluginhttp.NewInitHandler(b.st, b.registryManager, b.pluginHandler)
	b.healthHandler = healthhttp.NewHandler(b.healthManager, b.st)
	if b.evolutionService != nil {
		b.evolutionHandler = evolutionhttp.NewHandler(b.st, b.onboardingMgr, b.evolutionService)
	} else {
		b.evolutionHandler = nil
	}
	b.pluginUpdateHandler = pluginupdate.NewHandler(b.st, b.healthManager.GetChecker())
	b.pluginUpdateHandler.SetPluginRegistry(&b.pluginReg)
	b.pluginUpdateHandler.SetRegistryManager(b.registryManager)
	b.onboardingHandler = onboardinghttp.NewHandler(b.onboardingMgr)
	b.deviceHandler = devicehttp.NewHandler(b.onboardingMgr)
	b.resetHandler = settingshttp.NewResetHandler(b.onboardingMgr, b.st, ".")
	b.webPageHandler = pluginhttp.NewWebPageHandler(b.st, b.templateRenderer)
	b.webPageHandler.SetLoader(pluginhttp.NativeLoader{})

	// Initialize auto-config handler for agent creation
	b.autoConfigHandler = agenthttp.NewAutoConfigHandler(b.llmFactory, b.configManager)

	// Initialize smart onboarding handler
	systemProvider, systemModel := b.configManager.GetSystemModel()
	b.smartOnboardingHandler = onboardinghttp.NewSmartOnboardingHandler(b.st, b.llmFactory, b.onboardingMgr, systemProvider, systemModel)

	// Initialize plugin management components
	b.categoryManager = pluginmanager.NewCategoryManager()
	b.permissionManager = pluginmanager.NewPermissionManager("plugin_permissions.json")
	b.notificationManager = pluginmanager.NewNotificationManager("plugin_notifications.json")
	b.backupManager = pluginmanager.NewBackupManager("plugin_backups")

	// Initialize plugin management handlers
	b.pluginsPageHandler = pluginhttp.NewPluginsPageHandler(
		b.st,
		b.registryManager,
		b.categoryManager,
		b.permissionManager,
		pluginhttp.NativeLoader{},
	)
	b.permissionsHandler = pluginhttp.NewPermissionsHandler(
		b.permissionManager,
		b.registryManager,
	)
	b.backupHandler = pluginhttp.NewBackupHandler(b.backupManager)
	b.notificationsHandler = pluginhttp.NewNotificationsHandler(b.notificationManager)

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
	b.skillsHandler = skillshttp.New(b.skillsManager, b.st)
	b.chatHandler.SetSkillsManager(b.skillsManager)

	return nil
}
