package agenthttp

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

func newMCPHandlerTestStore(t *testing.T) store.Store {
	t.Helper()

	path := filepath.Join(t.TempDir(), "agents_index.json")
	st, err := store.NewFileStore(path, types.Settings{
		Model:       "gpt-5-nano",
		Temperature: 1.0,
	})
	if err != nil {
		t.Fatalf("failed to create test store: %v", err)
	}
	return st
}

func TestMCPHandler_EnableDisable_SyncsAgentMCPServers(t *testing.T) {
	st := newMCPHandlerTestStore(t)
	if err := st.CreateAgent("runner", &store.CreateAgentConfig{Type: "tool-calling"}); err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	baseDir := t.TempDir()
	configManager := mcp.NewConfigManager(baseDir)
	registry := mcp.NewRegistry()

	serverConfig := mcp.ServerConfig{
		Name:      "github",
		Command:   "npx",
		Args:      []string{"-y", "@modelcontextprotocol/server-github"},
		Env:       map[string]string{},
		Transport: "stdio",
		Enabled:   true,
	}
	if err := configManager.AddServer(serverConfig); err != nil {
		t.Fatalf("failed to add server config: %v", err)
	}
	if err := registry.AddServer(serverConfig); err != nil {
		t.Fatalf("failed to add server to registry: %v", err)
	}

	agentHandler := New(st)
	handler := NewMCPHandler(registry, configManager, agentHandler)

	enableReq := httptest.NewRequest(http.MethodPost, "/api/agents/runner/mcp-servers/github/enable", nil)
	enableRR := httptest.NewRecorder()
	handler.EnableAgentMCPServerHandler(enableRR, enableReq)
	if enableRR.Code != http.StatusOK {
		t.Fatalf("expected enable status %d, got %d body=%s", http.StatusOK, enableRR.Code, enableRR.Body.String())
	}

	ag, ok := st.GetAgent("runner")
	if !ok || ag == nil {
		t.Fatalf("agent not found after enable")
	}
	if len(ag.MCPServers) != 1 || ag.MCPServers[0] != "github" {
		t.Fatalf("expected MCPServers [github] after enable, got %v", ag.MCPServers)
	}

	disableReq := httptest.NewRequest(http.MethodPost, "/api/agents/runner/mcp-servers/github/disable", nil)
	disableRR := httptest.NewRecorder()
	handler.DisableAgentMCPServerHandler(disableRR, disableReq)
	if disableRR.Code != http.StatusOK {
		t.Fatalf("expected disable status %d, got %d body=%s", http.StatusOK, disableRR.Code, disableRR.Body.String())
	}

	ag, ok = st.GetAgent("runner")
	if !ok || ag == nil {
		t.Fatalf("agent not found after disable")
	}
	if len(ag.MCPServers) != 0 {
		t.Fatalf("expected empty MCPServers after disable, got %v", ag.MCPServers)
	}
}
