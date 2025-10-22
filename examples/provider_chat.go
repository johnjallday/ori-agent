// Example: Using the LLM Provider abstraction for chat
//
// This example demonstrates how to use the provider abstraction layer
// to make chat requests. The same code works with any provider (OpenAI, Claude, Ollama).
//
// Run with: go run examples/provider_chat.go

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/johnjallday/dolphin-agent/internal/llm"
)

func main() {
	// Create LLM factory
	factory := llm.NewFactory()

	// Register OpenAI provider if available
	openaiKey := os.Getenv("OPENAI_API_KEY")
	if openaiKey != "" {
		openaiProvider := llm.NewOpenAIProvider(llm.ProviderConfig{
			APIKey: openaiKey,
		})
		factory.Register("openai", openaiProvider)
		fmt.Println("✓ OpenAI provider registered")
	}

	// Register Claude provider if available
	claudeKey := os.Getenv("ANTHROPIC_API_KEY")
	if claudeKey != "" {
		claudeProvider := llm.NewClaudeProvider(llm.ProviderConfig{
			APIKey: claudeKey,
		})
		factory.Register("claude", claudeProvider)
		fmt.Println("✓ Claude provider registered")
	}

	if factory.ProviderCount() == 0 {
		log.Fatal("No providers available. Set OPENAI_API_KEY or ANTHROPIC_API_KEY environment variable.")
	}

	fmt.Println()

	// List all available providers
	providers := factory.ListProviders()
	fmt.Println("Available providers:")
	for _, p := range providers {
		fmt.Printf("  - %s (%s)\n", p.Name, p.Type)
		fmt.Printf("    Models: %v\n", p.Models)
	}
	fmt.Println()

	// Run examples with each provider
	for _, providerInfo := range providers {
		provider, err := factory.GetProvider(providerInfo.Name)
		if err != nil {
			log.Printf("Failed to get provider %s: %v", providerInfo.Name, err)
			continue
		}

		fmt.Printf("\n========================================\n")
		fmt.Printf("Testing with %s provider\n", provider.Name())
		fmt.Printf("========================================\n\n")

		// Example 1: Simple chat
		fmt.Println("=== Example 1: Simple Chat ===")
		simpleChat(provider)
		fmt.Println()

		// Example 2: Chat with tool calling
		fmt.Println("=== Example 2: Chat with Tools ===")
		chatWithTools(provider)
		fmt.Println()

		// Example 3: Multi-turn conversation
		fmt.Println("=== Example 3: Multi-turn Conversation ===")
		multiTurnChat(provider)
	}
}

func simpleChat(provider llm.Provider) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Choose appropriate model based on provider
	model := provider.DefaultModels()[0] // Use first default model

	req := llm.ChatRequest{
		Model: model,
		Messages: []llm.Message{
			llm.NewUserMessage("What is 2+2? Answer in one word."),
		},
		Temperature: 0.7,
		MaxTokens:   100,
	}

	resp, err := provider.Chat(ctx, req)
	if err != nil {
		log.Printf("Chat failed: %v", err)
		return
	}

	fmt.Printf("User: What is 2+2?\n")
	fmt.Printf("Assistant: %s\n", resp.Content)
	fmt.Printf("Model: %s\n", resp.Model)
	fmt.Printf("Tokens used: %d\n", resp.Usage.TotalTokens)
}

func chatWithTools(provider llm.Provider) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	model := provider.DefaultModels()[0]

	// Define a simple calculator tool
	calculatorTool := llm.Tool{
		Name:        "calculator",
		Description: "Perform basic arithmetic operations",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"operation": map[string]interface{}{
					"type": "string",
					"enum": []string{"add", "subtract", "multiply", "divide"},
				},
				"a": map[string]interface{}{
					"type":        "number",
					"description": "First number",
				},
				"b": map[string]interface{}{
					"type":        "number",
					"description": "Second number",
				},
			},
			"required": []string{"operation", "a", "b"},
		},
	}

	req := llm.ChatRequest{
		Model:        model,
		SystemPrompt: "You are a helpful assistant with access to a calculator tool.",
		Messages: []llm.Message{
			llm.NewUserMessage("What is 15 multiplied by 7?"),
		},
		Tools:       []llm.Tool{calculatorTool},
		Temperature: 0.0,
		MaxTokens:   1000,
	}

	resp, err := provider.Chat(ctx, req)
	if err != nil {
		log.Printf("Chat failed: %v", err)
		return
	}

	fmt.Printf("User: What is 15 multiplied by 7?\n")

	if llm.IsToolCallResponse(resp) {
		fmt.Printf("Assistant wants to call tool: %s\n", resp.ToolCalls[0].Name)
		fmt.Printf("Tool arguments: %s\n", resp.ToolCalls[0].Arguments)

		// In a real application, you would:
		// 1. Execute the tool with the arguments
		// 2. Add the tool result to messages
		// 3. Call the provider again to get the final response
		fmt.Println("(Tool would be executed here)")
	} else {
		fmt.Printf("Assistant: %s\n", resp.Content)
	}
}

func multiTurnChat(provider llm.Provider) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	model := provider.DefaultModels()[0]

	// Start conversation
	messages := []llm.Message{
		llm.NewUserMessage("Hi, what's your name?"),
	}

	// First turn
	resp1, err := provider.Chat(ctx, llm.ChatRequest{
		Model:        model,
		SystemPrompt: "You are a helpful assistant. Be concise.",
		Messages:     messages,
		Temperature:  0.7,
		MaxTokens:    100,
	})
	if err != nil {
		log.Printf("Chat failed: %v", err)
		return
	}

	fmt.Printf("User: Hi, what's your name?\n")
	fmt.Printf("Assistant: %s\n\n", resp1.Content)

	// Add assistant response to conversation
	messages = append(messages, llm.NewAssistantMessage(resp1.Content))

	// Second turn
	messages = append(messages, llm.NewUserMessage("What can you help me with?"))

	resp2, err := provider.Chat(ctx, llm.ChatRequest{
		Model:        model,
		SystemPrompt: "You are a helpful assistant. Be concise.",
		Messages:     messages,
		Temperature:  0.7,
		MaxTokens:    100,
	})
	if err != nil {
		log.Printf("Chat failed: %v", err)
		return
	}

	fmt.Printf("User: What can you help me with?\n")
	fmt.Printf("Assistant: %s\n", resp2.Content)
}
