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
