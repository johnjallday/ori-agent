package chathttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/openai/openai-go/v3"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/agentstudio"
	"github.com/johnjallday/ori-agent/internal/client"
	"github.com/johnjallday/ori-agent/internal/healthhttp"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/orchestration"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/pluginapi"
)

// Timeout constants for various operations
const (
	// ChatRequestTimeout is the maximum time allowed for a chat request to complete
	ChatRequestTimeout = 180 * time.Second

	// ToolExecutionTimeout is the maximum time allowed for a single tool execution
	ToolExecutionTimeout = 30 * time.Second

	// ContextTimeout is the timeout for context-based operations
	ContextTimeout = 45 * time.Second

	// StreamFlushInterval is how often to flush streaming responses
	StreamFlushInterval = 100 * time.Millisecond

	// PluginDefinitionCacheTTL is how long to cache plugin definitions
	PluginDefinitionCacheTTL = 5 * time.Minute
)

// pluginDefinitionCache stores cached plugin definitions with expiration
type pluginDefinitionCache struct {
	definition pluginapi.Tool
	cachedAt   time.Time
}

type Handler struct {
	defCache       map[string]*pluginDefinitionCache
	defMu          sync.RWMutex
	store          store.Store
	clientFactory  *client.Factory
	llmFactory     *llm.Factory
	healthManager  *healthhttp.Manager
	commandHandler *CommandHandler
	orchestrator   *orchestration.Orchestrator
	costTracker    *llm.CostTracker
	mcpRegistry    interface {
		GetToolsForServer(string) ([]pluginapi.PluginTool, error)
		GetAllTools() []pluginapi.PluginTool
	}
}

func NewHandler(store store.Store, clientFactory *client.Factory) *Handler {
	return &Handler{
		store:          store,
		clientFactory:  clientFactory,
		llmFactory:     nil,
		commandHandler: NewCommandHandler(store),
		defCache:       make(map[string]*pluginDefinitionCache),
	}
}

// getCachedDefinition retrieves a plugin definition from cache if valid
func (h *Handler) getCachedDefinition(pluginName string) (pluginapi.Tool, bool) {
	h.defMu.RLock()
	defer h.defMu.RUnlock()

	cached, ok := h.defCache[pluginName]
	if !ok {
		return pluginapi.Tool{}, false
	}

	// Check if cache is still valid
	if time.Since(cached.cachedAt) > PluginDefinitionCacheTTL {
		return pluginapi.Tool{}, false
	}

	return cached.definition, true
}

// setCachedDefinition stores a plugin definition in cache
func (h *Handler) setCachedDefinition(pluginName string, definition pluginapi.Tool) {
	h.defMu.Lock()
	defer h.defMu.Unlock()

	h.defCache[pluginName] = &pluginDefinitionCache{
		definition: definition,
		cachedAt:   time.Now(),
	}
}

// writeJSONResponse writes a JSON response and logs errors if encoding fails
func writeJSONResponse(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(data); err != nil {
		logger.Error("Failed to encode JSON response", logger.Fields{"error": err})
		// If we've already started writing, we can't change the status code
		// But at least we've logged the error
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

// SetCostTracker sets the cost tracker
func (h *Handler) SetCostTracker(tracker *llm.CostTracker) {
	h.costTracker = tracker
}

// SetMCPRegistry sets the MCP registry
func (h *Handler) SetMCPRegistry(registry interface {
	GetToolsForServer(string) ([]pluginapi.PluginTool, error)
	GetAllTools() []pluginapi.PluginTool
}) {
	h.mcpRegistry = registry
}

// SetWorkspaceStore sets the workspace store for workspace commands
func (h *Handler) SetWorkspaceStore(ws agentstudio.Store) {
	h.commandHandler.SetWorkspaceStore(ws)
}

// SetShutdownFunc sets the shutdown function for the /exit command
func (h *Handler) SetShutdownFunc(fn func()) {
	h.commandHandler.SetShutdownFunc(fn)
}

// findTool searches for a tool by name in both plugins and MCP servers
func (h *Handler) findTool(ag *agent.Agent, toolName string) (pluginapi.PluginTool, bool) {
	// First check native plugins
	for _, plugin := range ag.Plugins {
		if plugin.Definition.Name == toolName && plugin.Tool != nil {
			return plugin.Tool, true
		}
	}

	// Then check MCP tools
	if h.mcpRegistry != nil && len(ag.MCPServers) > 0 {
		for _, serverName := range ag.MCPServers {
			mcpTools, err := h.mcpRegistry.GetToolsForServer(serverName)
			if err != nil {
				continue
			}
			for _, mcpTool := range mcpTools {
				if mcpTool.Definition().Name == toolName {
					return mcpTool, true
				}
			}
		}
	}

	return nil, false
}

// getClientForAgent returns an OpenAI client using the agent's API key if provided, otherwise the global client
func (h *Handler) getClientForAgent(ag *agent.Agent) openai.Client {
	return h.clientFactory.GetForAgent(ag)
}

// handleClaudeChat handles chat requests for Claude models using the provider system
func (h *Handler) handleClaudeChat(w http.ResponseWriter, r *http.Request, ag *agent.Agent, userMessage string, tools []llm.Tool, agentName string, baseCtx context.Context, files []pluginapi.FileAttachment) {
	ctx, cancel := context.WithTimeout(baseCtx, ChatRequestTimeout)
	defer cancel()

	// Get Claude provider
	provider, err := h.llmFactory.GetProvider("claude")
	if err != nil {
		writeJSONResponse(w, map[string]any{
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

	// Tools are already in generic llm.Tool format, no conversion needed

	// Call Claude
	start := time.Now()
	resp, err := provider.Chat(ctx, llm.ChatRequest{
		Model:       ag.Settings.Model,
		Messages:    messages,
		Tools:       tools,
		Temperature: ag.Settings.Temperature,
		MaxTokens:   4000,
	})
	if err != nil {
		writeJSONResponse(w, map[string]any{
			"response": fmt.Sprintf("❌ **Error**: %v", err),
		})
		return
	}

	// Track usage and cost
	if h.costTracker != nil {
		if err := h.costTracker.TrackUsage("claude", ag.Settings.Model, agentName, resp.Usage, ""); err != nil {
			logger.Warn("Failed to track usage", logger.Fields{"error": err})
		}

		// Track statistics in agent
		h.trackAgentStatistics(ag, resp.Usage.TotalTokens, "claude", ag.Settings.Model)
	}

	// Tool-call branch
	if len(resp.ToolCalls) > 0 {
		logger.Info("Claude requested tool calls", logger.Fields{"count": len(resp.ToolCalls)})

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

			logger.Debug("Executing tool", logger.Fields{"name": name, "args": args})

			// Find tool by name (searches both plugins and MCP tools)
			tool, found := h.findTool(ag, name)

			var result string
			var err error

			if !found {
				result = fmt.Sprintf("❌ Error: Tool %q not found", name)
				logger.Warn("Tool not found", logger.Fields{"tool": name})
			} else {
				// Execute tool with timeout (30s for operations like API calls)
				toolCtx, toolCancel := context.WithTimeout(baseCtx, ToolExecutionTimeout)
				defer toolCancel()

				// Track tool call stats
				startTime := time.Now()

				logger.Info("Claude tool execution starting", logger.Fields{
					"tool":            name,
					"files_available": len(files),
				})

				result, err = ExecuteToolWithFiles(toolCtx, tool, name, args, files)
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
					result = augmentToolExecutionError(name, args, err)
					logger.Error("Tool execution failed", logger.Fields{"tool": name, "error": err})
				} else {
					logger.Info("Tool execution completed", logger.Fields{"tool": name})
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
			logger.Debug("Claude chat with structured tool result completed", logger.Fields{"duration": time.Since(start)})
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

			writeJSONResponse(w, response)
			return
		}

		// Ask Claude again with tool results
		resp2, err := provider.Chat(ctx, llm.ChatRequest{
			Model:       ag.Settings.Model,
			Messages:    append(messages, llm.NewSystemMessage("The tool was executed successfully. Simply acknowledge the result without suggesting follow-up actions or next steps. If the tool returned configuration data, settings, or structured information, display that data clearly. For action tools (like opening projects, launching applications), provide only a brief confirmation.")),
			Tools:       tools,
			Temperature: ag.Settings.Temperature,
			MaxTokens:   4000,
		})

		if err != nil || resp2 == nil {
			// If second turn fails, return the tool results as best-effort reply
			orihttp.WriteJSON(w, map[string]any{
				"response":  combinedResult,
				"toolCalls": toolResults,
			})
			return
		}

		// Track usage and cost for second call
		if h.costTracker != nil && resp2 != nil {
			if err := h.costTracker.TrackUsage("claude", ag.Settings.Model, agentName, resp2.Usage, ""); err != nil {
				logger.Warn("Failed to track usage", logger.Fields{"error": err})
			}
		}

		// Store final response
		ag.Messages = append(ag.Messages, openai.AssistantMessage(resp2.Content))

		logger.Debug("Claude chat with tool completed", logger.Fields{"duration": time.Since(start)})
		_ = h.store.SetAgent(agentName, ag)
		orihttp.WriteJSON(w, map[string]any{
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

	logger.Debug("Claude chat response completed", logger.Fields{"duration": time.Since(start)})
	_ = h.store.SetAgent(agentName, ag)
	writeJSONResponse(w, map[string]any{"response": text})
}

// handleOllamaChat handles chat requests for Ollama models using the provider system
func (h *Handler) handleOllamaChat(w http.ResponseWriter, r *http.Request, ag *agent.Agent, userMessage string, tools []llm.Tool, agentName string, baseCtx context.Context, files []pluginapi.FileAttachment) {
	ctx, cancel := context.WithTimeout(baseCtx, ChatRequestTimeout)
	defer cancel()

	// Get Ollama provider
	provider, err := h.llmFactory.GetProvider("ollama")
	if err != nil {
		orihttp.WriteJSON(w, map[string]any{
			"response": fmt.Sprintf("❌ **Error**: Ollama provider not available: %v", err),
		})
		return
	}

	// Build message list
	var messages []llm.Message

	// Add system message - use custom if set, otherwise use default that emphasizes tool usage
	systemPrompt := ag.Settings.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = "You are a helpful assistant with access to tools. When you use a tool and receive results, report those results directly to the user. Be concise and accurate."
	}
	messages = append(messages, llm.NewSystemMessage(systemPrompt))

	// Add user message
	messages = append(messages, llm.NewUserMessage(userMessage))

	// Tools are already in generic llm.Tool format, no conversion needed

	// Call Ollama
	start := time.Now()
	resp, err := provider.Chat(ctx, llm.ChatRequest{
		Model:       ag.Settings.Model,
		Messages:    messages,
		Tools:       tools,
		Temperature: ag.Settings.Temperature,
		MaxTokens:   4000,
	})
	if err != nil {
		writeJSONResponse(w, map[string]any{
			"response": fmt.Sprintf("❌ **Error**: %v", err),
		})
		return
	}

	// Track usage (Ollama is free/local, so no cost tracking needed)
	logger.Debug("Ollama response received", logger.Fields{"duration": time.Since(start)})

	// Track statistics in agent (with zero cost for Ollama)
	if resp.Usage.TotalTokens > 0 {
		h.trackAgentStatistics(ag, resp.Usage.TotalTokens, "ollama", ag.Settings.Model)
	}

	// Tool-call branch (similar to Claude handler)
	if len(resp.ToolCalls) > 0 {
		logger.Info("Ollama requested tool calls", logger.Fields{"count": len(resp.ToolCalls)})

		// Add assistant message to history WITH tool calls (important for Ollama protocol)
		assistantMsg := llm.NewAssistantMessage(resp.Content)
		assistantMsg.ToolCalls = resp.ToolCalls
		messages = append(messages, assistantMsg)
		ag.Messages = append(ag.Messages, openai.AssistantMessage(resp.Content))

		// Process tool calls
		var toolResults []map[string]string
		for _, tc := range resp.ToolCalls {
			logger.Debug("Looking for tool", logger.Fields{"name": tc.Name, "args": tc.Arguments})
			tool, found := h.findTool(ag, tc.Name)

			var result string
			if !found {
				logger.Warn("Tool not found", logger.Fields{"tool": tc.Name})
				result = fmt.Sprintf("❌ Error: Tool %q not found", tc.Name)
			} else {
				logger.Debug("Tool found", logger.Fields{"tool": tc.Name})
				toolCtx, toolCancel := context.WithTimeout(baseCtx, ToolExecutionTimeout)
				defer toolCancel()

				startTime := time.Now()

				result, err = ExecuteToolWithFilesDebug(toolCtx, tool, tc.Name, tc.Arguments, files)
				duration := time.Since(startTime)

				if h.healthManager != nil {
					if err != nil {
						h.healthManager.RecordCallFailure(tc.Name, duration, err)
					} else {
						h.healthManager.RecordCallSuccess(tc.Name, duration)
					}
				}

				if err != nil {
					logger.Error("Tool execution failed", logger.Fields{"tool": tc.Name, "error": err})
					result = augmentToolExecutionError(tc.Name, tc.Arguments, err)
				} else {
					logger.Debug("Tool executed successfully", logger.Fields{"tool": tc.Name, "result": result})
				}
			}

			messages = append(messages, llm.NewToolMessage(tc.ID, result))
			ag.Messages = append(ag.Messages, openai.ToolMessage(result, tc.ID))

			toolResults = append(toolResults, map[string]string{
				"function": tc.Name,
				"args":     tc.Arguments,
				"result":   result,
			})
		}

		logger.Debug("Sending tool results back to LLM", logger.Fields{"message_count": len(messages)})

		// Get final response after tool execution
		// IMPORTANT: Must include Tools array again for Ollama to understand the tool calling context
		finalResp, err := provider.Chat(ctx, llm.ChatRequest{
			Model:       ag.Settings.Model,
			Messages:    messages,
			Tools:       tools, // Include tools in follow-up request
			Temperature: ag.Settings.Temperature,
			MaxTokens:   4000,
		})
		if err != nil {
			logger.Error("Error getting final response from LLM", logger.Fields{"error": err})
			orihttp.WriteJSON(w, map[string]any{
				"response": fmt.Sprintf("❌ **Error**: %v", err),
			})
			return
		}

		finalText := strings.TrimSpace(finalResp.Content)
		if finalText == "" && len(toolResults) > 0 {
			var b strings.Builder
			for i, tr := range toolResults {
				if i > 0 {
					b.WriteString("\n\n")
				}
				b.WriteString(fmt.Sprintf("**%s**\n\n%s", tr["function"], tr["result"]))
			}
			finalText = b.String()
		}

		logger.Debug("Final response from LLM", logger.Fields{"content": finalText})

		ag.Messages = append(ag.Messages, openai.AssistantMessage(finalText))
		_ = h.store.SetAgent(agentName, ag)

		orihttp.WriteJSON(w, map[string]any{
			"response":  finalText,
			"toolCalls": toolResults,
		})
		return
	}

	// No tool calls - direct response
	ag.Messages = append(ag.Messages, openai.AssistantMessage(resp.Content))
	_ = h.store.SetAgent(agentName, ag)
	writeJSONResponse(w, map[string]any{"response": resp.Content})
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

			// Skip if plugin has no required configuration (e.g., simple plugins like math)
			// This handles the case where RPC clients always implement InitializationProvider
			// but the underlying plugin doesn't actually need configuration
			if len(configVars) == 0 {
				continue
			}

			// Get fresh definition for description
			def := plugin.Definition
			if plugin.Tool != nil {
				def = plugin.Tool.Definition()
			}

			uninitializedPlugins = append(uninitializedPlugins, map[string]any{
				"name":            name,
				"description":     def.Description,
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
		prompt.WriteString("🔧 **Plugin Setup Required**\n\n")
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
		prompt.WriteString("🔧 **Plugin Setup Required**\n\n")
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
		Question  string         `json:"question"`
		AgentName string         `json:"agent_name,omitempty"` // Allow specifying target agent
		Files     []UploadedFile `json:"files,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if err := orihttp.RespondBadRequest(w, err.Error()); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}
	q := strings.TrimSpace(req.Question)
	if q == "" {
		if err := orihttp.RespondBadRequest(w, "empty question"); err != nil {
			logger.

				// Debug: Log received files
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	logger.Info("Chat request received", logger.Fields{
		"question":   q[:min(50, len(q))],
		"file_count": len(req.Files),
	})
	for i, f := range req.Files {
		logger.Info("Received file", logger.Fields{
			"index": i,
			"name":  f.Name,
			"type":  f.Type,
			"size":  f.Size,
		})
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
	if q == "/exit" {
		h.commandHandler.HandleExit(w, r)
		return
	}
	if q == "/version" {
		h.commandHandler.HandleVersion(w, r)
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
	if strings.HasPrefix(q, "/workspace") {
		// Parse args after "/workspace"
		args := strings.TrimPrefix(q, "/workspace")
		h.commandHandler.HandleWorkspace(w, r, args)
		return
	}
	if strings.HasPrefix(q, "/tool ") {
		// Direct tool execution - bypass LLM decision-making
		cmd, err := parseDirectToolCommand(q)
		if err != nil {
			// Return parsing error as response
			orihttp.WriteJSON(w, map[string]any{
				"response":         fmt.Sprintf("❌ **Invalid command**: %v\n\nFormat: `/tool <tool_name> {\"key\": \"value\"}`\nExample: `/tool math {\"operation\": \"add\", \"a\": 5, \"b\": 3}`", err),
				"direct_tool_call": true,
				"success":          false,
			})
			return
		}

		// Attach files to the command if present
		if len(req.Files) > 0 {
			cmd.Files = ConvertUploadedFilesToAttachments(req.Files)
		}

		// Load agent
		ag, current, ok := store.GetCurrentAgent(h.store)
		if !ok {
			if err := orihttp.RespondInternalError(w, "current agent not found"); err != nil {
				logger.Error("Failed to write response", logger.Fields{"error": err})
			}
			return
		}

		result := h.executeDirectTool(r.Context(), ag, cmd)

		// Add to conversation history for context
		ag.Messages = append(ag.Messages, openai.UserMessage(q))
		ag.Messages = append(ag.Messages, openai.AssistantMessage(result.Result))
		_ = h.store.SetAgent(current, ag)

		// Return formatted response
		response := formatDirectToolResponse(result)
		writeJSONResponse(w, response)
		return
	}

	logger.Debug("Chat question received", logger.Fields{"question": q})
	// Context with timeout per request (prevents indefinite hang)
	base := r.Context()
	ctx, cancel := context.WithTimeout(base, ContextTimeout)
	defer cancel()

	// Load agent - use agent_name from request if provided, otherwise use current agent
	var current string
	if req.AgentName != "" {
		current = req.AgentName
	} else {
		// Fallback to current agent from store
		names, cur := h.store.ListAgents()
		current = cur
		if current == "" && len(names) > 0 {
			current = names[0] // fallback to first available agent
		}
	}

	ag, ok := h.store.GetAgent(current)
	if !ok {
		if err := orihttp.RespondInternalError(w, fmt.Sprintf("agent '%s' not found", current)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	logger.Debug("Agent MCP servers loaded", logger.Fields{"agent": current, "server_count": len(ag.MCPServers), "servers": ag.MCPServers})

	// Check for uninitialized plugins before proceeding with chat
	uninitializedPlugins := h.checkUninitializedPlugins(ag)
	if len(uninitializedPlugins) > 0 {
		initPrompt := h.generateInitializationPrompt(uninitializedPlugins)
		orihttp.WriteJSON(w, map[string]any{
			"response":                initPrompt,
			"requires_initialization": true,
			"uninitialized_plugins":   uninitializedPlugins,
		})
		return
	}

	// Check if orchestration is needed for this message
	if h.orchestrator != nil && h.orchestrator.DetectOrchestrationNeed(q) {
		logger.Info("Orchestration detected for message", logger.Fields{"message": q})

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
			logger.Error("Orchestration failed", logger.Fields{"error": err})
			// Fall through to normal chat handling
		} else {
			// Return orchestration result
			logger.Info("Orchestration completed successfully", logger.Fields{})
			orihttp.WriteJSON(w, map[string]any{
				"response":     result.FinalOutput,
				"orchestrated": true,
				"studio_id":    result.WorkspaceID,
				"status":       result.Status,
			})
			return
		}
	}

	// Build tools - refresh definitions to get latest dynamic enums (e.g., script lists)
	tools := []llm.Tool{}

	// Add native plugin tools
	for pluginName, pl := range ag.Plugins {
		var def pluginapi.Tool

		// Try to get from cache first
		if cachedDef, found := h.getCachedDefinition(pluginName); found {
			def = cachedDef
		} else if pl.Tool != nil {
			// Call Tool.Definition() to get fresh definition
			def = pl.Tool.Definition()
			// Cache it for future requests
			h.setCachedDefinition(pluginName, def)
		} else {
			// Fallback to stored definition if tool is not available
			def = pl.Definition
		}

		// Convert pluginapi.Tool to llm.Tool
		tools = append(tools, llm.Tool{
			Name:        def.Name,
			Description: def.Description,
			Parameters:  def.Parameters,
		})
	}

	// Add MCP tools for enabled servers
	logger.Debug("Checking MCP servers for agent", logger.Fields{"agent": current, "server_count": len(ag.MCPServers), "servers": ag.MCPServers})
	if h.mcpRegistry != nil && len(ag.MCPServers) > 0 {
		logger.Debug("Loading MCP tools for agent", logger.Fields{"agent": current})
		for _, serverName := range ag.MCPServers {
			logger.Debug("Attempting to get tools for MCP server", logger.Fields{"server": serverName})
			mcpTools, err := h.mcpRegistry.GetToolsForServer(serverName)
			if err != nil {
				logger.Warn("Failed to get MCP tools for server", logger.Fields{"server": serverName, "error": err})
				continue
			}
			for _, mcpTool := range mcpTools {
				mcpDef := mcpTool.Definition()
				tools = append(tools, llm.Tool{
					Name:        mcpDef.Name,
					Description: mcpDef.Description,
					Parameters:  mcpDef.Parameters,
				})
			}
			logger.Debug("Added MCP tools from server", logger.Fields{"count": len(mcpTools), "server": serverName})
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

	// Convert uploaded files to FileAttachments for tool execution
	fileAttachments := ConvertUploadedFilesToAttachments(req.Files)
	logger.Info("Converted files for tool execution", logger.Fields{
		"original_count":  len(req.Files),
		"converted_count": len(fileAttachments),
	})

	// Check if this is a Claude model - if so, use provider system
	if strings.HasPrefix(ag.Settings.Model, "claude-") && h.llmFactory != nil {
		// Use Claude provider
		h.handleClaudeChat(w, r, ag, q, tools, current, base, fileAttachments)
		return
	}

	// Check if Ollama has this model - route to Ollama provider (dynamic detection)
	if h.llmFactory != nil {
		if ollamaProvider, err := h.llmFactory.GetProvider("ollama"); err == nil {
			if ollamaProv, ok := ollamaProvider.(*llm.OllamaProvider); ok {
				if ollamaProv.HasModel(ag.Settings.Model) {
					logger.Info("Model found in Ollama, routing to Ollama provider", logger.Fields{"model": ag.Settings.Model})
					h.handleOllamaChat(w, r, ag, q, tools, current, base, fileAttachments)
					return
				}
			}
		}
	}

	// OpenAI models require an API key; return a clear error if none is configured.
	if h.clientFactory != nil && !h.clientFactory.HasKeyForAgent(ag) {
		writeJSONResponse(w, map[string]any{
			"response": "❌ **Error**: OpenAI API key is not configured. Set `OPENAI_API_KEY` for the server process, or add an API key in the app Settings.",
		})
		return
	}

	// Handle OpenAI models
	h.handleOpenAIChat(w, ag, q, tools, current, base, fileAttachments, agentClient)
}

// trackAgentStatistics records message and token usage in agent statistics
func (h *Handler) trackAgentStatistics(ag *agent.Agent, tokenCount int, provider string, model string) {
	// Initialize statistics if needed
	ag.InitializeStatistics()

	// Calculate cost estimate (this is a simple estimation, actual costs tracked by cost tracker)
	var costPerToken float64
	switch provider {
	case "ollama":
		costPerToken = 0.0 // Ollama is free/local
	default:
		switch {
		case strings.Contains(model, "gpt-4"):
			costPerToken = 0.00003 // ~$0.03 per 1K tokens (average of input/output)
		case strings.Contains(model, "gpt-3.5"):
			costPerToken = 0.000002 // ~$0.002 per 1K tokens
		case strings.Contains(model, "claude"):
			costPerToken = 0.00003 // ~$0.03 per 1K tokens (average)
		default:
			costPerToken = 0.00001 // Default estimate
		}
	}

	estimatedCost := float64(tokenCount) * costPerToken

	// Record the message with tokens and cost
	ag.Statistics.RecordMessage(tokenCount, estimatedCost)

	logger.Debug("Statistics updated", logger.Fields{
		"messages":   ag.Statistics.MessageCount,
		"tokens":     ag.Statistics.TokenUsage,
		"total_cost": ag.Statistics.TotalCost,
	})
}
