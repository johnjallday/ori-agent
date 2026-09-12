package setupjourney

import (
	"context"
	"errors"
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

// TestStaffRolesFromReviewedWorkspaceSetup_RequiresAProjectTarget records the
// current adapter boundary behind the live workspace-role callback: this
// workspace-setup helper resolves ownership from an Assistant Project Link, so
// passing a genuine Home ID cannot reach its Home Review/Commit path. A direct
// group-role operation needs a separately target-aware adapter entry point; it
// must not weaken this project-owned coordinator or let a child fill Home roles.
func TestStaffRolesFromReviewedWorkspaceSetup_RequiresAProjectTarget(t *testing.T) {
	adapter, workspaces, scope, _ := staffingFixture(t)

	err := adapter.StaffRolesFromReviewedWorkspaceSetup(context.Background(), scope.HomeWorkspaceID, []RoleFill{
		{RoleID: "home_guide", Mode: StaffingModeCreate, Name: "June Home"},
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("station-target staffing error = %v, want conflict", err)
	}
	station, _ := workspaces.Get(scope.HomeWorkspaceID)
	if got := len(station.GetAssistantProgramState().HomeBindings.Bindings); got != 0 {
		t.Fatalf("station-target project helper added %d Home bindings", got)
	}
	if _, found := adapter.profiles.GetAgent("June Home"); found {
		t.Fatal("station-target project helper created an agent before resolving an owner")
	}
}

func TestStaffRoleOnWorkspace_HomeTargetFillsAndClearsOnlyAHomeRole(t *testing.T) {
	adapter, workspaces, scope, _ := staffingFixture(t)

	err := adapter.StaffRoleOnWorkspace(context.Background(), scope.HomeWorkspaceID, []RoleFill{
		{RoleID: "home_guide", Mode: StaffingModeCreate, Name: "June Home"},
	})
	if err != nil {
		t.Fatal(err)
	}
	station, _ := workspaces.Get(scope.HomeWorkspaceID)
	project, _ := workspaces.Get(scope.ProjectWorkspaceID)
	bindings := station.GetAssistantProgramState().HomeBindings.Bindings
	if len(bindings) != 1 || bindings[0].RoleID != "home_guide" || bindings[0].AgentName != "June Home" {
		t.Fatalf("Home bindings = %#v", bindings)
	}
	if got := len(project.GetAssistantProjectLink().ProjectBindings.Bindings); got != 0 {
		t.Fatalf("Home role write added %d project bindings", got)
	}
	if got := len(project.GetAgentInstances()); got != 0 {
		t.Fatalf("Home role write attached %d project agents", got)
	}

	if err := adapter.UnstaffRoleFromWorkspace(context.Background(), scope.HomeWorkspaceID, "home_guide"); err != nil {
		t.Fatal(err)
	}
	station, _ = workspaces.Get(scope.HomeWorkspaceID)
	if got := len(station.GetAssistantProgramState().HomeBindings.Bindings); got != 0 {
		t.Fatalf("cleared Home still has %d bindings", got)
	}
	if got := len(station.GetAgentInstances()); got != 0 {
		t.Fatalf("cleared Home still has %d attached agents", got)
	}
	if _, found := adapter.profiles.GetAgent("June Home"); !found {
		t.Fatal("clearing a Home role deleted its reusable agent definition")
	}
}

func TestUnstaffRoleFromWorkspace_HomeTargetClearsAStaleDeletedHolder(t *testing.T) {
	adapter, workspaces, scope, _ := staffingFixture(t)
	if err := adapter.StaffRoleOnWorkspace(context.Background(), scope.HomeWorkspaceID, []RoleFill{
		{RoleID: "home_guide", Mode: StaffingModeCreate, Name: "Deleted June Home"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := adapter.profiles.DeleteAgent("Deleted June Home"); err != nil {
		t.Fatal(err)
	}

	if err := adapter.UnstaffRoleFromWorkspace(context.Background(), scope.HomeWorkspaceID, "home_guide"); err != nil {
		t.Fatalf("explicit stale-holder clear failed: %v", err)
	}
	station, _ := workspaces.Get(scope.HomeWorkspaceID)
	if len(station.GetAssistantProgramState().HomeBindings.Bindings) != 0 || len(station.GetAgentInstances()) != 0 {
		t.Fatalf("stale holder survived explicit clear: state=%#v instances=%#v",
			station.GetAssistantProgramState(), station.GetAgentInstances())
	}
}

func TestStaffRoleOnWorkspace_HomeTargetAssignsWithoutChangingTheSavedDefinition(t *testing.T) {
	adapter, workspaces, scope, _ := staffingFixture(t)
	saveExistingAgent(t, adapter)
	before := len(adapter.profiles.ListAgents())

	err := adapter.StaffRoleOnWorkspace(context.Background(), scope.HomeWorkspaceID, []RoleFill{
		{RoleID: "home_guide", Mode: StaffingModeBind, Name: existingAgentName},
	})
	if err != nil {
		t.Fatal(err)
	}
	if after := len(adapter.profiles.ListAgents()); after != before {
		t.Fatalf("assign changed saved agent count from %d to %d", before, after)
	}
	station, _ := workspaces.Get(scope.HomeWorkspaceID)
	instances := station.GetAgentInstances()
	if len(instances) != 1 || instances[0].RoleSource != workspace.RoleSourceAssigned {
		t.Fatalf("assigned Home instances = %#v", instances)
	}
	profile, found := adapter.profiles.GetAgent(existingAgentName)
	if !found || profile.Settings.SystemPrompt != "the user's own prompt" {
		t.Fatalf("saved definition changed = %#v, found=%v", profile, found)
	}
}

func TestStaffRoleOnWorkspace_ProjectTargetFillsOnlyAProjectRole(t *testing.T) {
	adapter, workspaces, scope, _ := staffingFixture(t)

	err := adapter.StaffRoleOnWorkspace(context.Background(), scope.ProjectWorkspaceID, []RoleFill{
		{RoleID: "project_lead", Mode: StaffingModeCreate, Name: "Alex Lead"},
	})
	if err != nil {
		t.Fatal(err)
	}
	station, _ := workspaces.Get(scope.HomeWorkspaceID)
	project, _ := workspaces.Get(scope.ProjectWorkspaceID)
	if got := len(station.GetAssistantProgramState().HomeBindings.Bindings); got != 0 {
		t.Fatalf("project role write added %d Home bindings", got)
	}
	bindings := project.GetAssistantProjectLink().ProjectBindings.Bindings
	if len(bindings) != 1 || bindings[0].RoleID != "project_lead" {
		t.Fatalf("project bindings = %#v", bindings)
	}
}

func TestStaffRoleOnWorkspace_RejectsInverseScopeAuthority(t *testing.T) {
	tests := []struct {
		name        string
		target      func(ReadScope) string
		roleID      string
		profileName string
	}{
		{name: "station cannot fill project role", target: func(scope ReadScope) string { return scope.HomeWorkspaceID }, roleID: "project_lead", profileName: "Wrong Project"},
		{name: "child cannot fill Home role", target: func(scope ReadScope) string { return scope.ProjectWorkspaceID }, roleID: "home_guide", profileName: "Wrong Home"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter, workspaces, scope, _ := staffingFixture(t)
			err := adapter.StaffRoleOnWorkspace(context.Background(), tt.target(scope), []RoleFill{
				{RoleID: tt.roleID, Mode: StaffingModeCreate, Name: tt.profileName},
			})
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("inverse-scope error = %v, want invalid", err)
			}
			station, _ := workspaces.Get(scope.HomeWorkspaceID)
			project, _ := workspaces.Get(scope.ProjectWorkspaceID)
			if len(station.GetAgentInstances()) != 0 || len(project.GetAgentInstances()) != 0 {
				t.Fatalf("inverse write attached agents: Home=%#v project=%#v", station.GetAgentInstances(), project.GetAgentInstances())
			}
			if _, found := adapter.profiles.GetAgent(tt.profileName); found {
				t.Fatal("inverse write created an agent definition")
			}
		})
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
