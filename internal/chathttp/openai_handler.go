package chathttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"

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
	ag *resolvedChatAgent,
	userMessage string,
	tools []llm.Tool,
	agentName string,
	baseCtx context.Context,
	files []pluginapi.FileAttachment,
	agentClient openai.Client,
	plannerDecision *types.PlannerDecision,
	runtimeSystemPrompt string,
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
		Messages:    injectRuntimeSystemPrompt(ag.Messages, runtimeSystemPrompt),
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
		h.trackAgentStatistics(ag.Agent, agentName, usage.TotalTokens, "openai", string(ag.Settings.Model), userMessage)
	}

	choice := resp.Choices[0].Message

	// Fallback if model answered with an empty assistant message and no tool calls
	if len(choice.ToolCalls) == 0 && strings.TrimSpace(choice.Content) == "" {
		fbCtx, fbCancel := context.WithTimeout(baseCtx, 20*time.Second)
		defer fbCancel()
		fallbackMessages := injectRuntimeSystemPrompt(ag.Messages, runtimeSystemPrompt)

		respFB, errFB := agentClient.Chat.Completions.New(fbCtx, openai.ChatCompletionNewParams{
			Model:       openai.ChatModel(ag.Settings.Model),
			Temperature: openai.Float(ag.Settings.Temperature),
			Messages: append(fallbackMessages,
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
				h.trackAgentStatistics(ag.Agent, agentName, usage.TotalTokens, "openai", string(ag.Settings.Model), userMessage)
			}
		}
	}

	// Tool-call branch
	if len(choice.ToolCalls) > 0 {
		h.handleOpenAIToolCalls(w, ag, agentName, baseCtx, ctx, files, agentClient, choice, openaiTools, start, sessionID, plannerDecision, runtimeSystemPrompt)
		return
	}

	// Plain answer path
	text := strings.TrimSpace(choice.Content)
	if text == "" {
		text = "I couldn't generate a reply just now. Please try again."
	}
	ag.Messages = append(ag.Messages, choice.ToParam())

	logger.Debug("Chat response completed", logger.Fields{"duration": time.Since(start), "response": text})
	_ = h.persistAgent(agentName, ag.Agent)

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
	ag *resolvedChatAgent,
	agentName string,
	baseCtx context.Context,
	ctx context.Context,
	files []pluginapi.FileAttachment,
	agentClient openai.Client,
	choice openai.ChatCompletionMessage,
	openaiTools []openai.ChatCompletionToolUnionParam,
	start time.Time,
	sessionID string,
	plannerDecision *types.PlannerDecision,
	runtimeSystemPrompt string,
) {
	loopResult := h.runBoundedToolLoop(
		choice.Content,
		convertOpenAIToolCallsToLLM(choice.ToolCalls),
		boundedToolLoopConfig{},
		boundedToolLoopCallbacks{
			AppendAssistantTurn: func(content string, toolCalls []llm.ToolCall) {
				ag.Messages = append(ag.Messages, buildOpenAIAssistantToolCallMessage(content, toolCalls))
			},
			ExecuteToolCalls: func(toolCalls []llm.ToolCall) ExecuteToolCallsResult {
				return h.executeToolCallsCommonWithSession(baseCtx, ag, agentName, toolCalls, files, sessionID)
			},
			AppendToolResults: func(toolCalls []llm.ToolCall, execResult ExecuteToolCallsResult) {
				for i, tc := range toolCalls {
					if i >= len(execResult.Results) {
						break
					}
					ag.Messages = append(ag.Messages, openai.ToolMessage(execResult.Results[i].Result, tc.ID))
				}
			},
			RequestNextResponse: func() (string, []llm.ToolCall, error) {
				messages := injectRuntimeSystemPrompt(ag.Messages, runtimeSystemPrompt)
				resp2, err := requestOpenAICompletionWithRetry(ctx, agentClient, openai.ChatCompletionNewParams{
					Model:       openai.ChatModel(ag.Settings.Model),
					Temperature: openai.Float(ag.Settings.Temperature),
					Messages: append(messages,
						openai.SystemMessage(getFollowUpSystemPrompt()),
					),
					Tools: openaiTools,
				})
				if err != nil {
					return "", nil, err
				}
				if resp2 == nil || len(resp2.Choices) == 0 {
					return "", nil, fmt.Errorf("openai follow-up returned no choices")
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

				next := resp2.Choices[0].Message
				return next.Content, convertOpenAIToolCallsToLLM(next.ToolCalls), nil
			},
		},
	)

	finalText := getResponseText(loopResult.FinalContent)
	if loopResult.HasStructuredResult {
		finalText = loopResult.FinalContent
	}

	ag.Messages = append(ag.Messages, openai.AssistantMessage(finalText))

	logger.Debug("Chat with tool completed", logger.Fields{"duration": time.Since(start)})
	_ = h.persistAgent(agentName, ag.Agent)

	// Store assistant response in session
	h.storeMessageInSession(baseCtx, sessionID, "assistant", finalText)

	response := attachActionReceipts(attachRouteMetadata(map[string]any{
		"response":  finalText,
		"toolCalls": loopResult.ToolCalls,
	}, chatRouteMetadata{
		Mode:      routeModeAssistantChat,
		ToolCount: len(loopResult.ToolCalls),
	}), loopResult.Receipts)

	if loopResult.HasStructuredResult && loopResult.StructuredData != nil {
		response["structured"] = true
		response["displayType"] = string(loopResult.StructuredData.DisplayType)
		response["title"] = loopResult.StructuredData.Title
		response["description"] = loopResult.StructuredData.Description
	}

	orihttp.WriteJSON(w, attachPlannerDecision(response, plannerDecision))
}

func convertOpenAIToolCallsToLLM(toolCalls []openai.ChatCompletionMessageToolCallUnion) []llm.ToolCall {
	result := make([]llm.ToolCall, 0, len(toolCalls))
	for _, tc := range toolCalls {
		result = append(result, llm.ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}
	return result
}

func buildOpenAIAssistantToolCallMessage(content string, toolCalls []llm.ToolCall) openai.ChatCompletionMessageParamUnion {
	trimmed := strings.TrimSpace(content)
	msg := openai.ChatCompletionMessageParamUnion{
		OfAssistant: &openai.ChatCompletionAssistantMessageParam{},
	}
	if trimmed != "" {
		msg = openai.AssistantMessage(content)
		if msg.OfAssistant == nil {
			msg.OfAssistant = &openai.ChatCompletionAssistantMessageParam{}
		}
	}
	msg.OfAssistant.ToolCalls = convertLLMToolCallsToOpenAI(toolCalls)
	return msg
}

func convertLLMToolCallsToOpenAI(toolCalls []llm.ToolCall) []openai.ChatCompletionMessageToolCallUnionParam {
	result := make([]openai.ChatCompletionMessageToolCallUnionParam, 0, len(toolCalls))
	for _, tc := range toolCalls {
		result = append(result, openai.ChatCompletionMessageToolCallUnionParam{
			OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
				ID: tc.ID,
				Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
					Name:      tc.Name,
					Arguments: tc.Arguments,
				},
			},
		})
	}
	return result
}

func injectRuntimeSystemPrompt(
	messages []openai.ChatCompletionMessageParamUnion,
	runtimeSystemPrompt string,
) []openai.ChatCompletionMessageParamUnion {
	runtime := strings.TrimSpace(runtimeSystemPrompt)
	if runtime == "" {
		return messages
	}

	runtimeMessage := openai.SystemMessage(runtime)
	if len(messages) == 0 {
		return []openai.ChatCompletionMessageParamUnion{runtimeMessage}
	}

	lastIdx := len(messages) - 1
	lastMessage := messages[lastIdx]
	if lastMessage.OfUser != nil {
		withRuntime := make([]openai.ChatCompletionMessageParamUnion, 0, len(messages)+1)
		withRuntime = append(withRuntime, messages[:lastIdx]...)
		withRuntime = append(withRuntime, runtimeMessage, lastMessage)
		return withRuntime
	}

	withRuntime := make([]openai.ChatCompletionMessageParamUnion, 0, len(messages)+1)
	withRuntime = append(withRuntime, messages...)
	withRuntime = append(withRuntime, runtimeMessage)
	return withRuntime
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
