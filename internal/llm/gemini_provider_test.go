package llm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestGeminiProviderDefaultModels_UsesLiveModelEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("x-goog-api-key"); got != "test-key" {
			http.Error(w, "missing api key header", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
		  "models": [
		    {"name": "models/gemini-2.5-pro", "supportedGenerationMethods": ["generateContent"]},
		    {"name": "models/gemini-2.5-flash", "supportedGenerationMethods": ["generateContent", "streamGenerateContent"]},
		    {"name": "models/gemini-2.0-flash", "supportedGenerationMethods": ["generateContent"]},
		    {"name": "models/gemini-embedding-001", "supportedGenerationMethods": ["embedContent"]},
		    {"name": "models/text-embedding-004", "supportedGenerationMethods": ["embedContent"]},
		    {"name": "models/imagen-3.0-generate-002", "supportedGenerationMethods": ["generateContent"]}
		  ]
		}`))
	}))
	defer server.Close()

	provider := NewGeminiProvider(ProviderConfig{
		APIKey:  "test-key",
		BaseURL: server.URL,
	})
	models := provider.DefaultModels()

	if len(models) < 3 {
		t.Fatalf("expected at least 3 Gemini models from API, got %v", models)
	}
	if models[0] != "gemini-2.5-pro" {
		t.Fatalf("expected gemini-2.5-pro first, got %q (all: %v)", models[0], models)
	}
	if !containsGeminiModel(models, "gemini-2.5-flash") {
		t.Fatalf("expected gemini-2.5-flash in models, got %v", models)
	}
	if !containsGeminiModel(models, "gemini-2.0-flash") {
		t.Fatalf("expected gemini-2.0-flash in models, got %v", models)
	}
	if containsGeminiModel(models, "gemini-embedding-001") {
		t.Fatalf("embedding model should not be included, got %v", models)
	}
	if containsGeminiModel(models, "imagen-3.0-generate-002") {
		t.Fatalf("non-gemini model should not be included, got %v", models)
	}
}

func TestGeminiProviderDefaultModels_FallsBackWhenEndpointUnavailable(t *testing.T) {
	provider := NewGeminiProvider(ProviderConfig{
		APIKey:  "test-key",
		BaseURL: "http://127.0.0.1:1",
	})

	models := provider.DefaultModels()
	if len(models) < 4 {
		t.Fatalf("expected fallback Gemini model list, got %v", models)
	}
	if !containsGeminiModel(models, "gemini-2.5-pro") || !containsGeminiModel(models, "gemini-2.5-flash") {
		t.Fatalf("expected fallback to include gemini-2.5 models, got %v", models)
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

	built := provider.buildRequest(req)

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

func TestGeminiProviderBuildRequest_SanitizesToolSchemaForGemini(t *testing.T) {
	provider := NewGeminiProvider(ProviderConfig{APIKey: "test-key"})

	originalParams := map[string]interface{}{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type":    "object",
		"properties": map[string]interface{}{
			"url": map[string]interface{}{
				"type": "string",
			},
			"filters": map[string]interface{}{
				"type": "array",
				"items": map[string]interface{}{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]interface{}{
						"name": map[string]interface{}{"type": "string"},
					},
				},
			},
		},
		"required":             []string{"url"},
		"additionalProperties": false,
	}

	req := ChatRequest{
		Model:    "gemini-2.5-flash",
		Messages: []Message{NewUserMessage("Open this URL")},
		Tools: []Tool{
			{
				Name:        "open_url",
				Description: "Open a URL",
				Parameters:  originalParams,
			},
		},
	}

	built := provider.buildRequest(req)

	if len(built.Tools) != 1 || len(built.Tools[0].FunctionDeclarations) != 1 {
		t.Fatalf("expected one function declaration, got %#v", built.Tools)
	}

	sanitized := built.Tools[0].FunctionDeclarations[0].Parameters
	raw, err := json.Marshal(sanitized)
	if err != nil {
		t.Fatalf("failed to marshal sanitized schema: %v", err)
	}
	text := string(raw)
	if strings.Contains(text, "\"$schema\"") {
		t.Fatalf("sanitized schema still contains $schema: %s", text)
	}
	if strings.Contains(text, "\"additionalProperties\"") {
		t.Fatalf("sanitized schema still contains additionalProperties: %s", text)
	}

	// Ensure original input schema remains untouched for other providers.
	if _, ok := originalParams["$schema"]; !ok {
		t.Fatal("expected original schema to still include $schema")
	}
	if _, ok := originalParams["additionalProperties"]; !ok {
		t.Fatal("expected original schema to still include additionalProperties")
	}
}

func containsGeminiModel(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
