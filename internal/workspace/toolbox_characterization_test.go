package workspace

import (
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/mcp"
)

// Characterization tests for the pre-Toolbox capability resolution behavior
// (tasks 1.1; PRD FR-1–FR-7, FR-16–FR-17, FR-24–FR-32).
//
// These pin the *legacy* effective capability set that migration must preserve:
// what an agent instance could actually use before named Toolboxes existed.
// Task 1.9 migrates each of these shapes into an explicit `Workspace Default`
// Toolbox, and the migration tests assert the migrated Toolbox resolves to the
// exact same capabilities these tests record.
//
// Two of these deliberately record behavior the feature CHANGES rather than
// preserves — implicit inheritance of every enabled binding, and silent
// adoption of newly added bindings. They are marked as such so a future reader
// does not mistake the recorded behavior for the intended contract.

func newCharacterizationResolver(t *testing.T, ws *Workspace, agents map[string]*agent.Agent, skillResolver SkillResolver) *AgentRuntimeResolver {
	t.Helper()

	agentStore := &resolverAgentStoreStub{agents: agents}
	workspaceStore := newTestWorkspaceStore(t, ws)
	registry := &runtimeRegistryStub{}
	templates := &templateLookupStub{servers: map[string]mcp.ServerConfig{
		"filesystem": {
			Name: "filesystem",
			Args: []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp/default-root"},
		},
		"notes": {Name: "notes"},
		"docs":  {Name: "docs"},
	}}

	resolver := NewAgentRuntimeResolver(agentStore, workspaceStore, registry, templates)
	if skillResolver != nil {
		resolver.SetSkillResolver(skillResolver)
	}
	return resolver
}

func resolvedSkillNames(skills []ResolvedSkill) []string {
	names := make([]string, 0, len(skills))
	for _, s := range skills {
		names = append(names, strings.TrimSpace(s.Name))
	}
	sort.Strings(names)
	return names
}

func assertStringsEqual(t *testing.T, label string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: got %v, want %v", label, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s: got %v, want %v", label, got, want)
		}
	}
}

// Legacy behavior: globally enabled agent skills are appended to every
// workspace resolution, regardless of workspace bindings (FR-3, FR-28).
func TestCharacterization_GlobalLearnedSkillsAlwaysResolve(t *testing.T) {
	ws := &Workspace{
		ID: "ws-char-learned",
		AgentInstances: []AgentInstance{
			{ID: "inst-1", Name: "Coder", NodeID: "coder-1"},
		},
	}
	skillResolver := &stubSkillResolver{
		agentSkills: map[string][]ResolvedSkill{
			"Coder": {{Name: "code-review", Prompt: "Review carefully.", Enabled: true}},
		},
	}

	resolver := newCharacterizationResolver(t, ws, map[string]*agent.Agent{"Coder": {}}, skillResolver)
	resolved, err := resolver.ResolveAgentForWorkspace("Coder", ws.ID, "coder-1")
	if err != nil {
		t.Fatalf("ResolveAgentForWorkspace() error = %v", err)
	}

	assertStringsEqual(t, "effective skills", resolvedSkillNames(resolved.EffectiveSkills), []string{"code-review"})
}

// Legacy behavior: a workspace skill binding is available to an instance that
// has no explicit access entry — implicit inheritance (FR-32 CHANGES this).
func TestCharacterization_ImplicitSkillBindingInheritance(t *testing.T) {
	ws := &Workspace{
		ID: "ws-char-implicit-skill",
		AgentInstances: []AgentInstance{
			{ID: "inst-1", Name: "Coder", NodeID: "coder-1"},
		},
		SkillBindings: []SkillBinding{
			{ID: "sb-1", SkillName: "testing", Enabled: true},
			{ID: "sb-2", SkillName: "drafting", Enabled: true},
			{ID: "sb-3", SkillName: "disabled-skill", Enabled: false},
		},
	}
	skillResolver := &stubSkillResolver{
		skills: map[string]ResolvedSkill{
			"testing":        {Name: "testing", Prompt: "Test.", Enabled: true},
			"drafting":       {Name: "drafting", Prompt: "Draft.", Enabled: true},
			"disabled-skill": {Name: "disabled-skill", Prompt: "Nope.", Enabled: true},
		},
	}

	resolver := newCharacterizationResolver(t, ws, map[string]*agent.Agent{"Coder": {}}, skillResolver)
	resolved, err := resolver.ResolveAgentForWorkspace("Coder", ws.ID, "coder-1")
	if err != nil {
		t.Fatalf("ResolveAgentForWorkspace() error = %v", err)
	}

	assertStringsEqual(t, "inherited workspace skills", resolvedSkillNames(resolved.EffectiveSkills), []string{"drafting", "testing"})
}

// Legacy behavior: on a case-insensitive name collision the agent-learned skill
// wins and the workspace binding is dropped. Migration must pin exactly this
// winning source rather than re-deriving it (FR-6, FR-30).
func TestCharacterization_AgentSkillWinsSourceCollision(t *testing.T) {
	ws := &Workspace{
		ID: "ws-char-collision",
		AgentInstances: []AgentInstance{
			{ID: "inst-1", Name: "Coder", NodeID: "coder-1"},
		},
		SkillBindings: []SkillBinding{
			{ID: "sb-1", SkillName: "Code-Review", Enabled: true, Trusted: true},
		},
	}
	skillResolver := &stubSkillResolver{
		agentSkills: map[string][]ResolvedSkill{
			"Coder": {{Name: "code-review", Prompt: "Agent-learned prompt.", Source: "agent", Enabled: true}},
		},
		skills: map[string]ResolvedSkill{
			"Code-Review": {Name: "Code-Review", Prompt: "Workspace prompt.", Source: "repo", Enabled: true},
		},
	}

	resolver := newCharacterizationResolver(t, ws, map[string]*agent.Agent{"Coder": {}}, skillResolver)
	resolved, err := resolver.ResolveAgentForWorkspace("Coder", ws.ID, "coder-1")
	if err != nil {
		t.Fatalf("ResolveAgentForWorkspace() error = %v", err)
	}

	if len(resolved.EffectiveSkills) != 1 {
		t.Fatalf("expected the collision to resolve to exactly one skill, got %v", resolvedSkillNames(resolved.EffectiveSkills))
	}
	if got := resolved.EffectiveSkills[0].Prompt; got != "Agent-learned prompt." {
		t.Fatalf("expected the agent-learned source to win the collision, got prompt %q", got)
	}
}

// Legacy behavior: an instance with no MCP access entry inherits every enabled
// workspace binding, and disabled bindings are excluded (FR-32 CHANGES the
// inheritance half of this).
func TestCharacterization_ImplicitMCPBindingInheritance(t *testing.T) {
	ws := &Workspace{
		ID: "ws-char-implicit-mcp",
		AgentInstances: []AgentInstance{
			{ID: "inst-1", Name: "Coder", NodeID: "coder-1"},
		},
		MCPBindings: []MCPBinding{
			{ID: "mb-1", ServerName: "notes", Enabled: true},
			{ID: "mb-2", ServerName: "docs", Enabled: true},
			{ID: "mb-3", ServerName: "notes", Enabled: false},
		},
	}

	resolver := newCharacterizationResolver(t, ws, map[string]*agent.Agent{"Coder": {}}, nil)
	resolved, err := resolver.ResolveAgentForWorkspace("Coder", ws.ID, "coder-1")
	if err != nil {
		t.Fatalf("ResolveAgentForWorkspace() error = %v", err)
	}

	want := []string{
		RuntimeMCPServerName(ws.ID, "notes", "mb-1"),
		RuntimeMCPServerName(ws.ID, "docs", "mb-2"),
	}
	assertStringsEqual(t, "inherited MCP servers", resolved.MCPServers, want)
}

// Legacy behavior: an explicit access entry narrows to exactly the listed
// bindings, and an empty list means no bindings at all (not "all").
func TestCharacterization_ExplicitMCPAccessNarrowsAndEmptyDenies(t *testing.T) {
	newWS := func(id string, enabledIDs []string) *Workspace {
		return &Workspace{
			ID: id,
			AgentInstances: []AgentInstance{
				{ID: "inst-1", Name: "Coder", NodeID: "coder-1"},
			},
			MCPBindings: []MCPBinding{
				{ID: "mb-1", ServerName: "notes", Enabled: true},
				{ID: "mb-2", ServerName: "docs", Enabled: true},
			},
			AgentMCPAccess: []AgentMCPAccess{
				{AgentInstanceID: "inst-1", EnabledBindingIDs: enabledIDs},
			},
		}
	}

	narrowed := newWS("ws-char-narrow", []string{"mb-2"})
	resolver := newCharacterizationResolver(t, narrowed, map[string]*agent.Agent{"Coder": {}}, nil)
	resolved, err := resolver.ResolveAgentForWorkspace("Coder", narrowed.ID, "coder-1")
	if err != nil {
		t.Fatalf("ResolveAgentForWorkspace() error = %v", err)
	}
	assertStringsEqual(t, "narrowed MCP servers", resolved.MCPServers, []string{
		RuntimeMCPServerName(narrowed.ID, "docs", "mb-2"),
	})

	denied := newWS("ws-char-denied", nil)
	deniedResolver := newCharacterizationResolver(t, denied, map[string]*agent.Agent{"Coder": {}}, nil)
	deniedResolved, err := deniedResolver.ResolveAgentForWorkspace("Coder", denied.ID, "coder-1")
	if err != nil {
		t.Fatalf("ResolveAgentForWorkspace() error = %v", err)
	}
	if len(deniedResolved.MCPServers) != 0 {
		t.Fatalf("expected an empty access list to deny every binding, got %v", deniedResolved.MCPServers)
	}
}

// Legacy behavior: a binding with a restricted AllowedTools list contributes a
// tool allowlist entry; a nil AllowedTools ("all tools") contributes none.
// FR-13 forbids new Toolboxes from carrying the nil/all-tools semantics.
func TestCharacterization_AllowedToolsRestrictionAndAllToolsDefault(t *testing.T) {
	ws := &Workspace{
		ID: "ws-char-allowlist",
		AgentInstances: []AgentInstance{
			{ID: "inst-1", Name: "Coder", NodeID: "coder-1"},
		},
		MCPBindings: []MCPBinding{
			{ID: "mb-1", ServerName: "notes", Enabled: true, AllowedTools: []string{"read_note"}},
			{ID: "mb-2", ServerName: "docs", Enabled: true},
		},
	}

	resolver := newCharacterizationResolver(t, ws, map[string]*agent.Agent{"Coder": {}}, nil)
	resolved, err := resolver.ResolveAgentForWorkspace("Coder", ws.ID, "coder-1")
	if err != nil {
		t.Fatalf("ResolveAgentForWorkspace() error = %v", err)
	}

	restricted := RuntimeMCPServerName(ws.ID, "notes", "mb-1")
	unrestricted := RuntimeMCPServerName(ws.ID, "docs", "mb-2")
	assertStringsEqual(t, "restricted binding allowlist", resolved.MCPToolAllowlist[restricted], []string{"read_note"})
	if _, present := resolved.MCPToolAllowlist[unrestricted]; present {
		t.Fatalf("expected an all-tools binding to carry no allowlist entry, got %v", resolved.MCPToolAllowlist)
	}
}

// Legacy behavior: a workspace with directory references and no explicit
// filesystem binding gets a synthesized one, scoped to those roots (FR-31).
func TestCharacterization_SynthesizedFilesystemBinding(t *testing.T) {
	root := t.TempDir()
	ws := &Workspace{
		ID: "ws-char-fs",
		AgentInstances: []AgentInstance{
			{ID: "inst-1", Name: "Coder", NodeID: "coder-1"},
		},
		DirectoryReferences: []DirectoryReference{
			{ID: "dir-1", Name: "Project", Path: root},
		},
		MCPBindings: []MCPBinding{
			{ID: "mb-1", ServerName: "notes", Enabled: true},
		},
	}

	resolver := newCharacterizationResolver(t, ws, map[string]*agent.Agent{"Coder": {}}, nil)
	resolved, err := resolver.ResolveAgentForWorkspace("Coder", ws.ID, "coder-1")
	if err != nil {
		t.Fatalf("ResolveAgentForWorkspace() error = %v", err)
	}

	synthesized := RuntimeMCPServerName(ws.ID, "filesystem", synthesizedFilesystemBindingID)
	if !slices.Contains(resolved.MCPServers, synthesized) {
		t.Fatalf("expected the synthesized filesystem binding %q, got %v", synthesized, resolved.MCPServers)
	}
}

// Legacy behavior: two instances of the same reusable agent are addressed by
// node ID and carry independent access entries (FR-16, FR-17).
func TestCharacterization_DuplicateAgentNamesResolveIndependently(t *testing.T) {
	ws := &Workspace{
		ID: "ws-char-duplicate",
		AgentInstances: []AgentInstance{
			{ID: "inst-1", Name: "Coder", NodeID: "coder-1", InstanceNumber: 1},
			{ID: "inst-2", Name: "Coder", NodeID: "coder-2", InstanceNumber: 2},
		},
		MCPBindings: []MCPBinding{
			{ID: "mb-1", ServerName: "notes", Enabled: true},
			{ID: "mb-2", ServerName: "docs", Enabled: true},
		},
		AgentMCPAccess: []AgentMCPAccess{
			{AgentInstanceID: "inst-1", EnabledBindingIDs: []string{"mb-1"}},
			{AgentInstanceID: "inst-2", EnabledBindingIDs: []string{"mb-2"}},
		},
	}

	resolver := newCharacterizationResolver(t, ws, map[string]*agent.Agent{"Coder": {}}, nil)

	first, err := resolver.ResolveAgentForWorkspace("Coder", ws.ID, "coder-1")
	if err != nil {
		t.Fatalf("ResolveAgentForWorkspace(coder-1) error = %v", err)
	}
	second, err := resolver.ResolveAgentForWorkspace("Coder", ws.ID, "coder-2")
	if err != nil {
		t.Fatalf("ResolveAgentForWorkspace(coder-2) error = %v", err)
	}

	assertStringsEqual(t, "instance 1 servers", first.MCPServers, []string{RuntimeMCPServerName(ws.ID, "notes", "mb-1")})
	assertStringsEqual(t, "instance 2 servers", second.MCPServers, []string{RuntimeMCPServerName(ws.ID, "docs", "mb-2")})
	if first.AgentInstance == nil || first.AgentInstance.ID != "inst-1" {
		t.Fatalf("expected instance 1 to resolve to inst-1, got %+v", first.AgentInstance)
	}
	if second.AgentInstance == nil || second.AgentInstance.ID != "inst-2" {
		t.Fatalf("expected instance 2 to resolve to inst-2, got %+v", second.AgentInstance)
	}
}

// Legacy behavior this feature exists to END: a binding added after the fact is
// silently adopted by an instance that never opted into it (FR-32). Migration
// gives the instance an explicit Toolbox so this stops happening; the test is
// kept to document what the pre-migration system did.
func TestCharacterization_NewBindingSilentlyInherited(t *testing.T) {
	ws := &Workspace{
		ID: "ws-char-silent",
		AgentInstances: []AgentInstance{
			{ID: "inst-1", Name: "Coder", NodeID: "coder-1"},
		},
		MCPBindings: []MCPBinding{
			{ID: "mb-1", ServerName: "notes", Enabled: true},
		},
	}

	store := newTestWorkspaceStore(t, ws)
	agentStore := &resolverAgentStoreStub{agents: map[string]*agent.Agent{"Coder": {}}}
	resolver := NewAgentRuntimeResolver(agentStore, store, &runtimeRegistryStub{}, &templateLookupStub{
		servers: map[string]mcp.ServerConfig{"notes": {Name: "notes"}, "docs": {Name: "docs"}},
	})

	before, err := resolver.ResolveAgentForWorkspace("Coder", ws.ID, "coder-1")
	if err != nil {
		t.Fatalf("ResolveAgentForWorkspace() before error = %v", err)
	}
	if len(before.MCPServers) != 1 {
		t.Fatalf("expected one binding before the addition, got %v", before.MCPServers)
	}

	if err := store.Update(ws.ID, func(current *Workspace) error {
		return current.UpsertMCPBinding(MCPBinding{ID: "mb-2", ServerName: "docs", Enabled: true})
	}); err != nil {
		t.Fatalf("failed to add the second binding: %v", err)
	}

	after, err := resolver.ResolveAgentForWorkspace("Coder", ws.ID, "coder-1")
	if err != nil {
		t.Fatalf("ResolveAgentForWorkspace() after error = %v", err)
	}
	if len(after.MCPServers) != 2 {
		t.Fatalf("expected the newly added binding to be silently inherited (legacy behavior), got %v", after.MCPServers)
	}
}
