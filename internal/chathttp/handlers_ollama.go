package chathttp

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/openai/openai-go/v3"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/oriagent/ori-pluginapi"
)

// handleOllamaChat handles chat requests for Ollama models using the provider system
func (h *Handler) handleOllamaChat(w http.ResponseWriter, r *http.Request, ag *resolvedChatAgent, userMessage string, tools []llm.Tool, agentName string, baseCtx context.Context, files []pluginapi.FileAttachment, images []llm.ImageAttachment, plannerDecision *types.PlannerDecision, runtimeSystemPrompt string) {
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

	systemPrompt := composeRuntimeSystemPrompt(
		h.buildSystemPromptWithSkills(
			ag, agentName,
			"You are a helpful assistant with access to tools. When you use a tool and receive results, report those results directly to the user. Be concise and accurate.",
		),
		runtimeSystemPrompt,
	)
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
	h.trackUsageCommon("ollama", ag.Settings.Model, agentName, resp.Usage, ag.Agent, userMessage)

	// Tool-call branch
	if len(resp.ToolCalls) > 0 {
		h.handleOllamaToolCalls(w, ctx, ag, agentName, messages, resp, tools, files, provider, baseCtx, sessionID, userMessage, plannerDecision)
		return
	}

	// No tool calls - direct response
	text := getResponseText(resp.Content)
	ag.Messages = append(ag.Messages, openai.AssistantMessage(resp.Content))
	_ = h.persistAgent(agentName, ag.Agent)

	// Store assistant response in session
	h.storeMessageInSession(baseCtx, sessionID, "assistant", text)

	writeJSONResponse(w, attachPlannerDecision(attachRouteMetadata(map[string]any{
		"response": text,
	}, chatRouteMetadata{
		Mode: routeModeAssistantChat,
	}), plannerDecision))
}

// handleOllamaToolCalls handles tool execution for Ollama
func (h *Handler) handleOllamaToolCalls(
	w http.ResponseWriter,
	ctx context.Context,
	ag *resolvedChatAgent,
	agentName string,
	messages []llm.Message,
	resp *llm.ChatResponse,
	tools []llm.Tool,
	files []pluginapi.FileAttachment,
	provider llm.Provider,
	baseCtx context.Context,
	sessionID string,
	userMessage string,
	plannerDecision *types.PlannerDecision,
) {
	logger.Info("Ollama requested tool calls", logger.Fields{"count": len(resp.ToolCalls)})

	loopResult := h.runBoundedToolLoop(
		resp.Content,
		resp.ToolCalls,
		boundedToolLoopConfig{},
		boundedToolLoopCallbacks{
			AppendAssistantTurn: func(content string, toolCalls []llm.ToolCall) {
				assistantMsg := llm.NewAssistantMessage(content)
				assistantMsg.ToolCalls = toolCalls
				messages = append(messages, assistantMsg)
				ag.Messages = append(ag.Messages, openai.AssistantMessage(content))
			},
			ExecuteToolCalls: func(toolCalls []llm.ToolCall) ExecuteToolCallsResult {
				return h.executeToolCallsCommonWithSession(baseCtx, ag, agentName, toolCalls, files, sessionID)
			},
			AppendToolResults: func(toolCalls []llm.ToolCall, execResult ExecuteToolCallsResult) {
				for i, tc := range toolCalls {
					if i >= len(execResult.Results) {
						break
					}
					messages = append(messages, llm.NewToolMessage(tc.ID, execResult.Results[i].Result))
					ag.Messages = append(ag.Messages, openai.ToolMessage(execResult.Results[i].Result, tc.ID))
				}
			},
			RequestNextResponse: func() (string, []llm.ToolCall, error) {
				logger.Debug("Sending tool results back to LLM", logger.Fields{"message_count": len(messages)})
				finalResp, err := provider.Chat(ctx, llm.ChatRequest{
					Model:       ag.Settings.Model,
					Messages:    messages,
					Tools:       tools,
					Temperature: ag.Settings.Temperature,
					MaxTokens:   4000,
				})
				if err != nil {
					return "", nil, err
				}
				if finalResp == nil {
					return "", nil, fmt.Errorf("ollama follow-up returned no response")
				}
				h.trackUsageCommon("ollama", ag.Settings.Model, agentName, finalResp.Usage, ag.Agent, userMessage)
				return finalResp.Content, finalResp.ToolCalls, nil
			},
		},
	)

	finalText := getResponseText(loopResult.FinalContent)
	if loopResult.HasStructuredResult {
		finalText = loopResult.FinalContent
	}

	logger.Debug("Final response from LLM", logger.Fields{"content": finalText})

	ag.Messages = append(ag.Messages, openai.AssistantMessage(finalText))
	_ = h.persistAgent(agentName, ag.Agent)

	// Store assistant response in session
	h.storeMessageInSession(baseCtx, sessionID, "assistant", finalText)

	orihttp.WriteJSON(w, attachPlannerDecision(attachActionReceipts(attachRouteMetadata(map[string]any{
		"response":  finalText,
		"toolCalls": loopResult.ToolCalls,
	}, chatRouteMetadata{
		Mode:      routeModeAssistantChat,
		ToolCount: len(loopResult.ToolCalls),
	}), loopResult.Receipts), plannerDecision))
}
