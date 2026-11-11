package server

import (
	"context"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// A folder fed from a Home's own page is recorded under that Home's reviewed
// provider; any other page, another owner's Home or a trashed one is an
// ordinary feed (confirm-and-visible-installs FR8).
func TestFolderAgentHomes_ResolvesOnlyTheOwnersLiveHomes(t *testing.T) {
	fixture := newFolderLinkerFixture(t)
	fixture.seed(t, "home-1", "Music-Production-Home", "", session.WorkspaceKindGroup)
	fixture.seed(t, "plain-1", "Notes", "", session.WorkspaceKindWorkspace)
	store := workspace.NewSyncStore(session.NewWorkspaceStoreAdapter(fixture.sessions), fixture.files)
	var provider reviewedintegration.HomeProvider
	for _, p := range reviewedintegration.HomeProviders() {
		provider = p
		break
	}
	if provider.Key == "" {
		t.Skip("no reviewed Home provider")
	}
	if err := store.Update("home-1", func(home *workspace.Workspace) error {
		home.Kind = "group"
		home.SetAssistantProgramState(&workspace.AssistantProgramState{SchemaVersion: workspace.AssistantProgramStateSchemaVersion,
			PluginAvailable: true,
			Key:             workspace.AssistantProgramKey{OwnerUserID: home.OwnerUserID, PluginID: provider.PluginID, ProgramID: provider.ProgramID},
			HomeProvider: &workspace.AssistantProgramHomeOwner{PluginID: provider.PluginID, PluginVersion: "0.5.0", ProgramID: provider.ProgramID,
				HomeSchemaVersion: 1, HomeVersion: 1, DeclarationDigest: strings.Repeat("a", 64), PluginGeneration: 1,
				ComponentFingerprint: strings.Repeat("b", 64)}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	home, err := store.Get("home-1")
	if err != nil {
		t.Fatal(err)
	}
	homes := folderAgentHomes{builder: &ServerBuilder{workspaceStore: store}}
	ctx := context.Background()
	if got := homes.HomeProviderOf(ctx, home.OwnerUserID, "home-1"); got != provider.Key {
		t.Fatalf("the owner's Home = %q, want %q", got, provider.Key)
	}
	if got := homes.HomeProviderOf(ctx, "someone-else", "home-1"); got != "" {
		t.Fatalf("another owner's Home = %q", got)
	}
	if got := homes.HomeProviderOf(ctx, home.OwnerUserID, "plain-1"); got != "" {
		t.Fatalf("a plain workspace = %q", got)
	}
	if got := homes.HomeProviderOf(ctx, home.OwnerUserID, "missing"); got != "" {
		t.Fatalf("an unknown id = %q", got)
	}
	if err := store.Update("home-1", func(ws *workspace.Workspace) error {
		ws.Status = workspace.StatusTrashed
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := homes.HomeProviderOf(ctx, home.OwnerUserID, "home-1"); got != "" {
		t.Fatalf("a trashed Home = %q", got)
	}
}
