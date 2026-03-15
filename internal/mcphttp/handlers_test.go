package mcphttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnjallday/ori-agent/internal/mcp"
)

func TestEnableServerHandler_EnablesServerGlobally(t *testing.T) {
	configManager := mcp.NewConfigManager(t.TempDir())
	registry := mcp.NewRegistry()
	handler := NewHandler(registry, configManager)

	serverConfig := mcp.ServerConfig{
		Name:      "filesystem",
		Command:   "definitely-not-a-real-command-for-mcp-test",
		Transport: "stdio",
		Enabled:   false,
	}
	if err := configManager.AddServer(serverConfig); err != nil {
		t.Fatalf("failed to save server config: %v", err)
	}
	if err := registry.AddServer(serverConfig); err != nil {
		t.Fatalf("failed to register server: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/mcp/servers/filesystem/enable", nil)
	req.SetPathValue("name", "filesystem")
	rr := httptest.NewRecorder()

	handler.EnableServerHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, rr.Code, rr.Body.String())
	}

	updated, err := configManager.GetServer("filesystem")
	if err != nil {
		t.Fatalf("failed to reload server config: %v", err)
	}
	if !updated.Enabled {
		t.Fatalf("expected filesystem to be globally enabled")
	}

	var payload struct {
		Status string `json:"status"`
		Scope  string `json:"scope"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v body=%s", err, rr.Body.String())
	}
	if payload.Status != "success" || payload.Scope != "global" {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestDisableServerHandler_DisablesServerGlobally(t *testing.T) {
	configManager := mcp.NewConfigManager(t.TempDir())
	registry := mcp.NewRegistry()
	handler := NewHandler(registry, configManager)

	serverConfig := mcp.ServerConfig{
		Name:      "filesystem",
		Command:   "definitely-not-a-real-command-for-mcp-test",
		Transport: "stdio",
		Enabled:   true,
	}
	if err := configManager.AddServer(serverConfig); err != nil {
		t.Fatalf("failed to save server config: %v", err)
	}
	if err := registry.AddServer(serverConfig); err != nil {
		t.Fatalf("failed to register server: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/mcp/servers/filesystem/disable", nil)
	req.SetPathValue("name", "filesystem")
	rr := httptest.NewRecorder()

	handler.DisableServerHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, rr.Code, rr.Body.String())
	}

	updated, err := configManager.GetServer("filesystem")
	if err != nil {
		t.Fatalf("failed to reload server config: %v", err)
	}
	if updated.Enabled {
		t.Fatalf("expected filesystem to be globally disabled")
	}
}

func TestDisableServerHandler_UnknownServerReturnsNotFound(t *testing.T) {
	configManager := mcp.NewConfigManager(t.TempDir())
	registry := mcp.NewRegistry()
	handler := NewHandler(registry, configManager)

	req := httptest.NewRequest(http.MethodPost, "/api/mcp/servers/filesystem/disable", nil)
	req.SetPathValue("name", "filesystem")
	rr := httptest.NewRecorder()

	handler.DisableServerHandler(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusNotFound, rr.Code, rr.Body.String())
	}
}

func TestGetServerToolsHandler_AttemptsLazyStartAndReturnsStartError(t *testing.T) {
	configManager := mcp.NewConfigManager(t.TempDir())
	registry := mcp.NewRegistry()
	handler := NewHandler(registry, configManager)

	if err := registry.AddServer(mcp.ServerConfig{
		Name:      "broken",
		Command:   "definitely-not-a-real-command-for-mcp-test",
		Transport: "stdio",
		Enabled:   false,
	}); err != nil {
		t.Fatalf("failed to add server: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/mcp/servers/broken/tools", nil)
	req.SetPathValue("name", "broken")
	rr := httptest.NewRecorder()

	handler.GetServerToolsHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, rr.Code, rr.Body.String())
	}

	var payload struct {
		Server     string           `json:"server"`
		Status     mcp.ServerStatus `json:"status"`
		StartError string           `json:"start_error"`
		Tools      []interface{}    `json:"tools"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to parse response: %v body=%s", err, rr.Body.String())
	}

	if payload.Server != "broken" {
		t.Fatalf("expected server name 'broken', got %q", payload.Server)
	}
	if payload.StartError == "" {
		t.Fatalf("expected start_error to be populated")
	}
	if payload.Status != mcp.StatusError {
		t.Fatalf("expected status %q after failed start, got %q", mcp.StatusError, payload.Status)
	}
	if len(payload.Tools) != 0 {
		t.Fatalf("expected no tools when start fails, got %d", len(payload.Tools))
	}
}
