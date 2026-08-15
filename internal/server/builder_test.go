package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/systemassistant"
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
	// Isolate ORI_DATA_DIR so this full Build() never touches a real user's
	// actual Ori data, and so config.DefaultDataDir() below resolves to a
	// known, verifiable path rather than whatever happens to be on the host.
	t.Setenv("ORI_DATA_DIR", t.TempDir())

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
	// The shared Setup Wizard is wired in the same phase and for the same
	// reason: its state lives in the workspace's canonical folder record. An
	// unwired wizard makes every blueprint's setup unreachable.
	if server.Handlers.SetupWizard == nil {
		t.Error("Setup Wizard handler not wired")
	}
	if builder.setupWizardService == nil || builder.setupWizardRegistry == nil {
		t.Error("Setup Wizard service/registry not wired")
	}
	// The reset handler must be confined to the same resolved data directory
	// every other store uses, not the process working directory. A cwd
	// value here would mean a reset previewed/executed from a different
	// launch directory (e.g. the menu-bar app) operates on the wrong files.
	if builder.resetHandler == nil {
		t.Fatal("reset handler not initialized")
	}
	wantDataDir := config.DefaultDataDir()
	if got := builder.resetHandler.DataDir(); got != wantDataDir {
		t.Errorf("reset handler data dir = %q, want %q (config.DefaultDataDir())", got, wantDataDir)
	}
	if cwd, err := os.Getwd(); err == nil {
		if got := builder.resetHandler.DataDir(); got == "." || got == cwd {
			t.Errorf("reset handler data dir = %q, must not be the process working directory", got)
		}
	}
}

// TestSetupWizardRegistry_MatchesTheAuthorableAdapters keeps the two halves of
// the adapter contract in step: what a blueprint manifest may name, and what
// this build can actually run.
//
// An adapter registered here but not authorable would be unreachable; an
// authorable adapter with no registration blocks its steps at runtime. The
// pending list is explicit so each blueprint migration has to remove its own
// entry — the test fails the moment the two lists drift for any other reason.
func TestSetupWizardRegistry_MatchesTheAuthorableAdapters(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	builder, err := NewServerBuilder()
	if err != nil {
		t.Fatalf("NewServerBuilder failed: %v", err)
	}
	if _, err := builder.Build(); err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	if builder.setupWizardRegistry == nil {
		t.Fatal("Setup Wizard registry not wired")
	}

	authorable := map[string]bool{}
	for _, id := range projecttemplates.ValidSetupWizardAdapters {
		authorable[id] = true
	}

	// Keys(), not IDs(): the question is whether a manifest naming an adapter
	// can RESOLVE it, and an adapter reached through an alias resolves exactly
	// as well as one reached by its own ID. IDs() lists primaries only, so it
	// reported the canonical `file_janitor` key as unregistered even though the
	// Downloads adapter declares it as an alias and serves it — a failure about
	// the test's own question rather than about the build.
	registered := map[string]bool{}
	for _, id := range builder.setupWizardRegistry.Keys() {
		registered[id] = true
		if !authorable[id] {
			t.Errorf("adapter key %q resolves but no manifest may name it", id)
		}
	}

	// Blueprints whose migration has not landed yet. Remove an entry in the
	// group that registers its adapter.
	// Every migrated blueprint's adapter is registered now. A new authorable
	// adapter with no registration fails here rather than at a user's first
	// blocked step.
	pending := map[string]bool{}
	for id := range authorable {
		switch {
		case registered[id] && pending[id]:
			t.Errorf("adapter %q is registered; remove it from this test's pending list", id)
		case !registered[id] && !pending[id]:
			t.Errorf("adapter %q may be authored but is not registered in this build", id)
		}
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

	// One identity: the guide and the working assistant are both Ask Ori now
	// (Issue #350). A fresh install must never create a legacy record first.
	ag, ok := builder.st.GetAgent(systemassistant.CanonicalName)
	if !ok || ag == nil {
		t.Fatalf("expected system assistant agent to be created, got %v", builder.st.ListAgents())
	}
	if ag.Settings.Provider != "claude_code" {
		t.Fatalf("expected system assistant provider claude_code, got %q", ag.Settings.Provider)
	}
	if ag.Settings.Model != "sonnet" {
		t.Fatalf("expected system assistant model sonnet, got %q", ag.Settings.Model)
	}

	if _, err := os.Stat(filepath.Join(tempDir, "agents", systemassistant.CanonicalName, "agent_settings.json")); err != nil {
		t.Fatalf("expected persisted system assistant settings: %v", err)
	}
}
