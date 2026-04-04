package chathttp

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/types"
)

// handleCodexChat handles chat requests routed through the Codex CLI provider.
// Codex provider currently does not support tool calling, so this path is request/response only.
func (h *Handler) handleCodexChat(w http.ResponseWriter, r *http.Request, ag *resolvedChatAgent, userMessage string, agentName string, baseCtx context.Context, images []llm.ImageAttachment, plannerDecision *types.PlannerDecision, runtimeSystemPrompt string, routeCtx normalizedChatRouteContext) {
	sessionID := h.getSessionID(r)
	ctx, cancel := context.WithTimeout(baseCtx, ChatRequestTimeout)
	defer cancel()

	if h.llmFactory == nil {
		writeErrorResponse(w, "Codex provider not available")
		return
	}

	provider, err := h.llmFactory.GetProvider("codex")
	if err != nil {
		writeErrorResponse(w, fmt.Sprintf("Codex provider not available: %v", err))
		return
	}

	systemPrompt := composeRuntimeSystemPrompt(
		h.buildChatSystemPrompt(
			ag, agentName,
			"You are a helpful assistant. Be concise and direct in your responses.",
			nil,
		),
		runtimeSystemPrompt,
	)
	messages := buildLLMConversationMessages(ag.Messages, userMessage, images)

	start := time.Now()
	resp, err := provider.Chat(ctx, llm.ChatRequest{
		Model:           ag.Settings.Model,
		Messages:        messages,
		SystemPrompt:    systemPrompt,
		ReasoningEffort: ag.Settings.EffectiveReasoningEffort("codex"),
	})
	if err != nil {
		writeErrorResponse(w, err.Error())
		return
	}

	text := getResponseText(resp.Content)

	logger.Debug("Codex chat response completed", logger.Fields{"duration": time.Since(start)})
	_ = h.persistAgent(agentName, ag.Agent)

	h.storeMessageInSession(baseCtx, sessionID, "assistant", text)
	h.trackUsageCommon("codex", ag.Settings.Model, agentName, resp.Usage, ag.Agent, userMessage)

	payload := attachRouteMetadata(map[string]any{
		"response": text,
	}, chatRouteMetadata{
		Mode: routeModeAssistantChat,
	})
	writeJSONResponse(w, attachPlannerDecision(attachDependencyResolution(payload, inferDependencyResolutionFromText(text, routeCtx, "codex")), plannerDecision))
}
