package chathttp

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"

	"github.com/johnjallday/ori-agent/internal/agent"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/oriagent/ori-pluginapi"
)

// handleGeminiChat handles chat requests for Gemini models using the provider system.
func (h *Handler) handleGeminiChat(w http.ResponseWriter, r *http.Request, ag *agent.Agent, userMessage string, tools []llm.Tool, agentName string, baseCtx context.Context, files []pluginapi.FileAttachment, images []llm.ImageAttachment, plannerDecision *types.PlannerDecision) {
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

	// Build message list
	var messages []llm.Message
	systemPrompt := h.buildSystemPromptWithSkills(
		ag, agentName,
		"You are a helpful assistant with access to tools. When you use a tool and receive results, report those results directly to the user. Be concise and accurate.",
	)
	messages = append(messages, llm.NewSystemMessage(systemPrompt))

	if len(images) > 0 {
		messages = append(messages, llm.NewUserMessageWithImages(userMessage, images))
		logger.Info("Gemini chat with images", logger.Fields{"image_count": len(images)})
	} else {
		messages = append(messages, llm.NewUserMessage(userMessage))
	}

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

	logger.Debug("Gemini response received", logger.Fields{"duration": time.Since(start)})
	h.trackUsageCommon("gemini", ag.Settings.Model, agentName, resp.Usage, ag, userMessage)

	if len(resp.ToolCalls) > 0 {
		h.handleGeminiToolCalls(w, ctx, ag, agentName, messages, resp, tools, files, provider, baseCtx, sessionID, plannerDecision)
		return
	}

	text := getResponseText(resp.Content)
	ag.Messages = append(ag.Messages, openai.AssistantMessage(text))
	_ = h.store.SetAgent(agentName, ag)

	h.storeMessageInSession(baseCtx, sessionID, "assistant", text)
	writeJSONResponse(w, attachPlannerDecision(attachRouteMetadata(map[string]any{
		"response": text,
	}, chatRouteMetadata{
		Mode: routeModeAssistantChat,
	}), plannerDecision))
}

// handleGeminiToolCalls handles tool execution for Gemini.
func (h *Handler) handleGeminiToolCalls(
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
	sessionID string,
	plannerDecision *types.PlannerDecision,
) {
	logger.Info("Gemini requested tool calls", logger.Fields{"count": len(resp.ToolCalls)})

	assistantMsg := llm.NewAssistantMessage(resp.Content)
	assistantMsg.ToolCalls = resp.ToolCalls
	messages = append(messages, assistantMsg)
	ag.Messages = append(ag.Messages, openai.AssistantMessage(resp.Content))

	execResult := h.executeToolCallsCommonWithSession(baseCtx, ag, agentName, resp.ToolCalls, files, sessionID)

	for i, tc := range resp.ToolCalls {
		messages = append(messages, llm.NewToolMessage(tc.ID, execResult.Results[i].Result))
		ag.Messages = append(ag.Messages, openai.ToolMessage(execResult.Results[i].Result, tc.ID))
	}

	finalResp, err := provider.Chat(ctx, llm.ChatRequest{
		Model:       ag.Settings.Model,
		Messages:    messages,
		Tools:       tools,
		Temperature: ag.Settings.Temperature,
		MaxTokens:   4000,
	})
	if err != nil {
		logger.Error("Error getting final response from Gemini", logger.Fields{"error": err})
		writeErrorResponse(w, err.Error())
		return
	}

	finalText := strings.TrimSpace(finalResp.Content)
	if finalText == "" && len(execResult.Results) > 0 {
		var b strings.Builder
		for i, tr := range execResult.Results {
			if i > 0 {
				b.WriteString("\n\n")
			}
			b.WriteString(fmt.Sprintf("**%s**\n\n%s", tr.Function, tr.Result))
		}
		finalText = b.String()
	}

	ag.Messages = append(ag.Messages, openai.AssistantMessage(finalText))
	_ = h.store.SetAgent(agentName, ag)

	h.storeMessageInSession(baseCtx, sessionID, "assistant", finalText)

	orihttp.WriteJSON(w, attachPlannerDecision(attachActionReceipts(attachRouteMetadata(map[string]any{
		"response":  finalText,
		"toolCalls": execResult.Results,
	}, chatRouteMetadata{
		Mode:      routeModeAssistantChat,
		ToolCount: len(execResult.Results),
	}), execResult.Receipts), plannerDecision))
}
