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

// handleLocalProviderToolCalls handles tool execution for local providers
// via the shared provider tool loop.
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
	h.runProviderToolLoop(w, ctx, baseCtx, providerToolLoopRun{
		Agent:                ag,
		AgentName:            agentName,
		Messages:             messages,
		InitialResponse:      resp,
		Tools:                tools,
		Files:                files,
		Provider:             provider,
		SessionID:            sessionID,
		UserMessage:          userMessage,
		PlannerDecision:      plannerDecision,
		ProviderLabel:        providerName,
		FollowUpSystemPrompt: systemPrompt,
		FinalSystemPrompt:    systemPrompt + "\n\n" + getFinalToolLoopSynthesisPrompt(),
	})
}
