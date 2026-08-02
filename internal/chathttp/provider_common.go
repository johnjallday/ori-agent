package chathttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/skills"
	"github.com/johnjallday/ori-agent/internal/toolapi"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// ToolCallResult holds the result of a tool execution
type ToolCallResult struct {
	Function   string `json:"function"`
	Args       string `json:"args"`
	Result     string `json:"result"`
	DurationMs int64  `json:"durationMs,omitempty"` // Execution time in milliseconds
	Success    bool   `json:"success"`              // Whether the tool executed successfully
}

// ExecuteToolCallsResult holds the results of executing multiple tool calls
type ExecuteToolCallsResult struct {
	Results             []ToolCallResult
	CombinedResult      string
	HasStructuredResult bool
	StructuredData      *toolapi.StructuredResult
	Receipts            []ActionReceipt
}

const (
	defaultToolLoopMaxTurns          = 4
	defaultToolLoopMaxRepeatedFinger = 2

	// defaultChatMaxTokens caps completion length for every provider chat
	// request in this package (initial turn and tool-loop turns alike).
	defaultChatMaxTokens = 4000
)

type boundedToolLoopConfig struct {
	MaxTurns                int
	MaxRepeatedFingerprints int
}

type boundedToolLoopCallbacks struct {
	AppendAssistantTurn  func(content string, toolCalls []llm.ToolCall)
	ExecuteToolCalls     func(toolCalls []llm.ToolCall) ExecuteToolCallsResult
	AppendToolResults    func(toolCalls []llm.ToolCall, execResult ExecuteToolCallsResult)
	RequestNextResponse  func() (content string, toolCalls []llm.ToolCall, err error)
	RequestFinalResponse func() (content string, err error)
}

type boundedToolLoopResult struct {
	FinalContent        string
	ToolCalls           []ToolCallResult
	Receipts            []ActionReceipt
	HasStructuredResult bool
	StructuredData      *toolapi.StructuredResult
	UsedToolFallback    bool
	StopReason          string
	Err                 error
}

func (h *Handler) runBoundedToolLoop(
	initialContent string,
	initialToolCalls []llm.ToolCall,
	cfg boundedToolLoopConfig,
	callbacks boundedToolLoopCallbacks,
) boundedToolLoopResult {
	maxTurns := cfg.MaxTurns
	if maxTurns <= 0 {
		maxTurns = defaultToolLoopMaxTurns
	}
	maxRepeatedFingerprints := cfg.MaxRepeatedFingerprints
	if maxRepeatedFingerprints <= 0 {
		maxRepeatedFingerprints = defaultToolLoopMaxRepeatedFinger
	}

	appendAssistant := callbacks.AppendAssistantTurn
	if appendAssistant == nil {
		appendAssistant = func(string, []llm.ToolCall) {}
	}
	appendToolResults := callbacks.AppendToolResults
	if appendToolResults == nil {
		appendToolResults = func([]llm.ToolCall, ExecuteToolCallsResult) {}
	}

	currentContent := initialContent
	currentToolCalls := initialToolCalls
	fingerprintCounts := make(map[string]int)
	allToolCalls := make([]ToolCallResult, 0)
	allReceipts := make([]ActionReceipt, 0)

	for turn := 1; turn <= maxTurns; turn++ {
		if len(currentToolCalls) == 0 {
			return boundedToolLoopResult{
				FinalContent: currentContent,
				ToolCalls:    allToolCalls,
				Receipts:     allReceipts,
			}
		}

		if repeated, fingerprint, count := detectRepeatedToolFingerprint(currentToolCalls, fingerprintCounts, maxRepeatedFingerprints); repeated {
			logger.Warn("Stopping tool loop due to repeated tool call fingerprint", logger.Fields{
				"fingerprint": fingerprint,
				"count":       count,
				"max_allowed": maxRepeatedFingerprints,
			})
			if finalContent, ok := tryToolLoopFinalSynthesis(callbacks.RequestFinalResponse); ok {
				return boundedToolLoopResult{
					FinalContent: finalContent,
					ToolCalls:    allToolCalls,
					Receipts:     allReceipts,
					StopReason:   "repeated_tool_call_synthesized",
				}
			}
			return boundedToolLoopResult{
				FinalContent:     fallbackToolLoopContent(allToolCalls, "tool loop repeated the same call"),
				ToolCalls:        allToolCalls,
				Receipts:         allReceipts,
				UsedToolFallback: true,
				StopReason:       "repeated_tool_call",
			}
		}

		appendAssistant(currentContent, currentToolCalls)

		execResult := callbacks.ExecuteToolCalls(currentToolCalls)
		allToolCalls = append(allToolCalls, execResult.Results...)
		allReceipts = append(allReceipts, execResult.Receipts...)
		appendToolResults(currentToolCalls, execResult)

		if execResult.HasStructuredResult {
			return boundedToolLoopResult{
				FinalContent:        execResult.CombinedResult,
				ToolCalls:           allToolCalls,
				Receipts:            allReceipts,
				HasStructuredResult: true,
				StructuredData:      execResult.StructuredData,
			}
		}

		if turn == maxTurns {
			logger.Warn("Stopping tool loop after max turns reached", logger.Fields{
				"max_turns": maxTurns,
			})
			if finalContent, ok := tryToolLoopFinalSynthesis(callbacks.RequestFinalResponse); ok {
				return boundedToolLoopResult{
					FinalContent: finalContent,
					ToolCalls:    allToolCalls,
					Receipts:     allReceipts,
					StopReason:   "max_turns_synthesized",
				}
			}
			return boundedToolLoopResult{
				FinalContent:     fallbackToolLoopContent(allToolCalls, "tool loop reached max turns"),
				ToolCalls:        allToolCalls,
				Receipts:         allReceipts,
				UsedToolFallback: true,
				StopReason:       "max_turns",
			}
		}

		nextContent, nextToolCalls, err := callbacks.RequestNextResponse()
		if err != nil {
			fallback := fallbackToolLoopContent(allToolCalls, "follow-up model request failed")
			return boundedToolLoopResult{
				FinalContent:     fallback,
				ToolCalls:        allToolCalls,
				Receipts:         allReceipts,
				UsedToolFallback: strings.TrimSpace(fallback) != "",
				StopReason:       "followup_error",
				Err:              err,
			}
		}

		currentContent = nextContent
		currentToolCalls = nextToolCalls
	}

	return boundedToolLoopResult{
		FinalContent:     fallbackToolLoopContent(allToolCalls, "tool loop stopped unexpectedly"),
		ToolCalls:        allToolCalls,
		Receipts:         allReceipts,
		UsedToolFallback: true,
		StopReason:       "unexpected_stop",
	}
}

// providerToolLoopRun bundles everything runProviderToolLoop needs to finish
// a chat turn in which the model requested tool calls. Used by the Claude,
// Gemini, and local-provider handlers, which previously each wired the same
// callbacks around runBoundedToolLoop by hand. (The OpenAI handler keeps its
// own wiring: it speaks the OpenAI SDK client, not llm.Provider.)
type providerToolLoopRun struct {
	Agent           *resolvedChatAgent
	AgentName       string
	Messages        []llm.Message
	InitialResponse *llm.ChatResponse
	Tools           []llm.Tool
	Files           []toolapi.FileAttachment
	Provider        llm.Provider
	SessionID       string
	UserMessage     string
	PlannerDecision *types.PlannerDecision

	// ProviderLabel is the usage-tracking / error-message name ("claude",
	// "gemini", or the local provider's name).
	ProviderLabel string
	// FollowUpSystemPrompt is the system prompt for follow-up turns inside
	// the loop; providers differ on whether they append follow-up guidance.
	FollowUpSystemPrompt string
	// FinalSystemPrompt is the system prompt for the last-resort synthesis
	// request when the loop stops without a final answer.
	FinalSystemPrompt string
}

// runProviderToolLoop drives the bounded tool loop for a generic
// llm.Provider and writes the chat response: assistant turns and tool
// results append to the conversation, follow-up/final-synthesis requests go
// back to the same provider, and the final text is persisted to the agent,
// stored in the session, and returned with receipts and route metadata.
func (h *Handler) runProviderToolLoop(w http.ResponseWriter, ctx, baseCtx context.Context, run providerToolLoopRun) {
	start := time.Now()
	messages := run.Messages
	ag := run.Agent

	loopResult := h.runBoundedToolLoop(
		run.InitialResponse.Content,
		run.InitialResponse.ToolCalls,
		boundedToolLoopConfig{},
		boundedToolLoopCallbacks{
			AppendAssistantTurn: func(content string, toolCalls []llm.ToolCall) {
				assistantMsg := llm.NewAssistantMessage(content)
				assistantMsg.ToolCalls = toolCalls
				messages = append(messages, assistantMsg)
			},
			ExecuteToolCalls: func(toolCalls []llm.ToolCall) ExecuteToolCallsResult {
				return h.executeToolCallsCommonWithSession(baseCtx, ag, toolCalls, run.Files, run.SessionID)
			},
			AppendToolResults: func(toolCalls []llm.ToolCall, execResult ExecuteToolCallsResult) {
				for i, tc := range toolCalls {
					if i >= len(execResult.Results) {
						break
					}
					messages = append(messages, llm.NewToolMessage(tc.ID, execResult.Results[i].Result))
				}
			},
			RequestNextResponse: func() (string, []llm.ToolCall, error) {
				resp, err := run.Provider.Chat(ctx, llm.ChatRequest{
					Model:        ag.Settings.Model,
					Messages:     messages,
					SystemPrompt: run.FollowUpSystemPrompt,
					Tools:        run.Tools,
					Temperature:  ag.Settings.Temperature,
					MaxTokens:    defaultChatMaxTokens,
				})
				if err != nil {
					return "", nil, err
				}
				if resp == nil {
					return "", nil, fmt.Errorf("%s follow-up returned no response", run.ProviderLabel)
				}
				h.trackUsageCommon(run.ProviderLabel, ag.Settings.Model, run.AgentName, resp.Usage, ag.Agent, run.UserMessage)
				return resp.Content, resp.ToolCalls, nil
			},
			RequestFinalResponse: func() (string, error) {
				resp, err := run.Provider.Chat(ctx, llm.ChatRequest{
					Model:        ag.Settings.Model,
					Messages:     messages,
					SystemPrompt: run.FinalSystemPrompt,
					Temperature:  ag.Settings.Temperature,
					MaxTokens:    defaultChatMaxTokens,
				})
				if err != nil {
					return "", err
				}
				if resp == nil {
					return "", fmt.Errorf("%s final synthesis returned no response", run.ProviderLabel)
				}
				h.trackUsageCommon(run.ProviderLabel, ag.Settings.Model, run.AgentName, resp.Usage, ag.Agent, run.UserMessage)
				return resp.Content, nil
			},
		},
	)

	finalText := getResponseText(loopResult.FinalContent)
	if loopResult.HasStructuredResult {
		finalText = loopResult.FinalContent
	}

	logger.Debug("Provider tool loop completed", logger.Fields{
		"provider": run.ProviderLabel,
		"duration": time.Since(start),
	})
	_ = h.persistAgent(run.AgentName, ag.Agent)
	h.storeMessageInSession(baseCtx, run.SessionID, "assistant", finalText)

	writeJSONResponse(w, attachPlannerDecision(attachActionReceipts(attachRouteMetadata(map[string]any{
		"response":  finalText,
		"toolCalls": loopResult.ToolCalls,
	}, chatRouteMetadata{
		Mode:      routeModeAssistantChat,
		ToolCount: len(loopResult.ToolCalls),
	}), loopResult.Receipts), run.PlannerDecision))
}

func tryToolLoopFinalSynthesis(request func() (string, error)) (string, bool) {
	if request == nil {
		return "", false
	}

	content, err := request()
	if err != nil {
		logger.Warn("Final tool-loop synthesis request failed", logger.Fields{
			"error": err,
		})
		return "", false
	}

	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return "", false
	}
	return trimmed, true
}

func detectRepeatedToolFingerprint(toolCalls []llm.ToolCall, seen map[string]int, maxAllowed int) (bool, string, int) {
	if maxAllowed <= 0 {
		return false, "", 0
	}
	for _, tc := range toolCalls {
		fingerprint := toolCallFingerprint(tc)
		seen[fingerprint]++
		if seen[fingerprint] > maxAllowed {
			return true, fingerprint, seen[fingerprint]
		}
	}
	return false, "", 0
}

func toolCallFingerprint(tc llm.ToolCall) string {
	name := strings.ToLower(strings.TrimSpace(tc.Name))
	args := canonicalizeToolArguments(tc.Arguments)
	return name + "|" + args
}

func canonicalizeToolArguments(arguments string) string {
	trimmed := strings.TrimSpace(arguments)
	if trimmed == "" {
		return ""
	}

	var parsed any
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return trimmed
	}

	normalized, err := json.Marshal(parsed)
	if err != nil {
		return trimmed
	}
	return string(normalized)
}

func fallbackToolLoopContent(results []ToolCallResult, fallbackMessage string) string {
	combined, _, _ := processToolResultsCommon(results)
	combined = strings.TrimSpace(combined)
	if combined != "" {
		return combined
	}
	if strings.TrimSpace(fallbackMessage) != "" {
		return fallbackMessage
	}
	return emptyResponseText
}

// executeToolCallsCommonWithSession executes tool calls and stores them for the given session
func (h *Handler) executeToolCallsCommonWithSession(
	baseCtx context.Context,
	ag *resolvedChatAgent,
	toolCalls []llm.ToolCall,
	files []toolapi.FileAttachment,
	sessionID string,
) ExecuteToolCallsResult {
	var results []ToolCallResult
	var receipts []ActionReceipt

	for _, tc := range toolCalls {
		name := tc.Name
		args := tc.Arguments

		logger.Debug("Executing tool", logger.Fields{"name": name})

		trackUtilityTool := h.utilityTelemetry != nil && isNativeUtilityToolName(name)
		if trackUtilityTool {
			h.utilityTelemetry.RecordToolInvocation(name, "")
		}

		// Find tool by name (searches both plugins and MCP tools, with lazy loading)
		tool, found := h.findTool(ag, name)

		var result string
		var err error
		var duration time.Duration

		if !found {
			result = fmt.Sprintf("❌ Error: Tool %q not found", name)
			logger.Warn("Tool not found", logger.Fields{"tool": name})
			err = fmt.Errorf("tool not found: %s", name)
		} else {
			toolCtx, toolCancel := context.WithTimeout(baseCtx, ToolExecutionTimeout)

			startTime := time.Now()
			logger.Info("Tool execution starting", logger.Fields{
				"tool":            name,
				"files_available": len(files),
			})

			result, err = ExecuteToolWithFiles(toolCtx, tool, name, args, files)
			duration = time.Since(startTime)
			toolCancel()

			if err != nil {
				result = fmt.Sprintf("Error executing %s: %v", name, err)
				logger.Error("Tool execution failed", logger.Fields{"tool": name, "error": err})
			} else {
				logger.Info("Tool execution completed", logger.Fields{"tool": name})
			}
		}

		if trackUtilityTool {
			errText := ""
			if err != nil {
				errText = err.Error()
			}
			h.utilityTelemetry.RecordToolResult(name, inferUtilityProvider(name, result), err == nil, duration, errText)
		}

		durationMs := int(duration.Milliseconds())

		// Store tool call for review analysis
		var errorMsg string
		if err != nil {
			errorMsg = err.Error()
		}
		h.storeToolCall(baseCtx, sessionID, tc.ID, name, args, result, errorMsg, durationMs)

		receipts = append(receipts, buildActionReceipt(
			"tool_call",
			"Executed tool call",
			"model requested tool execution",
			name,
			args,
			result,
			duration.Milliseconds(),
			err == nil,
			errorMsg,
		))

		results = append(results, ToolCallResult{
			Function:   name,
			Args:       args,
			Result:     result,
			DurationMs: duration.Milliseconds(),
			Success:    err == nil,
		})
	}

	// Process results for structured data
	combined, hasStructured, structuredData := processToolResultsCommon(results)

	return ExecuteToolCallsResult{
		Results:             results,
		CombinedResult:      combined,
		HasStructuredResult: hasStructured,
		StructuredData:      structuredData,
		Receipts:            receipts,
	}
}

// processToolResultsCommon checks tool results for structured data
func processToolResultsCommon(results []ToolCallResult) (combinedResult string, hasStructured bool, structuredData *toolapi.StructuredResult) {
	for i, tr := range results {
		result := tr.Result

		// Check if this is a structured result
		if sr, err := toolapi.ParseStructuredResult(result); err == nil {
			hasStructured = true
			structuredData = sr
		}

		// Legacy: Check if result is valid JSON array
		if !hasStructured && strings.HasPrefix(strings.TrimSpace(result), "[") && strings.HasSuffix(strings.TrimSpace(result), "]") {
			var testJSON []any
			if json.Unmarshal([]byte(result), &testJSON) == nil && len(testJSON) > 0 {
				hasStructured = true
			}
		}

		if i > 0 {
			combinedResult += "\n\n"
		}
		combinedResult += result
	}
	return
}

// trackUsageCommon tracks LLM usage and cost
func (h *Handler) trackUsageCommon(provider, model, agentName string, usage llm.Usage, ag *agent.Agent, userMessage string) {
	if h.costTracker != nil && usage.TotalTokens > 0 {
		if err := h.costTracker.TrackUsage(provider, model, agentName, usage, ""); err != nil {
			logger.Warn("Failed to track usage", logger.Fields{"error": err})
		}
	}

	if usage.TotalTokens > 0 {
		h.trackAgentStatistics(ag, agentName, usage.TotalTokens, provider, model, userMessage)
	}
}

// writeErrorResponse writes a standardized error response
func writeErrorResponse(w http.ResponseWriter, message string) {
	writeJSONResponse(w, attachRouteMetadata(map[string]any{
		"response": fmt.Sprintf("❌ **Error**: %v", message),
	}, chatRouteMetadata{
		Mode: routeModeAssistantChat,
	}))
}

// getFollowUpSystemPrompt returns the system prompt for follow-up requests after tool execution
func getFollowUpSystemPrompt() string {
	return "Use tool output as the source of truth. Do not invent data and do not hide requested details behind high-level summaries. If the user asks for names/items/files/paths, include the exact identifiers from the tool output. For file metadata responses, include filename or path with each metadata block. Keep the response concise and avoid unnecessary follow-up suggestions. For pure action tools (opening apps/projects), provide a brief confirmation."
}

func getFinalToolLoopSynthesisPrompt() string {
	return "You have reached the tool budget for this turn. Do not call any more tools. Using only the tool results already gathered, provide the single best direct answer to the user now. If a tool failed, ignore that failure unless it blocks the answer. Do not return raw JSON or tool logs. Synthesize the answer in normal language."
}

// emptyResponseText is the default text when the model returns an empty response
const emptyResponseText = "I couldn't generate a reply just now. Please try again."

// getResponseText returns the response text, or a default if empty
func getResponseText(content string) string {
	text := strings.TrimSpace(content)
	if text == "" {
		return emptyResponseText
	}
	return text
}

// defaultSystemAgentPrompt is the shared base prompt for the system agent.
// It establishes the coordinator identity used across all providers when no
// agent-specific system prompt is configured.
const defaultSystemAgentPrompt = "You are a helpful assistant that coordinates tasks and delegates to specialists when appropriate. " +
	"Use available tools when they provide a more accurate answer. " +
	"Be concise and direct in your responses."

func resolveSystemPromptForAgent(ag *agent.Agent, defaultPrompt string) string {
	if ag == nil {
		return defaultPrompt
	}

	base := strings.TrimSpace(ag.Settings.SystemPrompt)
	if base == "" {
		base = defaultPrompt
	}

	if ag.Evolution == nil || ag.Evolution.Path == "" {
		return base
	}

	switch ag.Evolution.Path {
	case types.AgentPathCoder:
		return base + "\n\n[Evolution Path: Coder]\nPrioritize implementation accuracy, tests, and concrete code-level fixes."
	case types.AgentPathResearcher:
		return base + "\n\n[Evolution Path: Researcher]\nPrioritize evidence quality, comparisons, and clear assumptions."
	case types.AgentPathWriter:
		return base + "\n\n[Evolution Path: Writer]\nPrioritize clarity, structure, tone, and concise polish."
	default:
		return base
	}
}

// buildSystemPromptWithSkills resolves the agent system prompt and appends
// the prompt text of all enabled skills so the agent benefits from skill
// knowledge during normal chat (not only via explicit /skill invocation).
// When the resolved chat agent carries pre-resolved workspace skills, those
// are used directly; otherwise falls back to SkillManager.
func (h *Handler) buildSystemPromptWithSkills(ag *resolvedChatAgent, agentName, defaultPrompt string) string {
	base := resolveSystemPromptForAgent(ag.Agent, defaultPrompt)

	// Use pre-resolved effective skills from workspace runtime resolution when available.
	if len(ag.EffectiveSkills) > 0 {
		return workspace.AppendSkillPromptsFromResolved(base, ag.EffectiveSkills)
	}

	// Direct, non-workspace chat: the agent's own Default Toolbox decides what
	// is active (PRD FR-24, FR-26).
	if h.skillsManager == nil || agentName == "" {
		return base
	}
	enabledSkills, err := h.skillsManager.ListEnabledSkillsWithPrompts(agentName)
	if err != nil {
		return base
	}
	enabledSkills = selectDefaultToolboxSkills(ag.Agent, enabledSkills)
	if len(enabledSkills) == 0 {
		return base
	}
	var sb strings.Builder
	sb.WriteString(base)
	sb.WriteString("\n\n---\n# Active Skills\n")
	for _, s := range enabledSkills {
		sb.WriteString("\n## ")
		sb.WriteString(s.Name)
		sb.WriteString("\n")
		sb.WriteString(strings.TrimSpace(s.Prompt))
		sb.WriteString("\n")
	}
	return sb.String()
}

// selectDefaultToolboxSkills narrows the agent's available skills to the ones
// its Default Toolbox activates (PRD FR-24, FR-26, FR-27).
//
// The narrowing is what makes direct chat explicit: learning or enabling a
// skill puts it in the agent's collection, but it does not become active in
// direct chat until the user selects it — the same rule workspace Toolboxes
// apply to workspace bindings (FR-2, FR-3).
//
// A nil Default Toolbox means "this agent has not been migrated yet" and falls
// back to the pre-Toolbox behavior of using every enabled skill. Migration
// fills the Default Toolbox from exactly that set, so migrating an agent
// changes nothing about how it answers (FR-28).
//
// A Default Toolbox entry naming a skill that is not currently available
// resolves to nothing here rather than erroring; direct chat has no preview
// surface to report it on, and readiness reporting for Default Toolboxes is
// part of the preview work, not the prompt path.
func selectDefaultToolboxSkills(ag *agent.Agent, available []skills.Skill) []skills.Skill {
	if ag == nil || ag.DefaultToolbox == nil {
		return available
	}
	selected := make([]skills.Skill, 0, len(available))
	for _, skill := range available {
		if ag.DefaultToolbox.Has(skill.Name) {
			selected = append(selected, skill)
		}
	}
	return selected
}

func composeRuntimeSystemPrompt(basePrompt, runtimePrompt string) string {
	base := strings.TrimSpace(basePrompt)
	runtime := strings.TrimSpace(runtimePrompt)
	if runtime == "" {
		return base
	}
	if base == "" {
		return runtime
	}
	return base + "\n\n---\n# Runtime Context\n" + runtime
}

func prioritizeToolsForPath(ag *agent.Agent, tools []llm.Tool) []llm.Tool {
	if ag == nil || len(tools) <= 1 || ag.Evolution == nil || ag.Evolution.Path == "" {
		return tools
	}

	scored := make([]struct {
		tool  llm.Tool
		score int
		idx   int
	}, 0, len(tools))

	for idx, tool := range tools {
		scored = append(scored, struct {
			tool  llm.Tool
			score int
			idx   int
		}{
			tool:  tool,
			score: scoreToolForPath(ag.Evolution.Path, tool.Name, tool.Description),
			idx:   idx,
		})
	}

	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score == scored[j].score {
			return scored[i].idx < scored[j].idx
		}
		return scored[i].score > scored[j].score
	})

	reordered := make([]llm.Tool, 0, len(tools))
	for _, item := range scored {
		reordered = append(reordered, item.tool)
	}
	return reordered
}

func scoreToolForPath(path types.AgentPath, name, description string) int {
	text := strings.ToLower(name + " " + description)
	score := 0

	addIfContains := func(words []string, points int) {
		for _, word := range words {
			if strings.Contains(text, word) {
				score += points
			}
		}
	}

	switch path {
	case types.AgentPathCoder:
		addIfContains([]string{"code", "git", "file", "shell", "build", "test", "repo"}, 3)
	case types.AgentPathResearcher:
		addIfContains([]string{"search", "web", "crawl", "query", "research", "docs", "fetch"}, 3)
	case types.AgentPathWriter:
		addIfContains([]string{"write", "summary", "format", "note", "draft", "document"}, 3)
	}

	return score
}
