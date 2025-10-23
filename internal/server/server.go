package server

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	agenthttp "github.com/johnjallday/dolphin-agent/internal/agenthttp"
	"github.com/johnjallday/dolphin-agent/internal/chathttp"
	"github.com/johnjallday/dolphin-agent/internal/client"
	"github.com/johnjallday/dolphin-agent/internal/config"
	"github.com/johnjallday/dolphin-agent/internal/devicehttp"
	"github.com/johnjallday/dolphin-agent/internal/filehttp"
	"github.com/johnjallday/dolphin-agent/internal/llm"
	"github.com/johnjallday/dolphin-agent/internal/onboarding"
	"github.com/johnjallday/dolphin-agent/internal/onboardinghttp"
	"github.com/johnjallday/dolphin-agent/internal/plugindownloader"
	pluginhttp "github.com/johnjallday/dolphin-agent/internal/pluginhttp"
	"github.com/johnjallday/dolphin-agent/internal/pluginloader"
	"github.com/johnjallday/dolphin-agent/internal/registry"
	"github.com/johnjallday/dolphin-agent/internal/settingshttp"
	"github.com/johnjallday/dolphin-agent/internal/store"
	"github.com/johnjallday/dolphin-agent/internal/types"
	"github.com/johnjallday/dolphin-agent/internal/updatehttp"
	"github.com/johnjallday/dolphin-agent/internal/updatemanager"
	"github.com/johnjallday/dolphin-agent/internal/version"
	web "github.com/johnjallday/dolphin-agent/internal/web"
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
	settingsHandler       *settingshttp.Handler
	chatHandler           *chathttp.Handler
	pluginRegistryHandler *pluginhttp.RegistryHandler
	pluginInitHandler     *pluginhttp.InitHandler
	onboardingMgr         *onboarding.Manager
	onboardingHandler     *onboardinghttp.Handler
	deviceHandler         *devicehttp.Handler
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

	// Get API key from configuration (checks settings then env var)
	apiKey := s.configManager.GetAPIKey()
	if apiKey == "" {
		return nil, log.New(os.Stderr, "", 0).Output(1, "OPENAI_API_KEY must be set either in settings.json or as environment variable").(error)
	}

	// Initialize OpenAI client factory (deprecated - will be replaced by LLM factory)
	s.clientFactory = client.NewFactory(apiKey)

	// Initialize LLM factory with available providers
	s.llmFactory = llm.NewFactory()

	// Register OpenAI provider
	openaiProvider := llm.NewOpenAIProvider(llm.ProviderConfig{
		APIKey: apiKey,
	})
	s.llmFactory.Register("openai", openaiProvider)

	// Register Claude provider if API key is available
	claudeAPIKey := s.configManager.GetAnthropicAPIKey()
	if claudeAPIKey != "" {
		claudeProvider := llm.NewClaudeProvider(llm.ProviderConfig{
			APIKey: claudeAPIKey,
		})
		s.llmFactory.Register("claude", claudeProvider)
		log.Printf("Claude provider registered")
	}

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

	// init plugin downloader
	pluginCacheDir := "plugin_cache"
	if p := os.Getenv("PLUGIN_CACHE_DIR"); p != "" {
		pluginCacheDir = p
	} else if abs, err := filepath.Abs(pluginCacheDir); err == nil {
		pluginCacheDir = abs
	}
	log.Printf("Using plugin cache: %s", pluginCacheDir)
	s.pluginDownloader = plugindownloader.NewDownloader(pluginCacheDir)

	// scan uploaded_plugins directory and auto-register any new plugins
	if err := s.registryManager.ScanUploadedPlugins(); err != nil {
		log.Printf("Warning: failed to scan uploaded_plugins directory: %v", err)
	}

	// init update manager
	currentVersion := version.GetVersion()
	s.updateMgr = updatemanager.NewManager(currentVersion, "johnjallday", "dolphin-agent")

	// restore plugin Tool instances for any persisted plugins
	names, _ := s.st.ListAgents()
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
				log.Printf("failed to load plugin %s for agent %s: %v", lp.Path, agName, err)
				log.Printf("removing failed plugin %s from agent %s config", key, agName)
				failedPlugins = append(failedPlugins, key)
				continue
			}

			agentSpecificStorePath := filepath.Join("agents", agName, "config.json")
			if abs, err := filepath.Abs(agentSpecificStorePath); err == nil {
				agentSpecificStorePath = abs
			}
			pluginloader.SetAgentContext(tool, agName, agentSpecificStorePath)

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

	// load plugin registry
	if reg, _, err := s.registryManager.Load(); err == nil {
		s.pluginReg = reg
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

	// initialize HTTP handlers
	s.settingsHandler = settingshttp.NewHandler(s.st, s.configManager, s.clientFactory)
	s.chatHandler = chathttp.NewHandler(s.st, s.clientFactory)
	s.chatHandler.SetLLMFactory(s.llmFactory) // Inject LLM factory
	s.pluginRegistryHandler = pluginhttp.NewRegistryHandler(s.st, s.registryManager, s.pluginDownloader, s.agentStorePath)
	s.pluginInitHandler = pluginhttp.NewInitHandler(s.st, s.registryManager)
	s.onboardingHandler = onboardinghttp.NewHandler(s.onboardingMgr)
	s.deviceHandler = devicehttp.NewHandler(s.onboardingMgr)

	return s, nil
}

// Handler returns the configured HTTP handler with all routes
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.serveIndex)
	mux.HandleFunc("/settings", s.serveSettings)

	// Static file server for CSS, JS, icons, and other assets
	mux.HandleFunc("/styles.css", s.serveStaticFile)
	mux.HandleFunc("/js/", s.serveStaticFile)
	mux.HandleFunc("/icons/", s.serveStaticFile)
	mux.HandleFunc("/chat-area.html", s.serveStaticFile)

	// Handlers: agents moved to separate package
	mux.Handle("/api/agents", agenthttp.New(s.st))

	// Plugin endpoints
	mux.HandleFunc("/api/plugin-registry", s.pluginRegistryHandler.PluginRegistryHandler)
	mux.HandleFunc("/api/plugin-updates", s.pluginRegistryHandler.PluginUpdatesHandler)
	mux.HandleFunc("/api/plugins/download", s.pluginRegistryHandler.PluginDownloadHandler)
	mux.HandleFunc("/api/plugins/updates/check", s.pluginRegistryHandler.PluginUpdatesCheckHandler)
	mux.HandleFunc("/api/plugins/execute", s.pluginInitHandler.PluginExecuteHandler)
	mux.HandleFunc("/api/plugins/init-status", s.pluginInitHandler.PluginInitStatusHandler)

	// Create a handler instance for save-settings
	pluginMainHandler := pluginhttp.New(s.st, pluginhttp.NativeLoader{})
	mux.HandleFunc("/api/plugins/save-settings", pluginMainHandler.ServeHTTP)
	mux.HandleFunc("/api/plugins/tool-call", pluginMainHandler.DirectToolCallHandler)
	mux.Handle("/api/plugins", pluginMainHandler)
	mux.HandleFunc("/api/plugins/", s.pluginInitHandler.PluginInitHandler)

	// Settings and configuration endpoints
	mux.HandleFunc("/api/settings", s.settingsHandler.SettingsHandler)
	mux.HandleFunc("/api/api-key", s.settingsHandler.APIKeyHandler)

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

	// Device endpoints
	mux.HandleFunc("/api/device/info", s.deviceHandler.GetDeviceInfo)
	mux.HandleFunc("/api/device/type", s.deviceHandler.SetDeviceType)

	// CORS middleware
	return s.corsHandler(mux)
}

// HTTPServer returns a fully configured http.Server
func (s *Server) HTTPServer(addr string) *http.Server {
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
		w.Header().Set("Access-Control-Allow-Origin", "*")
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

	html, err := s.templateRenderer.RenderTemplate("settings", data)
	if err != nil {
		log.Printf("Failed to render settings template: %v", err)
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
