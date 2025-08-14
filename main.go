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
	"path/filepath"
	"plugin"
	"strings"
	"sync"

	_ "embed"

	"github.com/johnjallday/dolphin-agent/pluginapi"
	"github.com/openai/openai-go/v2"
	"github.com/openai/openai-go/v2/option"
)

func main() {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		log.Fatal("OPENAI_API_KEY environment variable is required")
	}
	client = openai.NewClient(option.WithAPIKey(apiKey))

	// load persisted agents or initialize default
	loadAgents()
	// load plugin registry
	if err := json.Unmarshal([]byte(pluginRegistryJSON), &pluginRegistry); err != nil {
		log.Printf("failed to parse plugin registry: %v", err)
	}
	if _, ok := agents[currentAgent]; !ok {
		agents[currentAgent] = &Agent{Settings: defaultSettings, Plugins: make(map[string]LoadedPlugin)}
	}

	http.HandleFunc("/", serveIndex)
	http.HandleFunc("/api/plugins", pluginsHandler)
	http.HandleFunc("/api/plugin-registry", pluginRegistryHandler)
	http.HandleFunc("/api/agents", agentsHandler)
	http.HandleFunc("/api/settings", settingsHandler)
	http.HandleFunc("/api/chat", chatHandler)

	addr := ":8080"
	log.Printf("Listening on http://localhost%s", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}

// pluginRegistryHandler serves a central registry of available plugins
func pluginRegistryHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		json.NewEncoder(w).Encode(pluginRegistry)
	case http.MethodPost:
		var req struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// find registry entry
		var entryPath string
		var found bool
		for _, e := range pluginRegistry.Plugins {
			if e.Name == req.Name {
				entryPath = e.Path
				found = true
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
		mu.Lock()
		ag := agents[currentAgent]
		ag.Plugins[def.Name] = LoadedPlugin{Tool: tool, Definition: def, Path: entryPath}
		mu.Unlock()
		saveAgents()
		w.WriteHeader(http.StatusOK)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// serveIndex serves the main HTML page
func serveIndex(w http.ResponseWriter, r *http.Request) {
	tmpl.Execute(w, nil)
}

// pluginsHandler handles loading, listing, and unloading plugins
func pluginsHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		mu.Lock()
		ag := agents[currentAgent]
		plist := make([]map[string]string, 0, len(ag.Plugins))
		for name, pl := range ag.Plugins {
			plist = append(plist, map[string]string{
				"name":        name,
				"description": pl.Definition.Description.String(),
			})
		}
		mu.Unlock()
		json.NewEncoder(w).Encode(map[string]interface{}{"plugins": plist})
	case http.MethodPost:
		r.ParseMultipartForm(10 << 20)
		file, header, err := r.FormFile("plugin")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer file.Close()
		tmpFile := filepath.Join(os.TempDir(), header.Filename)
		out, err := os.Create(tmpFile)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		io.Copy(out, file)
		out.Close()
		p, err := plugin.Open(tmpFile)
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
		mu.Lock()
		ag := agents[currentAgent]
		ag.Plugins[def.Name] = LoadedPlugin{Tool: tool, Definition: def, Path: tmpFile}
		mu.Unlock()
		saveAgents()
		w.WriteHeader(http.StatusOK)
	case http.MethodDelete:
		name := r.URL.Query().Get("name")
		mu.Lock()
		ag := agents[currentAgent]
		delete(ag.Plugins, name)
		mu.Unlock()
		saveAgents()
		w.WriteHeader(http.StatusOK)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// agentsHandler manages listing, creating, switching, and deleting agents.
func agentsHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		mu.Lock()
		names := make([]string, 0, len(agents))
		for name := range agents {
			names = append(names, name)
		}
		cur := currentAgent
		mu.Unlock()
		json.NewEncoder(w).Encode(map[string]interface{}{"agents": names, "current": cur})
	case http.MethodPost:
		var req struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		if _, exists := agents[req.Name]; !exists {
			agents[req.Name] = &Agent{
				Settings: defaultSettings,
				Plugins:  make(map[string]LoadedPlugin),
			}
		}
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	case http.MethodPut:
		name := r.URL.Query().Get("name")
		mu.Lock()
		if _, exists := agents[name]; exists {
			currentAgent = name
		}
		mu.Unlock()
		saveAgents()
		w.WriteHeader(http.StatusOK)
	case http.MethodDelete:
		name := r.URL.Query().Get("name")
		mu.Lock()
		delete(agents, name)
		if currentAgent == name {
			currentAgent = ""
		}
		mu.Unlock()
		saveAgents()
		w.WriteHeader(http.StatusOK)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// settingsHandler exposes GET/POST for current agent settings (model, temperature).
func settingsHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		mu.Lock()
		s := agents[currentAgent].Settings
		mu.Unlock()
		json.NewEncoder(w).Encode(s)
	case http.MethodPost:
		var s Settings
		if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		agents[currentAgent].Settings = s
		mu.Unlock()
		saveAgents()
		w.WriteHeader(http.StatusOK)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// chatHandler handles chat requests, invokes model and tool plugins
func chatHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Question string `json:"question"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	log.Printf("Chat question: %s", req.Question)
	// ensure current agent exists (avoid nil pointer if deleted)
	mu.Lock()
	if _, ok := agents[currentAgent]; !ok {
		agents[currentAgent] = &Agent{Settings: defaultSettings, Plugins: make(map[string]LoadedPlugin)}
	}
	mu.Unlock()
	ctx := context.Background()

	// Add user message
	agents[currentAgent].Messages = append(agents[currentAgent].Messages, openai.UserMessage(req.Question))

	// Prepare function tools from plugins
	mu.Lock()
	tools := []openai.ChatCompletionToolUnionParam{}
	ag := agents[currentAgent]
	for _, pl := range ag.Plugins {
		tools = append(tools, openai.ChatCompletionFunctionTool(pl.Definition))
	}
	mu.Unlock()

	mu.Lock()
	mdl := agents[currentAgent].Settings.Model
	temp := agents[currentAgent].Settings.Temperature
	mu.Unlock()
	params := openai.ChatCompletionNewParams{
		Model:       mdl,
		Temperature: openai.Float(temp),
		Messages:    agents[currentAgent].Messages,
		Tools:       tools,
	}
	resp, err := client.Chat.Completions.New(ctx, params)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	choice := resp.Choices[0].Message

	var callInfo map[string]string

	// after getting `resp` and `choice := resp.Choices[0].Message`
	if len(choice.ToolCalls) == 0 && strings.TrimSpace(choice.Content) == "" {
		// Ask the model to answer directly in text (no tools)
		fallbackParams := openai.ChatCompletionNewParams{
			Model:       mdl,
			Temperature: openai.Float(temp),
			Messages: append(agents[currentAgent].Messages,
				openai.SystemMessage("Answer directly in plain text. Do not call any tools."),
			),
		}
		respFB, errFB := client.Chat.Completions.New(ctx, fallbackParams)
		if errFB == nil && len(respFB.Choices) > 0 {
			choice = respFB.Choices[0].Message
		}
	}
	// Handle tool call if any
	if len(choice.ToolCalls) > 0 {
		tc := choice.ToolCalls[0]
		name := tc.Function.Name
		args := tc.Function.Arguments
		mu.Lock()
		pl := agents[currentAgent].Plugins[name]
		mu.Unlock()
		if pl.Tool == nil {
			http.Error(w, fmt.Sprintf("plugin %q not loaded", name), http.StatusInternalServerError)
			return
		}
		result, err := pl.Tool.Call(ctx, args)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Append tool messages
		agent := agents[currentAgent]
		agent.Messages = append(agent.Messages, choice.ToParam())
		agent.Messages = append(agent.Messages, openai.ToolMessage(result, tc.ID))
		// Call model again with updated history and settings
		mu.Lock()
		resp2, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
			Model:       agents[currentAgent].Settings.Model,
			Temperature: openai.Float(agents[currentAgent].Settings.Temperature),
			Messages:    agents[currentAgent].Messages,
		})
		mu.Unlock()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		final := resp2.Choices[0].Message
		agent.Messages = append(agent.Messages, final.ToParam())
		callInfo = map[string]string{"function": name, "args": args, "result": result}
		//saveAgents()
		log.Printf("Chat response (with tool): %s", final.Content)
		json.NewEncoder(w).Encode(map[string]interface{}{"response": final.Content, "toolCall": callInfo})
		return
	}

	// No tool call
	agents[currentAgent].Messages = append(agents[currentAgent].Messages, choice.ToParam())
	//saveAgents()
	//saveAgents()
	log.Printf("Chat response: %s", choice.Content)
	json.NewEncoder(w).Encode(map[string]interface{}{"response": choice.Content})
}

var (
	client       openai.Client
	mu           sync.Mutex
	agents       = make(map[string]*Agent)
	currentAgent = "default"

	//go:embed static/index.html
	indexHTML string
	tmpl      = template.Must(template.New("index").Parse(indexHTML))
)

//go:embed static/plugin_registry.json
var pluginRegistryJSON string

// pluginRegistry holds a list of available plugins from registry
var pluginRegistry struct {
	Plugins []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Path        string `json:"path"`
	} `json:"plugins"`
}

var defaultSettings = Settings{Model: openai.ChatModelGPT4oMini, Temperature: 0.7}

// LoadedPlugin holds a plugin tool and its definition.
type LoadedPlugin struct {
	Tool       pluginapi.Tool `json:"-"`
	Definition openai.FunctionDefinitionParam
	Path       string
}

// Settings holds model and temperature for an agent.
type Settings struct {
	Model       string  `json:"model"`
	Temperature float64 `json:"temperature"`
}

// Agent represents a named chatbot instance with its own settings, plugins, and history.
type Agent struct {
	Settings Settings
	Plugins  map[string]LoadedPlugin
	Messages []openai.ChatCompletionMessageParamUnion `json:"-"`
}

// persistence store file
const agentStoreFile = "agents.json"

// loadAgents loads agents map and currentAgent from disk, if present.
func loadAgents() {
	data, err := os.ReadFile(agentStoreFile)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("failed to read agents file: %v", err)
		}
		return
	}
	var store struct {
		Agents  map[string]*Agent `json:"agents"`
		Current string            `json:"current"`
	}
	if err := json.Unmarshal(data, &store); err != nil {
		log.Printf("failed to unmarshal agents: %v", err)
		return
	}
	mu.Lock()
	agents = store.Agents
	currentAgent = store.Current
	mu.Unlock()

	// reload persisted plugins for each agent
	for _, ag := range agents {
		for name, pl := range ag.Plugins {
			if pl.Path == "" {
				continue
			}
			p, err := plugin.Open(pl.Path)
			if err != nil {
				log.Printf("failed to open plugin %s: %v", pl.Path, err)
				delete(ag.Plugins, name)
				continue
			}
			sym, err := p.Lookup("Tool")
			if err != nil {
				log.Printf("failed to lookup Tool symbol in plugin %s: %v", pl.Path, err)
				delete(ag.Plugins, name)
				continue
			}
			tool, ok := sym.(pluginapi.Tool)
			if !ok {
				log.Printf("invalid plugin type for plugin %s", pl.Path)
				delete(ag.Plugins, name)
				continue
			}
			ag.Plugins[name] = LoadedPlugin{Tool: tool, Definition: pl.Definition, Path: pl.Path}
		}
	}
}

// saveAgents persists the agents map and currentAgent to disk.
func saveAgents() {
	mu.Lock()
	store := struct {
		Agents  map[string]*Agent `json:"agents"`
		Current string            `json:"current"`
	}{Agents: agents, Current: currentAgent}
	mu.Unlock()
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		log.Printf("failed to marshal agents: %v", err)
		return
	}
	if err := os.WriteFile(agentStoreFile, data, 0644); err != nil {
		log.Printf("failed to write agents file: %v", err)
	}
}
