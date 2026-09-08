package mcphttp

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/resetstate"
)

func TestResetAdmissionMCPConnectTransfersBeforeResponse(t *testing.T) {
	registry := mcp.NewRegistry()
	config := mcp.NewConfigManager(t.TempDir())
	server := mcp.ServerConfig{Name: "owned", Command: "fixture", Transport: mcp.TransportStdio}
	if err := config.AddServer(server); err != nil {
		t.Fatal(err)
	}
	if err := registry.AddServer(server); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(registry, config)
	gate := &resetstate.WorkGate{}
	handler.SetAdmissionGate(gate)
	started, finish, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	handler.startServer = func(string) error { close(started); <-finish; close(done); return nil }
	req := httptest.NewRequest(http.MethodPost, "/api/mcp/servers/owned/connect", nil)
	req.SetPathValue("name", "owned")
	response := httptest.NewRecorder()
	handler.ConnectServerHandler(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("connect status = %d: %s", response.Code, response.Body.String())
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("detached MCP connect did not start")
	}
	if err := gate.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
		t.Fatalf("connect child not tracked after response: %v", err)
	}
	release, err := gate.Enter()
	if err != nil {
		t.Fatalf("busy refusal fenced new work: %v", err)
	}
	release()
	close(finish)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("MCP connect child did not finish")
	}
	deadline := time.Now().Add(5 * time.Second)
	for gate.Snapshot().Active != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestResetAdmissionMCPConnectRefusesBeforeLaunch(t *testing.T) {
	registry := mcp.NewRegistry()
	config := mcp.NewConfigManager(t.TempDir())
	server := mcp.ServerConfig{Name: "owned", Command: "fixture", Transport: mcp.TransportStdio}
	if err := config.AddServer(server); err != nil {
		t.Fatal(err)
	}
	if err := registry.AddServer(server); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(registry, config)
	gate := &resetstate.WorkGate{}
	handler.SetAdmissionGate(gate)
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	var calls int
	handler.startServer = func(string) error { calls++; return nil }
	req := httptest.NewRequest(http.MethodPost, "/api/mcp/servers/owned/connect", nil)
	req.SetPathValue("name", "owned")
	response := httptest.NewRecorder()
	handler.ConnectServerHandler(response, req)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("fenced connect status = %d: %s", response.Code, response.Body.String())
	}
	if calls != 0 {
		t.Fatal("fenced connect launched child")
	}
}
