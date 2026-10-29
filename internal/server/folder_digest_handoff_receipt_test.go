package server

import (
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/foldersetup"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// FR17 over the FileStore: a single song's receipt is read back from canonical
// state, in order: the Home, its Portfolio Manager, the song listed in the
// Home's library, the song's workspace opened by the manager, the shared
// assistant on it, and its first task.
func TestHandoffReceipts_ReadTheSongsSetupBackInOrder(t *testing.T) {
	fixture := newFolderLinkerFixture(t)
	fixture.seed(t, "home-1", "Music-Production-Home", "", session.WorkspaceKindGroup)
	fixture.seed(t, "song-1", "Album-5", "", session.WorkspaceKindWorkspace)
	primary := workspace.NewSyncStore(session.NewWorkspaceStoreAdapter(fixture.sessions), fixture.files)
	if err := primary.Update("home-1", func(home *workspace.Workspace) error {
		home.Kind = "group"
		home.SetAssistantProgramState(&workspace.AssistantProgramState{SchemaVersion: workspace.AssistantProgramStateSchemaVersion,
			Key: workspace.AssistantProgramKey{OwnerUserID: "local", PluginID: "music-project-management", ProgramID: "music-producer-assistant"},
			Declaration: &workspace.AssistantProgramDeclaration{Roles: []workspace.AssistantProgramRoleSpec{
				{ID: "portfolio_manager", Label: "Music Portfolio Manager", Scope: workspace.AssistantRoleScopeHome, Primary: true, Required: true}}},
			HomeBindings: workspace.AssistantRoleBindingSet{Bindings: []workspace.AssistantRoleBinding{
				{RoleID: "portfolio_manager", AgentName: "Music Portfolio Manager"}}}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := primary.Update("song-1", func(song *workspace.Workspace) error {
		song.ParentID = "home-1"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	host := &folderSetupHost{builder: &ServerBuilder{workspaceStore: primary, workspaceFileStore: fixture.files,
		sessionStore: fixture.sessions, sessionHandler: fixture.handler}}
	opener := &handoffOpener{task: &personalassistant.FolderReceiptRow{Kind: "task", Name: "Summarize Album-5", Detail: "ready to start"}}
	receipts := handoffReceipts{host: host, song: "Album-5", label: "Reaper Song", opener: opener}
	rows, err := receipts.Receipt(t.Context(), "home-1", foldersetup.PortfolioReceiptFacts{HomeCreated: true,
		AddedAgents: []string{"Music Portfolio Manager"}, Opened: &foldersetup.OpenOutcome{EntryID: "entry-5", WorkspaceID: "song-1",
			Created: true, Staffing: "added", AgentName: "REAPER Assistant", FirstTask: true}})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(rows))
	for _, row := range rows {
		got = append(got, row.Kind+": "+row.Name+" — "+row.Detail)
	}
	want := []string{
		"home: Music-Production-Home — created",
		"agent: Music Portfolio Manager — Music Portfolio Manager, added",
		"library: Album-5 listed in the Home's library — names and project files only",
		"workspace: Album-5 — Reaper Song workspace, opened by your Music Portfolio Manager",
		"agent: REAPER Assistant — added, shared with the songs you open in this Home",
		"task: Summarize Album-5 — ready to start",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("receipt =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// Only the song's own assistant is membership of the song's workspace.
	if rows[1].Assigned || !rows[4].Assigned {
		t.Fatalf("assigned = manager %v, song assistant %v", rows[1].Assigned, rows[4].Assigned)
	}
	if rows[3].Route != "/workspaces/album-5" || rows[3].Operation != "create" || !strings.HasSuffix(rows[0].Route, "#projectLibraryPanel") {
		t.Fatalf("routes = %+v", rows)
	}
	// A second song into the same Home: reused Home, manager already there,
	// the shared assistant joined.
	rows, err = receipts.Receipt(t.Context(), "home-1", foldersetup.PortfolioReceiptFacts{Opened: &foldersetup.OpenOutcome{
		EntryID: "entry-5", WorkspaceID: "song-1", Created: true, Staffing: "joined", AgentName: "REAPER Assistant", FirstTask: true}})
	if err != nil || rows[0].Detail != "your Home" || rows[1].Detail != "Music Portfolio Manager, already there" ||
		rows[4].Detail != "joined, shared with the songs you open in this Home" {
		t.Fatalf("second song receipt = %+v %v", rows, err)
	}
	// A song that is not the Home's child is no receipt.
	if _, err := receipts.Receipt(t.Context(), "home-1", foldersetup.PortfolioReceiptFacts{Opened: &foldersetup.OpenOutcome{WorkspaceID: "home-1"}}); err == nil {
		t.Fatal("a receipt named a workspace that is not the Home's song")
	}
}
