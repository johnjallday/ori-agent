package chathttp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/openai/openai-go/v3"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/client"
	"github.com/johnjallday/ori-agent/internal/fileparser"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/orchestration"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/skills"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/toolapi"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/utilitytelemetry"
	"github.com/johnjallday/ori-agent/internal/workspace"
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
)

type Handler struct {
	utilityRegistry  *UtilityToolRegistry
	utilityTelemetry *utilitytelemetry.Tracker
	settingsMu       sync.RWMutex
	browserMCPPref   string
	store            store.Store
	clientFactory    *client.Factory
	llmFactory       *llm.Factory
	commandHandler   *CommandHandler
	orchestrator     *orchestration.Orchestrator
	costTracker      *llm.CostTracker
	sessionStore     session.HybridStore
	workspaceStore   workspace.Store
	fileStore        *workspace.FileStore
	userProfileStore userprofile.UserStore
	userProvider     userprofile.UserProvider
	runtimeResolver  chatRuntimeResolver
	toolCallStore    session.ToolCallStore

	// Project-template tool dependencies (optional; see SetProjectTemplateDeps)
	templatesRootResolver func() string
	workspaceEventBus     *workspace.EventBus
	evolutionSvc          interface {
		AwardMessageXP(agentName string, tokenCount int, userMessage string) error
	}
	skillsManager interface {
		GetSkill(string, string) (*skills.Skill, bool, error)
		ListSkills(string) ([]skills.Skill, error)
		ListEnabledSkillsWithPrompts(string) ([]skills.Skill, error)
	}
	mcpRegistry interface {
		GetToolsForServer(string) ([]toolapi.Tool, error)
		GetAllTools() []toolapi.Tool
		StartServer(string) error
	}
	mcpConfigManager interface {
		GetServer(name string) (*mcp.ServerConfig, error)
	}
}

func NewHandler(store store.Store, clientFactory *client.Factory) *Handler {
	return &Handler{
		store:            store,
		clientFactory:    clientFactory,
		llmFactory:       nil,
		commandHandler:   NewCommandHandler(store),
		utilityRegistry:  NewDefaultUtilityToolRegistry(),
		utilityTelemetry: utilitytelemetry.NewTracker(200),
		browserMCPPref:   "auto",
	}
}

// writeJSONResponse writes a JSON response and logs errors if encoding fails
func writeJSONResponse(w http.ResponseWriter, data any) {
	orihttp.WriteJSON(w, data)
}

// SetLLMFactory sets the LLM factory
func (h *Handler) SetLLMFactory(factory *llm.Factory) {
	h.llmFactory = factory
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
	GetToolsForServer(string) ([]toolapi.Tool, error)
	GetAllTools() []toolapi.Tool
	StartServer(string) error
}) {
	h.mcpRegistry = registry
}

// SetMCPConfigManager sets the MCP config manager used for global MCP templates.
func (h *Handler) SetMCPConfigManager(manager interface {
	GetServer(name string) (*mcp.ServerConfig, error)
}) {
	h.mcpConfigManager = manager
}

// SetWorkspaceStore sets the workspace store for workspace commands
func (h *Handler) SetWorkspaceStore(ws workspace.Store) {
	h.workspaceStore = ws
	h.commandHandler.SetWorkspaceStore(ws)
}

// SetFileStore sets the folder-based workspace store for syncing notes to disk.
func (h *Handler) SetFileStore(fs *workspace.FileStore) {
	h.fileStore = fs
}

func (h *Handler) SetUserProfileDeps(store userprofile.UserStore, provider userprofile.UserProvider) {
	h.userProfileStore = store
	if provider == nil {
		provider = userprofile.LocalUserProvider{}
	}
	h.userProvider = provider
}

// SetProjectTemplateDeps enables the workspace project-template tools for
// chats: a resolver for the templates library directory and the event bus
// used to publish project.created.
func (h *Handler) SetProjectTemplateDeps(templatesRootResolver func() string, eventBus *workspace.EventBus) {
	h.templatesRootResolver = templatesRootResolver
	h.workspaceEventBus = eventBus
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
	ag *resolvedChatAgent,
	agentName string,
	query string,
	sessionID string,
	decisionInput *UtilityRouteDecision,
	plannerDecision *types.PlannerDecision,
) bool {
	if h == nil || ag == nil || ag.Agent == nil || strings.TrimSpace(query) == "" {
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
	if !isUtilityToolAllowedForAgent(ag.Agent, decision.ToolName) {
		responseText := disallowedUtilityToolMessage(decision.ToolName)
		ag.Messages = append(ag.Messages, openai.UserMessage(query))
		ag.Messages = append(ag.Messages, openai.AssistantMessage(responseText))
		_ = h.persistAgent(agentName, ag.Agent)

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

	tool, found := h.findTool(ag, decision.ToolName)
	if !found || tool == nil {
		if strings.EqualFold(strings.TrimSpace(decision.ToolName), "browser") {
			responseText := "I couldn't find an available browser tool for this agent. Attach/configure Playwright (or another browser MCP) and try again."
			ag.Messages = append(ag.Messages, openai.UserMessage(query))
			ag.Messages = append(ag.Messages, openai.AssistantMessage(responseText))
			_ = h.persistAgent(agentName, ag.Agent)

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
	if err != nil && strings.EqualFold(strings.TrimSpace(decision.ToolName), "browser") {
		if openURL, canRecover := extractBrowserOpenURLFromArgs(toolArgs); canRecover && isTransientBrowserNavigationError(err) {
			logger.Warn("Browser navigation hit transient context error; retrying once", logger.Fields{
				"tool": decision.ToolName,
				"url":  openURL,
				"err":  err.Error(),
			})
			retryCtx, retryCancel := context.WithTimeout(baseCtx, ToolExecutionTimeout)
			retryResult, retryErr := ExecuteToolWithFiles(retryCtx, tool, decision.ToolName, toolArgs, nil)
			retryCancel()
			if retryErr == nil {
				rawResult = retryResult
				err = nil
			} else {
				if recovered, recoveredResult := maybeRecoverBrowserNavigationResult(decision.ToolName, toolArgs, retryErr); recovered {
					rawResult = recoveredResult
					err = nil
				} else {
					err = retryErr
				}
			}
		}
	}

	duration := time.Since(start)
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
	_ = h.persistAgent(agentName, ag.Agent)

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
func (h *Handler) findTool(ag *resolvedChatAgent, toolName string) (toolapi.Tool, bool) {
	if ag == nil || ag.Agent == nil || !isUtilityToolAllowedForAgent(ag.Agent, toolName) {
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

	// Check workspace-scoped tools (notes, tasks, sessions, files, directories)
	if ag.WorkspaceTools != nil {
		for _, wt := range ag.WorkspaceTools.Tools() {
			if wt.Definition().Name == toolName {
				return wt, true
			}
		}
	}

	// Check MCP tools.
	if mcpTool, ok := h.findMCPToolByName(ag, toolName); ok {
		return mcpTool, true
	}

	return nil, false
}

func (h *Handler) findMCPToolByName(ag *resolvedChatAgent, toolName string) (toolapi.Tool, bool) {
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
			key := normalizeLogicalMCPServerName(name)
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
		key := normalizeLogicalMCPServerName(name)
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
		var direct map[string]any
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

func extractBrowserOpenURLFromArgs(rawArgs string) (string, bool) {
	trimmed := strings.TrimSpace(rawArgs)
	if trimmed == "" {
		return "", false
	}

	var req BrowserRequest
	if err := json.Unmarshal([]byte(trimmed), &req); err == nil {
		action := strings.ToLower(strings.TrimSpace(req.Action))
		if action == "" {
			action = "open_url"
		}
		urlValue := strings.TrimSpace(req.URL)
		if action == "open_url" && urlValue != "" {
			return urlValue, true
		}
	}

	var direct struct {
		Action string `json:"action"`
		URL    string `json:"url"`
	}
	if err := json.Unmarshal([]byte(trimmed), &direct); err != nil {
		return "", false
	}
	action := strings.ToLower(strings.TrimSpace(direct.Action))
	if action == "" {
		action = "open_url"
	}
	urlValue := strings.TrimSpace(direct.URL)
	if action != "open_url" || urlValue == "" {
		return "", false
	}
	return urlValue, true
}

func isTransientBrowserNavigationError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(strings.TrimSpace(err.Error()))
	if text == "" {
		return false
	}
	markers := []string{
		"execution context was destroyed",
		"most likely because of a navigation",
		"cannot find context with specified id",
		"navigating frame was detached",
	}
	for _, marker := range markers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func maybeRecoverBrowserNavigationResult(toolName, toolArgs string, callErr error) (bool, string) {
	if !strings.EqualFold(strings.TrimSpace(toolName), "browser") {
		return false, ""
	}
	if !isTransientBrowserNavigationError(callErr) {
		return false, ""
	}
	openURL, ok := extractBrowserOpenURLFromArgs(toolArgs)
	if !ok {
		return false, ""
	}

	logger.Warn("Recovering browser open_url as successful after transient navigation context error", logger.Fields{
		"tool": toolName,
		"url":  openURL,
		"err":  callErr.Error(),
	})

	result := BrowserResponse{
		Action:  "open_url",
		Success: true,
		Result:  fmt.Sprintf("Opened %s. Navigation completed, but the browser connector reported a transient context reset.", openURL),
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return false, ""
	}
	return true, string(encoded)
}

func shouldPreferMCPToolOverUtility(toolName string) bool {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "browser", "web_search", "web_fetch":
		return true
	default:
		return false
	}
}

func shouldSuppressUtilityToolForAgent(ag *resolvedChatAgent, toolName string) bool {
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
// chatRequest is the JSON body of POST /api/chat.
type chatRequest struct {
	Question             string                `json:"question"`
	AgentName            string                `json:"agent_name,omitempty"` // Allow specifying target agent
	Files                []UploadedFile        `json:"files,omitempty"`
	RouteContext         *chatRouteContext     `json:"route_context,omitempty"`
	WorkflowResponse     *WorkflowUserResponse `json:"workflow_response,omitempty"`
	MultiAgentMode       string                `json:"multi_agent_mode,omitempty"`
	MultiAgentThreshold  float64               `json:"multi_agent_threshold,omitempty"`
	PlanBeforeAction     bool                  `json:"plan_before_action,omitempty"`
	ApprovedActionPlanID string                `json:"approved_action_plan_id,omitempty"`
}

// ChatHandler serves POST /api/chat. It runs the chat turn as a pipeline of
// stages, most of which can short-circuit by writing a response: parse and
// resolve the question -> resolve the execution agent -> pre-routing rewrites
// (slash commands, skills, direct tools) -> workspace routing and agent load
// -> planning/approval intercepts -> planner orchestration -> tool aggregation
// -> provider dispatch.
func (h *Handler) ChatHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	if !isTrustedChatRequestSource(r) {
		orihttp.Forbidden(w, "Request origin not allowed")
		return
	}

	var req chatRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	q := strings.TrimSpace(req.Question)
	if req.WorkflowResponse != nil {
		workflowPrompt, err := buildPromptFromWorkflowResponse(req.WorkflowResponse)
		if err != nil {
			orihttp.BadRequest(w, err.Error())
			return
		}
		q = workflowPrompt
	}
	if q == "" {
		orihttp.BadRequest(w, "empty question")
		return
	}
	originalQuery := q
	approvedActionPlanID := strings.TrimSpace(req.ApprovedActionPlanID)
	normalizedRouteContext := normalizeChatRouteContext(req.RouteContext)

	// Get session ID from header for multi-tab support
	sessionID := h.getSessionID(r)

	logger.Info("Chat request received", logger.Fields{
		"question":   q[:min(50, len(q))],
		"file_count": len(req.Files),
		"session_id": sessionID,
	})
	executionAgent := h.resolveExecutionAgentName(r.Context(), sessionID, req.AgentName, normalizedRouteContext.WorkspaceID)
	if executionAgent.usesCompatibilityFallback() {
		logger.Info("Chat request used compatibility execution-agent fallback", logger.Fields{
			"agent_name": executionAgent.Name,
			"source":     executionAgent.Source,
			"session_id": sessionID,
		})
	}
	for i, f := range req.Files {
		logger.Info("Received file", logger.Fields{
			"index": i,
			"name":  f.Name,
			"type":  f.Type,
			"size":  f.Size,
		})
	}
	if !executionAgent.isResolved() {
		responseText := "❌ **Error**: Assistant is unavailable because no execution agent is configured. Configure a System Model so Assistant can be created, or use a session pinned to a specific agent."
		reason := "no execution agent resolved"
		if strings.TrimSpace(normalizedRouteContext.WorkspaceID) != "" {
			responseText = "❌ **Error**: This workspace has no runnable entry agent. Add or repair the workspace's entry agent, then try again."
			reason = "workspace has no runnable entry agent"
		}
		writeJSONResponse(w, attachRouteMetadata(map[string]any{
			"response": responseText,
		}, chatRouteMetadata{
			Mode:   routeModeAssistantChat,
			Reason: reason,
		}))
		return
	}
	if h.maybeHandleAssistantSpecialistHandoff(w, r, originalQuery, sessionID, normalizedRouteContext, executionAgent) {
		return
	}

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

	// Separate image files from text files for proper API handling.
	imageFiles, textFiles := partitionUploadedFiles(req.Files)

	// For text files, prepend their content to the question.
	q = buildQuestionWithUploadedFiles(q, textFiles)

	// Handle slash commands that route directly to the command handler.
	if h.dispatchSlashCommand(w, r, q, executionAgent) {
		return
	}

	// Resolve an explicit ("/skill ...") or implicit skill invocation.
	invokedSkill, done := h.resolveInvokedSkill(w, q, executionAgent)
	if done {
		return
	}
	if strings.HasPrefix(q, "/tool ") {
		h.handleDirectToolCommand(w, r, req, q, executionAgent, normalizedRouteContext, approvedActionPlanID)
		return
	}
	logger.Debug("Chat question received", logger.Fields{"question": q})
	// Context with timeout per request (prevents indefinite hang)
	base := r.Context()
	ctx, cancel := context.WithTimeout(base, ContextTimeout)
	defer cancel()

	// Load the execution agent for this chat turn.
	current := executionAgent.Name
	routeDecision := classifyUtilityRoute(originalQuery)

	if invokedSkill == nil && routeNeedsWorkspace(routeDecision.Mode) {
		hadWorkspaceContext := strings.TrimSpace(normalizedRouteContext.WorkspaceID) != ""
		autoWorkspace, created, wsErr := h.ensureWorkspaceForRoute(current, originalQuery, routeDecision, normalizedRouteContext)
		if wsErr != nil {
			logger.Warn("Failed to ensure workspace for routed chat request", logger.Fields{
				"agent":        current,
				"route_mode":   routeDecision.Mode,
				"workspace_id": normalizedRouteContext.WorkspaceID,
				"error":        wsErr,
			})
		} else if autoWorkspace != nil {
			normalizedRouteContext = applyWorkspaceRouteContext(normalizedRouteContext, autoWorkspace)
			if created || !hadWorkspaceContext {
				q = enrichPromptWithWorkspaceContext(q, autoWorkspace, routeDecision.Mode, created)
			}
		}
	}

	ag, err := h.resolveEffectiveAgent(current, normalizedRouteContext)
	if err != nil {
		if errors.Is(err, errAgentPaused) {
			orihttp.Conflict(w, fmt.Sprintf("Agent %q is disabled. Turn Enabled on before sending messages.", current))
			return
		}
		orihttp.InternalError(w, fmt.Sprintf("agent '%s' not found", current))
		return
	}

	// Rehydrate conversation history from session store so the LLM has
	// full context across page reloads and server restarts.
	h.rehydrateSessionHistory(r.Context(), sessionID, ag)

	if h.maybeHandleWorkspacePlanningForm(w, base, ag, current, originalQuery, sessionID, normalizedRouteContext) {
		return
	}

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

	preflight := h.maybeAutoEnableMCPForPrompt(current, ag, originalQuery, normalizedRouteContext)
	if preflight != nil && strings.TrimSpace(preflight.userMessage) != "" {
		payload := attachRouteMetadata(map[string]any{
			"response": preflight.userMessage,
		}, chatRouteMetadata{
			Mode:   routeModeAssistantChat,
			Reason: "mcp preflight notice",
		})
		writeJSONResponse(w, attachDependencyResolution(payload, preflight.dependencyResolution))
		return
	}

	if h.maybeRejectSkillMissingMCP(w, ag, invokedSkill, normalizedRouteContext) {
		return
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
	plannerDecision, handled := h.maybeRunPlannerOrchestration(ctx, w, q, current, req.MultiAgentMode, req.MultiAgentThreshold)
	if handled {
		return
	}

	tools := h.buildChatToolList(ag, current, invokedSkill)

	if invokedSkill == nil {
		if h.maybeHandleCapabilityRecovery(w, base, ag, current, originalQuery, sessionID, tools, plannerDecision) {
			return
		}
	}

	// Store user message in session if session ID is provided
	h.storeMessageInSession(r.Context(), sessionID, "user", sessionQuery)

	if h.maybeHandleWorkspaceSaveNoteWithoutModel(w, ag, current, originalQuery, base, sessionID, plannerDecision) {
		return
	}

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

	// Carry the resolved agent so the runtime prompt can layer this agent's
	// per-workspace refinement (PRD FR15/FR16/FR19).
	normalizedRouteContext.AgentName = current
	toolRuntimeSystemPrompt := h.buildRuntimeSystemPrompt(ctx, normalizedRouteContext)
	providerName, providerErr := resolveChatProviderName(current, ag, h.llmFactory)
	if providerErr != nil {
		writeJSONResponse(w, attachRouteMetadata(map[string]any{
			"response": fmt.Sprintf("❌ **Error**: %v", providerErr),
		}, chatRouteMetadata{
			Mode:   routeModeAssistantChat,
			Reason: "agent provider misconfigured",
		}))
		return
	}

	// Check if this is a Claude Code provider - route to Claude Code handler
	if providerName == "claude_code" && h.llmFactory != nil {
		runtimeSystemPrompt := h.buildRuntimeSystemPromptForToolCapability(ctx, normalizedRouteContext, false)
		h.handleClaudeCodeChat(w, r, ag, q, current, base, llmImages, plannerDecision, runtimeSystemPrompt, normalizedRouteContext)
		return
	}

	// Route Anthropic-backed models through the Claude provider.
	if providerName == "claude" && h.llmFactory != nil {
		// Use Claude provider
		h.handleClaudeChat(w, r, ag, q, tools, current, base, fileAttachments, llmImages, plannerDecision, toolRuntimeSystemPrompt)
		return
	}

	if llm.IsLocalProviderName(providerName) {
		h.handleLocalProviderChat(w, r, ag, q, tools, current, base, fileAttachments, llmImages, plannerDecision, toolRuntimeSystemPrompt, providerName)
		return
	}

	if providerName == "gemini" {
		h.handleGeminiChat(w, r, ag, q, tools, current, base, fileAttachments, llmImages, plannerDecision, toolRuntimeSystemPrompt)
		return
	}

	if providerName == "codex" && h.llmFactory != nil {
		runtimeSystemPrompt := h.buildRuntimeSystemPromptForToolCapability(ctx, normalizedRouteContext, false)
		h.handleCodexChat(w, r, ag, q, current, base, llmImages, plannerDecision, runtimeSystemPrompt, normalizedRouteContext)
		return
	}

	if providerName != "openai" {
		writeJSONResponse(w, attachRouteMetadata(map[string]any{
			"response": fmt.Sprintf("❌ **Error**: Agent %q is configured with unsupported provider %q.", current, providerName),
		}, chatRouteMetadata{
			Mode:   routeModeAssistantChat,
			Reason: "unsupported provider",
		}))
		return
	}

	// OpenAI models require an API key; return a clear error if none is configured.
	if h.clientFactory != nil && !h.clientFactory.HasKeyForAgent(ag.Agent) {
		writeJSONResponse(w, map[string]any{
			"response": "❌ **Error**: OpenAI API key is not configured. Set `OPENAI_API_KEY` for the server process, or add an API key in the app Settings.",
		})
		return
	}

	// Handle OpenAI models
	agentClient := h.getClientForAgent(ag.Agent)
	h.handleOpenAIChat(w, r, ag, q, tools, current, base, fileAttachments, llmImages, agentClient, plannerDecision, toolRuntimeSystemPrompt)
}

// handleDirectToolCommand executes a "/tool <name> {json}" chat message
// directly, bypassing LLM decision-making. It always writes the response.
func (h *Handler) handleDirectToolCommand(w http.ResponseWriter, r *http.Request, req chatRequest, q string, executionAgent executionAgentResolution, normalizedRouteContext normalizedChatRouteContext, approvedActionPlanID string) {
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
	current := executionAgent.Name
	if current == "" {
		orihttp.InternalError(w, "no agent available for direct tool execution")
		return
	}
	ag, err := h.resolveEffectiveAgent(current, normalizedRouteContext)
	if err != nil {
		if errors.Is(err, errAgentPaused) {
			orihttp.Conflict(w, fmt.Sprintf("Agent %q is disabled. Turn Enabled on before starting a chat or running tools.", current))
			return
		}
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

	result := h.executeDirectTool(r.Context(), ag, cmd)

	// Add to conversation history for context
	ag.Messages = append(ag.Messages, openai.UserMessage(q))
	ag.Messages = append(ag.Messages, openai.AssistantMessage(result.Result))
	_ = h.persistAgent(current, ag.Agent)

	// Return formatted response
	response := formatDirectToolResponse(result)
	writeJSONResponse(w, response)
	return
}

// maybeHandleWorkspacePlanningForm intercepts prompts that require a
// workspace planning form before any model call, returning true when it has
// written the form response.
func (h *Handler) maybeHandleWorkspacePlanningForm(w http.ResponseWriter, base context.Context, ag *resolvedChatAgent, current, originalQuery, sessionID string, normalizedRouteContext normalizedChatRouteContext) bool {
	if planningResp := maybeBuildWorkspacePlanningFormResponse(ag, originalQuery, normalizedRouteContext, h.workspaceStore, h.sessionStore); planningResp != nil && planningResp.Form != nil {
		responseText := strings.TrimSpace(planningResp.ResponseText)
		if responseText == "" {
			responseText = "Complete the planning step below."
		}

		ag.Messages = append(ag.Messages, openai.UserMessage(originalQuery))
		ag.Messages = append(ag.Messages, openai.AssistantMessage(responseText))
		_ = h.persistAgent(current, ag.Agent)

		h.storeMessageInSession(base, sessionID, "user", originalQuery)
		h.storeMessageInSession(base, sessionID, "assistant", responseText)

		writeJSONResponse(w, attachRouteMetadata(map[string]any{
			"response":      responseText,
			"planning_form": planningResp.Form,
			"workflow_step": buildWorkflowStepFromPlanningForm(planningResp.Form, sessionID),
		}, chatRouteMetadata{
			Mode:   routeModeAssistantChat,
			Reason: "workspace planning form required",
		}))
		return true
	}
	return false
}

// maybeRejectSkillMissingMCP rejects an invoked skill whose required MCP
// connectors are not enabled on the agent, returning true when it has written
// the dependency-resolution response.
func (h *Handler) maybeRejectSkillMissingMCP(w http.ResponseWriter, ag *resolvedChatAgent, invokedSkill *skillInvocation, normalizedRouteContext normalizedChatRouteContext) bool {
	if invokedSkill != nil && len(invokedSkill.Skill.RequiredMCPServers) > 0 {
		missing := missingMCPServers(ag.MCPServers, invokedSkill.Skill.RequiredMCPServers)
		if len(missing) > 0 {
			primaryServer := missing[0]
			preferenceKey := ""
			workspaceID := strings.TrimSpace(normalizedRouteContext.WorkspaceID)
			if workspaceID != "" {
				preferenceKey = workspace.DependencyPreferenceKey(dependencyTypeWorkspaceMCP, primaryServer)
			}
			writeJSONResponse(w, attachDependencyResolution(map[string]any{
				"response": fmt.Sprintf("❌ Skill '%s' requires MCP connectors: %s. Bind them from the target workspace.", invokedSkill.Skill.Name, strings.Join(missing, ", ")),
			}, &dependencyResolution{
				Version:            1,
				Title:              "Required MCP connectors are not enabled",
				Summary:            fmt.Sprintf("Enable the required connector for skill \"%s\" before retrying.", invokedSkill.Skill.Name),
				ReasonCode:         "skill_required_mcp_missing",
				RecommendedSurface: dependencyResolutionSurfaceModal,
				RetryContext:       buildDefaultRetryContext(workspaceID != ""),
				Steps: []dependencyResolutionStep{
					{
						ID:           "skill-required-mcp",
						Type:         dependencyTypeWorkspaceMCP,
						DisplayName:  primaryServer,
						Summary:      fmt.Sprintf("Enable %s in the current workspace to run \"%s\".", primaryServer, invokedSkill.Skill.Name),
						RiskLevel:    "low",
						Suppressible: workspaceID != "",
						Actions:      buildSkillRequiredMCPActions(workspaceID, primaryServer, preferenceKey),
					},
				},
			}))
			return true
		}
	}
	return false
}

// maybeRunPlannerOrchestration runs planner-first multi-agent routing. When
// the planner selects (and successfully executes) a multi-agent plan it
// writes the orchestration response and returns handled=true; otherwise it
// returns the planner decision (which may be nil) for the single-agent path.
func (h *Handler) maybeRunPlannerOrchestration(ctx context.Context, w http.ResponseWriter, q, current, requestedMode string, requestedThreshold float64) (*types.PlannerDecision, bool) {
	var plannerDecision *types.PlannerDecision
	if h.orchestrator == nil {
		return nil, false
	}

	mode, threshold := h.orchestrator.GetMultiAgentDefaults()
	if requestedMode != "" {
		if parsed, ok := types.ParseMultiAgentMode(strings.ToLower(strings.TrimSpace(requestedMode))); ok {
			mode = parsed
		}
	}
	if requestedThreshold > 0 {
		threshold = requestedThreshold
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
						"workspace_id":           result.WorkspaceID,
						"status":                 result.Status,
						"pending_plan_id":        result.PendingPlanID,
						"planner_decision":       result.PlannerDecision,
						"planner_plan":           plan,
						"dynamic_agent_requests": result.DynamicAgentRequests,
					}, chatRouteMetadata{
						Mode:   routeModeSpecialistFlow,
						Reason: "planner selected multi-agent execution",
					}), []ActionReceipt{receipt}))
					return plannerDecision, true
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
	return plannerDecision, false
}

// buildChatToolList aggregates the tool definitions offered to the model for
// this turn: native utility tools, workspace-scoped tools, and MCP tools from
// the agent's enabled servers. Duplicate names keep the first definition
// except when an MCP tool should supersede a same-named utility tool. The
// list is then filtered for an invoked skill and path-prioritized.
func (h *Handler) buildChatToolList(ag *resolvedChatAgent, current string, invokedSkill *skillInvocation) []llm.Tool {
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
			if !isUtilityToolAllowedForAgent(ag.Agent, def.Name) {
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

	// Add workspace-scoped tools when in a workspace context
	if ag.WorkspaceTools != nil {
		wsTools := ag.WorkspaceTools.Tools()
		logger.Info("Adding workspace tools to LLM request", logger.Fields{
			"count": len(wsTools),
		})
		for _, wt := range wsTools {
			def := wt.Definition()
			logger.Debug("Registering workspace tool", logger.Fields{"name": def.Name})
			appendTool(llm.Tool{
				Name:        def.Name,
				Description: def.Description,
				Parameters:  def.Parameters,
			}, "workspace")
		}
	} else {
		logger.Debug("No workspace tools to add (WorkspaceTools is nil)")
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
	tools = prioritizeToolsForPath(ag.Agent, tools)
	return tools
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

func isTrustedChatRequestSource(r *http.Request) bool {
	if r == nil {
		return false
	}

	origin := strings.TrimSpace(r.Header.Get("Origin"))
	referer := strings.TrimSpace(r.Header.Get("Referer"))
	if origin == "" && referer == "" {
		// Non-browser clients (CLI/tests) may omit browser source headers.
		return true
	}

	requestHost, requestPort := splitHostPortForSourceCheck(r.Host)
	if requestHost == "" {
		return false
	}

	if origin != "" && !sourceURLMatchesRequestHost(origin, requestHost, requestPort) {
		return false
	}
	if referer != "" && !sourceURLMatchesRequestHost(referer, requestHost, requestPort) {
		return false
	}
	return true
}

func sourceURLMatchesRequestHost(rawSourceURL, requestHost, requestPort string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawSourceURL))
	if err != nil {
		return false
	}

	sourceHost, sourcePort := splitHostPortForSourceCheck(parsed.Host)
	if sourceHost == "" {
		return false
	}

	sameHost := strings.EqualFold(sourceHost, requestHost)
	sameLoopback := isLoopbackHost(sourceHost) && isLoopbackHost(requestHost)
	if !sameHost && !sameLoopback {
		return false
	}

	// Only enforce strict port equality when both sides provide explicit ports.
	if sourcePort != "" && requestPort != "" && sourcePort != requestPort {
		return false
	}
	// If request host omits a port, only allow default browser ports from source.
	if sourcePort != "" && requestPort == "" && sourcePort != "80" && sourcePort != "443" {
		return false
	}
	return true
}

func splitHostPortForSourceCheck(rawHost string) (string, string) {
	host := strings.TrimSpace(rawHost)
	if host == "" {
		return "", ""
	}
	u := &url.URL{Host: host}
	return strings.ToLower(strings.TrimSpace(u.Hostname())), strings.TrimSpace(u.Port())
}

func isLoopbackHost(host string) bool {
	candidate := strings.ToLower(strings.TrimSpace(host))
	if candidate == "" {
		return false
	}
	if candidate == "localhost" {
		return true
	}
	ip := net.ParseIP(candidate)
	return ip != nil && ip.IsLoopback()
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
