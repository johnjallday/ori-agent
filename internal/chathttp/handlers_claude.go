package chathttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"

	"github.com/johnjallday/ori-agent/internal/agent"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/pluginapi"
)

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
