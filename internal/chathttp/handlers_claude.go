package chathttp

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/toolapi"
	"github.com/johnjallday/ori-agent/internal/types"
)

// handleClaudeChat handles chat requests for Claude models using the provider system
func (h *Handler) handleClaudeChat(w http.ResponseWriter, r *http.Request, ag *resolvedChatAgent, userMessage string, tools []llm.Tool, agentName string, baseCtx context.Context, files []toolapi.FileAttachment, images []llm.ImageAttachment, plannerDecision *types.PlannerDecision, runtimeSystemPrompt string) {
	sessionID := h.getSessionID(r)
	ctx, cancel := context.WithTimeout(baseCtx, ChatRequestTimeout)
	defer cancel()

	// Get Claude provider
	provider, err := h.llmFactory.GetProvider("claude")
	if err != nil {
		writeErrorResponse(w, fmt.Sprintf("Claude provider not available: %v", err))
		return
	}

	// Build message list
	systemPrompt := composeRuntimeSystemPrompt(
		h.buildChatSystemPrompt(
			ag, agentName,
			tools,
		),
		runtimeSystemPrompt,
	)
	messages := buildLLMConversationMessages(ag.Messages, userMessage, images)
	if len(images) > 0 {
		logger.Info("Claude chat with images", logger.Fields{"image_count": len(images)})
	}

	// Call Claude
	start := time.Now()
	resp, err := provider.Chat(ctx, llm.ChatRequest{
		Model:        ag.Settings.Model,
		Messages:     messages,
		SystemPrompt: systemPrompt,
		Tools:        tools,
		Temperature:  ag.Settings.Temperature,
		MaxTokens:    4000,
	})
	if err != nil {
		writeErrorResponse(w, err.Error())
		return
	}

	// Track usage and cost
	h.trackUsageCommon("claude", ag.Settings.Model, agentName, resp.Usage, ag.Agent, userMessage)

	// Tool-call branch
	if len(resp.ToolCalls) > 0 {
		h.handleClaudeToolCalls(w, ctx, ag, agentName, messages, resp, tools, files, provider, baseCtx, start, sessionID, userMessage, plannerDecision, systemPrompt)
		return
	}

	// Plain answer path (no tool calls)
	text := getResponseText(resp.Content)

	logger.Debug("Claude chat response completed", logger.Fields{"duration": time.Since(start)})
	_ = h.persistAgent(agentName, ag.Agent)

	// Store assistant response in session
	h.storeMessageInSession(baseCtx, sessionID, "assistant", text)

	writeJSONResponse(w, attachPlannerDecision(attachRouteMetadata(map[string]any{
		"response": text,
	}, chatRouteMetadata{
		Mode: routeModeAssistantChat,
	}), plannerDecision))
}

// handleClaudeToolCalls handles tool execution for Claude
func (h *Handler) handleClaudeToolCalls(
	w http.ResponseWriter,
	ctx context.Context,
	ag *resolvedChatAgent,
	agentName string,
	messages []llm.Message,
	resp *llm.ChatResponse,
	tools []llm.Tool,
	files []toolapi.FileAttachment,
	provider llm.Provider,
	baseCtx context.Context,
	start time.Time,
	sessionID string,
	userMessage string,
	plannerDecision *types.PlannerDecision,
	systemPrompt string,
) {
	logger.Info("Claude requested tool calls", logger.Fields{"count": len(resp.ToolCalls)})

	loopResult := h.runBoundedToolLoop(
		resp.Content,
		resp.ToolCalls,
		boundedToolLoopConfig{},
		boundedToolLoopCallbacks{
			AppendAssistantTurn: func(content string, toolCalls []llm.ToolCall) {
				assistantMsg := llm.NewAssistantMessage(content)
				assistantMsg.ToolCalls = toolCalls
				messages = append(messages, assistantMsg)
			},
			ExecuteToolCalls: func(toolCalls []llm.ToolCall) ExecuteToolCallsResult {
				return h.executeToolCallsCommonWithSession(baseCtx, ag, toolCalls, files, sessionID)
			},
			AppendToolResults: func(toolCalls []llm.ToolCall, execResult ExecuteToolCallsResult) {
				for i, tc := range toolCalls {
					if i >= len(execResult.Results) {
						break
					}
					messages = append(messages, llm.NewToolMessage(tc.ID, execResult.Results[i].Result))
				}
			},
			RequestNextResponse: func() (string, []llm.ToolCall, error) {
				resp2, err := provider.Chat(ctx, llm.ChatRequest{
					Model:        ag.Settings.Model,
					Messages:     messages,
					SystemPrompt: systemPrompt + "\n\n" + getFollowUpSystemPrompt(),
					Tools:        tools,
					Temperature:  ag.Settings.Temperature,
					MaxTokens:    4000,
				})
				if err != nil {
					return "", nil, err
				}
				if resp2 == nil {
					return "", nil, fmt.Errorf("claude follow-up returned no response")
				}
				h.trackUsageCommon("claude", ag.Settings.Model, agentName, resp2.Usage, ag.Agent, userMessage)
				return resp2.Content, resp2.ToolCalls, nil
			},
			RequestFinalResponse: func() (string, error) {
				resp2, err := provider.Chat(ctx, llm.ChatRequest{
					Model:        ag.Settings.Model,
					Messages:     messages,
					SystemPrompt: systemPrompt + "\n\n" + getFinalToolLoopSynthesisPrompt(),
					Temperature:  ag.Settings.Temperature,
					MaxTokens:    4000,
				})
				if err != nil {
					return "", err
				}
				if resp2 == nil {
					return "", fmt.Errorf("claude final synthesis returned no response")
				}
				h.trackUsageCommon("claude", ag.Settings.Model, agentName, resp2.Usage, ag.Agent, userMessage)
				return resp2.Content, nil
			},
		},
	)

	finalText := getResponseText(loopResult.FinalContent)
	if loopResult.HasStructuredResult {
		finalText = loopResult.FinalContent
	}

	logger.Debug("Claude chat with tool completed", logger.Fields{"duration": time.Since(start)})
	_ = h.persistAgent(agentName, ag.Agent)

	// Store assistant response in session
	h.storeMessageInSession(baseCtx, sessionID, "assistant", finalText)

	writeJSONResponse(w, attachPlannerDecision(attachActionReceipts(attachRouteMetadata(map[string]any{
		"response":  finalText,
		"toolCalls": loopResult.ToolCalls,
	}, chatRouteMetadata{
		Mode:      routeModeAssistantChat,
		ToolCount: len(loopResult.ToolCalls),
	}), loopResult.Receipts), plannerDecision))
}
