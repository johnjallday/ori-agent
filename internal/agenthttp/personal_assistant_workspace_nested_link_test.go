package agenthttp

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/assistantcontext"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// A song inside a folder workspace inside its Home is still exact membership:
// the assistant sees the program link, and loses it when the folder leaves.
func TestAssistantWorkspaceResolver_NestedSongKeepsItsProgramLink(t *testing.T) {
	r, store, home, project, _ := workspaceResolverFixture(t)
	albums := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Albums"})
	albums.FolderSlug, albums.Kind, albums.ParentID, albums.OwnerUserID = "albums", "group", home.ID, "local"
	if err := store.Save(albums); err != nil {
		t.Fatal(err)
	}
	key := workspace.AssistantProgramKey{OwnerUserID: "local", PluginID: "fixture-provider", ProgramID: "fixture-program"}
	if err := store.Update(home.ID, func(ws *workspace.Workspace) error {
		ws.SetAssistantProgramState(&workspace.AssistantProgramState{
			SchemaVersion: workspace.AssistantProgramStateSchemaVersion, Key: key,
			Declaration:      &workspace.AssistantProgramDeclaration{SchemaVersion: workspace.AssistantProgramSchemaVersion, ID: key.ProgramID},
			LinkedProjectIDs: []string{project.ID},
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Update(project.ID, func(ws *workspace.Workspace) error {
		ws.ParentID = albums.ID
		ws.SetAssistantProjectLink(&workspace.AssistantProjectLink{
			ID: workspace.AssistantProjectLinkID(home.ID, project.ID), SchemaVersion: workspace.AssistantProjectLinkSchemaVersion,
			StationWorkspaceID: home.ID, Key: key, DeclarationVersion: workspace.AssistantProgramSchemaVersion, StateRevision: 1,
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	refs := &HomeAssistantRouteContext{WorkspaceID: project.ID}
	nested := r.Resolve(context.Background(), "local", "Hello", refs)
	if nested.Overview.ProgramLink == nil || nested.Overview.Sources["program_link"].Status != assistantcontext.Available {
		t.Fatal("a nested song lost its program link", nested)
	}
	r.Source = alteredWorkspaceSnapshot{InMemoryStore: store, id: albums.ID, alter: func(ws *workspace.Workspace) { ws.ParentID = "" }}
	left := r.Resolve(context.Background(), "local", "Hello", refs)
	if left.Overview.ProgramLink != nil || left.Overview.Sources["program_link"].Status != assistantcontext.Unavailable {
		t.Fatal("a song whose folder left the Home still claimed exact membership", left)
	}
}
