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
	"github.com/johnjallday/ori-agent/internal/fileshttp"
	"github.com/johnjallday/ori-agent/internal/filewatcher"
	"github.com/johnjallday/ori-agent/internal/healthhttp"
	"github.com/johnjallday/ori-agent/internal/locationhttp"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/mcphttp"
	"github.com/johnjallday/ori-agent/internal/modelcategoryhttp"
	"github.com/johnjallday/ori-agent/internal/onboardinghttp"
	pluginhttp "github.com/johnjallday/ori-agent/internal/pluginhttp"
	"github.com/johnjallday/ori-agent/internal/pluginmanager"
	"github.com/johnjallday/ori-agent/internal/pluginupdate"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/sessionfiles"
	"github.com/johnjallday/ori-agent/internal/sessionhttp"
	"github.com/johnjallday/ori-agent/internal/settingshttp"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/usagehttp"
)

// initializeHandlers creates all HTTP handlers and wires up dependencies.
func (b *ServerBuilder) initializeHandlers() error {
	s := b.server

	s.locationHandler = locationhttp.NewHandler(s.locationManager)
	s.usageHandler = usagehttp.NewHandler(s.costTracker)
	s.mcpHandler = mcphttp.NewHandler(s.mcpRegistry, s.mcpConfigManager, s.st)
	s.settingsHandler = settingshttp.NewHandler(s.st, s.configManager, s.clientFactory, s.llmFactory)

	s.chatHandler = chathttp.NewHandler(s.st, s.clientFactory)
	s.chatHandler.SetLLMFactory(s.llmFactory)
	s.chatHandler.SetHealthManager(s.healthManager)
	s.chatHandler.SetCostTracker(s.costTracker)
	s.chatHandler.SetMCPRegistry(s.mcpRegistry)
	s.chatHandler.SetWorkspaceStore(s.workspaceStore) // Will be set later
	s.chatHandler.SetShutdownFunc(func() {
		logger.Info("Shutting down ori-agent server", logger.Fields{})
		s.Shutdown()
		logger.Info("Server shut down complete, exiting", logger.Fields{})
		os.Exit(0)
	})

	s.pluginRegistryHandler = pluginhttp.NewRegistryHandler(s.st, s.registryManager, s.pluginDownloader, s.agentStorePath)
	s.pluginHandler = pluginhttp.New(s.st, pluginhttp.NativeLoader{})
	s.pluginHandler.HealthManager = s.healthManager
	s.pluginInitHandler = pluginhttp.NewInitHandler(s.st, s.registryManager, s.pluginHandler)
	s.healthHandler = healthhttp.NewHandler(s.healthManager, s.st)
	s.pluginUpdateHandler = pluginupdate.NewHandler(s.st, s.healthManager.GetChecker())
	s.pluginUpdateHandler.SetPluginRegistry(&s.pluginReg)
	s.pluginUpdateHandler.SetRegistryManager(s.registryManager)
	s.onboardingHandler = onboardinghttp.NewHandler(s.onboardingMgr)
	s.deviceHandler = devicehttp.NewHandler(s.onboardingMgr)
	s.resetHandler = settingshttp.NewResetHandler(s.onboardingMgr, ".")
	s.webPageHandler = pluginhttp.NewWebPageHandler(s.st)

	// Initialize auto-config handler for agent creation
	s.autoConfigHandler = agenthttp.NewAutoConfigHandler(s.llmFactory, s.configManager)

	// Initialize plugin management components
	s.categoryManager = pluginmanager.NewCategoryManager()
	s.permissionManager = pluginmanager.NewPermissionManager("plugin_permissions.json")
	s.versionManager = pluginmanager.NewVersionManager("plugin_versions")
	s.notificationManager = pluginmanager.NewNotificationManager("plugin_notifications.json")
	s.backupManager = pluginmanager.NewBackupManager("plugin_backups")

	// Initialize plugin management handlers
	s.pluginsPageHandler = pluginhttp.NewPluginsPageHandler(
		s.st,
		s.registryManager,
		s.categoryManager,
		s.permissionManager,
		pluginhttp.NativeLoader{},
	)
	s.rollbackHandler = pluginhttp.NewRollbackHandler(
		s.st,
		s.versionManager,
		s.registryManager,
		pluginhttp.NativeLoader{},
	)
	s.permissionsHandler = pluginhttp.NewPermissionsHandler(
		s.permissionManager,
		s.registryManager,
	)
	s.backupHandler = pluginhttp.NewBackupHandler(s.backupManager)
	s.notificationsHandler = pluginhttp.NewNotificationsHandler(s.notificationManager)

	// Initialize model category store and handler
	modelCategoryStore, err := store.NewFileModelCategoryStore("model_categories.json")
	if err != nil {
		logger.Error("Failed to create model category store", logger.Fields{"error": err})
		// Non-fatal: continue without model categories
	} else {
		s.modelCategoryStore = modelCategoryStore
		s.modelCategoryHandler = modelcategoryhttp.NewHandler(modelCategoryStore)
	}

	// Initialize session store and handler
	ctx := context.Background()
	sessionStore, err := session.NewHybridStore(ctx, session.DefaultHybridStoreConfig())
	if err != nil {
		logger.Error("Failed to create session store", logger.Fields{"error": err})
		// Non-fatal: continue without session management
	} else {
		s.sessionStore = sessionStore
		s.sessionHandler = sessionhttp.New(sessionStore)
		// Wire session store to chat handler for multi-tab support
		s.chatHandler.SetSessionStore(sessionStore)
	}

	// Initialize session files store and handler
	sessionFilesPath := filepath.Join(".", "session_files")
	sessionFilesStore, err := sessionfiles.NewStore(sessionFilesPath)
	if err != nil {
		logger.Error("Failed to create session files store", logger.Fields{"error": err})
		// Non-fatal: continue without session files management
	} else {
		s.sessionFilesStore = sessionFilesStore

		// Create file watcher
		watcher, err := filewatcher.NewWatcher(filewatcher.DefaultWatcherConfig())
		if err != nil {
			logger.Error("Failed to create file watcher", logger.Fields{"error": err})
		} else {
			s.sessionFilesWatcher = watcher
			watcher.Start()
		}

		// Create files HTTP handler
		s.sessionFilesHandler = fileshttp.NewHandler(sessionFilesStore, s.sessionFilesWatcher)
		logger.Info("Session files management initialized", logger.Fields{"path": sessionFilesPath})
	}

	return nil
}
