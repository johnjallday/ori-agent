package chathttp

import (
	"context"
	"fmt"
	"net/http"
	"time"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/toolapi"
	"github.com/johnjallday/ori-agent/internal/types"
)

func localProviderDisplayName(providerName string) string {
	switch providerName {
	case "ollama":
		return "Ollama"
	case "lmstudio":
		return "LM Studio"
	case "mlx_lm":
		return "MLX-LM"
	default:
		return providerName
	}
}

// handleLocalProviderChat handles chat requests for local providers using the shared Provider interface.
func (h *Handler) handleLocalProviderChat(w http.ResponseWriter, r *http.Request, ag *resolvedChatAgent, userMessage string, tools []llm.Tool, agentName string, baseCtx context.Context, files []toolapi.FileAttachment, images []llm.ImageAttachment, plannerDecision *types.PlannerDecision, runtimeSystemPrompt string, providerName string) {
	sessionID := h.getSessionID(r)
	ctx, cancel := context.WithTimeout(baseCtx, ChatRequestTimeout)
	defer cancel()

	providerLabel := localProviderDisplayName(providerName)
	provider, err := h.llmFactory.GetProvider(providerName)
	if err != nil {
		writeErrorResponse(w, fmt.Sprintf("%s provider not available: %v", providerLabel, err))
		return
	}

	systemPrompt := composeRuntimeSystemPrompt(
		h.buildChatSystemPrompt(
			ag, agentName,
			tools,
		),
		runtimeSystemPrompt,
	)
	messages := buildLLMConversationMessages(ag.Messages, userMessage, images)
	if len(images) > 0 {
		logger.Info("Local provider chat with images", logger.Fields{
			"image_count": len(images),
			"provider":    providerName,
		})
	}

	start := time.Now()
	resp, err := provider.Chat(ctx, llm.ChatRequest{
		Model:        ag.Settings.Model,
		Messages:     messages,
		SystemPrompt: systemPrompt,
		Tools:        tools,
		Temperature:  ag.Settings.Temperature,
		MaxTokens:    defaultChatMaxTokens,
	})
	if err != nil {
		writeErrorResponse(w, err.Error())
		return
	}

	logger.Debug("Local provider response received", logger.Fields{
		"duration": time.Since(start),
		"provider": providerName,
	})

	h.trackUsageCommon(providerName, ag.Settings.Model, agentName, resp.Usage, ag.Agent, userMessage)

	if len(resp.ToolCalls) > 0 {
		h.handleLocalProviderToolCalls(w, ctx, ag, agentName, messages, resp, tools, files, provider, baseCtx, sessionID, userMessage, plannerDecision, systemPrompt, providerName)
		return
	}

	text := getResponseText(resp.Content)
	_ = h.persistAgent(agentName, ag.Agent)

	h.storeMessageInSession(baseCtx, sessionID, "assistant", text)

	writeJSONResponse(w, attachPlannerDecision(attachRouteMetadata(map[string]any{
		"response": text,
	}, chatRouteMetadata{
		Mode: routeModeAssistantChat,
	}), plannerDecision))
}

// handleLocalProviderToolCalls handles tool execution for local providers.
func (h *Handler) handleLocalProviderToolCalls(
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
	sessionID string,
	userMessage string,
	plannerDecision *types.PlannerDecision,
	systemPrompt string,
	providerName string,
) {
	logger.Info("Local provider requested tool calls", logger.Fields{
		"count":    len(resp.ToolCalls),
		"provider": providerName,
	})

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
				logger.Debug("Sending tool results back to local provider", logger.Fields{
					"message_count": len(messages),
					"provider":      providerName,
				})
				finalResp, err := provider.Chat(ctx, llm.ChatRequest{
					Model:        ag.Settings.Model,
					Messages:     messages,
					SystemPrompt: systemPrompt,
					Tools:        tools,
					Temperature:  ag.Settings.Temperature,
					MaxTokens:    defaultChatMaxTokens,
				})
				if err != nil {
					return "", nil, err
				}
				if finalResp == nil {
					return "", nil, fmt.Errorf("%s follow-up returned no response", providerName)
				}
				h.trackUsageCommon(providerName, ag.Settings.Model, agentName, finalResp.Usage, ag.Agent, userMessage)
				return finalResp.Content, finalResp.ToolCalls, nil
			},
			RequestFinalResponse: func() (string, error) {
				finalResp, err := provider.Chat(ctx, llm.ChatRequest{
					Model:        ag.Settings.Model,
					Messages:     messages,
					SystemPrompt: systemPrompt + "\n\n" + getFinalToolLoopSynthesisPrompt(),
					Temperature:  ag.Settings.Temperature,
					MaxTokens:    defaultChatMaxTokens,
				})
				if err != nil {
					return "", err
				}
				if finalResp == nil {
					return "", fmt.Errorf("%s final synthesis returned no response", providerName)
				}
				h.trackUsageCommon(providerName, ag.Settings.Model, agentName, finalResp.Usage, ag.Agent, userMessage)
				return finalResp.Content, nil
			},
		},
	)

	finalText := getResponseText(loopResult.FinalContent)
	if loopResult.HasStructuredResult {
		finalText = loopResult.FinalContent
	}

	logger.Debug("Final response from local provider", logger.Fields{
		"content":  finalText,
		"provider": providerName,
	})

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
