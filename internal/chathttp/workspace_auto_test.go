package chathttp

import (
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestEnsureWorkspaceForRoute_CreatesWorkspaceWhenNeeded(t *testing.T) {
	st := newPreflightStore("Ori", &agent.Agent{})
	h := NewHandler(st, nil)
	wsStore := workspace.NewInMemoryStore()
	h.SetWorkspaceStore(wsStore)

	decision := UtilityRouteDecision{
		Mode:   UtilityRouteWorkspace,
		Reason: "prompt indicates workspace-scoped execution",
	}
	ws, created, err := h.ensureWorkspaceForRoute("Ori", "run tests in this repository", decision, normalizedChatRouteContext{})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if ws == nil {
		t.Fatalf("expected workspace to be returned")
	}
	if !created {
		t.Fatalf("expected workspace to be created")
	}
	if !ws.HasAgent("Ori") {
		t.Fatalf("expected created workspace to include agent Ori")
	}

	allIDs, err := wsStore.List()
	if err != nil {
		t.Fatalf("failed to list workspaces: %v", err)
	}
	if len(allIDs) != 1 {
		t.Fatalf("expected exactly one workspace, got %d", len(allIDs))
	}
}

func TestEnsureWorkspaceForRoute_ReusesNewestActiveWorkspaceForAgent(t *testing.T) {
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
	got, created, err := h.ensureWorkspaceForRoute("Ori", "delegate this to specialists", decision, normalizedChatRouteContext{})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got == nil {
		t.Fatalf("expected workspace result")
	}
	if created {
		t.Fatalf("expected existing workspace to be reused")
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

func TestEnsureWorkspaceForRoute_PrefersExplicitRouteWorkspace(t *testing.T) {
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
	got, created, err := h.ensureWorkspaceForRoute("Ori", "review this workspace", decision, normalizedChatRouteContext{
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
	if created {
		t.Fatal("expected existing route workspace to be reused")
	}
	if got.ID != currentWS.ID {
		t.Fatalf("expected explicit route workspace %s, got %s", currentWS.ID, got.ID)
	}
}

func TestApplyWorkspaceRouteContext_PromotesNonWorkspaceSurface(t *testing.T) {
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Alpha"})
	updated := applyWorkspaceRouteContext(normalizedChatRouteContext{
		Surface:  "dashboard",
		PagePath: "/dashboard",
		Origin:   "ask_ori",
	}, ws)

	if updated.WorkspaceID != ws.ID {
		t.Fatalf("expected workspace_id %q, got %q", ws.ID, updated.WorkspaceID)
	}
	if updated.Surface != "workspace_chat" {
		t.Fatalf("expected workspace_chat surface, got %q", updated.Surface)
	}
	if updated.PagePath != "/workspaces/"+ws.ID {
		t.Fatalf("expected workspace page path, got %q", updated.PagePath)
	}
}
