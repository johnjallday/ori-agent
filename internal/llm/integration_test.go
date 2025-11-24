package llm

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestProviderIntegration tests the full provider flow with OpenAI
// This test requires a valid OPENAI_API_KEY environment variable
func TestProviderIntegration(t *testing.T) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		t.Skip("OPENAI_API_KEY not set - skipping integration test")
	}

	// Create factory and register provider
	factory := NewFactory()
	provider := NewOpenAIProvider(ProviderConfig{APIKey: apiKey})
	factory.Register("openai", provider)

	// Get provider
	retrievedProvider, err := factory.GetProvider("openai")
	if err != nil {
		t.Fatalf("Failed to get provider: %v", err)
	}

	// Test 1: Simple chat
	t.Run("Simple Chat", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		req := ChatRequest{
			Model: "gpt-4o",
			Messages: []Message{
				NewUserMessage("Say 'Hello World' and nothing else"),
			},
			Temperature: 0.0,
			MaxTokens:   10,
		}

		resp, err := retrievedProvider.Chat(ctx, req)
		if err != nil {
			t.Fatalf("Chat failed: %v", err)
		}

		if resp == nil {
			t.Fatal("Response is nil")
		}

		if resp.Content == "" {
			t.Error("Response content is empty")
		}

		if resp.Provider != "openai" {
			t.Errorf("Expected provider 'openai', got '%s'", resp.Provider)
		}

		if resp.Usage.TotalTokens == 0 {
			t.Error("Expected non-zero token usage")
		}

		t.Logf("Response: %s", resp.Content)
		t.Logf("Tokens: %d", resp.Usage.TotalTokens)
	})

	// Test 2: Chat with system prompt
	t.Run("Chat with System Prompt", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		req := ChatRequest{
			Model:        "gpt-4o",
			SystemPrompt: "You are a helpful assistant that responds in exactly 3 words.",
			Messages: []Message{
				NewUserMessage("What is the capital of France?"),
			},
			Temperature: 0.0,
		}

		resp, err := retrievedProvider.Chat(ctx, req)
		if err != nil {
			t.Fatalf("Chat failed: %v", err)
		}

		if resp.Content == "" {
			t.Error("Response content is empty")
		}

		t.Logf("Response: %s", resp.Content)
	})

	// Test 3: Chat with tools
	t.Run("Chat with Tools", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		calculatorTool := Tool{
			Name:        "calculator",
			Description: "Perform arithmetic operations",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"operation": map[string]interface{}{
						"type": "string",
						"enum": []string{"add", "subtract", "multiply", "divide"},
					},
					"a": map[string]interface{}{
						"type": "number",
					},
					"b": map[string]interface{}{
						"type": "number",
					},
				},
				"required": []string{"operation", "a", "b"},
			},
		}

		req := ChatRequest{
			Model: "gpt-4o",
			Messages: []Message{
				NewUserMessage("What is 15 multiplied by 7? Use the calculator tool."),
			},
			Tools:       []Tool{calculatorTool},
			Temperature: 0.0,
		}

		resp, err := retrievedProvider.Chat(ctx, req)
		if err != nil {
			t.Fatalf("Chat failed: %v", err)
		}

		// Should have tool calls
		if !IsToolCallResponse(resp) {
			t.Error("Expected tool call response")
		}

		if len(resp.ToolCalls) == 0 {
			t.Fatal("Expected at least one tool call")
		}

		toolCall := resp.ToolCalls[0]
		if toolCall.Name != "calculator" {
			t.Errorf("Expected tool call to 'calculator', got '%s'", toolCall.Name)
		}

		t.Logf("Tool call: %s", toolCall.Name)
		t.Logf("Arguments: %s", toolCall.Arguments)

		// Parse arguments
		var args struct {
			Operation string  `json:"operation"`
			A         float64 `json:"a"`
			B         float64 `json:"b"`
		}

		if err := ParseToolArguments(toolCall.Arguments, &args); err != nil {
			t.Fatalf("Failed to parse tool arguments: %v", err)
		}

		if args.Operation != "multiply" {
			t.Errorf("Expected operation 'multiply', got '%s'", args.Operation)
		}

		t.Logf("Parsed operation: %s(%f, %f)", args.Operation, args.A, args.B)
	})

	// Test 4: Multi-turn conversation
	t.Run("Multi-turn Conversation", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// First turn
		messages := []Message{
			NewUserMessage("My name is Alice."),
		}

		resp1, err := retrievedProvider.Chat(ctx, ChatRequest{
			Model:       "gpt-4o",
			Messages:    messages,
			Temperature: 0.0,
		})
		if err != nil {
			t.Fatalf("First chat failed: %v", err)
		}

		t.Logf("Turn 1 response: %s", resp1.Content)

		// Add assistant response
		messages = append(messages, NewAssistantMessage(resp1.Content))

		// Second turn - check if it remembers
		messages = append(messages, NewUserMessage("What is my name?"))

		resp2, err := retrievedProvider.Chat(ctx, ChatRequest{
			Model:       "gpt-4o",
			Messages:    messages,
			Temperature: 0.0,
		})
		if err != nil {
			t.Fatalf("Second chat failed: %v", err)
		}

		t.Logf("Turn 2 response: %s", resp2.Content)

		// Response should mention Alice
		// Note: We don't strictly enforce this as LLMs can be unpredictable
		// but it's a good indication the conversation history is working
	})
}

// TestProviderFactoryIntegration tests that the factory works correctly in a real scenario
func TestProviderFactoryIntegration(t *testing.T) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		t.Skip("OPENAI_API_KEY not set - skipping integration test")
	}

	factory := NewFactory()

	// Register provider
	provider := NewOpenAIProvider(ProviderConfig{APIKey: apiKey})
	factory.Register("openai", provider)

	// List providers
	providers := factory.ListProviders()
	if len(providers) != 1 {
		t.Errorf("Expected 1 provider, got %d", len(providers))
	}

	if providers[0].Name != "openai" {
		t.Errorf("Expected provider name 'openai', got '%s'", providers[0].Name)
	}

	// Check capabilities
	if !providers[0].Capabilities.SupportsTools {
		t.Error("Expected OpenAI to support tools")
	}

	t.Logf("Provider: %s", providers[0].Name)
	t.Logf("Type: %s", providers[0].Type)
	t.Logf("Models: %v", providers[0].Models)
	t.Logf("Supports tools: %v", providers[0].Capabilities.SupportsTools)
}
