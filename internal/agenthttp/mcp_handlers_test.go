package agenthttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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

func TestMCPHandler_UpdateFilesystemConfig_PersistsPath(t *testing.T) {
	st := newMCPHandlerTestStore(t)
	if err := st.CreateAgent("runner", &store.CreateAgentConfig{Type: "tool-calling"}); err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	baseDir := t.TempDir()
	configManager := mcp.NewConfigManager(baseDir)
	registry := mcp.NewRegistry()

	serverConfig := mcp.ServerConfig{
		Name:      "filesystem",
		Command:   "npx",
		Args:      []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp/original"},
		Env:       map[string]string{},
		Transport: "stdio",
		Enabled:   false,
	}
	if err := configManager.AddServer(serverConfig); err != nil {
		t.Fatalf("failed to add server config: %v", err)
	}
	if err := registry.AddServer(serverConfig); err != nil {
		t.Fatalf("failed to add server to registry: %v", err)
	}

	agentHandler := New(st)
	handler := NewMCPHandler(registry, configManager, agentHandler)

	updateReq := httptest.NewRequest(http.MethodPut, "/api/agents/runner/mcp-servers/filesystem/config", strings.NewReader(`{"path":"/Users/test/new-allowed-dir"}`))
	updateReq.Header.Set("Content-Type", "application/json")
	updateRR := httptest.NewRecorder()
	handler.UpdateAgentMCPServerConfigHandler(updateRR, updateReq)
	if updateRR.Code != http.StatusOK {
		t.Fatalf("expected update status %d, got %d body=%s", http.StatusOK, updateRR.Code, updateRR.Body.String())
	}

	var response struct {
		Success bool   `json:"success"`
		Server  string `json:"server"`
		Path    string `json:"path"`
	}
	if err := json.Unmarshal(updateRR.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode response: %v body=%s", err, updateRR.Body.String())
	}
	if !response.Success {
		t.Fatalf("expected success response, got %#v", response)
	}
	if response.Server != "filesystem" {
		t.Fatalf("expected server filesystem, got %q", response.Server)
	}
	if response.Path != "/Users/test/new-allowed-dir" {
		t.Fatalf("unexpected response path: %q", response.Path)
	}

	updatedConfig, err := configManager.GetServer("filesystem")
	if err != nil {
		t.Fatalf("failed to load updated config: %v", err)
	}
	if len(updatedConfig.Args) < 3 || updatedConfig.Args[2] != "/Users/test/new-allowed-dir" {
		t.Fatalf("expected saved filesystem path in args[2], got args=%v", updatedConfig.Args)
	}

	runtimeServer, err := registry.GetServer("filesystem")
	if err != nil {
		t.Fatalf("expected runtime server to be present after update: %v", err)
	}
	runtimeConfig := runtimeServer.GetConfig()
	if len(runtimeConfig.Args) < 3 || runtimeConfig.Args[2] != "/Users/test/new-allowed-dir" {
		t.Fatalf("expected runtime filesystem path in args[2], got args=%v", runtimeConfig.Args)
	}
}
