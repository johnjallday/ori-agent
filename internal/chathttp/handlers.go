package chathttp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/openai/openai-go/v2"

	"github.com/johnjallday/dolphin-agent/internal/client"
	"github.com/johnjallday/dolphin-agent/internal/store"
	"github.com/johnjallday/dolphin-agent/internal/types"
	"github.com/johnjallday/dolphin-agent/pluginapi"
)

type Handler struct {
	store         store.Store
	clientFactory *client.Factory
	commandHandler *CommandHandler
}

func NewHandler(store store.Store, clientFactory *client.Factory) *Handler {
	return &Handler{
		store:         store,
		clientFactory: clientFactory,
		commandHandler: NewCommandHandler(store),
	}
}

// getClientForAgent returns an OpenAI client using the agent's API key if provided, otherwise the global client
func (h *Handler) getClientForAgent(ag *types.Agent) openai.Client {
	return h.clientFactory.GetForAgent(ag)
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


// checkUninitializedPlugins checks which plugins need initialization
func (h *Handler) checkUninitializedPlugins(agent *types.Agent) []map[string]any {
	var uninitializedPlugins []map[string]any

	for name, plugin := range agent.Plugins {
		// Check if plugin supports initialization
		initProvider, supportsInit := plugin.Tool.(pluginapi.InitializationProvider)
		if !supportsInit {
			// Simplified: Skip plugins that don't support InitializationProvider
			continue
		}

		// Simplified: assume plugins with InitializationProvider need initialization
		isInitialized := false

		if !isInitialized {
			// Get required config for this plugin
			configVars := initProvider.GetRequiredConfig()
			uninitializedPlugins = append(uninitializedPlugins, map[string]any{
				"name":            name,
				"description":     plugin.Definition.Description.String(),
				"required_config": configVars,
			})
		}
	}
	return uninitializedPlugins
}

// generateInitializationPrompt creates a user-friendly prompt for plugin initialization
func (h *Handler) generateInitializationPrompt(uninitializedPlugins []map[string]any) string {
	if len(uninitializedPlugins) == 0 {
		return ""
	}

	var prompt strings.Builder

	if len(uninitializedPlugins) == 1 {
		plugin := uninitializedPlugins[0]
		prompt.WriteString(fmt.Sprintf("🔧 **Plugin Setup Required**\n\n"))
		prompt.WriteString(fmt.Sprintf("The **%s** plugin needs to be configured before you can use it.\n\n", plugin["name"]))
		prompt.WriteString(fmt.Sprintf("**Description:** %s\n\n", plugin["description"]))

		if configVars, ok := plugin["required_config"].([]pluginapi.ConfigVariable); ok && len(configVars) > 0 {
			prompt.WriteString("**Required configuration:**\n")
			for _, configVar := range configVars {
				prompt.WriteString(fmt.Sprintf("- **%s** (%s): %s\n", configVar.Name, configVar.Type, configVar.Description))
			}
		}

		prompt.WriteString("\n**Please click the 'Configure Plugin' button to set up this plugin.**")
	} else {
		prompt.WriteString(fmt.Sprintf("🔧 **Plugin Setup Required**\n\n"))
		prompt.WriteString(fmt.Sprintf("You have %d plugins that need to be configured before you can use them:\n\n", len(uninitializedPlugins)))

		for i, plugin := range uninitializedPlugins {
			prompt.WriteString(fmt.Sprintf("%d. **%s** - %s\n", i+1, plugin["name"], plugin["description"]))
		}

		prompt.WriteString("\n**Please configure these plugins to unlock their full functionality.**")
	}

	return prompt.String()
}

// ChatHandler handles chat requests
func (h *Handler) ChatHandler(w http.ResponseWriter, r *http.Request) {
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
		h.commandHandler.HandleAgentStatus(w, r)
		return
	}
	if q == "/tools" {
		h.commandHandler.HandleToolsList(w, r)
		return
	}

	log.Printf("Chat question received: %q", q)
	// Context with timeout per request (prevents indefinite hang)
	base := r.Context()
	ctx, cancel := context.WithTimeout(base, 45*time.Second)
	defer cancel()

	// Load agent - for single agent stores, use the current agent name stored in the config
	names, current := h.store.ListAgents()
	if current == "" && len(names) > 0 {
		current = names[0] // fallback to first available agent
	}
	ag, ok := h.store.GetAgent(current)
	if !ok {
		http.Error(w, "current agent not found", http.StatusInternalServerError)
		return
	}

	// Check for uninitialized plugins before proceeding with chat
	uninitializedPlugins := h.checkUninitializedPlugins(ag)
	if len(uninitializedPlugins) > 0 {
		initPrompt := h.generateInitializationPrompt(uninitializedPlugins)
		json.NewEncoder(w).Encode(map[string]any{
			"response":                initPrompt,
			"requires_initialization": true,
			"uninitialized_plugins":   uninitializedPlugins,
		})
		return
	}

	// Build tools
	tools := []openai.ChatCompletionToolUnionParam{}
	for _, pl := range ag.Plugins {
		tools = append(tools, openai.ChatCompletionFunctionTool(pl.Definition))
	}

	// Get appropriate client for this agent
	agentClient := h.getClientForAgent(ag)

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
			"response": "I couldn't generate a reply just now. Please try again.",
		})
		return
	}
	choice := resp.Choices[0].Message

	// Fallback if model answered with an empty assistant message and no tool calls
	if len(choice.ToolCalls) == 0 && strings.TrimSpace(choice.Content) == "" {
		// fresh timeout for fallback, so we don't reuse a nearly-expired ctx
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
				openai.SystemMessage("The tool was executed successfully. Simply acknowledge the result without suggesting follow-up actions or next steps. If the tool returned configuration data, settings, or structured information, display that data clearly. For action tools (like opening projects, launching applications), provide only a brief confirmation."),
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
		_ = h.store.SetAgent(current, ag) // persists settings/plugins only
		_ = json.NewEncoder(w).Encode(map[string]any{
			"response":  final.Content,
			"toolCalls": toolResults,
		})
		return
	}

	// Plain answer path
	text := strings.TrimSpace(choice.Content)
	if text == "" {
		text = "I couldn't generate a reply just now. Please try again."
	}
	ag.Messages = append(ag.Messages, choice.ToParam())

	log.Printf("Chat response in %s: %q", time.Since(start), text)
	_ = h.store.SetAgent(current, ag) // persists settings/plugins only
	_ = json.NewEncoder(w).Encode(map[string]any{"response": text})
}