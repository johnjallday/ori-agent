package server

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/agentstudio"
	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/registry"
	"github.com/johnjallday/ori-agent/internal/store"
)

func TestNewServerBuilder(t *testing.T) {
	builder, err := NewServerBuilder()
	if err != nil {
		t.Fatalf("NewServerBuilder failed: %v", err)
	}
	if builder == nil {
		t.Fatal("Expected builder to be non-nil")
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
	if builder.server.llmFactory != factory {
		t.Error("LLM factory not set correctly")
	}

	// Test WithConfigManager
	cfg := config.NewManager("test.json")
	result = builder.WithConfigManager(cfg)
	if result != builder {
		t.Error("WithConfigManager should return builder for chaining")
	}
	if builder.server.configManager != cfg {
		t.Error("Config manager not set correctly")
	}

	// Test WithRegistryManager
	reg := registry.NewManager()
	result = builder.WithRegistryManager(reg)
	if result != builder {
		t.Error("WithRegistryManager should return builder for chaining")
	}
	if builder.server.registryManager != reg {
		t.Error("Registry manager not set correctly")
	}
}

func TestServerBuilder_MethodChaining(t *testing.T) {
	builder, _ := NewServerBuilder()

	// Test that methods can be chained
	result := builder.
		WithLLMFactory(llm.NewFactory()).
		WithConfigManager(config.NewManager("test.json")).
		WithRegistryManager(registry.NewManager())

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
	}

	// Verify key dependencies were initialized
	if server.configManager == nil {
		t.Error("configManager not initialized")
	}
	if server.llmFactory == nil {
		t.Error("llmFactory not initialized")
	}
	if server.registryManager == nil {
		t.Error("registryManager not initialized")
	}
	if server.st == nil {
		t.Error("store not initialized")
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
	}

	// Verify key components initialized
	if server.configManager == nil {
		t.Error("configManager not initialized via New()")
	}
	if server.llmFactory == nil {
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
	if builder.server.st != mockStore {
		t.Error("Store not set correctly")
	}
}

// TestServerBuilder_WithWorkspaceStore tests workspace store injection
func TestServerBuilder_WithWorkspaceStore(t *testing.T) {
	builder, _ := NewServerBuilder()

	tempDir := t.TempDir()
	mockWorkspaceStore, err := agentstudio.NewFileStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create mock workspace store: %v", err)
	}

	result := builder.WithWorkspaceStore(mockWorkspaceStore)
	if result != builder {
		t.Error("WithWorkspaceStore should return builder for chaining")
	}
	if builder.server.workspaceStore != mockWorkspaceStore {
		t.Error("Workspace store not set correctly")
	}
}
