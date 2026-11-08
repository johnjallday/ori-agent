package workspace

import (
	"context"
	"strings"
	"testing"
)

// closingStepFixture is a Home (bound Manager "Manager", words snapshot for
// "Song") and one linked song kept by "Keeper", plus a plain workspace.
func closingStepFixture(t *testing.T, withWords bool) (*LLMTaskHandler, *Workspace, *Workspace, *Workspace) {
	t.Helper()
	store := NewInMemoryStore()
	key := AssistantProgramKey{OwnerUserID: "owner", PluginID: "music", ProgramID: "music-home"}
	home := NewWorkspace(CreateWorkspaceParams{Name: "Music Home"})
	home.OwnerUserID = "owner"
	home.AgentInstances = []AgentInstance{{ID: "manager-1", Name: "Manager", RoleID: "manager"}, {ID: "sample-1", Name: "Sample", RoleID: "sample"}}
	state := &AssistantProgramState{SchemaVersion: AssistantProgramStateSchemaVersion, Key: key,
		Declaration: &AssistantProgramDeclaration{Roles: []AssistantProgramRoleSpec{
			{ID: "manager", Scope: AssistantRoleScopeHome, Required: true, Primary: true},
			{ID: "sample", Scope: AssistantRoleScopeHome}}},
		HomeBindings: AssistantRoleBindingSet{StateRevision: 1, Bindings: []AssistantRoleBinding{
			{RoleID: "manager", AgentInstanceID: "manager-1", AgentName: "Manager"},
			{RoleID: "sample", AgentInstanceID: "sample-1", AgentName: "Sample"}}}}
	if withWords {
		state.ProjectCardWords = &ProjectCardWords{Noun: "Song", NounPlural: "Songs", RosterTitle: "Songs and assistants"}
	}
	home.SetAssistantProgramState(state)
	if err := store.Save(home); err != nil {
		t.Fatal(err)
	}
	song := NewWorkspace(CreateWorkspaceParams{Name: "Night Drive"})
	song.OwnerUserID = "owner"
	song.AgentInstances = []AgentInstance{{ID: "keeper-1", Name: "Keeper", EntryPoint: true}, {ID: "helper-1", Name: "Helper"}}
	if err := song.SetEntryAgentName("Keeper"); err != nil {
		t.Fatal(err)
	}
	song.SetAssistantProjectLink(&AssistantProjectLink{ID: AssistantProjectLinkID(home.ID, song.ID), SchemaVersion: 1,
		StationWorkspaceID: home.ID, Key: key, StateRevision: 1})
	if err := store.Save(song); err != nil {
		t.Fatal(err)
	}
	plain := NewWorkspace(CreateWorkspaceParams{Name: "Plain"})
	plain.OwnerUserID = "owner"
	plain.AgentInstances = []AgentInstance{{ID: "keeper-p", Name: "Keeper", EntryPoint: true}}
	if err := store.Save(plain); err != nil {
		t.Fatal(err)
	}
	return &LLMTaskHandler{workspaceStore: store}, home, song, plain
}

func TestTaskClosingStep_PresentForTheKeeperAndTheBoundManagerOnly(t *testing.T) {
	h, home, song, plain := closingStepFixture(t, true)
	keeper := h.buildTaskPrompt(context.Background(), Task{ID: "t1", WorkspaceID: song.ID, To: "Keeper", Description: "Bounce stems"})
	if !strings.Contains(keeper, "## Before You Finish") || !strings.Contains(keeper, "`project_card_propose_fields`") ||
		!strings.Contains(keeper, "what the song needs next") || !strings.Contains(keeper, "the song card changed") {
		t.Fatalf("keeper prompt lacks the closing step:\n%s", keeper)
	}
	// The excerpt the demo evidence keeps (go test -v prints it).
	t.Logf("keeper task prompt tail:\n%s", keeper[strings.Index(keeper, "## Before You Finish"):])
	manager := h.buildTaskPrompt(context.Background(), Task{ID: "t2", WorkspaceID: home.ID, To: "Manager", Description: "Review the songs", Timeout: 60e9})
	if !strings.Contains(manager, "`home_library_propose_fields`") || !strings.Contains(manager, "home_library_search") {
		t.Fatalf("Manager prompt lacks the closing step:\n%s", manager)
	}
	// After the output format, before the time limit.
	if strings.Index(manager, "## Before You Finish") > strings.Index(manager, "**Time Limit**") {
		t.Fatal("the closing step must come before the time limit")
	}
	for name, task := range map[string]Task{
		"another song agent":   {ID: "t3", WorkspaceID: song.ID, To: "Helper", Description: "x"},
		"another Home agent":   {ID: "t4", WorkspaceID: home.ID, To: "Sample", Description: "x"},
		"plain workspace":      {ID: "t5", WorkspaceID: plain.ID, To: "Keeper", Description: "x"},
		"no assignee":          {ID: "t6", WorkspaceID: song.ID, Description: "x"},
		"keeper on wrong node": {ID: "t7", WorkspaceID: song.ID, To: "Keeper", AssignedNodeID: "elsewhere", Description: "x"},
	} {
		if prompt := h.buildTaskPrompt(context.Background(), task); strings.Contains(prompt, "Before You Finish") {
			t.Errorf("%s got the closing step", name)
		}
	}
}

func TestTaskClosingStep_NeutralWordsAndNoCompletionClaim(t *testing.T) {
	h, _, song, _ := closingStepFixture(t, false)
	step := h.taskClosingStep(Task{WorkspaceID: song.ID, To: "Keeper"})
	if !strings.Contains(step, "what the project needs next") || strings.Contains(strings.ToLower(step), "song") {
		t.Fatalf("neutral wording: %s", step)
	}
	lower := strings.ToLower(step)
	for _, claim := range []string{" done", "finished", " ready", "completed", "has been updated"} {
		if strings.Contains(lower, claim) {
			t.Fatalf("the closing step claims %q: %s", claim, step)
		}
	}
	if !strings.Contains(step, "Do not call it when nothing changed") || !strings.Contains(step, "suggestion only") {
		t.Fatalf("the closing step must say when not to call and that it is a suggestion: %s", step)
	}
}

func TestTaskClosingStep_BrokenLinkAndResourceGetNone(t *testing.T) {
	h, home, song, _ := closingStepFixture(t, true)
	store := h.workspaceStore.(*InMemoryStore)
	if err := store.Update(song.ID, func(ws *Workspace) error {
		link := ws.GetAssistantProjectLink()
		link.ProjectProvider = &AssistantProjectProviderOwner{WorkspaceKind: AssistantWorkspaceKindResource}
		ws.SetAssistantProjectLink(link)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if step := h.taskClosingStep(Task{WorkspaceID: song.ID, To: "Keeper"}); step != "" {
		t.Fatalf("a resource workspace got the closing step: %s", step)
	}
	if err := store.Update(home.ID, func(ws *Workspace) error {
		ws.OwnerUserID = "someone-else"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Update(song.ID, func(ws *Workspace) error {
		link := ws.GetAssistantProjectLink()
		link.ProjectProvider = nil
		ws.SetAssistantProjectLink(link)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if step := h.taskClosingStep(Task{WorkspaceID: song.ID, To: "Keeper"}); step != "" {
		t.Fatalf("a foreign Home's song got the closing step: %s", step)
	}
}
