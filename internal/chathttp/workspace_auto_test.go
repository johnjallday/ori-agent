package chathttp

import (
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestWorkspaceForRoute_NeverCreatesWorkspace(t *testing.T) {
	st := newPreflightStore("Ori", &agent.Agent{})
	h := NewHandler(st, nil)
	wsStore := workspace.NewInMemoryStore()
	h.SetWorkspaceStore(wsStore)

	decision := UtilityRouteDecision{
		Mode:   UtilityRouteWorkspace,
		Reason: "prompt indicates workspace-scoped execution",
	}
	ws, err := h.workspaceForRoute("Ori", decision, normalizedChatRouteContext{})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if ws != nil {
		t.Fatalf("expected no workspace for an agent that has none, got %q", ws.Name)
	}

	allIDs, err := wsStore.List()
	if err != nil {
		t.Fatalf("failed to list workspaces: %v", err)
	}
	if len(allIDs) != 0 {
		t.Fatalf("chat must not create workspaces, got %d", len(allIDs))
	}
}

func TestWorkspaceForRoute_ReusesNewestActiveWorkspaceForAgent(t *testing.T) {
	st := newPreflightStore("Ori", &agent.Agent{})
	h := NewHandler(st, nil)
	wsStore := workspace.NewInMemoryStore()
	h.SetWorkspaceStore(wsStore)

	oldWS := workspace.NewWorkspace(workspace.CreateWorkspaceParams{
		Name:   "old",
		Agents: []string{"Ori"},
	})
	oldWS.UpdatedAt = time.Now().Add(-10 * time.Minute)
	if err := wsStore.Save(oldWS); err != nil {
		t.Fatalf("failed to save old workspace: %v", err)
	}

	newWS := workspace.NewWorkspace(workspace.CreateWorkspaceParams{
		Name:   "new",
		Agents: []string{"Ori"},
	})
	newWS.UpdatedAt = time.Now()
	if err := wsStore.Save(newWS); err != nil {
		t.Fatalf("failed to save new workspace: %v", err)
	}

	decision := UtilityRouteDecision{
		Mode:   UtilityRouteSpecial,
		Reason: "prompt indicates specialist handoff",
	}
	got, err := h.workspaceForRoute("Ori", decision, normalizedChatRouteContext{})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got == nil {
		t.Fatalf("expected workspace result")
	}
	if got.ID != newWS.ID {
		t.Fatalf("expected newest workspace %s, got %s", newWS.ID, got.ID)
	}

	allIDs, err := wsStore.List()
	if err != nil {
		t.Fatalf("failed to list workspaces: %v", err)
	}
	if len(allIDs) != 2 {
		t.Fatalf("expected no new workspace creation, got %d workspaces", len(allIDs))
	}
}

func TestWorkspaceForRoute_PrefersExplicitRouteWorkspace(t *testing.T) {
	st := newPreflightStore("Ori", &agent.Agent{})
	h := NewHandler(st, nil)
	wsStore := workspace.NewInMemoryStore()
	h.SetWorkspaceStore(wsStore)

	currentWS := workspace.NewWorkspace(workspace.CreateWorkspaceParams{
		Name:   "current",
		Agents: []string{"Ori"},
	})
	currentWS.UpdatedAt = time.Now().Add(-10 * time.Minute)
	if err := wsStore.Save(currentWS); err != nil {
		t.Fatalf("failed to save current workspace: %v", err)
	}

	newerWS := workspace.NewWorkspace(workspace.CreateWorkspaceParams{
		Name:   "newer",
		Agents: []string{"Ori"},
	})
	newerWS.UpdatedAt = time.Now()
	if err := wsStore.Save(newerWS); err != nil {
		t.Fatalf("failed to save newer workspace: %v", err)
	}

	decision := UtilityRouteDecision{
		Mode:   UtilityRouteWorkspace,
		Reason: "prompt indicates workspace-scoped execution",
	}
	got, err := h.workspaceForRoute("Ori", decision, normalizedChatRouteContext{
		WorkspaceID: currentWS.ID,
		Surface:     "workspace_detail",
		PagePath:    "/workspaces/" + currentWS.ID,
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got == nil {
		t.Fatal("expected workspace result")
	}
	if got.ID != currentWS.ID {
		t.Fatalf("expected explicit route workspace %s, got %s", currentWS.ID, got.ID)
	}
}

func TestApplyWorkspaceRouteContext_PromotesNonWorkspaceSurface(t *testing.T) {
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Alpha"})
	ws.FolderSlug = "alpha"
	updated := applyWorkspaceRouteContext(normalizedChatRouteContext{
		Surface:  "dashboard",
		PagePath: "/dashboard",
		Origin:   "ask_ori",
	}, ws)

	if updated.WorkspaceID != ws.ID {
		t.Fatalf("expected workspace_id %q, got %q", ws.ID, updated.WorkspaceID)
	}
	if updated.WorkspaceSlug != ws.FolderSlug {
		t.Fatalf("expected workspace_slug %q, got %q", ws.FolderSlug, updated.WorkspaceSlug)
	}
	if updated.Surface != "workspace_chat" {
		t.Fatalf("expected workspace_chat surface, got %q", updated.Surface)
	}
	if updated.PagePath != "/workspaces/"+ws.FolderSlug {
		t.Fatalf("expected workspace page path, got %q", updated.PagePath)
	}
}
