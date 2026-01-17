package chathttp

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/openai/openai-go/v3"

	"github.com/johnjallday/ori-agent/internal/agent"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/pluginapi"
)

// handleClaudeChat handles chat requests for Claude models using the provider system
func (h *Handler) handleClaudeChat(w http.ResponseWriter, r *http.Request, ag *agent.Agent, userMessage string, tools []llm.Tool, agentName string, baseCtx context.Context, files []pluginapi.FileAttachment, images []llm.ImageAttachment, plannerDecision *types.PlannerDecision) {
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
	var messages []llm.Message
	if ag.Settings.SystemPrompt != "" {
		messages = append(messages, llm.NewSystemMessage(ag.Settings.SystemPrompt))
	}

	// Use message with images if images are present
	if len(images) > 0 {
		messages = append(messages, llm.NewUserMessageWithImages(userMessage, images))
		logger.Info("Claude chat with images", logger.Fields{"image_count": len(images)})
	} else {
		messages = append(messages, llm.NewUserMessage(userMessage))
	}

	// Call Claude
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

	// Track usage and cost
	h.trackUsageCommon("claude", ag.Settings.Model, agentName, resp.Usage, ag)

	// Tool-call branch
	if len(resp.ToolCalls) > 0 {
		h.handleClaudeToolCalls(w, ctx, ag, agentName, messages, resp, tools, files, provider, baseCtx, start, sessionID, plannerDecision)
		return
	}

	// Plain answer path (no tool calls)
	text := getResponseText(resp.Content)
	ag.Messages = append(ag.Messages, openai.AssistantMessage(text))

	logger.Debug("Claude chat response completed", logger.Fields{"duration": time.Since(start)})
	_ = h.store.SetAgent(agentName, ag)

	// Store assistant response in session
	h.storeMessageInSession(baseCtx, sessionID, "assistant", text)

	writeJSONResponse(w, attachPlannerDecision(map[string]any{"response": text}, plannerDecision))
}

// handleClaudeToolCalls handles tool execution for Claude
func (h *Handler) handleClaudeToolCalls(
	w http.ResponseWriter,
	ctx context.Context,
	ag *agent.Agent,
	agentName string,
	messages []llm.Message,
	resp *llm.ChatResponse,
	tools []llm.Tool,
	files []pluginapi.FileAttachment,
	provider llm.Provider,
	baseCtx context.Context,
	start time.Time,
	sessionID string,
	plannerDecision *types.PlannerDecision,
) {
	logger.Info("Claude requested tool calls", logger.Fields{"count": len(resp.ToolCalls)})

	// Add assistant message to history
	messages = append(messages, llm.NewAssistantMessage(resp.Content))
	ag.Messages = append(ag.Messages, openai.AssistantMessage(resp.Content))

	// Execute tool calls using common helper with session tracking
	execResult := h.executeToolCallsCommonWithSession(baseCtx, ag, agentName, resp.ToolCalls, files, sessionID)

	// Add tool results to messages
	for i, tc := range resp.ToolCalls {
		messages = append(messages, llm.NewToolMessage(tc.ID, execResult.Results[i].Result))
		ag.Messages = append(ag.Messages, openai.ToolMessage(execResult.Results[i].Result, tc.ID))
	}

	// If we have structured results, return them directly
	if execResult.HasStructuredResult {
		ag.Messages = append(ag.Messages, openai.AssistantMessage(execResult.CombinedResult))
		logger.Debug("Claude chat with structured tool result completed", logger.Fields{"duration": time.Since(start)})
		_ = h.store.SetAgent(agentName, ag)

		// Store assistant response in session
		h.storeMessageInSession(baseCtx, sessionID, "assistant", execResult.CombinedResult)

		response := map[string]any{
			"response":  execResult.CombinedResult,
			"toolCalls": execResult.Results,
		}
		writeJSONResponse(w, attachPlannerDecision(response, plannerDecision))
		return
	}

	// Ask Claude again with tool results
	resp2, err := provider.Chat(ctx, llm.ChatRequest{
		Model:       ag.Settings.Model,
		Messages:    append(messages, llm.NewSystemMessage(getFollowUpSystemPrompt())),
		Tools:       tools,
		Temperature: ag.Settings.Temperature,
		MaxTokens:   4000,
	})

	if err != nil || resp2 == nil {
		// If second turn fails, return the tool results as best-effort reply
		orihttp.WriteJSON(w, attachPlannerDecision(map[string]any{
			"response":  execResult.CombinedResult,
			"toolCalls": execResult.Results,
		}, plannerDecision))
		return
	}

	// Track usage for second call
	h.trackUsageCommon("claude", ag.Settings.Model, agentName, resp2.Usage, ag)

	// Store final response
	ag.Messages = append(ag.Messages, openai.AssistantMessage(resp2.Content))

	logger.Debug("Claude chat with tool completed", logger.Fields{"duration": time.Since(start)})
	_ = h.store.SetAgent(agentName, ag)

	// Store assistant response in session
	h.storeMessageInSession(baseCtx, sessionID, "assistant", resp2.Content)

	orihttp.WriteJSON(w, attachPlannerDecision(map[string]any{
		"response":  resp2.Content,
		"toolCalls": execResult.Results,
	}, plannerDecision))
}
