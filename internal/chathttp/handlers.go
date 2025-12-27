package chathttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
	"github.com/johnjallday/ori-agent/internal/session"
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
	sessionStore   session.HybridStore
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
	if encErr := json.NewEncoder(w).Encode(data); encErr != nil {
		logger.Error("Failed to encode JSON response", logger.Fields{"error": encErr})
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

// SetSessionStore sets the session store for storing chat messages
func (h *Handler) SetSessionStore(store session.HybridStore) {
	h.sessionStore = store
}

// storeMessageInSession stores a message in the session if session ID is provided
func (h *Handler) storeMessageInSession(ctx context.Context, sessionID, role, content string) {
	if h.sessionStore == nil || sessionID == "" {
		return
	}

	msg := &session.Message{
		Role:    session.MessageRole(role),
		Content: content,
	}

	if err := h.sessionStore.AddMessage(ctx, sessionID, msg); err != nil {
		logger.Warn("Failed to store message in session", logger.Fields{
			"session_id": sessionID,
			"role":       role,
			"error":      err,
		})
	}
}

// getSessionID extracts the session ID from request header
func (h *Handler) getSessionID(r *http.Request) string {
	return r.Header.Get("X-Session-ID")
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
		if respErr := orihttp.RespondBadRequest(w, err.Error()); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}
	q := strings.TrimSpace(req.Question)
	if q == "" {
		if respErr := orihttp.RespondBadRequest(w, "empty question"); respErr != nil {
			logger.
				// Debug: Log received files
				Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}

	// Get session ID from header for multi-tab support
	sessionID := h.getSessionID(r)

	logger.Info("Chat request received", logger.Fields{
		"question":   q[:min(50, len(q))],
		"file_count": len(req.Files),
		"session_id": sessionID,
	})
	for i, f := range req.Files {
		logger.Info("Received file", logger.Fields{
			"index": i,
			"name":  f.Name,
			"type":  f.Type,
			"size":  f.Size,
		})
	}

	// Separate image files from text files for proper API handling
	var imageFiles []UploadedFile
	var textFiles []UploadedFile
	for _, file := range req.Files {
		if isImageMimeType(file.Type) {
			imageFiles = append(imageFiles, file)
		} else {
			textFiles = append(textFiles, file)
		}
	}

	// For text files, prepend their content to the question as before
	if len(textFiles) > 0 {
		var filesContext strings.Builder
		filesContext.WriteString("Here are the uploaded documents:\n\n")

		for _, file := range textFiles {
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
			if respErr := orihttp.RespondInternalError(w, "current agent not found"); respErr != nil {
				logger.Error("Failed to write response", logger.Fields{"error": respErr})
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

	// Load agent - priority: session's agent > request agent_name > global current agent
	var current string

	// First, try to get agent from the session (sessionID already extracted above)
	if sessionID != "" && h.sessionStore != nil {
		if sess, err := h.sessionStore.GetSession(ctx, sessionID); err == nil && sess != nil && sess.AgentName != "" {
			current = sess.AgentName
			logger.Debug("Using agent from session", logger.Fields{"session_id": sessionID, "agent": current})
		}
	}

	// If no agent from session, check request body
	if current == "" && req.AgentName != "" {
		current = req.AgentName
	}

	// Fallback to current agent from store
	if current == "" {
		names, cur := h.store.ListAgents()
		current = cur
		if current == "" && len(names) > 0 {
			current = names[0] // fallback to first available agent
		}
	}

	ag, ok := h.store.GetAgent(current)
	if !ok {
		if respErr := orihttp.RespondInternalError(w, fmt.Sprintf("agent '%s' not found", current)); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
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
	// If there are image files, use multi-part message with vision API format
	if len(imageFiles) > 0 {
		var contentParts []openai.ChatCompletionContentPartUnionParam

		// Add text content first
		contentParts = append(contentParts, openai.TextContentPart(q))

		// Add image parts with base64 data URLs
		for _, img := range imageFiles {
			// Build data URL: data:image/png;base64,<content>
			dataURL := fmt.Sprintf("data:%s;base64,%s", img.Type, img.Content)
			contentParts = append(contentParts, openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{
				URL:    dataURL,
				Detail: "auto",
			}))
			logger.Info("Added image to message", logger.Fields{"name": img.Name, "type": img.Type})
		}

		ag.Messages = append(ag.Messages, openai.UserMessage(contentParts))
	} else {
		ag.Messages = append(ag.Messages, openai.UserMessage(q))
	}

	// Store user message in session if session ID is provided
	h.storeMessageInSession(r.Context(), sessionID, "user", q)

	// Convert uploaded files to FileAttachments for tool execution
	fileAttachments := ConvertUploadedFilesToAttachments(req.Files)
	logger.Info("Converted files for tool execution", logger.Fields{
		"original_count":  len(req.Files),
		"converted_count": len(fileAttachments),
	})

	// Convert image files to llm.ImageAttachment for vision-capable providers
	var llmImages []llm.ImageAttachment
	for _, img := range imageFiles {
		llmImages = append(llmImages, llm.ImageAttachment{
			MimeType:   img.Type,
			Base64Data: img.Content,
		})
	}

	// Check if this is a Claude model - if so, use provider system
	if strings.HasPrefix(ag.Settings.Model, "claude-") && h.llmFactory != nil {
		// Use Claude provider
		h.handleClaudeChat(w, r, ag, q, tools, current, base, fileAttachments, llmImages)
		return
	}

	// Check if Ollama has this model - route to Ollama provider (dynamic detection)
	if h.llmFactory != nil {
		if ollamaProvider, err := h.llmFactory.GetProvider("ollama"); err == nil {
			if ollamaProv, ok := ollamaProvider.(*llm.OllamaProvider); ok {
				if ollamaProv.HasModel(ag.Settings.Model) {
					logger.Info("Model found in Ollama, routing to Ollama provider", logger.Fields{"model": ag.Settings.Model})
					h.handleOllamaChat(w, r, ag, q, tools, current, base, fileAttachments, llmImages)
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
	h.handleOpenAIChat(w, r, ag, q, tools, current, base, fileAttachments, agentClient)
}

// isImageMimeType checks if a MIME type represents an image
func isImageMimeType(mimeType string) bool {
	switch mimeType {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}
