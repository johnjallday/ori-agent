package chathttp

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/openai/openai-go/v3"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/types"
)

// handleClaudeCodeChat handles chat requests routed through the Claude Code CLI provider.
// This provider does not support tool calling, so the flow is a simple request/response.
func (h *Handler) handleClaudeCodeChat(w http.ResponseWriter, r *http.Request, ag *agent.Agent, userMessage string, agentName string, baseCtx context.Context, images []llm.ImageAttachment, plannerDecision *types.PlannerDecision) {
	sessionID := h.getSessionID(r)
	ctx, cancel := context.WithTimeout(baseCtx, ChatRequestTimeout)
	defer cancel()

	provider, err := h.llmFactory.GetProvider("claude_code")
	if err != nil {
		writeErrorResponse(w, fmt.Sprintf("Claude Code provider not available: %v", err))
		return
	}

	var messages []llm.Message
	systemPrompt := h.buildSystemPromptWithSkills(
		ag, agentName,
		"You are a helpful assistant. Be concise and direct in your responses.",
	)

	if len(images) > 0 {
		messages = append(messages, llm.NewUserMessageWithImages(userMessage, images))
	} else {
		messages = append(messages, llm.NewUserMessage(userMessage))
	}

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
	ag.Messages = append(ag.Messages, openai.UserMessage(userMessage))
	ag.Messages = append(ag.Messages, openai.AssistantMessage(text))

	logger.Debug("Claude Code chat response completed", logger.Fields{"duration": time.Since(start)})
	_ = h.store.SetAgent(agentName, ag)

	h.storeMessageInSession(baseCtx, sessionID, "user", userMessage)
	h.storeMessageInSession(baseCtx, sessionID, "assistant", text)

	h.trackUsageCommon("claude_code", ag.Settings.Model, agentName, resp.Usage, ag, userMessage)

	writeJSONResponse(w, attachPlannerDecision(attachRouteMetadata(map[string]any{
		"response": text,
	}, chatRouteMetadata{
		Mode: routeModeAssistantChat,
	}), plannerDecision))
}
