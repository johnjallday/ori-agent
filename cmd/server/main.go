package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"path/filepath"

	"github.com/openai/openai-go/v2"

	agenthttp "github.com/johnjallday/dolphin-agent/internal/agenthttp"
	"github.com/johnjallday/dolphin-agent/internal/plugindownloader"
	pluginhttp "github.com/johnjallday/dolphin-agent/internal/pluginhttp"
	"github.com/johnjallday/dolphin-agent/internal/pluginloader"
	"github.com/johnjallday/dolphin-agent/internal/store"
	"github.com/johnjallday/dolphin-agent/internal/types"
	"github.com/johnjallday/dolphin-agent/internal/updatehttp"
	"github.com/johnjallday/dolphin-agent/internal/updatemanager"
	"github.com/johnjallday/dolphin-agent/internal/version"
	web "github.com/johnjallday/dolphin-agent/internal/web"
	"github.com/johnjallday/dolphin-agent/internal/client"
	"github.com/johnjallday/dolphin-agent/internal/config"
	"github.com/johnjallday/dolphin-agent/internal/registry"
)

var (
	clientFactory *client.Factory
	registryManager *registry.Manager

	// runtime state (moved behind Store)
	st             store.Store
	pluginReg      types.PluginRegistry
	defaultConf    = types.Settings{Model: openai.ChatModelGPT4_1Nano, Temperature: 0}
	agentStorePath string
	configManager  *config.Manager

	// template renderer
	templateRenderer *web.TemplateRenderer

	// plugin downloader for external plugins
	pluginDownloader *plugindownloader.PluginDownloader

	// update manager for software updates
	updateMgr *updatemanager.Manager
)



// resolvePluginPath resolves an entry's Path against the registry base dir.
// If the plugin path is absolute, returns it directly. Otherwise, it first tries
// to resolve relative to baseDir, and if that file does not exist, falls back
// to using the path as-is (relative to working directory).
func resolvePluginPath(baseDir, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	// Try resolving relative to baseDir if provided.
	if baseDir != "" {
		candidate := filepath.Join(baseDir, p)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	// Fallback to p relative to current working directory.
	return p
}

// getPluginEmoji returns an appropriate emoji for a plugin based on its name
func getPluginEmoji(pluginName string) string {
	name := strings.ToLower(pluginName)

	// Music/Audio related
	if strings.Contains(name, "music") || strings.Contains(name, "reaper") || strings.Contains(name, "audio") {
		return "🎵"
	}

	// Development/Code related
	if strings.Contains(name, "code") || strings.Contains(name, "dev") || strings.Contains(name, "git") {
		return "💻"
	}

	// File/System related
	if strings.Contains(name, "file") || strings.Contains(name, "system") || strings.Contains(name, "manager") {
		return "📁"
	}

	// Data/Database related
	if strings.Contains(name, "data") || strings.Contains(name, "database") || strings.Contains(name, "sql") {
		return "📊"
	}

	// Network/Web related
	if strings.Contains(name, "web") || strings.Contains(name, "http") || strings.Contains(name, "api") {
		return "🌐"
	}

	// Default plugin emoji
	return "🔌"
}

// handleAgentStatusCommand handles the /agent command to show agent status dashboard
func handleAgentStatusCommand(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	
	// Get current agent information
	names, current := st.ListAgents()
	if current == "" && len(names) > 0 {
		current = names[0] // fallback to first available agent
	}
	
	ag, ok := st.GetAgent(current)
	if !ok {
		http.Error(w, "current agent not found", http.StatusInternalServerError)
		return
	}
	
	// Get API key status
	apiKey := configManager.GetAPIKey()
	apiKeyStatus := "Not Set"
	if apiKey != "" {
		apiKeyStatus = "Environment variable set"
		if configKey := configManager.Get().OpenAIAPIKey; configKey != "" {
			apiKeyStatus = "Settings file: " + configManager.MaskAPIKey()
		}
	}
	
	// Build status dashboard
	statusResponse := fmt.Sprintf(`## 🤖 Agent Status Dashboard

**Current Agent:** %s

**Model Configuration:**
- Model: %s
- Temperature: %.1f

**API Configuration:**
- API Key: %s

**Plugin Status:**
- Total Plugins: %d`,
		current,
		ag.Settings.Model,
		ag.Settings.Temperature,
		apiKeyStatus,
		len(ag.Plugins))
	
	// Add plugin details
	if len(ag.Plugins) > 0 {
		statusResponse += "\n- Active Plugins:\n"
		for name, plugin := range ag.Plugins {
			statusResponse += fmt.Sprintf("  - %s %s (v%s)\n", getPluginEmoji(name), name, plugin.Version)
		}
	} else {
		statusResponse += "\n- No plugins loaded"
	}
	
	// Add system information
	statusResponse += "\n\n**System Status:**\n- Server: Running ✅\n- Registry: Loaded ✅"
	
	// Return as a response that mimics a chat message
	response := map[string]any{
		"response": statusResponse,
	}
	
	json.NewEncoder(w).Encode(response)
}

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
	// Determine agent store path based on current agent from settings
	currentAgentPath := fmt.Sprintf("agents/%s/config.json", configManager.GetCurrentAgent())
	agentStorePath = currentAgentPath
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
				continue
			}

			// Set agent context for plugins that support it
			pluginloader.SetAgentContext(tool, agName, agentStorePath)

			lp.Tool = tool
			ag.Plugins[key] = lp
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

	mux := http.NewServeMux()
	mux.HandleFunc("/", serveIndex)

	// Static file server for CSS, JS, icons, and other assets
	mux.HandleFunc("/styles.css", serveStaticFile)
	mux.HandleFunc("/js/", serveStaticFile)
	mux.HandleFunc("/icons/", serveStaticFile)
	mux.HandleFunc("/chat-area.html", serveStaticFile)

	// Handlers: agents moved to separate package
	mux.Handle("/api/agents", agenthttp.New(st))

	// Other existing endpoints kept here for now (plugins, registry, settings, chat)
	mux.HandleFunc("/api/plugin-registry", pluginRegistryHandler)
	mux.HandleFunc("/api/plugin-updates", pluginUpdatesHandler)
	mux.HandleFunc("/api/plugins/download", pluginDownloadHandler)
	mux.Handle("/api/plugins", pluginhttp.New(st, pluginhttp.NativeLoader{}))
	mux.HandleFunc("/api/settings", settingsHandler)
	mux.HandleFunc("/api/api-key", apiKeyHandler)
	mux.HandleFunc("/api/chat", chatHandler)

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

	// Add cache headers for static assets
	w.Header().Set("Cache-Control", "public, max-age=3600")

	w.Write(content)
}

// Registry
func pluginRegistryHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		reg, _, err := registryManager.Load()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(reg)

	case http.MethodPost:
		var req struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(req.Name) == "" {
			http.Error(w, "name required", http.StatusBadRequest)
			return
		}

		reg, _, err := registryManager.Load()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// find entry by name
		var entryPath string
		var found bool
		for _, e := range reg.Plugins {
			if e.Name == req.Name {
				// Use plugin downloader to get the plugin (handles both local and remote)
				var err error
				var wasCached bool
				entryPath, wasCached, err = pluginDownloader.GetPlugin(e)
				if err != nil {
					http.Error(w, fmt.Sprintf("failed to get plugin %s: %v", e.Name, err), http.StatusInternalServerError)
					return
				}

				if wasCached {
					fmt.Printf("Plugin %s is already downloaded\n", e.Name)
				}

				// Ensure path is absolute
				if abs, err := filepath.Abs(entryPath); err == nil {
					entryPath = abs
				}
				// skip if already loaded for current agent (avoid duplicate plugin.Open errors)
				_, current := st.ListAgents()
				ag, ok := st.GetAgent(current)
				if ok {
					for _, lp := range ag.Plugins {
						// Check if plugin is already loaded from the same file path
						lpAbsPath, err1 := filepath.Abs(lp.Path)
						if err1 == nil && lpAbsPath == entryPath {
							w.WriteHeader(http.StatusOK)
							return
						}
						// Also check by definition name for backward compatibility
						if strings.EqualFold(lp.Definition.Name, e.Name) {
							w.WriteHeader(http.StatusOK)
							return
						}
					}
				}
				found = true
				break
			}
		}
		if !found {
			http.Error(w, "plugin not found in registry", http.StatusBadRequest)
			return
		}

		// load plugin using cache
		tool, err := pluginloader.LoadWithCache(entryPath)
		if err != nil {
			http.Error(w, fmt.Sprintf("load plugin %s: %v", entryPath, err), http.StatusInternalServerError)
			return
		}
		def := tool.Definition()

		// attach to current agent
		_, current := st.ListAgents()

		// Set agent context for plugins that support it
		pluginloader.SetAgentContext(tool, current, agentStorePath)
		ag, ok := st.GetAgent(current)
		if !ok {
			http.Error(w, "current agent not found", http.StatusInternalServerError)
			return
		}
		if ag.Plugins == nil {
			ag.Plugins = map[string]types.LoadedPlugin{}
		}
		version := pluginloader.GetPluginVersion(tool)
		ag.Plugins[def.Name] = types.LoadedPlugin{Tool: tool, Definition: def, Path: entryPath, Version: version}
		if err := st.SetAgent(current, ag); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Generic plugin welcome - try to display settings for any plugin that supports it
		go func() {
			// Small delay to ensure plugin is fully loaded
			time.Sleep(100 * time.Millisecond)

			ctx := context.Background()

			// Try common settings operations - plugins can implement any of these
			settingsOperations := []string{
				`{"operation":"get_settings"}`,
				`{"operation":"status"}`,
				`{"operation":"info"}`,
			}

			var settingsResult string
			var settingsErr error

			// Try each operation until one works
			for _, operation := range settingsOperations {
				settingsResult, settingsErr = tool.Call(ctx, operation)
				if settingsErr == nil {
					break
				}
			}

			if settingsErr != nil {
				// If no settings operation works, just log the basic load message
				log.Printf("🔌 Plugin '%s' loaded successfully!", def.Name)
				return
			}

			// Get a suitable emoji based on plugin name
			emoji := getPluginEmoji(def.Name)

			log.Printf("%s Plugin '%s' loaded successfully!", emoji, def.Name)
			log.Printf("Current settings/status:\n%s", settingsResult)
		}()

		w.WriteHeader(http.StatusOK)

	case http.MethodDelete:
		name := r.URL.Query().Get("name")
		if strings.TrimSpace(name) == "" {
			http.Error(w, "name required", http.StatusBadRequest)
			return
		}

		// Only delete from local registry (user uploaded plugins)
		localReg, err := registryManager.LoadLocal()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Find and remove the plugin from local registry only
		var foundIndex = -1
		var pluginToDelete types.PluginRegistryEntry
		for i, plugin := range localReg.Plugins {
			if plugin.Name == name {
				foundIndex = i
				pluginToDelete = plugin
				break
			}
		}

		if foundIndex == -1 {
			http.Error(w, "plugin not found in local registry (only user uploaded plugins can be deleted)", http.StatusNotFound)
			return
		}

		// Remove plugin from local registry
		localReg.Plugins = append(localReg.Plugins[:foundIndex], localReg.Plugins[foundIndex+1:]...)

		// Save updated local registry
		if err := registryManager.SaveLocal(localReg); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Remove the plugin file if it's in uploaded_plugins directory
		if pluginToDelete.Path != "" && strings.Contains(pluginToDelete.Path, "uploaded_plugins") {
			if err := os.Remove(pluginToDelete.Path); err != nil {
				// Log the error but don't fail the request - registry entry is already removed
				log.Printf("Warning: Failed to remove plugin file %s: %v", pluginToDelete.Path, err)
			}
		}

		// Also unload the plugin from current agent if it's loaded
		_, current := st.ListAgents()
		ag, ok := st.GetAgent(current)
		if ok && ag.Plugins != nil {
			delete(ag.Plugins, name)
			if err := st.SetAgent(current, ag); err != nil {
				log.Printf("Warning: Failed to unload plugin %s from agent: %v", name, err)
			}
		}

		w.WriteHeader(http.StatusOK)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// Settings
func settingsHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		_, current := st.ListAgents()
		ag, ok := st.GetAgent(current)
		if !ok {
			http.Error(w, "current agent not found", http.StatusInternalServerError)
			return
		}
		// Wrap settings in the expected format for frontend compatibility
		response := struct {
			Settings types.Settings `json:"Settings"`
		}{
			Settings: ag.Settings,
		}
		_ = json.NewEncoder(w).Encode(response)

	case http.MethodPost:
		var s types.Settings
		if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_, current := st.ListAgents()
		ag, ok := st.GetAgent(current)
		if !ok {
			http.Error(w, "current agent not found", http.StatusInternalServerError)
			return
		}
		ag.Settings = s
		if err := st.SetAgent(current, ag); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func apiKeyHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// Return masked API key for display
		response := struct {
			APIKey string `json:"api_key"`
			Masked string `json:"masked"`
		}{
			APIKey: configManager.Get().OpenAIAPIKey,
			Masked: configManager.MaskAPIKey(),
		}
		_ = json.NewEncoder(w).Encode(response)

	case http.MethodPost:
		var req struct {
			APIKey string `json:"api_key"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		
		// Set API key in config manager (includes validation)
		if err := configManager.SetAPIKey(req.APIKey); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		
		// Save configuration
		if err := configManager.Save(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		
		// Update global client with new API key (only if not empty)
		if req.APIKey != "" {
			clientFactory.UpdateDefaultClient(req.APIKey)
		}
		
		w.WriteHeader(http.StatusOK)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}


// getClientForAgent returns an OpenAI client using the agent's API key if provided, otherwise the global client
func getClientForAgent(ag *types.Agent) openai.Client {
	return clientFactory.GetForAgent(ag)
}

// Chat (same logic as yours; now pulls state from Store; messages stay in-memory)

func chatHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req struct {
		Question string `json:"question"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	q := strings.TrimSpace(req.Question)
	if q == "" {
		http.Error(w, "empty question", http.StatusBadRequest)
		return
	}
	
	// Handle special commands
	if q == "/agent" {
		handleAgentStatusCommand(w, r)
		return
	}

	log.Printf("Chat question received")
	// Context with timeout per request (prevents indefinite hang)
	base := r.Context()
	ctx, cancel := context.WithTimeout(base, 45*time.Second)
	defer cancel()

	// Load agent - for single agent stores, use the current agent name stored in the config
	names, current := st.ListAgents()
	if current == "" && len(names) > 0 {
		current = names[0] // fallback to first available agent
	}
	ag, ok := st.GetAgent(current)
	if !ok {
		http.Error(w, "current agent not found", http.StatusInternalServerError)
		return
	}

	// Build tools
	tools := []openai.ChatCompletionToolUnionParam{}
	for _, pl := range ag.Plugins {
		tools = append(tools, openai.ChatCompletionFunctionTool(pl.Definition))
	}

	// Get appropriate client for this agent
	agentClient := getClientForAgent(ag)

	// Add system message for better tool usage guidance
	if len(ag.Messages) == 0 {
		systemPrompt := "You are a helpful assistant with access to various tools. When a user request can be fulfilled by using an available tool, use the tool instead of providing general information. Be concise and direct in your responses."
		if len(tools) > 0 {
			systemPrompt += " Available tools: "
			var toolNames []string
			for _, pl := range ag.Plugins {
				toolNames = append(toolNames, pl.Definition.Name)
			}
			systemPrompt += strings.Join(toolNames, ", ") + "."
		}
		ag.Messages = append(ag.Messages, openai.SystemMessage(systemPrompt))
	}

	// Prepare and call the model
	ag.Messages = append(ag.Messages, openai.UserMessage(q))
	params := openai.ChatCompletionNewParams{
		Model:       ag.Settings.Model,
		Temperature: openai.Float(ag.Settings.Temperature),
		Messages:    ag.Messages,
		Tools:       tools,
	}

	start := time.Now()
	resp, err := agentClient.Chat.Completions.New(ctx, params)
	if err != nil {
		// Return error as a chat message instead of HTTP error
		errorResponse := map[string]any{
			"response": fmt.Sprintf("❌ **Error**: %v", err),
		}
		json.NewEncoder(w).Encode(errorResponse)
		return
	}
	if resp == nil || len(resp.Choices) == 0 {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"response": "I couldn’t generate a reply just now. Please try again.",
		})
		return
	}
	choice := resp.Choices[0].Message

	// Fallback if model answered with an empty assistant message and no tool calls
	if len(choice.ToolCalls) == 0 && strings.TrimSpace(choice.Content) == "" {
		// fresh timeout for fallback, so we don’t reuse a nearly-expired ctx
		fbCtx, fbCancel := context.WithTimeout(base, 20*time.Second)
		defer fbCancel()

		respFB, errFB := agentClient.Chat.Completions.New(fbCtx, openai.ChatCompletionNewParams{
			Model:       ag.Settings.Model,
			Temperature: openai.Float(ag.Settings.Temperature),
			Messages: append(ag.Messages,
				openai.SystemMessage("Answer directly in plain text. Do not call any tools."),
			),
		})
		if errFB == nil && respFB != nil && len(respFB.Choices) > 0 {
			choice = respFB.Choices[0].Message
		}
	}

	// Tool-call branch
	if len(choice.ToolCalls) > 0 {
		// Append the assistant message with tool calls first
		ag.Messages = append(ag.Messages, choice.ToParam())

		// Process ALL tool calls, not just the first one
		var toolResults []map[string]string
		for _, tc := range choice.ToolCalls {
			name := tc.Function.Name
			args := tc.Function.Arguments

			// Find plugin by function definition name
			var pl types.LoadedPlugin
			var found bool
			for _, plugin := range ag.Plugins {
				if plugin.Definition.Name == name {
					pl = plugin
					found = true
					break
				}
			}

			if !found || pl.Tool == nil {
				http.Error(w, fmt.Sprintf("plugin %q not loaded", name), http.StatusInternalServerError)
				return
			}

			// Execute tool with its own reasonable timeout
			toolCtx, toolCancel := context.WithTimeout(base, 20*time.Second)
			defer toolCancel()

			result, err := pl.Tool.Call(toolCtx, args)
			if err != nil {
				http.Error(w, fmt.Sprintf("tool %s error: %v", name, err), http.StatusBadGateway)
				return
			}

			// Add tool message response for this specific tool call ID
			ag.Messages = append(ag.Messages, openai.ToolMessage(result, tc.ID))

			// Store result for final response
			toolResults = append(toolResults, map[string]string{
				"function": name,
				"args":     args,
				"result":   result,
			})
		}

		// Ask model again with tool output, with guidance to focus on the tool result
		resp2, err := agentClient.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
			Model:       ag.Settings.Model,
			Temperature: openai.Float(ag.Settings.Temperature),
			Messages: append(ag.Messages,
				openai.SystemMessage("The tool was executed successfully. If the tool returned configuration data, settings, or structured information, please display that data clearly to the user. For other operations, provide a brief acknowledgment."),
			),
		})
		if err != nil || resp2 == nil || len(resp2.Choices) == 0 {
			// If second turn fails, still return the tool results as a best-effort reply
			var combinedResult string
			for i, tr := range toolResults {
				if i > 0 {
					combinedResult += "\n\n"
				}
				combinedResult += tr["result"]
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"response":  combinedResult,
				"toolCalls": toolResults,
			})
			return
		}
		final := resp2.Choices[0].Message
		ag.Messages = append(ag.Messages, final.ToParam())

		log.Printf("Chat (with tool) in %s", time.Since(start))
		_ = st.SetAgent(current, ag) // persists settings/plugins only
		_ = json.NewEncoder(w).Encode(map[string]any{
			"response":  final.Content,
			"toolCalls": toolResults,
		})
		return
	}

	// Plain answer path
	text := strings.TrimSpace(choice.Content)
	if text == "" {
		text = "I couldn’t generate a reply just now. Please try again."
	}
	ag.Messages = append(ag.Messages, choice.ToParam())

	log.Printf("Chat response in %s", time.Since(start))
	_ = st.SetAgent(current, ag) // persists settings/plugins only
	_ = json.NewEncoder(w).Encode(map[string]any{"response": text})
}

// Plugin Updates Handler
func pluginUpdatesHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// Check for available updates
		reg, _, err := registryManager.Load()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		updates, err := pluginDownloader.CheckForUpdates(reg)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"available_updates": updates,
			"count":             len(updates),
		})

	case http.MethodPost:
		// Trigger update for specific plugins or all
		var req struct {
			PluginNames []string `json:"plugin_names,omitempty"` // Empty = update all
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		reg, _, err := registryManager.Load()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		var updated []string
		var errors []string

		for _, entry := range reg.Plugins {
			// Skip if specific plugins requested and this isn't one of them
			if len(req.PluginNames) > 0 {
				found := false
				for _, name := range req.PluginNames {
					if name == entry.Name {
						found = true
						break
					}
				}
				if !found {
					continue
				}
			}

			// Only update plugins with URLs and auto-update enabled
			if entry.URL != "" && entry.AutoUpdate {
				_, _, err := pluginDownloader.GetPlugin(entry)
				if err != nil {
					errors = append(errors, fmt.Sprintf("%s: %v", entry.Name, err))
				} else {
					updated = append(updated, entry.Name)
				}
			}
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"updated": updated,
			"errors":  errors,
		})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// Plugin Download Handler - Downloads GitHub plugins to uploaded_plugins folder
func pluginDownloadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"message": "method not allowed",
		})
		return
	}

	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	if strings.TrimSpace(req.Name) == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"message": "name required",
		})
		return
	}

	// Load registry to find the plugin
	reg, _, err := registryManager.Load()
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	// Find the plugin entry
	var pluginEntry *types.PluginRegistryEntry
	for _, entry := range reg.Plugins {
		if entry.Name == req.Name {
			pluginEntry = &entry
			break
		}
	}

	if pluginEntry == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"message": "plugin not found in registry",
		})
		return
	}

	// Check if it has a download URL
	if pluginEntry.DownloadURL == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"message": "plugin does not have a download URL",
		})
		return
	}

	// Create uploaded_plugins directory if it doesn't exist
	uploadDir := "uploaded_plugins"
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"message": fmt.Sprintf("failed to create upload directory: %v", err),
		})
		return
	}

	// Download the plugin file
	resp, err := http.Get(pluginEntry.DownloadURL)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"message": fmt.Sprintf("failed to download plugin: %v", err),
		})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"message": fmt.Sprintf("download failed with status %d", resp.StatusCode),
		})
		return
	}

	// Determine the filename from the download URL
	filename := fmt.Sprintf("%s.so", pluginEntry.Name)
	if pluginEntry.DownloadURL != "" {
		// Extract filename from download URL
		parsedURL, err := url.Parse(pluginEntry.DownloadURL)
		if err == nil {
			urlFilename := filepath.Base(parsedURL.Path)
			if urlFilename != "." && urlFilename != "/" {
				filename = urlFilename
			}
		}
	}

	filePath := filepath.Join(uploadDir, filename)

	// Create the file
	file, err := os.Create(filePath)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"message": fmt.Sprintf("failed to create file: %v", err),
		})
		return
	}
	defer file.Close()

	// Copy the downloaded content to the file
	_, err = io.Copy(file, resp.Body)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"message": fmt.Sprintf("failed to save file: %v", err),
		})
		return
	}

	// Scan uploaded plugins to auto-register the newly downloaded plugin
	if err := registryManager.ScanUploadedPlugins(); err != nil {
		log.Printf("Warning: failed to scan uploaded_plugins after download: %v", err)
	}

	// Return success response
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"success":  true,
		"message":  fmt.Sprintf("Plugin %s downloaded successfully", pluginEntry.Name),
		"filename": filename,
		"path":     filePath,
	})
}
