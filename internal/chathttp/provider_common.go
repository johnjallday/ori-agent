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
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/oriagent/ori-pluginapi"
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
	StructuredData      *pluginapi.StructuredResult
	Receipts            []ActionReceipt
}

// executeToolCallsCommonWithSession executes tool calls and stores them for the given session
func (h *Handler) executeToolCallsCommonWithSession(
	baseCtx context.Context,
	ag *agent.Agent,
	agentName string,
	toolCalls []llm.ToolCall,
	files []pluginapi.FileAttachment,
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

			// Record call stats in health manager
			h.recordToolCallStats(name, duration, err)

			if err != nil {
				result = augmentToolExecutionError(name, args, err)
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
func processToolResultsCommon(results []ToolCallResult) (combinedResult string, hasStructured bool, structuredData *pluginapi.StructuredResult) {
	for i, tr := range results {
		result := tr.Result

		// Check if this is a structured result
		if sr, err := pluginapi.ParseStructuredResult(result); err == nil {
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

// recordToolCallStats records tool call statistics in health manager
func (h *Handler) recordToolCallStats(toolName string, duration time.Duration, err error) {
	if h.healthManager != nil {
		if err != nil {
			h.healthManager.RecordCallFailure(toolName, duration, err)
		} else {
			h.healthManager.RecordCallSuccess(toolName, duration)
		}
	}
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

func resolveSystemPromptForAgent(ag *agent.Agent, defaultPrompt string) string {
	if ag == nil {
		return defaultPrompt
	}
	if strings.TrimSpace(ag.Settings.SystemPrompt) != "" {
		// Respect explicit user override.
		return ag.Settings.SystemPrompt
	}

	if ag.Evolution == nil {
		return defaultPrompt
	}

	switch ag.Evolution.Path {
	case types.AgentPathCoder:
		return defaultPrompt + " You are on the Coder path: prioritize implementation accuracy, tests, and concrete code-level fixes."
	case types.AgentPathResearcher:
		return defaultPrompt + " You are on the Researcher path: prioritize evidence quality, comparisons, and clear assumptions."
	case types.AgentPathWriter:
		return defaultPrompt + " You are on the Writer path: prioritize clarity, structure, tone, and concise polish."
	default:
		return defaultPrompt
	}
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
