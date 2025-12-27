package llm

import (
	"context"
	"testing"
)

// MockProvider is a mock implementation of the Provider interface for testing
type MockProvider struct {
	name         string
	providerType ProviderType
	capabilities ProviderCapabilities
	models       []string
	chatFunc     func(ctx context.Context, req ChatRequest) (*ChatResponse, error)
}

func (m *MockProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	if m.chatFunc != nil {
		return m.chatFunc(ctx, req)
	}
	return &ChatResponse{
		Content:  "Mock response",
		Provider: m.name,
		Model:    req.Model,
	}, nil
}

func (m *MockProvider) StreamChat(ctx context.Context, req ChatRequest) (StreamReader, error) {
	return nil, nil
}

func (m *MockProvider) Name() string {
	return m.name
}

func (m *MockProvider) Type() ProviderType {
	return m.providerType
}

func (m *MockProvider) Capabilities() ProviderCapabilities {
	return m.capabilities
}

func (m *MockProvider) ValidateConfig(config ProviderConfig) error {
	return nil
}

func (m *MockProvider) DefaultModels() []string {
	return m.models
}

// Test factory creation
func TestNewFactory(t *testing.T) {
	factory := NewFactory()
	if factory == nil {
		t.Fatal("NewFactory returned nil")
	}
	if factory.ProviderCount() != 0 {
		t.Errorf("Expected 0 providers, got %d", factory.ProviderCount())
	}
}

// Test provider registration
func TestRegisterProvider(t *testing.T) {
	factory := NewFactory()

	mockProvider := &MockProvider{
		name:         "mock",
		providerType: ProviderTypeCloud,
		models:       []string{"model-1", "model-2"},
	}

	factory.Register("mock", mockProvider)

	if factory.ProviderCount() != 1 {
		t.Errorf("Expected 1 provider, got %d", factory.ProviderCount())
	}

	if !factory.HasProvider("mock") {
		t.Error("Provider 'mock' should be registered")
	}
}

// Test getting a provider
func TestGetProvider(t *testing.T) {
	factory := NewFactory()

	mockProvider := &MockProvider{
		name:         "mock",
		providerType: ProviderTypeCloud,
	}

	factory.Register("mock", mockProvider)

	provider, err := factory.GetProvider("mock")
	if err != nil {
		t.Fatalf("GetProvider failed: %v", err)
	}

	if provider.Name() != "mock" {
		t.Errorf("Expected provider name 'mock', got '%s'", provider.Name())
	}
}

// Test getting non-existent provider
func TestGetNonExistentProvider(t *testing.T) {
	factory := NewFactory()

	_, err := factory.GetProvider("nonexistent")
	if err == nil {
		t.Error("Expected error for non-existent provider, got nil")
	}
}

// Test listing providers
func TestListProviders(t *testing.T) {
	factory := NewFactory()

	factory.Register("provider1", &MockProvider{
		name:         "provider1",
		providerType: ProviderTypeCloud,
		models:       []string{"model-1"},
	})

	factory.Register("provider2", &MockProvider{
		name:         "provider2",
		providerType: ProviderTypeLocal,
		models:       []string{"model-2"},
	})

	providers := factory.ListProviders()
	if len(providers) != 2 {
		t.Errorf("Expected 2 providers, got %d", len(providers))
	}
}

// Test unregistering a provider
func TestUnregisterProvider(t *testing.T) {
	factory := NewFactory()

	factory.Register("mock", &MockProvider{name: "mock"})

	if !factory.HasProvider("mock") {
		t.Error("Provider should be registered")
	}

	factory.Unregister("mock")

	if factory.HasProvider("mock") {
		t.Error("Provider should be unregistered")
	}
}

// Test clearing all providers
func TestClearProviders(t *testing.T) {
	factory := NewFactory()

	factory.Register("provider1", &MockProvider{name: "provider1"})
	factory.Register("provider2", &MockProvider{name: "provider2"})

	if factory.ProviderCount() != 2 {
		t.Errorf("Expected 2 providers before clear, got %d", factory.ProviderCount())
	}

	factory.Clear()

	if factory.ProviderCount() != 0 {
		t.Errorf("Expected 0 providers after clear, got %d", factory.ProviderCount())
	}
}

// Test case-insensitive provider names
func TestCaseInsensitiveProviderNames(t *testing.T) {
	factory := NewFactory()

	factory.Register("OpenAI", &MockProvider{name: "openai"})

	tests := []string{"openai", "OPENAI", "OpenAI", "oPeNaI"}
	for _, name := range tests {
		if !factory.HasProvider(name) {
			t.Errorf("Provider lookup should be case-insensitive, failed for '%s'", name)
		}

		provider, err := factory.GetProvider(name)
		if err != nil {
			t.Errorf("GetProvider should be case-insensitive, failed for '%s': %v", name, err)
		}
		if provider == nil {
			t.Errorf("Expected provider for '%s', got nil", name)
		}
	}
}

// Test GetSystemModelProvider
func TestGetSystemModelProvider(t *testing.T) {
	factory := NewFactory()
	factory.Register("openai", &MockProvider{
		name:         "openai",
		providerType: ProviderTypeCloud,
		models:       []string{"gpt-4o-mini", "gpt-4o"},
	})
	factory.Register("ollama", &MockProvider{
		name:         "ollama",
		providerType: ProviderTypeLocal,
		models:       []string{"llama3.2"},
	})

	tests := []struct {
		name         string
		providerName string
		modelName    string
		wantErr      bool
		errContains  string
	}{
		{
			name:         "valid openai provider and model",
			providerName: "openai",
			modelName:    "gpt-4o-mini",
			wantErr:      false,
		},
		{
			name:         "case insensitive model name",
			providerName: "openai",
			modelName:    "GPT-4O-MINI",
			wantErr:      false,
		},
		{
			name:         "empty provider",
			providerName: "",
			modelName:    "gpt-4o-mini",
			wantErr:      true,
			errContains:  "not configured",
		},
		{
			name:         "empty model",
			providerName: "openai",
			modelName:    "",
			wantErr:      true,
			errContains:  "not configured",
		},
		{
			name:         "both empty",
			providerName: "",
			modelName:    "",
			wantErr:      true,
			errContains:  "not configured",
		},
		{
			name:         "provider not available",
			providerName: "claude",
			modelName:    "claude-3-haiku",
			wantErr:      true,
			errContains:  "not available",
		},
		{
			name:         "model not available for non-ollama provider",
			providerName: "openai",
			modelName:    "unknown-model",
			wantErr:      true,
			errContains:  "not available",
		},
		{
			name:         "ollama allows any model name",
			providerName: "ollama",
			modelName:    "custom-model:latest",
			wantErr:      false, // Ollama should allow any model name
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := factory.GetSystemModelProvider(tt.providerName, tt.modelName)

			if tt.wantErr {
				if err == nil {
					t.Errorf("GetSystemModelProvider() expected error, got nil")
					return
				}
				if tt.errContains != "" && !containsString(err.Error(), tt.errContains) {
					t.Errorf("GetSystemModelProvider() error = %v, want error containing %q", err, tt.errContains)
				}
				return
			}

			if err != nil {
				t.Errorf("GetSystemModelProvider() unexpected error: %v", err)
				return
			}

			if result == nil {
				t.Error("GetSystemModelProvider() returned nil result")
				return
			}

			if result.Provider == nil {
				t.Error("GetSystemModelProvider() returned nil provider")
			}

			if result.Model == "" {
				t.Error("GetSystemModelProvider() returned empty model")
			}
		})
	}
}

// Test ErrSystemModelNotConfigured error type
func TestErrSystemModelNotConfigured(t *testing.T) {
	factory := NewFactory()

	_, err := factory.GetSystemModelProvider("", "")
	if err != ErrSystemModelNotConfigured {
		t.Errorf("Expected ErrSystemModelNotConfigured, got %v", err)
	}
}

// containsString helper for testing
func containsString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
