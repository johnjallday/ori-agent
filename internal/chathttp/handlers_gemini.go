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

// handleGeminiChat handles chat requests for Gemini models using the provider system.
func (h *Handler) handleGeminiChat(w http.ResponseWriter, r *http.Request, ag *resolvedChatAgent, userMessage string, tools []llm.Tool, agentName string, baseCtx context.Context, files []toolapi.FileAttachment, images []llm.ImageAttachment, plannerDecision *types.PlannerDecision, runtimeSystemPrompt string) {
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

	systemPrompt := composeRuntimeSystemPrompt(
		h.buildChatSystemPrompt(
			ag, agentName,
			tools,
		),
		runtimeSystemPrompt,
	)
	messages := buildLLMConversationMessages(ag.Messages, userMessage, images)
	if len(images) > 0 {
		logger.Info("Gemini chat with images", logger.Fields{"image_count": len(images)})
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

	logger.Debug("Gemini response received", logger.Fields{"duration": time.Since(start)})
	h.trackUsageCommon("gemini", ag.Settings.Model, agentName, resp.Usage, ag.Agent, userMessage)

	if len(resp.ToolCalls) > 0 {
		h.handleGeminiToolCalls(w, ctx, ag, agentName, messages, resp, tools, files, provider, baseCtx, sessionID, userMessage, plannerDecision, systemPrompt)
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

// handleGeminiToolCalls handles tool execution for Gemini via the shared
// provider tool loop.
func (h *Handler) handleGeminiToolCalls(
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
) {
	logger.Info("Gemini requested tool calls", logger.Fields{"count": len(resp.ToolCalls)})
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
		ProviderLabel:        "gemini",
		FollowUpSystemPrompt: systemPrompt,
		FinalSystemPrompt:    systemPrompt + "\n\n" + getFinalToolLoopSynthesisPrompt(),
	})
}
