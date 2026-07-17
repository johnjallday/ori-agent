package sessionhttp

import (
	"context"
	"testing"

	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// designationOf reads the canonical Designation field straight from the folder
// store, bypassing SQLite (which has no column for it).
func designationOf(t *testing.T, fileStore *agentworkspace.FileStore, id string) string {
	t.Helper()
	ws, err := fileStore.Get(id)
	if err != nil {
		t.Fatalf("load workspace %q: %v", id, err)
	}
	return ws.Designation
}

func newDesignationTestHandler(t *testing.T) (*Handler, *agentworkspace.FileStore) {
	t.Helper()
	handler, cleanup := createTestHandler(t)
	t.Cleanup(cleanup)

	fileStore, err := agentworkspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	t.Cleanup(func() { _ = fileStore.Close() })
	handler.SetWorkspaceStore(fileStore)
	return handler, fileStore
}

func TestSetWorkspaceDesignationWritesAndClears(t *testing.T) {
	handler, fileStore := newDesignationTestHandler(t)
	ctx := context.Background()
	id := createTestWorkspace(t, handler, "Plain")

	if err := handler.SetWorkspaceDesignation(ctx, id, "personal_hq"); err != nil {
		t.Fatalf("set designation: %v", err)
	}
	if got := designationOf(t, fileStore, id); got != "personal_hq" {
		t.Fatalf("after set: designation = %q, want %q", got, "personal_hq")
	}

	if err := handler.SetWorkspaceDesignation(ctx, id, ""); err != nil {
		t.Fatalf("clear designation: %v", err)
	}
	if got := designationOf(t, fileStore, id); got != "" {
		t.Fatalf("after clear: designation = %q, want empty", got)
	}
}

func TestSetWorkspaceDesignationRefusesGroup(t *testing.T) {
	handler, fileStore := newDesignationTestHandler(t)
	ctx := context.Background()
	groupID := createTestGroup(t, handler, "Group")

	if err := handler.SetWorkspaceDesignation(ctx, groupID, "personal_hq"); err != nil {
		t.Fatalf("set designation on group: %v", err)
	}
	if got := designationOf(t, fileStore, groupID); got != "" {
		t.Fatalf("group must never carry a designation, got %q", got)
	}
}

func TestBackfillHealsMissingDesignation(t *testing.T) {
	handler, fileStore := newDesignationTestHandler(t)
	ctx := context.Background()
	hqID := createTestWorkspace(t, handler, "HQ")
	plainID := createTestWorkspace(t, handler, "Plain")

	if err := handler.BackfillWorkspaceDesignations(ctx, map[string]bool{hqID: true}); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if got := designationOf(t, fileStore, hqID); got != "personal_hq" {
		t.Fatalf("backfill should set designated HQ, got %q", got)
	}
	if got := designationOf(t, fileStore, plainID); got != "" {
		t.Fatalf("backfill must not touch undesignated workspace, got %q", got)
	}
}

func TestBackfillClearsUnbackedDesignation(t *testing.T) {
	handler, fileStore := newDesignationTestHandler(t)
	ctx := context.Background()
	staleID := createTestWorkspace(t, handler, "Stale HQ")

	// Seed a stale designation with no backing record.
	if err := handler.SetWorkspaceDesignation(ctx, staleID, "personal_hq"); err != nil {
		t.Fatalf("seed stale designation: %v", err)
	}

	if err := handler.BackfillWorkspaceDesignations(ctx, map[string]bool{}); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if got := designationOf(t, fileStore, staleID); got != "" {
		t.Fatalf("backfill should clear unbacked designation, got %q", got)
	}
}

func TestBackfillClearsDesignationOnGroup(t *testing.T) {
	handler, fileStore := newDesignationTestHandler(t)
	ctx := context.Background()
	groupID := createTestGroup(t, handler, "Group")

	// Force a designation onto the group's folder record directly (SetWorkspace-
	// Designation would refuse), simulating drift, then ensure the backfill
	// clears it even though the group ID is in the designated set.
	ws, err := fileStore.Get(groupID)
	if err != nil {
		t.Fatalf("load group: %v", err)
	}
	ws.Designation = "personal_hq"
	if err := fileStore.Save(ws); err != nil {
		t.Fatalf("seed group designation: %v", err)
	}

	if err := handler.BackfillWorkspaceDesignations(ctx, map[string]bool{groupID: true}); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if got := designationOf(t, fileStore, groupID); got != "" {
		t.Fatalf("backfill must clear designation from a group, got %q", got)
	}
}
