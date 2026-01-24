package llm

import (
	"encoding/json"
	"testing"
)

func TestGeminiProviderMetadata(t *testing.T) {
	provider := NewGeminiProvider(ProviderConfig{APIKey: "test-key"})

	if provider.Name() != "gemini" {
		t.Errorf("Expected name 'gemini', got %q", provider.Name())
	}

	if provider.Type() != ProviderTypeCloud {
		t.Errorf("Expected type ProviderTypeCloud, got %q", provider.Type())
	}

	caps := provider.Capabilities()
	if !caps.SupportsTools {
		t.Error("Expected Gemini to support tools")
	}
	if !caps.SupportsStreaming {
		t.Error("Expected Gemini to support streaming")
	}
	if !caps.SupportsSystemPrompt {
		t.Error("Expected Gemini to support system prompts")
	}
	if !caps.SupportsTemperature {
		t.Error("Expected Gemini to support temperature")
	}
	if !caps.RequiresAPIKey {
		t.Error("Expected Gemini to require API key")
	}
	if caps.SupportsCustomEndpoint {
		t.Error("Expected Gemini to not support custom endpoint")
	}
	if caps.MaxContextWindow != 0 {
		t.Errorf("Expected max context window 0, got %d", caps.MaxContextWindow)
	}
}

func TestGeminiProviderDefaultModels(t *testing.T) {
	provider := NewGeminiProvider(ProviderConfig{APIKey: "test-key"})
	models := provider.DefaultModels()

	if len(models) == 0 {
		t.Error("Expected at least one Gemini model, got none")
	}

	foundFlash := false
	foundPro := false
	for _, model := range models {
		if model == "gemini-2.5-flash" {
			foundFlash = true
		}
		if model == "gemini-2.5-pro" {
			foundPro = true
		}
	}

	if !foundFlash || !foundPro {
		t.Errorf("Expected gemini-2.5-flash and gemini-2.5-pro, got %v", models)
	}
}

func TestGeminiProviderValidateConfig(t *testing.T) {
	provider := NewGeminiProvider(ProviderConfig{})

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

func TestGeminiProviderBuildRequest(t *testing.T) {
	provider := NewGeminiProvider(ProviderConfig{APIKey: "test-key"})

	req := ChatRequest{
		Model:        "gemini-2.5-flash",
		SystemPrompt: "System prompt",
		Messages: []Message{
			NewSystemMessage("Extra system"),
			NewUserMessage("Hello"),
			{
				Role: RoleAssistant,
				ToolCalls: []ToolCall{
					{Name: "calculator", Arguments: `{"a":1,"b":2}`},
				},
			},
			NewToolMessage("calculator:0", `{"result": 3}`),
		},
		Tools: []Tool{
			{
				Name:        "calculator",
				Description: "Do math",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"a": map[string]interface{}{"type": "number"},
						"b": map[string]interface{}{"type": "number"},
					},
				},
			},
		},
	}

	built, err := provider.buildRequest(req)
	if err != nil {
		t.Fatalf("buildRequest failed: %v", err)
	}

	if built.SystemInstruction == nil || len(built.SystemInstruction.Parts) < 2 {
		t.Fatal("Expected system instruction to include system prompt and system message")
	}

	if len(built.Contents) != 3 {
		t.Fatalf("Expected 3 content items (user, assistant, tool), got %d", len(built.Contents))
	}

	foundFunctionCall := false
	for _, part := range built.Contents[1].Parts {
		if part.FunctionCall != nil && part.FunctionCall.Name == "calculator" {
			foundFunctionCall = true
			break
		}
	}
	if !foundFunctionCall {
		t.Error("Expected assistant message to include function call part")
	}

	foundFunctionResponse := false
	for _, part := range built.Contents[2].Parts {
		if part.FunctionResponse != nil && part.FunctionResponse.Name == "calculator" {
			foundFunctionResponse = true
			break
		}
	}
	if !foundFunctionResponse {
		t.Error("Expected tool message to include function response part")
	}

	responseBytes, err := json.Marshal(built.Contents[2].Parts[0].FunctionResponse.Response)
	if err != nil {
		t.Fatalf("Failed to marshal function response: %v", err)
	}
	if len(responseBytes) == 0 {
		t.Error("Expected function response payload to be non-empty")
	}
}
