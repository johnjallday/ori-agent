package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/vault"
)

func TestValidateSystemModel(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		model    string
		wantErr  bool
		errMsg   string
	}{
		{
			name:     "valid openai provider",
			provider: "openai",
			model:    "gpt-4o-mini",
			wantErr:  false,
		},
		{
			name:     "valid claude provider",
			provider: "claude",
			model:    "claude-3-haiku-20240307",
			wantErr:  false,
		},
		{
			name:     "valid ollama provider",
			provider: "ollama",
			model:    "llama3.2:latest",
			wantErr:  false,
		},
		{
			name:     "valid lmstudio provider",
			provider: "lmstudio",
			model:    "openai/gpt-oss-20b",
			wantErr:  false,
		},
		{
			name:     "valid mlx_lm provider",
			provider: "mlx_lm",
			model:    "mlx-community/Llama-3.2-3B-Instruct-4bit",
			wantErr:  false,
		},
		{
			name:     "valid gemini provider",
			provider: "gemini",
			model:    "gemini-2.5-flash",
			wantErr:  false,
		},
		{
			name:     "both empty is valid",
			provider: "",
			model:    "",
			wantErr:  false,
		},
		{
			name:     "provider without model is invalid",
			provider: "openai",
			model:    "",
			wantErr:  true,
			errMsg:   "system provider and model must both be set or both be empty",
		},
		{
			name:     "model without provider is invalid",
			provider: "",
			model:    "gpt-4o-mini",
			wantErr:  true,
			errMsg:   "system provider and model must both be set or both be empty",
		},
		{
			name:     "invalid provider",
			provider: "invalid-provider",
			model:    "some-model",
			wantErr:  true,
			errMsg:   "invalid system provider",
		},
		{
			name:     "model with invalid characters",
			provider: "openai",
			model:    "model with spaces",
			wantErr:  true,
			errMsg:   "contains invalid character",
		},
		{
			name:     "model with special chars is invalid",
			provider: "openai",
			model:    "model@version",
			wantErr:  true,
			errMsg:   "contains invalid character",
		},
		{
			name:     "case insensitive provider",
			provider: "OpenAI",
			model:    "gpt-4o-mini",
			wantErr:  false,
		},
		{
			name:     "case insensitive provider - Claude",
			provider: "CLAUDE",
			model:    "claude-3-haiku-20240307",
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSystemModel(tt.provider, tt.model)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateSystemModel() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && tt.errMsg != "" {
				if err == nil || !contains(err.Error(), tt.errMsg) {
					t.Errorf("validateSystemModel() error = %v, want error containing %q", err, tt.errMsg)
				}
			}
		})
	}
}

func TestValidProviders(t *testing.T) {
	providers := ValidProviders()

	// Check expected providers exist
	expected := []string{"openai", "codex", "claude_code", "claude", "gemini", "ollama", "lmstudio", "mlx_lm"}
	if len(providers) != len(expected) {
		t.Errorf("ValidProviders() returned %d providers, want %d", len(providers), len(expected))
	}

	for _, exp := range expected {
		found := false
		for _, p := range providers {
			if p == exp {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("ValidProviders() missing expected provider %q", exp)
		}
	}
}

func TestManagerSystemModel(t *testing.T) {
	// Create temp file for settings
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "settings.json")

	manager := NewManager(tmpFile)
	err := manager.Load()
	if err != nil {
		t.Fatalf("Failed to load manager: %v", err)
	}

	// Test initial state - should not be configured
	if manager.IsSystemModelConfigured() {
		t.Error("IsSystemModelConfigured() should be false initially")
	}

	provider, model := manager.GetSystemModel()
	if provider != "" || model != "" {
		t.Errorf("GetSystemModel() = (%q, %q), want (\"\", \"\")", provider, model)
	}

	// Test setting valid system model
	err = manager.SetSystemModel("openai", "gpt-4o-mini")
	if err != nil {
		t.Errorf("SetSystemModel() error = %v, want nil", err)
	}

	if !manager.IsSystemModelConfigured() {
		t.Error("IsSystemModelConfigured() should be true after setting")
	}

	provider, model = manager.GetSystemModel()
	if provider != "openai" || model != "gpt-4o-mini" {
		t.Errorf("GetSystemModel() = (%q, %q), want (\"openai\", \"gpt-4o-mini\")", provider, model)
	}

	// Test setting invalid provider
	err = manager.SetSystemModel("invalid", "some-model")
	if err == nil {
		t.Error("SetSystemModel() should return error for invalid provider")
	}

	// Test clearing system model
	err = manager.SetSystemModel("", "")
	if err != nil {
		t.Errorf("SetSystemModel(\"\", \"\") error = %v, want nil", err)
	}

	if manager.IsSystemModelConfigured() {
		t.Error("IsSystemModelConfigured() should be false after clearing")
	}
}

func TestManagerSystemModelPersistence(t *testing.T) {
	// Create temp file for settings
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "settings.json")

	// Create manager and set system model
	manager1 := NewManager(tmpFile)
	err := manager1.Load()
	if err != nil {
		t.Fatalf("Failed to load manager: %v", err)
	}

	err = manager1.SetSystemModel("claude", "claude-3-haiku-20240307")
	if err != nil {
		t.Fatalf("SetSystemModel() error = %v", err)
	}

	err = manager1.Save()
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Create new manager and load settings
	manager2 := NewManager(tmpFile)
	err = manager2.Load()
	if err != nil {
		t.Fatalf("Failed to load manager2: %v", err)
	}

	// Verify settings persisted
	provider, model := manager2.GetSystemModel()
	if provider != "claude" || model != "claude-3-haiku-20240307" {
		t.Errorf("After reload: GetSystemModel() = (%q, %q), want (\"claude\", \"claude-3-haiku-20240307\")", provider, model)
	}

	if !manager2.IsSystemModelConfigured() {
		t.Error("IsSystemModelConfigured() should be true after reload")
	}
}

func TestManagerSystemModelWhitespace(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "settings.json")

	manager := NewManager(tmpFile)
	err := manager.Load()
	if err != nil {
		t.Fatalf("Failed to load manager: %v", err)
	}

	// Test that whitespace is trimmed
	err = manager.SetSystemModel("  openai  ", "  gpt-4o-mini  ")
	if err != nil {
		t.Errorf("SetSystemModel() error = %v, want nil", err)
	}

	provider, model := manager.GetSystemModel()
	if provider != "openai" || model != "gpt-4o-mini" {
		t.Errorf("GetSystemModel() = (%q, %q), want (\"openai\", \"gpt-4o-mini\") - whitespace not trimmed", provider, model)
	}
}

func TestManagerSettingsJSON(t *testing.T) {
	// Create temp file for settings
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "settings.json")

	manager := NewManager(tmpFile)
	err := manager.Load()
	if err != nil {
		t.Fatalf("Failed to load manager: %v", err)
	}

	err = manager.SetSystemModel("ollama", "llama3.2:latest")
	if err != nil {
		t.Fatalf("SetSystemModel() error = %v", err)
	}

	err = manager.Save()
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Read raw JSON and verify fields are present
	data, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("Failed to read settings file: %v", err)
	}

	jsonStr := string(data)
	if !contains(jsonStr, `"system_provider"`) {
		t.Error("Settings JSON missing system_provider field")
	}
	if !contains(jsonStr, `"system_model"`) {
		t.Error("Settings JSON missing system_model field")
	}
	if !contains(jsonStr, `"ollama"`) {
		t.Error("Settings JSON missing ollama value")
	}
	if !contains(jsonStr, `"llama3.2:latest"`) {
		t.Error("Settings JSON missing llama3.2:latest value")
	}
}

func TestSystemReasoningEffort_DefaultAndValidation(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "settings.json")

	manager := NewManager(tmpFile)
	if err := manager.Load(); err != nil {
		t.Fatalf("Failed to load manager: %v", err)
	}

	// Default should be medium when unset.
	if got := manager.GetSystemReasoningEffort(); got != "medium" {
		t.Fatalf("GetSystemReasoningEffort() = %q, want %q", got, "medium")
	}

	if err := manager.SetSystemReasoningEffort("medium"); err != nil {
		t.Fatalf("SetSystemReasoningEffort(medium) error = %v", err)
	}
	if got := manager.GetSystemReasoningEffort(); got != "medium" {
		t.Fatalf("GetSystemReasoningEffort() = %q, want %q", got, "medium")
	}

	if err := manager.SetSystemReasoningEffort("invalid-level"); err == nil {
		t.Fatal("SetSystemReasoningEffort(invalid-level) expected error, got nil")
	}

	if err := manager.SetSystemReasoningEffort(""); err != nil {
		t.Fatalf("SetSystemReasoningEffort(\"\") error = %v", err)
	}
	if got := manager.GetSystemReasoningEffort(); got != "medium" {
		t.Fatalf("GetSystemReasoningEffort() after clear = %q, want %q", got, "medium")
	}
}

func TestSystemReasoningEffort_PersistenceAndClearWithSystemModel(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "settings.json")

	manager := NewManager(tmpFile)
	if err := manager.Load(); err != nil {
		t.Fatalf("Failed to load manager: %v", err)
	}

	if err := manager.SetSystemModel("codex", "gpt-5.3-codex"); err != nil {
		t.Fatalf("SetSystemModel() error = %v", err)
	}
	if err := manager.SetSystemReasoningEffort("xhigh"); err != nil {
		t.Fatalf("SetSystemReasoningEffort() error = %v", err)
	}
	if err := manager.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	manager2 := NewManager(tmpFile)
	if err := manager2.Load(); err != nil {
		t.Fatalf("Failed to load manager2: %v", err)
	}
	if got := manager2.GetSystemReasoningEffort(); got != "xhigh" {
		t.Fatalf("GetSystemReasoningEffort() after reload = %q, want %q", got, "xhigh")
	}

	// Clearing system model should clear persisted reasoning override.
	if err := manager2.SetSystemModel("", ""); err != nil {
		t.Fatalf("SetSystemModel clear error = %v", err)
	}
	if got := manager2.GetSystemReasoningEffort(); got != "medium" {
		t.Fatalf("GetSystemReasoningEffort() after system model clear = %q, want %q", got, "medium")
	}
}

func TestManagerSecretStoreSanitizesSavedSettings(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "settings.json")

	secretStore := vault.NewMemorySecretStore()
	manager := NewManagerWithSecretStore(tmpFile, secretStore)
	if err := manager.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	openAIKey := "sk-test1234567890abcdefghijklmnopqrstuvwxyz"
	anthropicKey := "sk-ant-test1234567890abcdefghijklmnopqrstuvwxyz"
	geminiKey := "gemini-secret"
	braveKey := "brave-secret-token"

	if err := manager.SetAPIKey(openAIKey); err != nil {
		t.Fatalf("SetAPIKey() error = %v", err)
	}
	if err := manager.SetAnthropicAPIKey(anthropicKey); err != nil {
		t.Fatalf("SetAnthropicAPIKey() error = %v", err)
	}
	if err := manager.SetGeminiAPIKey(geminiKey); err != nil {
		t.Fatalf("SetGeminiAPIKey() error = %v", err)
	}
	if err := manager.SetBraveAPIKey(braveKey); err != nil {
		t.Fatalf("SetBraveAPIKey() error = %v", err)
	}
	if err := manager.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	raw, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	rawText := string(raw)
	if contains(rawText, openAIKey) || contains(rawText, anthropicKey) || contains(rawText, geminiKey) || contains(rawText, braveKey) {
		t.Fatalf("settings.json should not contain secret values when secret store is active: %s", rawText)
	}

	if got := manager.GetAPIKey(); got != openAIKey {
		t.Fatalf("GetAPIKey() = %q, want %q", got, openAIKey)
	}
	if got := manager.GetAnthropicAPIKey(); got != anthropicKey {
		t.Fatalf("GetAnthropicAPIKey() = %q, want %q", got, anthropicKey)
	}
	if got := manager.GetGeminiAPIKey(); got != geminiKey {
		t.Fatalf("GetGeminiAPIKey() = %q, want %q", got, geminiKey)
	}
	if got := manager.GetBraveAPIKey(); got != braveKey {
		t.Fatalf("GetBraveAPIKey() = %q, want %q", got, braveKey)
	}

	effective := manager.Get()
	if effective.OpenAIAPIKey != openAIKey {
		t.Fatalf("effective OpenAIAPIKey = %q, want %q", effective.OpenAIAPIKey, openAIKey)
	}
	if effective.Utility.BraveAPIKey != braveKey {
		t.Fatalf("effective BraveAPIKey = %q, want %q", effective.Utility.BraveAPIKey, braveKey)
	}
}

func TestManagerSecretStoreFallsBackToSettingsJSON(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "settings.json")
	legacyKey := "sk-test1234567890abcdefghijklmnopqrstuvwxyz"

	if err := os.WriteFile(tmpFile, []byte(`{"openai_api_key":"`+legacyKey+`"}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	manager := NewManagerWithSecretStore(tmpFile, vault.NewMemorySecretStore())
	if err := manager.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got := manager.GetAPIKey(); got != legacyKey {
		t.Fatalf("GetAPIKey() = %q, want %q", got, legacyKey)
	}
}

func TestManagerSecretStoreTakesPrecedenceOverSettingsJSON(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "settings.json")
	legacyKey := "sk-test1234567890abcdefghijklmnopqrstuvwxyz"
	secretKey := "sk-secret1234567890abcdefghijklmnopqrstuvwxyz"

	if err := os.WriteFile(tmpFile, []byte(`{"openai_api_key":"`+legacyKey+`"}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	secretStore := vault.NewMemorySecretStore()
	if err := secretStore.Set(vault.SecretKeyOpenAIAPIKey, secretKey); err != nil {
		t.Fatalf("secretStore.Set() error = %v", err)
	}

	manager := NewManagerWithSecretStore(tmpFile, secretStore)
	if err := manager.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got := manager.GetAPIKey(); got != secretKey {
		t.Fatalf("GetAPIKey() = %q, want %q", got, secretKey)
	}
}

// contains checks if s contains substr
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
