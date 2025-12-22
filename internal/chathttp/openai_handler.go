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

// handleOpenAIChat handles chat requests for OpenAI models
func (h *Handler) handleOpenAIChat(
	w http.ResponseWriter,
	ag *agent.Agent,
	userMessage string,
	tools []llm.Tool,
	agentName string,
	baseCtx context.Context,
	files []pluginapi.FileAttachment,
	agentClient openai.Client,
) {
	ctx, cancel := context.WithTimeout(baseCtx, ContextTimeout)
	defer cancel()

	// Convert llm.Tool to OpenAI format
	var openaiTools []openai.ChatCompletionToolUnionParam
	for _, tool := range tools {
		funcDef := openai.FunctionDefinitionParam{
			Name:        tool.Name,
			Description: openai.String(tool.Description),
			Parameters:  openai.FunctionParameters(tool.Parameters),
		}
		openaiTools = append(openaiTools, openai.ChatCompletionFunctionTool(funcDef))
	}

	params := openai.ChatCompletionNewParams{
		Model:       openai.ChatModel(ag.Settings.Model),
		Temperature: openai.Float(ag.Settings.Temperature),
		Messages:    ag.Messages,
		Tools:       openaiTools,
	}

	start := time.Now()
	resp, err := agentClient.Chat.Completions.New(ctx, params)
	if err != nil {
		errorResponse := map[string]any{
			"response": fmt.Sprintf("❌ **Error**: %v", err),
		}
		writeJSONResponse(w, errorResponse)
		return
	}
	if resp == nil || len(resp.Choices) == 0 {
		orihttp.WriteJSON(w, map[string]any{
			"response": "I couldn't generate a reply just now. Please try again.",
		})
		return
	}

	// Track usage and cost for OpenAI
	if h.costTracker != nil && resp.Usage.TotalTokens > 0 {
		usage := llm.Usage{
			PromptTokens:     int(resp.Usage.PromptTokens),
			CompletionTokens: int(resp.Usage.CompletionTokens),
			TotalTokens:      int(resp.Usage.TotalTokens),
		}
		if err := h.costTracker.TrackUsage("openai", string(ag.Settings.Model), agentName, usage, ""); err != nil {
			logger.Warn("Failed to track usage", logger.Fields{"error": err})
		}
		h.trackAgentStatistics(ag, usage.TotalTokens, "openai", string(ag.Settings.Model))
	}

	choice := resp.Choices[0].Message

	// Fallback if model answered with an empty assistant message and no tool calls
	if len(choice.ToolCalls) == 0 && strings.TrimSpace(choice.Content) == "" {
		fbCtx, fbCancel := context.WithTimeout(baseCtx, 20*time.Second)
		defer fbCancel()

		respFB, errFB := agentClient.Chat.Completions.New(fbCtx, openai.ChatCompletionNewParams{
			Model:       openai.ChatModel(ag.Settings.Model),
			Temperature: openai.Float(ag.Settings.Temperature),
			Messages: append(ag.Messages,
				openai.SystemMessage("Answer directly in plain text. Do not call any tools."),
			),
		})
		if errFB == nil && respFB != nil && len(respFB.Choices) > 0 {
			choice = respFB.Choices[0].Message

			if h.costTracker != nil && respFB.Usage.TotalTokens > 0 {
				usage := llm.Usage{
					PromptTokens:     int(respFB.Usage.PromptTokens),
					CompletionTokens: int(respFB.Usage.CompletionTokens),
					TotalTokens:      int(respFB.Usage.TotalTokens),
				}
				if err := h.costTracker.TrackUsage("openai", string(ag.Settings.Model), agentName, usage, ""); err != nil {
					logger.Warn("Failed to track fallback usage", logger.Fields{"error": err})
				}
				h.trackAgentStatistics(ag, usage.TotalTokens, "openai", string(ag.Settings.Model))
			}
		}
	}

	// Tool-call branch
	if len(choice.ToolCalls) > 0 {
		h.handleOpenAIToolCalls(w, ag, agentName, baseCtx, ctx, files, agentClient, choice, start)
		return
	}

	// Plain answer path
	text := strings.TrimSpace(choice.Content)
	if text == "" {
		text = "I couldn't generate a reply just now. Please try again."
	}
	ag.Messages = append(ag.Messages, choice.ToParam())

	logger.Debug("Chat response completed", logger.Fields{"duration": time.Since(start), "response": text})
	_ = h.store.SetAgent(agentName, ag)
	writeJSONResponse(w, map[string]any{"response": text})
}

// handleOpenAIToolCalls handles the tool execution loop for OpenAI models
func (h *Handler) handleOpenAIToolCalls(
	w http.ResponseWriter,
	ag *agent.Agent,
	agentName string,
	baseCtx context.Context,
	ctx context.Context,
	files []pluginapi.FileAttachment,
	agentClient openai.Client,
	choice openai.ChatCompletionMessage,
	start time.Time,
) {
	// Append the assistant message with tool calls first
	ag.Messages = append(ag.Messages, choice.ToParam())

	// Process ALL tool calls
	var toolResults []map[string]string
	for _, tc := range choice.ToolCalls {
		name := tc.Function.Name
		args := tc.Function.Arguments

		tool, found := h.findTool(ag, name)
		if !found {
			if err := orihttp.RespondInternalError(w, fmt.Sprintf("tool %q not found", name)); err != nil {
				logger.Error("Failed to write response", logger.Fields{"error": err})
			}
			return
		}

		toolCtx, toolCancel := context.WithTimeout(baseCtx, ToolExecutionTimeout)
		defer toolCancel()

		startTime := time.Now()

		logger.Info("OpenAI tool execution starting", logger.Fields{
			"tool":            name,
			"files_available": len(files),
		})

		result, err := ExecuteToolWithFiles(toolCtx, tool, name, args, files)
		duration := time.Since(startTime)

		if h.healthManager != nil {
			if err != nil {
				h.healthManager.RecordCallFailure(name, duration, err)
			} else {
				h.healthManager.RecordCallSuccess(name, duration)
			}
		}

		if err != nil {
			errorMsg := fmt.Sprintf("❌ Error executing %s: %v", name, err)
			result = errorMsg
			logger.Error("Tool failed", logger.Fields{"tool": name, "error": err})
		} else {
			logger.Info("Tool execution completed", logger.Fields{"tool": name})
		}

		ag.Messages = append(ag.Messages, openai.ToolMessage(result, tc.ID))
		toolResults = append(toolResults, map[string]string{
			"function": name,
			"args":     args,
			"result":   result,
		})
	}

	// Check for structured results
	combinedResult, hasStructuredResult, structuredResultData := h.processToolResults(toolResults)

	if hasStructuredResult {
		ag.Messages = append(ag.Messages, openai.AssistantMessage(combinedResult))
		logger.Debug("Chat with structured tool result completed", logger.Fields{"duration": time.Since(start)})
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

	// Ask model again with tool output
	resp2, err := agentClient.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model:       openai.ChatModel(ag.Settings.Model),
		Temperature: openai.Float(ag.Settings.Temperature),
		Messages: append(ag.Messages,
			openai.SystemMessage("The tool was executed successfully. Simply acknowledge the result without suggesting follow-up actions or next steps. If the tool returned configuration data, settings, or structured information, display that data clearly. For action tools (like opening projects, launching applications), provide only a brief confirmation."),
		),
	})
	if err != nil || resp2 == nil || len(resp2.Choices) == 0 {
		orihttp.WriteJSON(w, map[string]any{
			"response":  combinedResult,
			"toolCalls": toolResults,
		})
		return
	}

	if h.costTracker != nil && resp2.Usage.TotalTokens > 0 {
		usage := llm.Usage{
			PromptTokens:     int(resp2.Usage.PromptTokens),
			CompletionTokens: int(resp2.Usage.CompletionTokens),
			TotalTokens:      int(resp2.Usage.TotalTokens),
		}
		if err := h.costTracker.TrackUsage("openai", string(ag.Settings.Model), agentName, usage, ""); err != nil {
			logger.Warn("Failed to track usage for tool response", logger.Fields{"error": err})
		}
	}

	final := resp2.Choices[0].Message
	ag.Messages = append(ag.Messages, final.ToParam())

	logger.Debug("Chat with tool completed", logger.Fields{"duration": time.Since(start)})
	_ = h.store.SetAgent(agentName, ag)
	orihttp.WriteJSON(w, map[string]any{
		"response":  final.Content,
		"toolCalls": toolResults,
	})
}

// processToolResults checks tool results for structured data and returns combined result
func (h *Handler) processToolResults(toolResults []map[string]string) (combinedResult string, hasStructuredResult bool, structuredResultData *pluginapi.StructuredResult) {
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
	return
}
