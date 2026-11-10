package server

import (
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/foldersetup"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// seedNestedHandoffSong puts song-1 inside the albums-1 folder group, which
// sits inside home-1, and links the song to the Home with a setup-open receipt.
// The provider ids match the reviewed Home provider the verifier requires.
func seedNestedHandoffSong(t *testing.T, fixture *folderLinkerFixture, folder string) *workspace.SyncStore {
	t.Helper()
	fixture.seed(t, "home-1", "Music-Production-Home", "", session.WorkspaceKindGroup)
	fixture.seed(t, "albums-1", "Albums", "", session.WorkspaceKindGroup)
	fixture.seed(t, "song-1", "Album-5", "", session.WorkspaceKindWorkspace)
	primary := workspace.NewSyncStore(session.NewWorkspaceStoreAdapter(fixture.sessions), fixture.files)
	key := workspace.AssistantProgramKey{OwnerUserID: "local", PluginID: "music-project-management", ProgramID: "music-producer-assistant"}
	linkID := workspace.AssistantProjectLinkID("home-1", "song-1")
	if err := primary.Update("home-1", func(home *workspace.Workspace) error {
		home.Kind = "group"
		state := &workspace.AssistantProgramState{SchemaVersion: workspace.AssistantProgramStateSchemaVersion, PluginAvailable: true,
			Key: key, LinkedProjectIDs: []string{"song-1"},
			HomeProvider: &workspace.AssistantProgramHomeOwner{PluginID: "music-project-management", PluginVersion: "0.4.0",
				ProgramID: "music-producer-assistant", HomeSchemaVersion: 1, HomeVersion: 1, DeclarationDigest: strings.Repeat("a", 64),
				PluginGeneration: 1, ComponentFingerprint: strings.Repeat("b", 64)},
			Declaration: &workspace.AssistantProgramDeclaration{SchemaVersion: workspace.AssistantProgramSchemaVersion,
				ID: "music-producer-assistant", StationName: "Music-Production-Home", Roles: []workspace.AssistantProgramRoleSpec{
					{ID: "portfolio_manager", Label: "Portfolio Manager", Scope: workspace.AssistantRoleScopeHome, Primary: true, Required: true}}},
			HomeBindings: workspace.AssistantRoleBindingSet{Bindings: []workspace.AssistantRoleBinding{
				{RoleID: "portfolio_manager", AgentName: "Portfolio Manager"}}}}
		if err := state.RecordSetupOpen(workspace.AssistantPortfolioSetupOpenReceipt{OfferID: "offer-1", EntryID: "entry-5",
			LinkID: linkID, ProjectWorkspaceID: "song-1", RecordedAt: time.Now()}); err != nil {
			return err
		}
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := primary.Update("albums-1", func(albums *workspace.Workspace) error {
		albums.Kind, albums.ParentID = "group", "home-1"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := primary.Update("song-1", func(song *workspace.Workspace) error {
		song.ParentID = "albums-1"
		song.SetAssistantProjectLink(&workspace.AssistantProjectLink{ID: linkID, SchemaVersion: 1,
			StationWorkspaceID: "home-1", Key: key, StateRevision: 1})
		if song.SharedData == nil {
			song.SharedData = map[string]any{}
		}
		if err := song.AddDirectoryReference(workspace.DirectoryReference{ID: "song-folder", Name: "Album-5", Path: folder}); err != nil {
			return err
		}
		return workspace.SetProjectEntryLocator(song.SharedData, workspace.ProjectEntryLocator{
			SchemaVersion: workspace.ProjectEntryLocatorSchemaVersion, Kind: workspace.ProjectEntryDirectoryReference,
			DirectoryReferenceID: "song-folder", RelativePath: "Album-5.rpp"})
	}); err != nil {
		t.Fatal(err)
	}
	return primary
}

func TestFolderHandoffVerifier_AcceptsASongInsideAFolderInsideTheHome(t *testing.T) {
	fixture := newFolderLinkerFixture(t)
	folder := t.TempDir()
	primary := seedNestedHandoffSong(t, fixture, folder)
	if song, err := fixture.files.Get("song-1"); err != nil || song.ParentID != "albums-1" {
		t.Fatalf("fixture must nest the song in the folder group: %+v %v", song, err)
	}
	verifier := folderHandoffVerifier{builder: &ServerBuilder{workspaceStore: primary, workspaceFileStore: fixture.files, sessionStore: fixture.sessions}}
	verified, err := verifier.VerifiedHandoff(t.Context(), "local", "offer-1", "home-1", folder, time.Now().Add(-time.Minute))
	if err != nil || verified.WorkspaceID != "song-1" || verified.Parent == nil || verified.Parent.Name != "Music-Production-Home" {
		t.Fatalf("nested song hand-off = %+v %v", verified, err)
	}
}

func TestHandoffReceipts_ListASongInsideAFolderInsideTheHome(t *testing.T) {
	fixture := newFolderLinkerFixture(t)
	primary := seedNestedHandoffSong(t, fixture, t.TempDir())
	host := &folderSetupHost{builder: &ServerBuilder{workspaceStore: primary, workspaceFileStore: fixture.files,
		sessionStore: fixture.sessions, sessionHandler: fixture.handler}}
	opener := &handoffOpener{task: &personalassistant.FolderReceiptRow{Kind: "task", Name: "Summarize Album-5", Detail: "ready to start"}}
	receipts := handoffReceipts{host: host, song: "Album-5", label: "Song", opener: opener}
	rows, err := receipts.Receipt(t.Context(), "home-1", foldersetup.PortfolioReceiptFacts{
		Opened: &foldersetup.OpenOutcome{EntryID: "entry-5", WorkspaceID: "song-1", Created: true}})
	if err != nil {
		t.Fatalf("a nested song's receipt was refused: %v", err)
	}
	for _, row := range rows {
		if row.Kind == "workspace" && row.Name == "Album-5" {
			return
		}
	}
	t.Fatalf("receipt has no row for the nested song: %+v", rows)
}
