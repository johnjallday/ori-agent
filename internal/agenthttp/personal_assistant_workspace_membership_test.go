package agenthttp

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/assistantcontext"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestAssistantWorkspaceResolver_ExactProgramLinkIsNotPhysicalGrouping(t *testing.T) {
	r, store, home, project, _ := workspaceResolverFixture(t)
	refs := &HomeAssistantRouteContext{WorkspaceID: project.ID}
	physical := r.Resolve(context.Background(), "local", "Hello", refs)
	if physical.Overview.Parent == nil || physical.Overview.ProgramLink != nil {
		t.Fatal("grouping became specialized membership")
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
		ws.SetAssistantProjectLink(&workspace.AssistantProjectLink{
			ID: workspace.AssistantProjectLinkID(home.ID, project.ID), SchemaVersion: workspace.AssistantProjectLinkSchemaVersion,
			StationWorkspaceID: home.ID, Key: key, DeclarationVersion: workspace.AssistantProgramSchemaVersion, StateRevision: 1,
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	exact := r.Resolve(context.Background(), "local", "Hello", refs)
	if exact.Overview.ProgramLink == nil || exact.Overview.ProgramLink.Home.Kind != "home" || exact.Overview.Sources["program_link"].Status != assistantcontext.Available {
		t.Fatal("reciprocal canonical membership missing", exact)
	}
	for _, change := range []func(*workspace.Workspace){
		func(ws *workspace.Workspace) { ws.ParentID = "" },
		func(ws *workspace.Workspace) {
			link := ws.GetAssistantProjectLink()
			link.Key.PluginID = "impostor"
			ws.SetAssistantProjectLink(link)
		},
	} {
		// The canonical store correctly guards live topology mutations. Supply
		// a corrupt historical/read snapshot instead of bypassing that guard.
		r.Source = alteredWorkspaceSnapshot{InMemoryStore: store, id: project.ID, alter: change}
		got := r.Resolve(context.Background(), "local", "Hello", refs)
		if got.Overview.ProgramLink != nil || got.Overview.Sources["program_link"].Status != assistantcontext.Unavailable {
			t.Fatal("broken membership still claimed exact", got)
		}
	}
}

// Get returns the store's independent snapshot, so this negative fixture never
// writes or alters the live program topology.
type alteredWorkspaceSnapshot struct {
	*workspace.InMemoryStore
	id    string
	alter func(*workspace.Workspace)
}

func (s alteredWorkspaceSnapshot) Get(id string) (*workspace.Workspace, error) {
	ws, err := s.InMemoryStore.Get(id)
	if err == nil && ws != nil && id == s.id {
		s.alter(ws)
	}
	return ws, err
}
