package sessionhttp

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/userprofile"
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

func TestBackfillPreservesDesignationWhenProfileHasNoHQ(t *testing.T) {
	handler, fileStore := newDesignationTestHandler(t)
	ctx := context.Background()
	markedID := createTestWorkspace(t, handler, "Shared HQ")

	if err := handler.SetWorkspaceDesignation(ctx, markedID, "personal_hq"); err != nil {
		t.Fatalf("seed designation: %v", err)
	}

	// An empty record set is the signature of a database that has never seen
	// this shared workspace tree (a fresh worktree or dev data dir), not of a
	// user without an HQ. Erasing here destroyed the portable marker.
	if err := handler.BackfillWorkspaceDesignations(ctx, map[string]bool{}); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if got := designationOf(t, fileStore, markedID); got != "personal_hq" {
		t.Fatalf("backfill must not erase the folder marker when the profile has no HQ, got %q", got)
	}
}

func TestBackfillAdoptsFolderDesignationWhenProfileHasNoHQ(t *testing.T) {
	handler, fileStore := newDesignationTestHandler(t)
	ctx := context.Background()
	markedID := createTestWorkspace(t, handler, "Shared HQ")
	plainID := createTestWorkspace(t, handler, "Plain")

	if err := handler.SetWorkspaceDesignation(ctx, markedID, "personal_hq"); err != nil {
		t.Fatalf("seed designation: %v", err)
	}

	designator := &recordingDesignator{}
	handler.SetPersonalHQDesignator(designator)

	if err := handler.BackfillWorkspaceDesignations(ctx, map[string]bool{}); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	if len(designator.calls) != 1 {
		t.Fatalf("expected the marked workspace to be adopted once, got %#v", designator.calls)
	}
	if got := designator.calls[0]; got.userID != userprofile.LocalUserID || got.workspaceID != markedID {
		t.Fatalf("expected Designate(%q, %q), got %#v", userprofile.LocalUserID, markedID, got)
	}
	if got := designationOf(t, fileStore, plainID); got != "" {
		t.Fatalf("adoption must not touch an unmarked workspace, got %q", got)
	}
}

func TestBackfillSkipsAdoptionWhenSeveralWorkspacesClaimHQ(t *testing.T) {
	handler, fileStore := newDesignationTestHandler(t)
	ctx := context.Background()
	firstID := createTestWorkspace(t, handler, "Claim One")
	secondID := createTestWorkspace(t, handler, "Claim Two")

	for _, id := range []string{firstID, secondID} {
		if err := handler.SetWorkspaceDesignation(ctx, id, "personal_hq"); err != nil {
			t.Fatalf("seed designation for %s: %v", id, err)
		}
	}

	designator := &recordingDesignator{}
	handler.SetPersonalHQDesignator(designator)

	if err := handler.BackfillWorkspaceDesignations(ctx, map[string]bool{}); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	// Ambiguous trees are reported, never resolved by guessing — and nothing
	// is erased while the ambiguity stands.
	if len(designator.calls) != 0 {
		t.Fatalf("expected no adoption from an ambiguous tree, got %#v", designator.calls)
	}
	for _, id := range []string{firstID, secondID} {
		if got := designationOf(t, fileStore, id); got != "personal_hq" {
			t.Fatalf("expected %s to keep its marker, got %q", id, got)
		}
	}
}

func TestBackfillStillClearsStaleDesignationWhenProfileHasAnHQ(t *testing.T) {
	handler, fileStore := newDesignationTestHandler(t)
	ctx := context.Background()
	currentID := createTestWorkspace(t, handler, "Current HQ")
	staleID := createTestWorkspace(t, handler, "Stale HQ")

	for _, id := range []string{currentID, staleID} {
		if err := handler.SetWorkspaceDesignation(ctx, id, "personal_hq"); err != nil {
			t.Fatalf("seed designation for %s: %v", id, err)
		}
	}

	// With a real record present, the record still wins in both directions.
	if err := handler.BackfillWorkspaceDesignations(ctx, map[string]bool{currentID: true}); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if got := designationOf(t, fileStore, currentID); got != "personal_hq" {
		t.Fatalf("expected the backed HQ to keep its marker, got %q", got)
	}
	if got := designationOf(t, fileStore, staleID); got != "" {
		t.Fatalf("expected the unbacked marker to be cleared, got %q", got)
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
