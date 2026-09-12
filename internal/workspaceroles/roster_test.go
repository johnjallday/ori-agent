package workspaceroles

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func lookupOf(names ...string) func(string) (AgentIdentity, bool) {
	known := make(map[string]struct{}, len(names))
	for _, name := range names {
		known[name] = struct{}{}
	}
	return func(name string) (AgentIdentity, bool) {
		if _, found := known[name]; !found {
			return AgentIdentity{}, false
		}
		return AgentIdentity{Name: name, Role: "specialist"}, true
	}
}

func musicRoles() []Role {
	return []Role{
		{ID: "producer", Label: "Producer", Scope: ScopeProject, Required: true, Primary: true},
		{ID: "mix-engineer", Label: "Mix Engineer", Scope: ScopeProject, Required: true},
		{ID: "songwriter", Label: "Songwriter", Scope: ScopeProject},
	}
}

// TestBuildStartsEveryRoleEmpty is the whole feature in one assertion: a
// blueprint's roles are slots, and declaring them fills nothing.
func TestBuildStartsEveryRoleEmpty(t *testing.T) {
	roster := Build(Input{WorkspaceID: "ws-1", ViewScope: ScopeProject, Roles: musicRoles(), Lookup: lookupOf()})
	if roster.TotalCount != 3 || roster.FilledCount != 0 || roster.EntryAgentName != "" {
		t.Fatalf("empty roster = %#v", roster)
	}
	for _, role := range roster.Roles {
		if role.State != StateEmpty || role.Agent != nil {
			t.Fatalf("role %q = %#v", role.RoleID, role)
		}
	}
	if len(roster.Unassigned) != 0 {
		t.Fatalf("unassigned = %#v", roster.Unassigned)
	}
}

func TestBuildProjectsFilledRolesAndTheirSource(t *testing.T) {
	roster := Build(Input{
		WorkspaceID: "ws-1", ViewScope: ScopeProject, Roles: musicRoles(),
		Attachments: []Attachment{
			{Name: "Producer", RoleID: "producer", RoleSource: SourceCreated, EntryPoint: true},
			{Name: "My Mixer", RoleID: "mix-engineer", RoleSource: SourceAssigned},
			{Name: "Note Taker"},
		},
		Lookup: lookupOf("Producer", "My Mixer", "Note Taker"),
	})
	if roster.FilledCount != 2 || roster.TotalCount != 3 {
		t.Fatalf("counts = %d of %d", roster.FilledCount, roster.TotalCount)
	}
	if roster.Roles[0].State != StateFilled || roster.Roles[0].Source != SourceCreated || roster.Roles[0].Agent.Name != "Producer" {
		t.Fatalf("primary role = %#v", roster.Roles[0])
	}
	if roster.Roles[1].Source != SourceAssigned {
		t.Fatalf("assigned role lost its source: %#v", roster.Roles[1])
	}
	if roster.Roles[2].State != StateEmpty {
		t.Fatalf("optional role = %#v", roster.Roles[2])
	}
	// An agent in the workspace that holds no role is listed, not dropped (FR63).
	if len(roster.Unassigned) != 1 || roster.Unassigned[0].Name != "Note Taker" {
		t.Fatalf("unassigned = %#v", roster.Unassigned)
	}
	if roster.EntryAgentName != "Producer" {
		t.Fatalf("entry agent = %q", roster.EntryAgentName)
	}
}

// TestBuildShowsARoleEmptyAgainWhenItsAgentIsDeleted covers FR42: the roster is
// projected from live state, so deleting an agent on /agents empties its role
// rather than leaving a row pointing at nothing.
func TestBuildShowsARoleEmptyAgainWhenItsAgentIsDeleted(t *testing.T) {
	roster := Build(Input{
		WorkspaceID: "ws-1", ViewScope: ScopeProject, Roles: musicRoles(),
		Attachments: []Attachment{{Name: "Deleted Producer", RoleID: "producer"}},
		Lookup:      lookupOf(),
	})
	if roster.FilledCount != 0 || roster.Roles[0].State != StateEmpty || !roster.Roles[0].NeedsClear {
		t.Fatalf("stale holder was not projected as an empty role needing an explicit clear: %#v", roster.Roles[0])
	}
	if len(roster.Unassigned) != 0 {
		t.Fatalf("a deleted agent was reported as present: %#v", roster.Unassigned)
	}
}

// TestBuildFallsBackToLegacyBindings covers FR38: a workspace staffed before
// attachments carried role ids still shows its roles filled.
func TestBuildFallsBackToLegacyBindings(t *testing.T) {
	roster := Build(Input{
		WorkspaceID: "ws-1", ViewScope: ScopeProject, Roles: musicRoles(),
		Attachments: []Attachment{{Name: "Producer"}},
		Bindings:    map[string]string{"producer": "Producer"},
		Lookup:      lookupOf("Producer"),
	})
	if roster.Roles[0].State != StateFilled || roster.Roles[0].Source != SourceCreated {
		t.Fatalf("legacy binding = %#v", roster.Roles[0])
	}
	if len(roster.Unassigned) != 0 {
		t.Fatalf("the bound agent was double-counted as unassigned: %#v", roster.Unassigned)
	}
}

// TestBuildMarksOutOfScopeRolesReadOnly covers D2: a group coordination role
// belongs to the group, and the project may show it but not change it.
func TestBuildMarksOutOfScopeRolesReadOnly(t *testing.T) {
	roles := append(musicRoles(), Role{ID: "portfolio", Label: "Portfolio Manager", Scope: ScopeHome, Required: true})
	roster := Build(Input{WorkspaceID: "ws-1", ViewScope: ScopeProject, GroupWorkspaceID: "station-1", Roles: roles, Lookup: lookupOf()})
	if !roster.Roles[3].ReadOnly || roster.Roles[3].ReadOnlyReason == "" {
		t.Fatalf("home role from a project = %#v", roster.Roles[3])
	}
	for _, role := range roster.Roles[:3] {
		if role.ReadOnly {
			t.Fatalf("project role marked read-only: %#v", role)
		}
	}
	if roster.GroupWorkspaceID != "station-1" {
		t.Fatalf("group workspace = %q", roster.GroupWorkspaceID)
	}
}

// TestEntryAgentFallsBackToDeclarationOrder covers D3: a workspace with agents
// in it is never dead just because the primary slot is empty.
func TestEntryAgentFallsBackToDeclarationOrder(t *testing.T) {
	roster := Build(Input{
		WorkspaceID: "ws-1", ViewScope: ScopeProject, Roles: musicRoles(),
		Attachments: []Attachment{
			{Name: "Songwriter", RoleID: "songwriter"},
			{Name: "My Mixer", RoleID: "mix-engineer"},
		},
		Lookup: lookupOf("Songwriter", "My Mixer"),
	})
	if roster.EntryAgentName != "My Mixer" {
		t.Fatalf("entry agent = %q, want the first filled role in declaration order", roster.EntryAgentName)
	}
}

func TestFromAssistantProgramCarriesScopeAndRequiredness(t *testing.T) {
	roles := FromAssistantProgram(&workspace.AssistantProgramDeclaration{
		Roles: []workspace.AssistantProgramRoleSpec{
			{ID: "home_guide", Label: "Home Guide", Scope: workspace.AssistantRoleScopeHome, Required: true, Primary: true},
			{ID: "catalog_guide", Label: "Catalog Guide", Scope: workspace.AssistantRoleScopeHome},
			{ID: "project_lead", Label: "Project Lead", Required: true},
		},
	})
	if len(roles) != 3 {
		t.Fatalf("roles = %#v", roles)
	}
	if roles[0].Scope != ScopeHome || !roles[0].Required || !roles[0].Primary {
		t.Fatalf("primary home role = %#v", roles[0])
	}
	if roles[1].Required {
		t.Fatalf("optional role marked required: %#v", roles[1])
	}
	// An unset scope defaults to the project, matching the declaration's own
	// omit-means-project convention.
	if roles[2].Scope != ScopeProject {
		t.Fatalf("scopeless role = %#v", roles[2])
	}
}

func TestFromTemplateAgentsMakesTheEntryAgentThePrimaryRole(t *testing.T) {
	roles := FromTemplateAgents([]projecttemplates.AgentSpec{
		{Name: "Producer", SystemPrompt: "You run the session. Keep everyone moving."},
		{Name: "Mix Engineer"},
	})
	if len(roles) != 2 {
		t.Fatalf("roles = %#v", roles)
	}
	if roles[0].ID != "producer" || !roles[0].Primary || !roles[0].Required {
		t.Fatalf("entry role = %#v", roles[0])
	}
	if roles[0].Description != "You run the session." {
		t.Fatalf("derived description = %q", roles[0].Description)
	}
	if roles[1].Required || roles[1].Primary {
		t.Fatalf("specialist role = %#v", roles[1])
	}
}
