package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"plugin"
	"strings"

	"github.com/johnjallday/dolphin-agent/pluginapi"
	"github.com/openai/openai-go/v2"
	"github.com/openai/openai-go/v2/option"

	agenthttp "github.com/johnjallday/dolphin-agent/internal/agenthttp"
	pluginhttp "github.com/johnjallday/dolphin-agent/internal/pluginhttp"
	"github.com/johnjallday/dolphin-agent/internal/store"
	"github.com/johnjallday/dolphin-agent/internal/types"
	web "github.com/johnjallday/dolphin-agent/internal/web"
)

var (
	client openai.Client

	// runtime state (moved behind Store)
	st          store.Store
	pluginReg   types.PluginRegistry
	defaultConf = types.Settings{Model: openai.ChatModelGPT4oMini, Temperature: 0.7}

	// template
	tmpl *template.Template
)

func main() {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		log.Fatal("OPENAI_API_KEY environment variable is required")
	}
	client = openai.NewClient(option.WithAPIKey(apiKey))

	// init store (persists agents/plugins/settings; not messages)
	var err error
	st, err = store.NewFileStore("agents.json", defaultConf)
	if err != nil {
		log.Fatalf("store init: %v", err)
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
	mux.Handle("/api/plugins", pluginhttp.New(st, pluginhttp.NativeLoader{}))
	mux.HandleFunc("/api/settings", settingsHandler)
	mux.HandleFunc("/api/chat", chatHandler)

	addr := ":8080"
	log.Printf("Listening on http://localhost%s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

// ---------- Handlers below still use the Store (no package-level agents/currentAgent) ----------

func serveIndex(w http.ResponseWriter, r *http.Request) {
	_ = tmpl.Execute(w, nil)
}

// Registry
func pluginRegistryHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		_ = json.NewEncoder(w).Encode(pluginReg)
	case http.MethodPost:
		var req struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var entryPath string
		var found bool
		for _, e := range pluginReg.Plugins {
			if e.Name == req.Name {
				entryPath, found = e.Path, true
				break
			}
		}
		if !found {
			http.Error(w, "plugin not found in registry", http.StatusBadRequest)
			return
		}
		p, err := plugin.Open(entryPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		sym, err := p.Lookup("Tool")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		tool, ok := sym.(pluginapi.Tool)
		if !ok {
			http.Error(w, "invalid plugin type", http.StatusBadRequest)
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
	var req struct {
		Question string `json:"question"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	log.Printf("Chat question received")

	_, current := st.ListAgents()
	ag, ok := st.GetAgent(current)
	if !ok {
		http.Error(w, "current agent not found", http.StatusInternalServerError)
		return
	}

	ctx := context.Background()
	ag.Messages = append(ag.Messages, openai.UserMessage(req.Question))

	// tools from plugins
	tools := []openai.ChatCompletionToolUnionParam{}
	for _, pl := range ag.Plugins {
		tools = append(tools, openai.ChatCompletionFunctionTool(pl.Definition))
	}

	params := openai.ChatCompletionNewParams{
		Model:       ag.Settings.Model,
		Temperature: openai.Float(ag.Settings.Temperature),
		Messages:    ag.Messages,
		Tools:       tools,
	}
	resp, err := client.Chat.Completions.New(ctx, params)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	choice := resp.Choices[0].Message

	var callInfo map[string]string

	if len(choice.ToolCalls) == 0 && strings.TrimSpace(choice.Content) == "" {
		respFB, errFB := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
			Model:       ag.Settings.Model,
			Temperature: openai.Float(ag.Settings.Temperature),
			Messages: append(ag.Messages,
				openai.SystemMessage("Answer directly in plain text. Do not call any tools."),
			),
		})
		if errFB == nil && len(respFB.Choices) > 0 {
			choice = respFB.Choices[0].Message
		}
	}

	if len(choice.ToolCalls) > 0 {
		tc := choice.ToolCalls[0]
		name := tc.Function.Name
		args := tc.Function.Arguments

		pl, ok := ag.Plugins[name]
		if !ok || pl.Tool == nil {
			http.Error(w, fmt.Errorf("plugin %q not loaded", name).Error(), http.StatusInternalServerError)
			return
		}
		result, err := pl.Tool.Call(ctx, args)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		ag.Messages = append(ag.Messages, choice.ToParam())
		ag.Messages = append(ag.Messages, openai.ToolMessage(result, tc.ID))

		resp2, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
			Model:       ag.Settings.Model,
			Temperature: openai.Float(ag.Settings.Temperature),
			Messages:    ag.Messages,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		final := resp2.Choices[0].Message
		ag.Messages = append(ag.Messages, final.ToParam())

		callInfo = map[string]string{"function": name, "args": args, "result": result}
		log.Printf("Chat response (with tool)")
		// save back in-memory state only (persists settings/plugins, not messages)
		_ = st.SetAgent(current, ag)
		_ = json.NewEncoder(w).Encode(map[string]any{"response": final.Content, "toolCall": callInfo})
		return
	}

	ag.Messages = append(ag.Messages, choice.ToParam())
	log.Printf("Chat response")
	_ = st.SetAgent(current, ag) // persists settings/plugins only
	_ = json.NewEncoder(w).Encode(map[string]any{"response": choice.Content})
}
