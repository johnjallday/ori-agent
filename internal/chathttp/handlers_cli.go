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

// cliChatProviderNames lists the CLI providers whose workspace chat runs
// through handleCLIChat, with the name their errors use.
var cliChatProviderNames = map[string]string{
	"codex":       "Codex",
	"claude_code": "Claude Code",
}

func isCLIChatProvider(providerName string) bool {
	_, ok := cliChatProviderNames[providerName]
	return ok
}

// handleCLIChat handles chat requests routed through a CLI provider (Codex or
// Claude Code). The provider brokers Ori's tools: a tool call comes back as
// one ToolCall, which the shared provider tool loop executes and feeds into
// the next CLI round. providerName is also the usage and loop label.
func (h *Handler) handleCLIChat(w http.ResponseWriter, r *http.Request, providerName string, ag *resolvedChatAgent, userMessage string, tools []llm.Tool, agentName string, baseCtx context.Context, files []toolapi.FileAttachment, images []llm.ImageAttachment, plannerDecision *types.PlannerDecision, runtimeSystemPrompt string, routeCtx normalizedChatRouteContext) {
	sessionID := h.getSessionID(r)
	ctx, cancel := context.WithTimeout(baseCtx, ChatRequestTimeout)
	defer cancel()

	displayName := cliChatProviderNames[providerName]
	if h.llmFactory == nil {
		writeErrorResponse(w, displayName+" provider not available")
		return
	}

	provider, err := h.llmFactory.GetProvider(providerName)
	if err != nil {
		writeErrorResponse(w, fmt.Sprintf("%s provider not available: %v", displayName, err))
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
	reasoningEffort := ag.Settings.EffectiveReasoningEffort(providerName)

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

	h.trackUsageCommon(providerName, ag.Settings.Model, agentName, resp.Usage, ag.Agent, userMessage)

	if len(resp.ToolCalls) > 0 {
		logger.Info("CLI provider requested tool calls", logger.Fields{"provider": providerName, "count": len(resp.ToolCalls)})
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
			FollowUpSystemPrompt: systemPrompt + "\n\n" + getFollowUpSystemPrompt(),
			FinalSystemPrompt:    systemPrompt + "\n\n" + getFinalToolLoopSynthesisPrompt(),
			ReasoningEffort:      reasoningEffort,
		})
		return
	}

	text := getResponseText(resp.Content)

	logger.Debug("CLI chat response completed", logger.Fields{"provider": providerName, "duration": time.Since(start)})
	_ = h.persistAgent(agentName, ag)

	// The router already stored the user's message before dispatch.
	h.storeMessageInSession(baseCtx, sessionID, "assistant", text)

	payload := attachRouteMetadata(map[string]any{
		"response": text,
	}, chatRouteMetadata{
		Mode: routeModeAssistantChat,
	})
	writeJSONResponse(w, attachPlannerDecision(attachDependencyResolution(payload, inferDependencyResolutionFromText(text, routeCtx, providerName)), plannerDecision))
}
