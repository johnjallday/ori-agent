package chathttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"

	"github.com/johnjallday/ori-agent/internal/agent"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/oriagent/ori-pluginapi"
)

const (
	openAITransportRetryAttempts = 1
	openAITransportRetryDelay    = 300 * time.Millisecond
)

// handleOpenAIChat handles chat requests for OpenAI models
func (h *Handler) handleOpenAIChat(
	w http.ResponseWriter,
	r *http.Request,
	ag *agent.Agent,
	userMessage string,
	tools []llm.Tool,
	agentName string,
	baseCtx context.Context,
	files []pluginapi.FileAttachment,
	agentClient openai.Client,
	plannerDecision *types.PlannerDecision,
) {
	sessionID := h.getSessionID(r)
	ctx, cancel := context.WithTimeout(baseCtx, ChatRequestTimeout)
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
	resp, err := requestOpenAICompletionWithRetry(ctx, agentClient, params)
	if err != nil {
		errorResponse := attachPlannerDecision(attachRouteMetadata(map[string]any{
			"response": fmt.Sprintf("❌ **Error**: %v", err),
		}, chatRouteMetadata{
			Mode: routeModeAssistantChat,
		}), plannerDecision)
		writeJSONResponse(w, errorResponse)
		return
	}
	if resp == nil || len(resp.Choices) == 0 {
		orihttp.WriteJSON(w, attachPlannerDecision(attachRouteMetadata(map[string]any{
			"response": "I couldn't generate a reply just now. Please try again.",
		}, chatRouteMetadata{
			Mode: routeModeAssistantChat,
		}), plannerDecision))
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
		h.trackAgentStatistics(ag, agentName, usage.TotalTokens, "openai", string(ag.Settings.Model), userMessage)
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
				h.trackAgentStatistics(ag, agentName, usage.TotalTokens, "openai", string(ag.Settings.Model), userMessage)
			}
		}
	}

	// Tool-call branch
	if len(choice.ToolCalls) > 0 {
		h.handleOpenAIToolCalls(w, ag, agentName, baseCtx, ctx, files, agentClient, choice, start, sessionID, plannerDecision)
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

	// Store assistant response in session
	h.storeMessageInSession(baseCtx, sessionID, "assistant", text)

	writeJSONResponse(w, attachPlannerDecision(attachRouteMetadata(map[string]any{
		"response": text,
	}, chatRouteMetadata{
		Mode: routeModeAssistantChat,
	}), plannerDecision))
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
	sessionID string,
	plannerDecision *types.PlannerDecision,
) {
	// Append the assistant message with tool calls first
	ag.Messages = append(ag.Messages, choice.ToParam())

	// Process ALL tool calls
	var toolResults []map[string]string
	for _, tc := range choice.ToolCalls {
		name := tc.Function.Name
		args := tc.Function.Arguments

		tool, found := h.findTool(ag, agentName, name)
		if !found {
			orihttp.InternalError(w, fmt.Sprintf("tool %q not found", name))
			return
		}

		toolCtx, toolCancel := context.WithTimeout(baseCtx, ToolExecutionTimeout)

		startTime := time.Now()

		logger.Info("OpenAI tool execution starting", logger.Fields{
			"tool":            name,
			"files_available": len(files),
		})

		result, err := ExecuteToolWithFiles(toolCtx, tool, name, args, files)
		toolCancel() // Cancel context immediately after use to avoid leak

		duration := time.Since(startTime)
		durationMs := int(duration.Milliseconds())

		if h.healthManager != nil {
			if err != nil {
				h.healthManager.RecordCallFailure(name, duration, err)
			} else {
				h.healthManager.RecordCallSuccess(name, duration)
			}
		}

		var errorMsg string
		if err != nil {
			errorMsg = err.Error()
			result = fmt.Sprintf("❌ Error executing %s: %v", name, err)
			logger.Error("Tool failed", logger.Fields{"tool": name, "error": err})
		} else {
			logger.Info("Tool execution completed", logger.Fields{"tool": name})
		}

		// Store tool call for review analysis
		h.storeToolCall(baseCtx, sessionID, tc.ID, name, args, result, errorMsg, durationMs)

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

		// Store assistant response in session
		h.storeMessageInSession(baseCtx, sessionID, "assistant", combinedResult)

		response := attachRouteMetadata(map[string]any{
			"response":  combinedResult,
			"toolCalls": toolResults,
		}, chatRouteMetadata{
			Mode:      routeModeAssistantChat,
			ToolCount: len(toolResults),
		})

		if structuredResultData != nil {
			response["structured"] = true
			response["displayType"] = string(structuredResultData.DisplayType)
			response["title"] = structuredResultData.Title
			response["description"] = structuredResultData.Description
		}

		writeJSONResponse(w, attachPlannerDecision(response, plannerDecision))
		return
	}

	// Ask model again with tool output
	resp2, err := agentClient.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model:       openai.ChatModel(ag.Settings.Model),
		Temperature: openai.Float(ag.Settings.Temperature),
		Messages: append(ag.Messages,
			openai.SystemMessage(getFollowUpSystemPrompt()),
		),
	})
	if err != nil || resp2 == nil || len(resp2.Choices) == 0 {
		orihttp.WriteJSON(w, attachPlannerDecision(attachRouteMetadata(map[string]any{
			"response":  combinedResult,
			"toolCalls": toolResults,
		}, chatRouteMetadata{
			Mode:      routeModeAssistantChat,
			ToolCount: len(toolResults),
		}), plannerDecision))
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

	// Store assistant response in session
	h.storeMessageInSession(baseCtx, sessionID, "assistant", final.Content)

	orihttp.WriteJSON(w, attachPlannerDecision(attachRouteMetadata(map[string]any{
		"response":  final.Content,
		"toolCalls": toolResults,
	}, chatRouteMetadata{
		Mode:      routeModeAssistantChat,
		ToolCount: len(toolResults),
	}), plannerDecision))
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

func requestOpenAICompletionWithRetry(
	ctx context.Context,
	agentClient openai.Client,
	params openai.ChatCompletionNewParams,
) (*openai.ChatCompletion, error) {
	resp, err := agentClient.Chat.Completions.New(ctx, params)
	if err == nil {
		return resp, nil
	}

	if !isRetryableOpenAITransportError(ctx, err) {
		return nil, err
	}

	for attempt := 1; attempt <= openAITransportRetryAttempts; attempt++ {
		logger.Warn("OpenAI transport error, retrying completion request", logger.Fields{
			"attempt": attempt,
			"error":   err.Error(),
		})

		timer := time.NewTimer(openAITransportRetryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}

		resp, err = agentClient.Chat.Completions.New(ctx, params)
		if err == nil {
			return resp, nil
		}
		if !isRetryableOpenAITransportError(ctx, err) {
			return nil, err
		}
	}

	return nil, err
}

func isRetryableOpenAITransportError(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	if ctx != nil && ctx.Err() != nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}

	var timeoutErr interface{ Timeout() bool }
	if errors.As(err, &timeoutErr) && timeoutErr.Timeout() {
		return true
	}

	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "context deadline exceeded") ||
		strings.Contains(msg, "client.timeout exceeded") ||
		strings.Contains(msg, "timeout while awaiting headers") ||
		strings.Contains(msg, "tls handshake timeout") ||
		strings.Contains(msg, "connection reset by peer")
}
