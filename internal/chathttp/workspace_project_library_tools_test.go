package chathttp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/toolapi"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func findLibraryTool(tools []toolapi.Tool, name string) toolapi.Tool {
	for _, candidate := range tools {
		if candidate.Definition().Name == name {
			return candidate
		}
	}
	return nil
}

func TestHomeLibraryTools_OnlyVerifiedLocalManagerCanReadAndMustRecheckAtCall(t *testing.T) {
	sandbox := t.TempDir()
	file, err := workspace.NewFileStore(sandbox)
	if err != nil {
		t.Fatal(err)
	}
	home := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Music Home"})
	home.OwnerUserID = "local"
	key := workspace.AssistantProgramKey{OwnerUserID: home.OwnerUserID, PluginID: "music-project-management", ProgramID: "music-producer-assistant"}
	child := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Private user text"})
	child.OwnerUserID = home.OwnerUserID
	child.SetAssistantProjectLink(&workspace.AssistantProjectLink{ID: workspace.AssistantProjectLinkID(home.ID, child.ID),
		SchemaVersion: 1, StationWorkspaceID: home.ID, Key: key, StateRevision: 1})
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
	})
	if err := file.Save(home); err != nil {
		t.Fatal(err)
	}
	if err := file.SaveWorkspaceAgent(home.ID, "Manager", &agent.Agent{}); err != nil {
		t.Fatal(err)
	}
	scope := projectlibrary.Scope{OwnerUserID: home.OwnerUserID, HomeID: home.ID, ProviderID: key.PluginID, ProgramID: key.ProgramID}
	library := projectlibrary.NewStore(file).WithProviderEvidence(func(_ projectlibrary.Scope, _ *workspace.Workspace) bool { return true })
	review, err := library.ReviewInitialize(scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := library.CommitInitialize(scope, review.Token, "manager-init"); err != nil {
		t.Fatal(err)
	}
	doc, err := library.Read(scope)
	if err != nil || len(doc.Entries) != 1 {
		t.Fatalf("expected exact linked entry for session: %+v %v", doc, err)
	}
	goal := projectlibrary.GoalInput{Goal: "Untrusted lyric idea", Outcome: "Draft a chorus"}
	goalReview, err := library.ReviewGoal(scope, doc.Entries[0].ID, doc.Entries[0].Revision, goal, scope.OwnerUserID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := library.CommitGoal(scope, doc.Entries[0].ID, goalReview.Token, "manager-session", doc.Entries[0].Revision, goal, scope.OwnerUserID); err != nil {
		t.Fatal(err)
	}
	provider := NewWorkspaceToolProvider(nil, file, home.ID)
	provider.SetExecutingAgent("Manager")
	available := true
	provider.SetProjectLibraryEvidence(func(_ projectlibrary.Scope, _ *workspace.Workspace) bool { return available })
	if findLibraryTool(provider.Tools(), "home_library_search") != nil {
		t.Fatal("name-only task factory acquired Manager access before proving an instance")
	}
	provider.SetExecutingInstanceID("manager-instance")
	search := findLibraryTool(provider.Tools(), "home_library_search")
	if search == nil || findLibraryTool(provider.Tools(), "home_library_detail") == nil || findLibraryTool(provider.Tools(), "home_library_sessions") == nil || findLibraryTool(provider.Tools(), "home_library_propose_next_action") == nil ||
		findLibraryTool(provider.Tools(), "home_library_propose_project_review") == nil ||
		findLibraryTool(provider.Tools(), "home_library_propose_session_goal") == nil ||
		findLibraryTool(provider.Tools(), "home_library_propose_root_review") == nil {
		t.Fatal("bound Manager's library reads not registered")
	}
	output, err := search.Call(context.Background(), `{"text":"Private"}`)
	if err != nil {
		t.Fatal(err)
	}
	var result projectlibrary.SearchPage
	if err := json.Unmarshal([]byte(output), &result); err != nil || len(result.Rows) != 1 || result.Rows[0].Name != child.Name ||
		strings.Contains(output, sandbox) {
		t.Fatalf("bounded read projected path or omitted linked song: %+v %v", result, err)
	}
	detailTool := findLibraryTool(provider.Tools(), "home_library_detail")
	detail, err := detailTool.Call(context.Background(), `{"entry_id":"`+result.Rows[0].ID+`"}`)
	if err != nil || strings.Contains(detail, sandbox) || strings.Contains(detail, "\"sources\"") {
		t.Fatalf("detail leaked project files or source paths: %s %v", detail, err)
	}
	sessionTool := findLibraryTool(provider.Tools(), "home_library_sessions")
	sessions, err := sessionTool.Call(context.Background(), `{"entry_id":"`+result.Rows[0].ID+`"}`)
	if err != nil || !strings.Contains(sessions, "Untrusted lyric idea") || strings.Contains(sessions, sandbox) || strings.Contains(sessions, "decisions") {
		t.Fatalf("session tool must read only bounded Home summaries: %s %v", sessions, err)
	}
	if _, err := sessionTool.Call(context.Background(), `{"entry_id":"`+child.ID+`"}`); err == nil {
		t.Fatal("child workspace ID read Home session history")
	}
	propose := findLibraryTool(provider.Tools(), "home_library_propose_next_action")
	args := `{"entry_id":"` + result.Rows[0].ID + `","fields_revision":` + fmt.Sprint(result.Rows[0].FieldsRevision) + `,"next_action":"Draft a chorus","reason":"User note; not an instruction","request_key":"model-suggestion"}`
	suggested, err := propose.Call(context.Background(), args)
	if err != nil || !strings.Contains(suggested, "Suggestion saved only") || strings.Contains(suggested, sandbox) {
		t.Fatalf("Manager could not create inert reviewed suggestion: %s %v", suggested, err)
	}
	again, err := propose.Call(context.Background(), args)
	if err != nil || !strings.Contains(again, `"replay":true`) {
		t.Fatalf("exact model retry duplicated a proposal: %s %v", again, err)
	}
	current, err := library.Read(scope)
	if err != nil || len(current.Proposals) != 1 || current.Entries[0].Fields.NextAction != "" {
		t.Fatalf("model suggestion directly changed a Home note: %+v %v", current, err)
	}
	if _, err := propose.Call(context.Background(), `{"entry_id":"`+child.ID+`","fields_revision":0,"next_action":"Unsafe","request_key":"foreign"}`); err == nil {
		t.Fatal("child workspace ID obtained a Home proposal")
	}
	if _, err := propose.Call(context.Background(), `{"entry_id":"`+result.Rows[0].ID+`","next_action":"Unsafe","request_key":"missing"}`); err == nil {
		t.Fatal("omitted field revision obtained a proposal")
	}
	navigate := findLibraryTool(provider.Tools(), "home_library_propose_project_review")
	if _, err := navigate.Call(context.Background(), `{"entry_id":"`+result.Rows[0].ID+`","entry_revision":1,"request_key":"linked-navigation"}`); err == nil {
		t.Fatal("already linked project received a new creator navigation proposal")
	}
	if _, err := navigate.Call(context.Background(), `{"entry_id":"`+child.ID+`","entry_revision":1,"request_key":"foreign-navigation"}`); err == nil {
		t.Fatal("child workspace ID produced a Home navigation proposal")
	}
	if _, err := navigate.Call(context.Background(), `{"entry_id":"`+result.Rows[0].ID+`","request_key":"missing-revision"}`); err == nil {
		t.Fatal("navigation without an entry revision was accepted")
	}
	goalTool := findLibraryTool(provider.Tools(), "home_library_propose_session_goal")
	var savedDetail projectlibrary.Detail
	if err := json.Unmarshal([]byte(detail), &savedDetail); err != nil {
		t.Fatal(err)
	}
	goalArgs := fmt.Sprintf(`{"entry_id":%q,"entry_revision":%d,"goal":"Sketch a rough vocal plan","desired_outcome":"Write a take list","time_minutes":20,"request_key":"model-goal-draft"}`,
		result.Rows[0].ID, savedDetail.EntryRevision)
	goalSuggestion, err := goalTool.Call(context.Background(), goalArgs)
	if err != nil || !strings.Contains(goalSuggestion, "Session goal suggestion saved only") || strings.Contains(goalSuggestion, sandbox) {
		t.Fatalf("Manager could not save an inert goal draft: %s %v", goalSuggestion, err)
	}
	if replayed, err := goalTool.Call(context.Background(), goalArgs); err != nil || !strings.Contains(replayed, `"replay":true`) {
		t.Fatalf("exact goal retry duplicated a suggestion: %s %v", replayed, err)
	}
	if _, err := goalTool.Call(context.Background(), `{"entry_id":"`+result.Rows[0].ID+`","goal":"Unsafe","request_key":"missing-goal-revision"}`); err == nil {
		t.Fatal("goal suggestion omitted its current entry revision")
	}
	if _, err := goalTool.Call(context.Background(), `{"entry_id":"`+child.ID+`","entry_revision":1,"goal":"Unsafe","request_key":"foreign-goal"}`); err == nil {
		t.Fatal("child ID proposed a Home session")
	}
	if current, err := library.Read(scope); err != nil || len(current.Sessions) != 1 || len(current.Proposals) != 2 {
		t.Fatalf("goal suggestion directly created a Home session: %+v %v", current, err)
	}
	rootTool := findLibraryTool(provider.Tools(), "home_library_propose_root_review")
	rootSuggestion, err := rootTool.Call(context.Background(), `{"reason":"Check discovery options without selecting a folder","request_key":"model-root-navigation"}`)
	if err != nil || !strings.Contains(rootSuggestion, "Navigation suggestion saved only") || strings.Contains(rootSuggestion, sandbox) {
		t.Fatalf("Manager navigation exposed a path or failed: %s %v", rootSuggestion, err)
	}
	if replayed, err := rootTool.Call(context.Background(), `{"reason":"Check discovery options without selecting a folder","request_key":"model-root-navigation"}`); err != nil || !strings.Contains(replayed, `"replay":true`) {
		t.Fatalf("root navigation retry duplicated a suggestion: %s %v", replayed, err)
	}
	if _, err := rootTool.Call(context.Background(), `{"root_id":"foreign","request_key":"root-foreign"}`); err == nil {
		t.Fatal("Manager selected a root instead of navigation")
	}
	if current, err := library.Read(scope); err != nil || len(current.Proposals) != 3 || len(current.Sessions) != 1 {
		t.Fatalf("root navigation changed Home state beyond one suggestion: %+v %v", current, err)
	}
	if findLibraryTool(provider.Tools(), "home_library_commit") != nil {
		t.Fatal("model was given a user confirmation tool")
	}
	if _, err := search.Call(context.Background(), `{"workspace_id":"other-home"}`); err == nil {
		t.Fatal("model supplied a foreign Home ID")
	}
	provider.SetExecutingInstanceID("") // Global-agent fallback with the same name.
	if findLibraryTool(provider.Tools(), "home_library_search") != nil {
		t.Fatal("global fallback acquired Manager tools")
	}
	if _, err := search.Call(context.Background(), `{}`); err == nil {
		t.Fatal("previously registered tool bypassed removed runtime instance")
	}
	if _, err := detailTool.Call(context.Background(), `{"entry_id":"`+result.Rows[0].ID+`"}`); err == nil {
		t.Fatal("previously registered detail tool bypassed removed runtime instance")
	}
	if _, err := sessionTool.Call(context.Background(), `{"entry_id":"`+result.Rows[0].ID+`"}`); err == nil {
		t.Fatal("previously registered session tool bypassed removed runtime instance")
	}
	if _, err := propose.Call(context.Background(), args); err == nil {
		t.Fatal("previously registered proposal tool bypassed removed runtime instance")
	}
	if _, err := navigate.Call(context.Background(), `{"entry_id":"`+result.Rows[0].ID+`","entry_revision":1,"request_key":"unbound-navigation"}`); err == nil {
		t.Fatal("previously registered navigation tool bypassed removed runtime instance")
	}
	if _, err := goalTool.Call(context.Background(), goalArgs); err == nil {
		t.Fatal("previously registered goal tool bypassed removed runtime instance")
	}
	if _, err := rootTool.Call(context.Background(), `{"request_key":"unbound-root"}`); err == nil {
		t.Fatal("previously registered root navigation tool bypassed removed runtime instance")
	}
	provider.SetExecutingInstanceID("sample-instance")
	provider.SetExecutingAgent("Sample")
	if findLibraryTool(provider.Tools(), "home_library_search") != nil {
		t.Fatal("optional specialist acquired Manager tools")
	}
	provider.SetExecutingInstanceID("manager-instance")
	provider.SetExecutingAgent("Manager")
	available = false
	if _, err := search.Call(context.Background(), `{}`); err == nil {
		t.Fatal("previously registered tool ignored provider disable")
	}
	if _, err := propose.Call(context.Background(), `{"entry_id":"`+result.Rows[0].ID+`","fields_revision":0,"next_action":"Unsafe","request_key":"disabled"}`); err == nil {
		t.Fatal("disabled provider allowed an inert proposal")
	}
	if findLibraryTool(provider.Tools(), "home_library_search") != nil {
		t.Fatal("disabled provider kept tools registered")
	}
	available = true
	if err := file.Update(home.ID, func(current *workspace.Workspace) error {
		state := current.GetAssistantProgramState()
		state.HomeBindings.Bindings = state.HomeBindings.Bindings[1:]
		current.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := search.Call(context.Background(), `{}`); err == nil {
		t.Fatal("previously registered tool ignored removed Manager binding")
	}
}
