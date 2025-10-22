package llm

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestClaudeProviderMetadata(t *testing.T) {
	config := ProviderConfig{
		APIKey: "test-api-key",
	}
	provider := NewClaudeProvider(config)

	// Test Name
	if provider.Name() != "claude" {
		t.Errorf("Expected name 'claude', got '%s'", provider.Name())
	}

	// Test Type
	if provider.Type() != ProviderTypeCloud {
		t.Errorf("Expected type ProviderTypeCloud, got '%s'", provider.Type())
	}

	// Test Capabilities
	caps := provider.Capabilities()
	if !caps.SupportsTools {
		t.Error("Expected Claude to support tools")
	}
	if !caps.SupportsStreaming {
		t.Error("Expected Claude to support streaming")
	}
	if !caps.SupportsSystemPrompt {
		t.Error("Expected Claude to support system prompts")
	}
	if !caps.SupportsTemperature {
		t.Error("Expected Claude to support temperature")
	}
	if !caps.RequiresAPIKey {
		t.Error("Expected Claude to require API key")
	}
	if caps.SupportsCustomEndpoint {
		t.Error("Expected Claude to not support custom endpoint")
	}
	if caps.MaxContextWindow != 200000 {
		t.Errorf("Expected max context window 200000, got %d", caps.MaxContextWindow)
	}
}

func TestClaudeProviderDefaultModels(t *testing.T) {
	config := ProviderConfig{
		APIKey: "test-api-key",
	}
	provider := NewClaudeProvider(config)

	models := provider.DefaultModels()
	expectedModels := []string{
		"claude-3-5-sonnet-20241022",
		"claude-3-5-sonnet-20240620",
		"claude-3-5-haiku-20241022",
		"claude-3-opus-20240229",
		"claude-3-sonnet-20240229",
		"claude-3-haiku-20240307",
	}

	if len(models) != len(expectedModels) {
		t.Errorf("Expected %d models, got %d", len(expectedModels), len(models))
	}

	for i, expected := range expectedModels {
		if i >= len(models) || models[i] != expected {
			t.Errorf("Expected model '%s' at index %d, got '%s'", expected, i, models[i])
		}
	}
}

func TestClaudeProviderValidateConfig(t *testing.T) {
	provider := NewClaudeProvider(ProviderConfig{})

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

func TestClaudeProviderConvertMessages(t *testing.T) {
	provider := NewClaudeProvider(ProviderConfig{APIKey: "test-key"})

	tests := []struct {
		name        string
		messages    []Message
		expectedLen int
	}{
		{
			name: "User and assistant messages",
			messages: []Message{
				NewUserMessage("Hello"),
				NewAssistantMessage("Hi there"),
			},
			expectedLen: 2,
		},
		{
			name: "System message is skipped",
			messages: []Message{
				NewSystemMessage("You are helpful"),
				NewUserMessage("Hello"),
			},
			expectedLen: 1, // System message should be skipped
		},
		{
			name: "Tool message converted to user message",
			messages: []Message{
				NewUserMessage("What is 2+2?"),
				NewToolMessage("tool-123", "4"),
			},
			expectedLen: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			converted := provider.convertMessages(tt.messages)
			if len(converted) != tt.expectedLen {
				t.Errorf("Expected %d messages, got %d", tt.expectedLen, len(converted))
			}
		})
	}
}

func TestClaudeProviderConvertTools(t *testing.T) {
	provider := NewClaudeProvider(ProviderConfig{APIKey: "test-key"})

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
				"required": []string{"operation"},
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

	// Just verify that tools were converted (the anthropic types are opaque)
	// Detailed verification would require making actual API calls
}

func TestClaudeProviderUpdateClient(t *testing.T) {
	provider := NewClaudeProvider(ProviderConfig{APIKey: "initial-key"})

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

func TestClaudeProviderStreamChatNotImplemented(t *testing.T) {
	provider := NewClaudeProvider(ProviderConfig{APIKey: "test-key"})

	req := ChatRequest{
		Model: "claude-3-5-sonnet-20241022",
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

// TestClaudeProviderIntegration tests the full flow with Claude API
// This test requires a valid ANTHROPIC_API_KEY environment variable
func TestClaudeProviderIntegration(t *testing.T) {
	t.Skip("Integration test - requires valid Anthropic API key")

	// To run this test:
	// 1. Set ANTHROPIC_API_KEY environment variable
	// 2. Remove t.Skip() above
	// 3. Run: go test -v -run TestClaudeProviderIntegration

	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("No API key provided")
	}

	provider := NewClaudeProvider(ProviderConfig{APIKey: apiKey})

	req := ChatRequest{
		Model: "claude-3-5-haiku-20241022",
		Messages: []Message{
			NewUserMessage("Say 'test successful' and nothing else"),
		},
		Temperature: 0.0,
		MaxTokens:   10,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := provider.Chat(ctx, req)
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}

	if resp == nil {
		t.Fatal("Response is nil")
	}

	if resp.Content == "" {
		t.Error("Response content is empty")
	}

	if resp.Provider != "claude" {
		t.Errorf("Expected provider 'claude', got '%s'", resp.Provider)
	}

	if resp.Usage.TotalTokens == 0 {
		t.Error("Expected non-zero token usage")
	}

	t.Logf("Response: %s", resp.Content)
	t.Logf("Usage: %+v", resp.Usage)
}
