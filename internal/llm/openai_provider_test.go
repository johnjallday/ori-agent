package llm

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestOpenAIProviderMetadata(t *testing.T) {
	config := ProviderConfig{
		APIKey: "test-api-key",
	}
	provider := NewOpenAIProvider(config)

	// Test Name
	if provider.Name() != "openai" {
		t.Errorf("Expected name 'openai', got '%s'", provider.Name())
	}

	// Test Type
	if provider.Type() != ProviderTypeCloud {
		t.Errorf("Expected type ProviderTypeCloud, got '%s'", provider.Type())
	}

	// Test Capabilities
	caps := provider.Capabilities()
	if !caps.SupportsTools {
		t.Error("Expected OpenAI to support tools")
	}
	if caps.SupportsStreaming {
		t.Error("OpenAI streaming is unimplemented; capability should be false")
	}
	if !caps.SupportsSystemPrompt {
		t.Error("Expected OpenAI to support system prompts")
	}
	if !caps.SupportsTemperature {
		t.Error("Expected OpenAI to support temperature")
	}
	if !caps.RequiresAPIKey {
		t.Error("Expected OpenAI to require API key")
	}
	if caps.SupportsCustomEndpoint {
		t.Error("Expected OpenAI to not support custom endpoint")
	}
	if caps.MaxContextWindow != 128000 {
		t.Errorf("Expected max context window 128000, got %d", caps.MaxContextWindow)
	}
}

func TestOpenAIProviderDefaultModels(t *testing.T) {
	config := ProviderConfig{
		APIKey: "test-api-key",
	}
	provider := NewOpenAIProvider(config)

	models := provider.DefaultModels()

	// Should return models (either from API or fallback)
	if len(models) == 0 {
		t.Error("Expected at least one model, got none")
	}

	// Check that models are returned (should be fallback models due to invalid key)
	// The fallback models should include at least some GPT models
	hasGPTModel := false
	for _, model := range models {
		if len(model) >= 3 && model[:3] == "gpt" {
			hasGPTModel = true
			break
		}
	}

	if !hasGPTModel {
		t.Error("Expected at least one GPT model in the results")
	}
}

func TestOpenAIProviderValidateConfig(t *testing.T) {
	provider := NewOpenAIProvider(ProviderConfig{})

	tests := []struct {
		name      string
		config    ProviderConfig
		expectErr bool
	}{
		{
			name:      "Valid config with API key",
			config:    ProviderConfig{APIKey: "test-key"},
			expectErr: false,
		},
		{
			name:      "Invalid config without API key",
			config:    ProviderConfig{},
			expectErr: true,
		},
		{
			name:      "Invalid config with empty API key",
			config:    ProviderConfig{APIKey: ""},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := provider.ValidateConfig(tt.config)
			if tt.expectErr && err == nil {
				t.Error("Expected error, got nil")
			}
			if !tt.expectErr && err != nil {
				t.Errorf("Expected no error, got: %v", err)
			}
		})
	}
}

func TestOpenAIProviderConvertMessages(t *testing.T) {
	provider := NewOpenAIProvider(ProviderConfig{APIKey: "test-key"})

	tests := []struct {
		name         string
		messages     []Message
		systemPrompt string
		expectedLen  int
	}{
		{
			name: "User and assistant messages",
			messages: []Message{
				NewUserMessage("Hello"),
				NewAssistantMessage("Hi there"),
			},
			systemPrompt: "",
			expectedLen:  2,
		},
		{
			name: "With system prompt",
			messages: []Message{
				NewUserMessage("Hello"),
			},
			systemPrompt: "You are a helpful assistant",
			expectedLen:  2, // System + user
		},
		{
			name: "All message types",
			messages: []Message{
				NewSystemMessage("System message"),
				NewUserMessage("User message"),
				NewAssistantMessage("Assistant message"),
				NewToolMessage("tool-123", "Tool result"),
			},
			systemPrompt: "",
			expectedLen:  4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			converted := provider.convertMessages(tt.messages, tt.systemPrompt)
			if len(converted) != tt.expectedLen {
				t.Errorf("Expected %d messages, got %d", tt.expectedLen, len(converted))
			}
		})
	}
}

func TestOpenAIProviderConvertTools(t *testing.T) {
	provider := NewOpenAIProvider(ProviderConfig{APIKey: "test-key"})

	tools := []Tool{
		{
			Name:        "calculator",
			Description: "Perform calculations",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"operation": map[string]interface{}{
						"type": "string",
					},
				},
			},
		},
		{
			Name:        "search",
			Description: "Search the web",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type": "string",
					},
				},
			},
		},
	}

	converted := provider.convertTools(tools)

	if len(converted) != len(tools) {
		t.Errorf("Expected %d tools, got %d", len(tools), len(converted))
	}

	// Just verify that tools were converted (the openai.ChatCompletionToolUnionParam is opaque)
	// Detailed verification would require making actual API calls
}

func TestOpenAIProviderUpdateClient(t *testing.T) {
	provider := NewOpenAIProvider(ProviderConfig{APIKey: "initial-key"})

	if provider.apiKey != "initial-key" {
		t.Errorf("Expected initial API key 'initial-key', got '%s'", provider.apiKey)
	}

	// Update with new key
	provider.UpdateClient("new-key")

	if provider.apiKey != "new-key" {
		t.Errorf("Expected updated API key 'new-key', got '%s'", provider.apiKey)
	}

	// Update with empty key should not change
	provider.UpdateClient("")

	if provider.apiKey != "new-key" {
		t.Errorf("Expected API key to remain 'new-key', got '%s'", provider.apiKey)
	}
}

func TestOpenAIProviderStreamChatNotImplemented(t *testing.T) {
	provider := NewOpenAIProvider(ProviderConfig{APIKey: "test-key"})

	req := ChatRequest{
		Model: "gpt-4o",
		Messages: []Message{
			NewUserMessage("Hello"),
		},
	}

	_, err := provider.StreamChat(context.Background(), req)
	if err == nil {
		t.Error("Expected error for unimplemented StreamChat, got nil")
	}

	expectedMsg := "streaming not yet implemented"
	if err != nil && len(err.Error()) > 0 {
		if err.Error()[:len(expectedMsg)] != expectedMsg {
			t.Errorf("Expected error message to start with '%s', got '%s'", expectedMsg, err.Error())
		}
	}
}

func TestOpenAIProviderConvertMessages_PreservesAssistantToolCalls(t *testing.T) {
	provider := NewOpenAIProvider(ProviderConfig{APIKey: "test-key"})

	toolCallID := strings.Repeat("x", 45)
	messages := []Message{
		{
			Role: RoleAssistant,
			ToolCalls: []ToolCall{
				{
					ID:        toolCallID,
					Name:      "list_allowed_directories",
					Arguments: `{}`,
				},
			},
		},
		NewToolMessage(toolCallID, "Allowed directories:\n/Users/jjdev/Documents"),
	}

	converted := provider.convertMessages(messages, "")
	if len(converted) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(converted))
	}

	assistantCalls := converted[0].GetToolCalls()
	if len(assistantCalls) != 1 {
		t.Fatalf("expected 1 assistant tool call, got %d", len(assistantCalls))
	}

	assistantToolCallID := assistantCalls[0].GetID()
	if assistantToolCallID == nil || *assistantToolCallID == "" {
		t.Fatalf("expected assistant tool call ID to be present")
	}
	if len(*assistantToolCallID) > 40 {
		t.Fatalf("expected assistant tool call ID <= 40 chars, got %d", len(*assistantToolCallID))
	}

	toolMessageID := converted[1].GetToolCallID()
	if toolMessageID == nil || *toolMessageID == "" {
		t.Fatalf("expected tool message tool_call_id to be present")
	}
	if *toolMessageID != *assistantToolCallID {
		t.Fatalf("expected tool message ID %q to match assistant tool call ID %q", *toolMessageID, *assistantToolCallID)
	}
}

func TestOpenAIProviderConvertMessages_UsesToolResultAsContentNotToolCallID(t *testing.T) {
	provider := NewOpenAIProvider(ProviderConfig{APIKey: "test-key"})

	converted := provider.convertMessages([]Message{
		NewToolMessage("tool-123", "Allowed directories:\n/Users/jjdev/Documents"),
	}, "")

	if len(converted) != 1 {
		t.Fatalf("expected 1 message, got %d", len(converted))
	}

	data, err := json.Marshal(converted[0])
	if err != nil {
		t.Fatalf("failed to marshal converted message: %v", err)
	}

	var payload struct {
		Content    string `json:"content"`
		ToolCallID string `json:"tool_call_id"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("failed to unmarshal converted message: %v", err)
	}

	if payload.ToolCallID != "tool-123" {
		t.Fatalf("expected tool_call_id to be tool-123, got %q", payload.ToolCallID)
	}
	if payload.Content != "Allowed directories:\n/Users/jjdev/Documents" {
		t.Fatalf("expected tool result content to be preserved, got %q", payload.Content)
	}
}

// TestOpenAIProviderIntegration tests the full flow (requires valid API key)
// This test is skipped by default - remove t.Skip() to run with a real API key
func TestOpenAIProviderIntegration(t *testing.T) {
	t.Skip("Integration test - requires valid OpenAI API key")

	// To run this test:
	// 1. Set OPENAI_API_KEY environment variable
	// 2. Remove t.Skip() above
	// 3. Run: go test -v -run TestOpenAIProviderIntegration

	apiKey := "" // Set your API key here or use env var
	if apiKey == "" {
		t.Skip("No API key provided")
	}

	provider := NewOpenAIProvider(ProviderConfig{APIKey: apiKey})

	req := ChatRequest{
		Model: "gpt-4o",
		Messages: []Message{
			NewUserMessage("Say 'test successful' and nothing else"),
		},
		Temperature: 0.0,
		MaxTokens:   10,
	}

	resp, err := provider.Chat(context.Background(), req)
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}

	if resp == nil {
		t.Fatal("Response is nil")
		return
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
	t.Logf("Usage: %+v", resp.Usage)
}
