package chathttp

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/oriagent/ori-pluginapi"
)

type preflightStore struct {
	agents  map[string]*agent.Agent
	names   []string
	current string
}

func newPreflightStore(name string, ag *agent.Agent) *preflightStore {
	if ag == nil {
		ag = &agent.Agent{}
	}
	return &preflightStore{
		agents:  map[string]*agent.Agent{name: ag},
		names:   []string{name},
		current: name,
	}
}

func (s *preflightStore) ListAgents() ([]string, string) {
	return append([]string{}, s.names...), s.current
}

func (s *preflightStore) SetCurrentAgent(name string) error {
	s.current = name
	return nil
}

func (s *preflightStore) CreateAgent(name string, _ *store.CreateAgentConfig) error {
	if s.agents == nil {
		s.agents = make(map[string]*agent.Agent)
	}
	s.agents[name] = &agent.Agent{}
	s.names = append(s.names, name)
	if s.current == "" {
		s.current = name
	}
	return nil
}

func (s *preflightStore) DeleteAgent(name string) error {
	delete(s.agents, name)
	return nil
}

func (s *preflightStore) GetAgent(name string) (*agent.Agent, bool) {
	ag, ok := s.agents[name]
	return ag, ok
}

func (s *preflightStore) SetAgent(name string, ag *agent.Agent) error {
	if s.agents == nil {
		s.agents = make(map[string]*agent.Agent)
	}
	s.agents[name] = ag
	return nil
}

func (s *preflightStore) ClearAgents() error {
	s.agents = make(map[string]*agent.Agent)
	s.names = nil
	s.current = ""
	return nil
}

func (s *preflightStore) Save() error {
	return nil
}

type preflightRegistry struct {
	started  []string
	startErr error
}

func (r *preflightRegistry) GetToolsForServer(string) ([]pluginapi.PluginTool, error) {
	return nil, nil
}

func (r *preflightRegistry) GetAllTools() []pluginapi.PluginTool {
	return nil
}

func (r *preflightRegistry) StartServer(serverName string) error {
	r.started = append(r.started, serverName)
	return r.startErr
}

type preflightConfigManager struct {
	servers   map[string]struct{}
	enabled   []string
	enableErr error
}

func (m *preflightConfigManager) EnableServerForAgent(agentName, serverName string) error {
	if m.enableErr != nil {
		return m.enableErr
	}
	m.enabled = append(m.enabled, agentName+":"+serverName)
	return nil
}

func (m *preflightConfigManager) GetServer(name string) (*mcp.ServerConfig, error) {
	if _, ok := m.servers[name]; !ok {
		return nil, errors.New("not found")
	}
	return &mcp.ServerConfig{Name: name}, nil
}

func TestMaybeAutoEnableMCPForPrompt_EnablesWebSearchForOri(t *testing.T) {
	st := newPreflightStore("Ori", &agent.Agent{})
	ag, _ := st.GetAgent("Ori")

	reg := &preflightRegistry{}
	cfg := &preflightConfigManager{
		servers: map[string]struct{}{
			"brave-search": {},
		},
	}

	h := &Handler{
		store:            st,
		mcpRegistry:      reg,
		mcpConfigManager: cfg,
	}

	result := h.maybeAutoEnableMCPForPrompt("Ori", ag, "Please search the web for the latest Go release notes")

	updated, _ := st.GetAgent("Ori")
	if !hasAnyMCPServer(updated.MCPServers, []string{"brave-search"}) {
		t.Fatalf("expected brave-search to be enabled, got %v", updated.MCPServers)
	}
	if result == nil {
		t.Fatalf("expected preflight result, got nil")
	}
	if result.serverName != "brave-search" {
		t.Fatalf("expected result server brave-search, got %q", result.serverName)
	}
	if result.userMessage != "" {
		t.Fatalf("expected empty user message, got %q", result.userMessage)
	}
	if len(cfg.enabled) != 1 || cfg.enabled[0] != "Ori:brave-search" {
		t.Fatalf("expected config enable call for Ori:brave-search, got %v", cfg.enabled)
	}
	if len(reg.started) != 1 || reg.started[0] != "brave-search" {
		t.Fatalf("expected server brave-search to be started once, got %v", reg.started)
	}
}

func TestMaybeAutoEnableMCPForPrompt_SkipsNonSystemAgent(t *testing.T) {
	st := newPreflightStore("Researcher", &agent.Agent{})
	ag, _ := st.GetAgent("Researcher")

	reg := &preflightRegistry{}
	cfg := &preflightConfigManager{
		servers: map[string]struct{}{
			"brave-search": {},
		},
	}

	h := &Handler{
		store:            st,
		mcpRegistry:      reg,
		mcpConfigManager: cfg,
	}

	result := h.maybeAutoEnableMCPForPrompt("Researcher", ag, "search the web for release notes")

	if result != nil {
		t.Fatalf("expected nil result for non-system agent, got %+v", result)
	}

	updated, _ := st.GetAgent("Researcher")
	if len(updated.MCPServers) != 0 {
		t.Fatalf("expected MCP servers to remain empty, got %v", updated.MCPServers)
	}
	if len(cfg.enabled) != 0 {
		t.Fatalf("expected no config enable call, got %v", cfg.enabled)
	}
	if len(reg.started) != 0 {
		t.Fatalf("expected no server start call, got %v", reg.started)
	}
}

func TestMaybeAutoEnableMCPForPrompt_SkipsWhenAlreadyEnabled(t *testing.T) {
	st := newPreflightStore("Ori", &agent.Agent{
		MCPServers: []string{"brave-search"},
	})
	ag, _ := st.GetAgent("Ori")

	reg := &preflightRegistry{}
	cfg := &preflightConfigManager{
		servers: map[string]struct{}{
			"brave-search": {},
		},
	}

	h := &Handler{
		store:            st,
		mcpRegistry:      reg,
		mcpConfigManager: cfg,
	}

	result := h.maybeAutoEnableMCPForPrompt("Ori", ag, "web search for latest news")

	if result == nil {
		t.Fatalf("expected result when requirement is detected")
	}
	if result.userMessage != "" {
		t.Fatalf("expected no user message when already enabled, got %q", result.userMessage)
	}

	if len(cfg.enabled) != 0 {
		t.Fatalf("expected no config enable call, got %v", cfg.enabled)
	}
	if len(reg.started) != 0 {
		t.Fatalf("expected no server start call, got %v", reg.started)
	}
}

func TestDetectMCPAutoRequirement_MatchesSearchOnWebPhrase(t *testing.T) {
	req := detectMCPAutoRequirement("search weather on web")
	if req == nil {
		t.Fatalf("expected requirement match, got nil")
	}
	if req.label != "web research" {
		t.Fatalf("expected web research label, got %q", req.label)
	}
}

func TestMaybeAutoEnableMCPForPrompt_ReturnsMessageWhenNoWebServerConfigured(t *testing.T) {
	st := newPreflightStore("Ori", &agent.Agent{})
	ag, _ := st.GetAgent("Ori")

	reg := &preflightRegistry{}
	cfg := &preflightConfigManager{
		servers: map[string]struct{}{},
	}

	h := &Handler{
		store:            st,
		mcpRegistry:      reg,
		mcpConfigManager: cfg,
	}

	result := h.maybeAutoEnableMCPForPrompt("Ori", ag, "search weather on web")
	if result == nil {
		t.Fatalf("expected result, got nil")
	}
	if result.userMessage == "" {
		t.Fatalf("expected user message when no server is available")
	}
	if len(cfg.enabled) != 0 {
		t.Fatalf("expected no enable calls, got %v", cfg.enabled)
	}
	if len(reg.started) != 0 {
		t.Fatalf("expected no start calls, got %v", reg.started)
	}
	if !containsAnyPhrase(result.userMessage, []string{"no web MCP server", "web search"}) {
		t.Fatalf("unexpected user message: %q", result.userMessage)
	}
}

func TestChatHandler_ReturnsHelpfulMessageWhenWebMCPMissing(t *testing.T) {
	st := newPreflightStore("Ori", &agent.Agent{
		Plugins: map[string]types.LoadedPlugin{},
	})

	h := NewHandler(st, nil)
	h.SetMCPRegistry(&preflightRegistry{})
	h.SetMCPConfigManager(&preflightConfigManager{
		servers: map[string]struct{}{},
	})

	body, _ := json.Marshal(map[string]any{
		"question": "search weather on web",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ChatHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v body=%s", err, rr.Body.String())
	}
	text, _ := resp["response"].(string)
	if text == "" {
		t.Fatalf("expected response message, got empty payload: %v", resp)
	}
	if !containsAnyPhrase(strings.ToLower(text), []string{"no web mcp server", "cannot run a real web search"}) {
		t.Fatalf("unexpected response: %q", text)
	}
}
