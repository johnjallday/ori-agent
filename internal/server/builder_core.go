// Package server provides core initialization methods for the ServerBuilder.
// This file contains methods for configuration, registry, clients, storage, and other core components.
package server

import (
	"context"
	"os"
	"time"

	agenthttp "github.com/johnjallday/ori-agent/internal/agenthttp"
	"github.com/johnjallday/ori-agent/internal/client"
	"github.com/johnjallday/ori-agent/internal/healthhttp"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/marketplacehttp"
	"github.com/johnjallday/ori-agent/internal/onboarding"
	"github.com/johnjallday/ori-agent/internal/privateservices"
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
	return nil
}

// initializeRegistry creates and refreshes the plugin registry manager with marketplace support.
func (b *ServerBuilder) initializeRegistry() error {
	mgr, mpStore, err := createRegistryManagerWithMarketplace()
	if err != nil {
		return err
	}
	b.registryManager = mgr
	b.marketplaceStore = mpStore

	// Create marketplace HTTP handler
	if mpStore != nil {
		b.marketplaceHandler = marketplacehttp.NewHandler(mpStore, mgr)
	}

	return nil
}

// initializeClientFactory creates the OpenAI client factory (deprecated).
func (b *ServerBuilder) initializeClientFactory() error {
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
	return nil
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

// initializeStorage creates the agent store and sets the path.
func (b *ServerBuilder) initializeStorage() error {
	defaultConf := loadDefaultSettings()

	agentStorePath, err := resolveAgentStorePath()
	if err != nil {
		return err
	}
	b.agentStorePath = agentStorePath

	st, err := createFileStore(agentStorePath, defaultConf)
	if err != nil {
		return err
	}
	b.st = st

	return nil
}

// initializeActivityLogger creates the activity logger.
func (b *ServerBuilder) initializeActivityLogger() error {
	verbose := os.Getenv("ORI_VERBOSE") == "true"
	activityLogDir := resolveActivityLogDir()

	activityLogger, err := agenthttp.NewActivityLogger(activityLogDir)
	if err != nil {
		logger.Error("Failed to initialize activity logger", logger.Fields{"err": err})
		b.activityLogger = nil
		return nil // Continue without activity logging
	}

	if verbose {
		logger.Info("Activity logger initialized", logger.Fields{"activityLogDir": activityLogDir})
	}
	b.activityLogger = activityLogger
	return nil
}

// initializeHealthManager creates the health manager.
func (b *ServerBuilder) initializeHealthManager() error {
	b.healthManager = healthhttp.NewManager()
	return nil
}

// initializeLocationManager sets up location detection and management.
func (b *ServerBuilder) initializeLocationManager() error {
	locationZonesPath := resolveLocationZonesPath()

	zones, err := loadLocationZones(locationZonesPath)
	if err != nil {
		return err
	}

	mgr := createLocationManager(zones, locationZonesPath)

	// Start location detection loop
	ctx := context.Background()
	mgr.Start(ctx, 60*time.Second)

	b.locationManager = mgr
	return nil
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

// initializeOnboardingManager creates the onboarding manager.
func (b *ServerBuilder) initializeOnboardingManager() error {
	b.onboardingMgr = onboarding.NewManager("app_state.json")
	return nil
}

// initializeCostTracker creates the cost tracker for LLM usage monitoring.
func (b *ServerBuilder) initializeCostTracker() error {
	verbose := os.Getenv("ORI_VERBOSE") == "true"
	usageDataDir := resolveCostTrackerDir()
	b.costTracker = llm.NewCostTracker(usageDataDir)
	if verbose {
		logger.Debug("Cost tracker initialized", logger.Fields{"dir": usageDataDir})
	}
	return nil
}
