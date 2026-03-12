package chathttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
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
	configs  map[string]mcp.ServerConfig
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

func (r *preflightRegistry) UpsertServer(config mcp.ServerConfig) error {
	if r.configs == nil {
		r.configs = make(map[string]mcp.ServerConfig)
	}
	r.configs[config.Name] = config
	return nil
}

type preflightConfigManager struct {
	servers   map[string]struct{}
	enabled   []string
	enableErr error
}

type preflightWebSearchAdapter struct{}

func (preflightWebSearchAdapter) WebSearch(_ context.Context, req WebSearchRequest) (WebSearchResponse, error) {
	return WebSearchResponse{
		Query: req.Query,
		Results: []WebSearchResult{
			{
				Title:   "Mock Result",
				URL:     "https://example.com",
				Snippet: "mock snippet",
			},
		},
		Source: "test",
	}, nil
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

type preflightWorkspaceStore struct {
	workspaces map[string]*workspace.Workspace
}

func (s *preflightWorkspaceStore) Save(ws *workspace.Workspace) error {
	if s.workspaces == nil {
		s.workspaces = make(map[string]*workspace.Workspace)
	}
	s.workspaces[ws.ID] = ws
	return nil
}

func (s *preflightWorkspaceStore) Get(id string) (*workspace.Workspace, error) {
	ws, ok := s.workspaces[id]
	if !ok {
		return nil, errors.New("workspace not found")
	}
	return ws, nil
}

func (s *preflightWorkspaceStore) List() ([]string, error) {
	out := make([]string, 0, len(s.workspaces))
	for id := range s.workspaces {
		out = append(out, id)
	}
	return out, nil
}

func (s *preflightWorkspaceStore) Delete(id string) error {
	delete(s.workspaces, id)
	return nil
}

func (s *preflightWorkspaceStore) ListActive() ([]*workspace.Workspace, error) {
	out := make([]*workspace.Workspace, 0, len(s.workspaces))
	for _, ws := range s.workspaces {
		out = append(out, ws)
	}
	return out, nil
}

func (s *preflightWorkspaceStore) GetFilesPath(workspaceID string) string {
	return "/tmp/" + workspaceID
}

func TestMaybeAutoEnableMCPForPrompt_EnablesWebSearchForOri(t *testing.T) {
	st := newPreflightStore("Ori", &agent.Agent{})
	ag, _ := st.GetAgent("Ori")
	runtimeAgent := &resolvedChatAgent{Agent: ag}
	wsStore := &preflightWorkspaceStore{
		workspaces: map[string]*workspace.Workspace{
			"ws-1": {
				ID:             "ws-1",
				Name:           "Workspace",
				AgentInstances: []workspace.AgentInstance{{ID: "agent-1", Name: "Ori", CreatedAt: time.Now()}},
			},
		},
	}

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
		workspaceStore:   wsStore,
	}
	h.SetRuntimeResolver(workspace.NewAgentRuntimeResolver(st, wsStore, reg, cfg))

	result, updatedAgent := h.maybeAutoEnableMCPForPrompt(
		"Ori",
		runtimeAgent,
		"Please search the web for the latest Go release notes",
		normalizedChatRouteContext{WorkspaceID: "ws-1"},
	)

	if result == nil {
		t.Fatalf("expected preflight result, got nil")
	}
	if result.serverName != "brave-search" {
		t.Fatalf("expected result server brave-search, got %q", result.serverName)
	}
	if result.userMessage != "" {
		t.Fatalf("expected empty user message, got %q", result.userMessage)
	}

	ws, err := wsStore.Get("ws-1")
	if err != nil {
		t.Fatalf("expected workspace to exist: %v", err)
	}
	bindings := ws.GetMCPBindings()
	if len(bindings) != 1 {
		t.Fatalf("expected 1 workspace MCP binding, got %d", len(bindings))
	}
	if bindings[0].ServerName != "brave-search" {
		t.Fatalf("expected workspace binding brave-search, got %q", bindings[0].ServerName)
	}
	if updatedAgent == nil || !hasAnyMCPServer(updatedAgent.MCPServers, []string{"brave-search"}) {
		t.Fatalf("expected resolved agent to include brave-search, got %v", updatedAgent)
	}
	if len(cfg.enabled) != 0 {
		t.Fatalf("expected no agent-scoped enable calls, got %v", cfg.enabled)
	}
	if len(reg.started) != 0 {
		t.Fatalf("expected no eager server start, got %v", reg.started)
	}
}

func TestMaybeAutoEnableMCPForPrompt_SkipsNonSystemAgent(t *testing.T) {
	st := newPreflightStore("Researcher", &agent.Agent{})
	ag, _ := st.GetAgent("Researcher")
	runtimeAgent := &resolvedChatAgent{Agent: ag}

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

	result, updatedAgent := h.maybeAutoEnableMCPForPrompt(
		"Researcher",
		runtimeAgent,
		"search the web for release notes",
		normalizedChatRouteContext{WorkspaceID: "ws-1"},
	)

	if result != nil {
		t.Fatalf("expected nil result for non-system agent, got %+v", result)
	}
	if updatedAgent != nil {
		t.Fatalf("expected no resolved agent override for non-system agent, got %+v", updatedAgent)
	}
	if len(cfg.enabled) != 0 {
		t.Fatalf("expected no config enable call, got %v", cfg.enabled)
	}
	if len(reg.started) != 0 {
		t.Fatalf("expected no server start call, got %v", reg.started)
	}
}

func TestMaybeAutoEnableMCPForPrompt_SkipsWhenAlreadyEnabled(t *testing.T) {
	st := newPreflightStore("Ori", &agent.Agent{})
	ag, _ := st.GetAgent("Ori")
	runtimeAgent := &resolvedChatAgent{
		Agent:      ag,
		MCPServers: []string{"ws:ws-1:mcp:brave-search:auto-brave-search"},
	}

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

	result, updatedAgent := h.maybeAutoEnableMCPForPrompt(
		"Ori",
		runtimeAgent,
		"web search for latest news",
		normalizedChatRouteContext{WorkspaceID: "ws-1"},
	)

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
	if updatedAgent != nil {
		t.Fatalf("expected no refreshed agent when already enabled, got %+v", updatedAgent)
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

func TestDetectMCPAutoRequirement_MatchesOpenDomainAsBrowserAutomation(t *testing.T) {
	req := detectMCPAutoRequirement("open instagram.com")
	if req == nil {
		t.Fatalf("expected browser automation requirement match, got nil")
	}
	if req.label != "browser automation" {
		t.Fatalf("expected browser automation label, got %q", req.label)
	}
}

func TestMaybeAutoEnableMCPForPrompt_ReturnsMessageWhenNoWebServerConfigured(t *testing.T) {
	st := newPreflightStore("Ori", &agent.Agent{})
	ag, _ := st.GetAgent("Ori")
	runtimeAgent := &resolvedChatAgent{Agent: ag}

	reg := &preflightRegistry{}
	cfg := &preflightConfigManager{
		servers: map[string]struct{}{},
	}

	h := &Handler{
		store:            st,
		mcpRegistry:      reg,
		mcpConfigManager: cfg,
	}

	result, _ := h.maybeAutoEnableMCPForPrompt(
		"Ori",
		runtimeAgent,
		"search weather on web",
		normalizedChatRouteContext{WorkspaceID: "ws-1"},
	)
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

func TestMaybeAutoEnableMCPForPrompt_RequiresWorkspaceContext(t *testing.T) {
	st := newPreflightStore("Ori", &agent.Agent{})
	ag, _ := st.GetAgent("Ori")
	runtimeAgent := &resolvedChatAgent{Agent: ag}

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

	result, updatedAgent := h.maybeAutoEnableMCPForPrompt("Ori", runtimeAgent, "search weather on web", normalizedChatRouteContext{})
	if result == nil {
		t.Fatalf("expected result, got nil")
	}
	if result.userMessage == "" || !strings.Contains(strings.ToLower(result.userMessage), "workspace") {
		t.Fatalf("expected workspace-required message, got %q", result.userMessage)
	}
	if updatedAgent != nil {
		t.Fatalf("expected no updated agent without workspace context, got %+v", updatedAgent)
	}
}

func TestChatHandler_UsesNativeWebSearchWithoutMCP(t *testing.T) {
	st := newPreflightStore("Ori", &agent.Agent{
		Plugins: map[string]types.LoadedPlugin{},
	})

	h := NewHandler(st, nil)
	h.SetUtilityToolRegistry(NewUtilityToolRegistry(UtilityAdapters{
		WebSearch: preflightWebSearchAdapter{},
	}, DefaultUtilityCallPolicy()))
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
	if !containsAnyPhrase(strings.ToLower(text), []string{"top web results", "mock result"}) {
		t.Fatalf("unexpected response: %q", text)
	}
	if mode, _ := resp["route_mode"].(string); mode != string(UtilityRouteDirect) {
		t.Fatalf("expected route_mode %q, got %v", UtilityRouteDirect, resp["route_mode"])
	}
}
