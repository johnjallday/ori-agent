// Package server provides HTTP handler initialization methods for the ServerBuilder.
// This file contains the method for initializing all HTTP handlers.
package server

import (
	"log"
	"os"

	"github.com/johnjallday/ori-agent/internal/chathttp"
	"github.com/johnjallday/ori-agent/internal/devicehttp"
	"github.com/johnjallday/ori-agent/internal/healthhttp"
	"github.com/johnjallday/ori-agent/internal/locationhttp"
	"github.com/johnjallday/ori-agent/internal/mcphttp"
	"github.com/johnjallday/ori-agent/internal/onboardinghttp"
	pluginhttp "github.com/johnjallday/ori-agent/internal/pluginhttp"
	"github.com/johnjallday/ori-agent/internal/pluginmanager"
	"github.com/johnjallday/ori-agent/internal/pluginupdate"
	"github.com/johnjallday/ori-agent/internal/settingshttp"
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
		log.Println("🛑 Shutting down ori-agent server...")
		s.Shutdown()
		log.Println("✅ Server shut down complete. Exiting...")
		os.Exit(0)
	})

	s.pluginRegistryHandler = pluginhttp.NewRegistryHandler(s.st, s.registryManager, s.pluginDownloader, s.agentStorePath)
	s.pluginHandler = pluginhttp.New(s.st, pluginhttp.NativeLoader{})
	s.pluginHandler.HealthManager = s.healthManager
	s.pluginInitHandler = pluginhttp.NewInitHandler(s.st, s.registryManager, s.pluginHandler)
	s.healthHandler = healthhttp.NewHandler(s.healthManager, s.st)
	s.pluginUpdateHandler = pluginupdate.NewHandler(s.st, s.healthManager.GetChecker())
	s.pluginUpdateHandler.SetPluginRegistry(&s.pluginReg)
	s.onboardingHandler = onboardinghttp.NewHandler(s.onboardingMgr)
	s.deviceHandler = devicehttp.NewHandler(s.onboardingMgr)
	s.webPageHandler = pluginhttp.NewWebPageHandler(s.st)

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

	return nil
}
