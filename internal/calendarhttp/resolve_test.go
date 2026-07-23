package calendarhttp

import (
	"context"
	"maps"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/session"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

func newActiveWorkspaceTestHandler(folderWorkspaces map[string]*agentworkspace.Workspace, listed []session.Workspace) *Handler {
	store := newFakeFolderStore()
	maps.Copy(store.workspaces, folderWorkspaces)
	return NewHandler(store, &fakeWorkspaceLister{workspaces: listed}, nil, nil, nil)
}

func TestActiveWorkspace_FindsSoleOwnedCalendarOpsWorkspace(t *testing.T) {
	ws := newCalendarOpsWorkspace("ws-cal")
	ws.OwnerUserID = "local"
	h := newActiveWorkspaceTestHandler(
		map[string]*agentworkspace.Workspace{"ws-cal": ws},
		[]session.Workspace{{ID: "ws-cal", Name: "Calendar Ops", OwnerUserID: "local", Status: session.WorkspaceStatusActive}},
	)

	got, ok := h.ActiveWorkspace(context.Background(), "local")
	if !ok || got == nil || got.ID != "ws-cal" {
		t.Fatalf("got %+v ok=%v, want ws-cal", got, ok)
	}
}

func TestActiveWorkspace_NoQualifyingWorkspaceReturnsFalse(t *testing.T) {
	h := newActiveWorkspaceTestHandler(nil, nil)
	if _, ok := h.ActiveWorkspace(context.Background(), "local"); ok {
		t.Fatal("expected ok=false when no workspace exists")
	}
}

func TestActiveWorkspace_IgnoresNonCalendarOpsWorkspace(t *testing.T) {
	other := &agentworkspace.Workspace{ID: "ws-other", Name: "Not Calendar Ops", OwnerUserID: "local"}
	h := newActiveWorkspaceTestHandler(
		map[string]*agentworkspace.Workspace{"ws-other": other},
		[]session.Workspace{{ID: "ws-other", Name: "Not Calendar Ops", OwnerUserID: "local", Status: session.WorkspaceStatusActive}},
	)
	if _, ok := h.ActiveWorkspace(context.Background(), "local"); ok {
		t.Fatal("expected ok=false for a workspace with no calendar-ops template provenance")
	}
}

func TestActiveWorkspace_IgnoresAnotherUsersWorkspace(t *testing.T) {
	ws := newCalendarOpsWorkspace("ws-cal")
	ws.OwnerUserID = "mallory"
	h := newActiveWorkspaceTestHandler(
		map[string]*agentworkspace.Workspace{"ws-cal": ws},
		[]session.Workspace{{ID: "ws-cal", Name: "Calendar Ops", OwnerUserID: "mallory", Status: session.WorkspaceStatusActive}},
	)
	if _, ok := h.ActiveWorkspace(context.Background(), "local"); ok {
		t.Fatal("expected ok=false for another user's Calendar Ops workspace")
	}
}

func TestActiveWorkspace_IgnoresInactiveWorkspace(t *testing.T) {
	ws := newCalendarOpsWorkspace("ws-cal")
	ws.OwnerUserID = "local"
	h := newActiveWorkspaceTestHandler(
		map[string]*agentworkspace.Workspace{"ws-cal": ws},
		[]session.Workspace{{ID: "ws-cal", Name: "Calendar Ops", OwnerUserID: "local", Status: session.WorkspaceStatusTrashed}},
	)
	if _, ok := h.ActiveWorkspace(context.Background(), "local"); ok {
		t.Fatal("expected ok=false for a trashed Calendar Ops workspace")
	}
}

func TestActiveWorkspace_IgnoresGroupWorkspace(t *testing.T) {
	ws := newCalendarOpsWorkspace("ws-cal")
	ws.OwnerUserID = "local"
	h := newActiveWorkspaceTestHandler(
		map[string]*agentworkspace.Workspace{"ws-cal": ws},
		[]session.Workspace{{ID: "ws-cal", Name: "Calendar Ops", OwnerUserID: "local", Status: session.WorkspaceStatusActive, Kind: session.WorkspaceKindGroup}},
	)
	if _, ok := h.ActiveWorkspace(context.Background(), "local"); ok {
		t.Fatal("expected ok=false for a group workspace")
	}
}

func TestActiveWorkspace_DuplicatesMostRecentlyUpdatedWins(t *testing.T) {
	older := newCalendarOpsWorkspace("ws-old")
	older.OwnerUserID = "local"
	older.UpdatedAt = time.Now().Add(-24 * time.Hour)

	newer := newCalendarOpsWorkspace("ws-new")
	newer.OwnerUserID = "local"
	newer.UpdatedAt = time.Now()

	h := newActiveWorkspaceTestHandler(
		map[string]*agentworkspace.Workspace{"ws-old": older, "ws-new": newer},
		[]session.Workspace{
			{ID: "ws-old", Name: "Calendar Ops (old)", OwnerUserID: "local", Status: session.WorkspaceStatusActive},
			{ID: "ws-new", Name: "Calendar Ops (new)", OwnerUserID: "local", Status: session.WorkspaceStatusActive},
		},
	)

	got, ok := h.ActiveWorkspace(context.Background(), "local")
	if !ok || got == nil || got.ID != "ws-new" {
		t.Fatalf("got %+v ok=%v, want ws-new (most recently updated)", got, ok)
	}
}

func TestActiveWorkspace_BlankOwnerDefaultsToLocalUser(t *testing.T) {
	ws := newCalendarOpsWorkspace("ws-cal")
	h := newActiveWorkspaceTestHandler(
		map[string]*agentworkspace.Workspace{"ws-cal": ws},
		[]session.Workspace{{ID: "ws-cal", Name: "Calendar Ops", Status: session.WorkspaceStatusActive}},
	)
	got, ok := h.ActiveWorkspace(context.Background(), "")
	if !ok || got == nil || got.ID != "ws-cal" {
		t.Fatalf("got %+v ok=%v, want ws-cal under the default local user", got, ok)
	}
}

// --- PreferredCalendarAgent ------------------------------------------------

func TestPreferredCalendarAgent_ReadyWorkspaceReturnsScheduler(t *testing.T) {
	ws, _ := readyCalendarWorkspace("ws-cal", "local")
	h := newGatewayTestHandler(ws, readyStatus, "local")
	h.lister = &fakeWorkspaceLister{workspaces: []session.Workspace{
		{ID: "ws-cal", Name: "Calendar Ops", OwnerUserID: "local", Status: session.WorkspaceStatusActive},
	}}

	name, ok := h.PreferredCalendarAgent(context.Background())
	if !ok || name != "Scheduler" {
		t.Fatalf("got %q ok=%v, want Scheduler", name, ok)
	}
}

func TestPreferredCalendarAgent_NoWorkspaceReturnsNoPreference(t *testing.T) {
	h := newActiveWorkspaceTestHandler(nil, nil)
	if name, ok := h.PreferredCalendarAgent(context.Background()); ok {
		t.Fatalf("got %q ok=%v, want ok=false", name, ok)
	}
}

func TestPreferredCalendarAgent_UnreadyWorkspaceReturnsNoPreference(t *testing.T) {
	// A Calendar Ops workspace exists but never finished setup (no binding).
	ws := newCalendarOpsWorkspace("ws-cal")
	ws.OwnerUserID = "local"
	h := newActiveWorkspaceTestHandler(
		map[string]*agentworkspace.Workspace{"ws-cal": ws},
		[]session.Workspace{{ID: "ws-cal", Name: "Calendar Ops", OwnerUserID: "local", Status: session.WorkspaceStatusActive}},
	)

	if name, ok := h.PreferredCalendarAgent(context.Background()); ok {
		t.Fatalf("got %q ok=%v, want ok=false for an unfinished setup", name, ok)
	}
}
