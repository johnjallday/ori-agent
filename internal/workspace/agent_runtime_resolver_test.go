package workspace

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/store"
)

type resolverAgentStoreStub struct {
	agents        map[string]*agent.Agent
	createConfigs map[string]*store.CreateAgentConfig
}

func (s *resolverAgentStoreStub) ListAgents() []string {
	names := make([]string, 0, len(s.agents))
	for name := range s.agents {
		names = append(names, name)
	}
	return names
}

func (s *resolverAgentStoreStub) CreateAgent(name string, cfg *store.CreateAgentConfig) error {
	if s.agents == nil {
		s.agents = make(map[string]*agent.Agent)
	}
	if s.createConfigs == nil {
		s.createConfigs = make(map[string]*store.CreateAgentConfig)
	}
	if cfg != nil {
		copyCfg := *cfg
		s.createConfigs[name] = &copyCfg
	} else {
		s.createConfigs[name] = nil
	}
	created := &agent.Agent{}
	if cfg != nil {
		created.Type = cfg.Type
		created.Role = cfg.Role
		created.Settings.SystemPrompt = cfg.SystemPrompt
	}
	s.agents[name] = created
	return nil
}

func (s *resolverAgentStoreStub) DeleteAgent(name string) error {
	if s.agents != nil {
		delete(s.agents, name)
	}
	return nil
}

func (s *resolverAgentStoreStub) GetAgent(name string) (*agent.Agent, bool) {
	ag, ok := s.agents[name]
	return ag, ok
}

func (s *resolverAgentStoreStub) SetAgent(name string, ag *agent.Agent) error {
	if s.agents == nil {
		s.agents = make(map[string]*agent.Agent)
	}
	s.agents[name] = ag
	return nil
}

func (s *resolverAgentStoreStub) UpdateAgent(name string, updateFn func(*agent.Agent) error) error {
	return nil
}

func (s *resolverAgentStoreStub) ClearAgents() error { return nil }

func (s *resolverAgentStoreStub) Save() error { return nil }

var _ store.Store = (*resolverAgentStoreStub)(nil)

func newTestWorkspaceStore(t *testing.T, workspaces ...*Workspace) Store {
	t.Helper()
	s := NewInMemoryStore()
	for _, ws := range workspaces {
		if err := s.Save(ws); err != nil {
			t.Fatalf("failed to seed workspace store: %v", err)
		}
	}
	return s
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
	workspaceStore := newTestWorkspaceStore(t, ws)
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

	runtimeName := RuntimeMCPServerName(ws.ID, "filesystem", synthesizedFilesystemBindingID)
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
	workspaceStore := newTestWorkspaceStore(t, wsA, wsB)
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
				Scope: map[string]any{
					"roots": []string{root},
				},
			},
		},
		AgentMCPAccess: []WorkspaceAgentMCPAccess{
			{AgentInstanceID: "allowed", EnabledBindingIDs: []string{"binding-1"}},
			{AgentInstanceID: "denied", EnabledBindingIDs: []string{}},
		},
	}
	workspaceStore := newTestWorkspaceStore(t, ws)
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

func TestAgentRuntimeResolver_AppliesWorkspaceBindingConfigOverrides(t *testing.T) {
	agentStore := &resolverAgentStoreStub{
		agents: map[string]*agent.Agent{
			"Coder": {},
		},
	}
	ws := &Workspace{
		ID: "ws-config",
		AgentInstances: []AgentInstance{
			{ID: "agent-1", Name: "Coder", NodeID: "coder-1"},
		},
		MCPBindings: []WorkspaceMCPBinding{
			{
				ID:         "binding-1",
				ServerName: "browser",
				Enabled:    true,
				Config: map[string]any{
					"command":   "uvx",
					"args":      []any{"playwright-mcp", "--headless"},
					"transport": "stdio",
					"env": map[string]any{
						"BROWSER_CONTEXT": "workspace",
					},
				},
			},
		},
	}
	workspaceStore := newTestWorkspaceStore(t, ws)
	registry := &runtimeRegistryStub{}
	templates := &templateLookupStub{
		servers: map[string]mcp.ServerConfig{
			"browser": {
				Name:      "browser",
				Command:   "npx",
				Args:      []string{"@playwright/mcp"},
				Transport: "stdio",
				Env: map[string]string{
					"TEMPLATE_ENV": "keep",
				},
			},
		},
	}

	resolver := NewAgentRuntimeResolver(agentStore, workspaceStore, registry, templates)
	resolved, err := resolver.ResolveAgentForTask("Coder", Task{WorkspaceID: ws.ID, AssignedNodeID: "coder-1"})
	if err != nil {
		t.Fatalf("ResolveAgentForTask() error = %v", err)
	}

	if len(resolved.MCPServers) != 1 {
		t.Fatalf("expected exactly one runtime MCP server, got %v", resolved.MCPServers)
	}

	config, ok := registry.configs[resolved.MCPServers[0]]
	if !ok {
		t.Fatalf("expected runtime config for %q to be materialized", resolved.MCPServers[0])
	}
	if config.Command != "uvx" {
		t.Fatalf("runtime command = %q, want uvx", config.Command)
	}
	if len(config.Args) != 2 || config.Args[0] != "playwright-mcp" || config.Args[1] != "--headless" {
		t.Fatalf("runtime args = %v, want overridden args", config.Args)
	}
	if config.Env["TEMPLATE_ENV"] != "keep" {
		t.Fatalf("expected template env to remain, got %v", config.Env)
	}
	if config.Env["BROWSER_CONTEXT"] != "workspace" {
		t.Fatalf("expected workspace env override, got %v", config.Env)
	}
}

func TestAgentRuntimeResolver_UsesFilesystemRootsFromBindingConfig(t *testing.T) {
	agentStore := &resolverAgentStoreStub{
		agents: map[string]*agent.Agent{
			"Coder": {},
		},
	}
	root := filepath.Join(t.TempDir(), "repo-config")
	ws := &Workspace{
		ID: "ws-config-roots",
		AgentInstances: []AgentInstance{
			{ID: "agent-1", Name: "Coder", NodeID: "coder-1"},
		},
		MCPBindings: []WorkspaceMCPBinding{
			{
				ID:         "binding-config-roots",
				ServerName: "filesystem",
				Enabled:    true,
				Config: map[string]any{
					"roots": []any{root},
				},
			},
		},
	}
	workspaceStore := newTestWorkspaceStore(t, ws)
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
	resolved, err := resolver.ResolveAgentForTask("Coder", Task{WorkspaceID: ws.ID, AssignedNodeID: "coder-1"})
	if err != nil {
		t.Fatalf("ResolveAgentForTask() error = %v", err)
	}

	if len(resolved.MCPServers) != 1 {
		t.Fatalf("expected one runtime MCP server, got %v", resolved.MCPServers)
	}

	config, ok := registry.configs[resolved.MCPServers[0]]
	if !ok {
		t.Fatalf("expected runtime config for %q to be materialized", resolved.MCPServers[0])
	}
	if got := config.Args[len(config.Args)-1]; got != root {
		t.Fatalf("expected runtime filesystem root %q, got args=%v", root, config.Args)
	}
}

// --- Skill resolution tests ---

type stubSkillResolver struct {
	skills      map[string]ResolvedSkill
	agentSkills map[string][]ResolvedSkill
}

func (s *stubSkillResolver) ResolveSkillsByNames(names []string) ([]ResolvedSkill, []string, error) {
	var resolved []ResolvedSkill
	var unresolved []string
	for _, name := range names {
		if skill, ok := s.skills[name]; ok {
			resolved = append(resolved, skill)
		} else {
			unresolved = append(unresolved, name)
		}
	}
	return resolved, unresolved, nil
}

func (s *stubSkillResolver) ListEnabledAgentSkills(agentName string) ([]ResolvedSkill, error) {
	return s.agentSkills[agentName], nil
}

func TestResolveEffectiveSkills_WorkspaceOnly(t *testing.T) {
	ws := &Workspace{
		ID: "ws-skill-1",
		AgentInstances: []AgentInstance{
			{ID: "inst-1", Name: "Coder", NodeID: "coder-1"},
		},
		SkillBindings: []WorkspaceSkillBinding{
			{ID: "sb-1", SkillName: "code-review", Enabled: true},
			{ID: "sb-2", SkillName: "testing", Enabled: true},
		},
	}

	agentStore := &resolverAgentStoreStub{agents: map[string]*agent.Agent{
		"Coder": {},
	}}
	workspaceStore := newTestWorkspaceStore(t, ws)
	registry := &runtimeRegistryStub{}
	templates := &templateLookupStub{servers: map[string]mcp.ServerConfig{}}
	skillResolver := &stubSkillResolver{
		skills: map[string]ResolvedSkill{
			"code-review": {Name: "code-review", Prompt: "Review code carefully.", Enabled: true},
			"testing":     {Name: "testing", Prompt: "Write thorough tests.", Enabled: true},
		},
	}

	resolver := NewAgentRuntimeResolver(agentStore, workspaceStore, registry, templates)
	resolver.SetSkillResolver(skillResolver)

	resolved, err := resolver.ResolveAgentForWorkspace("Coder", ws.ID, "")
	if err != nil {
		t.Fatalf("ResolveAgentForWorkspace() error = %v", err)
	}
	if len(resolved.EffectiveSkills) != 2 {
		t.Fatalf("expected 2 effective skills, got %d", len(resolved.EffectiveSkills))
	}
}

func TestResolveEffectiveSkills_PreservesPlanningConfig(t *testing.T) {
	ws := &Workspace{
		ID: "ws-skill-planning",
		AgentInstances: []AgentInstance{
			{ID: "inst-1", Name: "Workspace Manager", NodeID: "workspace-manager-1"},
		},
		SkillBindings: []WorkspaceSkillBinding{
			{
				ID:        "sb-planning",
				SkillName: "workspace-planning",
				Enabled:   true,
				Config: map[string]any{
					"profile_type":           "workspace_planning",
					"mode":                   "feature",
					"tasks_dir":              "tasks",
					"write_prd":              true,
					"default_execution_mode": "step_through",
				},
			},
		},
	}

	agentStore := &resolverAgentStoreStub{agents: map[string]*agent.Agent{
		"Workspace Manager": {},
	}}
	workspaceStore := newTestWorkspaceStore(t, ws)
	registry := &runtimeRegistryStub{}
	templates := &templateLookupStub{servers: map[string]mcp.ServerConfig{}}
	skillResolver := &stubSkillResolver{
		skills: map[string]ResolvedSkill{
			"workspace-planning": {
				Name:            "workspace-planning",
				Prompt:          "Plan work before execution.",
				PlanningProfile: true,
				Enabled:         true,
			},
		},
	}

	resolver := NewAgentRuntimeResolver(agentStore, workspaceStore, registry, templates)
	resolver.SetSkillResolver(skillResolver)

	resolved, err := resolver.ResolveAgentForWorkspace("Workspace Manager", ws.ID, "")
	if err != nil {
		t.Fatalf("ResolveAgentForWorkspace() error = %v", err)
	}
	if len(resolved.EffectiveSkills) != 1 {
		t.Fatalf("expected 1 effective skill, got %d", len(resolved.EffectiveSkills))
	}
	if !resolved.EffectiveSkills[0].PlanningProfile {
		t.Fatal("expected planning profile to be preserved")
	}
	if got := resolved.EffectiveSkills[0].Config["tasks_dir"]; got != "tasks" {
		t.Fatalf("expected tasks_dir config to be preserved, got %#v", got)
	}
}

func TestResolveEffectiveSkills_UsesWorkspaceSettingsManagedPlanningSkill(t *testing.T) {
	ws := &Workspace{
		ID: "ws-managed-planning",
		SharedData: map[string]any{
			"entry_agent_name": "Workspace Manager",
			"workspace_settings": map[string]any{
				"preset": "planner",
				"planning": map[string]any{
					"tasks_dir": "plans",
				},
			},
		},
		AgentInstances: []AgentInstance{
			{ID: "inst-1", Name: "Workspace Manager", NodeID: "workspace-manager-1", EntryPoint: true},
		},
	}

	agentStore := &resolverAgentStoreStub{agents: map[string]*agent.Agent{
		"Workspace Manager": {},
	}}
	workspaceStore := newTestWorkspaceStore(t, ws)
	registry := &runtimeRegistryStub{}
	templates := &templateLookupStub{servers: map[string]mcp.ServerConfig{}}
	skillResolver := &stubSkillResolver{
		skills: map[string]ResolvedSkill{
			"workspace-planning": {
				Name:            "workspace-planning",
				Prompt:          "Plan work before execution.",
				PlanningProfile: true,
				Enabled:         true,
			},
		},
	}

	resolver := NewAgentRuntimeResolver(agentStore, workspaceStore, registry, templates)
	resolver.SetSkillResolver(skillResolver)

	resolved, err := resolver.ResolveAgentForWorkspace("Workspace Manager", ws.ID, "")
	if err != nil {
		t.Fatalf("ResolveAgentForWorkspace() error = %v", err)
	}
	if len(resolved.EffectiveSkills) != 1 {
		t.Fatalf("expected one settings-managed planning skill, got %d", len(resolved.EffectiveSkills))
	}
	if resolved.EffectiveSkills[0].Name != "workspace-planning" {
		t.Fatalf("expected workspace-planning, got %#v", resolved.EffectiveSkills[0])
	}
	if got := resolved.EffectiveSkills[0].Config["tasks_dir"]; got != "plans" {
		t.Fatalf("expected tasks_dir plans from workspace settings, got %#v", got)
	}
}

func TestResolveEffectiveSkills_ManualBindingOverridesWorkspaceSettingsManagedSkill(t *testing.T) {
	ws := &Workspace{
		ID: "ws-managed-planning-override",
		SharedData: map[string]any{
			"entry_agent_name": "Workspace Manager",
			"workspace_settings": map[string]any{
				"preset": "planner",
				"planning": map[string]any{
					"tasks_dir": "managed-plans",
				},
			},
		},
		AgentInstances: []AgentInstance{
			{ID: "inst-1", Name: "Workspace Manager", NodeID: "workspace-manager-1", EntryPoint: true},
		},
		SkillBindings: []WorkspaceSkillBinding{
			{
				ID:        "sb-planning",
				SkillName: "workspace-planning",
				Enabled:   true,
				Config: map[string]any{
					"profile_type": "workspace_planning",
					"tasks_dir":    "manual-plans",
				},
			},
		},
	}

	agentStore := &resolverAgentStoreStub{agents: map[string]*agent.Agent{
		"Workspace Manager": {},
	}}
	workspaceStore := newTestWorkspaceStore(t, ws)
	registry := &runtimeRegistryStub{}
	templates := &templateLookupStub{servers: map[string]mcp.ServerConfig{}}
	skillResolver := &stubSkillResolver{
		skills: map[string]ResolvedSkill{
			"workspace-planning": {
				Name:            "workspace-planning",
				Prompt:          "Plan work before execution.",
				PlanningProfile: true,
				Enabled:         true,
			},
		},
	}

	resolver := NewAgentRuntimeResolver(agentStore, workspaceStore, registry, templates)
	resolver.SetSkillResolver(skillResolver)

	resolved, err := resolver.ResolveAgentForWorkspace("Workspace Manager", ws.ID, "")
	if err != nil {
		t.Fatalf("ResolveAgentForWorkspace() error = %v", err)
	}
	if len(resolved.EffectiveSkills) != 1 {
		t.Fatalf("expected one planning skill, got %d", len(resolved.EffectiveSkills))
	}
	if got := resolved.EffectiveSkills[0].Config["tasks_dir"]; got != "manual-plans" {
		t.Fatalf("expected manual workspace binding config to win, got %#v", got)
	}
}

func TestResolveAgentForWorkspace_MissingEntryAgentReturnsError(t *testing.T) {
	ws := &Workspace{
		ID:   "ws-entry-missing",
		Name: "Spain",
		SharedData: map[string]any{
			"entry_agent_name": "Workspace Manager",
		},
		Agents: []string{"Workspace Manager"},
		AgentInstances: []AgentInstance{
			{ID: "inst-1", Name: "Workspace Manager", NodeID: "workspace-manager-1", EntryPoint: true},
		},
	}

	agentStore := &resolverAgentStoreStub{
		agents: map[string]*agent.Agent{},
	}
	workspaceStore := newTestWorkspaceStore(t, ws)
	registry := &runtimeRegistryStub{}
	templates := &templateLookupStub{servers: map[string]mcp.ServerConfig{}}

	resolver := NewAgentRuntimeResolver(agentStore, workspaceStore, registry, templates)

	_, err := resolver.ResolveAgentForWorkspace("Workspace Manager", ws.ID, "")
	if err == nil {
		t.Fatal("expected error when entry agent does not exist in agent store")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected 'not found' error, got %q", err.Error())
	}
}

func TestResolveAgentForWorkspace_UsesWorkspaceLocalSnapshotWhenGlobalMissing(t *testing.T) {
	ws := &Workspace{
		ID:   "ws-local-agent",
		Name: "Imported",
		SharedData: map[string]any{
			"entry_agent_name": "Woodworking Manager",
		},
		Agents: []string{"Woodworking Manager"},
		AgentInstances: []AgentInstance{
			{ID: "inst-1", Name: "Woodworking Manager", NodeID: "woodworking-manager-1", EntryPoint: true},
		},
	}

	localAgent := &agent.Agent{Type: agent.TypeToolCalling}
	localAgent.Settings.Model = "gpt-5-nano"

	agentStore := &resolverAgentStoreStub{agents: map[string]*agent.Agent{}}
	workspaceStore := newTestWorkspaceStore(t, ws)
	if err := workspaceStore.SaveWorkspaceAgent(ws.ID, "Woodworking Manager", localAgent); err != nil {
		t.Fatalf("seed workspace-local agent: %v", err)
	}

	registry := &runtimeRegistryStub{}
	templates := &templateLookupStub{servers: map[string]mcp.ServerConfig{}}
	resolver := NewAgentRuntimeResolver(agentStore, workspaceStore, registry, templates)

	resolved, err := resolver.ResolveAgentForWorkspace("Woodworking Manager", ws.ID, "")
	if err != nil {
		t.Fatalf("expected workspace-local snapshot to satisfy resolver, got error: %v", err)
	}
	if resolved == nil || resolved.Agent == nil {
		t.Fatal("expected resolved agent, got nil")
	}
	if resolved.Agent.Settings.Model != "gpt-5-nano" {
		t.Fatalf("expected model from local snapshot, got %q", resolved.Agent.Settings.Model)
	}
}

func TestResolveAgentForWorkspace_LocalSnapshotPreferredOverGlobal(t *testing.T) {
	ws := &Workspace{
		ID:     "ws-precedence",
		Name:   "Precedence",
		Agents: []string{"Manager"},
		SharedData: map[string]any{
			"entry_agent_name": "Manager",
		},
		AgentInstances: []AgentInstance{
			{ID: "inst-1", Name: "Manager", NodeID: "manager-1", EntryPoint: true},
		},
	}

	globalAgent := &agent.Agent{Type: agent.TypeGeneral}
	globalAgent.Settings.Model = "global-model"
	localAgent := &agent.Agent{Type: agent.TypeToolCalling}
	localAgent.Settings.Model = "local-model"

	agentStore := &resolverAgentStoreStub{agents: map[string]*agent.Agent{"Manager": globalAgent}}
	workspaceStore := newTestWorkspaceStore(t, ws)
	if err := workspaceStore.SaveWorkspaceAgent(ws.ID, "Manager", localAgent); err != nil {
		t.Fatalf("seed local snapshot: %v", err)
	}

	resolver := NewAgentRuntimeResolver(agentStore, workspaceStore, &runtimeRegistryStub{}, &templateLookupStub{servers: map[string]mcp.ServerConfig{}})
	resolved, err := resolver.ResolveAgentForWorkspace("Manager", ws.ID, "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.Agent.Settings.Model != "local-model" {
		t.Fatalf("expected local snapshot to win, got %q", resolved.Agent.Settings.Model)
	}
}

func TestResolveEffectiveSkills_AgentOverridesWorkspace(t *testing.T) {
	ws := &Workspace{
		ID: "ws-skill-2",
		AgentInstances: []AgentInstance{
			{ID: "inst-1", Name: "Coder", NodeID: "coder-1"},
		},
		SkillBindings: []WorkspaceSkillBinding{
			{ID: "sb-1", SkillName: "code-review", Enabled: true},
		},
	}

	agentStore := &resolverAgentStoreStub{agents: map[string]*agent.Agent{
		"Coder": {},
	}}
	workspaceStore := newTestWorkspaceStore(t, ws)
	registry := &runtimeRegistryStub{}
	templates := &templateLookupStub{servers: map[string]mcp.ServerConfig{}}
	skillResolver := &stubSkillResolver{
		skills: map[string]ResolvedSkill{
			"code-review": {Name: "code-review", Prompt: "Workspace prompt", Enabled: true},
		},
		agentSkills: map[string][]ResolvedSkill{
			"Coder": {
				{Name: "code-review", Prompt: "Agent-specific prompt", Enabled: true},
			},
		},
	}

	resolver := NewAgentRuntimeResolver(agentStore, workspaceStore, registry, templates)
	resolver.SetSkillResolver(skillResolver)

	resolved, err := resolver.ResolveAgentForWorkspace("Coder", ws.ID, "")
	if err != nil {
		t.Fatalf("ResolveAgentForWorkspace() error = %v", err)
	}
	// Agent-specific overrides workspace — should be 1 skill with the agent prompt
	if len(resolved.EffectiveSkills) != 1 {
		t.Fatalf("expected 1 effective skill (agent overrides workspace), got %d", len(resolved.EffectiveSkills))
	}
	if resolved.EffectiveSkills[0].Prompt != "Agent-specific prompt" {
		t.Fatalf("expected agent-specific prompt to win, got %q", resolved.EffectiveSkills[0].Prompt)
	}
}

func TestResolveEffectiveSkills_AccessControlFilters(t *testing.T) {
	ws := &Workspace{
		ID: "ws-skill-3",
		AgentInstances: []AgentInstance{
			{ID: "inst-1", Name: "Coder", NodeID: "coder-1"},
		},
		SkillBindings: []WorkspaceSkillBinding{
			{ID: "sb-1", SkillName: "code-review", Enabled: true},
			{ID: "sb-2", SkillName: "testing", Enabled: true},
			{ID: "sb-3", SkillName: "deploy", Enabled: true},
		},
		AgentSkillAccess: []WorkspaceAgentSkillAccess{
			{AgentInstanceID: "inst-1", EnabledBindingIDs: []string{"sb-1", "sb-3"}},
		},
	}

	agentStore := &resolverAgentStoreStub{agents: map[string]*agent.Agent{
		"Coder": {},
	}}
	workspaceStore := newTestWorkspaceStore(t, ws)
	registry := &runtimeRegistryStub{}
	templates := &templateLookupStub{servers: map[string]mcp.ServerConfig{}}
	skillResolver := &stubSkillResolver{
		skills: map[string]ResolvedSkill{
			"code-review": {Name: "code-review", Prompt: "Review", Enabled: true},
			"testing":     {Name: "testing", Prompt: "Test", Enabled: true},
			"deploy":      {Name: "deploy", Prompt: "Deploy", Enabled: true},
		},
	}

	resolver := NewAgentRuntimeResolver(agentStore, workspaceStore, registry, templates)
	resolver.SetSkillResolver(skillResolver)

	resolved, err := resolver.ResolveAgentForWorkspace("Coder", ws.ID, "coder-1")
	if err != nil {
		t.Fatalf("ResolveAgentForWorkspace() error = %v", err)
	}
	// Access control allows sb-1 and sb-3 only
	if len(resolved.EffectiveSkills) != 2 {
		t.Fatalf("expected 2 skills (access control filtered), got %d", len(resolved.EffectiveSkills))
	}
	names := make(map[string]bool)
	for _, s := range resolved.EffectiveSkills {
		names[s.Name] = true
	}
	if !names["code-review"] || !names["deploy"] {
		t.Fatalf("expected code-review and deploy, got %v", names)
	}
	if names["testing"] {
		t.Fatal("testing should have been filtered out by access control")
	}
}

func TestResolveEffectiveSkills_UnresolvableSkipped(t *testing.T) {
	ws := &Workspace{
		ID: "ws-skill-4",
		AgentInstances: []AgentInstance{
			{ID: "inst-1", Name: "Coder", NodeID: "coder-1"},
		},
		SkillBindings: []WorkspaceSkillBinding{
			{ID: "sb-1", SkillName: "exists", Enabled: true},
			{ID: "sb-2", SkillName: "deleted-skill", Enabled: true},
		},
	}

	agentStore := &resolverAgentStoreStub{agents: map[string]*agent.Agent{
		"Coder": {},
	}}
	workspaceStore := newTestWorkspaceStore(t, ws)
	registry := &runtimeRegistryStub{}
	templates := &templateLookupStub{servers: map[string]mcp.ServerConfig{}}
	skillResolver := &stubSkillResolver{
		skills: map[string]ResolvedSkill{
			"exists": {Name: "exists", Prompt: "I exist", Enabled: true},
		},
	}

	resolver := NewAgentRuntimeResolver(agentStore, workspaceStore, registry, templates)
	resolver.SetSkillResolver(skillResolver)

	resolved, err := resolver.ResolveAgentForWorkspace("Coder", ws.ID, "")
	if err != nil {
		t.Fatalf("ResolveAgentForWorkspace() error = %v", err)
	}
	// Only the resolvable skill should be returned
	if len(resolved.EffectiveSkills) != 1 {
		t.Fatalf("expected 1 skill (unresolvable skipped), got %d", len(resolved.EffectiveSkills))
	}
	if resolved.EffectiveSkills[0].Name != "exists" {
		t.Fatalf("expected 'exists' skill, got %q", resolved.EffectiveSkills[0].Name)
	}
}

func TestResolveEffectiveSkills_NoSkillResolver(t *testing.T) {
	ws := &Workspace{
		ID: "ws-skill-5",
		AgentInstances: []AgentInstance{
			{ID: "inst-1", Name: "Coder", NodeID: "coder-1"},
		},
		SkillBindings: []WorkspaceSkillBinding{
			{ID: "sb-1", SkillName: "code-review", Enabled: true},
		},
	}

	agentStore := &resolverAgentStoreStub{agents: map[string]*agent.Agent{
		"Coder": {},
	}}
	workspaceStore := newTestWorkspaceStore(t, ws)
	registry := &runtimeRegistryStub{}
	templates := &templateLookupStub{servers: map[string]mcp.ServerConfig{}}

	resolver := NewAgentRuntimeResolver(agentStore, workspaceStore, registry, templates)
	// No skill resolver set

	resolved, err := resolver.ResolveAgentForWorkspace("Coder", ws.ID, "")
	if err != nil {
		t.Fatalf("ResolveAgentForWorkspace() error = %v", err)
	}
	if len(resolved.EffectiveSkills) != 0 {
		t.Fatalf("expected 0 skills when no resolver set, got %d", len(resolved.EffectiveSkills))
	}
}
