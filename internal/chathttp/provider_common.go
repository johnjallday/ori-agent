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

	var parsed interface{}
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
	agentName string,
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
		tool, found := h.findTool(ag, agentName, name)

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
			var testJSON []interface{}
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
		return appendSkillPromptsFromResolved(base, ag.EffectiveSkills)
	}

	// Fallback: load from SkillManager (for non-workspace contexts).
	if h.skillsManager == nil || agentName == "" {
		return base
	}
	enabledSkills, err := h.skillsManager.ListEnabledSkillsWithPrompts(agentName)
	if err != nil || len(enabledSkills) == 0 {
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

func appendSkillPromptsFromResolved(base string, skills []workspace.ResolvedSkill) string {
	var hasPrompt bool
	for _, s := range skills {
		if strings.TrimSpace(s.Prompt) != "" || strings.TrimSpace(formatResolvedSkillRuntimeSettings(s)) != "" {
			hasPrompt = true
			break
		}
	}
	if !hasPrompt {
		return base
	}
	var sb strings.Builder
	sb.WriteString(base)
	sb.WriteString("\n\n---\n# Active Skills\n")
	for _, s := range skills {
		prompt := strings.TrimSpace(s.Prompt)
		settings := strings.TrimSpace(formatResolvedSkillRuntimeSettings(s))
		if prompt == "" && settings == "" {
			continue
		}
		sb.WriteString("\n## ")
		sb.WriteString(s.Name)
		sb.WriteString("\n")
		if prompt != "" {
			sb.WriteString(prompt)
			sb.WriteString("\n")
		}
		if settings != "" {
			sb.WriteString("\n### Workspace Binding Settings\n")
			sb.WriteString(settings)
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

func formatResolvedSkillRuntimeSettings(skill workspace.ResolvedSkill) string {
	if !skill.PlanningProfile || len(skill.Config) == 0 {
		return ""
	}

	type planningSettings struct {
		ProfileType          string `json:"profile_type,omitempty"`
		Mode                 string `json:"mode,omitempty"`
		WritePRD             bool   `json:"write_prd,omitempty"`
		WriteTaskList        bool   `json:"write_task_list,omitempty"`
		TasksDir             string `json:"tasks_dir,omitempty"`
		ClarificationMode    string `json:"clarification_mode,omitempty"`
		SyncWorkspaceTasks   bool   `json:"sync_workspace_tasks,omitempty"`
		DefaultExecutionMode string `json:"default_execution_mode,omitempty"`
		RequireBranch        bool   `json:"require_branch,omitempty"`
	}

	settings := planningSettings{
		ProfileType:          "workspace_planning",
		Mode:                 "feature",
		WritePRD:             true,
		WriteTaskList:        true,
		TasksDir:             "tasks",
		ClarificationMode:    "standard",
		SyncWorkspaceTasks:   true,
		DefaultExecutionMode: "step_through",
		RequireBranch:        true,
	}
	if value := strings.TrimSpace(stringConfigValue(skill.Config, "profile_type")); value != "" {
		settings.ProfileType = value
	}
	if value := strings.TrimSpace(stringConfigValue(skill.Config, "mode")); value != "" {
		settings.Mode = value
	}
	if value := strings.TrimSpace(stringConfigValue(skill.Config, "tasks_dir")); value != "" {
		settings.TasksDir = value
	}
	if value := strings.TrimSpace(stringConfigValue(skill.Config, "clarification_mode")); value != "" {
		settings.ClarificationMode = value
	}
	if value := strings.TrimSpace(stringConfigValue(skill.Config, "default_execution_mode")); value != "" {
		settings.DefaultExecutionMode = value
	}
	settings.WritePRD = boolConfigValue(skill.Config, "write_prd", settings.WritePRD)
	settings.WriteTaskList = boolConfigValue(skill.Config, "write_task_list", settings.WriteTaskList)
	settings.SyncWorkspaceTasks = boolConfigValue(skill.Config, "sync_workspace_tasks", settings.SyncWorkspaceTasks)
	settings.RequireBranch = boolConfigValue(skill.Config, "require_branch", settings.RequireBranch)

	artifacts := make([]string, 0, 2)
	if settings.WritePRD {
		artifacts = append(artifacts, "PRD")
	}
	if settings.WriteTaskList {
		artifacts = append(artifacts, "task list")
	}
	if len(artifacts) == 0 {
		artifacts = append(artifacts, "none by default")
	}

	lines := []string{
		"Use these workspace-level planning defaults unless the user explicitly asks for something different.",
		fmt.Sprintf("- Planning mode: %s", settings.Mode),
		fmt.Sprintf("- Preferred planning artifacts: %s", strings.Join(artifacts, ", ")),
		fmt.Sprintf("- Save planning files under: %s", settings.TasksDir),
		fmt.Sprintf("- Clarification depth: %s", settings.ClarificationMode),
		fmt.Sprintf("- Sync approved plans into workspace tasks: %t", settings.SyncWorkspaceTasks),
		fmt.Sprintf("- Default workspace task execution mode: %s", settings.DefaultExecutionMode),
		fmt.Sprintf("- Require feature branch before implementation: %t", settings.RequireBranch),
	}

	if payload, err := json.MarshalIndent(settings, "", "  "); err == nil {
		lines = append(lines, "- Normalized config JSON:", string(payload))
	}

	return strings.Join(lines, "\n")
}

func stringConfigValue(config map[string]interface{}, key string) string {
	if len(config) == 0 {
		return ""
	}
	value, ok := config[key]
	if !ok {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return fmt.Sprintf("%v", value)
	}
}

func boolConfigValue(config map[string]interface{}, key string, fallback bool) bool {
	if len(config) == 0 {
		return fallback
	}
	value, ok := config[key]
	if !ok {
		return fallback
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		normalized := strings.TrimSpace(strings.ToLower(typed))
		switch normalized {
		case "true", "yes", "1":
			return true
		case "false", "no", "0":
			return false
		default:
			return fallback
		}
	default:
		return fallback
	}
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
