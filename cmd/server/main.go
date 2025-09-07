package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"path/filepath"

	"github.com/openai/openai-go/v2"
	"github.com/openai/openai-go/v2/option"

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
)

var (
	client openai.Client

	// runtime state (moved behind Store)
	st             store.Store
	pluginReg      types.PluginRegistry
	defaultConf    = types.Settings{Model: openai.ChatModelGPT4_1Nano, Temperature: 0}
	agentStorePath string

	// template
	tmpl *template.Template

	// plugin downloader for external plugins
	pluginDownloader *plugindownloader.PluginDownloader

	// update manager for software updates
	updateMgr *updatemanager.Manager
)

// fetchGitHubPluginRegistry fetches the plugin registry from GitHub
func fetchGitHubPluginRegistry() (types.PluginRegistry, error) {
	var reg types.PluginRegistry

	url := "https://raw.githubusercontent.com/johnjallday/dolphin-plugin-registry/main/plugin_registry.json"

	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	resp, err := client.Get(url)
	if err != nil {
		return reg, fmt.Errorf("failed to fetch plugin registry from GitHub: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return reg, fmt.Errorf("GitHub returned HTTP %d when fetching plugin registry", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return reg, fmt.Errorf("failed to read GitHub response: %w", err)
	}

	if err := json.Unmarshal(body, &reg); err != nil {
		return reg, fmt.Errorf("failed to parse GitHub plugin registry JSON: %w", err)
	}

	return reg, nil
}

// loadLocalPluginRegistry loads the user's local plugin registry
func loadLocalPluginRegistry() (types.PluginRegistry, error) {
	var reg types.PluginRegistry
	localRegistryPath := "local_plugin_registry.json"

	// Try to read local registry file
	if b, err := os.ReadFile(localRegistryPath); err == nil {
		if err := json.Unmarshal(b, &reg); err != nil {
			return reg, fmt.Errorf("failed to parse local plugin registry: %w", err)
		}
	}
	// If file doesn't exist, return empty registry (not an error)
	return reg, nil
}

// saveLocalPluginRegistry saves the local plugin registry to file
func saveLocalPluginRegistry(reg types.PluginRegistry) error {
	localRegistryPath := "local_plugin_registry.json"

	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal local registry: %w", err)
	}

	if err := os.WriteFile(localRegistryPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write local registry: %w", err)
	}

	return nil
}

// mergePluginRegistries combines online and local plugin registries
func mergePluginRegistries(online, local types.PluginRegistry) types.PluginRegistry {
	merged := types.PluginRegistry{}

	// Create a map to track plugin names and avoid duplicates
	pluginMap := make(map[string]types.PluginRegistryEntry)

	// Add online plugins first
	for _, plugin := range online.Plugins {
		pluginMap[plugin.Name] = plugin
	}

	// Add local plugins (they override online plugins with same name)
	for _, plugin := range local.Plugins {
		pluginMap[plugin.Name] = plugin
	}

	// Convert map back to slice
	for _, plugin := range pluginMap {
		merged.Plugins = append(merged.Plugins, plugin)
	}

	return merged
}

// loadPluginRegistry reads the registry dynamically with fallbacks, merging online and local registries.
// Returns: registry, baseDir (for resolving relative plugin paths), error.
func loadPluginRegistry() (types.PluginRegistry, string, error) {
	var onlineReg types.PluginRegistry
	cachePath := "plugin_registry_cache.json"

	// 1) Env override (highest priority) - if set, use only this and merge with local
	if p := os.Getenv("PLUGIN_REGISTRY_PATH"); p != "" {
		if b, err := os.ReadFile(p); err == nil {
			if err := json.Unmarshal(b, &onlineReg); err != nil {
				return onlineReg, "", fmt.Errorf("parse %s: %w", p, err)
			}
			// Merge with local registry
			if localReg, err := loadLocalPluginRegistry(); err == nil {
				merged := mergePluginRegistries(onlineReg, localReg)
				return merged, p, nil
			}
			return onlineReg, p, nil
		}
	}

	// 2) Try to fetch from GitHub (primary online source)
	if githubReg, err := fetchGitHubPluginRegistry(); err == nil {
		// Success! Cache it for offline use
		if data, marshalErr := json.MarshalIndent(githubReg, "", "  "); marshalErr == nil {
			os.WriteFile(cachePath, data, 0644) // Ignore error - caching is optional
		}
		onlineReg = githubReg
		fmt.Println("plugin registry loaded from GitHub")
	} else {
		fmt.Printf("Failed to load plugin registry from GitHub: %v\n", err)
	}

	// If GitHub failed, try other fallback sources
	if len(onlineReg.Plugins) == 0 {
		// 3) Try cached version (offline fallback)
		if b, err := os.ReadFile(cachePath); err == nil {
			if err := json.Unmarshal(b, &onlineReg); err == nil {
				fmt.Println("plugin registry loaded from cache")
			} else {
				fmt.Printf("Failed to parse cached plugin registry: %v\n", err)
			}
		}

		// 4) Local files (legacy fallback)
		if len(onlineReg.Plugins) == 0 {
			for _, p := range []string{
				"plugin_registry.json",
				filepath.Join("internal", "web", "static", "plugin_registry.json"),
			} {
				if b, err := os.ReadFile(p); err == nil {
					if err := json.Unmarshal(b, &onlineReg); err == nil {
						fmt.Printf("plugin registry loaded from local file: %s\n", p)
						break
					}
				}
			}
		}

		// 5) Embedded fallback (last resort)
		if len(onlineReg.Plugins) == 0 {
			if b, err := web.Static.ReadFile("static/plugin_registry.json"); err == nil {
				if err := json.Unmarshal(b, &onlineReg); err == nil {
					fmt.Println("plugin registry loaded from embedded file")
				}
			}
		}
	}

	// Always try to load and merge local registry
	localReg, err := loadLocalPluginRegistry()
	if err != nil {
		fmt.Printf("Warning: failed to load local plugin registry: %v\n", err)
		// Return online registry only if local loading fails
		return onlineReg, "", nil
	}

	// Merge online and local registries
	merged := mergePluginRegistries(onlineReg, localReg)
	if len(localReg.Plugins) > 0 {
		fmt.Printf("Merged %d online plugins with %d local plugins\n", len(onlineReg.Plugins), len(localReg.Plugins))
	}

	return merged, "", nil
}

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

func main() {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		log.Fatal("OPENAI_API_KEY environment variable is required")
	}

	// NEW: http client with sane timeouts for OpenAI calls
	httpClient := &http.Client{
		Timeout: 60 * time.Second, // hard cap per request
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
		},
	}
	client = openai.NewClient(
		option.WithAPIKey(apiKey),
		option.WithHTTPClient(httpClient), // <- important
	)
	//client = openai.NewClient(option.WithAPIKey(apiKey))

	// init store (persists agents/plugins/settings; not messages)
	var err error
	// Determine agent store path (absolute by default, override via AGENT_STORE_PATH)
	agentStorePath = "agents.json"
	if p := os.Getenv("AGENT_STORE_PATH"); p != "" {
		agentStorePath = p
	} else if abs, err2 := filepath.Abs(agentStorePath); err2 == nil {
		agentStorePath = abs
	}
	log.Printf("Using agent store: %s", agentStorePath)
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
	if reg, _, err := loadPluginRegistry(); err == nil {
		pluginReg = reg
	} else {
		log.Printf("failed to load plugin registry: %v", err)
	}

	// parse template from embedded FS
	if b, err := web.Static.ReadFile("static/index.html"); err == nil {
		tmpl = template.Must(template.New("index").Parse(string(b)))
	} else {
		log.Fatalf("failed to read embedded static/index.html: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", serveIndex)

	// Handlers: agents moved to separate package
	mux.Handle("/api/agents", agenthttp.New(st))

	// Other existing endpoints kept here for now (plugins, registry, settings, chat)
	mux.HandleFunc("/api/plugin-registry", pluginRegistryHandler)
	mux.HandleFunc("/api/plugin-updates", pluginUpdatesHandler)
	mux.HandleFunc("/api/plugins/download", pluginDownloadHandler)
	mux.Handle("/api/plugins", pluginhttp.New(st, pluginhttp.NativeLoader{}))
	mux.HandleFunc("/api/settings", settingsHandler)
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
	_ = tmpl.Execute(w, nil)
}

// Registry
func pluginRegistryHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		reg, _, err := loadPluginRegistry()
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

		reg, _, err := loadPluginRegistry()
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
		localReg, err := loadLocalPluginRegistry()
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
		if err := saveLocalPluginRegistry(localReg); err != nil {
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
		_ = json.NewEncoder(w).Encode(ag.Settings)

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

// getClientForAgent returns an OpenAI client using the agent's API key if provided, otherwise the global client
func getClientForAgent(ag *types.Agent) openai.Client {
	if ag.Settings.APIKey != "" {
		// Create client with agent-specific API key
		httpClient := &http.Client{
			Timeout: 60 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		}
		return openai.NewClient(
			option.WithAPIKey(ag.Settings.APIKey),
			option.WithHTTPClient(httpClient),
		)
	}
	// Use global client (with env API key)
	return client
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

	log.Printf("Chat question received")
	// Context with timeout per request (prevents indefinite hang)
	base := r.Context()
	ctx, cancel := context.WithTimeout(base, 45*time.Second)
	defer cancel()

	// Load agent
	_, current := st.ListAgents()
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
		// surface timeout/cancel clearly to the client
		http.Error(w, fmt.Sprintf("chat completion error: %v", err), http.StatusBadGateway)
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

			pl, ok := ag.Plugins[name]
			if !ok || pl.Tool == nil {
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
		reg, _, err := loadPluginRegistry()
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

		reg, _, err := loadPluginRegistry()
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
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

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

	// Load registry to find the plugin
	reg, _, err := loadPluginRegistry()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
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
		http.Error(w, "plugin not found in registry", http.StatusNotFound)
		return
	}

	// Check if it has a download URL
	if pluginEntry.DownloadURL == "" {
		http.Error(w, "plugin does not have a download URL", http.StatusBadRequest)
		return
	}

	// Create uploaded_plugins directory if it doesn't exist
	uploadDir := "uploaded_plugins"
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		http.Error(w, fmt.Sprintf("failed to create upload directory: %v", err), http.StatusInternalServerError)
		return
	}

	// Download the plugin file
	resp, err := http.Get(pluginEntry.DownloadURL)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to download plugin: %v", err), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		http.Error(w, fmt.Sprintf("download failed with status %d", resp.StatusCode), http.StatusInternalServerError)
		return
	}

	// Determine the filename from the URL or use plugin name
	filename := fmt.Sprintf("%s.so", pluginEntry.Name)
	if pluginEntry.Path != "" {
		filename = filepath.Base(pluginEntry.Path)
	}

	filePath := filepath.Join(uploadDir, filename)

	// Create the file
	file, err := os.Create(filePath)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to create file: %v", err), http.StatusInternalServerError)
		return
	}
	defer file.Close()

	// Copy the downloaded content to the file
	_, err = io.Copy(file, resp.Body)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to save file: %v", err), http.StatusInternalServerError)
		return
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
