package server

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/agentcomm"
	agenthttp "github.com/johnjallday/ori-agent/internal/agenthttp"
	"github.com/johnjallday/ori-agent/internal/agentstudio"
	"github.com/johnjallday/ori-agent/internal/chathttp"
	"github.com/johnjallday/ori-agent/internal/client"
	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/devicehttp"
	"github.com/johnjallday/ori-agent/internal/healthhttp"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/locationhttp"
	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/mcphttp"
	"github.com/johnjallday/ori-agent/internal/onboarding"
	"github.com/johnjallday/ori-agent/internal/onboardinghttp"
	"github.com/johnjallday/ori-agent/internal/orchestration"
	"github.com/johnjallday/ori-agent/internal/orchestration/templates"
	"github.com/johnjallday/ori-agent/internal/orchestrationhttp"
	pluginhttp "github.com/johnjallday/ori-agent/internal/pluginhttp"
	"github.com/johnjallday/ori-agent/internal/pluginloader"
	"github.com/johnjallday/ori-agent/internal/pluginupdate"
	"github.com/johnjallday/ori-agent/internal/registry"
	"github.com/johnjallday/ori-agent/internal/settingshttp"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/updatemanager"
	"github.com/johnjallday/ori-agent/internal/usagehttp"
	"github.com/johnjallday/ori-agent/internal/version"
	web "github.com/johnjallday/ori-agent/internal/web"
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

	// Log success
	if !verbose {
		log.Printf("✅ Server initialized successfully")
	}

	return b.server, nil
}

// initializeConfiguration loads the configuration manager and default settings.
func (b *ServerBuilder) initializeConfiguration() error {
	configMgr, err := createConfigManager("settings.json")
	if err != nil {
		return err
	}
	b.server.configManager = configMgr
	return nil
}

// initializeRegistry creates and refreshes the plugin registry manager.
func (b *ServerBuilder) initializeRegistry() error {
	mgr, err := createRegistryManager()
	if err != nil {
		return err
	}
	b.server.registryManager = mgr
	return nil
}

// initializeClientFactory creates the OpenAI client factory (deprecated).
func (b *ServerBuilder) initializeClientFactory() error {
	apiKey := b.server.configManager.GetAPIKey()
	if apiKey == "" {
		log.Printf("⚠️  OPENAI_API_KEY not set - OpenAI provider will be unavailable")
		log.Printf("   You can configure it later in the Settings page")
	} else {
		verbose := os.Getenv("ORI_VERBOSE") == "true"
		if verbose {
			log.Printf("✅ OpenAI API key configured (length: %d, starts with: %s)", len(apiKey), apiKey[:min(10, len(apiKey))])
		}
	}
	b.server.clientFactory = client.NewFactory(apiKey)
	return nil
}

// initializeLLMFactory creates the LLM factory and registers all providers.
func (b *ServerBuilder) initializeLLMFactory() error {
	factory := createLLMFactory()
	if err := registerLLMProviders(factory, b.server.configManager); err != nil {
		return err
	}
	b.server.llmFactory = factory
	return nil
}

// initializeStorage creates the agent store and sets the path.
func (b *ServerBuilder) initializeStorage() error {
	defaultConf := loadDefaultSettings()

	agentStorePath, err := resolveAgentStorePath()
	if err != nil {
		return err
	}
	b.server.agentStorePath = agentStorePath

	st, err := createFileStore(agentStorePath, defaultConf)
	if err != nil {
		return err
	}
	b.server.st = st

	return nil
}

// initializeActivityLogger creates the activity logger.
func (b *ServerBuilder) initializeActivityLogger() error {
	verbose := os.Getenv("ORI_VERBOSE") == "true"
	activityLogDir := resolveActivityLogDir()

	activityLogger, err := agenthttp.NewActivityLogger(activityLogDir)
	if err != nil {
		log.Printf("⚠️  Failed to initialize activity logger: %v", err)
		b.server.activityLogger = nil
		return nil // Continue without activity logging
	}

	if verbose {
		log.Printf("✅ Activity logger initialized: %s", activityLogDir)
	}
	b.server.activityLogger = activityLogger
	return nil
}

// initializeHealthManager creates the health manager.
func (b *ServerBuilder) initializeHealthManager() error {
	b.server.healthManager = healthhttp.NewManager()
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

	b.server.locationManager = mgr
	return nil
}

// initializePluginInfrastructure sets up plugin downloader and refreshes local registry.
func (b *ServerBuilder) initializePluginInfrastructure() error {
	pluginCacheDir := resolvePluginCacheDir()
	b.server.pluginDownloader = createPluginDownloader(pluginCacheDir)

	// Refresh local plugin registry
	if err := refreshLocalPluginRegistry(b.server.registryManager); err != nil {
		// Log but don't fail - this is non-critical
		return nil
	}

	return nil
}

// initializeUpdateManager creates the update manager.
func (b *ServerBuilder) initializeUpdateManager() error {
	currentVersion := version.GetVersion()
	b.server.updateMgr = updatemanager.NewManager(currentVersion, "johnjallday", "ori-agent")
	return nil
}

// loadPluginsAndHealthCheck restores plugins for all agents and runs health checks.
func (b *ServerBuilder) loadPluginsAndHealthCheck() error {
	verbose := os.Getenv("ORI_VERBOSE") == "true"
	names, _ := b.server.st.ListAgents()
	var healthySummary, degradedSummary, unhealthySummary []string

	for _, agName := range names {
		ag, ok := b.server.st.GetAgent(agName)
		if !ok {
			continue
		}

		var failedPlugins []string

		for key, lp := range ag.Plugins {
			if lp.Tool != nil {
				continue
			}

			tool, err := pluginloader.LoadPluginUnified(lp.Path)
			if err != nil {
				log.Printf("❌ Failed to load plugin %s for agent %s: %v", lp.Path, agName, err)
				log.Printf("   Removing failed plugin %s from agent %s config", key, agName)
				failedPlugins = append(failedPlugins, key)
				continue
			}

			// Run health check
			healthResult := b.server.healthManager.CheckAndCachePlugin(key, tool)
			if !healthResult.Health.Compatible {
				if healthResult.Health.Status == "unhealthy" {
					if verbose {
						log.Printf("❌ Plugin %s is UNHEALTHY", key)
						for _, err := range healthResult.Health.Errors {
							log.Printf("   Error: %s", err)
						}
						if healthResult.Health.Recommendation != "" {
							log.Printf("   💡 Recommendation: %s", healthResult.Health.Recommendation)
						}
					}
					unhealthySummary = append(unhealthySummary, fmt.Sprintf("%s v%s", key, healthResult.Health.Version))
				} else {
					if verbose {
						log.Printf("⚠️  Plugin %s is DEGRADED", key)
						for _, warn := range healthResult.Health.Warnings {
							log.Printf("   Warning: %s", warn)
						}
					}
					degradedSummary = append(degradedSummary, fmt.Sprintf("%s v%s", key, healthResult.Health.Version))
				}
			} else {
				if verbose {
					log.Printf("✅ Plugin %s v%s health check passed", key, healthResult.Health.Version)
				}
				healthySummary = append(healthySummary, fmt.Sprintf("%s v%s", key, healthResult.Health.Version))
			}

			agentSpecificStorePath := filepath.Join("agents", agName, "config.json")
			if abs, err := filepath.Abs(agentSpecificStorePath); err == nil {
				agentSpecificStorePath = abs
			}

			currentLocation := ""
			if b.server.locationManager != nil {
				currentLocation = b.server.locationManager.GetCurrentLocation()
			}
			pluginloader.SetAgentContext(tool, agName, agentSpecificStorePath, currentLocation)

			if err := pluginloader.ExtractPluginSettingsSchema(tool, agName); err != nil {
				if verbose {
					log.Printf("Warning: failed to extract settings schema for plugin %s in agent %s: %v", lp.Path, agName, err)
				}
			}

			lp.Tool = tool
			lp.Definition = tool.Definition()
			ag.Plugins[key] = lp
		}

		for _, pluginKey := range failedPlugins {
			delete(ag.Plugins, pluginKey)
		}

		if err := b.server.st.SetAgent(agName, ag); err != nil {
			log.Printf("failed to restore plugins for agent %s: %v", agName, err)
		}
	}

	// Print health summary
	if verbose && (len(healthySummary) > 0 || len(degradedSummary) > 0 || len(unhealthySummary) > 0) {
		b.printHealthSummary(healthySummary, degradedSummary, unhealthySummary)
	}

	// Health check all uploaded plugins
	if verbose {
		log.Println("Running initial health checks for all uploaded plugins...")
	}
	localReg, err := b.server.registryManager.LoadLocal()
	if err == nil {
		for _, pluginEntry := range localReg.Plugins {
			if _, exists := b.server.healthManager.GetPluginHealth(pluginEntry.Name); exists {
				continue
			}

			tool, err := pluginloader.LoadPluginRPC(pluginEntry.Path)
			if err != nil {
				if verbose {
					log.Printf("Warning: could not load plugin %s for initial health check: %v", pluginEntry.Name, err)
				}
				continue
			}

			healthResult := b.server.healthManager.CheckAndCachePlugin(pluginEntry.Name, tool)
			if verbose {
				if healthResult.Health.Compatible {
					log.Printf("✅ Plugin %s v%s health check passed", pluginEntry.Name, healthResult.Health.Version)
				} else {
					log.Printf("⚠️  Plugin %s v%s health check issues: %v", pluginEntry.Name, healthResult.Health.Version, healthResult.Health.Warnings)
				}
			}
		}
	}

	return nil
}

// printHealthSummary prints a formatted health summary table.
func (b *ServerBuilder) printHealthSummary(healthy, degraded, unhealthy []string) {
	log.Println("")
	log.Println("╔════════════════════════════════════════════════════════════════════════════════╗")
	log.Println("║  🏥 Plugin Health Summary                                                      ║")
	log.Println("╠════════════════════════════════════════════════════════════════════════════════╣")

	if len(healthy) > 0 {
		log.Printf("║  ✅ %d Healthy: %-66s║", len(healthy), truncateString(strings.Join(healthy, ", "), 66))
		if len(healthy) > 1 {
			healthyList := strings.Join(healthy, ", ")
			for i := 66; i < len(healthyList); i += 73 {
				end := i + 73
				if end > len(healthyList) {
					end = len(healthyList)
				}
				log.Printf("║     %-74s║", healthyList[i:end])
			}
		}
	} else {
		log.Println("║  ✅ 0 Healthy                                                                  ║")
	}

	if len(degraded) > 0 {
		log.Printf("║  ⚠️  %d Degraded: %-64s║", len(degraded), truncateString(strings.Join(degraded, ", "), 64))
		if len(degraded) > 1 {
			degradedList := strings.Join(degraded, ", ")
			for i := 64; i < len(degradedList); i += 73 {
				end := i + 73
				if end > len(degradedList) {
					end = len(degradedList)
				}
				log.Printf("║     %-74s║", degradedList[i:end])
			}
		}
	} else {
		log.Println("║  ⚠️  0 Degraded                                                                ║")
	}

	if len(unhealthy) > 0 {
		log.Printf("║  ❌ %d Unhealthy: %-63s║", len(unhealthy), truncateString(strings.Join(unhealthy, ", "), 63))
		if len(unhealthy) > 1 {
			unhealthyList := strings.Join(unhealthy, ", ")
			for i := 63; i < len(unhealthyList); i += 73 {
				end := i + 73
				if end > len(unhealthyList) {
					end = len(unhealthyList)
				}
				log.Printf("║     %-74s║", unhealthyList[i:end])
			}
		}
	} else {
		log.Println("║  ❌ 0 Unhealthy                                                                ║")
	}

	log.Println("╚════════════════════════════════════════════════════════════════════════════════╝")
	log.Println("")
}

// loadPluginRegistry loads the plugin registry and sets it for the health manager.
func (b *ServerBuilder) loadPluginRegistry() error {
	reg, _, err := b.server.registryManager.Load()
	if err != nil {
		log.Printf("failed to load plugin registry: %v", err)
		return nil // Non-critical, continue
	}

	b.server.pluginReg = reg

	// Set registry for health manager
	b.server.healthManager.SetRegistry(func() []healthhttp.PluginRegistryEntry {
		entries := make([]healthhttp.PluginRegistryEntry, len(reg.Plugins))
		for i, p := range reg.Plugins {
			entries[i] = healthhttp.PluginRegistryEntry{
				Name:    p.Name,
				Version: p.Version,
				URL:     p.URL,
			}
		}
		return entries
	})

	return nil
}

// initializeTemplateRenderer creates and loads the template renderer.
func (b *ServerBuilder) initializeTemplateRenderer() error {
	renderer := web.NewTemplateRenderer()
	if err := renderer.LoadTemplates(); err != nil {
		return err
	}
	b.server.templateRenderer = renderer
	return nil
}

// initializeOnboardingManager creates the onboarding manager.
func (b *ServerBuilder) initializeOnboardingManager() error {
	b.server.onboardingMgr = onboarding.NewManager("app_state.json")
	return nil
}

// initializeCostTracker creates the cost tracker for LLM usage monitoring.
func (b *ServerBuilder) initializeCostTracker() error {
	verbose := os.Getenv("ORI_VERBOSE") == "true"
	usageDataDir := resolveCostTrackerDir()
	b.server.costTracker = llm.NewCostTracker(usageDataDir)
	if verbose {
		log.Printf("💰 Cost tracker initialized: %s", usageDataDir)
	}
	return nil
}

// initializeMCP initializes the MCP system (registry, config manager, servers).
func (b *ServerBuilder) initializeMCP() error {
	verbose := os.Getenv("ORI_VERBOSE") == "true"

	b.server.mcpRegistry = mcp.NewRegistry()
	b.server.mcpConfigManager = mcp.NewConfigManager(".")

	if err := b.server.mcpConfigManager.InitializeDefaultServers(); err != nil {
		if verbose {
			log.Printf("Warning: failed to initialize default MCP servers: %v", err)
		}
	}

	mcpGlobalConfig, err := b.server.mcpConfigManager.LoadGlobalConfig()
	if err != nil {
		if verbose {
			log.Printf("Warning: failed to load MCP global config: %v", err)
		}
		return nil // Non-critical
	}

	for _, serverConfig := range mcpGlobalConfig.Servers {
		if err := b.server.mcpRegistry.AddServer(serverConfig); err != nil {
			if verbose {
				log.Printf("Warning: failed to add MCP server %s to registry: %v", serverConfig.Name, err)
			}
		}
	}

	if verbose {
		log.Printf("🔌 MCP system initialized with %d configured servers", len(mcpGlobalConfig.Servers))
	}

	return nil
}

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

	return nil
}

// initializeWorkspaceStore creates the workspace storage system.
func (b *ServerBuilder) initializeWorkspaceStore() error {
	workspaceDir := resolveWorkspaceDir()
	ws, err := createWorkspaceStore(workspaceDir)
	if err != nil {
		return err
	}
	b.server.workspaceStore = ws

	// Now update chat handler with workspace store
	b.server.chatHandler.SetWorkspaceStore(ws)

	return nil
}

// initializeEventSystem creates the event bus and notification service.
func (b *ServerBuilder) initializeEventSystem() error {
	verbose := os.Getenv("ORI_VERBOSE") == "true"

	b.server.eventBus = agentstudio.DefaultEventBus()
	if verbose {
		log.Println("✅ Event bus initialized")
	}

	b.server.notificationService = agentstudio.NewNotificationService(b.server.eventBus, 500)
	if verbose {
		log.Println("✅ Notification service initialized")
	}

	return nil
}

// initializeTaskExecution creates task handler, executor, step executor, and scheduler.
func (b *ServerBuilder) initializeTaskExecution() error {
	s := b.server

	taskHandler := agentstudio.NewLLMTaskHandler(s.st, s.llmFactory)
	taskHandler.SetEventBus(s.eventBus)

	s.taskExecutor = agentstudio.NewTaskExecutor(s.workspaceStore, taskHandler, agentstudio.ExecutorConfig{
		PollInterval:  10 * time.Second,
		MaxConcurrent: 5,
	})
	s.taskExecutor.SetEventBus(s.eventBus)

	s.stepExecutor = agentstudio.NewStepExecutor(s.workspaceStore, taskHandler, agentstudio.StepExecutorConfig{
		PollInterval: 5 * time.Second,
	})

	s.taskScheduler = agentstudio.NewTaskScheduler(s.workspaceStore, agentstudio.SchedulerConfig{
		PollInterval: 1 * time.Minute,
	})
	s.taskScheduler.SetEventBus(s.eventBus)

	return nil
}

// initializeOrchestration creates orchestrators and handlers.
func (b *ServerBuilder) initializeOrchestration() error {
	s := b.server

	communicator := agentcomm.NewCommunicator(s.workspaceStore)
	orch := orchestration.NewOrchestrator(s.st, s.workspaceStore, communicator)

	s.orchestrationHandler = orchestrationhttp.NewHandler(s.st, s.workspaceStore)
	s.orchestrationHandler.SetOrchestrator(orch)

	taskHandler := agentstudio.NewLLMTaskHandler(s.st, s.llmFactory)
	s.orchestrationHandler.SetTaskHandler(taskHandler)
	s.orchestrationHandler.SetEventBus(s.eventBus)
	s.orchestrationHandler.SetNotificationService(s.notificationService)

	// Store orchestrator for chat handler injection
	b.server.chatHandler.SetOrchestrator(orch)

	return nil
}

// initializeStudioOrchestrator creates the autonomous agent studio orchestrator.
func (b *ServerBuilder) initializeStudioOrchestrator() error {
	verbose := os.Getenv("ORI_VERBOSE") == "true"

	llmAdapter := agentstudio.NewLLMFactoryAdapter(b.server.llmFactory, "openai")
	b.server.studioOrchestrator = agentstudio.NewOrchestrator(b.server.workspaceStore, llmAdapter, b.server.eventBus)
	if verbose {
		log.Println("✅ Agent Studio orchestrator initialized")
	}

	b.server.studioHandler = agentstudio.NewHTTPHandler(b.server.workspaceStore, b.server.studioOrchestrator)
	if verbose {
		log.Println("✅ Agent Studio HTTP handler initialized")
	}

	return nil
}

// initializeTemplateManager loads workflow templates and injects into orchestration handler.
func (b *ServerBuilder) initializeTemplateManager() error {
	verbose := os.Getenv("ORI_VERBOSE") == "true"
	templatesDir := resolveWorkflowTemplatesDir()

	templateManager := templates.NewTemplateManager(templatesDir)
	if err := templateManager.LoadTemplates(); err != nil {
		if verbose {
			log.Printf("⚠️  Warning: failed to load workflow templates: %v", err)
		}
		return nil // Non-critical
	}

	if verbose {
		log.Printf("✅ Loaded %d workflow templates", len(templateManager.ListTemplates()))
	}

	b.server.orchestrationHandler.SetTemplateManager(templateManager)

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
