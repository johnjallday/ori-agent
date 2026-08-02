package workspace

import (
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/mcp"
)

// Runtime coverage proving an assigned Toolbox resolves EXACTLY what it names
// and nothing else (task 1.17; PRD FR-2–FR-7, FR-16–FR-17, FR-32).

func newToolboxRuntimeWorkspace() *Workspace {
	return &Workspace{
		ID: "ws-runtime-toolbox",
		AgentInstances: []AgentInstance{
			{ID: "inst-1", Name: "Coder", NodeID: "coder-1", InstanceNumber: 1},
			{ID: "inst-2", Name: "Coder", NodeID: "coder-2", InstanceNumber: 2},
		},
		SkillBindings: []SkillBinding{
			{ID: "sb-1", SkillName: "testing", Enabled: true, Trusted: true},
			{ID: "sb-2", SkillName: "drafting", Enabled: true},
		},
		MCPBindings: []MCPBinding{
			{ID: "mb-1", ServerName: "notes", Enabled: true, AllowedTools: []string{"read_note", "write_note"}},
			{ID: "mb-2", ServerName: "docs", Enabled: true},
		},
	}
}

func newToolboxRuntimeResolver(t *testing.T, ws *Workspace) *AgentRuntimeResolver {
	t.Helper()
	resolver := NewAgentRuntimeResolver(
		&resolverAgentStoreStub{agents: map[string]*agent.Agent{"Coder": {}}},
		newTestWorkspaceStore(t, ws),
		&runtimeRegistryStub{},
		&templateLookupStub{servers: map[string]mcp.ServerConfig{
			"notes": {Name: "notes"},
			"docs":  {Name: "docs"},
			"filesystem": {
				Name: "filesystem",
				Args: []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp/default-root"},
			},
		}},
	)
	resolver.SetSkillResolver(&stubSkillResolver{
		skills: map[string]ResolvedSkill{
			"testing":  {Name: "testing", Prompt: "Test.", Enabled: true},
			"drafting": {Name: "drafting", Prompt: "Draft.", Enabled: true},
		},
		agentSkills: map[string][]ResolvedSkill{
			"Coder": {
				{Name: "code-review", Prompt: "Review.", Enabled: true},
				{Name: "refactoring", Prompt: "Refactor.", Enabled: true},
			},
		},
	})
	return resolver
}

func assignToolbox(t *testing.T, ws *Workspace, instanceID, name string, skills []ToolboxSkillRef, bindings []ToolboxMCPRef) *ToolboxDefinition {
	t.Helper()
	created, err := ws.CreateToolbox(ToolboxDefinition{
		ID:          "tbx-" + strings.ToLower(strings.ReplaceAll(name, " ", "-")),
		Name:        name,
		Skills:      skills,
		MCPBindings: bindings,
	})
	if err != nil {
		t.Fatalf("CreateToolbox(%s) error = %v", name, err)
	}
	if _, err := ws.SetToolboxAssignment(AgentToolboxAssignment{AgentInstanceID: instanceID, ToolboxID: created.ID}); err != nil {
		t.Fatalf("SetToolboxAssignment(%s) error = %v", instanceID, err)
	}
	return created
}

// The pinned Toolbox selects from the agent's learned skills; the ones it does
// not name are NOT appended (FR-2, FR-55).
func TestToolboxRuntime_DoesNotAppendUnselectedLearnedSkills(t *testing.T) {
	ws := newToolboxRuntimeWorkspace()
	assignToolbox(t, ws, "inst-1", "Review Only",
		[]ToolboxSkillRef{{CapabilityID: "code-review", DisplayName: "code-review", Source: ToolboxSourceAgentLearned}},
		nil)

	resolved, err := newToolboxRuntimeResolver(t, ws).ResolveAgentForWorkspace("Coder", ws.ID, "coder-1")
	if err != nil {
		t.Fatalf("ResolveAgentForWorkspace() error = %v", err)
	}
	assertStringsEqual(t, "effective skills", resolvedSkillNames(resolved.EffectiveSkills), []string{"code-review"})
}

// A workspace-provided entry resolves through its exact binding, carrying that
// binding's trust and config (FR-6, FR-10).
func TestToolboxRuntime_ResolvesWorkspaceProvidedSkillThroughItsBinding(t *testing.T) {
	ws := newToolboxRuntimeWorkspace()
	assignToolbox(t, ws, "inst-1", "Testing Kit",
		[]ToolboxSkillRef{{CapabilityID: "testing", DisplayName: "testing", Source: ToolboxSourceWorkspaceProvided, BindingID: "sb-1"}},
		nil)

	resolved, err := newToolboxRuntimeResolver(t, ws).ResolveAgentForWorkspace("Coder", ws.ID, "coder-1")
	if err != nil {
		t.Fatalf("ResolveAgentForWorkspace() error = %v", err)
	}
	if len(resolved.EffectiveSkills) != 1 || resolved.EffectiveSkills[0].Name != "testing" {
		t.Fatalf("expected only the selected workspace skill, got %v", resolvedSkillNames(resolved.EffectiveSkills))
	}
	if !resolved.EffectiveSkills[0].Trusted {
		t.Fatalf("expected the binding's trust setting to be applied")
	}
}

// The Toolbox's exact operation subset becomes the runtime allowlist, narrower
// than the binding's own policy (FR-11, FR-12).
func TestToolboxRuntime_AppliesExactOperationSubset(t *testing.T) {
	ws := newToolboxRuntimeWorkspace()
	assignToolbox(t, ws, "inst-1", "Read Only", nil,
		[]ToolboxMCPRef{{BindingID: "mb-1", AllowedTools: []string{"read_note"}}})

	resolved, err := newToolboxRuntimeResolver(t, ws).ResolveAgentForWorkspace("Coder", ws.ID, "coder-1")
	if err != nil {
		t.Fatalf("ResolveAgentForWorkspace() error = %v", err)
	}

	runtimeName := RuntimeMCPServerName(ws.ID, "notes", "mb-1")
	assertStringsEqual(t, "runtime servers", resolved.MCPServers, []string{runtimeName})
	assertStringsEqual(t, "runtime allowlist", resolved.MCPToolAllowlist[runtimeName], []string{"read_note"})
}

// An explicit EMPTY tool list is a real selection: the binding is present but
// exposes no operations.
func TestToolboxRuntime_EmptyToolListExposesNoOperations(t *testing.T) {
	ws := newToolboxRuntimeWorkspace()
	assignToolbox(t, ws, "inst-1", "Nothing Exposed", nil,
		[]ToolboxMCPRef{{BindingID: "mb-1", AllowedTools: []string{}}})

	resolved, err := newToolboxRuntimeResolver(t, ws).ResolveAgentForWorkspace("Coder", ws.ID, "coder-1")
	if err != nil {
		t.Fatalf("ResolveAgentForWorkspace() error = %v", err)
	}
	runtimeName := RuntimeMCPServerName(ws.ID, "notes", "mb-1")
	allowed, present := resolved.MCPToolAllowlist[runtimeName]
	if !present {
		t.Fatalf("expected an explicit empty selection to produce an allowlist entry, got %v", resolved.MCPToolAllowlist)
	}
	if len(allowed) != 0 {
		t.Fatalf("expected no operations to be exposed, got %v", allowed)
	}
}

// THE headline behavior: a binding added after the Toolbox was pinned does not
// appear in the agent's hands (FR-32).
func TestToolboxRuntime_NewWorkspaceBindingIsNotSilentlyAdded(t *testing.T) {
	ws := newToolboxRuntimeWorkspace()
	assignToolbox(t, ws, "inst-1", "Notes Only", nil,
		[]ToolboxMCPRef{{BindingID: "mb-1", AllowedTools: []string{"read_note"}}})

	store := newTestWorkspaceStore(t, ws)
	resolver := NewAgentRuntimeResolver(
		&resolverAgentStoreStub{agents: map[string]*agent.Agent{"Coder": {}}},
		store,
		&runtimeRegistryStub{},
		&templateLookupStub{servers: map[string]mcp.ServerConfig{
			"notes":   {Name: "notes"},
			"docs":    {Name: "docs"},
			"tracker": {Name: "tracker"},
		}},
	)

	if err := store.Update(ws.ID, func(current *Workspace) error {
		return current.UpsertMCPBinding(MCPBinding{ID: "mb-new", ServerName: "tracker", Enabled: true})
	}); err != nil {
		t.Fatalf("failed to add a workspace binding: %v", err)
	}

	resolved, err := resolver.ResolveAgentForWorkspace("Coder", ws.ID, "coder-1")
	if err != nil {
		t.Fatalf("ResolveAgentForWorkspace() error = %v", err)
	}
	assertStringsEqual(t, "runtime servers after the addition", resolved.MCPServers,
		[]string{RuntimeMCPServerName(ws.ID, "notes", "mb-1")})
}

// Two instances of the same reusable agent resolve their own Toolboxes (FR-17).
func TestToolboxRuntime_DuplicateAgentNamesResolveTheirOwnToolbox(t *testing.T) {
	ws := newToolboxRuntimeWorkspace()
	assignToolbox(t, ws, "inst-1", "Notes Kit", nil,
		[]ToolboxMCPRef{{BindingID: "mb-1", AllowedTools: []string{"read_note"}}})
	assignToolbox(t, ws, "inst-2", "Docs Kit", nil,
		[]ToolboxMCPRef{{BindingID: "mb-2", AllowedTools: []string{"read_doc"}}})

	resolver := newToolboxRuntimeResolver(t, ws)
	first, err := resolver.ResolveAgentForWorkspace("Coder", ws.ID, "coder-1")
	if err != nil {
		t.Fatalf("ResolveAgentForWorkspace(coder-1) error = %v", err)
	}
	second, err := resolver.ResolveAgentForWorkspace("Coder", ws.ID, "coder-2")
	if err != nil {
		t.Fatalf("ResolveAgentForWorkspace(coder-2) error = %v", err)
	}

	assertStringsEqual(t, "instance 1", first.MCPServers, []string{RuntimeMCPServerName(ws.ID, "notes", "mb-1")})
	assertStringsEqual(t, "instance 2", second.MCPServers, []string{RuntimeMCPServerName(ws.ID, "docs", "mb-2")})
}

// A disabled or removed binding resolves to nothing — never to a substitute
// (FR-113).
func TestToolboxRuntime_DisabledBindingResolvesToNothing(t *testing.T) {
	ws := newToolboxRuntimeWorkspace()
	assignToolbox(t, ws, "inst-1", "Notes Kit", nil,
		[]ToolboxMCPRef{{BindingID: "mb-1", AllowedTools: []string{"read_note"}, Required: true}})

	for i := range ws.MCPBindings {
		if ws.MCPBindings[i].ID == "mb-1" {
			ws.MCPBindings[i].Enabled = false
		}
	}

	resolved, err := newToolboxRuntimeResolver(t, ws).ResolveAgentForWorkspace("Coder", ws.ID, "coder-1")
	if err != nil {
		t.Fatalf("ResolveAgentForWorkspace() error = %v", err)
	}
	if len(resolved.MCPServers) != 0 {
		t.Fatalf("expected a disabled binding to resolve to nothing, got %v", resolved.MCPServers)
	}
}

// A migrated entry that still defers to the binding's tool policy keeps that
// policy exactly (see ToolboxMCPRef.InheritsBindingTools).
func TestToolboxRuntime_InheritedToolsFollowTheBindingPolicy(t *testing.T) {
	ws := newToolboxRuntimeWorkspace()
	assignToolbox(t, ws, "inst-1", "Migrated Kit", nil, []ToolboxMCPRef{
		{BindingID: "mb-1", InheritsBindingTools: true},
		{BindingID: "mb-2", InheritsBindingTools: true},
	})

	resolved, err := newToolboxRuntimeResolver(t, ws).ResolveAgentForWorkspace("Coder", ws.ID, "coder-1")
	if err != nil {
		t.Fatalf("ResolveAgentForWorkspace() error = %v", err)
	}

	restricted := RuntimeMCPServerName(ws.ID, "notes", "mb-1")
	unrestricted := RuntimeMCPServerName(ws.ID, "docs", "mb-2")
	assertStringsEqual(t, "inherited restriction", resolved.MCPToolAllowlist[restricted], []string{"read_note", "write_note"})
	if _, present := resolved.MCPToolAllowlist[unrestricted]; present {
		t.Fatalf("expected an all-tools binding to stay unrestricted, got %v", resolved.MCPToolAllowlist)
	}
}

// Core capabilities are present regardless of Toolbox contents (FR-31, FR-59).
func TestToolboxRuntime_CoreFilesystemBindingSurvivesAnEmptyToolbox(t *testing.T) {
	ws := newToolboxRuntimeWorkspace()
	ws.DirectoryReferences = []DirectoryReference{{ID: "dir-1", Name: "Project", Path: t.TempDir()}}
	assignToolbox(t, ws, "inst-1", "Empty Kit", nil, nil)

	resolved, err := newToolboxRuntimeResolver(t, ws).ResolveAgentForWorkspace("Coder", ws.ID, "coder-1")
	if err != nil {
		t.Fatalf("ResolveAgentForWorkspace() error = %v", err)
	}
	assertStringsEqual(t, "core-only servers", resolved.MCPServers,
		[]string{RuntimeMCPServerName(ws.ID, "filesystem", synthesizedFilesystemBindingID)})
}

// An unmigrated instance keeps resolving through the legacy merge, so migration
// can proceed workspace by workspace without breaking anything mid-flight.
func TestToolboxRuntime_UnassignedInstanceStillUsesTheLegacyPath(t *testing.T) {
	ws := newToolboxRuntimeWorkspace()
	assignToolbox(t, ws, "inst-1", "Notes Kit", nil,
		[]ToolboxMCPRef{{BindingID: "mb-1", AllowedTools: []string{"read_note"}}})

	resolved, err := newToolboxRuntimeResolver(t, ws).ResolveAgentForWorkspace("Coder", ws.ID, "coder-2")
	if err != nil {
		t.Fatalf("ResolveAgentForWorkspace(coder-2) error = %v", err)
	}
	if len(resolved.MCPServers) != 2 {
		t.Fatalf("expected the unassigned instance to keep inheriting both bindings, got %v", resolved.MCPServers)
	}
}

// An unreadable assignment fails closed rather than falling back to the legacy
// merge, which would silently widen permissions (FR-157).
func TestToolboxRuntime_UnreadableAssignmentFailsClosed(t *testing.T) {
	ws := newToolboxRuntimeWorkspace()
	assignToolbox(t, ws, "inst-1", "Notes Kit", nil,
		[]ToolboxMCPRef{{BindingID: "mb-1", AllowedTools: []string{"read_note"}}})
	// Simulate a hand-edited workspace.json pointing at a toolbox that is gone.
	ws.Toolboxes = nil

	_, err := newToolboxRuntimeResolver(t, ws).ResolveAgentForWorkspace("Coder", ws.ID, "coder-1")
	if err == nil {
		t.Fatalf("expected an unreadable assignment to fail rather than fall back to implicit inheritance")
	}
	if !strings.Contains(err.Error(), "repaired") {
		t.Fatalf("expected the error to point at repair, got %v", err)
	}
}
