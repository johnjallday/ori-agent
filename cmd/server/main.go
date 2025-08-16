package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
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
	web "github.com/johnjallday/dolphin-agent/internal/web"
)

var (
	client openai.Client

	// runtime state (moved behind Store)
	st          store.Store
	pluginReg   types.PluginRegistry
	defaultConf = types.Settings{Model: openai.ChatModelGPT4_1Nano, Temperature: 0}

	// template
	tmpl *template.Template

	// plugin downloader for external plugins
	pluginDownloader *plugindownloader.PluginDownloader
)

// loadPluginRegistry reads the registry dynamically with fallbacks.
// Returns: registry, baseDir (for resolving relative plugin paths), error.
func loadPluginRegistry() (types.PluginRegistry, string, error) {
	var reg types.PluginRegistry

	// 1) Env override
	if p := os.Getenv("PLUGIN_REGISTRY_PATH"); p != "" {
		if b, err := os.ReadFile(p); err == nil {
			if err := json.Unmarshal(b, &reg); err != nil {
				return reg, "", fmt.Errorf("parse %s: %w", p, err)
			}
			return reg, filepath.Dir(p), nil
		}
	}

	// 2) Local files
	for _, p := range []string{
		"plugin_registry.json",
		filepath.Join("internal", "web", "static", "plugin_registry.json"),
	} {
		if b, err := os.ReadFile(p); err == nil {
			if err := json.Unmarshal(b, &reg); err != nil {
				return reg, "", fmt.Errorf("parse %s: %w", p, err)
			}
			return reg, filepath.Dir(p), nil
		}
	}
	fmt.Println("plugin registry load complete")

	// 3) Embedded fallback
	b, err := web.Static.ReadFile("static/plugin_registry.json")
	if err != nil {
		return reg, "", fmt.Errorf("embedded plugin_registry.json not found: %w", err)
	}
	if err := json.Unmarshal(b, &reg); err != nil {
		return reg, "", fmt.Errorf("parse embedded plugin_registry.json: %w", err)
	}
	// baseDir empty -> relative paths are treated as-is
	return reg, "", nil
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
	storePath := "agents.json"
	if p := os.Getenv("AGENT_STORE_PATH"); p != "" {
		storePath = p
	} else if abs, err2 := filepath.Abs(storePath); err2 == nil {
		storePath = abs
	}
	log.Printf("Using agent store: %s", storePath)
	st, err = store.NewFileStore(storePath, defaultConf)
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
			lp.Tool = tool
			ag.Plugins[key] = lp
		}
		if err := st.SetAgent(agName, ag); err != nil {
			log.Printf("failed to restore plugins for agent %s: %v", agName, err)
		}
	}

	// load plugin registry from embedded FS
	if b, err := web.Static.ReadFile("static/plugin_registry.json"); err == nil {
		if err := json.Unmarshal(b, &pluginReg); err != nil {
			log.Printf("failed to parse plugin registry: %v", err)
		}
	} else {
		log.Printf("failed to read embedded plugin registry: %v", err)
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
	mux.Handle("/api/plugins", pluginhttp.New(st, pluginhttp.NativeLoader{}))
	mux.HandleFunc("/api/settings", settingsHandler)
	mux.HandleFunc("/api/chat", chatHandler)

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
				entryPath, err = pluginDownloader.GetPlugin(e)
				if err != nil {
					http.Error(w, fmt.Sprintf("failed to get plugin %s: %v", e.Name, err), http.StatusInternalServerError)
					return
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
		ag, ok := st.GetAgent(current)
		if !ok {
			http.Error(w, "current agent not found", http.StatusInternalServerError)
			return
		}
		if ag.Plugins == nil {
			ag.Plugins = map[string]types.LoadedPlugin{}
		}
		ag.Plugins[def.Name] = types.LoadedPlugin{Tool: tool, Definition: def, Path: entryPath}
		if err := st.SetAgent(current, ag); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
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

	// Prepare and call the model
	ag.Messages = append(ag.Messages, openai.UserMessage(q))
	params := openai.ChatCompletionNewParams{
		Model:       ag.Settings.Model,
		Temperature: openai.Float(ag.Settings.Temperature),
		Messages:    ag.Messages,
		Tools:       tools,
	}

	start := time.Now()
	resp, err := client.Chat.Completions.New(ctx, params)
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

		respFB, errFB := client.Chat.Completions.New(fbCtx, openai.ChatCompletionNewParams{
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
		tc := choice.ToolCalls[0]
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

		// Append the tool call + result to history
		ag.Messages = append(ag.Messages, choice.ToParam())
		ag.Messages = append(ag.Messages, openai.ToolMessage(result, tc.ID))

		// Ask model again with tool output
		resp2, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
			Model:       ag.Settings.Model,
			Temperature: openai.Float(ag.Settings.Temperature),
			Messages:    ag.Messages,
		})
		if err != nil || resp2 == nil || len(resp2.Choices) == 0 {
			// If second turn fails, still return the tool result as a best-effort reply
			_ = json.NewEncoder(w).Encode(map[string]any{
				"response": result,
				"toolCall": map[string]string{
					"function": name,
					"args":     args,
					"result":   result,
				},
			})
			return
		}
		final := resp2.Choices[0].Message
		ag.Messages = append(ag.Messages, final.ToParam())

		log.Printf("Chat (with tool) in %s", time.Since(start))
		_ = st.SetAgent(current, ag) // persists settings/plugins only
		_ = json.NewEncoder(w).Encode(map[string]any{
			"response": final.Content,
			"toolCall": map[string]string{
				"function": name,
				"args":     args,
				"result":   result,
			},
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
				_, err := pluginDownloader.GetPlugin(entry)
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
