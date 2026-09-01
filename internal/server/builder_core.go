// Package server provides core initialization methods for the ServerBuilder.
// This file contains methods for configuration, registry, clients, storage, and other core components.
package server

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/agenthttp"
	"github.com/johnjallday/ori-agent/internal/client"
	"github.com/johnjallday/ori-agent/internal/gateway"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/onboarding"
	"github.com/johnjallday/ori-agent/internal/privateservices"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/updatemanager"
	"github.com/johnjallday/ori-agent/internal/version"
	web "github.com/johnjallday/ori-agent/internal/web"
)

// initializeConfiguration loads the configuration manager and default settings.
func (b *ServerBuilder) initializeConfiguration() error {
	configMgr, err := createConfigManager("settings.json")
	if err != nil {
		return err
	}
	b.configManager = configMgr
	b.privateServicesClient = privateservices.NewEnvClient()

	// Materialize the project templates library (starter templates are only
	// written when absent). Non-fatal: the app works without templates.
	if err := projecttemplates.EnsureLibrary(resolveTemplatesRoot(configMgr)); err != nil {
		logger.Warn("Failed to prepare project templates library", logger.Fields{"error": err})
	}
	return nil
}

// initializeClientFactory creates the OpenAI client factory (deprecated).
func (b *ServerBuilder) initializeClientFactory() {
	apiKey := b.configManager.GetAPIKey()
	if apiKey == "" {
		logger.Warn("OPENAI_API_KEY not set - OpenAI provider will be unavailable", logger.Fields{})
		logger.Debug("You can configure it later in the Settings page", logger.Fields{})
	} else {
		verbose := os.Getenv("ORI_VERBOSE") == "true"
		if verbose {
			// Log only the length, never partial key content for security
			logger.Info("OpenAI API key configured", logger.Fields{"key_length": len(apiKey)})
		}
	}
	b.clientFactory = client.NewFactory(apiKey)
}

// initializeLLMFactory creates the LLM factory and registers all providers.
func (b *ServerBuilder) initializeLLMFactory() error {
	factory := createLLMFactory()
	if err := registerLLMProviders(factory, b.configManager); err != nil {
		return err
	}
	b.llmFactory = factory
	return nil
}

// initializeGateway creates the gateway service.
func (b *ServerBuilder) initializeGateway() {
	b.gateway = gateway.NewService(logger.New("gateway"))
}

// initializeStorage creates the agent store and sets the path.
func (b *ServerBuilder) initializeStorage() error {
	defaultConf := loadDefaultSettings()

	agentStorePath := resolveAgentStorePath()
	b.agentStorePath = agentStorePath

	st, err := createFileStore(agentStorePath, defaultConf)
	if err != nil {
		return err
	}
	if b.configManager != nil {
		systemProvider, systemModel := b.configManager.GetSystemModel()
		if strings.TrimSpace(systemProvider) != "" && strings.TrimSpace(systemModel) != "" {
			if err := agenthttp.EnsureSystemAssistantAgentWithSystemModel(st, systemProvider, systemModel); err != nil {
				return err
			}
		}
	}
	b.st = st

	return nil
}

// initializeActivityLogger creates the activity logger.
func (b *ServerBuilder) initializeActivityLogger() {
	verbose := os.Getenv("ORI_VERBOSE") == "true"
	activityLogDir := resolveActivityLogDir()

	activityLogger, err := agenthttp.NewActivityLogger(activityLogDir)
	if err != nil {
		logger.Error("Failed to initialize activity logger", logger.Fields{"err": err})
		b.activityLogger = nil
		return // Continue without activity logging
	}

	if verbose {
		logger.Info("Activity logger initialized", logger.Fields{"activityLogDir": activityLogDir})
	}
	b.activityLogger = activityLogger
}

// initializeLocationManager sets up location detection and management.
func (b *ServerBuilder) initializeLocationManager() {
	locationZonesPath := resolveLocationZonesPath()

	zones := loadLocationZones(locationZonesPath)

	mgr := createLocationManager(zones, locationZonesPath)

	// Start location detection loop
	ctx := context.Background()
	mgr.Start(ctx, 60*time.Second)

	b.locationManager = mgr
}

// initializeTemplateRenderer creates and loads the template renderer.
func (b *ServerBuilder) initializeTemplateRenderer() error {
	renderer := web.NewTemplateRenderer()
	if err := renderer.LoadTemplates(); err != nil {
		return err
	}
	b.templateRenderer = renderer
	return nil
}

// initializeOnboardingManager creates the canonical personal-assistant
// onboarding manager used by every installation.
func (b *ServerBuilder) initializeOnboardingManager() {
	b.onboardingMgr = onboarding.NewManager("app_state.json")
}

// initializeCostTracker creates the cost tracker for LLM usage monitoring.
func (b *ServerBuilder) initializeCostTracker() {
	verbose := os.Getenv("ORI_VERBOSE") == "true"
	usageDataDir := resolveCostTrackerDir()
	b.costTracker = llm.NewCostTracker(usageDataDir)
	if verbose {
		logger.Debug("Cost tracker initialized", logger.Fields{"dir": usageDataDir})
	}
}

// initializeUpdateManager creates the update manager.
func (b *ServerBuilder) initializeUpdateManager() {
	currentVersion := version.GetVersion()
	b.updateMgr = updatemanager.NewManager(currentVersion, "johnjallday", "ori-agent")
}
