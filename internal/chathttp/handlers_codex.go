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

// handleCodexChat handles chat requests routed through the Codex CLI provider.
// The provider brokers Ori's tools: a tool call comes back as one ToolCall,
// which the shared provider tool loop executes and feeds into the next round.
func (h *Handler) handleCodexChat(w http.ResponseWriter, r *http.Request, ag *resolvedChatAgent, userMessage string, tools []llm.Tool, agentName string, baseCtx context.Context, files []toolapi.FileAttachment, images []llm.ImageAttachment, plannerDecision *types.PlannerDecision, runtimeSystemPrompt string, routeCtx normalizedChatRouteContext) {
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
			tools,
		),
		runtimeSystemPrompt,
	)
	messages := buildLLMConversationMessages(ag.Messages, userMessage, images)
	reasoningEffort := ag.Settings.EffectiveReasoningEffort("codex")

	start := time.Now()
	resp, err := provider.Chat(ctx, llm.ChatRequest{
		Model:           ag.Settings.Model,
		Messages:        messages,
		SystemPrompt:    systemPrompt,
		Tools:           tools,
		ReasoningEffort: reasoningEffort,
	})
	if err != nil {
		writeErrorResponse(w, err.Error())
		return
	}

	h.trackUsageCommon("codex", ag.Settings.Model, agentName, resp.Usage, ag.Agent, userMessage)

	if len(resp.ToolCalls) > 0 {
		logger.Info("Codex requested tool calls", logger.Fields{"count": len(resp.ToolCalls)})
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
			ProviderLabel:        "codex",
			FollowUpSystemPrompt: systemPrompt + "\n\n" + getFollowUpSystemPrompt(),
			FinalSystemPrompt:    systemPrompt + "\n\n" + getFinalToolLoopSynthesisPrompt(),
			ReasoningEffort:      reasoningEffort,
		})
		return
	}

	text := getResponseText(resp.Content)

	logger.Debug("Codex chat response completed", logger.Fields{"duration": time.Since(start)})
	_ = h.persistAgent(agentName, ag)

	h.storeMessageInSession(baseCtx, sessionID, "assistant", text)

	payload := attachRouteMetadata(map[string]any{
		"response": text,
	}, chatRouteMetadata{
		Mode: routeModeAssistantChat,
	})
	writeJSONResponse(w, attachPlannerDecision(attachDependencyResolution(payload, inferDependencyResolutionFromText(text, routeCtx, "codex")), plannerDecision))
}
