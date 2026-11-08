package sessionhttp

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

func TestCreateFolderAgentWorkspace_AnchorsTheFolderWithItsAgent(t *testing.T) {
	h, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	anchor := filepath.Join(canonicalAnchorTestDir(t), "Ori Music Demo")
	if err := os.MkdirAll(anchor, 0o750); err != nil {
		t.Fatal(err)
	}
	created, err := h.CreateFolderAgentWorkspace(t.Context(), FolderAgentWorkspaceRequest{
		Name: "Ori Music Demo", AnchorPath: anchor, Mission: "Find out what's here and set it up", AgentName: "Ori Music Demo Manager",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !created.AgentCreated || created.Name != "Ori Music Demo" || created.Nested != nil {
		t.Fatalf("created = %+v", created)
	}
	ws, err := h.workspaceStore.Get(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !ws.IsAnchored() || ws.EntryAgentName() != "Ori Music Demo Manager" || ws.Mission != "Find out what's here and set it up" {
		t.Fatalf("workspace: anchored=%v entry=%q mission=%q", ws.IsAnchored(), ws.EntryAgentName(), ws.Mission)
	}
	agent, ok := h.agentStore.GetAgent("Ori Music Demo Manager")
	if !ok || agent == nil {
		t.Fatal("the folder's agent was not saved")
	}

	// A second folder of the same name binds the saved agent instead of
	// creating another (hire once, bind many); a nested folder is allowed and
	// names its parent's workspace.
	nested := filepath.Join(anchor, "Albums")
	if err := os.MkdirAll(nested, 0o750); err != nil {
		t.Fatal(err)
	}
	second, err := h.CreateFolderAgentWorkspace(t.Context(), FolderAgentWorkspaceRequest{
		Name: "Albums", AnchorPath: nested, AgentName: "Ori Music Demo Manager",
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.AgentCreated || second.Nested == nil || second.Nested.WorkspaceID != created.ID {
		t.Fatalf("second = %+v", second)
	}
}

func TestCreateFolderAgentWorkspace_MemberUnderAGroupHasNoAgent(t *testing.T) {
	h, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	root := filepath.Join(canonicalAnchorTestDir(t), "Three Drafts")
	member := filepath.Join(root, "Drafts", "Night Train")
	if err := os.MkdirAll(member, 0o750); err != nil {
		t.Fatal(err)
	}
	group, err := h.CreateFolderAgentWorkspace(t.Context(), FolderAgentWorkspaceRequest{Name: "Three Drafts", AnchorPath: root, AgentName: "Three Drafts Manager"})
	if err != nil {
		t.Fatal(err)
	}
	// The group is a promoted folder workspace; members need a group parent.
	if err := h.workspaceStore.Update(group.ID, func(ws *agentworkspace.Workspace) error { ws.Kind = "group"; return nil }); err != nil {
		t.Fatal(err)
	}
	row, err := h.store.GetWorkspace(t.Context(), group.ID)
	if err != nil {
		t.Fatal(err)
	}
	row.Kind = "group"
	if err := h.store.UpdateWorkspace(t.Context(), row); err != nil {
		t.Fatal(err)
	}
	created, err := h.CreateFolderAgentWorkspace(t.Context(), FolderAgentWorkspaceRequest{Name: "Night Train", AnchorPath: member, ParentID: group.ID})
	if err != nil {
		t.Fatal(err)
	}
	ws, err := h.workspaceStore.Get(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ws.ParentID != group.ID || !ws.IsAnchored() || len(ws.GetAgentInstances()) != 0 || created.AgentCreated {
		t.Fatalf("member parent=%q anchored=%v agents=%d", ws.ParentID, ws.IsAnchored(), len(ws.GetAgentInstances()))
	}
	if created.Nested == nil || created.Nested.WorkspaceID != group.ID {
		t.Fatalf("nested hint = %+v", created.Nested)
	}
}

func TestCreateFolderAgentWorkspace_RefusalLeavesNothingBehind(t *testing.T) {
	h, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	anchor := canonicalAnchorTestDir(t)
	if err := os.WriteFile(filepath.Join(anchor, "workspace.json"), []byte(`{"id":"x","name":"Other"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := h.CreateFolderAgentWorkspace(t.Context(), FolderAgentWorkspaceRequest{Name: "Claimed", AnchorPath: anchor, AgentName: "Claimed Manager"})
	var refusal *FolderAgentRefusal
	if !errors.As(err, &refusal) || refusal.Code != "anchor_contains_workspace" || refusal.Message == "" {
		t.Fatalf("err = %v", err)
	}
	assertAgentAbsent(t, h, "Claimed Manager")
	rows, err := h.store.ListWorkspaces(t.Context())
	if err != nil || len(rows) != 0 {
		t.Fatalf("left records: %+v / %v", rows, err)
	}

	// Already owned: the refusal names the owner so the card can offer Open it.
	free := canonicalAnchorTestDir(t)
	first, err := h.CreateFolderAgentWorkspace(t.Context(), FolderAgentWorkspaceRequest{Name: "Songs", AnchorPath: free, AgentName: "Songs Manager"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.CreateFolderAgentWorkspace(t.Context(), FolderAgentWorkspaceRequest{Name: "Songs 2", AnchorPath: free, AgentName: "Songs Manager"})
	if !errors.As(err, &refusal) || refusal.Code != "anchor_already_owned" || refusal.Duplicate == nil || refusal.Duplicate.WorkspaceID != first.ID {
		t.Fatalf("duplicate = %v (%+v)", err, refusal)
	}
	if _, ok := h.agentStore.GetAgent("Songs Manager"); !ok {
		t.Fatal("a refusal must not remove a saved agent it did not create")
	}
}
