package setupjourney

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// TestStaffRolesFromReviewedWorkspaceSetup_StaffsNothingWhenNoRoleIsFilled is
// the headline outcome: creating a workspace and filling no role creates zero
// agents. Today's behavior creates every required role.
func TestStaffRolesFromReviewedWorkspaceSetup_StaffsNothingWhenNoRoleIsFilled(t *testing.T) {
	adapter, workspaces, scope, _ := staffingFixture(t)
	before := len(adapter.profiles.ListAgents())

	if err := adapter.StaffRolesFromReviewedWorkspaceSetup(context.Background(), scope.ProjectWorkspaceID, nil); err != nil {
		t.Fatal(err)
	}
	if after := len(adapter.profiles.ListAgents()); after != before {
		t.Fatalf("agent definitions = %d, want %d", after, before)
	}
	station, _ := workspaces.Get(scope.HomeWorkspaceID)
	project, _ := workspaces.Get(scope.ProjectWorkspaceID)
	if len(station.GetAgentInstances()) != 0 || len(project.GetAgentInstances()) != 0 {
		t.Fatalf("staffing nothing attached agents: home=%#v project=%#v",
			station.GetAgentInstances(), project.GetAgentInstances())
	}
	if project.EntryAgentName() != "" {
		t.Fatalf("an unstaffed workspace named an entry agent: %q", project.EntryAgentName())
	}
}

// TestStaffRolesFromReviewedWorkspaceSetup_StaffsOnlyTheFilledRoles covers the
// partial fill the adapter used to refuse outright: staffing had to cover every
// unfilled role of a scope at once.
func TestStaffRolesFromReviewedWorkspaceSetup_StaffsOnlyTheFilledRoles(t *testing.T) {
	adapter, workspaces, scope, _ := staffingFixture(t)

	// project_lead and project_reviewer are both required; fill only the lead.
	err := adapter.StaffRolesFromReviewedWorkspaceSetup(context.Background(), scope.ProjectWorkspaceID, []RoleFill{
		{RoleID: "project_lead", Mode: StaffingModeCreate, Name: "Alex Lead"},
	})
	if err != nil {
		t.Fatal(err)
	}
	project, _ := workspaces.Get(scope.ProjectWorkspaceID)
	bindings := project.GetAssistantProjectLink().ProjectBindings.Bindings
	if len(bindings) != 1 || bindings[0].RoleID != "project_lead" {
		t.Fatalf("project bindings = %#v", bindings)
	}
	if _, found := adapter.profiles.GetAgent("Project Reviewer"); found {
		t.Fatal("the unfilled role was staffed anyway")
	}
	if project.EntryAgentName() != "Alex Lead" {
		t.Fatalf("entry agent = %q", project.EntryAgentName())
	}

	// The still-empty role remains fillable afterwards.
	read, err := adapter.Read(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if read.Staffing.Scopes[1].RequiredComplete {
		t.Fatal("a half-staffed scope reported its required roles complete")
	}
}

// TestStaffRolesFromReviewedWorkspaceSetup_MixesCreateAndAssign proves the two
// fill modes travel together through one commit.
func TestStaffRolesFromReviewedWorkspaceSetup_MixesCreateAndAssign(t *testing.T) {
	adapter, workspaces, scope, _ := staffingFixture(t)
	saveExistingAgent(t, adapter)
	before := len(adapter.profiles.ListAgents())

	err := adapter.StaffRolesFromReviewedWorkspaceSetup(context.Background(), scope.ProjectWorkspaceID, []RoleFill{
		{RoleID: "project_lead", Mode: StaffingModeCreate, Name: "Alex Lead"},
		{RoleID: "project_reviewer", Mode: StaffingModeBind, Name: "My Reviewer"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Exactly one definition was created; the assigned one was attached as-is.
	if after := len(adapter.profiles.ListAgents()); after != before+1 {
		t.Fatalf("agent definitions = %d, want %d", after, before+1)
	}
	project, _ := workspaces.Get(scope.ProjectWorkspaceID)
	sources := map[string]string{}
	for _, instance := range project.GetAgentInstances() {
		sources[instance.RoleID] = instance.RoleSource
	}
	if sources["project_lead"] != workspace.RoleSourceCreated {
		t.Fatalf("created role source = %q", sources["project_lead"])
	}
	if sources["project_reviewer"] != workspace.RoleSourceAssigned {
		t.Fatalf("assigned role source = %q", sources["project_reviewer"])
	}
}

// TestStaffRolesFromReviewedWorkspaceSetup_FillsAnOptionalHomeRole covers a
// role that was invisible in the wizard before this feature: optional roles
// were filtered out of the Team step entirely.
func TestStaffRolesFromReviewedWorkspaceSetup_FillsAnOptionalHomeRole(t *testing.T) {
	adapter, workspaces, scope, _ := staffingFixture(t)

	err := adapter.StaffRolesFromReviewedWorkspaceSetup(context.Background(), scope.ProjectWorkspaceID, []RoleFill{
		{RoleID: "catalog_guide", Mode: StaffingModeCreate, Name: "Catalog Guide"},
	})
	if err != nil {
		t.Fatal(err)
	}
	station, _ := workspaces.Get(scope.HomeWorkspaceID)
	bindings := station.GetAssistantProgramState().HomeBindings.Bindings
	if len(bindings) != 1 || bindings[0].RoleID != "catalog_guide" {
		t.Fatalf("home bindings = %#v", bindings)
	}
	// The required home role stayed empty; filling an optional one does not
	// complete the required set.
	read, err := adapter.Read(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if read.Staffing.Scopes[0].RequiredComplete {
		t.Fatal("an optional fill reported required staffing complete")
	}
}

// TestStaffRolesFromReviewedWorkspaceSetup_SpansBothScopesInOneRequest checks
// the home/project split: the two scopes are separately revisioned, so they
// have to commit separately even though the user filled them in one step.
func TestStaffRolesFromReviewedWorkspaceSetup_SpansBothScopesInOneRequest(t *testing.T) {
	adapter, workspaces, scope, _ := staffingFixture(t)

	err := adapter.StaffRolesFromReviewedWorkspaceSetup(context.Background(), scope.ProjectWorkspaceID, []RoleFill{
		{RoleID: "home_guide", Mode: StaffingModeCreate, Name: "June Home"},
		{RoleID: "project_lead", Mode: StaffingModeCreate, Name: "Alex Lead"},
	})
	if err != nil {
		t.Fatal(err)
	}
	station, _ := workspaces.Get(scope.HomeWorkspaceID)
	project, _ := workspaces.Get(scope.ProjectWorkspaceID)
	if got := len(station.GetAssistantProgramState().HomeBindings.Bindings); got != 1 {
		t.Fatalf("home bindings = %d", got)
	}
	if got := len(project.GetAssistantProjectLink().ProjectBindings.Bindings); got != 1 {
		t.Fatalf("project bindings = %d", got)
	}
	if station.EntryAgentName() != "June Home" || project.EntryAgentName() != "Alex Lead" {
		t.Fatalf("entry identities home=%q project=%q", station.EntryAgentName(), project.EntryAgentName())
	}
}

// TestStaffFromReviewedWorkspaceSetup_StillStaffsEveryRequiredRole pins FR62:
// the pre-vacancy caller is unchanged, so CreateFromTemplate and the Personal
// HQ coordinator keep today's behavior.
func TestStaffFromReviewedWorkspaceSetup_StillStaffsEveryRequiredRole(t *testing.T) {
	adapter, workspaces, scope, _ := staffingFixture(t)
	if err := adapter.StaffFromReviewedWorkspaceSetup(context.Background(), scope.ProjectWorkspaceID, "June Home", "", ""); err != nil {
		t.Fatal(err)
	}
	station, _ := workspaces.Get(scope.HomeWorkspaceID)
	project, _ := workspaces.Get(scope.ProjectWorkspaceID)
	if got := len(station.GetAssistantProgramState().HomeBindings.Bindings); got != 1 {
		t.Fatalf("home bindings = %d, want every required home role", got)
	}
	if got := len(project.GetAssistantProjectLink().ProjectBindings.Bindings); got != 2 {
		t.Fatalf("project bindings = %d, want every required project role", got)
	}
}
