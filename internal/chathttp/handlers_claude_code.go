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

// handleClaudeCodeChat handles chat requests routed through the Claude Code CLI provider.
// This provider does not support tool calling, so the flow is a simple request/response.
func (h *Handler) handleClaudeCodeChat(w http.ResponseWriter, r *http.Request, ag *resolvedChatAgent, userMessage string, agentName string, baseCtx context.Context, images []llm.ImageAttachment, plannerDecision *types.PlannerDecision, runtimeSystemPrompt string, routeCtx normalizedChatRouteContext) {
	sessionID := h.getSessionID(r)
	ctx, cancel := context.WithTimeout(baseCtx, ChatRequestTimeout)
	defer cancel()

	provider, err := h.llmFactory.GetProvider("claude_code")
	if err != nil {
		writeErrorResponse(w, fmt.Sprintf("Claude Code provider not available: %v", err))
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
		Model:        ag.Settings.Model,
		Messages:     messages,
		SystemPrompt: systemPrompt,
	})
	if err != nil {
		writeErrorResponse(w, err.Error())
		return
	}

	text := getResponseText(resp.Content)

	logger.Debug("Claude Code chat response completed", logger.Fields{"duration": time.Since(start)})
	_ = h.persistAgent(agentName, ag.Agent)

	h.storeMessageInSession(baseCtx, sessionID, "user", userMessage)
	h.storeMessageInSession(baseCtx, sessionID, "assistant", text)

	h.trackUsageCommon("claude_code", ag.Settings.Model, agentName, resp.Usage, ag.Agent, userMessage)

	payload := attachRouteMetadata(map[string]any{
		"response": text,
	}, chatRouteMetadata{
		Mode: routeModeAssistantChat,
	})
	writeJSONResponse(w, attachPlannerDecision(attachDependencyResolution(payload, inferDependencyResolutionFromText(text, routeCtx, "claude_code")), plannerDecision))
}
