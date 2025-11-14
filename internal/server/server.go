package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
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
	"github.com/johnjallday/ori-agent/internal/filehttp"
	"github.com/johnjallday/ori-agent/internal/healthhttp"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/location"
	"github.com/johnjallday/ori-agent/internal/locationhttp"
	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/mcphttp"
	"github.com/johnjallday/ori-agent/internal/onboarding"
	"github.com/johnjallday/ori-agent/internal/onboardinghttp"
	"github.com/johnjallday/ori-agent/internal/orchestration"
	"github.com/johnjallday/ori-agent/internal/orchestration/templates"
	"github.com/johnjallday/ori-agent/internal/orchestrationhttp"
	"github.com/johnjallday/ori-agent/internal/platform"
	"github.com/johnjallday/ori-agent/internal/plugindownloader"
	pluginhttp "github.com/johnjallday/ori-agent/internal/pluginhttp"
	"github.com/johnjallday/ori-agent/internal/pluginloader"
	"github.com/johnjallday/ori-agent/internal/pluginupdate"
	"github.com/johnjallday/ori-agent/internal/registry"
	"github.com/johnjallday/ori-agent/internal/settingshttp"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/updatehttp"
	"github.com/johnjallday/ori-agent/internal/updatemanager"
	"github.com/johnjallday/ori-agent/internal/usagehttp"
	"github.com/johnjallday/ori-agent/internal/version"
	web "github.com/johnjallday/ori-agent/internal/web"
)

// Server holds all the dependencies and state for the HTTP server
type Server struct {
	clientFactory         *client.Factory
	llmFactory            *llm.Factory
	registryManager       *registry.Manager
	st                    store.Store
	pluginReg             types.PluginRegistry
	agentStorePath        string
	configManager         *config.Manager
	templateRenderer      *web.TemplateRenderer
	pluginDownloader      *plugindownloader.PluginDownloader
	updateMgr             *updatemanager.Manager
	healthManager         *healthhttp.Manager
	settingsHandler       *settingshttp.Handler
	chatHandler           *chathttp.Handler
	pluginHandler         *pluginhttp.Handler
	pluginRegistryHandler *pluginhttp.RegistryHandler
	pluginInitHandler     *pluginhttp.InitHandler
	healthHandler         *healthhttp.Handler
	pluginUpdateHandler   *pluginupdate.Handler
	onboardingMgr         *onboarding.Manager
	onboardingHandler     *onboardinghttp.Handler
	deviceHandler         *devicehttp.Handler
	webPageHandler        *pluginhttp.WebPageHandler
	workspaceStore        agentstudio.Store
	taskExecutor          *agentstudio.TaskExecutor
	stepExecutor          *agentstudio.StepExecutor
	taskScheduler         *agentstudio.TaskScheduler
	eventBus              *agentstudio.EventBus
	notificationService   *agentstudio.NotificationService
	orchestrationHandler  *orchestrationhttp.Handler
	studioOrchestrator    *agentstudio.Orchestrator
	studioHandler         *agentstudio.HTTPHandler
	costTracker           *llm.CostTracker
	usageHandler          *usagehttp.Handler
	mcpRegistry           *mcp.Registry
	mcpConfigManager      *mcp.ConfigManager
	mcpHandler            *mcphttp.Handler
	locationManager       *location.Manager
	locationHandler       *locationhttp.Handler
}

// New creates and initializes a new Server with all dependencies
func New() (*Server, error) {
	s := &Server{}

	defaultConf := types.Settings{
		Model:        "gpt-5-nano",
		Temperature:  1,
		SystemPrompt: "You are a helpful assistant with access to various tools. When a user request can be fulfilled by using an available tool, use the tool instead of providing general information. Be concise and direct in your responses.",
	}

	// Initialize configuration manager
	s.configManager = config.NewManager("settings.json")
	if err := s.configManager.Load(); err != nil {
		return nil, err
	}

	// Initialize registry manager
	s.registryManager = registry.NewManager()

	// Refresh plugin registry from GitHub on startup
	if err := s.registryManager.RefreshFromGitHub(); err != nil {
		log.Printf("⚠️  Failed to refresh plugin registry from GitHub: %v", err)
		log.Printf("   Will use cached or local registry")
	}

	// Get API key from configuration (checks settings then env var)
	apiKey := s.configManager.GetAPIKey()
	if apiKey == "" {
		log.Printf("⚠️  OPENAI_API_KEY not set - OpenAI provider will be unavailable")
		log.Printf("   You can configure it later in the Settings page")
	}

	// Initialize OpenAI client factory (deprecated - will be replaced by LLM factory)
	// Create with empty key if not available - will be updated when user configures it
	s.clientFactory = client.NewFactory(apiKey)

	// Initialize LLM factory with available providers
	s.llmFactory = llm.NewFactory()

	// Register OpenAI provider only if API key is available
	if apiKey != "" {
		openaiProvider := llm.NewOpenAIProvider(llm.ProviderConfig{
			APIKey: apiKey,
		})
		s.llmFactory.Register("openai", openaiProvider)
		log.Printf("✅ OpenAI provider registered")
	}

	// Register Claude provider if API key is available
	claudeAPIKey := s.configManager.GetAnthropicAPIKey()
	if claudeAPIKey != "" {
		claudeProvider := llm.NewClaudeProvider(llm.ProviderConfig{
			APIKey: claudeAPIKey,
		})
		s.llmFactory.Register("claude", claudeProvider)
		log.Printf("Claude provider registered")
	}

	// Register Ollama provider (always available, no API key required)
	ollamaBaseURL := os.Getenv("OLLAMA_BASE_URL")
	if ollamaBaseURL == "" {
		ollamaBaseURL = "http://localhost:11434"
	}
	ollamaProvider := llm.NewOllamaProvider(llm.ProviderConfig{
		BaseURL: ollamaBaseURL,
	})
	s.llmFactory.Register("ollama", ollamaProvider)
	log.Printf("Ollama provider registered (base URL: %s)", ollamaBaseURL)

	// init store (persists agents/plugins/settings; not messages)
	s.agentStorePath = "agents.json"
	if p := os.Getenv("AGENT_STORE_PATH"); p != "" {
		s.agentStorePath = p
	} else if abs, err := filepath.Abs(s.agentStorePath); err == nil {
		s.agentStorePath = abs
	}
	log.Printf("Using agent store: %s", s.agentStorePath)

	var err error
	s.st, err = store.NewFileStore(s.agentStorePath, defaultConf)
	if err != nil {
		return nil, err
	}

	// initialize health manager (must be before plugin loading)
	s.healthManager = healthhttp.NewManager()

	// initialize location manager (must be before plugin loading so plugins can access location context)
	locationZonesPath := "locations.json"
	if abs, err := filepath.Abs(locationZonesPath); err == nil {
		locationZonesPath = abs
	}
	log.Printf("Using location zones file: %s", locationZonesPath)

	// Load zones from file
	zones, err := location.LoadZones(locationZonesPath)
	if err != nil {
		log.Printf("Warning: failed to load location zones: %v", err)
		zones = []location.Zone{}
	}
	log.Printf("📍 Loaded %d location zones", len(zones))

	// Create detectors
	manualDetector := location.NewManualDetector()
	wifiDetector := location.NewWiFiDetector()
	detectors := []location.Detector{manualDetector, wifiDetector}

	// Initialize location manager
	s.locationManager = location.NewManager(detectors, zones)

	// Set zones file path for persistence
	s.locationManager.SetZonesFilePath(locationZonesPath)

	// Start location detection loop
	ctx := context.Background()
	s.locationManager.Start(ctx, 60*time.Second)
	log.Printf("📍 Location manager initialized and detection started")

	// init plugin downloader
	pluginCacheDir := "plugin_cache"
	if p := os.Getenv("PLUGIN_CACHE_DIR"); p != "" {
		pluginCacheDir = p
	} else if abs, err := filepath.Abs(pluginCacheDir); err == nil {
		pluginCacheDir = abs
	}
	log.Printf("Using plugin cache: %s", pluginCacheDir)
	s.pluginDownloader = plugindownloader.NewDownloader(pluginCacheDir)

	// refresh local plugin registry from uploaded_plugins directory
	// This rebuilds the registry from scratch, ensuring all metadata is current
	if err := s.registryManager.RefreshLocalRegistry(); err != nil {
		log.Printf("Warning: failed to refresh local plugin registry: %v", err)
	}

	// init update manager
	currentVersion := version.GetVersion()
	s.updateMgr = updatemanager.NewManager(currentVersion, "johnjallday", "ori-agent")

	// restore plugin Tool instances for any persisted plugins
	names, _ := s.st.ListAgents()
	var healthySummary, degradedSummary, unhealthySummary []string

	for _, agName := range names {
		ag, ok := s.st.GetAgent(agName)
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

			// Run health check on the loaded plugin
			healthResult := s.healthManager.CheckAndCachePlugin(key, tool)
			if !healthResult.Health.Compatible {
				if healthResult.Health.Status == "unhealthy" {
					log.Printf("❌ Plugin %s is UNHEALTHY", key)
					for _, err := range healthResult.Health.Errors {
						log.Printf("   Error: %s", err)
					}
					if healthResult.Health.Recommendation != "" {
						log.Printf("   💡 Recommendation: %s", healthResult.Health.Recommendation)
					}
					unhealthySummary = append(unhealthySummary, fmt.Sprintf("%s v%s", key, healthResult.Health.Version))
				} else {
					log.Printf("⚠️  Plugin %s is DEGRADED", key)
					for _, warn := range healthResult.Health.Warnings {
						log.Printf("   Warning: %s", warn)
					}
					degradedSummary = append(degradedSummary, fmt.Sprintf("%s v%s", key, healthResult.Health.Version))
				}
			} else {
				log.Printf("✅ Plugin %s v%s health check passed", key, healthResult.Health.Version)
				healthySummary = append(healthySummary, fmt.Sprintf("%s v%s", key, healthResult.Health.Version))
			}

			agentSpecificStorePath := filepath.Join("agents", agName, "config.json")
			if abs, err := filepath.Abs(agentSpecificStorePath); err == nil {
				agentSpecificStorePath = abs
			}
			// Get current location from location manager (will be initialized later)
			currentLocation := ""
			if s.locationManager != nil {
				currentLocation = s.locationManager.GetCurrentLocation()
			}
			pluginloader.SetAgentContext(tool, agName, agentSpecificStorePath, currentLocation)

			if err := pluginloader.ExtractPluginSettingsSchema(tool, agName); err != nil {
				log.Printf("Warning: failed to extract settings schema for plugin %s in agent %s: %v", lp.Path, agName, err)
			}

			lp.Tool = tool
			ag.Plugins[key] = lp
		}

		for _, pluginKey := range failedPlugins {
			delete(ag.Plugins, pluginKey)
		}

		if err := s.st.SetAgent(agName, ag); err != nil {
			log.Printf("failed to restore plugins for agent %s: %v", agName, err)
		}
	}

	// Print health summary
	if len(healthySummary) > 0 || len(degradedSummary) > 0 || len(unhealthySummary) > 0 {
		log.Println("")
		log.Println("╔════════════════════════════════════════════════════════════════════════════════╗")
		log.Println("║  🏥 Plugin Health Summary                                                      ║")
		log.Println("╠════════════════════════════════════════════════════════════════════════════════╣")

		if len(healthySummary) > 0 {
			log.Printf("║  ✅ %d Healthy: %-66s║", len(healthySummary), truncateString(strings.Join(healthySummary, ", "), 66))
			if len(healthySummary) > 1 {
				// Show additional lines if list is long
				healthyList := strings.Join(healthySummary, ", ")
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

		if len(degradedSummary) > 0 {
			log.Printf("║  ⚠️  %d Degraded: %-64s║", len(degradedSummary), truncateString(strings.Join(degradedSummary, ", "), 64))
			if len(degradedSummary) > 1 {
				degradedList := strings.Join(degradedSummary, ", ")
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

		if len(unhealthySummary) > 0 {
			log.Printf("║  ❌ %d Unhealthy: %-63s║", len(unhealthySummary), truncateString(strings.Join(unhealthySummary, ", "), 63))
			if len(unhealthySummary) > 1 {
				unhealthyList := strings.Join(unhealthySummary, ", ")
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

	// Health check all uploaded plugins (not just agent-loaded ones)
	log.Println("Running initial health checks for all uploaded plugins...")
	localReg, err := s.registryManager.LoadLocal()
	if err == nil {
		for _, pluginEntry := range localReg.Plugins {
			// Check if we already health checked this plugin (from agent loading)
			if _, exists := s.healthManager.GetPluginHealth(pluginEntry.Name); exists {
				continue // Skip if already checked
			}

			// Load and health check this plugin
			tool, err := pluginloader.LoadPluginRPC(pluginEntry.Path)
			if err != nil {
				log.Printf("Warning: could not load plugin %s for initial health check: %v", pluginEntry.Name, err)
				continue
			}

			// Run health check and cache the result
			healthResult := s.healthManager.CheckAndCachePlugin(pluginEntry.Name, tool)
			if healthResult.Health.Compatible {
				log.Printf("✅ Plugin %s v%s health check passed", pluginEntry.Name, healthResult.Health.Version)
			} else {
				log.Printf("⚠️  Plugin %s v%s health check issues: %v", pluginEntry.Name, healthResult.Health.Version, healthResult.Health.Warnings)
			}
		}
	}

	// load plugin registry
	if reg, _, err := s.registryManager.Load(); err == nil {
		s.pluginReg = reg

		// Set registry for health manager (for update checking)
		s.healthManager.SetRegistry(func() []healthhttp.PluginRegistryEntry {
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
	} else {
		log.Printf("failed to load plugin registry: %v", err)
	}

	// initialize template renderer
	s.templateRenderer = web.NewTemplateRenderer()
	if err := s.templateRenderer.LoadTemplates(); err != nil {
		return nil, err
	}

	// initialize onboarding manager
	s.onboardingMgr = onboarding.NewManager("app_state.json")

	// initialize cost tracker
	usageDataDir := filepath.Join(os.Getenv("HOME"), ".ori-agent", "usage_data")
	s.costTracker = llm.NewCostTracker(usageDataDir)
	log.Printf("💰 Cost tracker initialized: %s", usageDataDir)

	// initialize MCP system
	s.mcpRegistry = mcp.NewRegistry()
	s.mcpConfigManager = mcp.NewConfigManager(".")
	if err := s.mcpConfigManager.InitializeDefaultServers(); err != nil {
		log.Printf("Warning: failed to initialize default MCP servers: %v", err)
	}

	// Load global MCP configuration
	mcpGlobalConfig, err := s.mcpConfigManager.LoadGlobalConfig()
	if err != nil {
		log.Printf("Warning: failed to load MCP global config: %v", err)
	} else {
		// Add all configured servers to registry
		for _, serverConfig := range mcpGlobalConfig.Servers {
			if err := s.mcpRegistry.AddServer(serverConfig); err != nil {
				log.Printf("Warning: failed to add MCP server %s to registry: %v", serverConfig.Name, err)
			}
		}
		log.Printf("🔌 MCP system initialized with %d configured servers", len(mcpGlobalConfig.Servers))
	}

	// initialize HTTP handlers
	s.locationHandler = locationhttp.NewHandler(s.locationManager)
	s.usageHandler = usagehttp.NewHandler(s.costTracker)
	s.mcpHandler = mcphttp.NewHandler(s.mcpRegistry, s.mcpConfigManager, s.st)
	s.settingsHandler = settingshttp.NewHandler(s.st, s.configManager, s.clientFactory, s.llmFactory)
	s.chatHandler = chathttp.NewHandler(s.st, s.clientFactory)
	s.chatHandler.SetLLMFactory(s.llmFactory)         // Inject LLM factory
	s.chatHandler.SetHealthManager(s.healthManager)   // Inject health manager
	s.chatHandler.SetCostTracker(s.costTracker)       // Inject cost tracker
	s.chatHandler.SetMCPRegistry(s.mcpRegistry)       // Inject MCP registry
	s.chatHandler.SetWorkspaceStore(s.workspaceStore) // Inject workspace store for /workspace commands
	s.chatHandler.SetShutdownFunc(func() {
		// Gracefully shut down server and exit
		log.Println("🛑 Shutting down ori-agent server...")
		s.Shutdown()
		log.Println("✅ Server shut down complete. Exiting...")
		os.Exit(0)
	})
	s.pluginRegistryHandler = pluginhttp.NewRegistryHandler(s.st, s.registryManager, s.pluginDownloader, s.agentStorePath)

	// Create plugin main handler first so we can pass it to init handler
	s.pluginHandler = pluginhttp.New(s.st, pluginhttp.NativeLoader{})
	s.pluginHandler.HealthManager = s.healthManager // Inject health manager
	s.pluginInitHandler = pluginhttp.NewInitHandler(s.st, s.registryManager, s.pluginHandler)
	s.healthHandler = healthhttp.NewHandler(s.healthManager, s.st)
	s.pluginUpdateHandler = pluginupdate.NewHandler(s.st, s.healthManager.GetChecker())
	s.pluginUpdateHandler.SetPluginRegistry(&s.pluginReg)
	s.onboardingHandler = onboardinghttp.NewHandler(s.onboardingMgr)
	s.deviceHandler = devicehttp.NewHandler(s.onboardingMgr)
	s.webPageHandler = pluginhttp.NewWebPageHandler(s.st)

	// initialize workspace store
	workspaceDir := "workspaces"
	if p := os.Getenv("WORKSPACE_DIR"); p != "" {
		workspaceDir = p
	} else if abs, err := filepath.Abs(workspaceDir); err == nil {
		workspaceDir = abs
	}
	log.Printf("Using workspace directory: %s", workspaceDir)

	s.workspaceStore, err = agentstudio.NewFileStore(workspaceDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create workspace store: %w", err)
	}

	// initialize event bus for real-time updates
	s.eventBus = agentstudio.DefaultEventBus()
	log.Println("✅ Event bus initialized")

	// initialize notification service
	s.notificationService = agentstudio.NewNotificationService(s.eventBus, 500) // Keep last 500 notifications
	log.Println("✅ Notification service initialized")

	// initialize agent communicator
	communicator := agentcomm.NewCommunicator(s.workspaceStore)

	// initialize task handler and executor
	taskHandler := agentstudio.NewLLMTaskHandler(s.st, s.llmFactory)
	taskHandler.SetEventBus(s.eventBus) // Wire up event bus for execution events
	s.taskExecutor = agentstudio.NewTaskExecutor(s.workspaceStore, taskHandler, agentstudio.ExecutorConfig{
		PollInterval:  10 * time.Second,
		MaxConcurrent: 5,
	})
	s.taskExecutor.SetEventBus(s.eventBus) // Wire up event bus

	// initialize step executor for workflow execution
	s.stepExecutor = agentstudio.NewStepExecutor(s.workspaceStore, taskHandler, agentstudio.StepExecutorConfig{
		PollInterval: 5 * time.Second,
	})

	// initialize task scheduler for scheduled/recurring tasks
	s.taskScheduler = agentstudio.NewTaskScheduler(s.workspaceStore, agentstudio.SchedulerConfig{
		PollInterval: 1 * time.Minute, // Check every minute
	})
	s.taskScheduler.SetEventBus(s.eventBus) // Wire up event bus

	// initialize orchestrator
	orch := orchestration.NewOrchestrator(s.st, s.workspaceStore, communicator)

	// initialize orchestration handler (creates its own communicator internally)
	s.orchestrationHandler = orchestrationhttp.NewHandler(s.st, s.workspaceStore)

	// inject orchestrator into orchestration handler
	s.orchestrationHandler.SetOrchestrator(orch)
	s.orchestrationHandler.SetTaskHandler(taskHandler)

	// inject event bus and notification service
	s.orchestrationHandler.SetEventBus(s.eventBus)
	s.orchestrationHandler.SetNotificationService(s.notificationService)

	// initialize autonomous agent studio orchestrator
	// Create LLM adapter with default provider (openai or first available)
	llmAdapter := agentstudio.NewLLMFactoryAdapter(s.llmFactory, "openai")
	s.studioOrchestrator = agentstudio.NewOrchestrator(s.workspaceStore, llmAdapter, s.eventBus)
	log.Println("✅ Agent Studio orchestrator initialized")

	// initialize studio HTTP handler
	s.studioHandler = agentstudio.NewHTTPHandler(s.workspaceStore, s.studioOrchestrator)
	log.Println("✅ Agent Studio HTTP handler initialized")

	// initialize template manager
	templatesDir := "workflow_templates"
	if p := os.Getenv("WORKFLOW_TEMPLATES_DIR"); p != "" {
		templatesDir = p
	} else if abs, err := filepath.Abs(templatesDir); err == nil {
		templatesDir = abs
	}
	templateManager := templates.NewTemplateManager(templatesDir)
	if err := templateManager.LoadTemplates(); err != nil {
		log.Printf("⚠️  Warning: failed to load workflow templates: %v", err)
	} else {
		log.Printf("✅ Loaded %d workflow templates", len(templateManager.ListTemplates()))
	}

	// inject template manager into orchestration handler
	s.orchestrationHandler.SetTemplateManager(templateManager)

	// inject orchestrator into chat handler
	s.chatHandler.SetOrchestrator(orch)

	return s, nil
}

// Handler returns the configured HTTP handler with all routes
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.serveIndex)
	mux.HandleFunc("/settings", s.serveSettings)
	mux.HandleFunc("/marketplace", s.serveMarketplace)
	mux.HandleFunc("/workflows", s.serveWorkflows)
	mux.HandleFunc("/studios/", s.serveWorkspaceDashboard) // Dynamic route for /studios/:id
	mux.HandleFunc("/studios", s.serveWorkspaces)
	mux.HandleFunc("/usage", s.serveUsage)

	// Static file server for CSS, JS, icons, and other assets
	mux.HandleFunc("/styles.css", s.serveStaticFile)
	mux.HandleFunc("/css/", s.serveStaticFile)
	mux.HandleFunc("/js/", s.serveStaticFile)
	mux.HandleFunc("/icons/", s.serveStaticFile)
	mux.HandleFunc("/chat-area.html", s.serveStaticFile)
	mux.HandleFunc("/agents/", s.serveAgentFiles)

	// Handlers: agents moved to separate package
	agentHandler := agenthttp.New(s.st)
	mux.Handle("/api/agents", agentHandler)
	mux.Handle("/api/agents/", agentHandler) // Support /api/agents/{name}

	// Plugin endpoints
	mux.HandleFunc("/api/plugin-registry", s.pluginRegistryHandler.PluginRegistryHandler)
	mux.HandleFunc("/api/plugin-updates", s.pluginRegistryHandler.PluginUpdatesHandler)
	mux.HandleFunc("/api/plugins/download", s.pluginRegistryHandler.PluginDownloadHandler)
	mux.HandleFunc("/api/plugins/updates/check", s.pluginRegistryHandler.PluginUpdatesCheckHandler)
	mux.HandleFunc("/api/plugins/execute", s.pluginInitHandler.PluginExecuteHandler)
	mux.HandleFunc("/api/plugins/init-status", s.pluginInitHandler.PluginInitStatusHandler)

	// Plugin health check endpoints (must be before catch-all /api/plugins/ pattern)
	mux.HandleFunc("/api/plugins/health", s.healthHandler.HandleAllPluginsHealth)
	mux.HandleFunc("/api/plugins/check-updates", s.pluginUpdateHandler.HandleCheckUpdates)
	mux.HandleFunc("/api/plugins/backups", s.pluginUpdateHandler.HandleListBackups)
	mux.HandleFunc("/api/plugins/backups/clean", s.pluginUpdateHandler.HandleCleanBackups)

	// Plugin upload endpoint (must be before catch-all /api/plugins/ pattern)
	mux.HandleFunc("/api/plugins/upload", s.pluginHandler.ServeHTTP)
	mux.HandleFunc("/api/plugins/", func(w http.ResponseWriter, r *http.Request) {
		// Check if this is a web page request
		if strings.Contains(r.URL.Path, "/pages/") {
			s.webPageHandler.ServeHTTP(w, r)
			return
		}
		// Check if this is a pages list request
		if strings.HasSuffix(r.URL.Path, "/pages") {
			s.webPageHandler.ListPages(w, r)
			return
		}
		// Check if this is a health endpoint for a specific plugin
		if strings.HasSuffix(r.URL.Path, "/health") {
			s.healthHandler.HandlePluginHealth(w, r)
			return
		}
		// Check if this is an update endpoint
		if strings.HasSuffix(r.URL.Path, "/update") {
			s.pluginUpdateHandler.HandleUpdatePlugin(w, r)
			return
		}
		// Check if this is a rollback endpoint
		if strings.HasSuffix(r.URL.Path, "/rollback") {
			s.pluginUpdateHandler.HandleRollbackPlugin(w, r)
			return
		}
		// Otherwise, delegate to init handler
		s.pluginInitHandler.PluginInitHandler(w, r)
	})

	// Reuse the plugin handler instance
	mux.HandleFunc("/api/plugins/save-settings", s.pluginHandler.ServeHTTP)
	mux.HandleFunc("/api/plugins/tool-call", s.pluginHandler.DirectToolCallHandler)
	mux.Handle("/api/plugins", s.pluginHandler)

	// Settings and configuration endpoints
	mux.HandleFunc("/api/settings", s.settingsHandler.SettingsHandler)
	mux.HandleFunc("/api/api-key", s.settingsHandler.APIKeyHandler)
	mux.HandleFunc("/api/providers", s.settingsHandler.ProvidersHandler)

	// Chat endpoint
	mux.HandleFunc("/api/chat", s.chatHandler.ChatHandler)

	// Update management endpoints
	updateHandler := updatehttp.NewHandler(s.updateMgr)
	mux.HandleFunc("/api/updates/check", updateHandler.CheckUpdatesHandler)
	mux.HandleFunc("/api/updates/releases", updateHandler.ListReleasesHandler)
	mux.HandleFunc("/api/updates/download", updateHandler.DownloadUpdateHandler)
	mux.HandleFunc("/api/updates/version", updateHandler.GetVersionHandler)

	// File parsing endpoint
	fileHandler := filehttp.NewHandler()
	mux.HandleFunc("/api/files/parse", fileHandler.ParseFileHandler)

	// Onboarding endpoints
	mux.HandleFunc("/api/onboarding/status", s.onboardingHandler.GetStatus)
	mux.HandleFunc("/api/onboarding/step", s.onboardingHandler.CompleteStep)
	mux.HandleFunc("/api/onboarding/skip", s.onboardingHandler.Skip)
	mux.HandleFunc("/api/onboarding/complete", s.onboardingHandler.Complete)
	mux.HandleFunc("/api/onboarding/reset", s.onboardingHandler.Reset)

	// Theme endpoints
	mux.HandleFunc("/api/theme", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			s.onboardingHandler.GetTheme(w, r)
		} else if r.Method == http.MethodPost {
			s.onboardingHandler.SetTheme(w, r)
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// Device endpoints
	mux.HandleFunc("/api/device/info", s.deviceHandler.GetDeviceInfo)
	mux.HandleFunc("/api/device/type", s.deviceHandler.SetDeviceType)

	// Usage and cost tracking endpoints
	mux.HandleFunc("/api/usage/stats/all", s.usageHandler.GetAllTimeStats)
	mux.HandleFunc("/api/usage/stats/today", s.usageHandler.GetTodayStats)
	mux.HandleFunc("/api/usage/stats/month", s.usageHandler.GetThisMonthStats)
	mux.HandleFunc("/api/usage/stats/range", s.usageHandler.GetCustomRangeStats)
	mux.HandleFunc("/api/usage/summary", s.usageHandler.GetSummary)
	mux.HandleFunc("/api/usage/pricing", s.usageHandler.GetPricingModels)

	// Location management endpoints
	mux.HandleFunc("/api/location/current", s.locationHandler.GetCurrentLocation)
	mux.HandleFunc("/api/location/zones", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.locationHandler.GetZones(w, r)
		case http.MethodPost:
			s.locationHandler.CreateZone(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/location/zones/", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			s.locationHandler.UpdateZone(w, r)
		case http.MethodDelete:
			s.locationHandler.DeleteZone(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/location/override", s.locationHandler.SetManualLocation)

	// MCP (Model Context Protocol) endpoints
	mux.HandleFunc("/api/mcp/servers", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.mcpHandler.ListServersHandler(w, r)
		case http.MethodPost:
			s.mcpHandler.AddServerHandler(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/mcp/servers/", func(w http.ResponseWriter, r *http.Request) {
		// Check for specific actions in the path
		if strings.HasSuffix(r.URL.Path, "/enable") {
			s.mcpHandler.EnableServerHandler(w, r)
		} else if strings.HasSuffix(r.URL.Path, "/disable") {
			s.mcpHandler.DisableServerHandler(w, r)
		} else if strings.HasSuffix(r.URL.Path, "/tools") {
			s.mcpHandler.GetServerToolsHandler(w, r)
		} else if strings.HasSuffix(r.URL.Path, "/status") {
			s.mcpHandler.GetServerStatusHandler(w, r)
		} else if r.Method == http.MethodDelete {
			s.mcpHandler.RemoveServerHandler(w, r)
		} else {
			http.Error(w, "Not found", http.StatusNotFound)
		}
	})

	// Orchestration endpoints
	mux.HandleFunc("/api/orchestration/workspace", s.orchestrationHandler.WorkspaceHandler)
	mux.HandleFunc("/api/orchestration/workspace/agents", s.orchestrationHandler.WorkspaceAgentsHandler)
	mux.HandleFunc("/api/orchestration/workspace/layout", s.orchestrationHandler.SaveLayoutHandler)
	mux.HandleFunc("/api/orchestration/messages", s.orchestrationHandler.MessagesHandler)
	mux.HandleFunc("/api/orchestration/delegate", s.orchestrationHandler.DelegateHandler)
	mux.HandleFunc("/api/orchestration/tasks", s.orchestrationHandler.TasksHandler)
	mux.HandleFunc("/api/orchestration/tasks/execute", s.orchestrationHandler.ExecuteTaskHandler)
	mux.HandleFunc("/api/orchestration/task-results", s.orchestrationHandler.TaskResultsHandler)
	mux.HandleFunc("/api/orchestration/workflow/status", s.orchestrationHandler.WorkflowStatusHandler)
	mux.HandleFunc("/api/orchestration/workflow/stream", s.orchestrationHandler.WorkflowStatusStreamHandler)
	mux.HandleFunc("/api/orchestration/progress/stream", s.orchestrationHandler.ProgressStreamHandler)
	mux.HandleFunc("/api/agents/capabilities", s.orchestrationHandler.AgentCapabilitiesHandler)

	// Workflow template endpoints
	mux.HandleFunc("/api/orchestration/templates", s.orchestrationHandler.TemplatesHandler)
	mux.HandleFunc("/api/orchestration/templates/instantiate", s.orchestrationHandler.InstantiateTemplateHandler)

	// Notification endpoints
	mux.HandleFunc("/api/orchestration/notifications", s.orchestrationHandler.NotificationsHandler)
	mux.HandleFunc("/api/orchestration/notifications/stream", s.orchestrationHandler.NotificationStreamHandler)

	// Event history endpoint
	mux.HandleFunc("/api/orchestration/events", s.orchestrationHandler.EventHistoryHandler)

	// Scheduled task endpoints
	mux.HandleFunc("/api/orchestration/scheduled-tasks", s.orchestrationHandler.ScheduledTasksHandler)
	mux.HandleFunc("/api/orchestration/scheduled-tasks/", s.orchestrationHandler.ScheduledTaskHandler)

	// Agent Studio API endpoints
	mux.HandleFunc("/api/studios", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			s.studioHandler.CreateStudio(w, r)
		} else if r.Method == http.MethodGet {
			s.studioHandler.ListStudios(w, r)
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// Handle routes with studio ID
	mux.HandleFunc("/api/studios/", func(w http.ResponseWriter, r *http.Request) {
		// Parse the path to determine which handler to use
		if strings.HasSuffix(r.URL.Path, "/mission") {
			s.studioHandler.ExecuteMission(w, r)
		} else if strings.HasSuffix(r.URL.Path, "/events") {
			s.studioHandler.GetStudioEvents(w, r)
		} else if strings.Contains(r.URL.Path, "/tasks") {
			// Handle task operations
			if strings.HasSuffix(r.URL.Path, "/execute") && r.Method == http.MethodPost {
				s.studioHandler.ExecuteTaskManually(w, r)
			} else if r.Method == http.MethodPost {
				s.studioHandler.CreateTask(w, r)
			} else if r.Method == http.MethodDelete {
				s.studioHandler.DeleteTask(w, r)
			} else {
				http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			}
		} else if strings.Contains(r.URL.Path, "/agents") {
			// Handle agent add/remove operations
			if r.Method == http.MethodPost {
				s.studioHandler.AddAgent(w, r)
			} else if r.Method == http.MethodDelete {
				s.studioHandler.RemoveAgent(w, r)
			} else {
				http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			}
		} else {
			s.studioHandler.GetStudio(w, r)
		}
	})

	// CORS middleware
	return s.corsHandler(mux)
}

// Start starts background services (task executor, etc.)
func (s *Server) Start() {
	if s.taskExecutor != nil {
		s.taskExecutor.Start()
	}
	if s.stepExecutor != nil {
		s.stepExecutor.Start()
	}
	if s.taskScheduler != nil {
		s.taskScheduler.Start()
	}
}

// Shutdown gracefully shuts down background services
func (s *Server) Shutdown() {
	// Stop background services
	if s.taskExecutor != nil {
		s.taskExecutor.Stop()
	}
	if s.stepExecutor != nil {
		s.stepExecutor.Stop()
	}
	if s.taskScheduler != nil {
		s.taskScheduler.Stop()
	}
	if s.notificationService != nil {
		s.notificationService.Shutdown()
	}
	if s.eventBus != nil {
		s.eventBus.Shutdown()
	}

	// Clean up all loaded plugins
	s.cleanupPlugins()
}

// cleanupPlugins closes all RPC plugin connections for all agents
func (s *Server) cleanupPlugins() {
	log.Println("Cleaning up plugins...")

	agentNames, _ := s.st.ListAgents()
	cleanedCount := 0

	for _, agentName := range agentNames {
		ag, ok := s.st.GetAgent(agentName)
		if !ok {
			continue
		}

		// Clean up each loaded plugin
		for pluginName, loadedPlugin := range ag.Plugins {
			if loadedPlugin.Tool != nil {
				pluginloader.CloseRPCPlugin(loadedPlugin.Tool)
				cleanedCount++
				log.Printf("Closed plugin '%s' for agent '%s'", pluginName, agentName)
			}
		}
	}

	log.Printf("Plugin cleanup complete: closed %d plugin(s)", cleanedCount)
}

// HTTPServer returns a fully configured http.Server
func (s *Server) HTTPServer(addr string) *http.Server {
	// Start background services
	s.Start()

	return &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       90 * time.Second,
	}
}

func (s *Server) corsHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Get allowed origins from configuration
		allowedOrigins := s.configManager.GetAllowedOrigins()
		origin := r.Header.Get("Origin")

		// Check if origin is allowed
		allowed := false
		for _, allowedOrigin := range allowedOrigins {
			if origin == allowedOrigin {
				allowed = true
				break
			}
		}

		// Only set CORS headers if origin is allowed
		if allowed {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}

		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (s *Server) serveIndex(w http.ResponseWriter, r *http.Request) {
	data := web.GetDefaultData()
	data.CurrentPage = "index"

	// Get theme from app state
	data.Theme = s.onboardingMgr.GetTheme()

	if agents, current := s.st.ListAgents(); len(agents) > 0 {
		currentAgentName := current
		if currentAgentName == "" {
			currentAgentName = agents[0]
		}
		if agent, found := s.st.GetAgent(currentAgentName); found && agent != nil {
			data.CurrentAgent = currentAgentName
			if agent.Settings.Model != "" {
				data.Model = agent.Settings.Model
			}
		}
	}

	html, err := s.templateRenderer.RenderTemplate("index", data)
	if err != nil {
		log.Printf("Failed to render template: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(html))
}

func (s *Server) serveSettings(w http.ResponseWriter, r *http.Request) {
	data := web.GetDefaultData()
	data.CurrentPage = "settings"

	// Get theme from app state
	data.Theme = s.onboardingMgr.GetTheme()

	if agents, current := s.st.ListAgents(); len(agents) > 0 {
		currentAgentName := current
		if currentAgentName == "" {
			currentAgentName = agents[0]
		}
		if agent, found := s.st.GetAgent(currentAgentName); found && agent != nil {
			data.CurrentAgent = currentAgentName
			if agent.Settings.Model != "" {
				data.Model = agent.Settings.Model
			}
		}
	}

	// Add platform information for system info display
	currentPlatform := platform.DetectPlatform()
	currentPlatformDisplay := platform.GetPlatformDisplayName(currentPlatform)
	data.Extra = map[string]interface{}{
		"CurrentPlatform":        currentPlatform,
		"CurrentPlatformDisplay": currentPlatformDisplay,
	}

	html, err := s.templateRenderer.RenderTemplate("settings", data)
	if err != nil {
		log.Printf("Failed to render settings template: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(html))
}

func (s *Server) serveMarketplace(w http.ResponseWriter, r *http.Request) {
	data := web.GetDefaultData()
	data.CurrentPage = "marketplace"

	// Get theme from app state
	data.Theme = s.onboardingMgr.GetTheme()

	if agents, current := s.st.ListAgents(); len(agents) > 0 {
		currentAgentName := current
		if currentAgentName == "" {
			currentAgentName = agents[0]
		}
		data.CurrentAgent = currentAgentName
	}

	// Add platform information for compatibility checking
	currentPlatform := platform.DetectPlatform()
	currentPlatformDisplay := platform.GetPlatformDisplayName(currentPlatform)
	data.Extra = map[string]interface{}{
		"CurrentPlatform":        currentPlatform,
		"CurrentPlatformDisplay": currentPlatformDisplay,
	}

	html, err := s.templateRenderer.RenderTemplate("marketplace", data)
	if err != nil {
		log.Printf("Failed to render marketplace template: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(html))
}

func (s *Server) serveWorkflows(w http.ResponseWriter, r *http.Request) {
	data := web.GetDefaultData()
	data.Title = "Workflow Templates - Ori Agent"
	data.CurrentPage = "workflows"

	// Get theme from app state
	data.Theme = s.onboardingMgr.GetTheme()

	if agents, current := s.st.ListAgents(); len(agents) > 0 {
		currentAgentName := current
		if currentAgentName == "" {
			currentAgentName = agents[0]
		}
		if agent, found := s.st.GetAgent(currentAgentName); found && agent != nil {
			data.CurrentAgent = currentAgentName
			if agent.Settings.Model != "" {
				data.Model = agent.Settings.Model
			}
		}
	}

	html, err := s.templateRenderer.RenderTemplate("workflows", data)
	if err != nil {
		log.Printf("Failed to render workflows template: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(html))
}

func (s *Server) serveWorkspaces(w http.ResponseWriter, r *http.Request) {
	data := web.GetDefaultData()
	data.Title = "Workspaces - Ori Agent"
	data.CurrentPage = "workspaces"

	// Get theme from app state
	data.Theme = s.onboardingMgr.GetTheme()

	if agents, current := s.st.ListAgents(); len(agents) > 0 {
		currentAgentName := current
		if currentAgentName == "" {
			currentAgentName = agents[0]
		}
		if agent, found := s.st.GetAgent(currentAgentName); found && agent != nil {
			data.CurrentAgent = currentAgentName
			if agent.Settings.Model != "" {
				data.Model = agent.Settings.Model
			}
		}
	}

	html, err := s.templateRenderer.RenderTemplate("studios", data)
	if err != nil {
		log.Printf("Failed to render studios template: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(html))
}

func (s *Server) serveWorkspaceDashboard(w http.ResponseWriter, r *http.Request) {
	// Extract workspace ID from URL path
	// URL format: /studios/:workspaceId
	path := strings.TrimPrefix(r.URL.Path, "/studios/")
	if path == "" || path == r.URL.Path {
		// No ID provided, redirect to studios list
		http.Redirect(w, r, "/studios", http.StatusSeeOther)
		return
	}

	workspaceID := path

	data := web.GetDefaultData()
	data.Title = "Workspace Dashboard - Ori Agent"
	data.CurrentPage = "workspaces"

	// Add workspace ID to template data
	data.Extra = map[string]interface{}{
		"WorkspaceID": workspaceID,
	}

	// Get theme from app state
	data.Theme = s.onboardingMgr.GetTheme()

	if agents, current := s.st.ListAgents(); len(agents) > 0 {
		currentAgentName := current
		if currentAgentName == "" {
			currentAgentName = agents[0]
		}
		if agent, found := s.st.GetAgent(currentAgentName); found && agent != nil {
			data.CurrentAgent = currentAgentName
			if agent.Settings.Model != "" {
				data.Model = agent.Settings.Model
			}
		}
	}

	html, err := s.templateRenderer.RenderTemplate("workspace-dashboard", data)
	if err != nil {
		log.Printf("Failed to render workspace dashboard template: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(html))
}

func (s *Server) serveUsage(w http.ResponseWriter, r *http.Request) {
	data := web.GetDefaultData()
	data.Title = "Usage & Cost Tracking - Ori Agent"
	data.CurrentPage = "usage"

	// Get theme from app state
	data.Theme = s.onboardingMgr.GetTheme()

	if agents, current := s.st.ListAgents(); len(agents) > 0 {
		currentAgentName := current
		if currentAgentName == "" {
			currentAgentName = agents[0]
		}
		if agent, found := s.st.GetAgent(currentAgentName); found && agent != nil {
			data.CurrentAgent = currentAgentName
			if agent.Settings.Model != "" {
				data.Model = agent.Settings.Model
			}
		}
	}

	html, err := s.templateRenderer.RenderTemplate("usage", data)
	if err != nil {
		log.Printf("Failed to render usage template: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(html))
}

func (s *Server) serveStaticFile(w http.ResponseWriter, r *http.Request) {
	path := "static" + r.URL.Path

	content, err := web.Static.ReadFile(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	switch {
	case strings.HasSuffix(path, ".css"):
		w.Header().Set("Content-Type", "text/css")
	case strings.HasSuffix(path, ".js"):
		w.Header().Set("Content-Type", "application/javascript")
	case strings.HasSuffix(path, ".svg"):
		w.Header().Set("Content-Type", "image/svg+xml")
	case strings.HasSuffix(path, ".html"):
		w.Header().Set("Content-Type", "text/html")
	case strings.HasSuffix(path, ".json"):
		w.Header().Set("Content-Type", "application/json")
	default:
		w.Header().Set("Content-Type", "application/octet-stream")
	}

	w.Header().Set("Content-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")

	w.Write(content)
}

func (s *Server) serveAgentFiles(w http.ResponseWriter, r *http.Request) {
	// Serve files from the agents directory
	// URL format: /agents/<agent-name>/agent_settings.json
	path := strings.TrimPrefix(r.URL.Path, "/")

	// Security: prevent directory traversal
	if strings.Contains(path, "..") {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	content, err := os.ReadFile(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// Set content type based on file extension
	if strings.HasSuffix(path, ".json") {
		w.Header().Set("Content-Type", "application/json")
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}

	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")

	w.Write(content)
}

// truncateString truncates a string to a maximum length with ellipsis
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

// HTTPServerWrapper wraps http.Server to provide graceful shutdown capabilities
type HTTPServerWrapper struct {
	Server *http.Server
}

// Shutdown gracefully shuts down the HTTP server
func (w *HTTPServerWrapper) Shutdown(ctx context.Context) error {
	if w.Server == nil {
		return nil
	}
	return w.Server.Shutdown(ctx)
}
