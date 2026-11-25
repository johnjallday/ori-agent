package chathttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/toolapi"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Shared chat test fixtures. They once lived beside the MCP preflight, which
// was retired with Ori's protected system assistant; the fixtures themselves
// are generic in-memory stores and registries.

type preflightStore struct {
	agents map[string]*agent.Agent
	names  []string
}

func newPreflightStore(name string, ag *agent.Agent) *preflightStore {
	if ag == nil {
		ag = &agent.Agent{}
	}
	return &preflightStore{
		agents: map[string]*agent.Agent{name: ag},
		names:  []string{name},
	}
}

func (s *preflightStore) ListAgents() []string {
	return append([]string{}, s.names...)
}

func (s *preflightStore) CreateAgent(name string, _ *store.CreateAgentConfig) error {
	if s.agents == nil {
		s.agents = make(map[string]*agent.Agent)
	}
	s.agents[name] = &agent.Agent{}
	s.names = append(s.names, name)
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

func (s *preflightStore) UpdateAgent(name string, updateFn func(*agent.Agent) error) error {
	ag, ok := s.agents[name]
	if !ok || ag == nil {
		return fmt.Errorf("agent %q not found", name)
	}
	return updateFn(ag)
}

func (s *preflightStore) ClearAgents() error {
	s.agents = make(map[string]*agent.Agent)
	s.names = nil
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

func (r *preflightRegistry) GetToolsForServer(string) ([]toolapi.Tool, error) {
	return nil, nil
}

func (r *preflightRegistry) GetAllTools() []toolapi.Tool {
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

func (s *preflightWorkspaceStore) GetOutputsPath(workspaceID string) string {
	return "/tmp/" + workspaceID + "/outputs"
}

func (s *preflightWorkspaceStore) GetWorkspaceAgent(workspaceID, agentName string) (*agent.Agent, bool, error) {
	return nil, false, nil
}

func (s *preflightWorkspaceStore) SaveWorkspaceAgent(workspaceID, agentName string, ag *agent.Agent) error {
	return nil
}

func (s *preflightWorkspaceStore) Lock(wsID string) func() { return func() {} }

func (s *preflightWorkspaceStore) Update(wsID string, fn func(*workspace.Workspace) error) error {
	return workspace.CanonicalUpdate(s, wsID, fn)
}

func TestChatHandler_UsesNativeWebSearchWithoutMCP(t *testing.T) {
	st := newPreflightStore("Scout", &agent.Agent{})

	h := NewHandler(st, nil)
	h.SetUtilityToolRegistry(NewUtilityToolRegistry(UtilityAdapters{
		WebSearch: preflightWebSearchAdapter{},
	}, DefaultUtilityCallPolicy()))
	h.SetMCPRegistry(&preflightRegistry{})
	h.SetMCPConfigManager(&preflightConfigManager{
		servers: map[string]struct{}{},
	})

	body, _ := json.Marshal(map[string]any{
		"question":   "search weather on web",
		"agent_name": "Scout",
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
