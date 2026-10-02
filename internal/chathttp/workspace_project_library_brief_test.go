package chathttp

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// The collection-brief tool is the scan-review turn's alone (T5): a Manager
// chat never sees it, nor does a suggestions turn, and a brief turn's tool
// still saves nothing outside an active receipt.
func TestHomeLibraryBriefTool_OnlyInsideABriefTurn(t *testing.T) {
	file, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Music Home"})
	home.OwnerUserID = "local"
	key := workspace.AssistantProgramKey{OwnerUserID: "local", PluginID: "music-project-management", ProgramID: "music-producer-assistant"}
	home.AgentInstances = []workspace.AgentInstance{{ID: "manager-instance", Name: "Manager", RoleID: "portfolio_manager"}}
	home.SetAssistantProgramState(&workspace.AssistantProgramState{
		SchemaVersion: workspace.AssistantProgramStateSchemaVersion, PluginAvailable: true, Key: key,
		Declaration: &workspace.AssistantProgramDeclaration{Roles: []workspace.AssistantProgramRoleSpec{
			{ID: "portfolio_manager", Label: "Portfolio Manager", Scope: workspace.AssistantRoleScopeHome, Required: true, Primary: true}}},
		HomeBindings: workspace.AssistantRoleBindingSet{StateRevision: 1, Bindings: []workspace.AssistantRoleBinding{
			{RoleID: "portfolio_manager", AgentInstanceID: "manager-instance", AgentName: "Manager"}}},
	})
	if err := file.Save(home); err != nil {
		t.Fatal(err)
	}
	if err := file.SaveWorkspaceAgent(home.ID, "Manager", &agent.Agent{}); err != nil {
		t.Fatal(err)
	}
	if err := workspace.NewSongDetailsConsents(file).Grant(home.ID, "offer-1"); err != nil {
		t.Fatal(err)
	}
	scope := projectlibrary.Scope{OwnerUserID: "local", HomeID: home.ID, ProviderID: key.PluginID, ProgramID: key.ProgramID}
	library := projectlibrary.NewStore(file).WithProviderEvidence(func(projectlibrary.Scope, *workspace.Workspace) bool { return true })
	review, err := library.ReviewInitialize(scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := library.CommitInitialize(scope, review.Token, "brief-init"); err != nil {
		t.Fatal(err)
	}
	provider := func(run projectlibrary.ManagerRunContext) *WorkspaceToolProvider {
		p := NewWorkspaceToolProvider(nil, file, home.ID)
		p.SetExecutingAgent("Manager")
		p.SetExecutingInstanceID("manager-instance")
		p.SetProjectLibraryEvidence(func(projectlibrary.Scope, *workspace.Workspace) bool { return true })
		p.SetManagerRun(run)
		return p
	}
	chat := provider(projectlibrary.ManagerRunContext{})
	if findLibraryTool(chat.Tools(), "home_library_search") == nil {
		t.Fatal("fixture Manager has no library tools")
	}
	if findLibraryTool(chat.Tools(), "home_library_save_brief") != nil {
		t.Fatal("a Manager chat was offered the brief tool")
	}
	suggestions := provider(projectlibrary.ManagerRunContext{ScanID: "scan-1", Model: "test-model"})
	if findLibraryTool(suggestions.Tools(), "home_library_save_brief") != nil {
		t.Fatal("a suggestions turn was offered the brief tool")
	}
	brief := findLibraryTool(provider(projectlibrary.ManagerRunContext{ScanID: "scan-1", Model: "test-model", Brief: true}).Tools(),
		"home_library_save_brief")
	if brief == nil {
		t.Fatal("a brief turn has no brief tool")
	}
	if out, err := brief.Call(context.Background(), `{"text":"Four songs sit between 90 and 99 BPM."}`); err == nil {
		t.Fatalf("the brief tool saved outside an active turn: %s", out)
	}
	if out, err := brief.Call(context.Background(), `{"text":"Four songs.","workspace_id":"x"}`); err == nil {
		t.Fatalf("the brief tool accepted an unknown argument: %s", out)
	}
	if doc, err := library.Read(scope); err != nil || doc.CollectionBrief != nil {
		t.Fatalf("a refused brief was stored: %+v %v", doc.CollectionBrief, err)
	}
}
