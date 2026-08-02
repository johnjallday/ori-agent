package workspace

import (
	"testing"
	"time"
)

// Workshop inventory coverage (task 2.5, 2.6, 2.9, 2.14; PRD FR-43–FR-50).

type stubLibrary struct {
	skills  []ToolboxLibraryItem
	servers []ToolboxLibraryItem
}

func (s *stubLibrary) ListLibrarySkills() []ToolboxLibraryItem     { return s.skills }
func (s *stubLibrary) ListLibraryMCPServers() []ToolboxLibraryItem { return s.servers }

func newWorkshopWorkspace() *Workspace {
	return &Workspace{
		ID:             "ws-workshop",
		AgentInstances: []AgentInstance{{ID: "inst-1", Name: "Coder", NodeID: "coder-1"}},
		SkillBindings: []SkillBinding{
			{ID: "sb-1", SkillName: "testing", Enabled: true, Trusted: true},
			{ID: "sb-2", SkillName: "retired", Enabled: false},
		},
		MCPBindings: []MCPBinding{
			{
				ID: "mb-1", ServerName: "notes", Alias: "Notes", Enabled: true,
				AllowedTools:      []string{"read_note", "write_note"},
				DefaultSideEffect: SideEffectRead,
				ToolOverrides:     map[string]SideEffect{"write_note": SideEffectWrite},
				Scope:             map[string]any{"folder": "notes/"},
			},
			{ID: "mb-2", ServerName: "docs", Enabled: false, AllowedTools: []string{"read_doc"}},
			{ID: "mb-3", ServerName: "tracker", Enabled: true, AllowedTools: []string{"list_issues"}},
		},
	}
}

func findWorkshopItem(items []WorkshopItem, capabilityID string) *WorkshopItem {
	for i := range items {
		if items[i].CapabilityID == capabilityID {
			return &items[i]
		}
	}
	return nil
}

// FR-43: workspace-approved capabilities and globally known ones are separate
// groups, so "select this" and "go set this up" are distinguishable before the
// click.
func TestBuildWorkshopInventory_SeparatesApprovedFromGlobalLibrary(t *testing.T) {
	ws := newWorkshopWorkspace()
	instance := ws.AgentInstances[0]
	library := &stubLibrary{
		skills:  []ToolboxLibraryItem{{Name: "testing"}, {Name: "summarizing"}},
		servers: []ToolboxLibraryItem{{Name: "notes"}, {Name: "calendar"}},
	}

	inventory := BuildWorkshopInventory(ws, &instance, nil,
		[]ResolvedSkill{{Name: "code-review", Description: "Reviews diffs."}},
		library, 4, false)

	if findWorkshopItem(inventory.AgentLearned, "code-review") == nil {
		t.Fatalf("expected the agent's learned skill in the agent-learned group, got %+v", inventory.AgentLearned)
	}
	if findWorkshopItem(inventory.WorkspaceProvided, "testing") == nil {
		t.Fatalf("expected the workspace skill binding in the workspace-provided group")
	}
	// Already approved here → not offered as something to set up.
	if findWorkshopItem(inventory.GlobalLibrary, "testing") != nil {
		t.Fatalf("expected an approved skill to be excluded from the global library")
	}
	if findWorkshopItem(inventory.GlobalLibrary, "notes") != nil {
		t.Fatalf("expected an approved server to be excluded from the global library")
	}
	// Known to Ori, not approved here → offered, but unavailable.
	unapproved := findWorkshopItem(inventory.GlobalLibrary, "summarizing")
	if unapproved == nil || unapproved.Available {
		t.Fatalf("expected an unapproved library skill to be listed as unavailable, got %+v", unapproved)
	}
	if unapproved.UnavailableReason == "" {
		t.Fatalf("expected the library item to explain what is missing")
	}
	if findWorkshopItem(inventory.GlobalLibrary, "calendar") == nil {
		t.Fatalf("expected an unapproved server to be offered from the global library")
	}
}

// FR-47/FR-48: core capabilities are locked, always present, and consume no
// skill space.
func TestBuildWorkshopInventory_CoreIsLockedAndFree(t *testing.T) {
	ws := newWorkshopWorkspace()
	ws.DirectoryReferences = []DirectoryReference{{ID: "dir-1", Name: "Project", Path: t.TempDir()}}
	instance := ws.AgentInstances[0]

	inventory := BuildWorkshopInventory(ws, &instance, nil, nil, nil, 4, false)

	if len(inventory.Core) == 0 {
		t.Fatalf("expected the synthesized filesystem binding in the core group")
	}
	for _, item := range inventory.Core {
		if !item.Locked || !item.Selected || item.ConsumesSkillSpace {
			t.Fatalf("expected core items to be locked, always selected, and space-free, got %+v", item)
		}
	}
}

// FR-50: MCP cards carry connection, scope, exposed operations, and risk
// classification — including which operations have none.
func TestBuildWorkshopInventory_MCPCardsCarryRiskAndConnectionDetail(t *testing.T) {
	ws := newWorkshopWorkspace()
	instance := ws.AgentInstances[0]

	inventory := BuildWorkshopInventory(ws, &instance, nil, nil, nil, 4, false)

	notes := findWorkshopItem(inventory.WorkspaceProvided, "mb-1")
	if notes == nil {
		t.Fatalf("expected the notes binding in the workspace-provided group")
	}
	if !notes.Connected || !notes.Available {
		t.Fatalf("expected an enabled binding to report as connected, got %+v", notes)
	}
	if notes.Scope["folder"] != "notes/" {
		t.Fatalf("expected the binding scope to be reported, got %v", notes.Scope)
	}
	if len(notes.ExposedTools) != 2 {
		t.Fatalf("expected the concrete operations to be listed, got %v", notes.ExposedTools)
	}
	if notes.DefaultSideEffect != string(SideEffectRead) || notes.ToolRisks["write_note"] != string(SideEffectWrite) {
		t.Fatalf("expected the risk classification to be reported, got %+v", notes)
	}
	if len(notes.UnclassifiedTools) != 0 {
		t.Fatalf("expected a fully classified binding to report no unclassified operations, got %v", notes.UnclassifiedTools)
	}

	// A binding with no classification at all must show its operations as
	// unclassified — they fail closed under a Goal's autonomy gate (FR-159).
	tracker := findWorkshopItem(inventory.WorkspaceProvided, "mb-3")
	if tracker == nil || len(tracker.UnclassifiedTools) != 1 || tracker.UnclassifiedTools[0] != "list_issues" {
		t.Fatalf("expected unclassified operations to be surfaced, got %+v", tracker)
	}

	// A disabled binding is visible but unavailable, with a reason.
	docs := findWorkshopItem(inventory.WorkspaceProvided, "mb-2")
	if docs == nil || docs.Available || docs.UnavailableReason == "" {
		t.Fatalf("expected a switched-off binding to be shown as unavailable with a reason, got %+v", docs)
	}
}

// FR-14/FR-46: an entry naming something that no longer resolves becomes a
// visible requirement rather than silently vanishing.
func TestBuildWorkshopInventory_ReportsUnmetRequirements(t *testing.T) {
	ws := newWorkshopWorkspace()
	instance := ws.AgentInstances[0]
	recipe := &ToolboxRecipe{
		Version: 3,
		Skills: []ToolboxSkillRef{
			{CapabilityID: "testing", DisplayName: "testing", Source: ToolboxSourceWorkspaceProvided, BindingID: "sb-1", Required: true},
			{CapabilityID: "citations", DisplayName: "citations", Source: ToolboxSourceWorkspaceProvided, BindingID: "sb-gone", Required: true},
		},
		MCPBindings: []ToolboxMCPRef{
			{BindingID: "mb-1", AllowedTools: []string{"read_note"}},
			{BindingID: "mb-gone", AllowedTools: []string{"search"}, Required: true},
		},
	}

	inventory := BuildWorkshopInventory(ws, &instance, recipe, nil, nil, 4, false)

	if len(inventory.Requirements) != 2 {
		t.Fatalf("expected both unresolvable entries to be reported, got %+v", inventory.Requirements)
	}
	for _, requirement := range inventory.Requirements {
		if requirement.Available || requirement.UnavailableReason == "" {
			t.Fatalf("expected a requirement to be unavailable with a reason, got %+v", requirement)
		}
	}
	// The resolvable entries are still marked selected in their own groups.
	testing := findWorkshopItem(inventory.WorkspaceProvided, "testing")
	if testing == nil || !testing.Selected {
		t.Fatalf("expected the resolvable skill to be marked selected, got %+v", testing)
	}
	notes := findWorkshopItem(inventory.WorkspaceProvided, "mb-1")
	if notes == nil || !notes.Selected || len(notes.SelectedTools) != 1 {
		t.Fatalf("expected the selected binding's operation subset to be reported, got %+v", notes)
	}
}

// FR-6/FR-44: a skill drawn from two sources is surfaced for the user to
// resolve, not silently decided.
func TestBuildWorkshopInventory_SurfacesSourceCollisions(t *testing.T) {
	ws := newWorkshopWorkspace()
	instance := ws.AgentInstances[0]
	recipe := &ToolboxRecipe{
		Skills: []ToolboxSkillRef{
			{CapabilityID: "testing", Source: ToolboxSourceAgentLearned},
			{CapabilityID: "testing", Source: ToolboxSourceWorkspaceProvided, BindingID: "sb-1"},
		},
	}

	inventory := BuildWorkshopInventory(ws, &instance, recipe, []ResolvedSkill{{Name: "testing"}}, nil, 4, false)

	if len(inventory.Collisions) != 1 || inventory.Collisions[0].CapabilityID != "testing" {
		t.Fatalf("expected the source collision to be surfaced, got %+v", inventory.Collisions)
	}
}

// FR-33/FR-55: the inventory reports the agent's capacity position so the
// editor can show `Toolbox full` before a save is attempted.
func TestBuildWorkshopInventory_ReportsCapacityPosition(t *testing.T) {
	ws := newWorkshopWorkspace()
	instance := ws.AgentInstances[0]
	recipe := &ToolboxRecipe{Skills: []ToolboxSkillRef{
		{CapabilityID: "a", Source: ToolboxSourceAgentLearned},
		{CapabilityID: "b", Source: ToolboxSourceAgentLearned},
		{CapabilityID: "c", Source: ToolboxSourceAgentLearned},
	}}

	full := BuildWorkshopInventory(ws, &instance, recipe, nil, nil, 2, false)
	if !full.Capacity.Full || !full.Capacity.Grandfathered || full.Capacity.Used != 3 {
		t.Fatalf("expected a grandfathered full toolbox, got %+v", full.Capacity)
	}

	expert := BuildWorkshopInventory(ws, &instance, recipe, nil, nil, 2, true)
	if expert.Capacity.Full {
		t.Fatalf("expected expert mode to lift the ceiling, got %+v", expert.Capacity)
	}
}

// FR-32: installed-capability provenance rides along for grouping, without
// implying the capability is active.
func TestBuildWorkshopInventory_CarriesCapabilityProvenance(t *testing.T) {
	ws := newWorkshopWorkspace()
	ws.InstalledCapabilities = []InstalledCapability{{
		ID:             CapabilityFileJanitor,
		Version:        1,
		InstalledAt:    time.Now(),
		OwnedResources: []CapabilityResource{{Kind: ResourceMCPBinding, ID: "mb-1"}},
	}}
	instance := ws.AgentInstances[0]

	inventory := BuildWorkshopInventory(ws, &instance, nil, nil, nil, 4, false)

	owned := findWorkshopItem(inventory.WorkspaceProvided, "mb-1")
	if owned == nil || owned.OwnerCapabilityID != CapabilityFileJanitor {
		t.Fatalf("expected the binding to record its capability provenance, got %+v", owned)
	}
	unowned := findWorkshopItem(inventory.WorkspaceProvided, "mb-3")
	if unowned == nil || unowned.OwnerCapabilityID != "" {
		t.Fatalf("expected an unowned binding to record no provenance, got %+v", unowned)
	}
}

// FR-13: a binding whose policy permits everything is reported as such, so the
// editor can ask the user to pin an explicit subset.
func TestBuildWorkshopInventory_FlagsAllToolsBindings(t *testing.T) {
	ws := newWorkshopWorkspace()
	ws.MCPBindings = append(ws.MCPBindings, MCPBinding{ID: "mb-open", ServerName: "open", Enabled: true})
	instance := ws.AgentInstances[0]

	inventory := BuildWorkshopInventory(ws, &instance, nil, nil, nil, 4, false)

	open := findWorkshopItem(inventory.WorkspaceProvided, "mb-open")
	if open == nil || !open.ExposesAllTools || len(open.ExposedTools) != 0 {
		t.Fatalf("expected an all-tools binding to be flagged rather than listing invented operations, got %+v", open)
	}
}
