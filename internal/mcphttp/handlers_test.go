package mcphttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/store"
)

type stubStore struct {
	names   []string
	current string
	agents  map[string]*agent.Agent
}

func newStubStore(names []string, current string) *stubStore {
	agents := make(map[string]*agent.Agent, len(names))
	for _, name := range names {
		agents[name] = &agent.Agent{}
	}
	return &stubStore{
		names:   append([]string{}, names...),
		current: current,
		agents:  agents,
	}
}

func (s *stubStore) ListAgents() (names []string, current string) {
	return append([]string{}, s.names...), s.current
}

func (s *stubStore) SetCurrentAgent(name string) error {
	s.current = name
	return nil
}

func (s *stubStore) CreateAgent(name string, _ *store.CreateAgentConfig) error {
	if s.agents == nil {
		s.agents = map[string]*agent.Agent{}
	}
	s.agents[name] = &agent.Agent{}
	s.names = append(s.names, name)
	if s.current == "" {
		s.current = name
	}
	return nil
}

func (s *stubStore) DeleteAgent(name string) error {
	delete(s.agents, name)
	filtered := make([]string, 0, len(s.names))
	for _, existing := range s.names {
		if existing == name {
			continue
		}
		filtered = append(filtered, existing)
	}
	s.names = filtered
	if s.current == name {
		s.current = ""
	}
	return nil
}

func (s *stubStore) GetAgent(name string) (*agent.Agent, bool) {
	ag, ok := s.agents[name]
	return ag, ok
}

func (s *stubStore) SetAgent(name string, ag *agent.Agent) error {
	if s.agents == nil {
		s.agents = map[string]*agent.Agent{}
	}
	s.agents[name] = ag
	return nil
}

func (s *stubStore) ClearAgents() error {
	s.agents = map[string]*agent.Agent{}
	s.names = nil
	s.current = ""
	return nil
}

func (s *stubStore) Save() error {
	return nil
}

func TestDisableServerHandler_UsesBodyAgentWhenCurrentMissing(t *testing.T) {
	st := newStubStore([]string{"runner"}, "")
	configManager := mcp.NewConfigManager(t.TempDir())
	registry := mcp.NewRegistry()
	handler := NewHandler(registry, configManager, st)

	if err := configManager.EnableServerForAgent("runner", "filesystem"); err != nil {
		t.Fatalf("failed to pre-enable server: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/mcp/servers/filesystem/disable", strings.NewReader(`{"agent_name":"runner"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.DisableServerHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, rr.Code, rr.Body.String())
	}

	enabled, err := configManager.IsServerEnabledForAgent("runner", "filesystem")
	if err != nil {
		t.Fatalf("failed to check agent server status: %v", err)
	}
	if enabled {
		t.Fatalf("filesystem should be disabled for runner")
	}
}

func TestDisableServerHandler_FallsBackToFirstAgentWhenCurrentMissing(t *testing.T) {
	st := newStubStore([]string{"runner"}, "")
	configManager := mcp.NewConfigManager(t.TempDir())
	registry := mcp.NewRegistry()
	handler := NewHandler(registry, configManager, st)

	if err := configManager.EnableServerForAgent("runner", "filesystem"); err != nil {
		t.Fatalf("failed to pre-enable server: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/mcp/servers/filesystem/disable", nil)
	rr := httptest.NewRecorder()

	handler.DisableServerHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, rr.Code, rr.Body.String())
	}

	enabled, err := configManager.IsServerEnabledForAgent("runner", "filesystem")
	if err != nil {
		t.Fatalf("failed to check agent server status: %v", err)
	}
	if enabled {
		t.Fatalf("filesystem should be disabled for runner")
	}
}

func TestDisableServerHandler_UnknownBodyAgentReturnsNotFound(t *testing.T) {
	st := newStubStore([]string{"runner"}, "")
	configManager := mcp.NewConfigManager(t.TempDir())
	registry := mcp.NewRegistry()
	handler := NewHandler(registry, configManager, st)

	req := httptest.NewRequest(http.MethodPost, "/api/mcp/servers/filesystem/disable", strings.NewReader(`{"agent_name":"missing"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.DisableServerHandler(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusNotFound, rr.Code, rr.Body.String())
	}
}

func TestGetServerToolsHandler_AttemptsLazyStartAndReturnsStartError(t *testing.T) {
	st := newStubStore([]string{"runner"}, "runner")
	configManager := mcp.NewConfigManager(t.TempDir())
	registry := mcp.NewRegistry()
	handler := NewHandler(registry, configManager, st)

	if err := registry.AddServer(mcp.ServerConfig{
		Name:      "broken",
		Command:   "definitely-not-a-real-command-for-mcp-test",
		Transport: "stdio",
		Enabled:   false,
	}); err != nil {
		t.Fatalf("failed to add server: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/mcp/servers/broken/tools", nil)
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
