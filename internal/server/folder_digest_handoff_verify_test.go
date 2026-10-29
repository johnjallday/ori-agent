package server

import (
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// FR16 over the FileStore: a single song's setup is proved only by the Home,
// its setup-open receipt for the offer, the child that receipt names, the
// child's linked folder, the reciprocal link and the child's page.
func TestFolderHandoffVerifier_ProvesTheOpenedSongFromCanonicalState(t *testing.T) {
	fixture := newFolderLinkerFixture(t)
	fixture.seed(t, "home-1", "Music-Production-Home", "", session.WorkspaceKindGroup)
	fixture.seed(t, "song-1", "Album-5", "", session.WorkspaceKindWorkspace)
	primary := workspace.NewSyncStore(session.NewWorkspaceStoreAdapter(fixture.sessions), fixture.files)
	folder := t.TempDir()
	accepted := time.Now().Add(-time.Minute)
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
					{ID: "portfolio_manager", Label: "Portfolio Manager", Scope: workspace.AssistantRoleScopeHome, Primary: true, Required: true}}}}
		if err := state.RecordSetupOpen(workspace.AssistantPortfolioSetupOpenReceipt{OfferID: "offer-1", EntryID: "entry-5",
			LinkID: linkID, ProjectWorkspaceID: "song-1", RecordedAt: time.Now()}); err != nil {
			return err
		}
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := primary.Update("song-1", func(song *workspace.Workspace) error {
		song.ParentID = "home-1"
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
	verifier := folderHandoffVerifier{builder: &ServerBuilder{workspaceStore: primary, workspaceFileStore: fixture.files, sessionStore: fixture.sessions}}
	verified, err := verifier.VerifiedHandoff(t.Context(), "local", "offer-1", "home-1", folder, accepted)
	if err != nil || verified.WorkspaceID != "song-1" || verified.Route != "/workspaces/album-5" ||
		verified.HomeRoute != "/workspaces/music-production-home/assistant#projectLibraryPanel" ||
		verified.OpenedBy != "Portfolio Manager" || verified.Parent == nil || verified.Parent.Name != "Music-Production-Home" {
		t.Fatalf("verified = %+v %v", verified, err)
	}
	refused := func(name, offerID, homeID, path string, after time.Time) {
		t.Helper()
		if got, err := verifier.VerifiedHandoff(t.Context(), "local", offerID, homeID, path, after); err == nil {
			t.Errorf("%s was accepted: %+v", name, got)
		}
	}
	refused("another offer's open", "offer-2", "home-1", folder, accepted)
	refused("another Home", "offer-1", "song-1", folder, accepted)
	refused("another folder", "offer-1", "home-1", t.TempDir(), accepted)
	refused("an open before the yes", "offer-1", "home-1", folder, time.Now().Add(time.Hour))
	if _, err := verifier.VerifiedHandoff(t.Context(), "someone-else", "offer-1", "home-1", folder, accepted); err == nil {
		t.Error("another owner was accepted")
	}
	// The Home no longer lists the song: no reciprocal link, no proof.
	if err := primary.Update("home-1", func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.LinkedProjectIDs = nil
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	refused("a song the Home no longer lists", "offer-1", "home-1", folder, accepted)
	if err := primary.Update("home-1", func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.LinkedProjectIDs = []string{"song-1"}
		state.Portfolio.SetupOpenReceipts[0].LinkID = strings.Repeat("x", 12)
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	refused("a receipt naming another link", "offer-1", "home-1", folder, accepted)
}
