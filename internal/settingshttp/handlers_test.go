package settingshttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/llm"
)

// mockProvider implements llm.Provider for testing
type mockProvider struct{}

func (m *mockProvider) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{
		Content: "mock response",
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
		SupportsTools:  true,
		RequiresAPIKey: true,
	}
}

func (m *mockProvider) ValidateConfig(config llm.ProviderConfig) error {
	return nil
}

func (m *mockProvider) DefaultModels() []string {
	return []string{"test-model-1", "test-model-2"}
}

func TestSystemModelHandler_Get(t *testing.T) {
	// Setup
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "settings.json")
	configManager := config.NewManager(tmpFile)
	_ = configManager.Load()

	llmFactory := llm.NewFactory()
	handler := NewHandler(nil, configManager, nil, llmFactory)

	// Test GET when not configured
	req := httptest.NewRequest(http.MethodGet, "/api/settings/system-model", nil)
	rec := httptest.NewRecorder()
	handler.SystemModelHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
	}

	var resp SystemModelResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp.Configured {
		t.Error("Expected Configured to be false")
	}
	if resp.Provider != "" || resp.Model != "" {
		t.Errorf("Expected empty provider/model, got %q/%q", resp.Provider, resp.Model)
	}
}

func TestSystemModelHandler_Post(t *testing.T) {
	// Setup
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "settings.json")
	configManager := config.NewManager(tmpFile)
	_ = configManager.Load()

	llmFactory := llm.NewFactory()
	llmFactory.Register("openai", &mockProvider{})

	handler := NewHandler(nil, configManager, nil, llmFactory)

	// Test POST with valid system model
	reqBody := SystemModelRequest{
		Provider: "openai",
		Model:    "test-model-1",
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/api/settings/system-model", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.SystemModelHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp SystemModelResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if !resp.Configured {
		t.Error("Expected Configured to be true")
	}
	if resp.Provider != "openai" {
		t.Errorf("Expected provider 'openai', got %q", resp.Provider)
	}
	if resp.Model != "test-model-1" {
		t.Errorf("Expected model 'test-model-1', got %q", resp.Model)
	}

	// Verify it persisted
	configManager2 := config.NewManager(tmpFile)
	_ = configManager2.Load()
	if !configManager2.IsSystemModelConfigured() {
		t.Error("Expected system model to be persisted")
	}
}

func TestSystemModelHandler_Post_InvalidProvider(t *testing.T) {
	// Setup
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "settings.json")
	configManager := config.NewManager(tmpFile)
	_ = configManager.Load()

	llmFactory := llm.NewFactory()
	// Don't register the provider

	handler := NewHandler(nil, configManager, nil, llmFactory)

	// Test POST with unavailable provider
	reqBody := SystemModelRequest{
		Provider: "openai",
		Model:    "gpt-4o-mini",
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/api/settings/system-model", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.SystemModelHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", rec.Code)
	}
}

func TestSystemModelHandler_Post_Clear(t *testing.T) {
	// Setup
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "settings.json")
	configManager := config.NewManager(tmpFile)
	_ = configManager.Load()

	llmFactory := llm.NewFactory()
	llmFactory.Register("openai", &mockProvider{})

	handler := NewHandler(nil, configManager, nil, llmFactory)

	// First set a system model
	_ = configManager.SetSystemModel("openai", "gpt-4o-mini")
	_ = configManager.Save()

	// Test POST with empty values to clear
	reqBody := SystemModelRequest{
		Provider: "",
		Model:    "",
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/api/settings/system-model", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.SystemModelHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp SystemModelResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp.Configured {
		t.Error("Expected Configured to be false after clearing")
	}
}

func TestSystemModelHandler_Post_CodexReasoning(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "settings.json")
	configManager := config.NewManager(tmpFile)
	_ = configManager.Load()

	llmFactory := llm.NewFactory()
	llmFactory.Register("codex", &mockProvider{})

	handler := NewHandler(nil, configManager, nil, llmFactory)

	reqBody := SystemModelRequest{
		Provider:        "codex",
		Model:           "gpt-5.3-codex",
		ReasoningEffort: "xhigh",
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/api/settings/system-model", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.SystemModelHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp SystemModelResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if resp.ReasoningEffort != "xhigh" {
		t.Fatalf("Expected reasoning_effort=xhigh, got %q", resp.ReasoningEffort)
	}
	if got := configManager.GetSystemReasoningEffort(); got != "xhigh" {
		t.Fatalf("Expected config reasoning=xhigh, got %q", got)
	}
}

func TestSystemModelHandler_Post_InvalidCodexReasoning(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "settings.json")
	configManager := config.NewManager(tmpFile)
	_ = configManager.Load()

	llmFactory := llm.NewFactory()
	llmFactory.Register("codex", &mockProvider{})

	handler := NewHandler(nil, configManager, nil, llmFactory)

	reqBody := SystemModelRequest{
		Provider:        "codex",
		Model:           "gpt-5.3-codex",
		ReasoningEffort: "ultra",
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/api/settings/system-model", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.SystemModelHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAvailableModelsHandler(t *testing.T) {
	// Setup
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "settings.json")
	configManager := config.NewManager(tmpFile)
	_ = configManager.Load()

	llmFactory := llm.NewFactory()
	llmFactory.Register("openai", &mockProvider{})

	handler := NewHandler(nil, configManager, nil, llmFactory)

	// Test with available provider
	req := httptest.NewRequest(http.MethodGet, "/api/settings/available-models?provider=openai", nil)
	rec := httptest.NewRecorder()
	handler.AvailableModelsHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp["available"] != true {
		t.Error("Expected available to be true")
	}
	models, ok := resp["models"].([]interface{})
	if !ok {
		t.Fatal("Expected models to be an array")
	}
	if len(models) != 2 {
		t.Errorf("Expected 2 models, got %d", len(models))
	}
}

func TestAvailableModelsHandler_UnavailableProvider(t *testing.T) {
	// Setup
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "settings.json")
	configManager := config.NewManager(tmpFile)
	_ = configManager.Load()

	llmFactory := llm.NewFactory()
	// Don't register the provider

	handler := NewHandler(nil, configManager, nil, llmFactory)

	req := httptest.NewRequest(http.MethodGet, "/api/settings/available-models?provider=openai", nil)
	rec := httptest.NewRecorder()
	handler.AvailableModelsHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp["available"] != false {
		t.Error("Expected available to be false")
	}
}

func TestAvailableModelsHandler_MissingProvider(t *testing.T) {
	// Setup
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "settings.json")
	configManager := config.NewManager(tmpFile)
	_ = configManager.Load()

	llmFactory := llm.NewFactory()
	handler := NewHandler(nil, configManager, nil, llmFactory)

	req := httptest.NewRequest(http.MethodGet, "/api/settings/available-models", nil)
	rec := httptest.NewRecorder()
	handler.AvailableModelsHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", rec.Code)
	}
}

func TestExternalAgentsSettingsHandler_CodexToggleDoesNotUnregisterProvider(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "settings.json")
	configManager := config.NewManager(tmpFile)
	_ = configManager.Load()

	llmFactory := llm.NewFactory()
	llmFactory.Register("codex", &mockProvider{})

	handler := NewHandler(nil, configManager, nil, llmFactory)

	req := httptest.NewRequest(http.MethodPost, "/api/settings/external-agents", bytes.NewReader([]byte(`{"codex_enabled":false}`)))
	rec := httptest.NewRecorder()
	handler.ExternalAgentsSettingsHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	if configManager.GetExternalAgentsCodexEnabled() {
		t.Error("Expected external Codex agents setting to be false")
	}

	if _, err := llmFactory.GetProvider("codex"); err != nil {
		t.Fatalf("Expected codex provider to remain registered, got error: %v", err)
	}
}

// Ensure temp directory cleanup
func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
