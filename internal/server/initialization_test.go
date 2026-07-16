package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
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

func TestRegisterLLMProviders_LegacySettingsJSONStillWorksWithSecretStoreAttached(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "legacy_settings.json")
	configContent := `{"openai_api_key": "sk-test1234567890abcdefghijklmnopqrstuvwxyz"}`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to create test config: %v", err)
	}

	configMgr, err := createConfigManager(configPath)
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}
	if configMgr.SecretStore() == nil {
		t.Fatal("expected secret store to be attached")
	}

	if got := configMgr.GetAPIKey(); got != "sk-test1234567890abcdefghijklmnopqrstuvwxyz" {
		t.Fatalf("expected legacy settings API key to remain readable, got %q", got)
	}

	factory := createLLMFactory()
	if err := registerLLMProviders(factory, configMgr); err != nil {
		t.Fatalf("registerLLMProviders failed for legacy settings: %v", err)
	}
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

	for _, providerName := range []string{"ollama", "lmstudio", "mlx_lm"} {
		if !factory.HasProvider(providerName) {
			t.Fatalf("expected %s provider to be registered without API keys", providerName)
		}
	}
}

func TestResolveAgentStorePath(t *testing.T) {
	// Test default path
	path := resolveAgentStorePath()
	if path == "" {
		t.Error("Expected non-empty path")
	}

	// Test with environment variable
	_ = os.Setenv("AGENT_STORE_PATH", "/custom/path/agents.json")
	defer func() { _ = os.Unsetenv("AGENT_STORE_PATH") }()

	path = resolveAgentStorePath()
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

func TestResolveWorkspaceDir(t *testing.T) {
	// Test default path
	dir := resolveWorkspaceDir()
	if dir == "" {
		t.Error("Expected non-empty directory")
	}

	// Test with environment variable
	_ = os.Setenv("WORKSPACE_DIR", "/custom/workspaces")
	defer func() { _ = os.Unsetenv("WORKSPACE_DIR") }()

	dir = resolveWorkspaceDir()
	if dir != "/custom/workspaces" {
		t.Errorf("Expected '/custom/workspaces', got '%s'", dir)
	}
}

func TestResolveWorkspaceRoot_UsesStagingUntilDirectoryIsConfirmed(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("ORI_DATA_DIR", dataDir)
	t.Setenv("WORKSPACE_DIR", "")

	manager := config.NewManager(filepath.Join(dataDir, "settings.json"))
	if err := manager.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, want := resolveWorkspaceRoot(manager), config.UnconfirmedWorkspaceRoot(); got != want {
		t.Fatalf("resolveWorkspaceRoot(unconfirmed) = %q, want staging %q", got, want)
	}

	selected := filepath.Join(t.TempDir(), "selected-workspaces")
	if err := manager.SetWorkspaceRoot(selected); err != nil {
		t.Fatalf("SetWorkspaceRoot: %v", err)
	}
	if got := resolveWorkspaceRoot(manager); got != selected {
		t.Fatalf("resolveWorkspaceRoot(confirmed) = %q, want %q", got, selected)
	}
}

func TestResolveWorkspaceRoot_DoesNotAdoptSuggestedDirectoryBeforeConfirmation(t *testing.T) {
	homeDir := t.TempDir()
	dataDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("ORI_DATA_DIR", dataDir)
	t.Setenv("WORKSPACE_DIR", "")

	suggestedRoot := config.DefaultWorkspaceRoot()
	existingStore, err := workspace.NewFileStore(suggestedRoot)
	if err != nil {
		t.Fatalf("NewFileStore(suggested): %v", err)
	}
	if err := existingStore.Save(workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Existing Folder"})); err != nil {
		t.Fatalf("seed suggested directory: %v", err)
	}

	manager := config.NewManager(filepath.Join(dataDir, "settings.json"))
	if err := manager.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	startupStore, err := workspace.NewFileStore(resolveWorkspaceRoot(manager))
	if err != nil {
		t.Fatalf("NewFileStore(unconfirmed): %v", err)
	}
	ids, err := startupStore.List()
	if err != nil {
		t.Fatalf("List(unconfirmed): %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("unconfirmed startup adopted %d workspace(s) from the suggested directory", len(ids))
	}

	if err := manager.SetWorkspaceRoot(suggestedRoot); err != nil {
		t.Fatalf("SetWorkspaceRoot: %v", err)
	}
	confirmedStore, err := workspace.NewFileStore(resolveWorkspaceRoot(manager))
	if err != nil {
		t.Fatalf("NewFileStore(confirmed): %v", err)
	}
	ids, err = confirmedStore.List()
	if err != nil {
		t.Fatalf("List(confirmed): %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("confirmed startup found %d workspaces, want 1", len(ids))
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
	_ = os.Setenv("WORKFLOW_TEMPLATES_DIR", "/custom/templates")
	defer func() { _ = os.Unsetenv("WORKFLOW_TEMPLATES_DIR") }()

	dir = resolveWorkflowTemplatesDir()
	if dir != "/custom/templates" {
		t.Errorf("Expected '/custom/templates', got '%s'", dir)
	}
}

func TestLoadLocationZones_NonExistentFile(t *testing.T) {
	// Test with non-existent file (should return empty zones, not error)
	zones := loadLocationZones("/nonexistent/path/zones.json")
	if len(zones) != 0 {
		t.Errorf("Expected 0 zones for missing file, got %d", len(zones))
	}
}

func TestResolveAgentStorePath_DefaultsToStableDataDir(t *testing.T) {
	// With ORI_DATA_DIR set, the default agent store must live inside it and be
	// independent of the current working directory.
	dataDir := t.TempDir()
	t.Setenv("ORI_DATA_DIR", dataDir)
	t.Setenv("AGENT_STORE_PATH", "")

	got := resolveAgentStorePath()
	want := filepath.Join(dataDir, "agents.json")
	if got != want {
		t.Fatalf("expected agent store %q, got %q", want, got)
	}
}

// chdir switches the working directory for the duration of the test.
func chdir(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
}

// writeAgent creates a minimal agent folder under agentsDir.
func writeAgent(t *testing.T, agentsDir, name string) {
	t.Helper()
	dir := filepath.Join(agentsDir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir agent: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent_settings.json"), []byte(`{"type":"tool-calling","Settings":{}}`), 0o644); err != nil {
		t.Fatalf("write agent settings: %v", err)
	}
}

func TestResolveAllowlistPath_AnchoredToDataDir(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("ORI_DATA_DIR", dataDir)

	got := resolveAllowlistPath()
	want := filepath.Join(dataDir, workspace.DefaultAllowlistFilename)
	if got != want {
		t.Fatalf("expected allowlist path %q, got %q", want, got)
	}
}

func TestMigrateLegacyAgentStore_AdoptsCWDAgents(t *testing.T) {
	// Legacy agents live under <cwd>/agents; the stable store is empty.
	cwd := t.TempDir()
	chdir(t, cwd)
	writeAgent(t, filepath.Join(cwd, "agents"), "alice")
	writeAgent(t, filepath.Join(cwd, "agents"), "bob")

	dataDir := t.TempDir()
	storePath := filepath.Join(dataDir, "agents.json")

	if err := migrateLegacyAgentStore(storePath); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	for _, name := range []string{"alice", "bob"} {
		p := filepath.Join(dataDir, "agents", name, "agent_settings.json")
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected adopted agent %q at %s: %v", name, p, err)
		}
	}
}

func TestMigrateLegacyAgentStore_SkipsWhenDestPopulated(t *testing.T) {
	cwd := t.TempDir()
	chdir(t, cwd)
	writeAgent(t, filepath.Join(cwd, "agents"), "legacy")

	dataDir := t.TempDir()
	storePath := filepath.Join(dataDir, "agents.json")
	// Destination already has an agent; migration must not touch it or import legacy.
	writeAgent(t, filepath.Join(dataDir, "agents"), "existing")

	if err := migrateLegacyAgentStore(storePath); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dataDir, "agents", "legacy")); !os.IsNotExist(err) {
		t.Errorf("legacy agent should not be adopted into a populated store (err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "agents", "existing")); err != nil {
		t.Errorf("existing agent should be untouched: %v", err)
	}
}

func TestMigrateLegacyAgentStore_NoopWhenSameDir(t *testing.T) {
	// When the stable store resolves to the CWD itself, there is nothing to move.
	cwd := t.TempDir()
	chdir(t, cwd)
	writeAgent(t, filepath.Join(cwd, "agents"), "solo")

	storePath := filepath.Join(cwd, "agents.json")
	if err := migrateLegacyAgentStore(storePath); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Agent stays exactly where it was; no duplication.
	if _, err := os.Stat(filepath.Join(cwd, "agents", "solo")); err != nil {
		t.Errorf("agent should remain in place: %v", err)
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
