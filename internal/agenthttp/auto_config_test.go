package agenthttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnjallday/ori-agent/internal/llm"
)

// TestAutoConfigHandler_CheckLLMAvailability tests the LLM availability check endpoint
func TestAutoConfigHandler_CheckLLMAvailability(t *testing.T) {
	tests := []struct {
		name           string
		setupFactory   func() *llm.Factory
		expectedAvail  bool
		expectedStatus int
	}{
		{
			name: "no providers registered",
			setupFactory: func() *llm.Factory {
				return llm.NewFactory()
			},
			expectedAvail:  false,
			expectedStatus: http.StatusOK,
		},
		{
			name: "provider registered",
			setupFactory: func() *llm.Factory {
				factory := llm.NewFactory()
				// Register a mock provider
				factory.Register("mock", &mockProvider{})
				return factory
			},
			expectedAvail:  true,
			expectedStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			factory := tt.setupFactory()
			handler := NewAutoConfigHandler(factory)

			req := httptest.NewRequest(http.MethodGet, "/api/agents/auto-config/availability", nil)
			rr := httptest.NewRecorder()

			handler.CheckLLMAvailabilityHandler(rr, req)

			if rr.Code != tt.expectedStatus {
				t.Errorf("Expected status %d, got %d", tt.expectedStatus, rr.Code)
			}

			var response LLMAvailabilityResponse
			if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
				t.Fatalf("Failed to decode response: %v", err)
			}

			if response.Available != tt.expectedAvail {
				t.Errorf("Expected available=%v, got %v", tt.expectedAvail, response.Available)
			}

			if !tt.expectedAvail && response.Message == "" {
				t.Error("Expected message when LLM not available")
			}
		})
	}
}

// TestAutoConfigHandler_AutoConfig_NoProvider tests auto-config when no LLM provider is available
func TestAutoConfigHandler_AutoConfig_NoProvider(t *testing.T) {
	factory := llm.NewFactory() // Empty factory
	handler := NewAutoConfigHandler(factory)

	reqBody := AutoConfigRequest{
		Description: "An agent that helps with file management",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/agents/auto-config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.AutoConfigHandler(rr, req)

	// Should return service unavailable when no LLM provider
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected status %d, got %d. Body: %s", http.StatusServiceUnavailable, rr.Code, rr.Body.String())
	}
}

// TestAutoConfigHandler_AutoConfig_EmptyDescription tests auto-config with empty description
func TestAutoConfigHandler_AutoConfig_EmptyDescription(t *testing.T) {
	factory := llm.NewFactory()
	factory.Register("mock", &mockProvider{})
	handler := NewAutoConfigHandler(factory)

	reqBody := AutoConfigRequest{
		Description: "",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/agents/auto-config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.AutoConfigHandler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("Expected status %d, got %d. Body: %s", http.StatusBadRequest, rr.Code, rr.Body.String())
	}
}

// TestAutoConfigHandler_AutoConfig_InvalidMethod tests auto-config with wrong HTTP method
func TestAutoConfigHandler_AutoConfig_InvalidMethod(t *testing.T) {
	factory := llm.NewFactory()
	handler := NewAutoConfigHandler(factory)

	req := httptest.NewRequest(http.MethodGet, "/api/agents/auto-config", nil)
	rr := httptest.NewRecorder()

	handler.AutoConfigHandler(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected status %d, got %d", http.StatusMethodNotAllowed, rr.Code)
	}
}

// TestAutoConfigHandler_validateAndSanitizeConfig tests config validation
func TestAutoConfigHandler_validateAndSanitizeConfig(t *testing.T) {
	handler := &AutoConfigHandler{}

	tests := []struct {
		name     string
		input    AutoConfigResponse
		expected AutoConfigResponse
	}{
		{
			name: "valid config unchanged",
			input: AutoConfigResponse{
				AgentType:    "tool-calling",
				Model:        "gpt-4o-mini",
				Provider:     "openai",
				Temperature:  0.5,
				SystemPrompt: "You are helpful.",
			},
			expected: AutoConfigResponse{
				AgentType:    "tool-calling",
				Model:        "gpt-4o-mini",
				Provider:     "openai",
				Temperature:  0.5,
				SystemPrompt: "You are helpful.",
			},
		},
		{
			name: "invalid agent type defaults to tool-calling",
			input: AutoConfigResponse{
				AgentType:    "invalid-type",
				Model:        "gpt-4o",
				Provider:     "openai",
				Temperature:  0.7,
				SystemPrompt: "Test",
			},
			expected: AutoConfigResponse{
				AgentType:    "tool-calling",
				Model:        "gpt-4o",
				Provider:     "openai",
				Temperature:  0.7,
				SystemPrompt: "Test",
			},
		},
		{
			name: "negative temperature corrected to 0",
			input: AutoConfigResponse{
				AgentType:    "general",
				Model:        "gpt-4o",
				Provider:     "openai",
				Temperature:  -0.5,
				SystemPrompt: "Test",
			},
			expected: AutoConfigResponse{
				AgentType:    "general",
				Model:        "gpt-4o",
				Provider:     "openai",
				Temperature:  0,
				SystemPrompt: "Test",
			},
		},
		{
			name: "temperature above 2 corrected to 1",
			input: AutoConfigResponse{
				AgentType:    "research",
				Model:        "gpt-4o",
				Provider:     "openai",
				Temperature:  3.0,
				SystemPrompt: "Test",
			},
			expected: AutoConfigResponse{
				AgentType:    "research",
				Model:        "gpt-4o",
				Provider:     "openai",
				Temperature:  1.0,
				SystemPrompt: "Test",
			},
		},
		{
			name: "empty model gets default for type",
			input: AutoConfigResponse{
				AgentType:    "tool-calling",
				Model:        "",
				Provider:     "openai",
				Temperature:  0.7,
				SystemPrompt: "Test",
			},
			expected: AutoConfigResponse{
				AgentType:    "tool-calling",
				Model:        "gpt-4o-mini",
				Provider:     "openai",
				Temperature:  0.7,
				SystemPrompt: "Test",
			},
		},
		{
			name: "empty system prompt gets default",
			input: AutoConfigResponse{
				AgentType:    "general",
				Model:        "gpt-4o",
				Provider:     "openai",
				Temperature:  0.7,
				SystemPrompt: "",
			},
			expected: AutoConfigResponse{
				AgentType:    "general",
				Model:        "gpt-4o",
				Provider:     "openai",
				Temperature:  0.7,
				SystemPrompt: "You are a helpful AI assistant.",
			},
		},
		{
			name: "invalid provider defaults to openai",
			input: AutoConfigResponse{
				AgentType:    "general",
				Model:        "gpt-4o",
				Provider:     "invalid-provider",
				Temperature:  0.7,
				SystemPrompt: "Test",
			},
			expected: AutoConfigResponse{
				AgentType:    "general",
				Model:        "gpt-4o",
				Provider:     "openai",
				Temperature:  0.7,
				SystemPrompt: "Test",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := handler.validateAndSanitizeConfig(tt.input)

			if result.AgentType != tt.expected.AgentType {
				t.Errorf("AgentType: expected %q, got %q", tt.expected.AgentType, result.AgentType)
			}
			if result.Model != tt.expected.Model {
				t.Errorf("Model: expected %q, got %q", tt.expected.Model, result.Model)
			}
			if result.Provider != tt.expected.Provider {
				t.Errorf("Provider: expected %q, got %q", tt.expected.Provider, result.Provider)
			}
			if result.Temperature != tt.expected.Temperature {
				t.Errorf("Temperature: expected %v, got %v", tt.expected.Temperature, result.Temperature)
			}
			if result.SystemPrompt != tt.expected.SystemPrompt {
				t.Errorf("SystemPrompt: expected %q, got %q", tt.expected.SystemPrompt, result.SystemPrompt)
			}
		})
	}
}

// TestAutoConfigHandler_getDefaultConfig tests default config generation
func TestAutoConfigHandler_getDefaultConfig(t *testing.T) {
	handler := &AutoConfigHandler{}

	config := handler.getDefaultConfig()

	if config.AgentType != "tool-calling" {
		t.Errorf("Expected default agent type 'tool-calling', got %q", config.AgentType)
	}
	if config.Model != "gpt-4o-mini" {
		t.Errorf("Expected default model 'gpt-4o-mini', got %q", config.Model)
	}
	if config.Provider != "openai" {
		t.Errorf("Expected default provider 'openai', got %q", config.Provider)
	}
	if config.Temperature != 0.7 {
		t.Errorf("Expected default temperature 0.7, got %v", config.Temperature)
	}
	if config.SystemPrompt == "" {
		t.Error("Expected non-empty default system prompt")
	}
}

// mockProvider is a minimal mock LLM provider for testing
type mockProvider struct{}

func (m *mockProvider) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	// Return a mock auto-config response
	return &llm.ChatResponse{
		Content: `{
			"agent_type": "tool-calling",
			"model": "gpt-4o-mini",
			"provider": "openai",
			"temperature": 0.5,
			"system_prompt": "You are a helpful assistant.",
			"reasoning": "Mock response for testing"
		}`,
	}, nil
}

func (m *mockProvider) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	return nil, nil
}

func (m *mockProvider) Name() string {
	return "mock"
}

func (m *mockProvider) Type() llm.ProviderType {
	return llm.ProviderTypeCloud
}

func (m *mockProvider) Capabilities() llm.ProviderCapabilities {
	return llm.ProviderCapabilities{
		SupportsTools:        true,
		SupportsStreaming:    true,
		SupportsSystemPrompt: true,
		SupportsTemperature:  true,
		RequiresAPIKey:       false,
	}
}

func (m *mockProvider) ValidateConfig(config llm.ProviderConfig) error {
	return nil
}

func (m *mockProvider) DefaultModels() []string {
	return []string{"mock-model"}
}
