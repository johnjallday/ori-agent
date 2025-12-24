package agentstudio

import (
	"context"
	"fmt"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/openai/openai-go/v3"
)

// LLMFactoryAdapter adapts llm.Factory to implement LLMProvider interface
type LLMFactoryAdapter struct {
	factory      *llm.Factory
	providerName string
	model        string
}

// NewLLMFactoryAdapter creates an adapter for llm.Factory
func NewLLMFactoryAdapter(factory *llm.Factory, providerName string) *LLMFactoryAdapter {
	return &LLMFactoryAdapter{
		factory:      factory,
		providerName: providerName,
		model:        "gpt-4o", // Default model
	}
}

// ChatCompletion implements the LLMProvider interface (legacy, for mission analysis)
func (a *LLMFactoryAdapter) ChatCompletion(ctx context.Context, messages []openai.ChatCompletionMessageParamUnion, tools []openai.ChatCompletionToolUnionParam) (*openai.ChatCompletion, error) {
	// Get the provider from the factory
	provider, err := a.factory.GetProvider(a.providerName)
	if err != nil {
		return nil, fmt.Errorf("failed to get provider %s: %w", a.providerName, err)
	}

	// Convert openai messages to llm.Message
	// Since we're just analyzing missions (text), we'll serialize the messages
	llmMessages := make([]llm.Message, 0, len(messages))

	// Iterate through message params and extract content
	for _, msgParam := range messages {
		// Use reflection or just convert to string
		// For simplicity, we'll extract the basic content
		msgStr := fmt.Sprintf("%v", msgParam)

		// Determine role from the message type
		var role string
		if len(msgStr) > 0 {
			// Very simplified - just check for common patterns
			if len(llmMessages) == 0 {
				role = "system"
			} else if len(llmMessages)%2 == 1 {
				role = "user"
			} else {
				role = "assistant"
			}
		}

		llmMessages = append(llmMessages, llm.Message{
			Role:    role,
			Content: msgStr,
		})
	}

	// Create ChatRequest
	chatReq := llm.ChatRequest{
		Model:    a.model,
		Messages: llmMessages,
		Tools:    []llm.Tool{}, // Empty tools for mission analysis
	}

	// Call provider
	resp, err := provider.Chat(ctx, chatReq)
	if err != nil {
		return nil, err
	}

	// Convert llm.ChatResponse back to openai.ChatCompletion
	completion := &openai.ChatCompletion{
		ID:    "chatcmpl-" + fmt.Sprintf("%d", ctx.Value("request_id")),
		Model: resp.Model,
		Choices: []openai.ChatCompletionChoice{
			{
				Message: openai.ChatCompletionMessage{
					Role:    "assistant",
					Content: resp.Content,
				},
				FinishReason: "stop",
				Index:        0,
			},
		},
		Usage: openai.CompletionUsage{
			PromptTokens:     int64(resp.Usage.PromptTokens),
			CompletionTokens: int64(resp.Usage.CompletionTokens),
			TotalTokens:      int64(resp.Usage.TotalTokens),
		},
	}

	return completion, nil
}

// ChatWithTools implements tool-calling support for task execution
func (a *LLMFactoryAdapter) ChatWithTools(ctx context.Context, systemPrompt, userPrompt string, tools []llm.Tool) (*llm.ChatResponse, error) {
	// Build messages
	messages := []llm.Message{
		{Role: "system", Content: systemPrompt},
	}

	if userPrompt != "" {
		messages = append(messages, llm.Message{Role: "user", Content: userPrompt})
	}

	return a.ChatWithMessages(ctx, messages, tools)
}

// ChatWithMessages implements tool-calling support with full message history
func (a *LLMFactoryAdapter) ChatWithMessages(ctx context.Context, messages []llm.Message, tools []llm.Tool) (*llm.ChatResponse, error) {
	// Get the provider from the factory
	provider, err := a.factory.GetProvider(a.providerName)
	if err != nil {
		return nil, fmt.Errorf("failed to get provider %s: %w", a.providerName, err)
	}

	logger.Debug("[LLMAdapter] ChatWithMessages", logger.Fields{
		"provider":   a.providerName,
		"model":      a.model,
		"tool_count": len(tools),
		"msg_count":  len(messages),
	})

	// Create ChatRequest with tools
	chatReq := llm.ChatRequest{
		Model:    a.model,
		Messages: messages,
		Tools:    tools,
	}

	// Call provider
	resp, err := provider.Chat(ctx, chatReq)
	if err != nil {
		return nil, fmt.Errorf("provider chat failed: %w", err)
	}

	logger.Debug("[LLMAdapter] ChatWithMessages response", logger.Fields{
		"content_length": len(resp.Content),
		"tool_calls":     len(resp.ToolCalls),
	})

	return resp, nil
}
