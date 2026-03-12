package chathttp

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/openai/openai-go/v3"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/oriagent/ori-pluginapi"
)

// handleGeminiChat handles chat requests for Gemini models using the provider system.
func (h *Handler) handleGeminiChat(w http.ResponseWriter, r *http.Request, ag *resolvedChatAgent, userMessage string, tools []llm.Tool, agentName string, baseCtx context.Context, files []pluginapi.FileAttachment, images []llm.ImageAttachment, plannerDecision *types.PlannerDecision, runtimeSystemPrompt string) {
	sessionID := h.getSessionID(r)
	ctx, cancel := context.WithTimeout(baseCtx, ChatRequestTimeout)
	defer cancel()

	if h.llmFactory == nil {
		writeErrorResponse(w, "Gemini provider not available")
		return
	}

	provider, err := h.llmFactory.GetProvider("gemini")
	if err != nil {
		writeErrorResponse(w, fmt.Sprintf("Gemini provider not available: %v", err))
		return
	}

	// Build message list
	var messages []llm.Message
	systemPrompt := composeRuntimeSystemPrompt(
		h.buildSystemPromptWithSkills(
			ag.Agent, agentName,
			"You are a helpful assistant with access to tools. When you use a tool and receive results, report those results directly to the user. Be concise and accurate.",
		),
		runtimeSystemPrompt,
	)
	messages = append(messages, llm.NewSystemMessage(systemPrompt))

	if len(images) > 0 {
		messages = append(messages, llm.NewUserMessageWithImages(userMessage, images))
		logger.Info("Gemini chat with images", logger.Fields{"image_count": len(images)})
	} else {
		messages = append(messages, llm.NewUserMessage(userMessage))
	}

	start := time.Now()
	resp, err := provider.Chat(ctx, llm.ChatRequest{
		Model:       ag.Settings.Model,
		Messages:    messages,
		Tools:       tools,
		Temperature: ag.Settings.Temperature,
		MaxTokens:   4000,
	})
	if err != nil {
		writeErrorResponse(w, err.Error())
		return
	}

	logger.Debug("Gemini response received", logger.Fields{"duration": time.Since(start)})
	h.trackUsageCommon("gemini", ag.Settings.Model, agentName, resp.Usage, ag.Agent, userMessage)

	if len(resp.ToolCalls) > 0 {
		h.handleGeminiToolCalls(w, ctx, ag, agentName, messages, resp, tools, files, provider, baseCtx, sessionID, userMessage, plannerDecision)
		return
	}

	text := getResponseText(resp.Content)
	ag.Messages = append(ag.Messages, openai.AssistantMessage(text))
	_ = h.persistAgent(agentName, ag.Agent)

	h.storeMessageInSession(baseCtx, sessionID, "assistant", text)
	writeJSONResponse(w, attachPlannerDecision(attachRouteMetadata(map[string]any{
		"response": text,
	}, chatRouteMetadata{
		Mode: routeModeAssistantChat,
	}), plannerDecision))
}

// handleGeminiToolCalls handles tool execution for Gemini.
func (h *Handler) handleGeminiToolCalls(
	w http.ResponseWriter,
	ctx context.Context,
	ag *resolvedChatAgent,
	agentName string,
	messages []llm.Message,
	resp *llm.ChatResponse,
	tools []llm.Tool,
	files []pluginapi.FileAttachment,
	provider llm.Provider,
	baseCtx context.Context,
	sessionID string,
	userMessage string,
	plannerDecision *types.PlannerDecision,
) {
	logger.Info("Gemini requested tool calls", logger.Fields{"count": len(resp.ToolCalls)})

	loopResult := h.runBoundedToolLoop(
		resp.Content,
		resp.ToolCalls,
		boundedToolLoopConfig{},
		boundedToolLoopCallbacks{
			AppendAssistantTurn: func(content string, toolCalls []llm.ToolCall) {
				assistantMsg := llm.NewAssistantMessage(content)
				assistantMsg.ToolCalls = toolCalls
				messages = append(messages, assistantMsg)
				ag.Messages = append(ag.Messages, openai.AssistantMessage(content))
			},
			ExecuteToolCalls: func(toolCalls []llm.ToolCall) ExecuteToolCallsResult {
				return h.executeToolCallsCommonWithSession(baseCtx, ag, agentName, toolCalls, files, sessionID)
			},
			AppendToolResults: func(toolCalls []llm.ToolCall, execResult ExecuteToolCallsResult) {
				for i, tc := range toolCalls {
					if i >= len(execResult.Results) {
						break
					}
					messages = append(messages, llm.NewToolMessage(tc.ID, execResult.Results[i].Result))
					ag.Messages = append(ag.Messages, openai.ToolMessage(execResult.Results[i].Result, tc.ID))
				}
			},
			RequestNextResponse: func() (string, []llm.ToolCall, error) {
				finalResp, err := provider.Chat(ctx, llm.ChatRequest{
					Model:       ag.Settings.Model,
					Messages:    messages,
					Tools:       tools,
					Temperature: ag.Settings.Temperature,
					MaxTokens:   4000,
				})
				if err != nil {
					return "", nil, err
				}
				if finalResp == nil {
					return "", nil, fmt.Errorf("gemini follow-up returned no response")
				}
				h.trackUsageCommon("gemini", ag.Settings.Model, agentName, finalResp.Usage, ag.Agent, userMessage)
				return finalResp.Content, finalResp.ToolCalls, nil
			},
		},
	)

	finalText := getResponseText(loopResult.FinalContent)
	if loopResult.HasStructuredResult {
		finalText = loopResult.FinalContent
	}
	ag.Messages = append(ag.Messages, openai.AssistantMessage(finalText))
	_ = h.persistAgent(agentName, ag.Agent)

	h.storeMessageInSession(baseCtx, sessionID, "assistant", finalText)

	orihttp.WriteJSON(w, attachPlannerDecision(attachActionReceipts(attachRouteMetadata(map[string]any{
		"response":  finalText,
		"toolCalls": loopResult.ToolCalls,
	}, chatRouteMetadata{
		Mode:      routeModeAssistantChat,
		ToolCount: len(loopResult.ToolCalls),
	}), loopResult.Receipts), plannerDecision))
}
