package agenthttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/llm"
)

// createTestConfigManager creates a config manager for testing
func createTestConfigManager(t *testing.T, systemProvider, systemModel string) *config.Manager {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "settings.json")
	configManager := config.NewManager(tmpFile)
	_ = configManager.Load()
	if systemProvider != "" && systemModel != "" {
		_ = configManager.SetSystemModel(systemProvider, systemModel)
	}
	return configManager
}

// TestAutoConfigHandler_CheckLLMAvailability tests the LLM availability check endpoint
func TestAutoConfigHandler_CheckLLMAvailability(t *testing.T) {
	tests := []struct {
		name             string
		setupFactory     func() *llm.Factory
		systemProvider   string
		systemModel      string
		expectedAvail    bool
		expectedSMConfig bool
		expectedStatus   int
	}{
		{
			name: "no providers registered, no system model",
			setupFactory: func() *llm.Factory {
				return llm.NewFactory()
			},
			systemProvider:   "",
			systemModel:      "",
			expectedAvail:    false,
			expectedSMConfig: false,
			expectedStatus:   http.StatusOK,
		},
		{
			name: "provider registered but no system model",
			setupFactory: func() *llm.Factory {
				factory := llm.NewFactory()
				factory.Register("openai", &mockProvider{})
				return factory
			},
			systemProvider:   "",
			systemModel:      "",
			expectedAvail:    false, // Not available because no system model
			expectedSMConfig: false,
			expectedStatus:   http.StatusOK,
		},
		{
			name: "provider registered and system model configured",
			setupFactory: func() *llm.Factory {
				factory := llm.NewFactory()
				factory.Register("openai", &mockProvider{})
				return factory
			},
			systemProvider:   "openai",
			systemModel:      "gpt-4o-mini",
			expectedAvail:    true,
			expectedSMConfig: true,
			expectedStatus:   http.StatusOK,
		},
		{
			name: "system model configured but provider not available",
			setupFactory: func() *llm.Factory {
				return llm.NewFactory() // Empty factory
			},
			systemProvider:   "openai",
			systemModel:      "gpt-4o-mini",
			expectedAvail:    false, // Provider not available
			expectedSMConfig: true,
			expectedStatus:   http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			factory := tt.setupFactory()
			configManager := createTestConfigManager(t, tt.systemProvider, tt.systemModel)
			handler := NewAutoConfigHandler(factory, configManager)

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

			if response.SystemModelConfigured != tt.expectedSMConfig {
				t.Errorf("Expected system_model_configured=%v, got %v", tt.expectedSMConfig, response.SystemModelConfigured)
			}

			if !tt.expectedAvail && response.Message == "" {
				t.Error("Expected message when not available")
			}
		})
	}
}

// TestAutoConfigHandler_AutoConfig_NoSystemModel tests auto-config when system model not configured
func TestAutoConfigHandler_AutoConfig_NoSystemModel(t *testing.T) {
	factory := llm.NewFactory()
	factory.Register("openai", &mockProvider{})
	configManager := createTestConfigManager(t, "", "") // No system model
	handler := NewAutoConfigHandler(factory, configManager)

	reqBody := AutoConfigRequest{
		Description: "An agent that helps with file management",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/agents/auto-config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.AutoConfigHandler(rr, req)

	// Should return service unavailable when no system model configured
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected status %d, got %d. Body: %s", http.StatusServiceUnavailable, rr.Code, rr.Body.String())
	}
}

// TestAutoConfigHandler_AutoConfig_ProviderNotAvailable tests auto-config when provider not registered
func TestAutoConfigHandler_AutoConfig_ProviderNotAvailable(t *testing.T) {
	factory := llm.NewFactory()                                          // Empty factory - provider not registered
	configManager := createTestConfigManager(t, "openai", "gpt-4o-mini") // System model configured
	handler := NewAutoConfigHandler(factory, configManager)

	reqBody := AutoConfigRequest{
		Description: "An agent that helps with file management",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/agents/auto-config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.AutoConfigHandler(rr, req)

	// Should return service unavailable when provider not available
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected status %d, got %d. Body: %s", http.StatusServiceUnavailable, rr.Code, rr.Body.String())
	}
}

// TestAutoConfigHandler_AutoConfig_EmptyDescription tests auto-config with empty description
func TestAutoConfigHandler_AutoConfig_EmptyDescription(t *testing.T) {
	factory := llm.NewFactory()
	factory.Register("openai", &mockProvider{})
	configManager := createTestConfigManager(t, "openai", "gpt-4o-mini")
	handler := NewAutoConfigHandler(factory, configManager)

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

func TestAutoConfigHandler_AutoConfig_SuccessIncludesGeneratedDescription(t *testing.T) {
	factory := llm.NewFactory()
	factory.Register("openai", &mockProvider{})
	configManager := createTestConfigManager(t, "openai", "mock-model")
	handler := NewAutoConfigHandler(factory, configManager)

	reqBody := AutoConfigRequest{
		Description: "An agent that helps with weather forecasts and severe weather alerts",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/agents/auto-config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.AutoConfigHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("Expected status %d, got %d. Body: %s", http.StatusOK, rr.Code, rr.Body.String())
	}

	var response AutoConfigResponse
	if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response.Description == "" {
		t.Fatal("Expected generated description to be populated")
	}
}

// TestAutoConfigHandler_AutoConfig_InvalidMethod tests auto-config with wrong HTTP method
func TestAutoConfigHandler_AutoConfig_InvalidMethod(t *testing.T) {
	factory := llm.NewFactory()
	configManager := createTestConfigManager(t, "", "")
	handler := NewAutoConfigHandler(factory, configManager)

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
				Model:        "gpt-4.1-nano",
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

// TestAutoConfigHandler_validateAndSanitizeConfig_OrchestrationPrefersSystemModel
// verifies that orchestration agents always use the configured system model,
// overriding whatever the LLM returned. The LLM often echoes the example
// model (gpt-4.1-nano) from its prompt, which isn't suitable for
// coordination work.
func TestAutoConfigHandler_validateAndSanitizeConfig_OrchestrationPrefersSystemModel(t *testing.T) {
	configManager := createTestConfigManager(t, "ollama", "gemma4:e4b")
	handler := &AutoConfigHandler{configManager: configManager}

	tests := []struct {
		name             string
		input            AutoConfigResponse
		expectedModel    string
		expectedProvider string
	}{
		{
			name: "orchestration with LLM-returned gpt-4.1-nano is overridden",
			input: AutoConfigResponse{
				AgentType:    "orchestration",
				Model:        "gpt-4.1-nano",
				Provider:     "openai",
				Temperature:  0.5,
				SystemPrompt: "You coordinate.",
			},
			expectedModel:    "gemma4:e4b",
			expectedProvider: "ollama",
		},
		{
			name: "orchestration with empty model picks up system model",
			input: AutoConfigResponse{
				AgentType:    "orchestration",
				Model:        "",
				Provider:     "",
				Temperature:  0.5,
				SystemPrompt: "You coordinate.",
			},
			expectedModel:    "gemma4:e4b",
			expectedProvider: "ollama",
		},
		{
			name: "non-orchestration is left alone",
			input: AutoConfigResponse{
				AgentType:    "tool-calling",
				Model:        "gpt-4.1-nano",
				Provider:     "openai",
				Temperature:  0.5,
				SystemPrompt: "You are helpful.",
			},
			expectedModel:    "gpt-4.1-nano",
			expectedProvider: "openai",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := handler.validateAndSanitizeConfig(tt.input)
			if result.Model != tt.expectedModel {
				t.Errorf("Model: expected %q, got %q", tt.expectedModel, result.Model)
			}
			if result.Provider != tt.expectedProvider {
				t.Errorf("Provider: expected %q, got %q", tt.expectedProvider, result.Provider)
			}
		})
	}
}

// TestAutoConfigHandler_validateAndSanitizeConfig_OrchestrationWithoutSystemModel
// verifies that when no system model is configured, orchestration agents
// fall back to a capable default (gpt-5) rather than gpt-4.1-nano.
func TestAutoConfigHandler_validateAndSanitizeConfig_OrchestrationWithoutSystemModel(t *testing.T) {
	configManager := createTestConfigManager(t, "", "")
	handler := &AutoConfigHandler{configManager: configManager}

	input := AutoConfigResponse{
		AgentType:    "orchestration",
		Model:        "",
		Provider:     "",
		Temperature:  0.5,
		SystemPrompt: "You coordinate.",
	}

	result := handler.validateAndSanitizeConfig(input)
	if result.Model != "gpt-5" {
		t.Errorf("expected orchestration fallback model 'gpt-5', got %q", result.Model)
	}
}

// TestAutoConfigHandler_getDefaultConfig tests default config generation
func TestAutoConfigHandler_getDefaultConfig(t *testing.T) {
	handler := &AutoConfigHandler{}

	config := handler.getDefaultConfig()

	if config.AgentType != "tool-calling" {
		t.Errorf("Expected default agent type 'tool-calling', got %q", config.AgentType)
	}
	if config.Model != "gpt-4.1-nano" {
		t.Errorf("Expected default model 'gpt-4.1-nano', got %q", config.Model)
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
	if config.Description == "" {
		t.Error("Expected non-empty default description")
	}
}

func TestResolveAutoConfigDescription(t *testing.T) {
	tests := []struct {
		name      string
		generated string
		source    string
		agentName string
		expected  string
	}{
		{
			name:      "prefers generated description",
			generated: "Provides concise weather forecasts and storm updates.",
			source:    "weather helper",
			agentName: "Weather Assistant",
			expected:  "Provides concise weather forecasts and storm updates.",
		},
		{
			name:      "falls back to source description",
			source:    "  Helps triage inbox requests for the team.  ",
			agentName: "Inbox Agent",
			expected:  "Helps triage inbox requests for the team.",
		},
		{
			name:      "falls back to agent name",
			agentName: "Research Agent",
			expected:  "Research Agent helps with specialized tasks.",
		},
		{
			name:     "falls back to generic description",
			expected: "Helpful AI assistant for general tasks.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := resolveAutoConfigDescription(tt.generated, tt.source, tt.agentName)
			if result != tt.expected {
				t.Fatalf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

// mockProvider is a minimal mock LLM provider for testing
type mockProvider struct{}

func (m *mockProvider) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	// Return a mock auto-config response
	return &llm.ChatResponse{
		Content: `{
			"agent_name": "Weather Assistant",
			"description": "Provides weather forecasts, current conditions, and alert guidance in a concise format.",
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
