package projectlibrary

import (
	"errors"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestManagerPolicy_RequiresExactLocalPrimaryRoleAndLiveProvider(t *testing.T) {
	file, scope := libraryHome(t)
	available := true
	store := NewStore(file).WithProviderEvidence(func(_ Scope, _ *workspace.Workspace) bool { return available })
	initializeLibrary(t, store, scope)
	if err := file.Update(scope.HomeID, func(home *workspace.Workspace) error {
		home.AgentInstances = []workspace.AgentInstance{{ID: "manager-instance", Name: "Manager", RoleID: "portfolio_manager"},
			{ID: "sample-instance", Name: "Sample", RoleID: "sample_library_manager"},
			{ID: "unbound-instance", Name: "Unbound", RoleID: ""}}
		state := home.GetAssistantProgramState()
		state.Declaration = &workspace.AssistantProgramDeclaration{Roles: []workspace.AssistantProgramRoleSpec{
			{ID: "portfolio_manager", Scope: workspace.AssistantRoleScopeHome, Required: true, Primary: true},
			{ID: "sample_library_manager", Scope: workspace.AssistantRoleScopeHome},
		}}
		state.HomeBindings = workspace.AssistantRoleBindingSet{StateRevision: 1, Bindings: []workspace.AssistantRoleBinding{
			{RoleID: "portfolio_manager", AgentInstanceID: "manager-instance", AgentName: "Manager"},
			{RoleID: "sample_library_manager", AgentInstanceID: "sample-instance", AgentName: "Sample"},
		}}
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Manager", "Sample"} {
		if err := file.SaveWorkspaceAgent(scope.HomeID, name, &agent.Agent{}); err != nil {
			t.Fatal(err)
		}
	}
	child := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Linked song"})
	child.OwnerUserID = scope.OwnerUserID
	child.SetAssistantProjectLink(&workspace.AssistantProjectLink{ID: workspace.AssistantProjectLinkID(scope.HomeID, child.ID),
		SchemaVersion: 1, StationWorkspaceID: scope.HomeID, StateRevision: 1,
		Key: workspace.AssistantProgramKey{OwnerUserID: scope.OwnerUserID, PluginID: scope.ProviderID, ProgramID: scope.ProgramID}})
	if err := file.Save(child); err != nil {
		t.Fatal(err)
	}
	if err := file.Update(scope.HomeID, func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.LinkedProjectIDs = []string{child.ID}
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	doc, err := store.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = store.mutate(scope, doc.Revision, operation{key: "manager-fixture-entry", action: "fixture", digest: "fixture"}, func(current *Document) (string, error) {
		current.Entries = append(current.Entries, Entry{ID: "song", Revision: 1,
			Link:   &ExactLink{WorkspaceID: child.ID, LinkID: workspace.AssistantProjectLinkID(scope.HomeID, child.ID), Revision: 1},
			Fields: Fields{DisplayName: "Private user note", Purpose: "Untrusted lyrics"}})
		return "song", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := ManagerAuthority{HomeID: scope.HomeID, AgentInstanceID: "manager-instance", AgentName: "Manager"}
	page, err := store.SearchForManager(manager, Search{PageSize: 100, Sort: "name"})
	if err != nil || len(page.Rows) != 1 || page.Rows[0].Name != "Private user note" {
		t.Fatalf("valid local Manager read: %+v %v", page, err)
	}
	detail, err := store.DetailForManager(manager, "song")
	if err != nil || detail.Row.ID != "song" || len(detail.Sources) != 0 || detail.Fields.Purpose != "Untrusted lyrics" {
		t.Fatalf("bounded Home-only detail: %+v %v", detail, err)
	}
	for name, denied := range map[string]ManagerAuthority{
		"global fallback without instance": {HomeID: scope.HomeID, AgentName: "Manager"},
		"another Home":                     {HomeID: "other-home", AgentInstanceID: "manager-instance", AgentName: "Manager"},
		"wrong instance":                   {HomeID: scope.HomeID, AgentInstanceID: "sample-instance", AgentName: "Manager"},
		"optional Sample":                  {HomeID: scope.HomeID, AgentInstanceID: "sample-instance", AgentName: "Sample"},
		"unbound local":                    {HomeID: scope.HomeID, AgentInstanceID: "unbound-instance", AgentName: "Unbound"},
	} {
		if _, err := store.SearchForManager(denied, Search{}); !errors.Is(err, ErrUnavailable) {
			t.Errorf("%s acquired Manager access: %v", name, err)
		}
	}
	if err := file.Update(scope.HomeID, func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.HomeBindings.Bindings = state.HomeBindings.Bindings[1:] // remove only Manager binding
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SearchForManager(manager, Search{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("removed role binding retained access: %v", err)
	}
	if err := file.Update(scope.HomeID, func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.HomeBindings.Bindings = append(state.HomeBindings.Bindings, workspace.AssistantRoleBinding{
			RoleID: "portfolio_manager", AgentInstanceID: "manager-instance", AgentName: "Manager"})
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	available = false
	if _, err := store.SearchForManager(manager, Search{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("provider loss retained tool read: %v", err)
	}
	available = true
	if _, err := store.DetailForManager(manager, strings.Repeat("x", 161)); !errors.Is(err, ErrConflict) {
		t.Fatalf("malformed entry ID acquired details: %v", err)
	}
}
