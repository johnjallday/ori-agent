package chathttp

import (
	"context"
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

	llmToolCalls := make([]llm.ToolCall, 0, len(choice.ToolCalls))
	for _, tc := range choice.ToolCalls {
		llmToolCalls = append(llmToolCalls, llm.ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}

	execResult := h.executeToolCallsCommonWithSession(baseCtx, ag, agentName, llmToolCalls, files, sessionID)

	for i, tc := range llmToolCalls {
		if i >= len(execResult.Results) {
			break
		}
		ag.Messages = append(ag.Messages, openai.ToolMessage(execResult.Results[i].Result, tc.ID))
	}

	if execResult.HasStructuredResult {
		ag.Messages = append(ag.Messages, openai.AssistantMessage(execResult.CombinedResult))
		logger.Debug("Chat with structured tool result completed", logger.Fields{"duration": time.Since(start)})
		_ = h.store.SetAgent(agentName, ag)

		// Store assistant response in session
		h.storeMessageInSession(baseCtx, sessionID, "assistant", execResult.CombinedResult)

		response := attachActionReceipts(attachRouteMetadata(map[string]any{
			"response":  execResult.CombinedResult,
			"toolCalls": execResult.Results,
		}, chatRouteMetadata{
			Mode:      routeModeAssistantChat,
			ToolCount: len(execResult.Results),
		}), execResult.Receipts)

		if execResult.StructuredData != nil {
			response["structured"] = true
			response["displayType"] = string(execResult.StructuredData.DisplayType)
			response["title"] = execResult.StructuredData.Title
			response["description"] = execResult.StructuredData.Description
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
		orihttp.WriteJSON(w, attachPlannerDecision(attachActionReceipts(attachRouteMetadata(map[string]any{
			"response":  execResult.CombinedResult,
			"toolCalls": execResult.Results,
		}, chatRouteMetadata{
			Mode:      routeModeAssistantChat,
			ToolCount: len(execResult.Results),
		}), execResult.Receipts), plannerDecision))
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

	orihttp.WriteJSON(w, attachPlannerDecision(attachActionReceipts(attachRouteMetadata(map[string]any{
		"response":  final.Content,
		"toolCalls": execResult.Results,
	}, chatRouteMetadata{
		Mode:      routeModeAssistantChat,
		ToolCount: len(execResult.Results),
	}), execResult.Receipts), plannerDecision))
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
