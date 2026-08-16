package settingshttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/client"
	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/modelinfo"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/vault"
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

type mockLocalProvider struct {
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

func (m *mockLocalProvider) Type() llm.ProviderType {
	return llm.ProviderTypeLocal
}

func (m *mockLocalProvider) Capabilities() llm.ProviderCapabilities {
	return llm.ProviderCapabilities{
		SupportsTools:          true,
		RequiresAPIKey:         false,
		SupportsCustomEndpoint: true,
	}
}

func (m *mockLocalProvider) DefaultModels() []string {
	return []string{"openai/gpt-oss-20b"}
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

func TestSettingsHandler_GetDoesNotExposeAPIKey(t *testing.T) {
	tmpDir := t.TempDir()
	st, err := store.NewFileStore(filepath.Join(tmpDir, "agents.json"), types.Settings{})
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	if err := st.CreateAgent("Ori", nil); err != nil {
		t.Fatalf("CreateAgent() error = %v", err)
	}

	const secret = "sk-test-per-agent-0123456789abcdef"
	if err := st.UpdateAgent("Ori", func(ag *agent.Agent) error {
		ag.Settings.APIKey = secret
		ag.Settings.Model = "gpt-5"
		return nil
	}); err != nil {
		t.Fatalf("UpdateAgent() error = %v", err)
	}

	handler := NewHandler(st, nil, nil, llm.NewFactory())

	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rec := httptest.NewRecorder()
	handler.SettingsHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// The raw key must never appear anywhere in the response body.
	if strings.Contains(rec.Body.String(), secret) {
		t.Fatalf("GET /api/settings leaked the raw API key: %s", rec.Body.String())
	}

	var resp struct {
		Settings     types.Settings `json:"Settings"`
		HasAPIKey    bool           `json:"has_api_key"`
		MaskedAPIKey string         `json:"masked_api_key"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp.Settings.APIKey != "" {
		t.Errorf("Settings.api_key should be empty in the response, got %q", resp.Settings.APIKey)
	}
	if !resp.HasAPIKey {
		t.Error("has_api_key should be true when a key is configured")
	}
	if !strings.Contains(resp.MaskedAPIKey, "***") {
		t.Errorf("masked_api_key should be redacted, got %q", resp.MaskedAPIKey)
	}
	// Non-secret settings must still round-trip.
	if resp.Settings.Model != "gpt-5" {
		t.Errorf("Settings.model = %q, want gpt-5", resp.Settings.Model)
	}
}

func TestAPIKeyHandler_SecretStoreWritesDoNotPersistPlaintext(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "settings.json")
	configManager := config.NewManagerWithSecretStore(tmpFile, vault.NewMemorySecretStore())
	_ = configManager.Load()

	llmFactory := llm.NewFactory()
	handler := NewHandler(nil, configManager, client.NewFactory(""), llmFactory)

	body := []byte(`{
		"openai_api_key":"sk-test1234567890abcdefghijklmnopqrstuvwxyz",
		"anthropic_api_key":"sk-ant-test1234567890abcdefghijklmnopqrstuvwxyz",
		"gemini_api_key":"gemini-secret"
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/settings/api-key", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler.APIKeyHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	raw, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	text := string(raw)
	if strings.Contains(text, "sk-test1234567890abcdefghijklmnopqrstuvwxyz") || strings.Contains(text, "sk-ant-test1234567890abcdefghijklmnopqrstuvwxyz") || strings.Contains(text, "gemini-secret") {
		t.Fatalf("settings.json should not contain plaintext API keys: %s", text)
	}

	if got := configManager.GetAPIKey(); got != "sk-test1234567890abcdefghijklmnopqrstuvwxyz" {
		t.Fatalf("GetAPIKey() = %q", got)
	}
	if got := configManager.GetAnthropicAPIKey(); got != "sk-ant-test1234567890abcdefghijklmnopqrstuvwxyz" {
		t.Fatalf("GetAnthropicAPIKey() = %q", got)
	}
	if got := configManager.GetGeminiAPIKey(); got != "gemini-secret" {
		t.Fatalf("GetGeminiAPIKey() = %q", got)
	}
}

func TestUtilitySettingsHandler_SecretStoreWritesDoNotPersistBravePlaintext(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "settings.json")
	configManager := config.NewManagerWithSecretStore(tmpFile, vault.NewMemorySecretStore())
	_ = configManager.Load()

	handler := NewHandler(nil, configManager, client.NewFactory(""), llm.NewFactory())
	body := []byte(`{"search_provider":"brave","brave_api_key":"brave-secret-token"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/settings/utility", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler.UtilitySettingsHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	raw, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.Contains(string(raw), "brave-secret-token") {
		t.Fatalf("settings.json should not contain plaintext Brave key: %s", string(raw))
	}

	cfg := configManager.Get()
	if cfg.Utility.BraveAPIKey != "brave-secret-token" {
		t.Fatalf("effective BraveAPIKey = %q", cfg.Utility.BraveAPIKey)
	}
}

func TestWorkspaceRootSettingsHandler_Get_EnvironmentFallback(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "settings.json")
	configManager := config.NewManager(tmpFile)
	_ = configManager.Load()

	envRoot := filepath.Join(tmpDir, "env-workspaces")
	t.Setenv("WORKSPACE_DIR", envRoot)

	handler := NewHandler(nil, configManager, nil, llm.NewFactory())

	req := httptest.NewRequest(http.MethodGet, "/api/settings/workspace-root", nil)
	rec := httptest.NewRecorder()
	handler.WorkspaceRootSettingsHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", rec.Code)
	}

	var resp WorkspaceRootResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp.WorkspaceRoot != "" {
		t.Fatalf("Expected no configured workspace_root, got %q", resp.WorkspaceRoot)
	}
	if resp.Source != "environment" {
		t.Fatalf("Expected source environment, got %q", resp.Source)
	}
	if resp.EffectiveWorkspaceRoot != envRoot {
		t.Fatalf("Expected effective root %q, got %q", envRoot, resp.EffectiveWorkspaceRoot)
	}
	if !resp.Confirmed {
		t.Fatal("WORKSPACE_DIR is an explicit operator choice and must be treated as confirmed")
	}
}

func TestWorkspaceRootSettingsHandler_Get_NewInstallRequiresConfirmation(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("WORKSPACE_DIR", "")
	configManager := config.NewManager(filepath.Join(tmpDir, "settings.json"))
	if err := configManager.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	handler := NewHandler(nil, configManager, nil, llm.NewFactory())
	req := httptest.NewRequest(http.MethodGet, "/api/settings/workspace-root", nil)
	rec := httptest.NewRecorder()
	handler.WorkspaceRootSettingsHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", rec.Code)
	}
	var resp WorkspaceRootResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Confirmed {
		t.Fatal("new install must require workspace-root confirmation")
	}
	if resp.Source != "unconfirmed" {
		t.Fatalf("source = %q, want unconfirmed", resp.Source)
	}
	if resp.EffectiveWorkspaceRoot != "" {
		t.Fatalf("effective root = %q, want empty before consent", resp.EffectiveWorkspaceRoot)
	}
	if resp.DefaultWorkspaceRoot == "" {
		t.Fatal("response should still offer the built-in path as a suggestion")
	}
}

func TestWorkspaceRootSettingsHandler_Post(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "settings.json")
	configManager := config.NewManager(tmpFile)
	_ = configManager.Load()

	handler := NewHandler(nil, configManager, nil, llm.NewFactory())

	customRoot := filepath.Join(tmpDir, "custom-workspaces")
	body, _ := json.Marshal(map[string]string{
		"workspace_root": customRoot,
	})

	req := httptest.NewRequest(http.MethodPost, "/api/settings/workspace-root", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.WorkspaceRootSettingsHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Success                bool   `json:"success"`
		WorkspaceRoot          string `json:"workspace_root"`
		EffectiveWorkspaceRoot string `json:"effective_workspace_root"`
		Source                 string `json:"source"`
		Confirmed              bool   `json:"confirmed"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if !resp.Success {
		t.Fatal("Expected success=true")
	}
	if resp.WorkspaceRoot != customRoot {
		t.Fatalf("Expected workspace_root %q, got %q", customRoot, resp.WorkspaceRoot)
	}
	if resp.EffectiveWorkspaceRoot != customRoot {
		t.Fatalf("Expected effective root %q, got %q", customRoot, resp.EffectiveWorkspaceRoot)
	}
	if resp.Source != "settings" {
		t.Fatalf("Expected source settings, got %q", resp.Source)
	}
	if !resp.Confirmed {
		t.Fatal("saving a workspace directory must confirm it")
	}
	if _, err := os.Stat(customRoot); err != nil {
		t.Fatalf("Expected workspace root directory to exist: %v", err)
	}

	configManager2 := config.NewManager(tmpFile)
	_ = configManager2.Load()
	if got := configManager2.GetWorkspaceRoot(); got != customRoot {
		t.Fatalf("Expected persisted workspace root %q, got %q", customRoot, got)
	}
}

func TestWorkspaceRootSettingsHandler_Post_ClearFallsBackToDefault(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "settings.json")
	configManager := config.NewManager(tmpFile)
	_ = configManager.Load()
	if err := configManager.SetWorkspaceRoot(filepath.Join(tmpDir, "custom-workspaces")); err != nil {
		t.Fatalf("SetWorkspaceRoot: %v", err)
	}
	if err := configManager.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	handler := NewHandler(nil, configManager, nil, llm.NewFactory())

	req := httptest.NewRequest(http.MethodPost, "/api/settings/workspace-root", bytes.NewReader([]byte(`{"workspace_root":""}`)))
	rec := httptest.NewRecorder()
	handler.WorkspaceRootSettingsHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		WorkspaceRoot          string `json:"workspace_root"`
		EffectiveWorkspaceRoot string `json:"effective_workspace_root"`
		Source                 string `json:"source"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp.WorkspaceRoot != "" {
		t.Fatalf("Expected cleared workspace_root, got %q", resp.WorkspaceRoot)
	}
	if resp.Source != "default" {
		t.Fatalf("Expected source default, got %q", resp.Source)
	}
	if resp.EffectiveWorkspaceRoot != config.DefaultWorkspaceRoot() {
		t.Fatalf("Expected default effective root %q, got %q", config.DefaultWorkspaceRoot(), resp.EffectiveWorkspaceRoot)
	}
}

// workspaceRootPostResponse is the full POST payload including the refresh
// block added so callers can report what the live application changed.
type workspaceRootPostResponse struct {
	Success                bool                 `json:"success"`
	WorkspaceRoot          string               `json:"workspace_root"`
	EffectiveWorkspaceRoot string               `json:"effective_workspace_root"`
	Source                 string               `json:"source"`
	Confirmed              bool                 `json:"confirmed"`
	Refresh                WorkspaceRootRefresh `json:"refresh"`
}

func postWorkspaceRoot(t *testing.T, handler *Handler, root string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]string{"workspace_root": root})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/settings/workspace-root", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.WorkspaceRootSettingsHandler(rec, req)
	return rec
}

func TestWorkspaceRootSettingsHandler_Post_AppliesRootToRunningProcess(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("WORKSPACE_DIR", "")
	configManager := config.NewManager(filepath.Join(tmpDir, "settings.json"))
	_ = configManager.Load()

	handler := NewHandler(nil, configManager, nil, llm.NewFactory())

	var appliedRoots []string
	handler.SetWorkspaceRootUpdater(func(root string) (WorkspaceRootRefresh, error) {
		appliedRoots = append(appliedRoots, root)
		return WorkspaceRootRefresh{
			Imported:   2,
			Reparented: 1,
			Orphaned:   3,
			Restored:   4,
			Warnings:   []string{"Failed to load Notes"},
		}, nil
	})

	customRoot := filepath.Join(tmpDir, "custom-workspaces")
	rec := postWorkspaceRoot(t, handler, customRoot)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	if len(appliedRoots) != 1 {
		t.Fatalf("expected the updater to run exactly once, got %d calls", len(appliedRoots))
	}
	if appliedRoots[0] != customRoot {
		t.Fatalf("updater got root %q, want the effective root %q", appliedRoots[0], customRoot)
	}

	var resp workspaceRootPostResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Success || resp.EffectiveWorkspaceRoot != customRoot {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if resp.Refresh.Imported != 2 || resp.Refresh.Reparented != 1 || resp.Refresh.Orphaned != 3 || resp.Refresh.Restored != 4 {
		t.Fatalf("refresh counts = %+v, want imported 2 / reparented 1 / orphaned 3 / restored 4", resp.Refresh)
	}
	if len(resp.Refresh.Warnings) != 1 || resp.Refresh.Warnings[0] != "Failed to load Notes" {
		t.Fatalf("refresh warnings = %v", resp.Refresh.Warnings)
	}

	// The directory is still persisted, exactly as before.
	reloaded := config.NewManager(filepath.Join(tmpDir, "settings.json"))
	_ = reloaded.Load()
	if got := reloaded.GetWorkspaceRoot(); got != customRoot {
		t.Fatalf("persisted workspace root = %q, want %q", got, customRoot)
	}
}

func TestWorkspaceRootSettingsHandler_Post_ClearAppliesEffectiveRoot(t *testing.T) {
	tmpDir := t.TempDir()
	envRoot := filepath.Join(tmpDir, "env-workspaces")
	t.Setenv("WORKSPACE_DIR", envRoot)
	configManager := config.NewManager(filepath.Join(tmpDir, "settings.json"))
	_ = configManager.Load()
	if err := configManager.SetWorkspaceRoot(filepath.Join(tmpDir, "custom-workspaces")); err != nil {
		t.Fatalf("SetWorkspaceRoot: %v", err)
	}

	handler := NewHandler(nil, configManager, nil, llm.NewFactory())

	var appliedRoot string
	handler.SetWorkspaceRootUpdater(func(root string) (WorkspaceRootRefresh, error) {
		appliedRoot = root
		return WorkspaceRootRefresh{Imported: 1}, nil
	})

	rec := postWorkspaceRoot(t, handler, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	// Clearing a custom root must apply the effective root, not an empty string.
	if appliedRoot != envRoot {
		t.Fatalf("updater got root %q, want the effective environment root %q", appliedRoot, envRoot)
	}

	var resp workspaceRootPostResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.WorkspaceRoot != "" || resp.EffectiveWorkspaceRoot != envRoot {
		t.Fatalf("unexpected cleared response: %+v", resp)
	}
	if resp.Refresh.Imported != 1 {
		t.Fatalf("refresh = %+v, want the cleared root's refresh result", resp.Refresh)
	}
}

func TestWorkspaceRootSettingsHandler_Post_UpdaterFailureIsNotReportedAsSuccess(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("WORKSPACE_DIR", "")
	configManager := config.NewManager(filepath.Join(tmpDir, "settings.json"))
	_ = configManager.Load()

	handler := NewHandler(nil, configManager, nil, llm.NewFactory())
	handler.SetWorkspaceRootUpdater(func(string) (WorkspaceRootRefresh, error) {
		return WorkspaceRootRefresh{Imported: 9}, errors.New("index is locked")
	})

	rec := postWorkspaceRoot(t, handler, filepath.Join(tmpDir, "custom-workspaces"))
	if rec.Code == http.StatusOK {
		t.Fatalf("expected a non-success status when the root cannot be applied, got 200: %s", rec.Body.String())
	}

	body := rec.Body.String()
	var resp map[string]any
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	if success, ok := resp["success"].(bool); ok && success {
		t.Fatal("a failed live application must not report success")
	}
	if _, ok := resp["refresh"]; ok {
		t.Fatalf("a failed live application must not report refresh counts: %v", resp)
	}
	if !strings.Contains(body, "index is locked") {
		t.Fatalf("expected the underlying failure to be surfaced, got %s", body)
	}
}

func TestWorkspaceRootSettingsHandler_Post_WithoutUpdaterStillSucceeds(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("WORKSPACE_DIR", "")
	configManager := config.NewManager(filepath.Join(tmpDir, "settings.json"))
	_ = configManager.Load()

	// No updater wired: focused tests and builds without a folder store must
	// still be able to save a directory.
	handler := NewHandler(nil, configManager, nil, llm.NewFactory())

	customRoot := filepath.Join(tmpDir, "custom-workspaces")
	rec := postWorkspaceRoot(t, handler, customRoot)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var resp workspaceRootPostResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Success {
		t.Fatal("expected success without an updater")
	}
	if resp.Refresh.Imported != 0 || resp.Refresh.Orphaned != 0 || resp.Refresh.Restored != 0 || resp.Refresh.Reparented != 0 {
		t.Fatalf("expected a zeroed refresh without an updater, got %+v", resp.Refresh)
	}
	if resp.Refresh.Warnings == nil {
		t.Fatal("warnings should serialize as an empty list, not null")
	}
}

func TestVaultRootSettingsHandler_Get_EnvironmentFallback(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "settings.json")
	configManager := config.NewManager(tmpFile)
	_ = configManager.Load()

	envRoot := filepath.Join(tmpDir, "env-vaults")
	t.Setenv("ORI_VAULT_DIR", envRoot)

	handler := NewHandler(nil, configManager, nil, llm.NewFactory())

	req := httptest.NewRequest(http.MethodGet, "/api/settings/vault-root", nil)
	rec := httptest.NewRecorder()
	handler.VaultRootSettingsHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", rec.Code)
	}

	var resp VaultRootResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp.VaultRoot != "" {
		t.Fatalf("Expected no configured vault_root, got %q", resp.VaultRoot)
	}
	if resp.Source != "environment" {
		t.Fatalf("Expected source environment, got %q", resp.Source)
	}
	if resp.EffectiveVaultRoot != envRoot {
		t.Fatalf("Expected effective root %q, got %q", envRoot, resp.EffectiveVaultRoot)
	}
}

func TestVaultRootSettingsHandler_Post(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "settings.json")
	configManager := config.NewManager(tmpFile)
	_ = configManager.Load()

	handler := NewHandler(nil, configManager, nil, llm.NewFactory())

	customRoot := filepath.Join(tmpDir, "custom-vaults")
	var appliedRoot string
	handler.SetVaultRootUpdater(func(root string) error {
		appliedRoot = root
		return nil
	})

	body, _ := json.Marshal(map[string]string{
		"vault_root": customRoot,
	})

	req := httptest.NewRequest(http.MethodPost, "/api/settings/vault-root", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.VaultRootSettingsHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Success            bool   `json:"success"`
		VaultRoot          string `json:"vault_root"`
		EffectiveVaultRoot string `json:"effective_vault_root"`
		Source             string `json:"source"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if !resp.Success {
		t.Fatal("Expected success=true")
	}
	if resp.VaultRoot != customRoot {
		t.Fatalf("Expected vault_root %q, got %q", customRoot, resp.VaultRoot)
	}
	if resp.EffectiveVaultRoot != customRoot {
		t.Fatalf("Expected effective root %q, got %q", customRoot, resp.EffectiveVaultRoot)
	}
	if resp.Source != "settings" {
		t.Fatalf("Expected source settings, got %q", resp.Source)
	}
	if appliedRoot != customRoot {
		t.Fatalf("Expected updater root %q, got %q", customRoot, appliedRoot)
	}
	if _, err := os.Stat(customRoot); err != nil {
		t.Fatalf("Expected vault root directory to exist: %v", err)
	}

	configManager2 := config.NewManager(tmpFile)
	_ = configManager2.Load()
	if got := configManager2.GetVaultRoot(); got != customRoot {
		t.Fatalf("Expected persisted vault root %q, got %q", customRoot, got)
	}
}

func TestVaultRootSettingsHandler_Post_ClearFallsBackToDefault(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "settings.json")
	configManager := config.NewManager(tmpFile)
	_ = configManager.Load()
	if err := configManager.SetVaultRoot(filepath.Join(tmpDir, "custom-vaults")); err != nil {
		t.Fatalf("SetVaultRoot: %v", err)
	}
	if err := configManager.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	handler := NewHandler(nil, configManager, nil, llm.NewFactory())

	req := httptest.NewRequest(http.MethodPost, "/api/settings/vault-root", bytes.NewReader([]byte(`{"vault_root":""}`)))
	rec := httptest.NewRecorder()
	handler.VaultRootSettingsHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		VaultRoot          string `json:"vault_root"`
		EffectiveVaultRoot string `json:"effective_vault_root"`
		Source             string `json:"source"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp.VaultRoot != "" {
		t.Fatalf("Expected cleared vault_root, got %q", resp.VaultRoot)
	}
	if resp.Source != "default" {
		t.Fatalf("Expected source default, got %q", resp.Source)
	}
	if resp.EffectiveVaultRoot != config.DefaultVaultRoot() {
		t.Fatalf("Expected default effective root %q, got %q", config.DefaultVaultRoot(), resp.EffectiveVaultRoot)
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
		{name: "codex mini has tool-calling, general, and orchestration", model: "gpt-5.1-codex-mini", expected: []string{"tool-calling", "general", "orchestration"}},
		{name: "codex standard has research and orchestration", model: "gpt-5.3-codex", expected: []string{"research", "orchestration"}},
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

func TestProvidersHandler_LocalProvidersExposed(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "settings.json")
	configManager := config.NewManager(tmpFile)
	_ = configManager.Load()

	llmFactory := llm.NewFactory()
	llmFactory.Register("lmstudio", &mockLocalProvider{})
	llmFactory.Register("mlx_lm", &mockLocalProvider{})

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

	providersByName := make(map[string]ProviderInfo, len(resp.Providers))
	for _, provider := range resp.Providers {
		providersByName[provider.Name] = provider
	}

	for _, providerName := range []string{"lmstudio", "mlx_lm"} {
		info, ok := providersByName[providerName]
		if !ok {
			t.Fatalf("Expected %s provider in response", providerName)
		}
		if !info.Available {
			t.Fatalf("Expected %s provider to be available", providerName)
		}
		if info.Type != "local" {
			t.Fatalf("Expected %s provider type local, got %q", providerName, info.Type)
		}
		if info.RequiresKey {
			t.Fatalf("Expected %s provider to not require key", providerName)
		}
		if len(info.Models) == 0 {
			t.Fatalf("Expected %s provider models", providerName)
		}
	}
}

// Ensure temp directory cleanup
func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
