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
	"github.com/johnjallday/ori-agent/internal/modelinfo"
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

type mockClaudeCodeProvider struct {
	mockProvider
}

type mockCodexProvider struct {
	mockProvider
}

func (m *mockCodexProvider) Capabilities() llm.ProviderCapabilities {
	return llm.ProviderCapabilities{
		SupportsTools:  false,
		RequiresAPIKey: false,
	}
}

func (m *mockCodexProvider) DefaultModels() []string {
	return []string{"gpt-5.3-codex"}
}

func (m *mockClaudeCodeProvider) Name() string {
	return "claude_code"
}

func (m *mockClaudeCodeProvider) Capabilities() llm.ProviderCapabilities {
	return llm.ProviderCapabilities{
		SupportsTools:  false,
		RequiresAPIKey: false,
	}
}

func (m *mockClaudeCodeProvider) DefaultModels() []string {
	return []string{"opus", "sonnet", "haiku"}
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

	var resp struct {
		Provider     string                 `json:"provider"`
		Available    bool                   `json:"available"`
		Models       []string               `json:"models"`
		ModelOptions []AvailableModelOption `json:"model_options"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if !resp.Available {
		t.Error("Expected available to be true")
	}
	if len(resp.Models) != 2 {
		t.Errorf("Expected 2 models, got %d", len(resp.Models))
	}
	if len(resp.ModelOptions) != 2 {
		t.Errorf("Expected 2 model options, got %d", len(resp.ModelOptions))
	}
	if resp.ModelOptions[0].ID != "test-model-1" || resp.ModelOptions[0].Label != "test-model-1" {
		t.Errorf("Expected first model option to mirror model ID, got %+v", resp.ModelOptions[0])
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

	var resp struct {
		Provider     string                 `json:"provider"`
		Available    bool                   `json:"available"`
		Models       []string               `json:"models"`
		ModelOptions []AvailableModelOption `json:"model_options"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp.Available {
		t.Error("Expected available to be false")
	}
	if len(resp.ModelOptions) != 0 {
		t.Errorf("Expected no model options, got %d", len(resp.ModelOptions))
	}
}

func TestAvailableModelsHandler_ClaudeCodeModelOptions(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "settings.json")
	configManager := config.NewManager(tmpFile)
	_ = configManager.Load()

	llmFactory := llm.NewFactory()
	llmFactory.Register("claude_code", &mockClaudeCodeProvider{})

	handler := NewHandler(nil, configManager, nil, llmFactory)

	req := httptest.NewRequest(http.MethodGet, "/api/settings/available-models?provider=claude_code", nil)
	rec := httptest.NewRecorder()
	handler.AvailableModelsHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", rec.Code)
	}

	var resp struct {
		Provider     string                 `json:"provider"`
		Available    bool                   `json:"available"`
		Models       []string               `json:"models"`
		ModelOptions []AvailableModelOption `json:"model_options"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if !resp.Available {
		t.Fatal("Expected available to be true")
	}
	if len(resp.ModelOptions) != 3 {
		t.Fatalf("Expected 3 model options, got %d", len(resp.ModelOptions))
	}

	optionsByID := make(map[string]AvailableModelOption, len(resp.ModelOptions))
	for _, option := range resp.ModelOptions {
		optionsByID[option.ID] = option
	}

	sonnet, ok := optionsByID["sonnet"]
	if !ok {
		t.Fatal("Expected sonnet model option")
	}
	if sonnet.Label != "Sonnet" {
		t.Errorf("Expected sonnet label 'Sonnet', got %q", sonnet.Label)
	}
	if sonnet.Description == "" {
		t.Error("Expected sonnet description to be populated")
	}

	haiku, ok := optionsByID["haiku"]
	if !ok {
		t.Fatal("Expected haiku model option")
	}
	if !haiku.Recommended {
		t.Error("Expected haiku to be recommended")
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

func TestSessionSettingsHandler_Get(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "settings.json")
	configManager := config.NewManager(tmpFile)
	_ = configManager.Load()

	handler := NewHandler(nil, configManager, nil, llm.NewFactory())

	req := httptest.NewRequest(http.MethodGet, "/api/settings/session", nil)
	rec := httptest.NewRecorder()
	handler.SessionSettingsHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", rec.Code)
	}

	var resp struct {
		SessionCleanupEnabled bool `json:"session_cleanup_enabled"`
		SessionCleanupDays    int  `json:"session_cleanup_days"`
		SessionMaxCount       int  `json:"session_max_count"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if !resp.SessionCleanupEnabled {
		t.Error("Expected session_cleanup_enabled=true by default")
	}
	if resp.SessionCleanupDays != 30 {
		t.Errorf("Expected session_cleanup_days=30, got %d", resp.SessionCleanupDays)
	}
	if resp.SessionMaxCount != 1000 {
		t.Errorf("Expected session_max_count=1000, got %d", resp.SessionMaxCount)
	}
}

func TestSessionSettingsHandler_Post(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "settings.json")
	configManager := config.NewManager(tmpFile)
	_ = configManager.Load()

	handler := NewHandler(nil, configManager, nil, llm.NewFactory())

	reqBody := map[string]any{
		"session_cleanup_enabled": false,
		"session_cleanup_days":    14,
		"session_max_count":       250,
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/api/settings/session", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.SessionSettingsHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	configManager2 := config.NewManager(tmpFile)
	_ = configManager2.Load()
	if configManager2.GetSessionCleanupEnabled() {
		t.Error("Expected session cleanup to be disabled after update")
	}
	if got := configManager2.GetSessionCleanupDays(); got != 14 {
		t.Errorf("Expected session_cleanup_days=14, got %d", got)
	}
	if got := configManager2.GetSessionMaxCount(); got != 250 {
		t.Errorf("Expected session_max_count=250, got %d", got)
	}
}

func TestCategorizeModel_Codex(t *testing.T) {
	tests := []struct {
		name     string
		model    string
		expected string
	}{
		{name: "codex nano is tool-calling", model: "gpt-5-codex-nano", expected: "tool-calling"},
		{name: "codex mini is general", model: "gpt-5.1-codex-mini", expected: "general"},
		{name: "codex standard is research", model: "gpt-5.3-codex", expected: "research"},
		{name: "codex max is research", model: "gpt-5.1-codex-max", expected: "research"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := categorizeModel("codex", tt.model)
			if got != tt.expected {
				t.Fatalf("categorizeModel(\"codex\", %q) = %q, want %q", tt.model, got, tt.expected)
			}
		})
	}
}

func TestGetModelCategories_Codex(t *testing.T) {
	tests := []struct {
		name     string
		model    string
		expected []string
	}{
		{name: "codex nano has tool-calling and general", model: "gpt-5-codex-nano", expected: []string{"tool-calling", "general"}},
		{name: "codex mini has tool-calling, general, workspace-manager, and orchestration", model: "gpt-5.1-codex-mini", expected: []string{"tool-calling", "general", "workspace-manager", "orchestration"}},
		{name: "codex standard has research, workspace-manager, and orchestration", model: "gpt-5.3-codex", expected: []string{"research", "workspace-manager", "orchestration"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getModelCategories("codex", tt.model)
			if len(got) != len(tt.expected) {
				t.Fatalf("getModelCategories(\"codex\", %q) = %v, want %v", tt.model, got, tt.expected)
			}
			for i := range tt.expected {
				if got[i] != tt.expected[i] {
					t.Fatalf("getModelCategories(\"codex\", %q) = %v, want %v", tt.model, got, tt.expected)
				}
			}
		})
	}
}

func TestProvidersHandler_CodexPricingHidden(t *testing.T) {
	if modelinfo.GetPricing("gpt-5.3-codex") == nil {
		t.Skip("codex pricing entry missing from model catalog")
	}

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "settings.json")
	configManager := config.NewManager(tmpFile)
	_ = configManager.Load()

	llmFactory := llm.NewFactory()
	llmFactory.Register("codex", &mockCodexProvider{})

	handler := NewHandler(nil, configManager, nil, llmFactory)

	req := httptest.NewRequest(http.MethodGet, "/api/providers", nil)
	rec := httptest.NewRecorder()
	handler.ProvidersHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", rec.Code)
	}

	var resp struct {
		Providers []ProviderInfo `json:"providers"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	var codex *ProviderInfo
	for i := range resp.Providers {
		if resp.Providers[i].Name == "codex" {
			codex = &resp.Providers[i]
			break
		}
	}
	if codex == nil {
		t.Fatal("Expected codex provider in response")
	}
	if len(codex.Models) == 0 {
		t.Fatal("Expected codex models in response")
	}

	for _, model := range codex.Models {
		if model.Pricing != "" {
			t.Fatalf("Expected empty pricing for codex model %q, got %q", model.Value, model.Pricing)
		}
	}
}

// Ensure temp directory cleanup
func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
