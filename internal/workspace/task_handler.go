package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/toolapi"
	"github.com/johnjallday/ori-agent/internal/userprofile"
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
	userProfileStore userprofile.UserStore
	eventBus         *EventBus // Optional event bus for publishing execution events
	mcpRegistry      mcpRegistry
	// nativeMCPExecTimeout bounds a native-MCP CLI task run (which runs its own
	// multi-tool agent loop). Zero falls back to defaultNativeMCPExecTimeout.
	nativeMCPExecTimeout time.Duration
	runtimeResolver      *AgentRuntimeResolver
	workspaceToolsFn     WorkspaceToolFactory
	utilityTools         UtilityToolProvider
}

type mcpRegistry interface {
	GetToolsForServer(string) ([]toolapi.Tool, error)
	StartServer(string) error
	// ListServers returns the runtime server configs (command/args/env) so a
	// native-MCP provider can be handed the resolved specs for an agent's
	// servers. Satisfied by *mcp.Registry.
	ListServers() []mcp.ServerConfig
}

// UtilityToolProvider exposes native utility tools (time, weather, web search,
// browser, etc.) to task execution without coupling workspace to chathttp.
type UtilityToolProvider interface {
	GetTool(string) (toolapi.Tool, bool)
}

// WorkspaceToolFactory returns workspace-scoped tools (notes, tasks, sessions, files, etc.)
// for use during task execution. Tools are constructed per workspace so the agent can
// read and update workspace state without forcing the user to paste it into the prompt.
type WorkspaceToolFactory func(workspaceID, agentName string) []toolapi.Tool

type resolvedTaskAgent struct {
	*agent.Agent
	MCPServers []string
}

const (
	maxTaskToolRounds          = 6
	maxTaskToolResultFollowups = 1
)

// taskUtilityToolNames lists the utility tool names this handler exposes
// to agents during task execution. Browser-intent detection lives in
// task_handler_browser_intent.go, which owns its own regex/extension data.
var taskUtilityToolNames = []string{"time", "weather", "air_quality", "web_search", "web_fetch", "browser"}

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

// defaultNativeMCPExecTimeout is the fallback budget for a native-MCP CLI task
// run. These runs drive a full multi-tool agent loop inside the CLI, so they
// routinely exceed the ordinary LLM-call budget (and the 120s auto-task parse
// timeout). 150s is typically too tight; 300s is the recommended default.
const defaultNativeMCPExecTimeout = 300 * time.Second

// SetNativeMCPExecTimeout overrides the native-MCP CLI execution timeout
// (e.g. from configuration). A non-positive value restores the default.
func (h *LLMTaskHandler) SetNativeMCPExecTimeout(d time.Duration) {
	h.nativeMCPExecTimeout = d
}

func (h *LLMTaskHandler) effectiveNativeMCPExecTimeout() time.Duration {
	if h != nil && h.nativeMCPExecTimeout > 0 {
		return h.nativeMCPExecTimeout
	}
	return defaultNativeMCPExecTimeout
}

// SetRuntimeResolver configures workspace-aware runtime MCP resolution for task execution.
func (h *LLMTaskHandler) SetRuntimeResolver(resolver *AgentRuntimeResolver) {
	h.runtimeResolver = resolver
}

// SetContextStore configures optional workspace note/session summaries for task prompts.
func (h *LLMTaskHandler) SetContextStore(store taskPromptContextStore) {
	h.contextStore = store
}

func (h *LLMTaskHandler) SetUserProfileStore(store userprofile.UserStore) {
	h.userProfileStore = store
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
		event := NewTaskEvent(EventTaskThinking, task.WorkspaceID, task.ID, agentName, map[string]any{
			"phase":   "starting",
			"message": "Agent is analyzing the task...",
		})
		h.eventBus.Publish(event)
	}

	// Spawn a heartbeat goroutine so the UI sees activity even during very
	// long phases (e.g. a 60s LLM call between awaiting_llm and
	// llm_returned). Cancelled when ExecuteTask returns; the existing
	// thinking/tool events update the badge label, heartbeats only refresh
	// "active Xs ago".
	if h.eventBus != nil {
		heartbeatCtx, heartbeatCancel := context.WithCancel(ctx)
		defer heartbeatCancel()
		go h.runTaskHeartbeats(heartbeatCtx, task.WorkspaceID, task.ID, agentName)
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

// resolveExecutionAgent and the rest of the agent-resolution helpers live
// in task_handler_agent_resolver.go.

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
	successfulToolCalls := map[string]bool{}
	forceFinalAnswer := false

	for round := 0; round < maxTaskToolRounds; round++ {
		requestTools := tools
		if forceFinalAnswer {
			requestTools = nil
		}

		// Bracket the LLM call so the UI can show "awaiting LLM" instead of a
		// silent in_progress span. provider.Chat() can take tens of seconds,
		// during which no other event would otherwise fire.
		if h.eventBus != nil {
			h.eventBus.Publish(NewTaskEvent(EventTaskThinking, task.WorkspaceID, task.ID, agentName, map[string]any{
				"phase":   "awaiting_llm",
				"round":   round + 1,
				"model":   modelName,
				"message": "Awaiting model response",
			}))
		}

		chatReq := llm.ChatRequest{
			Model:           modelName,
			Messages:        conversation,
			Temperature:     ag.Settings.Temperature,
			ReasoningEffort: ag.Settings.EffectiveReasoningEffort(providerName),
			Tools:           requestTools,
		}
		// Native-MCP providers (CLI agents) run their own MCP loop instead of
		// round-tripping tool calls through ori-agent, so hand them the agent's
		// resolved MCP server specs plus the workspace context they need to key
		// the persistent config and confine the run. SupportsTools stays false
		// for these providers, so requestTools is ignored by them. Gated behind
		// the workspace+agent opt-in (the CLI runs tools without ori's per-tool
		// confirmation); when not opted in, the provider runs text-only as before.
		nativeMCPActive := false
		if providerSupportsNativeMCP(provider) && h.nativeMCPAllowed(task.WorkspaceID, ag) {
			if specs := h.resolveNativeMCPSpecs(ag); len(specs) > 0 {
				chatReq.MCPServers = specs
				chatReq.WorkspaceID = task.WorkspaceID
				if h.workspaceStore != nil {
					chatReq.WorkspaceDir = h.workspaceStore.GetFilesPath(task.WorkspaceID)
				}
				nativeMCPActive = true
			}
		}

		// Native-MCP CLI runs get their own, longer budget than an ordinary LLM
		// call. Cancel right after the call (not deferred) to avoid piling up
		// contexts across tool rounds.
		callCtx := ctx
		var cancelCall context.CancelFunc
		if nativeMCPActive {
			callCtx, cancelCall = context.WithTimeout(ctx, h.effectiveNativeMCPExecTimeout())
		}
		resp, err := provider.Chat(callCtx, chatReq)
		if cancelCall != nil {
			cancelCall()
		}
		if err != nil {
			// Surface the raw provider error (CLI stderr/stdout) before it is
			// replaced by a friendly message, so MCP connection / permission /
			// timeout failures stay diagnosable.
			if nativeMCPActive {
				logger.Warn("Native-MCP CLI task call failed", logger.Fields{
					"workspace_id": task.WorkspaceID,
					"agent":        agentName,
					"provider":     providerName,
					"error":        err.Error(),
				})
			}
			if friendlyMsg := classifyContextError(err); friendlyMsg != "" {
				return "", fmt.Errorf("%s", friendlyMsg)
			}
			return "", fmt.Errorf("LLM call failed: %w", err)
		}

		if h.eventBus != nil {
			h.eventBus.Publish(NewTaskEvent(EventTaskThinking, task.WorkspaceID, task.ID, agentName, map[string]any{
				"phase":           "llm_returned",
				"round":           round + 1,
				"tool_call_count": len(resp.ToolCalls),
			}))
		}

		if len(resp.ToolCalls) == 0 {
			forceFinalAnswer = false
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

			if responseLooksLikeRawToolResults(resp.Content) {
				rawResponse := strings.TrimSpace(firstNonEmptyString(lastToolSummary, resp.Content))
				if strings.TrimSpace(rawResponse) != "" {
					if toolResultFollowups < maxTaskToolResultFollowups {
						toolResultFollowups++
						forceFinalAnswer = !toolSummaryLooksLikeEmptyWebSearch(rawResponse)
						conversation = append(conversation, llm.Message{
							Role:    llm.RoleAssistant,
							Content: resp.Content,
						})
						conversation = append(conversation, llm.NewUserMessage(buildToolResultFollowupPrompt(task)))
						continue
					}
					return "", buildToolOnlyBlockedError(rawResponse)
				}
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

		if forceFinalAnswer {
			return "", buildToolOnlyBlockedError(firstNonEmptyString(lastToolSummary, resp.Content))
		}

		if repeatedSuccessfulToolCalls(resp.ToolCalls, successfulToolCalls) && strings.TrimSpace(lastToolSummary) != "" {
			if toolResultFollowups < maxTaskToolResultFollowups {
				toolResultFollowups++
				forceFinalAnswer = true
				conversation = append(conversation, llm.NewUserMessage(buildRepeatedToolCallFollowupPrompt(task)))
				continue
			}
			return "", buildToolOnlyBlockedError(lastToolSummary)
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
			if tr.Error == nil {
				successfulToolCalls[taskToolCallSignature(resp.ToolCalls[index])] = true
			}

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

func repeatedSuccessfulToolCalls(toolCalls []llm.ToolCall, successfulToolCalls map[string]bool) bool {
	if len(toolCalls) == 0 || len(successfulToolCalls) == 0 {
		return false
	}
	for _, toolCall := range toolCalls {
		if !successfulToolCalls[taskToolCallSignature(toolCall)] {
			return false
		}
	}
	return true
}

func taskToolCallSignature(toolCall llm.ToolCall) string {
	name := strings.ToLower(strings.TrimSpace(toolCall.Name))
	args := strings.TrimSpace(toolCall.Arguments)
	if name == "web_search" {
		args = normalizeWebSearchToolArguments(args)
	}
	return name + "\x00" + args
}

func normalizeWebSearchToolArguments(arguments string) string {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(arguments), &parsed); err != nil {
		return strings.ToLower(strings.Join(strings.Fields(arguments), " "))
	}
	if query, ok := parsed["query"].(string); ok {
		parsed["query"] = strings.ToLower(strings.Join(strings.Fields(query), " "))
	}
	normalized, err := json.Marshal(parsed)
	if err != nil {
		return strings.ToLower(strings.Join(strings.Fields(arguments), " "))
	}
	return string(normalized)
}

func responseLooksLikeRawToolResults(content string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(content)), "tool results:")
}

func buildRepeatedToolCallFollowupPrompt(task Task) string {
	var prompt strings.Builder
	prompt.WriteString("You already received a successful result for that same tool call. Do not call tools again. Use the existing tool result to answer the task concisely. ")
	prompt.WriteString("Do not return raw Tool Results.")
	if strings.TrimSpace(task.Description) != "" {
		prompt.WriteString("\n\nTask: ")
		prompt.WriteString(strings.TrimSpace(task.Description))
	}
	return prompt.String()
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

// Provider-selection helpers (normalizeProviderName, isClaudeFamilyModel,
// isGeminiFamilyModel, isCodexFamilyModel, normalizeModelForProvider,
// getProviderForAgent, getProviderForModel) live in
// task_handler_provider_selection.go.

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

// Browser-intent detection (agentSupportsBrowserAutomation, the
// isLikelyBrowserAutomationIntent / taskRequiresBrowserAutomation /
// looksLikeBrowserCapabilityRefusal helpers and their pattern data) lives
// in task_handler_browser_intent.go.

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
	// task.To is the executing agent; the factory uses it to gate
	// coordinator-only tools (delegate_task) to the workspace coordinator.
	return h.workspaceToolsFn(workspaceID, strings.TrimSpace(task.To))
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

// taskHeartbeatInterval controls how often EventTaskHeartbeat fires while a
// task is executing. Short enough that "active Xs ago" stays meaningful for
// users; long enough that a noisy event bus won't impact performance.
const taskHeartbeatInterval = 5 * time.Second

// runTaskHeartbeats publishes EventTaskHeartbeat at a fixed cadence until ctx
// is cancelled. The events carry no phase — they exist only to advance the
// "last activity" timestamp the UI uses to distinguish "still working" from
// "stuck". Returns immediately if the bus is unset.
func (h *LLMTaskHandler) runTaskHeartbeats(ctx context.Context, workspaceID, taskID, agentName string) {
	if h.eventBus == nil {
		return
	}
	ticker := time.NewTicker(taskHeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.eventBus.Publish(NewTaskEvent(EventTaskHeartbeat, workspaceID, taskID, agentName, nil))
		}
	}
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

	// Autonomy gate. Applies to mission runs (policy from the mission context)
	// and to delegated subtasks (policy from the workspace) — interactive chat
	// with the same agent is unaffected. v1 uses a heuristic classifier
	// (SuggestSideEffect) when no per-binding classification can be resolved;
	// tools whose names don't match a read-prefix are conservatively treated as
	// write/external and denied under Watch. Per-tool binding-driven
	// classification is a follow-up.
	if denial := h.evaluateExecutionAutonomyGate(task, toolCall.Name); denial != nil {
		if h.eventBus != nil {
			event := NewTaskEvent(EventTaskToolResult, task.WorkspaceID, task.ID, agentName, map[string]any{
				"tool_name": toolCall.Name,
				"success":   false,
				"error":     denial.Error(),
				"blocked":   true,
			})
			h.eventBus.Publish(event)
		}
		return toolCallResult{Name: toolCall.Name, Error: denial}
	}

	// Publish tool call event
	if h.eventBus != nil {
		event := NewTaskEvent(EventTaskToolCall, task.WorkspaceID, task.ID, agentName, map[string]any{
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
			event := NewTaskEvent(EventTaskToolResult, task.WorkspaceID, task.ID, agentName, map[string]any{
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
		data := map[string]any{
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

// nativeMCPAllowed reports whether native-MCP CLI execution is permitted for
// this run: it requires the workspace and the agent to both opt in (the CLI
// runs tools outside ori-agent's per-tool confirmation gate). Defaults off.
func (h *LLMTaskHandler) nativeMCPAllowed(workspaceID string, ag *resolvedTaskAgent) bool {
	if h == nil || ag == nil || !ag.Settings.IsNativeMCPToolsAllowed() {
		return false
	}
	if h.workspaceStore == nil {
		return false
	}
	ws, err := h.workspaceStore.Get(workspaceID)
	if err != nil {
		return false
	}
	return nativeMCPGateAllowed(ws, ag)
}

// nativeMCPGateAllowed is the pure opt-in predicate: both the workspace and the
// agent must allow native-MCP CLI tooling.
func nativeMCPGateAllowed(ws *Workspace, ag *resolvedTaskAgent) bool {
	return ws != nil && ws.AllowNativeMCPCLI && ag != nil && ag.Settings.IsNativeMCPToolsAllowed()
}

// providerSupportsNativeMCP reports whether the provider runs its own MCP loop
// (CLI agents like Claude Code / Codex). Such providers receive the agent's MCP
// servers as resolved specs on the request rather than via the internal tool
// loop.
func providerSupportsNativeMCP(provider llm.Provider) bool {
	if provider == nil {
		return false
	}
	return provider.Capabilities().SupportsNativeMCP
}

// resolveNativeMCPSpecs maps the agent's runtime MCP server names to resolved
// specs (command/args/env) for native-MCP providers. The CLI-config key uses a
// CLI-safe alias derived from the runtime name (which contains colons). Returns
// nil when the registry is unavailable or the agent has no MCP servers.
func (h *LLMTaskHandler) resolveNativeMCPSpecs(ag *resolvedTaskAgent) []llm.MCPServerSpec {
	if h == nil || h.mcpRegistry == nil || ag == nil || len(ag.MCPServers) == 0 {
		return nil
	}

	configByName := make(map[string]mcp.ServerConfig)
	for _, cfg := range h.mcpRegistry.ListServers() {
		configByName[cfg.Name] = cfg
	}

	specs := make([]llm.MCPServerSpec, 0, len(ag.MCPServers))
	for _, serverName := range ag.MCPServers {
		name := strings.TrimSpace(serverName)
		if name == "" {
			continue
		}
		cfg, ok := configByName[name]
		if !ok || strings.TrimSpace(cfg.Command) == "" {
			continue
		}
		specs = append(specs, llm.MCPServerSpec{
			Name:    nativeMCPAlias(name),
			Command: cfg.Command,
			Args:    append([]string(nil), cfg.Args...),
			Env:     cfg.Env,
		})
	}
	if len(specs) == 0 {
		return nil
	}
	return specs
}

// nativeMCPAlias derives a CLI-safe MCP-config key from a runtime server name.
// Runtime names are "ws:{workspaceID}:mcp:{server}:{bindingID}" (colon-bearing);
// the logical server segment ("{server}") is the stable, CLI-friendly basis.
// The segment may still contain "/" (e.g. "reaper-plugin/ori-reaper"), so all
// chars outside [A-Za-z0-9_-] are mapped to "_" to keep the MCP-config key and
// the resulting "mcp__<key>__<tool>" tool names valid. (Alias rules + dedup are
// hardened in task 2.2.)
func nativeMCPAlias(runtimeName string) string {
	base := runtimeName
	parts := strings.Split(runtimeName, ":")
	if len(parts) >= 5 && parts[0] == "ws" && parts[2] == "mcp" {
		if server := strings.TrimSpace(parts[3]); server != "" {
			base = server
		}
	}
	var b strings.Builder
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
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
