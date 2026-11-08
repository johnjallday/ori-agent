package server

import (
	"reflect"
	"testing"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func folderAgentPinWorkspace() *workspace.Workspace {
	all := append(append([]string(nil), folderAgentListingTools...), folderAgentContentTools...)
	return &workspace.Workspace{
		ID:             "ws-folder",
		AgentInstances: []workspace.AgentInstance{{ID: "inst-1", Name: "Songs Manager", NodeID: "n-1", InstanceNumber: 1}},
		MCPBindings: []workspace.MCPBinding{
			{ID: "mb-anchor", ServerName: "filesystem", Alias: workspace.AnchorFolderMCPAlias, Enabled: true, AllowedTools: all},
		},
	}
}

func pinnedTools(t *testing.T, ws *workspace.Workspace) []string {
	t.Helper()
	_, recipe, ok, err := ws.ResolveAssignedToolbox("inst-1")
	if err != nil || !ok || len(recipe.MCPBindings) != 1 {
		t.Fatalf("assigned toolbox: ok=%v err=%v recipe=%+v", ok, err, recipe)
	}
	return recipe.MCPBindings[0].AllowedTools
}

func TestPinFolderAgentToolbox_ListingUntilTheOwnerChooses(t *testing.T) {
	ws := folderAgentPinWorkspace()
	if err := pinFolderAgentToolbox(ws, "Songs Manager", false); err != nil {
		t.Fatal(err)
	}
	got := pinnedTools(t, ws)
	want := append([]string(nil), folderAgentListingTools...)
	if !sameSet(got, want) {
		t.Fatalf("first look tools = %v, want %v", got, want)
	}
	// Pinning again changes nothing.
	if err := pinFolderAgentToolbox(ws, "Songs Manager", false); err != nil {
		t.Fatal(err)
	}
	if def, _ := ws.GetToolbox("folder-agent-inst-1"); def.Version != 1 {
		t.Fatalf("version = %d", def.Version)
	}

	// Set it up / Look further widens, as a new version the instance uses.
	if err := pinFolderAgentToolbox(ws, "Songs Manager", true); err != nil {
		t.Fatal(err)
	}
	got = pinnedTools(t, ws)
	want = append(want, folderAgentContentTools...)
	if !sameSet(got, want) {
		t.Fatalf("after Set it up = %v, want %v", got, want)
	}
	if def, _ := ws.GetToolbox("folder-agent-inst-1"); def.Version != 2 {
		t.Fatalf("widened version = %d", def.Version)
	}
	// A widened pin is never narrowed again by a later look.
	if err := pinFolderAgentToolbox(ws, "Songs Manager", false); err != nil {
		t.Fatal(err)
	}
	if !sameSet(pinnedTools(t, ws), want) {
		t.Fatal("a later look narrowed the pin")
	}
}

func TestPinFolderAgentToolbox_NeedsTheAgentAndTheAnchorBinding(t *testing.T) {
	ws := folderAgentPinWorkspace()
	if err := pinFolderAgentToolbox(ws, "Someone Else", false); err == nil {
		t.Fatal("an agent outside the workspace must not be pinned")
	}
	ws.MCPBindings = nil
	if err := pinFolderAgentToolbox(ws, "Songs Manager", false); err != errFolderAgentNoAnchorBinding {
		t.Fatalf("err = %v", err)
	}
}

func TestPluginMatches_UsesNameDescriptionKeywordsAndTags(t *testing.T) {
	entry := plugin.MarketplaceEntry{Name: "release-planner", Description: "Plan song releases", Keywords: []string{"Audio"}, Tags: []string{"music"}}
	for _, word := range []string{"release", "SONG", "audio", "Music"} {
		if !pluginMatches(entry, word) {
			t.Errorf("%q should match", word)
		}
	}
	for _, word := range []string{"", " ", "manuscript"} {
		if pluginMatches(entry, word) {
			t.Errorf("%q should not match", word)
		}
	}
}

func TestPinFolderAgentToolbox_ReleaseRemovesOnlyTheFirstLookPin(t *testing.T) {
	ws := folderAgentPinWorkspace()
	if err := pinFolderAgentToolbox(ws, "Songs Manager", true); err != nil {
		t.Fatal(err)
	}
	store := &singleWorkspaceStore{ws: ws}
	if err := (folderAgentPins{store: store}).Release(ws.ID, "Songs Manager"); err != nil {
		t.Fatal(err)
	}
	if _, ok := ws.GetToolboxAssignment("inst-1"); ok {
		t.Fatal("the first-look pin is still assigned")
	}
	if _, ok := ws.GetToolbox("folder-agent-inst-1"); !ok {
		t.Fatal("the Toolbox's history must stay")
	}
	// Nothing to release is not an error.
	if err := (folderAgentPins{store: store}).Release(ws.ID, "Songs Manager"); err != nil {
		t.Fatal(err)
	}
}

// singleWorkspaceStore is just enough workspace.Store for Update.
type singleWorkspaceStore struct {
	workspace.Store
	ws *workspace.Workspace
}

func (s *singleWorkspaceStore) Update(id string, fn func(*workspace.Workspace) error) error {
	return fn(s.ws)
}

func sameSet(a, b []string) bool {
	set := func(list []string) map[string]bool {
		out := map[string]bool{}
		for _, v := range list {
			out[v] = true
		}
		return out
	}
	return len(a) == len(b) && reflect.DeepEqual(set(a), set(b))
}
