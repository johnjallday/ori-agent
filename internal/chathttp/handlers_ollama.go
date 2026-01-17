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
	"github.com/johnjallday/ori-agent/pluginapi"
)

// handleOllamaChat handles chat requests for Ollama models using the provider system
func (h *Handler) handleOllamaChat(w http.ResponseWriter, r *http.Request, ag *agent.Agent, userMessage string, tools []llm.Tool, agentName string, baseCtx context.Context, files []pluginapi.FileAttachment, images []llm.ImageAttachment, plannerDecision *types.PlannerDecision) {
	sessionID := h.getSessionID(r)
	ctx, cancel := context.WithTimeout(baseCtx, ChatRequestTimeout)
	defer cancel()

	// Get Ollama provider
	provider, err := h.llmFactory.GetProvider("ollama")
	if err != nil {
		writeErrorResponse(w, fmt.Sprintf("Ollama provider not available: %v", err))
		return
	}

	// Build message list
	var messages []llm.Message

	// Add system message - use custom if set, otherwise use default
	systemPrompt := ag.Settings.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = "You are a helpful assistant with access to tools. When you use a tool and receive results, report those results directly to the user. Be concise and accurate."
	}
	messages = append(messages, llm.NewSystemMessage(systemPrompt))

	// Use message with images if images are present (for vision models like llava)
	if len(images) > 0 {
		messages = append(messages, llm.NewUserMessageWithImages(userMessage, images))
		logger.Info("Ollama chat with images", logger.Fields{"image_count": len(images)})
	} else {
		messages = append(messages, llm.NewUserMessage(userMessage))
	}

	// Call Ollama
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

	logger.Debug("Ollama response received", logger.Fields{"duration": time.Since(start)})

	// Track statistics (Ollama is free/local, no cost tracking)
	h.trackUsageCommon("ollama", ag.Settings.Model, agentName, resp.Usage, ag)

	// Tool-call branch
	if len(resp.ToolCalls) > 0 {
		h.handleOllamaToolCalls(w, ctx, ag, agentName, messages, resp, tools, files, provider, baseCtx, sessionID, plannerDecision)
		return
	}

	// No tool calls - direct response
	text := getResponseText(resp.Content)
	ag.Messages = append(ag.Messages, openai.AssistantMessage(resp.Content))
	_ = h.store.SetAgent(agentName, ag)

	// Store assistant response in session
	h.storeMessageInSession(baseCtx, sessionID, "assistant", text)

	writeJSONResponse(w, attachPlannerDecision(map[string]any{"response": text}, plannerDecision))
}

// handleOllamaToolCalls handles tool execution for Ollama
func (h *Handler) handleOllamaToolCalls(
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
	logger.Info("Ollama requested tool calls", logger.Fields{"count": len(resp.ToolCalls)})

	// Add assistant message with tool calls
	assistantMsg := llm.NewAssistantMessage(resp.Content)
	assistantMsg.ToolCalls = resp.ToolCalls
	messages = append(messages, assistantMsg)
	ag.Messages = append(ag.Messages, openai.AssistantMessage(resp.Content))

	// Execute tool calls using common helper with session tracking
	execResult := h.executeToolCallsCommonWithSession(baseCtx, ag, agentName, resp.ToolCalls, files, sessionID)

	// Add tool results to messages
	for i, tc := range resp.ToolCalls {
		messages = append(messages, llm.NewToolMessage(tc.ID, execResult.Results[i].Result))
		ag.Messages = append(ag.Messages, openai.ToolMessage(execResult.Results[i].Result, tc.ID))
	}

	logger.Debug("Sending tool results back to LLM", logger.Fields{"message_count": len(messages)})

	// Get final response - include Tools for Ollama protocol
	finalResp, err := provider.Chat(ctx, llm.ChatRequest{
		Model:       ag.Settings.Model,
		Messages:    messages,
		Tools:       tools,
		Temperature: ag.Settings.Temperature,
		MaxTokens:   4000,
	})
	if err != nil {
		logger.Error("Error getting final response from LLM", logger.Fields{"error": err})
		writeErrorResponse(w, err.Error())
		return
	}

	finalText := strings.TrimSpace(finalResp.Content)
	if finalText == "" && len(execResult.Results) > 0 {
		// Build fallback response from tool results
		var b strings.Builder
		for i, tr := range execResult.Results {
			if i > 0 {
				b.WriteString("\n\n")
			}
			b.WriteString(fmt.Sprintf("**%s**\n\n%s", tr.Function, tr.Result))
		}
		finalText = b.String()
	}

	logger.Debug("Final response from LLM", logger.Fields{"content": finalText})

	ag.Messages = append(ag.Messages, openai.AssistantMessage(finalText))
	_ = h.store.SetAgent(agentName, ag)

	// Store assistant response in session
	h.storeMessageInSession(baseCtx, sessionID, "assistant", finalText)

	orihttp.WriteJSON(w, attachPlannerDecision(map[string]any{
		"response":  finalText,
		"toolCalls": execResult.Results,
	}, plannerDecision))
}
