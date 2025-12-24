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
// # Initialization Phase Groups
//
// The server initialization is organized into logical phase groups:
//
//	GROUP 1: CORE (Phases 1-4)
//	  - Configuration, Registry, Client Factory, LLM Factory
//	  - These are foundational and have no dependencies
//
//	GROUP 2: STORAGE (Phases 5-6)
//	  - Storage, Activity Logger
//	  - Depends on: Configuration
//
//	GROUP 3: SERVICES (Phases 7-16)
//	  - Health, Location, Plugins, Updates, Templates, Onboarding, Cost, MCP
//	  - Depends on: Core, Storage
//
//	GROUP 4: HANDLERS (Phase 17)
//	  - HTTP Handlers for all API endpoints
//	  - Depends on: Core, Storage, Services
//
//	GROUP 5: ORCHESTRATION (Phases 18-23)
//	  - Workspace, Events, Tasks, Orchestration, Studio, Templates
//	  - Depends on: Handlers, Services
//
//	GROUP 6: FINALIZATION (Phases 24-25)
//	  - Domain Facades, Plugin Update Service
//	  - Depends on: All previous phases
//
// # Usage
//
//	builder, err := NewServerBuilder()
//	if err != nil {
//	    return nil, err
//	}
//	server, err := builder.Build()
//
// # Testing
//
// Use With* methods to inject mock dependencies:
//
//	builder.WithStore(mockStore).WithLLMFactory(mockFactory)
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
// Returns an error if any phase fails. See ServerBuilder documentation for phase groups.
func (b *ServerBuilder) Build() (*Server, error) {
	verbose := os.Getenv("ORI_VERBOSE") == "true"

	// ═══════════════════════════════════════════════════════════════════════════
	// GROUP 1: CORE - Foundational components with no dependencies
	// ═══════════════════════════════════════════════════════════════════════════

	if err := b.initializeConfiguration(); err != nil { // Phase 1
		return nil, fmt.Errorf("configuration phase failed: %w", err)
	}
	if err := b.initializeRegistry(); err != nil { // Phase 2
		return nil, fmt.Errorf("registry phase failed: %w", err)
	}
	if err := b.initializeClientFactory(); err != nil { // Phase 3 (deprecated)
		return nil, fmt.Errorf("client factory phase failed: %w", err)
	}
	if err := b.initializeLLMFactory(); err != nil { // Phase 4
		return nil, fmt.Errorf("LLM factory phase failed: %w", err)
	}

	// ═══════════════════════════════════════════════════════════════════════════
	// GROUP 2: STORAGE - Persistence layer (depends on: Core)
	// ═══════════════════════════════════════════════════════════════════════════

	if err := b.initializeStorage(); err != nil { // Phase 5
		return nil, fmt.Errorf("storage phase failed: %w", err)
	}
	if err := b.initializeActivityLogger(); err != nil { // Phase 6
		return nil, fmt.Errorf("activity logger phase failed: %w", err)
	}

	// ═══════════════════════════════════════════════════════════════════════════
	// GROUP 3: SERVICES - Business logic layer (depends on: Core, Storage)
	// ═══════════════════════════════════════════════════════════════════════════

	if err := b.initializeHealthManager(); err != nil { // Phase 7
		return nil, fmt.Errorf("health manager phase failed: %w", err)
	}
	if err := b.initializeLocationManager(); err != nil { // Phase 8
		return nil, fmt.Errorf("location manager phase failed: %w", err)
	}
	if err := b.initializePluginInfrastructure(); err != nil { // Phase 9
		return nil, fmt.Errorf("plugin infrastructure phase failed: %w", err)
	}
	if err := b.initializeUpdateManager(); err != nil { // Phase 10
		return nil, fmt.Errorf("update manager phase failed: %w", err)
	}
	if err := b.loadPluginsAndHealthCheck(); err != nil { // Phase 11
		return nil, fmt.Errorf("plugin loading phase failed: %w", err)
	}
	if err := b.loadPluginRegistry(); err != nil { // Phase 12
		return nil, fmt.Errorf("plugin registry loading phase failed: %w", err)
	}
	if err := b.initializeTemplateRenderer(); err != nil { // Phase 13
		return nil, fmt.Errorf("template renderer phase failed: %w", err)
	}
	if err := b.initializeOnboardingManager(); err != nil { // Phase 14
		return nil, fmt.Errorf("onboarding manager phase failed: %w", err)
	}
	if err := b.initializeCostTracker(); err != nil { // Phase 15
		return nil, fmt.Errorf("cost tracker phase failed: %w", err)
	}
	if err := b.initializeMCP(); err != nil { // Phase 16
		return nil, fmt.Errorf("MCP phase failed: %w", err)
	}

	// ═══════════════════════════════════════════════════════════════════════════
	// GROUP 4: HANDLERS - HTTP API layer (depends on: Core, Storage, Services)
	// ═══════════════════════════════════════════════════════════════════════════

	if err := b.initializeHandlers(); err != nil { // Phase 17
		return nil, fmt.Errorf("handlers phase failed: %w", err)
	}

	// ═══════════════════════════════════════════════════════════════════════════
	// GROUP 5: ORCHESTRATION - Multi-agent coordination (depends on: Handlers)
	// ═══════════════════════════════════════════════════════════════════════════

	if err := b.initializeWorkspaceStore(); err != nil { // Phase 18
		return nil, fmt.Errorf("workspace store phase failed: %w", err)
	}
	if err := b.initializeEventSystem(); err != nil { // Phase 19
		return nil, fmt.Errorf("event system phase failed: %w", err)
	}
	if err := b.initializeTaskExecution(); err != nil { // Phase 20
		return nil, fmt.Errorf("task execution phase failed: %w", err)
	}
	if err := b.initializeOrchestration(); err != nil { // Phase 21
		return nil, fmt.Errorf("orchestration phase failed: %w", err)
	}
	if err := b.initializeStudioOrchestrator(); err != nil { // Phase 22
		return nil, fmt.Errorf("studio orchestrator phase failed: %w", err)
	}
	if err := b.initializeTemplateManager(); err != nil { // Phase 23
		return nil, fmt.Errorf("template manager phase failed: %w", err)
	}

	// ═══════════════════════════════════════════════════════════════════════════
	// GROUP 6: FINALIZATION - Wire everything together (depends on: All)
	// ═══════════════════════════════════════════════════════════════════════════

	if err := b.createDomainFacades(); err != nil { // Phase 24
		return nil, fmt.Errorf("facade creation phase failed: %w", err)
	}
	if err := b.startPluginUpdateService(); err != nil { // Phase 25
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
