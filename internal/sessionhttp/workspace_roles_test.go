package sessionhttp

import (
	"reflect"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspaceroles"
)

// splitHomeAndChild builds a Home and one linked child the way the connection
// flow leaves them: the Home's declaration lists the Home's roles, and the child's
// link snapshots its own project roles.
func splitHomeAndChild(t *testing.T, projectRoles []workspace.AssistantProgramRoleSpec) (*Handler, *workspace.Workspace, *workspace.Workspace) {
	t.Helper()
	handler, cleanup := createTestHandler(t)
	t.Cleanup(cleanup)
	store := workspace.NewInMemoryStore()
	handler.SetWorkspaceTaskStore(store)
	key := workspace.AssistantProgramKey{OwnerUserID: "local", PluginID: "test-home", ProgramID: "portfolio"}

	child := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Linked Song"})
	child.OwnerUserID = "local"
	home := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Portfolio Home"})
	home.OwnerUserID = "local"
	home.SetAssistantProgramState(&workspace.AssistantProgramState{
		Key: key,
		Declaration: &workspace.AssistantProgramDeclaration{
			SchemaVersion: workspace.AssistantProgramSchemaVersion, ID: "portfolio",
			Roles: []workspace.AssistantProgramRoleSpec{
				{ID: "portfolio_manager", Label: "Portfolio Manager", Scope: workspace.AssistantRoleScopeHome, Required: true, Primary: true},
				{ID: "sample_library", Label: "Sample Library", Scope: workspace.AssistantRoleScopeHome},
			},
		},
		LinkedProjectIDs: []string{child.ID},
		HomeBindings: workspace.AssistantRoleBindingSet{Bindings: []workspace.AssistantRoleBinding{
			{RoleID: "portfolio_manager", AgentName: "Home Manager"},
		}},
	})
	child.SetAssistantProjectLink(&workspace.AssistantProjectLink{
		ID: "link-1", SchemaVersion: 1, StationWorkspaceID: home.ID, Key: key,
		ProjectRoles: projectRoles,
	})
	for _, item := range []*workspace.Workspace{home, child} {
		if err := store.Save(item); err != nil {
			t.Fatal(err)
		}
	}
	return handler, home, child
}

func roleIDs(roles []workspaceroles.Role) []string {
	ids := make([]string, 0, len(roles))
	for _, role := range roles {
		ids = append(ids, role.ID)
	}
	return ids
}

func TestDeclaredWorkspaceRoles_ALinkedChildListsItsOwnProjectRolesNotTheHomes(t *testing.T) {
	handler, _, child := splitHomeAndChild(t, []workspace.AssistantProgramRoleSpec{
		{ID: "song-assistant", Label: "Song Assistant", Scope: workspace.AssistantRoleScopeProject, Required: true, Primary: true},
		// A Home-scoped entry can never be staffed on a child, even if a snapshot
		// were to carry one.
		{ID: "portfolio_manager", Label: "Portfolio Manager", Scope: workspace.AssistantRoleScopeHome},
	})
	stored, err := handler.workspaceTaskStore.Get(child.ID)
	if err != nil {
		t.Fatal(err)
	}
	roles, bindings, home := handler.declaredWorkspaceRoles(stored)
	if got := roleIDs(roles); !reflect.DeepEqual(got, []string{"song-assistant"}) {
		t.Fatalf("a linked child listed %v, want only its own project role", got)
	}
	if roles[0].Scope != workspaceroles.ScopeProject || !roles[0].Required || !roles[0].Primary {
		t.Fatalf("project role lost its declaration: %+v", roles[0])
	}
	if len(bindings) != 0 {
		t.Fatalf("the child inherited the Home's bindings: %v", bindings)
	}
	if home == "" {
		t.Fatal("the roster lost the Home it belongs to")
	}
}

func TestDeclaredWorkspaceRoles_AChildUsesOnlyItsOwnBindings(t *testing.T) {
	handler, _, child := splitHomeAndChild(t, []workspace.AssistantProgramRoleSpec{
		{ID: "song-assistant", Label: "Song Assistant", Scope: workspace.AssistantRoleScopeProject, Required: true},
	})
	stored, err := handler.workspaceTaskStore.Get(child.ID)
	if err != nil {
		t.Fatal(err)
	}
	link := stored.GetAssistantProjectLink()
	link.ProjectBindings = workspace.AssistantRoleBindingSet{Bindings: []workspace.AssistantRoleBinding{
		{RoleID: "song-assistant", AgentName: "Song Helper"},
	}}
	stored.SetAssistantProjectLink(link)
	_, bindings, _ := handler.declaredWorkspaceRoles(stored)
	if !reflect.DeepEqual(bindings, map[string]string{"song-assistant": "Song Helper"}) {
		t.Fatalf("bindings = %v, want only this child's own", bindings)
	}
}

func TestDeclaredWorkspaceRoles_TheHomeAndAnOlderLinkKeepTheirBehavior(t *testing.T) {
	handler, home, child := splitHomeAndChild(t, nil)

	// The Home page still lists the Home's declaration and its own bindings.
	storedHome, err := handler.workspaceTaskStore.Get(home.ID)
	if err != nil {
		t.Fatal(err)
	}
	homeRoles, homeBindings, homeID := handler.declaredWorkspaceRoles(storedHome)
	if got := roleIDs(homeRoles); !reflect.DeepEqual(got, []string{"portfolio_manager", "sample_library"}) {
		t.Fatalf("the Home listed %v", got)
	}
	if homeBindings["portfolio_manager"] != "Home Manager" || homeID != home.ID {
		t.Fatalf("the Home lost its bindings: %v (%s)", homeBindings, homeID)
	}

	// A link with no role snapshot (recorded before snapshots) is not guessed at:
	// it keeps the station-declaration behavior it always had.
	storedChild, err := handler.workspaceTaskStore.Get(child.ID)
	if err != nil {
		t.Fatal(err)
	}
	legacy, _, _ := handler.declaredWorkspaceRoles(storedChild)
	if got := roleIDs(legacy); !reflect.DeepEqual(got, []string{"portfolio_manager", "sample_library"}) {
		t.Fatalf("a child with no snapshot changed behavior: %v", got)
	}
}
