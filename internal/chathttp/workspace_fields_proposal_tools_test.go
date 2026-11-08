package chathttp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type fieldsToolFixture struct {
	file    *workspace.FileStore
	home    *workspace.Workspace
	child   *workspace.Workspace
	scope   projectlibrary.Scope
	library *projectlibrary.Store
	entry   projectlibrary.Entry
}

// newFieldsToolFixture is a Home with a bound Manager, a second Home agent,
// one linked song with a library entry, and the words snapshot of a package
// that declares a "key" text field.
func newFieldsToolFixture(t *testing.T) *fieldsToolFixture {
	t.Helper()
	file, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Music Home"})
	home.OwnerUserID = "local"
	key := workspace.AssistantProgramKey{OwnerUserID: home.OwnerUserID, PluginID: "music-project-management", ProgramID: "music-producer-assistant"}
	child := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Night Drive"})
	child.OwnerUserID = home.OwnerUserID
	child.SetAssistantProjectLink(&workspace.AssistantProjectLink{ID: workspace.AssistantProjectLinkID(home.ID, child.ID),
		SchemaVersion: 1, StationWorkspaceID: home.ID, Key: key, StateRevision: 1})
	child.AgentInstances = []workspace.AgentInstance{{ID: "keeper-instance", Name: "Keeper", EntryPoint: true},
		{ID: "helper-instance", Name: "Helper"}}
	if err := child.SetEntryAgentName("Keeper"); err != nil {
		t.Fatal(err)
	}
	if err := file.Save(child); err != nil {
		t.Fatal(err)
	}
	home.AgentInstances = []workspace.AgentInstance{{ID: "manager-instance", Name: "Manager", RoleID: "portfolio_manager"},
		{ID: "sample-instance", Name: "Sample", RoleID: "sample_library_manager"}}
	home.SetAssistantProgramState(&workspace.AssistantProgramState{
		SchemaVersion: workspace.AssistantProgramStateSchemaVersion, PluginAvailable: true, Key: key,
		Declaration: &workspace.AssistantProgramDeclaration{Roles: []workspace.AssistantProgramRoleSpec{
			{ID: "portfolio_manager", Scope: workspace.AssistantRoleScopeHome, Required: true, Primary: true},
			{ID: "sample_library_manager", Scope: workspace.AssistantRoleScopeHome}}},
		HomeBindings: workspace.AssistantRoleBindingSet{StateRevision: 1, Bindings: []workspace.AssistantRoleBinding{
			{RoleID: "portfolio_manager", AgentInstanceID: "manager-instance", AgentName: "Manager"},
			{RoleID: "sample_library_manager", AgentInstanceID: "sample-instance", AgentName: "Sample"}}},
		LinkedProjectIDs: []string{child.ID},
		ProjectCardWords: &workspace.ProjectCardWords{PluginID: key.PluginID, Version: "0.5.0", Noun: "Song",
			NounPlural: "Songs", RosterTitle: "Songs and assistants",
			Fields: []workspace.ProjectCardWordsField{{ID: "key", Label: "Key", Kind: "text"}}},
	})
	if err := file.Save(home); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Manager", "Sample"} {
		if err := file.SaveWorkspaceAgent(home.ID, name, &agent.Agent{}); err != nil {
			t.Fatal(err)
		}
	}
	scope := projectlibrary.Scope{OwnerUserID: home.OwnerUserID, HomeID: home.ID, ProviderID: key.PluginID, ProgramID: key.ProgramID}
	library := projectlibrary.NewStore(file).WithProviderEvidence(func(projectlibrary.Scope, *workspace.Workspace) bool { return true })
	review, err := library.ReviewInitialize(scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := library.CommitInitialize(scope, review.Token, "fields-tool-init"); err != nil {
		t.Fatal(err)
	}
	doc, err := library.Read(scope)
	if err != nil || len(doc.Entries) != 1 {
		t.Fatalf("library: %+v %v", doc, err)
	}
	return &fieldsToolFixture{file: file, home: home, child: child, scope: scope, library: library, entry: doc.Entries[0]}
}

func (f *fieldsToolFixture) provider(agentName, instanceID string) *WorkspaceToolProvider {
	provider := NewWorkspaceToolProvider(nil, f.file, f.home.ID)
	provider.SetExecutingAgent(agentName)
	provider.SetExecutingInstanceID(instanceID)
	provider.SetProjectLibraryEvidence(func(projectlibrary.Scope, *workspace.Workspace) bool { return true })
	return provider
}

func (f *fieldsToolFixture) songProvider(agentName, instanceID string) *WorkspaceToolProvider {
	provider := NewWorkspaceToolProvider(nil, f.file, f.child.ID)
	provider.SetExecutingAgent(agentName)
	provider.SetExecutingInstanceID(instanceID)
	provider.SetProjectLibraryEvidence(func(projectlibrary.Scope, *workspace.Workspace) bool { return true })
	return provider
}

func TestProjectCardTool_OnlyTheSongsKeeperSuggestsAndLearnsNothingBack(t *testing.T) {
	f := newFieldsToolFixture(t)
	for name, provider := range map[string]*WorkspaceToolProvider{
		"another song agent": f.songProvider("Helper", "helper-instance"),
		"name only":          f.songProvider("Keeper", ""),
		"forged instance":    f.songProvider("Keeper", "manager-instance"),
		"the Home Manager":   f.provider("Manager", "manager-instance"),
	} {
		if findLibraryTool(provider.Tools(), "project_card_propose_fields") != nil {
			t.Errorf("%s got the keeper tool", name)
		}
	}
	tool := findLibraryTool(f.songProvider("Keeper", "keeper-instance").Tools(), "project_card_propose_fields")
	if tool == nil {
		t.Fatal("the song's keeper lacks the tool")
	}
	if !strings.Contains(tool.Definition().Description, "this song's card") {
		t.Fatalf("the tool is not worded from the Home's words: %s", tool.Definition().Description)
	}
	out, err := tool.Call(context.Background(), `{"next_action":"Send stems","deliverables":["Stems v2"],"reason":"Bounced","request_key":"keeper-tool-1"}`)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(result))
	for key := range result {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if strings.Join(keys, ",") != "effect,expires_at,proposal_id,replay" ||
		result["effect"] != "Suggestion saved only; the owner accepts or dismisses it on the song card." {
		t.Fatalf("the keeper learned more than its receipt: %s", out)
	}
	for _, leak := range []string{f.entry.ID, f.home.ID, "fields_revision", "/workspaces/"} {
		if strings.Contains(out, leak) {
			t.Fatalf("result leaked %q: %s", leak, out)
		}
	}
	doc, err := f.library.Read(f.scope)
	if err != nil || len(doc.Proposals) != 1 || doc.Proposals[0].ProposerKind != "keeper" || doc.Proposals[0].EntryID != f.entry.ID {
		t.Fatalf("saved: %+v %v", doc.Proposals, err)
	}
	// IDs and owner-only fields are refused; nothing more is saved.
	for name, bad := range map[string]string{
		"entry id":     `{"entry_id":"` + f.entry.ID + `","next_action":"x","request_key":"k1"}`,
		"workspace id": `{"workspace_id":"` + f.home.ID + `","next_action":"x","request_key":"k2"}`,
		"stage":        `{"stage":"mixing","request_key":"k3"}`,
		"undeclared":   `{"extra":{"mood":["dark"]},"request_key":"k4"}`,
	} {
		if _, err := tool.Call(context.Background(), bad); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := tool.Call(context.Background(), `{"stage":"mixing","request_key":"k5"}`); err == nil ||
		!strings.Contains(err.Error(), "stage cannot be suggested") {
		t.Fatalf("stage refusal: %v", err)
	}
	if doc, _ := f.library.Read(f.scope); len(doc.Proposals) != 1 {
		t.Fatalf("a refused call saved something: %d", len(doc.Proposals))
	}
	// A non-song workspace (the Home itself) never exposes it.
	if findLibraryTool(f.provider("Keeper", "keeper-instance").Tools(), "project_card_propose_fields") != nil {
		t.Fatal("the Home workspace exposed the keeper tool")
	}
}

func TestHomeLibraryTools_ProposeFieldsForTheBoundManagerOnly(t *testing.T) {
	f := newFieldsToolFixture(t)
	if findLibraryTool(f.provider("Manager", "").Tools(), "home_library_propose_fields") != nil {
		t.Fatal("a name-only provider got the fields tool")
	}
	if findLibraryTool(f.provider("Sample", "sample-instance").Tools(), "home_library_propose_fields") != nil {
		t.Fatal("another Home agent got the fields tool")
	}
	songProvider := NewWorkspaceToolProvider(nil, f.file, f.child.ID)
	songProvider.SetExecutingAgent("Manager")
	songProvider.SetExecutingInstanceID("manager-instance")
	songProvider.SetProjectLibraryEvidence(func(projectlibrary.Scope, *workspace.Workspace) bool { return true })
	if findLibraryTool(songProvider.Tools(), "home_library_propose_fields") != nil {
		t.Fatal("a song workspace exposed the Home Manager's fields tool")
	}
	tool := findLibraryTool(f.provider("Manager", "manager-instance").Tools(), "home_library_propose_fields")
	if tool == nil {
		t.Fatal("the bound Manager lacks the fields tool")
	}
	head := fmt.Sprintf(`"entry_id":%q,"fields_revision":%d,"request_key":`, f.entry.ID, f.entry.Fields.Revision)
	args := `{` + head + `"tool-fields-1","next_action":"Send stems","deliverables":["Stems v2"],"extra":{"key":["F minor"]},"reason":"Bounced"}`
	out, err := tool.Call(context.Background(), args)
	if err != nil || !strings.Contains(out, `"proposal_id"`) || !strings.Contains(out, `"replay":false`) ||
		!strings.Contains(out, "Suggestion saved only") {
		t.Fatalf("save: %s %v", out, err)
	}
	if again, err := tool.Call(context.Background(), args); err != nil || !strings.Contains(again, `"replay":true`) {
		t.Fatalf("replay: %s %v", again, err)
	}
	doc, err := f.library.Read(f.scope)
	if err != nil || len(doc.Proposals) != 1 || doc.Proposals[0].Kind != "fields" || doc.Entries[0].Fields.NextAction != "" {
		t.Fatalf("the suggestion changed a note or did not save: %+v %v", doc.Proposals, err)
	}

	// Owner-only fields are refused by name; nothing is saved.
	for _, field := range []string{"stage", "status", "priority", "purpose", "release_date", "archive_review_state"} {
		_, err := tool.Call(context.Background(), `{`+head+`"owner-`+field+`","`+field+`":"x","next_action":"y"}`)
		if err == nil || !strings.Contains(err.Error(), field+" cannot be suggested") {
			t.Fatalf("%s: %v", field, err)
		}
	}
	// An undeclared plugin field, a workspace id and an unknown key are refused.
	for name, bad := range map[string]string{
		"undeclared":   `{` + head + `"undeclared","extra":{"mood":["dark"]}}`,
		"workspace id": `{` + head + `"wsid","next_action":"x","workspace_id":"` + f.child.ID + `"}`,
		"nothing":      `{` + head + `"nothing"}`,
		"no revision":  fmt.Sprintf(`{"entry_id":%q,"request_key":"no-rev","next_action":"x"}`, f.entry.ID),
	} {
		if _, err := tool.Call(context.Background(), bad); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if doc, _ := f.library.Read(f.scope); len(doc.Proposals) != 1 {
		t.Fatalf("a refused call saved something: %+v", doc.Proposals)
	}

	// Policy is rechecked at the call: an unbound Manager's held tool refuses.
	if err := f.file.Update(f.home.ID, func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.HomeBindings.Bindings = state.HomeBindings.Bindings[1:]
		state.HomeBindings.StateRevision++
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Call(context.Background(), `{`+head+`"after-unbind","next_action":"x"}`); err == nil {
		t.Fatal("an unbound Manager saved a suggestion through a held tool")
	}
}
