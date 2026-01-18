package chathttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
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

	for _, tc := range toolCalls {
		name := tc.Name
		args := tc.Arguments

		logger.Debug("Executing tool", logger.Fields{"name": name})

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

		durationMs := int(duration.Milliseconds())

		// Store tool call for review analysis
		var errorMsg string
		if err != nil {
			errorMsg = err.Error()
		}
		h.storeToolCall(baseCtx, sessionID, tc.ID, name, args, result, errorMsg, durationMs)

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
func (h *Handler) trackUsageCommon(provider, model, agentName string, usage llm.Usage, ag *agent.Agent) {
	if h.costTracker != nil && usage.TotalTokens > 0 {
		if err := h.costTracker.TrackUsage(provider, model, agentName, usage, ""); err != nil {
			logger.Warn("Failed to track usage", logger.Fields{"error": err})
		}
	}

	if usage.TotalTokens > 0 {
		h.trackAgentStatistics(ag, usage.TotalTokens, provider, model)
	}
}

// writeErrorResponse writes a standardized error response
func writeErrorResponse(w http.ResponseWriter, message string) {
	writeJSONResponse(w, map[string]any{
		"response": fmt.Sprintf("❌ **Error**: %v", message),
	})
}

// getFollowUpSystemPrompt returns the system prompt for follow-up requests after tool execution
func getFollowUpSystemPrompt() string {
	return "The tool was executed successfully. Simply acknowledge the result without suggesting follow-up actions or next steps. If the tool returned configuration data, settings, or structured information, display that data clearly. For action tools (like opening projects, launching applications), provide only a brief confirmation."
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
