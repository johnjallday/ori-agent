// Package server provides the HTTP server for the Ori Agent application.
// This file implements the ServerBuilder pattern for constructing server instances
// with proper dependency injection and testability.
package server

import (
	"fmt"
	"os"

	"github.com/johnjallday/ori-agent/internal/agentstudio"
	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/pluginupdateservice"
	"github.com/johnjallday/ori-agent/internal/registry"
	"github.com/johnjallday/ori-agent/internal/store"
)

// ServerBuilder builds a Server instance through a series of initialization phases.
// It provides a structured, testable way to construct a server with all its dependencies.
//
// Usage:
//
//	builder, err := NewServerBuilder()
//	if err != nil {
//	    return nil, err
//	}
//	server, err := builder.Build()
type ServerBuilder struct {
	server *Server
}

// NewServerBuilder creates a new ServerBuilder instance with an empty Server.
func NewServerBuilder() (*ServerBuilder, error) {
	return &ServerBuilder{
		server: &Server{},
	}, nil
}

// Build executes all initialization phases in order and returns the fully constructed Server.
// Returns an error if any phase fails.
func (b *ServerBuilder) Build() (*Server, error) {
	verbose := os.Getenv("ORI_VERBOSE") == "true"

	// Phase 1: Configuration
	if err := b.initializeConfiguration(); err != nil {
		return nil, fmt.Errorf("configuration phase failed: %w", err)
	}

	// Phase 2: Registry
	if err := b.initializeRegistry(); err != nil {
		return nil, fmt.Errorf("registry phase failed: %w", err)
	}

	// Phase 3: Client Factory (deprecated but still used)
	if err := b.initializeClientFactory(); err != nil {
		return nil, fmt.Errorf("client factory phase failed: %w", err)
	}

	// Phase 4: LLM Factory & Providers
	if err := b.initializeLLMFactory(); err != nil {
		return nil, fmt.Errorf("LLM factory phase failed: %w", err)
	}

	// Phase 5: Storage
	if err := b.initializeStorage(); err != nil {
		return nil, fmt.Errorf("storage phase failed: %w", err)
	}

	// Phase 6: Activity Logger
	if err := b.initializeActivityLogger(); err != nil {
		return nil, fmt.Errorf("activity logger phase failed: %w", err)
	}

	// Phase 7: Health Manager
	if err := b.initializeHealthManager(); err != nil {
		return nil, fmt.Errorf("health manager phase failed: %w", err)
	}

	// Phase 8: Location Manager
	if err := b.initializeLocationManager(); err != nil {
		return nil, fmt.Errorf("location manager phase failed: %w", err)
	}

	// Phase 9: Plugin Infrastructure
	if err := b.initializePluginInfrastructure(); err != nil {
		return nil, fmt.Errorf("plugin infrastructure phase failed: %w", err)
	}

	// Phase 10: Update Manager
	if err := b.initializeUpdateManager(); err != nil {
		return nil, fmt.Errorf("update manager phase failed: %w", err)
	}

	// Phase 11: Plugin Loading & Health Checks
	if err := b.loadPluginsAndHealthCheck(); err != nil {
		return nil, fmt.Errorf("plugin loading phase failed: %w", err)
	}

	// Phase 12: Plugin Registry
	if err := b.loadPluginRegistry(); err != nil {
		return nil, fmt.Errorf("plugin registry loading phase failed: %w", err)
	}

	// Phase 13: Template Renderer
	if err := b.initializeTemplateRenderer(); err != nil {
		return nil, fmt.Errorf("template renderer phase failed: %w", err)
	}

	// Phase 14: Onboarding Manager
	if err := b.initializeOnboardingManager(); err != nil {
		return nil, fmt.Errorf("onboarding manager phase failed: %w", err)
	}

	// Phase 15: Cost Tracker
	if err := b.initializeCostTracker(); err != nil {
		return nil, fmt.Errorf("cost tracker phase failed: %w", err)
	}

	// Phase 16: MCP System
	if err := b.initializeMCP(); err != nil {
		return nil, fmt.Errorf("MCP phase failed: %w", err)
	}

	// Phase 17: HTTP Handlers
	if err := b.initializeHandlers(); err != nil {
		return nil, fmt.Errorf("handlers phase failed: %w", err)
	}

	// Phase 18: Workspace Store
	if err := b.initializeWorkspaceStore(); err != nil {
		return nil, fmt.Errorf("workspace store phase failed: %w", err)
	}

	// Phase 19: Event Bus & Notifications
	if err := b.initializeEventSystem(); err != nil {
		return nil, fmt.Errorf("event system phase failed: %w", err)
	}

	// Phase 20: Task Execution
	if err := b.initializeTaskExecution(); err != nil {
		return nil, fmt.Errorf("task execution phase failed: %w", err)
	}

	// Phase 21: Orchestration
	if err := b.initializeOrchestration(); err != nil {
		return nil, fmt.Errorf("orchestration phase failed: %w", err)
	}

	// Phase 22: Studio Orchestrator
	if err := b.initializeStudioOrchestrator(); err != nil {
		return nil, fmt.Errorf("studio orchestrator phase failed: %w", err)
	}

	// Phase 23: Template Manager
	if err := b.initializeTemplateManager(); err != nil {
		return nil, fmt.Errorf("template manager phase failed: %w", err)
	}

	// Phase 24: Create Domain Facades (organize dependencies)
	if err := b.createDomainFacades(); err != nil {
		return nil, fmt.Errorf("facade creation phase failed: %w", err)
	}

	// Phase 25: Start Plugin Update Service
	if err := b.startPluginUpdateService(); err != nil {
		return nil, fmt.Errorf("plugin update service phase failed: %w", err)
	}

	// Log success
	if !verbose {
		logger.Info("Server initialized successfully", logger.Fields{})
	}

	return b.server, nil
}

// createDomainFacades organizes dependencies into domain-specific facades
func (b *ServerBuilder) createDomainFacades() error {
	// Core System Facade
	b.server.Core = NewCoreSystemFacade(
		b.server.clientFactory,
		b.server.llmFactory,
		b.server.configManager,
		b.server.costTracker,
	)

	// Plugin System Facade
	b.server.Plugin = NewPluginSystemFacade(
		b.server.registryManager,
		b.server.pluginReg,
		b.server.pluginDownloader,
		b.server.categoryManager,
		b.server.permissionManager,
		b.server.versionManager,
		b.server.notificationManager,
		b.server.backupManager,
	)

	// Storage System Facade
	b.server.Storage = NewStorageSystemFacade(
		b.server.st,
		b.server.agentStorePath,
		b.server.workspaceStore,
		b.server.onboardingMgr,
		b.server.locationManager,
	)

	// Workflow System Facade
	b.server.Workflow = NewWorkflowSystemFacade(
		b.server.taskExecutor,
		b.server.stepExecutor,
		b.server.taskScheduler,
		b.server.eventBus,
		b.server.notificationService,
		b.server.studioOrchestrator,
	)

	// Integration System Facade
	b.server.Integration = NewIntegrationSystemFacade(
		b.server.mcpRegistry,
		b.server.mcpConfigManager,
		b.server.updateMgr,
	)

	// UI System Facade
	b.server.UI = NewUISystemFacade(
		b.server.templateRenderer,
	)

	return nil
}

// startPluginUpdateService initializes and starts the plugin update service.
func (b *ServerBuilder) startPluginUpdateService() error {
	if b.server.st == nil {
		return fmt.Errorf("store not initialized for plugin update service")
	}

	b.server.pluginUpdateService = pluginupdateservice.NewService(b.server.st, &b.server.pluginReg)
	b.server.pluginUpdateService.Start()

	if b.server.pluginUpdateHandler != nil {
		b.server.pluginUpdateHandler.SetUpdateService(b.server.pluginUpdateService)
	}
	if b.server.pluginsPageHandler != nil {
		b.server.pluginsPageHandler.SetUpdateService(b.server.pluginUpdateService)
	}

	return nil
}

// WithLLMFactory injects a custom LLM factory (for testing).
func (b *ServerBuilder) WithLLMFactory(f *llm.Factory) *ServerBuilder {
	b.server.llmFactory = f
	return b
}

// WithConfigManager injects a custom config manager (for testing).
func (b *ServerBuilder) WithConfigManager(c *config.Manager) *ServerBuilder {
	b.server.configManager = c
	return b
}

// WithRegistryManager injects a custom registry manager (for testing).
func (b *ServerBuilder) WithRegistryManager(r *registry.Manager) *ServerBuilder {
	b.server.registryManager = r
	return b
}

// WithStore injects a custom store (for testing).
func (b *ServerBuilder) WithStore(s store.Store) *ServerBuilder {
	b.server.st = s
	return b
}

// WithWorkspaceStore injects a custom workspace store (for testing).
func (b *ServerBuilder) WithWorkspaceStore(ws agentstudio.Store) *ServerBuilder {
	b.server.workspaceStore = ws
	return b
}
