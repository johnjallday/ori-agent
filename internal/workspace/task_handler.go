package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/toolapi"
)

// classifyContextError returns a user-friendly error message based on the context error type
func classifyContextError(err error) string {
	if err == nil {
		return ""
	}

	// Check for context errors
	if errors.Is(err, context.DeadlineExceeded) {
		return "task timed out - the operation took too long to complete. Consider breaking the task into smaller parts or increasing the timeout."
	}
	if errors.Is(err, context.Canceled) {
		return "task was canceled - this may happen if the server was restarted or the task was manually stopped."
	}

	// Check for wrapped context errors in the error chain
	errStr := err.Error()
	if strings.Contains(errStr, "context deadline exceeded") {
		return "task timed out - the operation took too long to complete. Consider breaking the task into smaller parts or increasing the timeout."
	}
	if strings.Contains(errStr, "context canceled") {
		return "task was canceled - this may happen if the server was restarted or the task was manually stopped."
	}

	return ""
}

// LLMTaskHandler executes tasks using the LLM system
type LLMTaskHandler struct {
	agentStore       store.Store
	llmFactory       *llm.Factory
	workspaceStore   Store // Added to access workspace attachments
	contextStore     taskPromptContextStore
	eventBus         *EventBus // Optional event bus for publishing execution events
	mcpRegistry      mcpRegistry
	runtimeResolver  *AgentRuntimeResolver
	workspaceToolsFn WorkspaceToolFactory
	utilityTools     UtilityToolProvider
}

type mcpRegistry interface {
	GetToolsForServer(string) ([]toolapi.Tool, error)
	StartServer(string) error
}

// UtilityToolProvider exposes native utility tools (time, weather, web search,
// browser, etc.) to task execution without coupling workspace to chathttp.
type UtilityToolProvider interface {
	GetTool(string) (toolapi.Tool, bool)
}

// WorkspaceToolFactory returns workspace-scoped tools (notes, tasks, sessions, files, etc.)
// for use during task execution. Tools are constructed per workspace so the agent can
// read and update workspace state without forcing the user to paste it into the prompt.
type WorkspaceToolFactory func(workspaceID string) []toolapi.Tool

type resolvedTaskAgent struct {
	*agent.Agent
	MCPServers []string
}

const (
	maxTaskToolRounds          = 6
	maxTaskToolResultFollowups = 1
)

var (
	browserIntentWordPattern = regexp.MustCompile(`\b(open|visit|navigate|browse|click|fill|type|extract)\b`)
	browserIntentGoToPattern = regexp.MustCompile(`\bgo\s+to\b`)
	browserDomainPattern     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*(\.[a-z0-9-]+)+$`)
)

var taskUtilityToolNames = []string{"time", "weather", "air_quality", "web_search", "web_fetch", "browser"}

var browserLikeFileExtensions = map[string]struct{}{
	"app": {}, "csv": {}, "doc": {}, "docx": {}, "gif": {}, "go": {}, "gz": {}, "heic": {}, "jpeg": {}, "jpg": {},
	"js": {}, "json": {}, "key": {}, "md": {}, "mov": {}, "mp3": {}, "mp4": {}, "numbers": {}, "pages": {}, "pdf": {}, "png": {},
	"ppt": {}, "pptx": {}, "py": {}, "rb": {}, "sh": {}, "svg": {}, "tar": {}, "ts": {}, "txt": {}, "wav": {}, "webp": {}, "xlsx": {}, "xls": {}, "zip": {},
}

// NewLLMTaskHandler creates a new LLM-based task handler
func NewLLMTaskHandler(agentStore store.Store, llmFactory *llm.Factory, workspaceStore Store) *LLMTaskHandler {
	return &LLMTaskHandler{
		agentStore:     agentStore,
		llmFactory:     llmFactory,
		workspaceStore: workspaceStore,
	}
}

// SetEventBus sets the event bus for publishing execution progress events
func (h *LLMTaskHandler) SetEventBus(eventBus *EventBus) {
	h.eventBus = eventBus
}

// SetMCPRegistry enables MCP tool resolution for workspace task execution.
func (h *LLMTaskHandler) SetMCPRegistry(registry mcpRegistry) {
	h.mcpRegistry = registry
}

// SetRuntimeResolver configures workspace-aware runtime MCP resolution for task execution.
func (h *LLMTaskHandler) SetRuntimeResolver(resolver *AgentRuntimeResolver) {
	h.runtimeResolver = resolver
}

// SetContextStore configures optional workspace note/session summaries for task prompts.
func (h *LLMTaskHandler) SetContextStore(store taskPromptContextStore) {
	h.contextStore = store
}

// SetWorkspaceToolFactory wires workspace-scoped tools (notes, tasks, sessions, files)
// into task execution. Without this, task agents only see the truncated workspace
// snapshot embedded in the prompt and cannot fetch full note content on demand.
func (h *LLMTaskHandler) SetWorkspaceToolFactory(fn WorkspaceToolFactory) {
	h.workspaceToolsFn = fn
}

// SetUtilityToolProvider wires native utility tools into task execution. Web
// tools are still filtered per assigned-agent settings.
func (h *LLMTaskHandler) SetUtilityToolProvider(provider UtilityToolProvider) {
	h.utilityTools = provider
}

// ExecuteTask executes a task by sending it to the agent's LLM
func (h *LLMTaskHandler) ExecuteTask(ctx context.Context, agentName string, task Task) (string, error) {
	logger.Debug("🤖 Executing task for agent", logger.Fields{"task_id": task.ID, "agentName": agentName})

	// Publish thinking event
	if h.eventBus != nil {
		event := NewTaskEvent(EventTaskThinking, task.WorkspaceID, task.ID, agentName, map[string]interface{}{
			"message": "Agent is analyzing the task...",
		})
		h.eventBus.Publish(event)
	}

	// Get the agent
	ag, err := h.resolveExecutionAgent(agentName, task)
	if err != nil {
		return "", err
	}

	// Guard common browser-automation intents to avoid misleading "completed" responses
	// when the assigned agent cannot actually drive web tooling.
	if taskRequiresBrowserAutomation(task) && !h.agentSupportsBrowserAutomation(ag) {
		return "", &TaskBlockedError{
			ReasonCode: "capability_mismatch",
			Reason:     fmt.Sprintf("%s cannot execute browser actions for this task", agentName),
			Question:   "Switch to an agent with browser capability and retry?",
			SuggestedActions: []string{
				"switch_agent_retry",
				"continue_with_instruction",
				"retry",
				"mark_failed",
			},
		}
	}

	// Determine which provider to use based on explicit agent provider + model fallback.
	providerName := h.getProviderForAgent(ag.Settings.Provider, ag.Settings.Model)
	provider, err := h.llmFactory.GetProvider(providerName)
	if err != nil {
		return "", fmt.Errorf("failed to get LLM provider: %w", err)
	}
	modelName := h.normalizeModelForProvider(providerName, ag.Settings.Model)

	// Build the prompt for the task
	prompt := h.buildTaskPrompt(ctx, task)

	// Prepare messages
	messages := []llm.Message{
		llm.NewUserMessage(prompt),
	}

	// Use a task-specific system prompt that's more conservative about tool use
	// The agent's system prompt may encourage aggressive tool use which is inappropriate for workspace tasks
	taskSystemPrompt := h.buildTaskSystemPrompt()

	messages = append([]llm.Message{llm.NewSystemMessage(taskSystemPrompt)}, messages...)

	// Convert agent tools (MCP + workspace) to LLM format
	tools := h.convertAgentToolsToLLMTools(ag, task)

	// Call the LLM
	return h.executeTaskConversation(ctx, provider, providerName, modelName, ag, agentName, task, messages, tools)
}

func (h *LLMTaskHandler) resolveExecutionAgent(agentName string, task Task) (*resolvedTaskAgent, error) {
	normalizedAgentName := strings.TrimSpace(agentName)

	if h.runtimeResolver != nil {
		resolved, err := h.runtimeResolver.ResolveAgentForTask(normalizedAgentName, task)
		if err != nil {
			if blockedErr := h.buildMissingAssignedAgentBlockedError(normalizedAgentName, task); blockedErr != nil {
				return nil, blockedErr
			}
			return nil, err
		}
		if resolved != nil && resolved.Agent != nil {
			return &resolvedTaskAgent{
				Agent:      resolved.Agent,
				MCPServers: append([]string{}, resolved.MCPServers...),
			}, nil
		}
	}

	if localAgent, ok := h.getWorkspaceLocalAgentSnapshot(task.WorkspaceID, normalizedAgentName); ok {
		return &resolvedTaskAgent{Agent: localAgent}, nil
	}

	ag, ok := h.agentStore.GetAgent(normalizedAgentName)
	if !ok {
		if blockedErr := h.buildMissingAssignedAgentBlockedError(normalizedAgentName, task); blockedErr != nil {
			return nil, blockedErr
		}
		return nil, fmt.Errorf("agent %s not found", normalizedAgentName)
	}
	return &resolvedTaskAgent{Agent: ag}, nil
}

func (h *LLMTaskHandler) getWorkspaceLocalAgentSnapshot(workspaceID, agentName string) (*agent.Agent, bool) {
	if h == nil || h.workspaceStore == nil {
		return nil, false
	}
	workspaceID = strings.TrimSpace(workspaceID)
	agentName = strings.TrimSpace(agentName)
	if workspaceID == "" || agentName == "" {
		return nil, false
	}

	local, ok, err := h.workspaceStore.GetWorkspaceAgent(workspaceID, agentName)
	if err != nil {
		logger.Warn("workspace-local task agent lookup failed", logger.Fields{
			"workspace_id": workspaceID,
			"agent":        agentName,
			"error":        err.Error(),
		})
		return nil, false
	}
	return local, ok && local != nil
}

func (h *LLMTaskHandler) buildMissingAssignedAgentBlockedError(agentName string, task Task) *TaskBlockedError {
	if h == nil || h.agentStore == nil {
		return nil
	}

	normalizedAgentName := strings.TrimSpace(agentName)
	if normalizedAgentName == "" {
		return nil
	}

	if existing, ok := h.agentStore.GetAgent(normalizedAgentName); ok && existing != nil {
		return nil
	}

	if _, ok := h.getWorkspaceLocalAgentSnapshot(task.WorkspaceID, normalizedAgentName); ok {
		return nil
	}

	isWorkspaceAgent := false
	if strings.TrimSpace(task.WorkspaceID) != "" && h.workspaceStore != nil {
		if ws, err := h.workspaceStore.Get(task.WorkspaceID); err == nil && ws != nil {
			isWorkspaceAgent = ws.HasAgent(normalizedAgentName)
		}
	}

	reason := fmt.Sprintf("Assigned agent %s is no longer available.", normalizedAgentName)
	question := fmt.Sprintf(
		"This task is assigned to %s, but that agent no longer exists. Switch to another agent or recreate it, then retry.",
		normalizedAgentName,
	)
	if isWorkspaceAgent {
		reason = fmt.Sprintf("Assigned workspace agent %s is no longer available.", normalizedAgentName)
		question = fmt.Sprintf(
			"This task still points at workspace agent %s, but that agent no longer exists as a runnable definition. Switch to another agent or recreate it, then retry.",
			normalizedAgentName,
		)
	}

	if availableAgents := h.listAvailableExecutionAgents(normalizedAgentName); len(availableAgents) > 0 {
		question = fmt.Sprintf(
			"%s %d other runnable agent%s %s currently available.",
			question,
			len(availableAgents),
			map[bool]string{true: "", false: "s"}[len(availableAgents) == 1],
			map[bool]string{true: "is", false: "are"}[len(availableAgents) == 1],
		)
	}

	return &TaskBlockedError{
		ReasonCode: "assigned_agent_missing",
		Reason:     reason,
		Question:   question,
		SuggestedActions: []string{
			"switch_agent_retry",
			"mark_failed",
		},
	}
}

func (h *LLMTaskHandler) listAvailableExecutionAgents(excludeAgent string) []string {
	if h == nil || h.agentStore == nil {
		return nil
	}

	names := append([]string(nil), h.agentStore.ListAgents()...)
	sort.Strings(names)

	excludedKey := strings.ToLower(strings.TrimSpace(excludeAgent))
	seen := make(map[string]struct{}, len(names))
	available := make([]string, 0, len(names))
	for _, candidate := range names {
		trimmed := strings.TrimSpace(candidate)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if key == excludedKey {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		available = append(available, trimmed)
	}

	return available
}

func (h *LLMTaskHandler) executeTaskConversation(
	ctx context.Context,
	provider llm.Provider,
	providerName string,
	modelName string,
	ag *resolvedTaskAgent,
	agentName string,
	task Task,
	messages []llm.Message,
	tools []llm.Tool,
) (string, error) {
	conversation := append([]llm.Message(nil), messages...)
	var lastToolSummary string
	toolResultFollowups := 0

	for round := 0; round < maxTaskToolRounds; round++ {
		resp, err := provider.Chat(ctx, llm.ChatRequest{
			Model:           modelName,
			Messages:        conversation,
			Temperature:     ag.Settings.Temperature,
			ReasoningEffort: ag.Settings.EffectiveReasoningEffort(providerName),
			Tools:           tools,
		})
		if err != nil {
			if friendlyMsg := classifyContextError(err); friendlyMsg != "" {
				return "", fmt.Errorf("%s", friendlyMsg)
			}
			return "", fmt.Errorf("LLM call failed: %w", err)
		}

		if len(resp.ToolCalls) == 0 {
			if strings.TrimSpace(resp.Content) == "" {
				if strings.TrimSpace(lastToolSummary) != "" {
					if toolResultFollowups < maxTaskToolResultFollowups {
						toolResultFollowups++
						conversation = append(conversation, llm.NewUserMessage(buildToolResultFollowupPrompt(task)))
						continue
					}
					return "", buildToolOnlyBlockedError(lastToolSummary)
				}
				return "Task completed (no output)", nil
			}

			if taskRequiresBrowserAutomation(task) && looksLikeBrowserCapabilityRefusal(resp.Content) {
				return "", &TaskBlockedError{
					ReasonCode: "capability_refusal",
					Reason:     fmt.Sprintf("%s responded with a capability refusal for a browser task", agentName),
					Question:   "Would you like to switch agents, add guidance, or retry?",
					SuggestedActions: []string{
						"switch_agent_retry",
						"continue_with_instruction",
						"retry",
						"mark_failed",
					},
					RawResponse: resp.Content,
				}
			}

			return resp.Content, nil
		}

		logger.Debug("Task triggered tool call(s)", logger.Fields{"task_id": task.ID, "toolcalls)": len(resp.ToolCalls), "round": round + 1})
		conversationToolCalls := make([]llm.ToolCall, len(resp.ToolCalls))
		copy(conversationToolCalls, resp.ToolCalls)
		for index := range conversationToolCalls {
			if strings.TrimSpace(conversationToolCalls[index].ID) == "" {
				conversationToolCalls[index].ID = fmt.Sprintf("tool_%d_%d", round+1, index+1)
			}
		}
		conversation = append(conversation, llm.Message{
			Role:      llm.RoleAssistant,
			Content:   resp.Content,
			ToolCalls: conversationToolCalls,
		})

		toolResults := h.executeToolCalls(ctx, ag, agentName, task, resp.ToolCalls)
		lastToolSummary = buildToolResultsSummary(resp.Content, toolResults)
		for index, tr := range toolResults {
			toolContent := tr.Result
			if tr.Error != nil {
				toolContent = fmt.Sprintf("ERROR: %s", tr.Error.Error())
			}

			toolCallID := strings.TrimSpace(conversationToolCalls[index].ID)
			conversation = append(conversation, llm.NewToolMessage(toolCallID, toolContent))
		}
	}

	if strings.TrimSpace(lastToolSummary) != "" {
		return "", buildToolOnlyBlockedError(lastToolSummary)
	}

	return "", fmt.Errorf("task exceeded %d tool rounds without a final answer", maxTaskToolRounds)
}

func buildToolResultFollowupPrompt(task Task) string {
	var prompt strings.Builder
	prompt.WriteString("The previous tool result is not a final answer. Continue from the tool result and return a concise answer to the task. ")
	prompt.WriteString("Do not return raw Tool Results. If a search result is empty, too narrow, wrong-location, or source-specific, broaden the search instead of stopping. ")
	prompt.WriteString("Do not restrict search to one website unless the user explicitly asked for that source. ")
	prompt.WriteString("For public-information tasks, verify the source matches the requested city, region, ZIP, and date when available; discard sources that show a different location or no location. ")
	prompt.WriteString("If you still cannot complete the task after trying reasonable source discovery, explain the specific blocker and what source or permission is missing.")
	if strings.TrimSpace(task.Description) != "" {
		prompt.WriteString("\n\nTask: ")
		prompt.WriteString(strings.TrimSpace(task.Description))
	}
	return prompt.String()
}

func buildToolOnlyBlockedError(rawResponse string) *TaskBlockedError {
	trimmed := strings.TrimSpace(rawResponse)
	reasonCode := "tool_only_result"
	reason := "The agent returned raw tool output instead of a final answer."
	question := "Retry this task and require the agent to synthesize the tool result into an answer?"
	if toolSummaryLooksLikeEmptyWebSearch(trimmed) {
		reasonCode = "empty_web_search_results"
		reason = "The web search returned no results and the agent did not broaden the search or synthesize an answer."
		question = "Retry this task with a broader search across public sources?"
	}

	return &TaskBlockedError{
		ReasonCode: reasonCode,
		Reason:     reason,
		Question:   question,
		SuggestedActions: []string{
			"retry",
			"continue_with_instruction",
			"switch_agent_retry",
			"mark_failed",
		},
		RawResponse: trimmed,
	}
}

func toolSummaryLooksLikeEmptyWebSearch(summary string) bool {
	normalized := strings.ToLower(strings.TrimSpace(summary))
	if !strings.Contains(normalized, "web_search") {
		return false
	}
	compacted := strings.NewReplacer(" ", "", "\n", "", "\t", "", "\r", "").Replace(normalized)
	return strings.Contains(compacted, `"results":[]`) ||
		strings.Contains(compacted, `"results":null`) ||
		strings.Contains(normalized, "no search results") ||
		strings.Contains(normalized, "no results found")
}

func buildToolResultsSummary(content string, toolResults []toolCallResult) string {
	var resultBuilder strings.Builder
	if strings.TrimSpace(content) != "" {
		resultBuilder.WriteString(strings.TrimSpace(content))
		resultBuilder.WriteString("\n\n")
	}

	resultBuilder.WriteString("Tool Results:\n")
	for _, tr := range toolResults {
		if tr.Error != nil {
			resultBuilder.WriteString(fmt.Sprintf("- %s: ERROR: %s\n", tr.Name, tr.Error.Error()))
		} else {
			resultBuilder.WriteString(fmt.Sprintf("- %s: %s\n", tr.Name, tr.Result))
		}
	}

	return strings.TrimSpace(resultBuilder.String())
}

func normalizeProviderName(provider string) string {
	normalized := strings.ToLower(strings.TrimSpace(provider))
	switch normalized {
	case "anthropic":
		return "claude"
	default:
		return normalized
	}
}

func isClaudeFamilyModel(model string) bool {
	normalized := strings.ToLower(strings.TrimSpace(model))
	if normalized == "" {
		return false
	}
	if strings.HasPrefix(normalized, "claude-") {
		return true
	}
	return normalized == "haiku" || normalized == "sonnet" || normalized == "opus"
}

func isGeminiFamilyModel(model string) bool {
	normalized := strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(normalized, "gemini")
}

func isCodexFamilyModel(model string) bool {
	normalized := strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(normalized, "codex")
}

func (h *LLMTaskHandler) normalizeModelForProvider(providerName, model string) string {
	trimmedModel := strings.TrimSpace(model)
	normalizedModel := strings.ToLower(trimmedModel)

	if providerName == "claude" {
		switch normalizedModel {
		case "haiku":
			return "claude-3-5-haiku-latest"
		case "sonnet":
			return "claude-3-5-sonnet-latest"
		case "opus":
			return "claude-3-opus-latest"
		}
	}

	return trimmedModel
}

// getProviderForAgent resolves the best provider for the agent's configured provider/model.
func (h *LLMTaskHandler) getProviderForAgent(configuredProvider, model string) string {
	explicitProvider := normalizeProviderName(configuredProvider)
	inferredProvider := h.getProviderForModel(model)

	if explicitProvider == "" {
		return inferredProvider
	}

	if h.llmFactory.HasProvider(explicitProvider) {
		// Auto-correct common stale mismatch cases where provider was not persisted with model updates.
		if explicitProvider == "openai" &&
			inferredProvider != "" &&
			inferredProvider != "openai" &&
			(isClaudeFamilyModel(model) || isGeminiFamilyModel(model) || isCodexFamilyModel(model)) {
			logFields := logger.Fields{
				"configured_provider": explicitProvider,
				"inferred_provider":   inferredProvider,
				"model":               model,
			}
			if h.llmFactory.HasProvider(inferredProvider) {
				logger.Warn("Detected provider/model mismatch; using inferred provider for task execution", logFields)
				return inferredProvider
			}
			logger.Warn("Detected provider/model mismatch; inferred provider is not configured, keeping configured provider", logFields)
			return explicitProvider
		}

		// Claude API does not accept short Claude Code model aliases.
		if explicitProvider == "claude" && isClaudeFamilyModel(model) &&
			(strings.EqualFold(strings.TrimSpace(model), "haiku") ||
				strings.EqualFold(strings.TrimSpace(model), "sonnet") ||
				strings.EqualFold(strings.TrimSpace(model), "opus")) &&
			h.llmFactory.HasProvider("claude_code") {
			logger.Warn("Detected Claude short model alias; using claude_code provider", logger.Fields{
				"configured_provider": explicitProvider,
				"inferred_provider":   "claude_code",
				"model":               model,
			})
			return "claude_code"
		}

		return explicitProvider
	}

	if inferredProvider != "" && h.llmFactory.HasProvider(inferredProvider) {
		logger.Warn("Configured provider unavailable; falling back to inferred provider", logger.Fields{
			"configured_provider": explicitProvider,
			"inferred_provider":   inferredProvider,
			"model":               model,
		})
		return inferredProvider
	}

	// Preserve configured name for clearer upstream error messaging if no fallback exists.
	return explicitProvider
}

// substitutePlaceholders replaces placeholders like {result}, {input}, {result1}, {result2} in task description
func (h *LLMTaskHandler) substitutePlaceholders(task Task) string {
	description := task.Description

	// Get input task results from runtime inputs (rebuilt fresh each execution).
	if task.RuntimeInputs == nil {
		return description
	}
	resultsMap := task.RuntimeInputs.TaskResults
	if len(resultsMap) == 0 {
		return description
	}

	// Extract actual result values (strip "Tool Results:" prefix if present)
	var resultValues []string
	for _, result := range resultsMap {
		cleanResult := h.cleanToolResult(result)
		if cleanResult != "" {
			resultValues = append(resultValues, cleanResult)
		}
	}

	if len(resultValues) == 0 {
		return description
	}

	// Replace placeholders
	// {result} or {input} - use the first result (or combined if multiple)
	if strings.Contains(description, "{result}") || strings.Contains(description, "{input}") {
		combinedResult := strings.Join(resultValues, ", ")
		description = strings.ReplaceAll(description, "{result}", combinedResult)
		description = strings.ReplaceAll(description, "{input}", combinedResult)
	}

	// {result1}, {result2}, etc. - use specific results by index
	for i, result := range resultValues {
		placeholder := fmt.Sprintf("{result%d}", i+1)
		description = strings.ReplaceAll(description, placeholder, result)
	}

	return description
}

// cleanToolResult extracts the actual result from tool output format
func (h *LLMTaskHandler) cleanToolResult(result string) string {
	// Remove "Tool Results:" prefix and clean up
	result = strings.TrimSpace(result)

	// If it starts with "Tool Results:", extract the actual result
	if strings.HasPrefix(result, "Tool Results:") {
		lines := strings.Split(result, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			// Look for lines like "- tool_name: actual_result"
			if strings.HasPrefix(line, "-") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					actualResult := strings.TrimSpace(parts[1])
					if actualResult != "" {
						return actualResult
					}
				}
			}
		}
	}

	// If not in tool format, return as-is
	return result
}

func (h *LLMTaskHandler) agentSupportsBrowserAutomation(ag *resolvedTaskAgent) bool {
	if ag == nil || ag.Agent == nil {
		return false
	}
	if !ag.Settings.IsWebSearchAllowed() {
		return false
	}

	for _, capability := range ag.Capabilities {
		switch strings.ToLower(strings.TrimSpace(capability)) {
		case "browser", "browser_automation", "web_search", "web_fetch":
			return true
		}
	}

	for _, serverName := range ag.MCPServers {
		name := strings.ToLower(strings.TrimSpace(serverName))
		if name == "" {
			continue
		}
		if strings.Contains(name, "playwright") || strings.Contains(name, "browserbase") || strings.Contains(name, "puppeteer") || strings.Contains(name, "browser") {
			return true
		}
	}

	for _, tool := range h.getAgentUtilityTools(ag) {
		if tool == nil {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(tool.Definition().Name))
		if name == "web_search" || name == "web_fetch" || name == "browser" {
			return true
		}
	}

	for _, tool := range h.getAgentMCPTools(ag) {
		if tool == nil {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(tool.Definition().Name))
		if strings.HasPrefix(name, "browser") ||
			strings.HasPrefix(name, "web_fetch") ||
			strings.HasPrefix(name, "web_search") ||
			name == "navigate" ||
			name == "open_url" {
			return true
		}
	}

	return false
}

func isLikelyBrowserAutomationIntent(description string) bool {
	lower := strings.ToLower(strings.TrimSpace(description))
	if lower == "" {
		return false
	}

	if !browserIntentWordPattern.MatchString(lower) && !browserIntentGoToPattern.MatchString(lower) {
		return false
	}

	if strings.Contains(lower, "http://") || strings.Contains(lower, "https://") || strings.Contains(lower, "www.") || strings.Contains(lower, "localhost:") {
		return true
	}

	for _, token := range strings.Fields(lower) {
		cleaned := strings.Trim(token, " \t\r\n,.;:!?\"'`()[]{}<>")
		if strings.Count(cleaned, ".") < 1 || strings.Contains(cleaned, "/") {
			continue
		}
		if !browserDomainPattern.MatchString(cleaned) {
			continue
		}
		parts := strings.Split(cleaned, ".")
		tld := parts[len(parts)-1]
		if _, isFileExtension := browserLikeFileExtensions[tld]; isFileExtension {
			continue
		}
		if len(tld) >= 2 && len(tld) <= 12 {
			return true
		}
	}

	return false
}

func taskRequiresBrowserAutomation(task Task) bool {
	return isLikelyBrowserAutomationIntent(taskBrowserIntentDescription(task))
}

func taskBrowserIntentDescription(task Task) string {
	if task.Context != nil {
		if overall, ok := task.Context["execution_overall_task_description"].(string); ok {
			if trimmed := strings.TrimSpace(overall); trimmed != "" {
				return trimmed
			}
		}
	}
	return task.Description
}

func looksLikeBrowserCapabilityRefusal(response string) bool {
	lower := strings.ToLower(strings.TrimSpace(response))
	if lower == "" {
		return false
	}

	markers := []string{
		"i don't have the capability",
		"i do not have the capability",
		"i can't open websites",
		"i cannot open websites",
		"cannot open websites directly",
		"can't access websites directly",
		"cannot access websites directly",
		"i'm unable to open websites",
		"i am unable to open websites",
		"i can't browse",
		"i cannot browse",
	}
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			return true
		}
	}

	return false
}

// buildTaskPrompt creates a prompt for the task
// AttachmentContent holds attachment info and file contents
type AttachmentContent struct {
	Title    string
	Body     string
	FilePath string
	Content  string
}

// getAttachedFileContents finds attachments connected to this task and reads their file contents
func (h *LLMTaskHandler) getAttachedFileContents(task Task) []AttachmentContent {
	if h.workspaceStore == nil || strings.TrimSpace(task.WorkspaceID) == "" {
		return nil
	}

	// Get the workspace to access attachments and connections
	workspace, err := h.workspaceStore.Get(task.WorkspaceID)
	if err != nil {
		logger.Error("Failed to get workspace for attachment reading", logger.Fields{"workspace_id": task.WorkspaceID, "error": err})
		return nil
	}

	var attachmentContents []AttachmentContent

	// Find connections from attachments to this task
	if workspace.Layout != nil && workspace.Layout.WorkflowConnections != nil {
		for _, conn := range workspace.Layout.WorkflowConnections {
			// Check if connection points to this task
			if conn.To == task.ID {
				// Check if source is an attachment
				for _, att := range workspace.Attachments {
					if att.ID == conn.From {
						attContent := AttachmentContent{
							Title: att.Title,
							Body:  att.Body,
						}

						// Read file contents if a file path is provided
						if att.File != nil && att.File.URL != "" {
							filePath := att.File.URL
							attContent.FilePath = filePath

							// Try to read the file
							content, err := os.ReadFile(filePath)
							if err != nil {
								logger.Warn("Failed to read attachment file", logger.Fields{"file": filePath, "error": err})
								attContent.Content = fmt.Sprintf("[Failed to read file: %v]", err)
							} else {
								attContent.Content = string(content)
							}
						}

						attachmentContents = append(attachmentContents, attContent)
					}
				}
			}
		}
	}

	return attachmentContents
}

// formatInputResults renders the upstream tasks' outputs into the prompt.
// For every input task ID we emit:
//
//   - the raw text result inside a fenced block (always, when present), so
//     downstream LLMs can see the upstream's full natural-language reply; and
//   - the parsed structured output as a JSON code block (when the upstream
//     declared an OutputSchema and the result matched). The JSON gives the
//     downstream task a machine-precise view it can quote/extract from
//     without re-parsing markdown.
//
// Tasks IDs that appear in only one of the two maps are still emitted with
// just the section they have. Iteration order is sorted by task ID so prompts
// stay deterministic across runs (Go map range is randomized).
func (h *LLMTaskHandler) formatInputResults(prompt *strings.Builder, inputs *TaskRuntimeInputs) {
	if inputs == nil {
		return
	}
	if len(inputs.TaskResults) == 0 && len(inputs.StructuredOutputs) == 0 {
		return
	}

	idSet := make(map[string]struct{}, len(inputs.TaskResults)+len(inputs.StructuredOutputs))
	for id := range inputs.TaskResults {
		idSet[id] = struct{}{}
	}
	for id := range inputs.StructuredOutputs {
		idSet[id] = struct{}{}
	}
	ids := make([]string, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	prompt.WriteString("## Input from Previous Tasks\n\n")
	for _, taskID := range ids {
		if result, ok := inputs.TaskResults[taskID]; ok && result != "" {
			fmt.Fprintf(prompt, "**Task %s Result:**\n```\n%s\n```\n\n", taskID, result)
		}
		if structured, ok := inputs.StructuredOutputs[taskID]; ok && len(structured) > 0 {
			encoded, err := json.MarshalIndent(structured, "", "  ")
			if err != nil {
				// Marshal of map[string]any is total in practice (any reachable
				// value an OutputSchema produces is JSON-able); the fallback
				// keeps the prompt valid if a future field type slips through.
				continue
			}
			fmt.Fprintf(prompt, "**Task %s Structured Output (JSON):**\n```json\n%s\n```\n\n", taskID, encoded)
		}
	}
}

// getProviderForModel determines which LLM provider to use (dynamic detection)
func (h *LLMTaskHandler) getProviderForModel(model string) string {
	trimmedModel := strings.TrimSpace(model)
	normalizedModel := strings.ToLower(trimmedModel)
	if trimmedModel == "" {
		return "openai"
	}

	// Claude Code short aliases map directly to claude_code when available.
	if normalizedModel == "haiku" || normalizedModel == "sonnet" || normalizedModel == "opus" {
		if h.llmFactory.HasProvider("claude_code") {
			return "claude_code"
		}
		if h.llmFactory.HasProvider("claude") {
			return "claude"
		}
		// Keep this in the Claude family even if provider isn't currently configured.
		return "claude"
	}

	// Check for Claude API models.
	if strings.HasPrefix(normalizedModel, "claude-") {
		return "claude"
	}

	// Check for Gemini models.
	if strings.HasPrefix(normalizedModel, "gemini") {
		return "gemini"
	}

	// Check for Codex models.
	if strings.HasPrefix(normalizedModel, "codex") {
		return "codex"
	}

	if localProvider := llm.FindLocalProviderByModel(h.llmFactory, trimmedModel); localProvider != "" {
		logger.Info("Model found in local provider, using local provider", logger.Fields{
			"model":    trimmedModel,
			"provider": localProvider,
		})
		return localProvider
	}

	// Default to OpenAI
	return "openai"
}

// convertAgentToolsToLLMTools converts MCP and workspace tools into LLM tools.
func (h *LLMTaskHandler) convertAgentToolsToLLMTools(ag *resolvedTaskAgent, task Task) []llm.Tool {
	var tools []llm.Tool
	seen := make(map[string]struct{})

	appendTool := func(t toolapi.Tool) {
		if t == nil {
			return
		}
		def := t.Definition()
		name := strings.ToLower(strings.TrimSpace(def.Name))
		if name == "" {
			return
		}
		if _, exists := seen[name]; exists {
			return
		}
		seen[name] = struct{}{}
		tools = append(tools, llm.Tool{
			Name:        def.Name,
			Description: def.Description,
			Parameters:  def.Parameters,
		})
	}

	for _, mcpTool := range h.getAgentMCPTools(ag) {
		appendTool(mcpTool)
	}
	for _, utilityTool := range h.getAgentUtilityTools(ag) {
		appendTool(utilityTool)
	}
	for _, wsTool := range h.getWorkspaceTools(task) {
		appendTool(wsTool)
	}

	return tools
}

func (h *LLMTaskHandler) getWorkspaceTools(task Task) []toolapi.Tool {
	if h == nil || h.workspaceToolsFn == nil {
		return nil
	}
	workspaceID := strings.TrimSpace(task.WorkspaceID)
	if workspaceID == "" {
		return nil
	}
	return h.workspaceToolsFn(workspaceID)
}

func (h *LLMTaskHandler) getAgentUtilityTools(ag *resolvedTaskAgent) []toolapi.Tool {
	if h == nil || h.utilityTools == nil || ag == nil || ag.Agent == nil {
		return nil
	}

	allowWeb := ag.Settings.IsWebSearchAllowed()
	tools := make([]toolapi.Tool, 0, len(taskUtilityToolNames))
	for _, name := range taskUtilityToolNames {
		if isWebUtilityToolNameForTask(name) && !allowWeb {
			continue
		}
		tool, ok := h.utilityTools.GetTool(name)
		if ok && tool != nil {
			tools = append(tools, tool)
		}
	}
	return tools
}

func isWebUtilityToolNameForTask(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "web_search", "web_fetch", "browser":
		return true
	default:
		return false
	}
}

// toolCallResult represents the result of a tool call
type toolCallResult struct {
	Name   string
	Result string
	Error  error
}

// executeToolCalls executes tool calls and returns results
func (h *LLMTaskHandler) executeToolCalls(ctx context.Context, ag *resolvedTaskAgent, agentName string, task Task, toolCalls []llm.ToolCall) []toolCallResult {
	results := make([]toolCallResult, len(toolCalls))

	for i, tc := range toolCalls {
		results[i] = h.executeToolCall(ctx, ag, agentName, task, tc)
	}

	return results
}

// executeToolCall executes a single tool call
func (h *LLMTaskHandler) executeToolCall(ctx context.Context, ag *resolvedTaskAgent, agentName string, task Task, toolCall llm.ToolCall) toolCallResult {
	logger.Debug("Executing tool", logger.Fields{"tool": toolCall.Name})

	// Publish tool call event
	if h.eventBus != nil {
		event := NewTaskEvent(EventTaskToolCall, task.WorkspaceID, task.ID, agentName, map[string]interface{}{
			"tool_name": toolCall.Name,
			"arguments": toolCall.Arguments,
		})
		h.eventBus.Publish(event)
	}

	tool, found := h.findTool(ag, task, toolCall.Name)

	if !found || tool == nil {
		result := toolCallResult{
			Name:  toolCall.Name,
			Error: fmt.Errorf("tool %s not found", toolCall.Name),
		}

		// Publish tool result event (error)
		if h.eventBus != nil {
			event := NewTaskEvent(EventTaskToolResult, task.WorkspaceID, task.ID, agentName, map[string]interface{}{
				"tool_name": toolCall.Name,
				"success":   false,
				"error":     result.Error.Error(),
			})
			h.eventBus.Publish(event)
		}

		return result
	}

	// Execute the tool
	result, err := tool.Call(ctx, toolCall.Arguments)

	// Publish tool result event
	if h.eventBus != nil {
		data := map[string]interface{}{
			"tool_name": toolCall.Name,
			"success":   err == nil,
		}
		if err != nil {
			data["error"] = err.Error()
		} else {
			// Truncate result if too long for event
			resultPreview := result
			if len(resultPreview) > 200 {
				resultPreview = resultPreview[:200] + "..."
			}
			data["result_preview"] = resultPreview
		}
		event := NewTaskEvent(EventTaskToolResult, task.WorkspaceID, task.ID, agentName, data)
		h.eventBus.Publish(event)
	}

	if err != nil {
		return toolCallResult{
			Name:  toolCall.Name,
			Error: err,
		}
	}

	return toolCallResult{
		Name:   toolCall.Name,
		Result: result,
	}
}

func (h *LLMTaskHandler) findTool(ag *resolvedTaskAgent, task Task, toolName string) (toolapi.Tool, bool) {
	target := strings.ToLower(strings.TrimSpace(toolName))
	if target == "" {
		return nil, false
	}

	for _, utilityTool := range h.getAgentUtilityTools(ag) {
		if utilityTool == nil {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(utilityTool.Definition().Name))
		if name == target {
			return utilityTool, true
		}
	}

	for _, mcpTool := range h.getAgentMCPTools(ag) {
		if mcpTool == nil {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(mcpTool.Definition().Name))
		if name == target {
			return mcpTool, true
		}
	}

	for _, wsTool := range h.getWorkspaceTools(task) {
		if wsTool == nil {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(wsTool.Definition().Name))
		if name == target {
			return wsTool, true
		}
	}

	return nil, false
}

func (h *LLMTaskHandler) getAgentMCPTools(ag *resolvedTaskAgent) []toolapi.Tool {
	if h == nil || h.mcpRegistry == nil || ag == nil || len(ag.MCPServers) == 0 {
		return nil
	}

	tools := make([]toolapi.Tool, 0, 8)
	for _, serverName := range ag.MCPServers {
		name := strings.TrimSpace(serverName)
		if name == "" {
			continue
		}
		serverTools, err := h.getMCPToolsForServer(name)
		if err != nil {
			logger.Warn("Failed to load MCP tools for task execution", logger.Fields{
				"server": name,
				"error":  err.Error(),
			})
			continue
		}
		tools = append(tools, serverTools...)
	}

	return tools
}

func (h *LLMTaskHandler) getMCPToolsForServer(serverName string) ([]toolapi.Tool, error) {
	if h == nil || h.mcpRegistry == nil {
		return nil, fmt.Errorf("mcp registry is not configured")
	}

	mcpTools, err := h.mcpRegistry.GetToolsForServer(serverName)
	if err == nil {
		return mcpTools, nil
	}
	if !isMCPServerNotRunningError(err) {
		return nil, err
	}

	if startErr := h.mcpRegistry.StartServer(serverName); startErr != nil {
		return nil, fmt.Errorf("failed to start MCP server %q: %w", serverName, startErr)
	}

	mcpTools, retryErr := h.mcpRegistry.GetToolsForServer(serverName)
	if retryErr != nil {
		return nil, fmt.Errorf("MCP server %q started but tool discovery failed: %w", serverName, retryErr)
	}

	return mcpTools, nil
}

func isMCPServerNotRunningError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "is not running")
}
