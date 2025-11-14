package agentstudio

import (
	"context"
	"fmt"

	"github.com/johnjallday/ori-agent/internal/llm"
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

// ChatCompletion implements the LLMProvider interface
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
		Tools:    []llm.Tool{}, // Empty tools for now
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
