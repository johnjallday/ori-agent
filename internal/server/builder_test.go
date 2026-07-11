package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestNewServerBuilder(t *testing.T) {
	builder, err := NewServerBuilder()
	if err != nil {
		t.Fatalf("NewServerBuilder failed: %v", err)
	}
	if builder == nil {
		t.Fatal("Expected builder to be non-nil")
		return
	}
	if builder.server == nil {
		t.Fatal("Expected builder.server to be non-nil")
	}
}

func TestServerBuilder_WithMethods(t *testing.T) {
	builder, _ := NewServerBuilder()

	// Test WithLLMFactory
	factory := llm.NewFactory()
	result := builder.WithLLMFactory(factory)
	if result != builder {
		t.Error("WithLLMFactory should return builder for chaining")
	}
	if builder.server.Core.LLMFactory != factory {
		t.Error("LLM factory not set correctly")
	}

	// Test WithConfigManager
	cfg := config.NewManager("test.json")
	result = builder.WithConfigManager(cfg)
	if result != builder {
		t.Error("WithConfigManager should return builder for chaining")
	}
	if builder.server.Core.ConfigManager != cfg {
		t.Error("Config manager not set correctly")
	}

}

func TestServerBuilder_MethodChaining(t *testing.T) {
	builder, _ := NewServerBuilder()

	// Test that methods can be chained
	result := builder.
		WithLLMFactory(llm.NewFactory()).
		WithConfigManager(config.NewManager("test.json"))

	if result != builder {
		t.Error("Method chaining should return same builder instance")
	}
}

// TestServerBuilder_Build_Integration tests the full build process
// This is more of an integration test and may be slow
func TestServerBuilder_Build_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	builder, err := NewServerBuilder()
	if err != nil {
		t.Fatalf("NewServerBuilder failed: %v", err)
	}

	server, err := builder.Build()
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	if server == nil {
		t.Fatal("Expected server to be non-nil")
		return
	}

	// Verify key dependencies were initialized
	if server.Core.ConfigManager == nil {
		t.Error("configManager not initialized")
	}
	if server.Core.LLMFactory == nil {
		t.Error("llmFactory not initialized")
	}
	if server.Storage.AgentStore == nil {
		t.Error("store not initialized")
	}
	if server.Handlers.WorkspaceRuns == nil {
		t.Error("workspace runs handler not initialized")
	}
	if builder.runBackedTaskHandler == nil {
		t.Error("run-backed task handler not initialized")
	}
	// REAPER readiness/preview/repair must be wired after the workspace store is
	// created (Phase 18), not during handler init (Phase 17) when the store is
	// still nil. If this regresses, the create preview is stuck on plugin_missing.
	if server.Handlers.Session == nil || !server.Handlers.Session.ReaperSetupWired() {
		t.Error("REAPER setup (resolver/preview/repair) not wired onto the session handler")
	}
}

// TestNew_UsesBuilder verifies New() delegates to builder
func TestNew_UsesBuilder(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	server, err := New()
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	if server == nil {
		t.Fatal("Expected server to be non-nil")
		return
	}

	// Verify key components initialized
	if server.Core.ConfigManager == nil {
		t.Error("configManager not initialized via New()")
	}
	if server.Core.LLMFactory == nil {
		t.Error("llmFactory not initialized via New()")
	}
}

// TestServerBuilder_WithStore tests store injection
func TestServerBuilder_WithStore(t *testing.T) {
	builder, _ := NewServerBuilder()

	tempDir := t.TempDir()
	mockStore, err := store.NewFileStore(tempDir+"/agents.json", loadDefaultSettings())
	if err != nil {
		t.Fatalf("Failed to create mock store: %v", err)
	}

	result := builder.WithStore(mockStore)
	if result != builder {
		t.Error("WithStore should return builder for chaining")
	}
	if builder.server.Storage.AgentStore != mockStore {
		t.Error("Store not set correctly")
	}
}

// TestServerBuilder_WithWorkspaceStore tests workspace store injection
func TestServerBuilder_WithWorkspaceStore(t *testing.T) {
	builder, _ := NewServerBuilder()

	tempDir := t.TempDir()
	mockWorkspaceStore, err := workspace.NewFileStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create mock workspace store: %v", err)
	}

	result := builder.WithWorkspaceStore(mockWorkspaceStore)
	if result != builder {
		t.Error("WithWorkspaceStore should return builder for chaining")
	}
	if builder.server.Storage.WorkspaceStore != mockWorkspaceStore {
		t.Error("Workspace store not set correctly")
	}
}

func TestServerBuilder_InitializeStorage_EnsuresAssistantFromSystemModel(t *testing.T) {
	builder, err := NewServerBuilder()
	if err != nil {
		t.Fatalf("NewServerBuilder failed: %v", err)
	}

	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "agents.json")
	t.Setenv("AGENT_STORE_PATH", storePath)

	cfg := config.NewManager(filepath.Join(tempDir, "settings.json"))
	if err := cfg.Load(); err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	if err := cfg.SetSystemModel("claude_code", "sonnet"); err != nil {
		t.Fatalf("failed to set system model: %v", err)
	}
	builder.configManager = cfg

	if err := builder.initializeStorage(); err != nil {
		t.Fatalf("initializeStorage failed: %v", err)
	}

	ag, ok := builder.st.GetAgent("Ori")
	if !ok || ag == nil {
		t.Fatalf("expected system assistant agent to be created")
	}
	if ag.Settings.Provider != "claude_code" {
		t.Fatalf("expected system assistant provider claude_code, got %q", ag.Settings.Provider)
	}
	if ag.Settings.Model != "sonnet" {
		t.Fatalf("expected system assistant model sonnet, got %q", ag.Settings.Model)
	}

	if _, err := os.Stat(filepath.Join(tempDir, "agents", "Ori", "agent_settings.json")); err != nil {
		t.Fatalf("expected persisted Ori agent settings: %v", err)
	}
}
