package chathttp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/openai/openai-go/v2"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/client"
	"github.com/johnjallday/ori-agent/internal/healthhttp"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/orchestration"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/pluginapi"
)

type Handler struct {
	store          store.Store
	clientFactory  *client.Factory
	llmFactory     *llm.Factory
	healthManager  *healthhttp.Manager
	commandHandler *CommandHandler
	orchestrator   *orchestration.Orchestrator
}

func NewHandler(store store.Store, clientFactory *client.Factory) *Handler {
	return &Handler{
		store:          store,
		clientFactory:  clientFactory,
		llmFactory:     nil,
		commandHandler: NewCommandHandler(store),
	}
}

// SetLLMFactory sets the LLM factory
func (h *Handler) SetLLMFactory(factory *llm.Factory) {
	h.llmFactory = factory
}

// SetHealthManager sets the health manager
func (h *Handler) SetHealthManager(manager *healthhttp.Manager) {
	h.healthManager = manager
}

// SetOrchestrator sets the orchestrator
func (h *Handler) SetOrchestrator(orch *orchestration.Orchestrator) {
	h.orchestrator = orch
}

// getClientForAgent returns an OpenAI client using the agent's API key if provided, otherwise the global client
func (h *Handler) getClientForAgent(ag *agent.Agent) openai.Client {
	return h.clientFactory.GetForAgent(ag)
}

// handleClaudeChat handles chat requests for Claude models using the provider system
func (h *Handler) handleClaudeChat(w http.ResponseWriter, r *http.Request, ag *agent.Agent, userMessage string, tools []openai.ChatCompletionToolUnionParam, agentName string, baseCtx context.Context) {
	ctx, cancel := context.WithTimeout(baseCtx, 180*time.Second)
	defer cancel()

	// Get Claude provider
	provider, err := h.llmFactory.GetProvider("claude")
	if err != nil {
		json.NewEncoder(w).Encode(map[string]any{
			"response": fmt.Sprintf("❌ **Error**: Claude provider not available: %v", err),
		})
		return
	}

	// Build simple message list - just system + history + new message
	var messages []llm.Message

	// Add system message if present - just use system prompt from settings
	systemPrompt := ""
	if ag.Settings.SystemPrompt != "" {
		systemPrompt = ag.Settings.SystemPrompt
		messages = append(messages, llm.NewSystemMessage(systemPrompt))
	}

	// Add user message
	messages = append(messages, llm.NewUserMessage(userMessage))

	// Convert tools
	var llmTools []llm.Tool
	for _, tool := range tools {
		if tool.OfFunction != nil {
			// Extract function definition
			fn := tool.OfFunction.Function

			// Get description string
			desc := fn.Description.String()

			// Parameters is already map[string]interface{}
			llmTools = append(llmTools, llm.Tool{
				Name:        fn.Name,
				Description: desc,
				Parameters:  fn.Parameters,
			})
		}
	}

	// Call Claude
	start := time.Now()
	resp, err := provider.Chat(ctx, llm.ChatRequest{
		Model:       ag.Settings.Model,
		Messages:    messages,
		Tools:       llmTools,
		Temperature: ag.Settings.Temperature,
		MaxTokens:   4000,
	})
	if err != nil {
		json.NewEncoder(w).Encode(map[string]any{
			"response": fmt.Sprintf("❌ **Error**: %v", err),
		})
		return
	}

	// Tool-call branch
	if len(resp.ToolCalls) > 0 {
		log.Printf("Claude requested %d tool call(s)", len(resp.ToolCalls))

		// Add the assistant message with tool calls to conversation history
		assistantMsg := llm.NewAssistantMessage(resp.Content)
		messages = append(messages, assistantMsg)

		// Also store in OpenAI format for agent history
		ag.Messages = append(ag.Messages, openai.AssistantMessage(resp.Content))

		// Process ALL tool calls
		var toolResults []map[string]string
		for _, tc := range resp.ToolCalls {
			name := tc.Name
			args := tc.Arguments

			log.Printf("Executing tool: %s with args: %s", name, args)

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

			var result string
			var err error

			if !found || pl.Tool == nil {
				result = fmt.Sprintf("❌ Error: Plugin %q not found or not loaded", name)
				err = fmt.Errorf("plugin not found")
				log.Printf("Plugin %s not found", name)
			} else {
				// Execute tool with timeout (30s for operations like API calls)
				toolCtx, toolCancel := context.WithTimeout(baseCtx, 30*time.Second)
				defer toolCancel()

				// Track plugin call stats
				startTime := time.Now()
				result, err = pl.Tool.Call(toolCtx, args)
				duration := time.Since(startTime)

				// Record call stats in health manager
				if h.healthManager != nil {
					if err != nil {
						h.healthManager.RecordCallFailure(name, duration, err)
					} else {
						h.healthManager.RecordCallSuccess(name, duration)
					}
				}

				// IMPORTANT: Convert error to string result instead of returning HTTP error
				// This prevents conversation history corruption
				if err != nil {
					errorMsg := fmt.Sprintf("❌ Error executing %s: %v", name, err)
					result = errorMsg
					log.Printf("Tool %s failed: %v", name, err)
				} else {
					log.Printf("Tool %s returned: %s", name, result)
				}
			}

			// Add tool result message (even if it's an error)
			messages = append(messages, llm.NewToolMessage(tc.ID, result))

			// Also store in OpenAI format for agent history
			ag.Messages = append(ag.Messages, openai.ToolMessage(result, tc.ID))

			// Store result for final response
			toolResults = append(toolResults, map[string]string{
				"function": name,
				"args":     args,
				"result":   result,
			})
		}

		// Check if any tool result is a structured result or legacy JSON
		var combinedResult string
		hasStructuredResult := false
		var structuredResultData *pluginapi.StructuredResult

		for i, tr := range toolResults {
			result := tr["result"]

			// Check if this is a structured result
			if sr, err := pluginapi.ParseStructuredResult(result); err == nil {
				hasStructuredResult = true
				structuredResultData = sr
				if i > 0 {
					combinedResult += "\n\n"
				}
				combinedResult += result
				continue
			}

			// Legacy: Check if result is valid JSON array
			if strings.HasPrefix(strings.TrimSpace(result), "[") && strings.HasSuffix(strings.TrimSpace(result), "]") {
				var testJSON []interface{}
				if json.Unmarshal([]byte(result), &testJSON) == nil && len(testJSON) > 0 {
					hasStructuredResult = true
				}
			}
			if i > 0 {
				combinedResult += "\n\n"
			}
			combinedResult += result
		}

		// If we have structured or JSON results, return them directly
		if hasStructuredResult {
			ag.Messages = append(ag.Messages, openai.AssistantMessage(combinedResult))
			log.Printf("Claude chat (with structured tool result) in %s", time.Since(start))
			_ = h.store.SetAgent(agentName, ag)

			response := map[string]any{
				"response":  combinedResult,
				"toolCalls": toolResults,
			}

			if structuredResultData != nil {
				response["structured"] = true
				response["displayType"] = string(structuredResultData.DisplayType)
				response["title"] = structuredResultData.Title
				response["description"] = structuredResultData.Description
			}

			_ = json.NewEncoder(w).Encode(response)
			return
		}

		// Ask Claude again with tool results
		resp2, err := provider.Chat(ctx, llm.ChatRequest{
			Model:       ag.Settings.Model,
			Messages:    append(messages, llm.NewSystemMessage("The tool was executed successfully. Simply acknowledge the result without suggesting follow-up actions or next steps. If the tool returned configuration data, settings, or structured information, display that data clearly. For action tools (like opening projects, launching applications), provide only a brief confirmation.")),
			Tools:       llmTools,
			Temperature: ag.Settings.Temperature,
			MaxTokens:   4000,
		})

		if err != nil || resp2 == nil {
			// If second turn fails, return the tool results as best-effort reply
			_ = json.NewEncoder(w).Encode(map[string]any{
				"response":  combinedResult,
				"toolCalls": toolResults,
			})
			return
		}

		// Store final response
		ag.Messages = append(ag.Messages, openai.AssistantMessage(resp2.Content))

		log.Printf("Claude chat (with tool) in %s", time.Since(start))
		_ = h.store.SetAgent(agentName, ag)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"response":  resp2.Content,
			"toolCalls": toolResults,
		})
		return
	}

	// Plain answer path (no tool calls)
	text := strings.TrimSpace(resp.Content)
	if text == "" {
		text = "I couldn't generate a reply just now. Please try again."
	}

	// Store response in OpenAI format for history
	ag.Messages = append(ag.Messages, openai.AssistantMessage(text))

	log.Printf("Claude chat response in %s", time.Since(start))
	h.store.SetAgent(agentName, ag)
	json.NewEncoder(w).Encode(map[string]any{"response": text})
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
func (h *Handler) checkUninitializedPlugins(ag *agent.Agent) []map[string]any {
	var uninitializedPlugins []map[string]any

	for name, plugin := range ag.Plugins {
		// Check if plugin supports initialization
		initProvider, supportsInit := plugin.Tool.(pluginapi.InitializationProvider)
		if !supportsInit {
			// Simplified: Skip plugins that don't support InitializationProvider
			continue
		}

		// Check if plugin is initialized by checking if settings file exists
		_, currentAgent := h.store.ListAgents()
		settingsFilePath := fmt.Sprintf("agents/%s/%s_settings.json", currentAgent, name)
		_, err := os.Stat(settingsFilePath)
		isInitialized := err == nil // If file exists, plugin is initialized

		if !isInitialized {
			// Get required config for this plugin
			configVars := initProvider.GetRequiredConfig()

			// Get fresh definition for description
			def := plugin.Definition
			if plugin.Tool != nil {
				def = plugin.Tool.Definition()
			}

			uninitializedPlugins = append(uninitializedPlugins, map[string]any{
				"name":            name,
				"description":     def.Description.String(),
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

// UploadedFile represents a file uploaded with a chat message
type UploadedFile struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Size    int64  `json:"size"`
	Content string `json:"content"`
}

// ChatHandler handles chat requests
func (h *Handler) ChatHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req struct {
		Question string         `json:"question"`
		Files    []UploadedFile `json:"files,omitempty"`
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

	// If files are attached, prepend their content to the question
	if len(req.Files) > 0 {
		var filesContext strings.Builder
		filesContext.WriteString("Here are the uploaded documents:\n\n")

		for _, file := range req.Files {
			filesContext.WriteString(fmt.Sprintf("=== File: %s ===\n", file.Name))
			filesContext.WriteString(file.Content)
			filesContext.WriteString("\n\n")
		}

		filesContext.WriteString("User's question about the documents:\n")
		filesContext.WriteString(q)

		q = filesContext.String()
	}

	// Handle special commands
	if q == "/help" {
		h.commandHandler.HandleHelp(w, r)
		return
	}
	if q == "/agent" {
		h.commandHandler.HandleAgentStatus(w, r)
		return
	}
	if q == "/agents" {
		h.commandHandler.HandleAgentsList(w, r)
		return
	}
	if q == "/tools" {
		h.commandHandler.HandleToolsList(w, r)
		return
	}
	if strings.HasPrefix(q, "/switch") {
		// Parse the agent name from the command
		parts := strings.Fields(q)
		var agentName string
		if len(parts) > 1 {
			agentName = parts[1]
		}
		h.commandHandler.HandleSwitch(w, r, agentName)
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

	// Check if orchestration is needed for this message
	if h.orchestrator != nil && h.orchestrator.DetectOrchestrationNeed(q) {
		log.Printf("🎯 Orchestration detected for message: %q", q)

		// Identify required roles
		roles := h.orchestrator.IdentifyRequiredRoles(q)

		// Create collaborative task
		task := orchestration.CollaborativeTask{
			Goal:          q,
			RequiredRoles: roles,
			MaxDuration:   10 * time.Minute,
			Context:       map[string]interface{}{},
		}

		// Execute collaborative task
		result, err := h.orchestrator.ExecuteCollaborativeTask(ctx, current, task)
		if err != nil {
			log.Printf("❌ Orchestration failed: %v", err)
			// Fall through to normal chat handling
		} else {
			// Return orchestration result
			log.Printf("✅ Orchestration completed successfully")
			json.NewEncoder(w).Encode(map[string]any{
				"response":     result.FinalOutput,
				"orchestrated": true,
				"workspace_id": result.WorkspaceID,
				"status":       result.Status,
			})
			return
		}
	}

	// Build tools - refresh definitions to get latest dynamic enums (e.g., script lists)
	tools := []openai.ChatCompletionToolUnionParam{}
	for _, pl := range ag.Plugins {
		// Call Tool.Definition() to get fresh definition with dynamic enums
		if pl.Tool != nil {
			freshDef := pl.Tool.Definition()
			tools = append(tools, openai.ChatCompletionFunctionTool(freshDef))
		} else {
			// Fallback to cached definition if tool is not available
			tools = append(tools, openai.ChatCompletionFunctionTool(pl.Definition))
		}
	}

	// Get appropriate client for this agent
	agentClient := h.getClientForAgent(ag)

	// Add system message for better tool usage guidance
	if len(ag.Messages) == 0 {
		var systemPrompt string

		// Use custom system prompt if set, otherwise use default
		if ag.Settings.SystemPrompt != "" {
			systemPrompt = ag.Settings.SystemPrompt
		} else {
			systemPrompt = "You are a helpful assistant with access to various tools. When a user request can be fulfilled by using an available tool, use the tool instead of providing general information. Be concise and direct in your responses."
		}

		// Append available tools list if there are any
		if len(tools) > 0 {
			systemPrompt += " Available tools: "
			var toolNames []string
			for _, pl := range ag.Plugins {
				// Use fresh definition to get correct tool name
				if pl.Tool != nil {
					toolNames = append(toolNames, pl.Tool.Definition().Name)
				} else {
					toolNames = append(toolNames, pl.Definition.Name)
				}
			}
			systemPrompt += strings.Join(toolNames, ", ") + "."
		}

		ag.Messages = append(ag.Messages, openai.SystemMessage(systemPrompt))
	}

	// Prepare and call the model
	ag.Messages = append(ag.Messages, openai.UserMessage(q))

	// Check if this is a Claude model - if so, use provider system
	if strings.HasPrefix(ag.Settings.Model, "claude-") && h.llmFactory != nil {
		// Use Claude provider
		h.handleClaudeChat(w, r, ag, q, tools, current, base)
		return
	}

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

			// Execute tool with its own reasonable timeout (30s for operations like GitHub API calls)
			toolCtx, toolCancel := context.WithTimeout(base, 30*time.Second)
			defer toolCancel()

			// Track plugin call stats
			startTime := time.Now()
			result, err := pl.Tool.Call(toolCtx, args)
			duration := time.Since(startTime)

			// Record call stats in health manager
			if h.healthManager != nil {
				if err != nil {
					h.healthManager.RecordCallFailure(name, duration, err)
				} else {
					h.healthManager.RecordCallSuccess(name, duration)
				}
			}

			// IMPORTANT: Always add a tool response message, even on error
			// This prevents conversation history corruption when tool calls fail
			if err != nil {
				errorMsg := fmt.Sprintf("❌ Error executing %s: %v", name, err)
				result = errorMsg
				log.Printf("Tool %s failed: %v", name, err)
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

		// Check if any tool result is a structured result or legacy JSON - if so, return it directly without LLM processing
		var combinedResult string
		hasStructuredResult := false
		var structuredResultData *pluginapi.StructuredResult

		for i, tr := range toolResults {
			result := tr["result"]

			// First, check if this is a structured result
			if sr, err := pluginapi.ParseStructuredResult(result); err == nil {
				hasStructuredResult = true
				structuredResultData = sr
				// For structured results, we'll use the raw result as-is
				if i > 0 {
					combinedResult += "\n\n"
				}
				combinedResult += result
				continue
			}

			// Legacy: Check if result is valid JSON array
			if strings.HasPrefix(strings.TrimSpace(result), "[") && strings.HasSuffix(strings.TrimSpace(result), "]") {
				var testJSON []interface{}
				if json.Unmarshal([]byte(result), &testJSON) == nil && len(testJSON) > 0 {
					hasStructuredResult = true
				}
			}
			if i > 0 {
				combinedResult += "\n\n"
			}
			combinedResult += result
		}

		// If we have structured or JSON results, return them directly for frontend rendering
		if hasStructuredResult {
			// Add assistant message with the raw result for conversation history
			ag.Messages = append(ag.Messages, openai.AssistantMessage(combinedResult))
			log.Printf("Chat (with structured tool result) in %s", time.Since(start))
			_ = h.store.SetAgent(current, ag)

			response := map[string]any{
				"response":  combinedResult,
				"toolCalls": toolResults,
			}

			// Add structured result metadata if available
			if structuredResultData != nil {
				response["structured"] = true
				response["displayType"] = string(structuredResultData.DisplayType)
				response["title"] = structuredResultData.Title
				response["description"] = structuredResultData.Description
			}

			_ = json.NewEncoder(w).Encode(response)
			return
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