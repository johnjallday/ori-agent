package chathttp

import (
	"context"
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

// handleOllamaChat handles chat requests for Ollama models using the provider system
func (h *Handler) handleOllamaChat(w http.ResponseWriter, r *http.Request, ag *agent.Agent, userMessage string, tools []llm.Tool, agentName string, baseCtx context.Context, files []pluginapi.FileAttachment) {
	ctx, cancel := context.WithTimeout(baseCtx, ChatRequestTimeout)
	defer cancel()

	// Get Ollama provider
	provider, err := h.llmFactory.GetProvider("ollama")
	if err != nil {
		orihttp.WriteJSON(w, map[string]any{
			"response": fmt.Sprintf("❌ **Error**: Ollama provider not available: %v", err),
		})
		return
	}

	// Build message list
	var messages []llm.Message

	// Add system message - use custom if set, otherwise use default that emphasizes tool usage
	systemPrompt := ag.Settings.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = "You are a helpful assistant with access to tools. When you use a tool and receive results, report those results directly to the user. Be concise and accurate."
	}
	messages = append(messages, llm.NewSystemMessage(systemPrompt))

	// Add user message
	messages = append(messages, llm.NewUserMessage(userMessage))

	// Tools are already in generic llm.Tool format, no conversion needed

	// Call Ollama
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

	// Track usage (Ollama is free/local, so no cost tracking needed)
	logger.Debug("Ollama response received", logger.Fields{"duration": time.Since(start)})

	// Track statistics in agent (with zero cost for Ollama)
	if resp.Usage.TotalTokens > 0 {
		h.trackAgentStatistics(ag, resp.Usage.TotalTokens, "ollama", ag.Settings.Model)
	}

	// Tool-call branch (similar to Claude handler)
	if len(resp.ToolCalls) > 0 {
		logger.Info("Ollama requested tool calls", logger.Fields{"count": len(resp.ToolCalls)})

		// Add assistant message to history WITH tool calls (important for Ollama protocol)
		assistantMsg := llm.NewAssistantMessage(resp.Content)
		assistantMsg.ToolCalls = resp.ToolCalls
		messages = append(messages, assistantMsg)
		ag.Messages = append(ag.Messages, openai.AssistantMessage(resp.Content))

		// Process tool calls
		var toolResults []map[string]string
		for _, tc := range resp.ToolCalls {
			logger.Debug("Looking for tool", logger.Fields{"name": tc.Name, "args": tc.Arguments})
			tool, found := h.findTool(ag, tc.Name)

			var result string
			if !found {
				logger.Warn("Tool not found", logger.Fields{"tool": tc.Name})
				result = fmt.Sprintf("❌ Error: Tool %q not found", tc.Name)
			} else {
				logger.Debug("Tool found", logger.Fields{"tool": tc.Name})
				toolCtx, toolCancel := context.WithTimeout(baseCtx, ToolExecutionTimeout)
				defer toolCancel()

				startTime := time.Now()

				result, err = ExecuteToolWithFilesDebug(toolCtx, tool, tc.Name, tc.Arguments, files)
				duration := time.Since(startTime)

				if h.healthManager != nil {
					if err != nil {
						h.healthManager.RecordCallFailure(tc.Name, duration, err)
					} else {
						h.healthManager.RecordCallSuccess(tc.Name, duration)
					}
				}

				if err != nil {
					logger.Error("Tool execution failed", logger.Fields{"tool": tc.Name, "error": err})
					result = augmentToolExecutionError(tc.Name, tc.Arguments, err)
				} else {
					logger.Debug("Tool executed successfully", logger.Fields{"tool": tc.Name, "result": result})
				}
			}

			messages = append(messages, llm.NewToolMessage(tc.ID, result))
			ag.Messages = append(ag.Messages, openai.ToolMessage(result, tc.ID))

			toolResults = append(toolResults, map[string]string{
				"function": tc.Name,
				"args":     tc.Arguments,
				"result":   result,
			})
		}

		logger.Debug("Sending tool results back to LLM", logger.Fields{"message_count": len(messages)})

		// Get final response after tool execution
		// IMPORTANT: Must include Tools array again for Ollama to understand the tool calling context
		finalResp, err := provider.Chat(ctx, llm.ChatRequest{
			Model:       ag.Settings.Model,
			Messages:    messages,
			Tools:       tools, // Include tools in follow-up request
			Temperature: ag.Settings.Temperature,
			MaxTokens:   4000,
		})
		if err != nil {
			logger.Error("Error getting final response from LLM", logger.Fields{"error": err})
			orihttp.WriteJSON(w, map[string]any{
				"response": fmt.Sprintf("❌ **Error**: %v", err),
			})
			return
		}

		finalText := strings.TrimSpace(finalResp.Content)
		if finalText == "" && len(toolResults) > 0 {
			var b strings.Builder
			for i, tr := range toolResults {
				if i > 0 {
					b.WriteString("\n\n")
				}
				b.WriteString(fmt.Sprintf("**%s**\n\n%s", tr["function"], tr["result"]))
			}
			finalText = b.String()
		}

		logger.Debug("Final response from LLM", logger.Fields{"content": finalText})

		ag.Messages = append(ag.Messages, openai.AssistantMessage(finalText))
		_ = h.store.SetAgent(agentName, ag)

		orihttp.WriteJSON(w, map[string]any{
			"response":  finalText,
			"toolCalls": toolResults,
		})
		return
	}

	// No tool calls - direct response
	ag.Messages = append(ag.Messages, openai.AssistantMessage(resp.Content))
	_ = h.store.SetAgent(agentName, ag)
	writeJSONResponse(w, map[string]any{"response": resp.Content})
}
