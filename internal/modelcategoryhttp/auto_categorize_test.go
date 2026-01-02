package modelcategoryhttp

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
	"github.com/johnjallday/ori-agent/internal/store"
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

// setupAutoCategorizeHandler creates an AutoCategorizeHandler for testing
func setupAutoCategorizeHandler(t *testing.T, systemProvider, systemModel string, factory *llm.Factory) (*AutoCategorizeHandler, store.ModelCategoryStore) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "model_categories.json")

	categoryStore, err := store.NewFileModelCategoryStore(path)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	configManager := createTestConfigManager(t, systemProvider, systemModel)
	handler := NewAutoCategorizeHandler(categoryStore, factory, configManager)
	return handler, categoryStore
}

func TestAutoCategorizeHandler_CheckAvailability(t *testing.T) {
	tests := []struct {
		name             string
		setupFactory     func() *llm.Factory
		systemProvider   string
		systemModel      string
		hideCategories   bool
		expectedAvail    bool
		expectedSMConfig bool
		expectedHasCats  bool
		expectedStatus   int
	}{
		{
			name: "no system model configured",
			setupFactory: func() *llm.Factory {
				factory := llm.NewFactory()
				factory.Register("openai", &mockProvider{})
				return factory
			},
			systemProvider:   "",
			systemModel:      "",
			hideCategories:   false,
			expectedAvail:    false,
			expectedSMConfig: false,
			expectedHasCats:  true, // Default categories exist
			expectedStatus:   http.StatusOK,
		},
		{
			name: "system model configured with categories",
			setupFactory: func() *llm.Factory {
				factory := llm.NewFactory()
				factory.Register("openai", &mockProvider{})
				return factory
			},
			systemProvider:   "openai",
			systemModel:      "gpt-4o-mini",
			hideCategories:   false,
			expectedAvail:    true,
			expectedSMConfig: true,
			expectedHasCats:  true,
			expectedStatus:   http.StatusOK,
		},
		{
			name: "system model configured but all categories hidden",
			setupFactory: func() *llm.Factory {
				factory := llm.NewFactory()
				factory.Register("openai", &mockProvider{})
				return factory
			},
			systemProvider:   "openai",
			systemModel:      "gpt-4o-mini",
			hideCategories:   true,
			expectedAvail:    false, // No visible categories
			expectedSMConfig: true,
			expectedHasCats:  false,
			expectedStatus:   http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			factory := tt.setupFactory()
			handler, categoryStore := setupAutoCategorizeHandler(t, tt.systemProvider, tt.systemModel, factory)

			// Hide all categories if requested
			if tt.hideCategories {
				categories := categoryStore.GetCategories()
				for _, cat := range categories {
					_ = categoryStore.SetCategoryVisibility(cat.ID, true)
				}
			}

			req := httptest.NewRequest(http.MethodGet, "/api/models/auto-categorize/availability", nil)
			rr := httptest.NewRecorder()

			handler.CheckAvailabilityHandler(rr, req)

			if rr.Code != tt.expectedStatus {
				t.Errorf("Expected status %d, got %d", tt.expectedStatus, rr.Code)
			}

			var response AvailabilityResponse
			if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
				t.Fatalf("Failed to decode response: %v", err)
			}

			if response.Available != tt.expectedAvail {
				t.Errorf("Expected available=%v, got %v", tt.expectedAvail, response.Available)
			}

			if response.SystemModelConfigured != tt.expectedSMConfig {
				t.Errorf("Expected system_model_configured=%v, got %v", tt.expectedSMConfig, response.SystemModelConfigured)
			}

			if response.HasCategories != tt.expectedHasCats {
				t.Errorf("Expected has_categories=%v, got %v", tt.expectedHasCats, response.HasCategories)
			}

			if !tt.expectedAvail && response.Message == "" {
				t.Error("Expected message when not available")
			}
		})
	}
}

func TestAutoCategorizeHandler_CheckAvailability_WrongMethod(t *testing.T) {
	factory := llm.NewFactory()
	handler, _ := setupAutoCategorizeHandler(t, "", "", factory)

	req := httptest.NewRequest(http.MethodPost, "/api/models/auto-categorize/availability", nil)
	rr := httptest.NewRecorder()

	handler.CheckAvailabilityHandler(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected status %d, got %d", http.StatusMethodNotAllowed, rr.Code)
	}
}

func TestAutoCategorizeHandler_AutoCategorize_WrongMethod(t *testing.T) {
	factory := llm.NewFactory()
	handler, _ := setupAutoCategorizeHandler(t, "", "", factory)

	req := httptest.NewRequest(http.MethodGet, "/api/models/auto-categorize", nil)
	rr := httptest.NewRecorder()

	handler.AutoCategorizeHandler(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected status %d, got %d", http.StatusMethodNotAllowed, rr.Code)
	}
}

func TestAutoCategorizeHandler_AutoCategorize_EmptyModelIDs(t *testing.T) {
	factory := llm.NewFactory()
	factory.Register("openai", &mockProvider{})
	handler, _ := setupAutoCategorizeHandler(t, "openai", "gpt-4o-mini", factory)

	reqBody := AutoCategorizeRequest{
		ModelIDs: []string{},
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/models/auto-categorize", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.AutoCategorizeHandler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("Expected status %d, got %d. Body: %s", http.StatusBadRequest, rr.Code, rr.Body.String())
	}
}

func TestAutoCategorizeHandler_AutoCategorize_TooManyModels(t *testing.T) {
	factory := llm.NewFactory()
	factory.Register("openai", &mockProvider{})
	handler, _ := setupAutoCategorizeHandler(t, "openai", "gpt-4o-mini", factory)

	// Create 51 model IDs (over the limit of 50)
	modelIDs := make([]string, 51)
	for i := 0; i < 51; i++ {
		modelIDs[i] = "model-" + string(rune('a'+i%26))
	}

	reqBody := AutoCategorizeRequest{
		ModelIDs: modelIDs,
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/models/auto-categorize", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.AutoCategorizeHandler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("Expected status %d, got %d. Body: %s", http.StatusBadRequest, rr.Code, rr.Body.String())
	}
}

func TestAutoCategorizeHandler_AutoCategorize_NoSystemModel(t *testing.T) {
	factory := llm.NewFactory()
	factory.Register("openai", &mockProvider{})
	handler, _ := setupAutoCategorizeHandler(t, "", "", factory) // No system model

	reqBody := AutoCategorizeRequest{
		ModelIDs: []string{"gpt-4o", "claude-3-5-sonnet"},
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/models/auto-categorize", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.AutoCategorizeHandler(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected status %d, got %d. Body: %s", http.StatusServiceUnavailable, rr.Code, rr.Body.String())
	}
}

func TestAutoCategorizeHandler_AutoCategorize_NoCategories(t *testing.T) {
	factory := llm.NewFactory()
	factory.Register("openai", &mockProvider{})
	handler, categoryStore := setupAutoCategorizeHandler(t, "openai", "gpt-4o-mini", factory)

	// Hide all categories
	categories := categoryStore.GetCategories()
	for _, cat := range categories {
		_ = categoryStore.SetCategoryVisibility(cat.ID, true)
	}

	reqBody := AutoCategorizeRequest{
		ModelIDs: []string{"gpt-4o"},
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/models/auto-categorize", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.AutoCategorizeHandler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("Expected status %d, got %d. Body: %s", http.StatusBadRequest, rr.Code, rr.Body.String())
	}
}

func TestAutoCategorizeHandler_AutoCategorize_Success(t *testing.T) {
	factory := llm.NewFactory()
	mockProv := &mockCategorizationProvider{}
	factory.Register("openai", mockProv)
	handler, _ := setupAutoCategorizeHandler(t, "openai", "gpt-4o-mini", factory)

	reqBody := AutoCategorizeRequest{
		ModelIDs: []string{"gpt-4o", "gpt-4o-mini"},
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/models/auto-categorize", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.AutoCategorizeHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d. Body: %s", http.StatusOK, rr.Code, rr.Body.String())
	}

	var response AutoCategorizeResponse
	if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if len(response.Suggestions) != 2 {
		t.Errorf("Expected 2 suggestions, got %d", len(response.Suggestions))
	}

	// Verify suggestions have expected fields
	for _, s := range response.Suggestions {
		if s.ModelID == "" {
			t.Error("Expected non-empty model_id")
		}
		if s.Confidence < 0 || s.Confidence > 1 {
			t.Errorf("Expected confidence between 0 and 1, got %v", s.Confidence)
		}
	}
}

// mockProvider is a minimal mock LLM provider for testing
type mockProvider struct{}

func (m *mockProvider) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{
		Content: `{"error": "mock provider - not for categorization"}`,
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

// mockCategorizationProvider returns valid categorization responses
type mockCategorizationProvider struct{}

func (m *mockCategorizationProvider) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	// Return a mock categorization response
	return &llm.ChatResponse{
		Content: `[
			{
				"model_id": "gpt-4o",
				"category_id": "cat_default_general_purpose",
				"confidence": 0.85,
				"reasoning": "GPT-4o is a balanced, general-purpose model"
			},
			{
				"model_id": "gpt-4o-mini",
				"category_id": "cat_default_tool_calling",
				"confidence": 0.9,
				"reasoning": "Mini models are optimized for tool calling"
			}
		]`,
	}, nil
}

func (m *mockCategorizationProvider) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	return nil, nil
}

func (m *mockCategorizationProvider) Name() string {
	return "openai"
}

func (m *mockCategorizationProvider) Type() llm.ProviderType {
	return llm.ProviderTypeCloud
}

func (m *mockCategorizationProvider) Capabilities() llm.ProviderCapabilities {
	return llm.ProviderCapabilities{
		SupportsTools:        true,
		SupportsStreaming:    true,
		SupportsSystemPrompt: true,
		SupportsTemperature:  true,
		RequiresAPIKey:       false,
	}
}

func (m *mockCategorizationProvider) ValidateConfig(config llm.ProviderConfig) error {
	return nil
}

func (m *mockCategorizationProvider) DefaultModels() []string {
	return []string{"gpt-4o-mini", "gpt-4o"}
}
