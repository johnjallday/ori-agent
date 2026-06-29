package server

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/johnjallday/ori-agent/internal/mcp"
)

func TestInitializeMCPRegistersEnabledServersWithoutStarting(t *testing.T) {
	t.Setenv(disableExternalMCPImportEnv, "true")
	t.Chdir(t.TempDir())

	cfg := mcp.GlobalConfig{
		Servers: []mcp.ServerConfig{
			{
				Name:      "broken",
				Command:   "definitely-not-a-real-command-for-mcp-test",
				Transport: "stdio",
				Enabled:   true,
			},
		},
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("failed to marshal config: %v", err)
	}
	if err := os.WriteFile("mcp_registry.json", data, 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	builder := &ServerBuilder{}
	builder.initializeMCP()

	status, err := builder.mcpRegistry.GetServerStatus("broken")
	if err != nil {
		t.Fatalf("expected broken server to be registered: %v", err)
	}
	if status != mcp.StatusStopped {
		t.Fatalf("expected globally enabled server to remain stopped until lazy use, got %s", status)
	}
}

func TestExternalMCPImportEnabled(t *testing.T) {
	t.Run("default enabled", func(t *testing.T) {
		t.Setenv(disableExternalMCPImportEnv, "")
		if !externalMCPImportEnabled() {
			t.Fatal("expected external MCP import to be enabled by default")
		}
	})

	t.Run("explicit disable", func(t *testing.T) {
		t.Setenv(disableExternalMCPImportEnv, "true")
		if externalMCPImportEnabled() {
			t.Fatal("expected external MCP import to be disabled when env is true")
		}
	})

	t.Run("explicit enable", func(t *testing.T) {
		t.Setenv(disableExternalMCPImportEnv, "false")
		if !externalMCPImportEnabled() {
			t.Fatal("expected external MCP import to remain enabled when env is false")
		}
	})

	t.Run("invalid value keeps default", func(t *testing.T) {
		t.Setenv(disableExternalMCPImportEnv, "not-a-bool")
		if !externalMCPImportEnabled() {
			t.Fatal("expected invalid env values to preserve default enabled behavior")
		}
	})
}
