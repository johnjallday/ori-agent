package main

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

var (
	clientFactory   *client.Factory
	registryManager *registry.Manager

	// runtime state (moved behind Store)
	st             store.Store
	pluginReg      types.PluginRegistry
	defaultConf    = types.Settings{Model: "gpt-5-nano", Temperature: 1}
	agentStorePath string
	configManager  *config.Manager

	// template renderer
	templateRenderer *web.TemplateRenderer

	// plugin downloader for external plugins
	pluginDownloader *plugindownloader.PluginDownloader

	// update manager for software updates
	updateMgr *updatemanager.Manager

	// HTTP handlers
	settingsHandler       *settingshttp.Handler
	chatHandler           *chathttp.Handler
	pluginRegistryHandler *pluginhttp.RegistryHandler
	pluginInitHandler     *pluginhttp.InitHandler
)

func main() {
	// Initialize configuration manager
	configManager = config.NewManager("settings.json")
	if err := configManager.Load(); err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Initialize registry manager
	registryManager = registry.NewManager()

	// Get API key from configuration (checks settings then env var)
	apiKey := configManager.GetAPIKey()
	if apiKey == "" {
		log.Fatal("OPENAI_API_KEY must be set either in settings.json or as environment variable")
	}

	// Initialize OpenAI client factory
	clientFactory = client.NewFactory(apiKey)

	// init store (persists agents/plugins/settings; not messages)
	// Use the index file path for the store, not the individual agent config
	agentStorePath = "agents.json"
	if p := os.Getenv("AGENT_STORE_PATH"); p != "" {
		agentStorePath = p
	} else if abs, err2 := filepath.Abs(agentStorePath); err2 == nil {
		agentStorePath = abs
	}
	log.Printf("Using agent store: %s", agentStorePath)
	var err error
	st, err = store.NewFileStore(agentStorePath, defaultConf)
	if err != nil {
		log.Fatalf("store init: %v", err)
	}

	// init plugin downloader
	pluginCacheDir := "plugin_cache"
	if p := os.Getenv("PLUGIN_CACHE_DIR"); p != "" {
		pluginCacheDir = p
	} else if abs, err2 := filepath.Abs(pluginCacheDir); err2 == nil {
		pluginCacheDir = abs
	}
	log.Printf("Using plugin cache: %s", pluginCacheDir)
	pluginDownloader = plugindownloader.NewDownloader(pluginCacheDir)

	// scan uploaded_plugins directory and auto-register any new plugins
	if err := registryManager.ScanUploadedPlugins(); err != nil {
		log.Printf("Warning: failed to scan uploaded_plugins directory: %v", err)
	}

	// init update manager
	currentVersion := version.GetVersion()
	updateMgr = updatemanager.NewManager(currentVersion, "johnjallday", "dolphin-agent")
	// restore plugin Tool instances for any persisted plugins
	// so that Chat handlers can invoke them after a restart
	names, _ := st.ListAgents()
	for _, agName := range names {
		ag, ok := st.GetAgent(agName)
		if !ok {
			continue
		}

		// Track failed plugins to remove from config
		var failedPlugins []string

		for key, lp := range ag.Plugins {
			// If tool is already set, just add it to cache
			if lp.Tool != nil {
				absPath, err := filepath.Abs(lp.Path)
				if err == nil {
					pluginloader.AddToCache(absPath, lp.Tool)
				}
				continue
			}

			tool, err := pluginloader.LoadWithCache(lp.Path)
			if err != nil {
				log.Printf("failed to load plugin %s for agent %s: %v", lp.Path, agName, err)
				log.Printf("removing failed plugin %s from agent %s config", key, agName)
				failedPlugins = append(failedPlugins, key)
				continue
			}

			// Set agent context for plugins that support it
			// Construct the correct agent store path for this specific agent
			agentSpecificStorePath := filepath.Join("agents", agName, "config.json")
			if abs, err := filepath.Abs(agentSpecificStorePath); err == nil {
				agentSpecificStorePath = abs
			}
			pluginloader.SetAgentContext(tool, agName, agentSpecificStorePath)

			// Extract plugin settings schema for this agent if plugin supports get_settings
			if err := pluginloader.ExtractPluginSettingsSchema(tool, agName); err != nil {
				log.Printf("Warning: failed to extract settings schema for plugin %s in agent %s: %v", lp.Path, agName, err)
			}

			lp.Tool = tool
			ag.Plugins[key] = lp
		}

		// Remove failed plugins from agent config
		for _, pluginKey := range failedPlugins {
			delete(ag.Plugins, pluginKey)
		}

		if err := st.SetAgent(agName, ag); err != nil {
			log.Printf("failed to restore plugins for agent %s: %v", agName, err)
		}
	}

	// load plugin registry (now from GitHub with fallbacks)
	if reg, _, err := registryManager.Load(); err == nil {
		pluginReg = reg
	} else {
		log.Printf("failed to load plugin registry: %v", err)
	}

	// initialize template renderer
	templateRenderer = web.NewTemplateRenderer()
	if err := templateRenderer.LoadTemplates(); err != nil {
		log.Fatalf("failed to load templates: %v", err)
	}

	// initialize HTTP handlers
	settingsHandler = settingshttp.NewHandler(st, configManager, clientFactory)
	chatHandler = chathttp.NewHandler(st, clientFactory)
	pluginRegistryHandler = pluginhttp.NewRegistryHandler(st, registryManager, pluginDownloader, agentStorePath)
	pluginInitHandler = pluginhttp.NewInitHandler(st, registryManager)

	mux := http.NewServeMux()
	mux.HandleFunc("/", serveIndex)

	// Static file server for CSS, JS, icons, and other assets
	mux.HandleFunc("/styles.css", serveStaticFile)
	mux.HandleFunc("/js/", serveStaticFile)
	mux.HandleFunc("/icons/", serveStaticFile)
	mux.HandleFunc("/chat-area.html", serveStaticFile)

	// Handlers: agents moved to separate package
	mux.Handle("/api/agents", agenthttp.New(st))

	// Plugin endpoints
	mux.HandleFunc("/api/plugin-registry", pluginRegistryHandler.PluginRegistryHandler)
	mux.HandleFunc("/api/plugin-updates", pluginRegistryHandler.PluginUpdatesHandler)
	mux.HandleFunc("/api/plugins/download", pluginRegistryHandler.PluginDownloadHandler)
	mux.HandleFunc("/api/plugins/execute", pluginInitHandler.PluginExecuteHandler)
	mux.HandleFunc("/api/plugins/init-status", pluginInitHandler.PluginInitStatusHandler)

	// Create a handler instance for save-settings
	pluginMainHandler := pluginhttp.New(st, pluginhttp.NativeLoader{})
	mux.HandleFunc("/api/plugins/save-settings", pluginMainHandler.ServeHTTP)
	mux.Handle("/api/plugins", pluginMainHandler)
	mux.HandleFunc("/api/plugins/", pluginInitHandler.PluginInitHandler) // Handle /api/plugins/{name}/config and /api/plugins/{name}/initialize

	// Settings and configuration endpoints
	mux.HandleFunc("/api/settings", settingsHandler.SettingsHandler)
	mux.HandleFunc("/api/api-key", settingsHandler.APIKeyHandler)

	// Chat endpoint
	mux.HandleFunc("/api/chat", chatHandler.ChatHandler)

	// Update management endpoints
	updateHandler := updatehttp.NewHandler(updateMgr)
	mux.HandleFunc("/api/updates/check", updateHandler.CheckUpdatesHandler)
	mux.HandleFunc("/api/updates/releases", updateHandler.ListReleasesHandler)
	mux.HandleFunc("/api/updates/download", updateHandler.DownloadUpdateHandler)
	mux.HandleFunc("/api/updates/version", updateHandler.GetVersionHandler)

	addr := ":8080"
	log.Printf("Listening on http://localhost%s", addr)

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      120 * time.Second, // allow for model latency + tool calls
		IdleTimeout:       90 * time.Second,
	}
	log.Fatal(srv.ListenAndServe())
}

// ---------- Handlers below still use the Store (no package-level agents/currentAgent) ----------

func serveIndex(w http.ResponseWriter, r *http.Request) {
	// Get current agent info for template data
	data := web.GetDefaultData()

	// Try to get current agent from store
	if agents, current := st.ListAgents(); len(agents) > 0 {
		// Get the current agent as specified by the store
		currentAgentName := current
		if currentAgentName == "" {
			currentAgentName = agents[0] // fallback to first agent
		}
		if agent, found := st.GetAgent(currentAgentName); found && agent != nil {
			data.CurrentAgent = currentAgentName
			if agent.Settings.Model != "" {
				data.Model = agent.Settings.Model
			}
		}
	}

	// Render the index template
	html, err := templateRenderer.RenderTemplate("index", data)
	if err != nil {
		log.Printf("Failed to render template: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(html))
}

// serveStaticFile serves static files from the embedded filesystem
func serveStaticFile(w http.ResponseWriter, r *http.Request) {
	// Remove leading slash and prepend "static/"
	path := "static" + r.URL.Path

	// Read file from embedded filesystem
	content, err := web.Static.ReadFile(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// Set appropriate content type based on file extension
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

	// Disable cache for development to ensure updates are picked up
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")

	w.Write(content)
}

