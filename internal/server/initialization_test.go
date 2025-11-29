package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/types"
)

func TestLoadDefaultSettings(t *testing.T) {
	settings := loadDefaultSettings()

	// Verify default values
	if settings.Model != "gpt-5-nano" {
		t.Errorf("Expected model to be 'gpt-5-nano', got '%s'", settings.Model)
	}

	if settings.Temperature != 1 {
		t.Errorf("Expected temperature to be 1, got %f", settings.Temperature)
	}

	if settings.SystemPrompt == "" {
		t.Error("Expected system prompt to be non-empty")
	}

	expectedPromptPrefix := "You are a helpful assistant"
	if len(settings.SystemPrompt) < len(expectedPromptPrefix) {
		t.Error("System prompt is unexpectedly short")
	}
}

func TestCreateConfigManager(t *testing.T) {
	// Create temporary config file
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "test_settings.json")

	// Write a minimal valid config with properly formatted API key
	configContent := `{"openai_api_key": "sk-test1234567890abcdefghijklmnopqrstuvwxyz"}`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to create test config: %v", err)
	}

	// Test with valid config
	mgr, err := createConfigManager(configPath)
	if err != nil {
		t.Fatalf("createConfigManager failed: %v", err)
	}
	if mgr == nil {
		t.Fatal("Expected config manager to be non-nil")
	}

	// Verify it loaded the config
	apiKey := mgr.GetAPIKey()
	if apiKey != "sk-test1234567890abcdefghijklmnopqrstuvwxyz" {
		t.Errorf("Expected API key 'sk-test1234567890abcdefghijklmnopqrstuvwxyz', got '%s'", apiKey)
	}
}

func TestCreateConfigManager_NonExistentFile(t *testing.T) {
	// Test with non-existent file (should create new config)
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "nonexistent.json")

	mgr, err := createConfigManager(configPath)
	// This should not error - it creates a new config
	if err != nil {
		t.Fatalf("createConfigManager should handle non-existent file: %v", err)
	}
	if mgr == nil {
		t.Fatal("Expected config manager to be non-nil")
	}
}

func TestCreateRegistryManager(t *testing.T) {
	mgr, err := createRegistryManager()
	if err != nil {
		t.Fatalf("createRegistryManager failed: %v", err)
	}
	if mgr == nil {
		t.Fatal("Expected registry manager to be non-nil")
	}
}

func TestCreateLLMFactory(t *testing.T) {
	factory := createLLMFactory()
	if factory == nil {
		t.Fatal("Expected LLM factory to be non-nil")
	}
}

func TestRegisterLLMProviders(t *testing.T) {
	// Create a temporary config
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "test_settings.json")
	configContent := `{"openai_api_key": "sk-test1234567890abcdefghijklmnopqrstuvwxyz", "anthropic_api_key": "sk-ant-test1234567890abcdefghijklmnopqrstuvwxyz"}`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to create test config: %v", err)
	}

	configMgr, err := createConfigManager(configPath)
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}

	factory := createLLMFactory()

	// Register providers
	err = registerLLMProviders(factory, configMgr)
	if err != nil {
		t.Fatalf("registerLLMProviders failed: %v", err)
	}

	// Verify providers were registered
	// Note: We can't easily test this without exposing internals,
	// but we can at least verify it doesn't error
}

func TestRegisterLLMProviders_NoAPIKeys(t *testing.T) {
	// Test with empty config (no API keys)
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "empty_settings.json")
	configContent := `{}`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to create test config: %v", err)
	}

	configMgr, err := createConfigManager(configPath)
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}

	factory := createLLMFactory()

	// Should not error even without API keys (Ollama always available)
	err = registerLLMProviders(factory, configMgr)
	if err != nil {
		t.Fatalf("registerLLMProviders should succeed without API keys: %v", err)
	}
}

func TestResolveAgentStorePath(t *testing.T) {
	// Test default path
	path, err := resolveAgentStorePath()
	if err != nil {
		t.Fatalf("resolveAgentStorePath failed: %v", err)
	}
	if path == "" {
		t.Error("Expected non-empty path")
	}

	// Test with environment variable
	os.Setenv("AGENT_STORE_PATH", "/custom/path/agents.json")
	defer os.Unsetenv("AGENT_STORE_PATH")

	path, err = resolveAgentStorePath()
	if err != nil {
		t.Fatalf("resolveAgentStorePath failed with env var: %v", err)
	}
	if path != "/custom/path/agents.json" {
		t.Errorf("Expected '/custom/path/agents.json', got '%s'", path)
	}
}

func TestCreateFileStore(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "test_agents.json")

	defaultConf := types.Settings{
		Model:       "gpt-4",
		Temperature: 0.7,
	}

	store, err := createFileStore(storePath, defaultConf)
	if err != nil {
		t.Fatalf("createFileStore failed: %v", err)
	}
	if store == nil {
		t.Fatal("Expected store to be non-nil")
	}
}

func TestResolvePluginCacheDir(t *testing.T) {
	// Test default path
	dir := resolvePluginCacheDir()
	if dir == "" {
		t.Error("Expected non-empty directory")
	}

	// Test with environment variable
	os.Setenv("PLUGIN_CACHE_DIR", "/custom/cache")
	defer os.Unsetenv("PLUGIN_CACHE_DIR")

	dir = resolvePluginCacheDir()
	if dir != "/custom/cache" {
		t.Errorf("Expected '/custom/cache', got '%s'", dir)
	}
}

func TestCreatePluginDownloader(t *testing.T) {
	tempDir := t.TempDir()
	downloader := createPluginDownloader(tempDir)
	if downloader == nil {
		t.Fatal("Expected plugin downloader to be non-nil")
	}
}

func TestResolveWorkspaceDir(t *testing.T) {
	// Test default path
	dir := resolveWorkspaceDir()
	if dir == "" {
		t.Error("Expected non-empty directory")
	}

	// Test with environment variable
	os.Setenv("WORKSPACE_DIR", "/custom/workspaces")
	defer os.Unsetenv("WORKSPACE_DIR")

	dir = resolveWorkspaceDir()
	if dir != "/custom/workspaces" {
		t.Errorf("Expected '/custom/workspaces', got '%s'", dir)
	}
}

func TestCreateWorkspaceStore(t *testing.T) {
	tempDir := t.TempDir()

	store, err := createWorkspaceStore(tempDir)
	if err != nil {
		t.Fatalf("createWorkspaceStore failed: %v", err)
	}
	if store == nil {
		t.Fatal("Expected workspace store to be non-nil")
	}
}

func TestResolveCostTrackerDir(t *testing.T) {
	dir := resolveCostTrackerDir()
	if dir == "" {
		t.Error("Expected non-empty directory")
	}

	// Should contain .ori-agent/usage_data
	if !filepath.IsAbs(dir) {
		t.Error("Expected absolute path for cost tracker directory")
	}
}

func TestResolveActivityLogDir(t *testing.T) {
	dir := resolveActivityLogDir()
	if dir == "" {
		t.Error("Expected non-empty directory")
	}
}

func TestResolveLocationZonesPath(t *testing.T) {
	path := resolveLocationZonesPath()
	if path == "" {
		t.Error("Expected non-empty path")
	}
}

func TestResolveWorkflowTemplatesDir(t *testing.T) {
	// Test default path
	dir := resolveWorkflowTemplatesDir()
	if dir == "" {
		t.Error("Expected non-empty directory")
	}

	// Test with environment variable
	os.Setenv("WORKFLOW_TEMPLATES_DIR", "/custom/templates")
	defer os.Unsetenv("WORKFLOW_TEMPLATES_DIR")

	dir = resolveWorkflowTemplatesDir()
	if dir != "/custom/templates" {
		t.Errorf("Expected '/custom/templates', got '%s'", dir)
	}
}

func TestLoadLocationZones_NonExistentFile(t *testing.T) {
	// Test with non-existent file (should return empty zones, not error)
	zones, err := loadLocationZones("/nonexistent/path/zones.json")
	if err != nil {
		t.Errorf("loadLocationZones should not error on missing file: %v", err)
	}
	if len(zones) != 0 {
		t.Errorf("Expected 0 zones for missing file, got %d", len(zones))
	}
}

func TestCreateLocationManager(t *testing.T) {
	mgr := createLocationManager(nil, "test_zones.json")
	if mgr == nil {
		t.Fatal("Expected location manager to be non-nil")
	}

	// Clean up
	mgr.Stop()
}
