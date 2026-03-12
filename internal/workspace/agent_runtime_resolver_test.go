package workspace

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/store"
)

type resolverAgentStoreStub struct {
	agents map[string]*agent.Agent
}

func (s *resolverAgentStoreStub) ListAgents() ([]string, string) {
	names := make([]string, 0, len(s.agents))
	for name := range s.agents {
		names = append(names, name)
	}
	return names, ""
}

func (s *resolverAgentStoreStub) SetCurrentAgent(string) error { return nil }

func (s *resolverAgentStoreStub) CreateAgent(string, *store.CreateAgentConfig) error { return nil }

func (s *resolverAgentStoreStub) DeleteAgent(string) error { return nil }

func (s *resolverAgentStoreStub) GetAgent(name string) (*agent.Agent, bool) {
	ag, ok := s.agents[name]
	return ag, ok
}

func (s *resolverAgentStoreStub) SetAgent(string, *agent.Agent) error { return nil }

func (s *resolverAgentStoreStub) ClearAgents() error { return nil }

func (s *resolverAgentStoreStub) Save() error { return nil }

var _ store.Store = (*resolverAgentStoreStub)(nil)

type resolverWorkspaceStoreStub struct {
	workspaces map[string]*Workspace
}

func (s *resolverWorkspaceStoreStub) Save(ws *Workspace) error {
	s.workspaces[ws.ID] = ws
	return nil
}

func (s *resolverWorkspaceStoreStub) Get(id string) (*Workspace, error) {
	ws, ok := s.workspaces[id]
	if !ok {
		return nil, errWorkspaceNotFound{id: id}
	}
	return ws, nil
}

func (s *resolverWorkspaceStoreStub) List() ([]string, error) { return nil, nil }

func (s *resolverWorkspaceStoreStub) Delete(string) error { return nil }

func (s *resolverWorkspaceStoreStub) ListActive() ([]*Workspace, error) { return nil, nil }

func (s *resolverWorkspaceStoreStub) GetFilesPath(string) string { return "" }

var _ Store = (*resolverWorkspaceStoreStub)(nil)

type errWorkspaceNotFound struct {
	id string
}

func (e errWorkspaceNotFound) Error() string {
	return "workspace " + e.id + " not found"
}

type runtimeRegistryStub struct {
	configs map[string]mcp.ServerConfig
}

func (r *runtimeRegistryStub) UpsertServer(config mcp.ServerConfig) error {
	if r.configs == nil {
		r.configs = make(map[string]mcp.ServerConfig)
	}
	r.configs[config.Name] = config
	return nil
}

type templateLookupStub struct {
	servers map[string]mcp.ServerConfig
}

func (t *templateLookupStub) GetServer(name string) (*mcp.ServerConfig, error) {
	cfg, ok := t.servers[name]
	if !ok {
		return nil, errTemplateNotFound{name: name}
	}
	copy := cfg
	return &copy, nil
}

type errTemplateNotFound struct {
	name string
}

func (e errTemplateNotFound) Error() string {
	return "template " + e.name + " not found"
}

func TestAgentRuntimeResolver_UsesWorkspaceFilesystemRuntimeServer(t *testing.T) {
	agentStore := &resolverAgentStoreStub{
		agents: map[string]*agent.Agent{
			"Coder": {},
		},
	}
	rootA := filepath.Join(t.TempDir(), "repo-a")
	rootB := filepath.Join(t.TempDir(), "repo-b")
	ws := &Workspace{
		ID: "ws-a",
		AgentInstances: []AgentInstance{
			{ID: "inst-1", Name: "Coder", NodeID: "coder-node-1"},
		},
		DirectoryReferences: []DirectoryReference{
			{ID: "dir-a", Path: rootA},
			{ID: "dir-b", Path: rootB},
		},
		UpdatedAt: time.Now(),
	}
	workspaceStore := &resolverWorkspaceStoreStub{workspaces: map[string]*Workspace{ws.ID: ws}}
	registry := &runtimeRegistryStub{}
	templates := &templateLookupStub{
		servers: map[string]mcp.ServerConfig{
			"filesystem": {
				Name: "filesystem",
				Args: []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp/default-root"},
			},
		},
	}

	resolver := NewAgentRuntimeResolver(agentStore, workspaceStore, registry, templates)
	resolved, err := resolver.ResolveAgentForTask("Coder", Task{
		WorkspaceID:    ws.ID,
		AssignedNodeID: "coder-node-1",
	})
	if err != nil {
		t.Fatalf("ResolveAgentForTask() error = %v", err)
	}

	if len(resolved.MCPServers) != 1 {
		t.Fatalf("expected exactly one runtime MCP server, got %v", resolved.MCPServers)
	}

	runtimeName := runtimeMCPServerName(ws.ID, "filesystem", synthesizedFilesystemBindingID)
	if resolved.MCPServers[0] != runtimeName {
		t.Fatalf("runtime server name = %q, want %q", resolved.MCPServers[0], runtimeName)
	}

	config, ok := registry.configs[runtimeName]
	if !ok {
		t.Fatalf("expected runtime config %q to be materialized", runtimeName)
	}

	if got, want := config.Args, []string{"-y", "@modelcontextprotocol/server-filesystem", rootA, rootB}; len(got) != len(want) {
		t.Fatalf("runtime args length = %d, want %d (%v)", len(got), len(want), got)
	} else {
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("runtime args[%d] = %q, want %q (full=%v)", i, got[i], want[i], got)
			}
		}
	}

}

func TestAgentRuntimeResolver_DifferentWorkspacesGetDifferentRuntimeServers(t *testing.T) {
	agentStore := &resolverAgentStoreStub{
		agents: map[string]*agent.Agent{
			"Coder": {},
		},
	}
	rootA := filepath.Join(t.TempDir(), "repo-a")
	rootB := filepath.Join(t.TempDir(), "repo-b")
	wsA := &Workspace{
		ID: "ws-a",
		AgentInstances: []AgentInstance{
			{ID: "inst-a", Name: "Coder", NodeID: "coder-a"},
		},
		DirectoryReferences: []DirectoryReference{{ID: "dir-a", Path: rootA}},
		UpdatedAt:           time.Now(),
	}
	wsB := &Workspace{
		ID: "ws-b",
		AgentInstances: []AgentInstance{
			{ID: "inst-b", Name: "Coder", NodeID: "coder-b"},
		},
		DirectoryReferences: []DirectoryReference{{ID: "dir-b", Path: rootB}},
		UpdatedAt:           time.Now(),
	}
	workspaceStore := &resolverWorkspaceStoreStub{workspaces: map[string]*Workspace{
		wsA.ID: wsA,
		wsB.ID: wsB,
	}}
	registry := &runtimeRegistryStub{}
	templates := &templateLookupStub{
		servers: map[string]mcp.ServerConfig{
			"filesystem": {
				Name: "filesystem",
				Args: []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp/default-root"},
			},
		},
	}

	resolver := NewAgentRuntimeResolver(agentStore, workspaceStore, registry, templates)
	resolvedA, err := resolver.ResolveAgentForTask("Coder", Task{WorkspaceID: wsA.ID, AssignedNodeID: "coder-a"})
	if err != nil {
		t.Fatalf("ResolveAgentForTask(wsA) error = %v", err)
	}
	resolvedB, err := resolver.ResolveAgentForTask("Coder", Task{WorkspaceID: wsB.ID, AssignedNodeID: "coder-b"})
	if err != nil {
		t.Fatalf("ResolveAgentForTask(wsB) error = %v", err)
	}

	if resolvedA.MCPServers[0] == resolvedB.MCPServers[0] {
		t.Fatalf("expected distinct runtime server names, got %q", resolvedA.MCPServers[0])
	}

	cfgA := registry.configs[resolvedA.MCPServers[0]]
	cfgB := registry.configs[resolvedB.MCPServers[0]]
	if cfgA.Args[len(cfgA.Args)-1] == cfgB.Args[len(cfgB.Args)-1] {
		t.Fatalf("expected workspace-specific roots, got %v and %v", cfgA.Args, cfgB.Args)
	}
}

func TestAgentRuntimeResolver_RespectsAgentInstanceBindingAccess(t *testing.T) {
	agentStore := &resolverAgentStoreStub{
		agents: map[string]*agent.Agent{
			"Coder": {},
		},
	}
	root := filepath.Join(t.TempDir(), "repo")
	ws := &Workspace{
		ID: "ws-access",
		AgentInstances: []AgentInstance{
			{ID: "allowed", Name: "Coder", NodeID: "coder-allowed"},
			{ID: "denied", Name: "Coder", NodeID: "coder-denied"},
		},
		MCPBindings: []WorkspaceMCPBinding{
			{
				ID:         "binding-1",
				ServerName: "filesystem",
				Enabled:    true,
				Scope: map[string]interface{}{
					"roots": []string{root},
				},
			},
		},
		AgentMCPAccess: []WorkspaceAgentMCPAccess{
			{AgentInstanceID: "allowed", EnabledBindingIDs: []string{"binding-1"}},
			{AgentInstanceID: "denied", EnabledBindingIDs: []string{}},
		},
	}
	workspaceStore := &resolverWorkspaceStoreStub{workspaces: map[string]*Workspace{ws.ID: ws}}
	registry := &runtimeRegistryStub{}
	templates := &templateLookupStub{
		servers: map[string]mcp.ServerConfig{
			"filesystem": {
				Name: "filesystem",
				Args: []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp/default-root"},
			},
		},
	}

	resolver := NewAgentRuntimeResolver(agentStore, workspaceStore, registry, templates)
	allowed, err := resolver.ResolveAgentForTask("Coder", Task{WorkspaceID: ws.ID, AssignedNodeID: "coder-allowed"})
	if err != nil {
		t.Fatalf("ResolveAgentForTask(allowed) error = %v", err)
	}
	denied, err := resolver.ResolveAgentForTask("Coder", Task{WorkspaceID: ws.ID, AssignedNodeID: "coder-denied"})
	if err != nil {
		t.Fatalf("ResolveAgentForTask(denied) error = %v", err)
	}

	if len(allowed.MCPServers) != 1 {
		t.Fatalf("allowed agent MCP servers = %v", allowed.MCPServers)
	}
	if len(denied.MCPServers) != 0 {
		t.Fatalf("denied agent should not inherit base filesystem server when workspace binding overrides it, got %v", denied.MCPServers)
	}
}
