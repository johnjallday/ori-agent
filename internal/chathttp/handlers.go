package chathttp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/openai/openai-go/v3"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/client"
	"github.com/johnjallday/ori-agent/internal/fileparser"
	"github.com/johnjallday/ori-agent/internal/healthhttp"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/orchestration"
	"github.com/johnjallday/ori-agent/internal/pluginloader"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/skills"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/utilitytelemetry"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/oriagent/ori-pluginapi"
)

// Timeout constants for various operations
const (
	// ChatRequestTimeout is the maximum time allowed for a chat request to complete
	ChatRequestTimeout = 5 * time.Minute

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
	defCache         map[string]*pluginDefinitionCache
	defMu            sync.RWMutex
	pluginLoadMu     sync.Mutex // Mutex for lazy loading plugins
	utilityRegistry  *UtilityToolRegistry
	utilityTelemetry *utilitytelemetry.Tracker
	settingsMu       sync.RWMutex
	browserMCPPref   string
	store            store.Store
	clientFactory    *client.Factory
	llmFactory       *llm.Factory
	healthManager    *healthhttp.Manager
	commandHandler   *CommandHandler
	orchestrator     *orchestration.Orchestrator
	costTracker      *llm.CostTracker
	sessionStore     session.HybridStore
	toolCallStore    session.ToolCallStore
	evolutionSvc     interface {
		AwardMessageXP(agentName string, tokenCount int, userMessage string) error
	}
	skillsManager interface {
		GetSkill(string, string) (*skills.Skill, bool, error)
		ListSkills(string) ([]skills.Skill, error)
		ListEnabledSkillsWithPrompts(string) ([]skills.Skill, error)
	}
	mcpRegistry interface {
		GetToolsForServer(string) ([]pluginapi.PluginTool, error)
		GetAllTools() []pluginapi.PluginTool
		StartServer(string) error
	}
	mcpConfigManager interface {
		EnableServerForAgent(agentName, serverName string) error
		GetServer(name string) (*mcp.ServerConfig, error)
	}
}

func NewHandler(store store.Store, clientFactory *client.Factory) *Handler {
	return &Handler{
		store:            store,
		clientFactory:    clientFactory,
		llmFactory:       nil,
		commandHandler:   NewCommandHandler(store),
		defCache:         make(map[string]*pluginDefinitionCache),
		utilityRegistry:  NewDefaultUtilityToolRegistry(),
		utilityTelemetry: utilitytelemetry.NewTracker(200),
		browserMCPPref:   "auto",
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
	StartServer(string) error
}) {
	h.mcpRegistry = registry
}

// SetMCPConfigManager sets the MCP config manager used for per-agent enablement.
func (h *Handler) SetMCPConfigManager(manager interface {
	EnableServerForAgent(agentName, serverName string) error
	GetServer(name string) (*mcp.ServerConfig, error)
}) {
	h.mcpConfigManager = manager
}

// SetWorkspaceStore sets the workspace store for workspace commands
func (h *Handler) SetWorkspaceStore(ws workspace.Store) {
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

// SetToolCallStore sets the tool call store for storing tool execution data
func (h *Handler) SetToolCallStore(store session.ToolCallStore) {
	h.toolCallStore = store
}

// SetEvolutionService configures agent/assistant XP tracking.
func (h *Handler) SetEvolutionService(service interface {
	AwardMessageXP(agentName string, tokenCount int, userMessage string) error
}) {
	h.evolutionSvc = service
}

// SetSkillsManager sets the skills manager for chat commands and skill execution
func (h *Handler) SetSkillsManager(manager interface {
	GetSkill(string, string) (*skills.Skill, bool, error)
	ListSkills(string) ([]skills.Skill, error)
	ListEnabledSkillsWithPrompts(string) ([]skills.Skill, error)
}) {
	h.skillsManager = manager
	h.commandHandler.SetSkillsManager(manager)
}

// SetUtilityToolRegistry sets the native utility tool registry used by chat routing.
func (h *Handler) SetUtilityToolRegistry(registry *UtilityToolRegistry) {
	h.utilityRegistry = registry
}

// SetBrowserMCPPreference configures preferred MCP browser connector ordering.
func (h *Handler) SetBrowserMCPPreference(preference string) {
	if h == nil {
		return
	}
	pref := normalizeBrowserMCPPreference(preference)
	h.settingsMu.Lock()
	h.browserMCPPref = pref
	h.settingsMu.Unlock()
}

func (h *Handler) getBrowserMCPPreference() string {
	if h == nil {
		return "auto"
	}
	h.settingsMu.RLock()
	defer h.settingsMu.RUnlock()
	return normalizeBrowserMCPPreference(h.browserMCPPref)
}

// SetUtilityTelemetry sets the utility telemetry tracker.
func (h *Handler) SetUtilityTelemetry(tracker *utilitytelemetry.Tracker) {
	h.utilityTelemetry = tracker
}

// UtilityTelemetry returns the utility telemetry tracker.
func (h *Handler) UtilityTelemetry() *utilitytelemetry.Tracker {
	return h.utilityTelemetry
}

// storeToolCall stores a tool call record for analysis
func (h *Handler) storeToolCall(ctx context.Context, sessionID, messageID, toolName, arguments, result, errorMsg string, durationMs int) {
	if h.toolCallStore == nil || sessionID == "" {
		return
	}

	tc := &session.ToolCall{
		MessageID:  messageID,
		SessionID:  sessionID,
		ToolName:   toolName,
		Arguments:  arguments,
		Result:     result,
		Error:      errorMsg,
		DurationMs: durationMs,
	}

	if err := h.toolCallStore.AddToolCall(ctx, tc); err != nil {
		logger.Warn("Failed to store tool call", logger.Fields{
			"session_id": sessionID,
			"tool_name":  toolName,
			"error":      err,
		})
	}
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

func (h *Handler) tryHandleUtilityDirect(
	w http.ResponseWriter,
	baseCtx context.Context,
	ag *agent.Agent,
	agentName string,
	query string,
	sessionID string,
	decisionInput *UtilityRouteDecision,
	plannerDecision *types.PlannerDecision,
) bool {
	if h == nil || ag == nil || strings.TrimSpace(query) == "" {
		return false
	}

	decision := UtilityRouteDecision{}
	if decisionInput != nil {
		decision = *decisionInput
	} else {
		decision = classifyUtilityRoute(query)
	}
	if decision.Mode != UtilityRouteDirect || strings.TrimSpace(decision.ToolName) == "" {
		return false
	}
	if !isUtilityToolAllowedForAgent(ag, decision.ToolName) {
		responseText := disallowedUtilityToolMessage(decision.ToolName)
		ag.Messages = append(ag.Messages, openai.UserMessage(query))
		ag.Messages = append(ag.Messages, openai.AssistantMessage(responseText))
		_ = h.store.SetAgent(agentName, ag)

		h.storeMessageInSession(baseCtx, sessionID, "user", query)
		h.storeMessageInSession(baseCtx, sessionID, "assistant", responseText)

		receipt := buildActionReceipt(
			"utility_direct",
			"Blocked utility tool call",
			"utility tool disabled by agent policy",
			decision.ToolName,
			decision.ToolArgs,
			responseText,
			0,
			false,
			"tool disabled by agent policy",
		)
		writeJSONResponse(w, attachPlannerDecision(attachActionReceipts(attachRouteMetadata(map[string]any{
			"response": responseText,
			"success":  false,
			"error":    "tool disabled by agent policy",
		}, chatRouteMetadata{
			Mode:     string(decision.Mode),
			ToolName: decision.ToolName,
			Reason:   "utility tool disabled by agent policy",
		}), []ActionReceipt{receipt}), plannerDecision))
		return true
	}

	if h.utilityTelemetry != nil {
		h.utilityTelemetry.RecordRouteDecision(string(decision.Mode), decision.Reason)
	}

	tool, found := h.findTool(ag, agentName, decision.ToolName)
	if !found || tool == nil {
		if strings.EqualFold(strings.TrimSpace(decision.ToolName), "browser") {
			responseText := "I couldn't find an available browser tool for this agent. Attach/configure Playwright (or another browser MCP) and try again."
			ag.Messages = append(ag.Messages, openai.UserMessage(query))
			ag.Messages = append(ag.Messages, openai.AssistantMessage(responseText))
			_ = h.store.SetAgent(agentName, ag)

			h.storeMessageInSession(baseCtx, sessionID, "user", query)
			h.storeMessageInSession(baseCtx, sessionID, "assistant", responseText)

			receipt := buildActionReceipt(
				"utility_direct",
				"Browser tool unavailable",
				"no compatible browser tool was available",
				decision.ToolName,
				decision.ToolArgs,
				responseText,
				0,
				false,
				"browser tool unavailable",
			)
			writeJSONResponse(w, attachPlannerDecision(attachActionReceipts(attachRouteMetadata(map[string]any{
				"response": responseText,
				"success":  false,
				"error":    "browser tool unavailable",
			}, chatRouteMetadata{
				Mode:     string(decision.Mode),
				ToolName: decision.ToolName,
				Reason:   "no compatible browser tool was available",
			}), []ActionReceipt{receipt}), plannerDecision))
			return true
		}
		if h.utilityTelemetry != nil {
			h.utilityTelemetry.RecordDelegationEvent(routeModeAssistantChat, "utility tool missing; fallback to chat", agentName)
		}
		return false
	}

	start := time.Now()
	if h.utilityTelemetry != nil {
		h.utilityTelemetry.RecordToolInvocation(decision.ToolName, "")
	}
	toolArgs := decision.ToolArgs
	var rawResult string
	var err error
	if strings.EqualFold(strings.TrimSpace(decision.ToolName), "browser") {
		if adapted, adaptErr := adaptBrowserToolArgsForDefinition(tool.Definition().Name, decision.ToolArgs); adaptErr != nil {
			err = adaptErr
		} else {
			toolArgs = adapted
		}
	}
	if err == nil {
		toolCtx, toolCancel := context.WithTimeout(baseCtx, ToolExecutionTimeout)
		rawResult, err = ExecuteToolWithFiles(toolCtx, tool, decision.ToolName, toolArgs, nil)
		toolCancel()
	}

	duration := time.Since(start)
	h.recordToolCallStats(decision.ToolName, duration, err)
	providerName := inferUtilityProvider(decision.ToolName, rawResult)
	if h.utilityTelemetry != nil {
		errText := ""
		if err != nil {
			errText = err.Error()
		}
		h.utilityTelemetry.RecordToolResult(decision.ToolName, providerName, err == nil, duration, errText)
	}

	responseText := ""
	success := err == nil
	if err != nil {
		responseText = formatUtilityDirectError(decision.ToolName, err)
	} else {
		responseText = formatUtilityDirectResponse(decision.ToolName, rawResult)
	}
	if strings.TrimSpace(responseText) == "" {
		responseText = "I completed the utility request."
	}

	ag.Messages = append(ag.Messages, openai.UserMessage(query))
	ag.Messages = append(ag.Messages, openai.AssistantMessage(responseText))
	_ = h.store.SetAgent(agentName, ag)

	h.storeMessageInSession(baseCtx, sessionID, "user", query)
	h.storeMessageInSession(baseCtx, sessionID, "assistant", responseText)

	errorText := ""
	if err != nil {
		errorText = err.Error()
	}
	receipt := buildActionReceipt(
		"utility_direct",
		"Executed utility tool",
		decision.Reason,
		decision.ToolName,
		toolArgs,
		rawResult,
		duration.Milliseconds(),
		success,
		errorText,
	)
	payload := attachActionReceipts(attachRouteMetadata(map[string]any{
		"response":  responseText,
		"tool_args": toolArgs,
		"success":   success,
		"toolCalls": []map[string]string{
			{
				"function": decision.ToolName,
				"args":     toolArgs,
				"result":   rawResult,
			},
		},
	}, chatRouteMetadata{
		Mode:      string(decision.Mode),
		ToolName:  decision.ToolName,
		Provider:  providerName,
		Reason:    decision.Reason,
		ToolCount: 1,
	}), []ActionReceipt{receipt})
	if err != nil {
		payload["error"] = err.Error()
	}
	writeJSONResponse(w, attachPlannerDecision(payload, plannerDecision))
	return true
}

// findTool searches for a tool by name in both plugins and MCP servers.
// If the plugin is not yet loaded, it will be loaded lazily on first use.
func (h *Handler) findTool(ag *agent.Agent, agentName, toolName string) (pluginapi.PluginTool, bool) {
	if !isUtilityToolAllowedForAgent(ag, toolName) {
		return nil, false
	}

	if shouldPreferMCPToolOverUtility(toolName) {
		if mcpTool, ok := h.findMCPToolByName(ag, toolName); ok {
			return mcpTool, true
		}
		if shouldSuppressUtilityToolForAgent(ag, toolName) {
			return nil, false
		}
	}

	// Native utility tools are checked first to prioritize accurate daily utility behavior.
	if h.utilityRegistry != nil {
		if tool, ok := h.utilityRegistry.GetTool(toolName); ok {
			return tool, true
		}
	}

	// First check native plugins
	for pluginName, plugin := range ag.Plugins {
		if plugin.Definition.Name == toolName {
			// If already loaded, return it
			if plugin.Tool != nil {
				return plugin.Tool, true
			}
			// Lazy load the plugin
			tool, err := h.loadPluginLazily(agentName, pluginName, plugin)
			if err != nil {
				logger.Error("Failed to lazy load plugin", logger.Fields{
					"plugin": pluginName,
					"agent":  agentName,
					"error":  err.Error(),
				})
				return nil, false
			}
			return tool, true
		}
	}

	// Then check MCP tools.
	if mcpTool, ok := h.findMCPToolByName(ag, toolName); ok {
		return mcpTool, true
	}

	return nil, false
}

func (h *Handler) findMCPToolByName(ag *agent.Agent, toolName string) (pluginapi.PluginTool, bool) {
	if h == nil || h.mcpRegistry == nil || ag == nil || len(ag.MCPServers) == 0 {
		return nil, false
	}
	target := strings.TrimSpace(toolName)
	if target == "" {
		return nil, false
	}
	candidateNames := mcpToolNameCandidates(target)
	serverNames := prioritizeMCPServersForTool(ag.MCPServers, target, h.getBrowserMCPPreference())
	for _, serverName := range serverNames {
		mcpTools, err := h.getMCPToolsForServer(serverName)
		if err != nil {
			continue
		}
		for _, mcpTool := range mcpTools {
			defName := strings.TrimSpace(mcpTool.Definition().Name)
			for _, candidate := range candidateNames {
				if defName == candidate {
					return mcpTool, true
				}
			}
		}
	}
	return nil, false
}

func mcpToolNameCandidates(target string) []string {
	trimmed := strings.TrimSpace(target)
	if strings.EqualFold(trimmed, "browser") {
		return []string{"browser", "browser_navigate"}
	}
	return []string{trimmed}
}

func prioritizeMCPServersForTool(serverNames []string, toolName, browserPreference string) []string {
	if len(serverNames) == 0 {
		return []string{}
	}

	normalizedTool := strings.ToLower(strings.TrimSpace(toolName))
	if normalizedTool != "browser" {
		out := make([]string, 0, len(serverNames))
		for _, serverName := range serverNames {
			name := strings.TrimSpace(serverName)
			if name == "" {
				continue
			}
			out = append(out, name)
		}
		return out
	}

	out := make([]string, 0, len(serverNames))
	seen := make(map[string]bool)

	appendByName := func(target string) {
		for _, serverName := range serverNames {
			name := strings.TrimSpace(serverName)
			if name == "" {
				continue
			}
			key := strings.ToLower(name)
			if seen[key] {
				continue
			}
			if key == target {
				seen[key] = true
				out = append(out, name)
			}
		}
	}

	for _, preferredServer := range browserMCPPriorityOrder(browserPreference) {
		appendByName(preferredServer)
	}

	for _, serverName := range serverNames {
		name := strings.TrimSpace(serverName)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, name)
	}

	return out
}

func browserMCPPriorityOrder(preference string) []string {
	switch normalizeBrowserMCPPreference(preference) {
	case "browserbase":
		return []string{"browserbase", "playwright", "puppeteer"}
	case "puppeteer":
		return []string{"puppeteer", "playwright", "browserbase"}
	default:
		// Auto and explicit Playwright both prioritize Playwright first.
		return []string{"playwright", "browserbase", "puppeteer"}
	}
}

func normalizeBrowserMCPPreference(preference string) string {
	switch strings.ToLower(strings.TrimSpace(preference)) {
	case "playwright", "browserbase", "puppeteer":
		return strings.ToLower(strings.TrimSpace(preference))
	default:
		return "auto"
	}
}

func adaptBrowserToolArgsForDefinition(definitionName, rawArgs string) (string, error) {
	name := strings.ToLower(strings.TrimSpace(definitionName))
	if name == "" || name == "browser" {
		return rawArgs, nil
	}
	if name != "browser_navigate" {
		return rawArgs, nil
	}

	req := BrowserRequest{}
	if err := json.Unmarshal([]byte(rawArgs), &req); err != nil {
		var direct map[string]interface{}
		if err2 := json.Unmarshal([]byte(rawArgs), &direct); err2 == nil {
			if url, ok := direct["url"].(string); ok && strings.TrimSpace(url) != "" {
				payload, _ := json.Marshal(map[string]string{"url": normalizeBrowserOpenTargetURL(url)})
				return string(payload), nil
			}
		}
		return "", fmt.Errorf("%w: invalid browser arguments", ErrUtilityInvalidInput)
	}

	action := strings.ToLower(strings.TrimSpace(req.Action))
	if action == "" {
		action = "open_url"
	}
	if action != "open_url" {
		return "", fmt.Errorf("%w: this browser connector supports open_url for direct open commands", ErrUtilityInvalidInput)
	}
	if strings.TrimSpace(req.URL) == "" {
		return "", fmt.Errorf("%w: url is required", ErrUtilityInvalidInput)
	}

	payload, _ := json.Marshal(map[string]string{"url": normalizeBrowserOpenTargetURL(req.URL)})
	return string(payload), nil
}

func shouldPreferMCPToolOverUtility(toolName string) bool {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "browser", "web_search", "web_fetch":
		return true
	default:
		return false
	}
}

func shouldSuppressUtilityToolForAgent(ag *agent.Agent, toolName string) bool {
	if ag == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "browser":
		// If a browser-control MCP is attached, avoid silently falling back to
		// the lightweight native browser utility for auth-heavy flows (e.g. Gmail).
		return hasAnyMCPServer(ag.MCPServers, []string{"playwright", "browserbase", "puppeteer"})
	default:
		return false
	}
}

// loadPluginLazily loads a plugin on first use and updates the agent's plugin map.
func (h *Handler) loadPluginLazily(agentName, pluginName string, lp types.LoadedPlugin) (pluginapi.PluginTool, error) {
	h.pluginLoadMu.Lock()
	defer h.pluginLoadMu.Unlock()

	// Double-check if already loaded (another goroutine may have loaded it)
	ag, ok := h.store.GetAgent(agentName)
	if !ok {
		return nil, fmt.Errorf("agent not found: %s", agentName)
	}
	if existing, exists := ag.Plugins[pluginName]; exists && existing.Tool != nil {
		return existing.Tool, nil
	}

	logger.Info("Lazy loading plugin", logger.Fields{
		"plugin": pluginName,
		"agent":  agentName,
	})

	// Load the plugin
	tool, err := pluginloader.LoadPluginUnified(lp.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to load plugin %s: %w", pluginName, err)
	}

	// Set agent context
	agentSpecificStorePath := filepath.Join("agents", agentName, "config.json")
	if abs, err := filepath.Abs(agentSpecificStorePath); err == nil {
		agentSpecificStorePath = abs
	}
	pluginloader.SetAgentContext(tool, agentName, agentSpecificStorePath, "")

	// Run health check if health manager is available
	if h.healthManager != nil {
		h.healthManager.CheckAndCachePlugin(pluginName, tool)
	}

	// Update the plugin in the agent
	lp.Tool = tool
	lp.Definition = tool.Definition()
	ag.Plugins[pluginName] = lp

	// Save the updated agent
	if err := h.store.SetAgent(agentName, ag); err != nil {
		logger.Warn("Failed to save agent after lazy loading plugin", logger.Fields{
			"agent":  agentName,
			"plugin": pluginName,
			"error":  err.Error(),
		})
	}

	return tool, nil
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
		Question             string            `json:"question"`
		AgentName            string            `json:"agent_name,omitempty"` // Allow specifying target agent
		Files                []UploadedFile    `json:"files,omitempty"`
		RouteContext         *chatRouteContext `json:"route_context,omitempty"`
		MultiAgentMode       string            `json:"multi_agent_mode,omitempty"`
		MultiAgentThreshold  float64           `json:"multi_agent_threshold,omitempty"`
		PlanBeforeAction     bool              `json:"plan_before_action,omitempty"`
		ApprovedActionPlanID string            `json:"approved_action_plan_id,omitempty"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	q := strings.TrimSpace(req.Question)
	if q == "" {
		orihttp.BadRequest(w, "empty question")
		return
	}
	originalQuery := q
	approvedActionPlanID := strings.TrimSpace(req.ApprovedActionPlanID)
	normalizedRouteContext := normalizeChatRouteContext(req.RouteContext)
	runtimeSystemPrompt := buildRouteContextSystemPrompt(normalizedRouteContext)

	// Natural language app launch shortcut:
	// "open safari" -> "/openapp safari"
	if len(req.Files) == 0 {
		if rewritten, ok := inferOpenAppCommandFromChat(q); ok {
			logger.Debug("Auto-routed chat prompt to /openapp", logger.Fields{
				"original":  q,
				"rewritten": rewritten,
			})
			q = rewritten
		}
	}

	// Debug: Log received files

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
			fileText := file.Content
			if isParseableDocument(file.Name) {
				parsedText, err := parseUploadedFileText(file)
				if err != nil {
					logger.Warn("Failed to extract text from uploaded file", logger.Fields{
						"name":  file.Name,
						"type":  file.Type,
						"error": err.Error(),
					})
					fileText = fmt.Sprintf("[Unable to extract text from %s]", file.Name)
				} else if strings.TrimSpace(parsedText) == "" {
					fileText = fmt.Sprintf("[No extractable text found in %s]", file.Name)
				} else {
					fileText = parsedText
				}
			}

			filesContext.WriteString(fmt.Sprintf("=== File: %s ===\n", file.Name))
			filesContext.WriteString(fileText)
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
	if q == "/skills" {
		h.commandHandler.HandleSkillsList(w, r)
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
	if strings.HasPrefix(q, "/openapp") {
		appName := strings.TrimSpace(strings.TrimPrefix(q, "/openapp"))
		h.commandHandler.HandleOpenApp(w, r, appName)
		return
	}

	resolveAgentName := func() string {
		if sessionID != "" && h.sessionStore != nil {
			if sess, err := h.sessionStore.GetSession(r.Context(), sessionID); err == nil && sess != nil && sess.AgentName != "" {
				return sess.AgentName
			}
		}
		if req.AgentName != "" {
			return req.AgentName
		}
		names, current := h.store.ListAgents()
		if current == "" && len(names) > 0 {
			return names[0]
		}
		return current
	}

	var invokedSkill *skillInvocation
	if strings.HasPrefix(q, "/skill") {
		if h.skillsManager == nil {
			orihttp.WriteJSON(w, map[string]any{
				"response": "❌ Skills are not enabled.",
			})
			return
		}
		name, args, err := parseSkillCommand(q)
		if err != nil {
			orihttp.WriteJSON(w, map[string]any{
				"response": fmt.Sprintf("❌ **Invalid command**: %v\n\nFormat: `/skill <name> <args>`", err),
			})
			return
		}
		agentName := resolveAgentName()
		skill, found, err := h.skillsManager.GetSkill(agentName, name)
		if err != nil {
			var conflicts *skills.SkillConflictError
			if errors.As(err, &conflicts) {
				orihttp.WriteJSON(w, map[string]any{
					"response":  "❌ Duplicate skill names detected. Resolve conflicts in your skills folders before running skills.",
					"conflicts": conflicts.Conflicts,
				})
				return
			}
			orihttp.InternalError(w, err.Error())
			return
		}
		if !found {
			orihttp.WriteJSON(w, map[string]any{
				"response": fmt.Sprintf("❌ Skill '%s' not found. Use /skills to list available skills.", name),
			})
			return
		}
		if len(skill.ValidationErrors) > 0 {
			orihttp.WriteJSON(w, map[string]any{
				"response": fmt.Sprintf("❌ Skill '%s' has validation errors: %s", skill.Name, strings.Join(skill.ValidationErrors, "; ")),
			})
			return
		}
		if !skill.Enabled {
			orihttp.WriteJSON(w, map[string]any{
				"response": fmt.Sprintf("❌ Skill '%s' is disabled.", skill.Name),
			})
			return
		}
		if skill.HasScripts && !skill.Trusted {
			orihttp.WriteJSON(w, map[string]any{
				"response": fmt.Sprintf("❌ Skill '%s' requires trust before it can run.", skill.Name),
			})
			return
		}
		invokedSkill = &skillInvocation{Skill: skill, Args: args, Explicit: true}
	}

	if invokedSkill == nil {
		if name, args, ok := parseImplicitSkillCommand(q); ok && h.skillsManager != nil {
			agentName := resolveAgentName()
			skill, found, err := h.skillsManager.GetSkill(agentName, name)
			if err != nil {
				var conflicts *skills.SkillConflictError
				if errors.As(err, &conflicts) {
					orihttp.WriteJSON(w, map[string]any{
						"response":  "❌ Duplicate skill names detected. Resolve conflicts in your skills folders before running skills.",
						"conflicts": conflicts.Conflicts,
					})
					return
				}
				orihttp.InternalError(w, err.Error())
				return
			}
			if found {
				if len(skill.ValidationErrors) > 0 {
					orihttp.WriteJSON(w, map[string]any{
						"response": fmt.Sprintf("❌ Skill '%s' has validation errors: %s", skill.Name, strings.Join(skill.ValidationErrors, "; ")),
					})
					return
				}
				if !skill.Enabled {
					orihttp.WriteJSON(w, map[string]any{
						"response": fmt.Sprintf("❌ Skill '%s' is disabled.", skill.Name),
					})
					return
				}
				if skill.HasScripts && !skill.Trusted {
					orihttp.WriteJSON(w, map[string]any{
						"response": fmt.Sprintf("❌ Skill '%s' requires trust before it can run.", skill.Name),
					})
					return
				}
				invokedSkill = &skillInvocation{Skill: skill, Args: args, Explicit: false}
			}
		}
	}
	if strings.HasPrefix(q, "/tool ") {
		// Direct tool execution - bypass LLM decision-making
		cmd, err := parseDirectToolCommand(q)
		if err != nil {
			// Return parsing error as response
			orihttp.WriteJSON(w, attachRouteMetadata(map[string]any{
				"response":         fmt.Sprintf("❌ **Invalid command**: %v\n\nFormat: `/tool <tool_name> {\"key\": \"value\"}`\nExample: `/tool math {\"operation\": \"add\", \"a\": 5, \"b\": 3}`", err),
				"direct_tool_call": true,
				"success":          false,
			}, chatRouteMetadata{
				Mode: routeModeDirectTool,
			}))
			return
		}

		// Attach files to the command if present
		if len(req.Files) > 0 {
			cmd.Files = ConvertUploadedFilesToAttachments(req.Files)
		}

		// Load agent - use session-bound agent if available
		current := resolveAgentName()
		if current == "" {
			orihttp.InternalError(w, "no agent available for direct tool execution")
			return
		}
		ag, ok := h.store.GetAgent(current)
		if !ok || ag == nil {
			orihttp.InternalError(w, fmt.Sprintf("agent '%s' not found", current))
			return
		}

		if req.PlanBeforeAction && approvedActionPlanID == "" {
			plan := buildDirectToolActionPlan(q, cmd)
			response := attachRouteMetadata(map[string]any{
				"response":           formatActionPlanMessage(plan),
				"requires_approval":  true,
				"approval_type":      "action_plan",
				"action_plan_id":     plan.ID,
				"action_plan":        plan,
				"plan_before_action": true,
			}, chatRouteMetadata{
				Mode:   routeModeDirectTool,
				Reason: "awaiting action plan approval",
			})
			writeJSONResponse(w, response)
			return
		}

		result := h.executeDirectTool(r.Context(), ag, current, cmd)

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
	current := resolveAgentName()

	ag, ok := h.store.GetAgent(current)
	if !ok {
		orihttp.InternalError(w, fmt.Sprintf("agent '%s' not found", current))
		return
	}

	routeDecision := classifyUtilityRoute(originalQuery)

	if req.PlanBeforeAction && approvedActionPlanID == "" {
		actionPlan, planDecision := h.buildChatActionPlan(ctx, q, routeDecision, req.MultiAgentMode, req.MultiAgentThreshold)
		response := attachPlannerDecision(attachRouteMetadata(map[string]any{
			"response":           formatActionPlanMessage(actionPlan),
			"requires_approval":  true,
			"approval_type":      "action_plan",
			"action_plan_id":     actionPlan.ID,
			"action_plan":        actionPlan,
			"plan_before_action": true,
		}, chatRouteMetadata{
			Mode:   actionPlan.RouteMode,
			Reason: "awaiting action plan approval",
		}), planDecision)
		writeJSONResponse(w, response)
		return
	}

	if invokedSkill == nil && routeNeedsWorkspace(routeDecision.Mode) {
		autoWorkspace, created, wsErr := h.ensureWorkspaceForRoute(current, originalQuery, routeDecision)
		if wsErr != nil {
			logger.Warn("Failed to ensure workspace for routed chat request", logger.Fields{
				"agent":      current,
				"route_mode": routeDecision.Mode,
				"error":      wsErr,
			})
		} else if autoWorkspace != nil {
			q = enrichPromptWithWorkspaceContext(q, autoWorkspace, routeDecision.Mode, created)
		}
	}

	// Utility-direct requests bypass planner/delegation and execute native tools immediately.
	if invokedSkill == nil {
		if routeDecision.Mode != "" && routeDecision.Mode != UtilityRouteDirect && h.utilityTelemetry != nil {
			h.utilityTelemetry.RecordRouteDecision(string(routeDecision.Mode), routeDecision.Reason)
		}
		if h.tryHandleUtilityDirect(w, base, ag, current, originalQuery, sessionID, &routeDecision, nil) {
			return
		}
		if h.utilityTelemetry != nil {
			if routeDecision.Mode != "" {
				h.utilityTelemetry.RecordDelegationEvent(string(routeDecision.Mode), routeDecision.Reason, current)
			} else {
				h.utilityTelemetry.RecordDelegationEvent(routeModeAssistantChat, "standard assistant chat flow", current)
			}
		}
	}

	preflight := h.maybeAutoEnableMCPForPrompt(current, ag, originalQuery)
	if preflight != nil && strings.TrimSpace(preflight.userMessage) != "" {
		writeJSONResponse(w, attachRouteMetadata(map[string]any{
			"response": preflight.userMessage,
		}, chatRouteMetadata{
			Mode:   routeModeAssistantChat,
			Reason: "mcp preflight notice",
		}))
		return
	}

	if invokedSkill != nil && len(invokedSkill.Skill.RequiredMCPServers) > 0 {
		missing := missingMCPServers(ag.MCPServers, invokedSkill.Skill.RequiredMCPServers)
		if len(missing) > 0 {
			orihttp.WriteJSON(w, map[string]any{
				"response": fmt.Sprintf("❌ Skill '%s' requires MCP servers: %s. Enable them in agent settings.", invokedSkill.Skill.Name, strings.Join(missing, ", ")),
			})
			return
		}
	}

	sessionQuery := originalQuery
	if invokedSkill != nil {
		q = buildSkillPrompt(invokedSkill.Skill, invokedSkill.Args)
		if q == "" {
			orihttp.WriteJSON(w, map[string]any{
				"response": fmt.Sprintf("❌ Skill '%s' has no prompt content.", invokedSkill.Skill.Name),
			})
			return
		}
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

	var plannerDecision *types.PlannerDecision

	// Planner-first orchestration routing
	if h.orchestrator != nil {
		mode, threshold := h.orchestrator.GetMultiAgentDefaults()
		if req.MultiAgentMode != "" {
			if parsed, ok := types.ParseMultiAgentMode(strings.ToLower(strings.TrimSpace(req.MultiAgentMode))); ok {
				mode = parsed
			}
		}
		if req.MultiAgentThreshold > 0 {
			threshold = req.MultiAgentThreshold
		}

		if mode != types.MultiAgentModeOff {
			plan, err := h.orchestrator.PlanTask(ctx, q)
			if err != nil {
				logger.Error("Planner failed", logger.Fields{"error": err})
			} else {
				decision := h.orchestrator.DecideMultiAgent(plan, mode, threshold)
				plannerDecision = &decision
				logger.Info("Planner routing decision", logger.Fields{
					"mode":        decision.Mode,
					"complexity":  decision.ComplexityScore,
					"threshold":   decision.Threshold,
					"multi_agent": decision.MultiAgent,
				})

				if decision.MultiAgent {
					result, err := h.orchestrator.ExecutePlannedTask(ctx, current, q, plan, decision, 10*time.Minute)
					if err != nil {
						logger.Error("Orchestration failed", logger.Fields{"error": err})
					} else {
						logger.Info("Orchestration completed successfully", logger.Fields{})
						responseText := result.FinalOutput
						if responseText == "" && result.Status == "pending_approval" {
							responseText = "Dynamic agent approval required to continue."
						}
						if h.utilityTelemetry != nil {
							h.utilityTelemetry.RecordDelegationEvent(routeModeSpecialistFlow, "planner selected multi-agent execution", current)
						}
						receipt := buildActionReceipt(
							"orchestration",
							"Executed multi-agent workflow",
							"planner selected multi-agent execution",
							"",
							"",
							responseText,
							0,
							result.Error == "",
							result.Error,
						)
						orihttp.WriteJSON(w, attachActionReceipts(attachRouteMetadata(map[string]any{
							"response":               responseText,
							"orchestrated":           true,
							"studio_id":              result.WorkspaceID,
							"status":                 result.Status,
							"pending_plan_id":        result.PendingPlanID,
							"planner_decision":       result.PlannerDecision,
							"planner_plan":           plan,
							"dynamic_agent_requests": result.DynamicAgentRequests,
						}, chatRouteMetadata{
							Mode:   routeModeSpecialistFlow,
							Reason: "planner selected multi-agent execution",
						}), []ActionReceipt{receipt}))
						return
					}
				}
			}
		} else {
			decision := types.PlannerDecision{
				ComplexityScore: 0,
				Threshold:       threshold,
				Mode:            string(mode),
				MultiAgent:      false,
				Rationale:       "Multi-agent disabled",
				CreatedAt:       time.Now(),
			}
			plannerDecision = &decision
		}
	}

	// Build tools - refresh definitions to get latest dynamic enums (e.g., script lists)
	tools := []llm.Tool{}
	toolIndex := make(map[string]int)
	toolSource := make(map[string]string)
	appendTool := func(def llm.Tool, source string) {
		name := strings.TrimSpace(def.Name)
		if name == "" {
			return
		}
		if idx, exists := toolIndex[name]; exists {
			if source == "mcp" && toolSource[name] == "utility" && shouldPreferMCPToolOverUtility(name) {
				tools[idx] = def
				toolSource[name] = source
			}
			return
		}
		toolIndex[name] = len(tools)
		toolSource[name] = source
		tools = append(tools, def)
	}

	// Add native utility tools first
	if h.utilityRegistry != nil {
		for _, def := range h.utilityRegistry.ListToolDefinitions() {
			if !isUtilityToolAllowedForAgent(ag, def.Name) {
				continue
			}
			if shouldSuppressUtilityToolForAgent(ag, def.Name) {
				continue
			}
			appendTool(llm.Tool{
				Name:        def.Name,
				Description: def.Description,
				Parameters:  def.Parameters,
			}, "utility")
		}
	}

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
		appendTool(llm.Tool{
			Name:        def.Name,
			Description: def.Description,
			Parameters:  def.Parameters,
		}, "plugin")
	}

	// Add MCP tools for enabled servers
	logger.Debug("Checking MCP servers for agent", logger.Fields{"agent": current, "server_count": len(ag.MCPServers), "servers": ag.MCPServers})
	if h.mcpRegistry != nil && len(ag.MCPServers) > 0 {
		logger.Debug("Loading MCP tools for agent", logger.Fields{"agent": current})
		for _, serverName := range ag.MCPServers {
			logger.Debug("Attempting to get tools for MCP server", logger.Fields{"server": serverName})
			mcpTools, err := h.getMCPToolsForServer(serverName)
			if err != nil {
				logger.Warn("Failed to get MCP tools for server", logger.Fields{"server": serverName, "error": err})
				continue
			}
			for _, mcpTool := range mcpTools {
				mcpDef := mcpTool.Definition()
				appendTool(llm.Tool{
					Name:        mcpDef.Name,
					Description: mcpDef.Description,
					Parameters:  mcpDef.Parameters,
				}, "mcp")
			}
			logger.Debug("Added MCP tools from server", logger.Fields{"count": len(mcpTools), "server": serverName})
		}
	}

	if invokedSkill != nil {
		tools = filterToolsForSkill(tools, invokedSkill.Skill)
	}
	tools = prioritizeToolsForPath(ag, tools)

	// Get appropriate client for this agent
	agentClient := h.getClientForAgent(ag)

	// Add system message for better tool usage guidance
	if len(ag.Messages) == 0 {
		systemPrompt := h.buildSystemPromptWithSkills(
			ag, current,
			"You are a helpful assistant with access to various tools. When a user request can be fulfilled by using an available tool, use the tool instead of providing general information. Be concise and direct in your responses.",
		)

		// Append available tools list if there are any
		if len(tools) > 0 {
			systemPrompt += " Available tools: "
			var toolNames []string
			for _, tool := range tools {
				toolNames = append(toolNames, tool.Name)
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
	h.storeMessageInSession(r.Context(), sessionID, "user", sessionQuery)

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

	// Check if this is a Claude Code provider - route to Claude Code handler
	if strings.EqualFold(ag.Settings.Provider, "claude_code") && h.llmFactory != nil {
		h.handleClaudeCodeChat(w, r, ag, q, current, base, llmImages, plannerDecision, runtimeSystemPrompt)
		return
	}

	// Check if this is a Claude model - if so, use provider system
	if (strings.HasPrefix(ag.Settings.Model, "claude-") || strings.EqualFold(ag.Settings.Provider, "claude") || strings.EqualFold(ag.Settings.Provider, "anthropic")) && h.llmFactory != nil {
		// Use Claude provider
		h.handleClaudeChat(w, r, ag, q, tools, current, base, fileAttachments, llmImages, plannerDecision, runtimeSystemPrompt)
		return
	}

	// Check if Ollama has this model - route to Ollama provider (dynamic detection)
	if h.llmFactory != nil {
		if ollamaProvider, err := h.llmFactory.GetProvider("ollama"); err == nil {
			if ollamaProv, ok := ollamaProvider.(*llm.OllamaProvider); ok {
				if ollamaProv.HasModel(ag.Settings.Model) {
					logger.Info("Model found in Ollama, routing to Ollama provider", logger.Fields{"model": ag.Settings.Model})
					h.handleOllamaChat(w, r, ag, q, tools, current, base, fileAttachments, llmImages, plannerDecision, runtimeSystemPrompt)
					return
				}
			}
		}
	}

	// Check if this is a Gemini model or provider
	if strings.HasPrefix(strings.ToLower(ag.Settings.Model), "gemini-") || strings.EqualFold(ag.Settings.Provider, "gemini") {
		h.handleGeminiChat(w, r, ag, q, tools, current, base, fileAttachments, llmImages, plannerDecision, runtimeSystemPrompt)
		return
	}

	// Route Codex provider/model through Codex CLI provider path (no OpenAI API key required).
	if isCodexProviderOrModel(ag.Settings.Provider, ag.Settings.Model) && h.llmFactory != nil {
		h.handleCodexChat(w, r, ag, q, current, base, llmImages, plannerDecision, runtimeSystemPrompt)
		return
	}

	// OpenAI models require an API key; return a clear error if none is configured.
	if h.clientFactory != nil && !h.clientFactory.HasKeyForAgent(ag) {
		writeJSONResponse(w, map[string]any{
			"response": "❌ **Error**: OpenAI API key is not configured. Set `OPENAI_API_KEY` for the server process, or add an API key in the app Settings.",
		})
		return
	}

	// Handle OpenAI models
	h.handleOpenAIChat(w, r, ag, q, tools, current, base, fileAttachments, agentClient, plannerDecision, runtimeSystemPrompt)
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

func isCodexProviderOrModel(provider, model string) bool {
	if strings.EqualFold(strings.TrimSpace(provider), "codex") {
		return true
	}
	return strings.Contains(strings.ToLower(strings.TrimSpace(model)), "codex")
}

func isParseableDocument(filename string) bool {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".pdf", ".docx", ".pptx", ".xlsx":
		return true
	default:
		return false
	}
}

func parseUploadedFileText(file UploadedFile) (string, error) {
	decoded, err := base64.StdEncoding.DecodeString(file.Content)
	if err != nil {
		return "", fmt.Errorf("invalid base64 content")
	}

	if err := fileparser.ValidateFileSize(int64(len(decoded))); err != nil {
		return "", err
	}

	return fileparser.ParseFile(file.Name, decoded)
}
