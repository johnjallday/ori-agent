package downloadsjanitor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// applyOneApprovedMove runs a full scan → decide → preview → apply cycle and
// returns the resulting journal entry.
func applyOneApprovedMove(t *testing.T, service *Service, workspaceID, root, name string) FileAction {
	t.Helper()
	agedFile(t, root, name, 120)

	batch, created, err := service.ScanNow(workspaceID, ScanSourceManual)
	if err != nil || !created {
		t.Fatalf("ScanNow: created=%v err=%v", created, err)
	}
	candidateID := batch.CandidateIDs[0]
	if _, err := service.ApplyDecisions(workspaceID, []DecisionUpdate{
		{CandidateID: candidateID, Decision: DecisionMove},
	}); err != nil {
		t.Fatalf("ApplyDecisions: %v", err)
	}
	preview, err := service.PreviewMoves(PreviewRequest{
		WorkspaceID: workspaceID,
		UserID:      "local",
		Items:       []PreviewRequestItem{{CandidateID: candidateID, Operation: OperationMove}},
	})
	if err != nil {
		t.Fatalf("PreviewMoves: %v", err)
	}
	result, err := service.ConfirmMoves(context.Background(), ConfirmRequest{
		WorkspaceID: workspaceID,
		UserID:      "local",
		BatchID:     batch.ID,
		Token:       preview.Token,
		Items:       []PreviewRequestItem{{CandidateID: candidateID, Operation: OperationMove}},
	})
	if err != nil || result.Applied != 1 {
		t.Fatalf("ConfirmMoves: applied=%d err=%v", result.Applied, err)
	}

	actions, err := service.ListActions(workspaceID)
	if err != nil {
		t.Fatalf("ListActions: %v", err)
	}
	if len(actions) == 0 {
		t.Fatal("no journal entry was written")
	}
	return actions[len(actions)-1]
}

func relinkService(t *testing.T) (*Service, string, string) {
	t.Helper()
	store, _ := newTestStore(t)
	service := NewService(store, newFakeWorkspaceStore("ws-1"))
	service.SetMover(&realMover{})
	service.SetTrash(newFakeTrash(t))

	base := tempDirCanonical(t)
	return service, mkdir(t, filepath.Join(base, "Old")), mkdir(t, filepath.Join(base, "New"))
}

// TestRelink_HistoricalActionsDoNotFollowTheNewFolder is FR-57.
//
// Every path in the journal is relative to the configured folder, which is what
// keeps absolute paths out of it. Without a root annotation, a relink would
// silently reinterpret all of history against the NEW folder — and undo would
// restore a file into a folder it never came from. The root generation id is
// what makes an old action identifiable as old.
func TestRelink_HistoricalActionsDoNotFollowTheNewFolder(t *testing.T) {
	service, oldRoot, newRoot := relinkService(t)

	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: oldRoot}); err != nil {
		t.Fatalf("setup: %v", err)
	}
	before, err := service.store.LoadSettings("ws-1")
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if before.RootID == "" {
		t.Fatal("setup did not issue a folder generation id")
	}

	action := applyOneApprovedMove(t, service, "ws-1", oldRoot, "invoice.pdf")
	if action.RootID != before.RootID {
		t.Fatalf("action root = %q, want the configured %q", action.RootID, before.RootID)
	}

	// Relink to a different folder.
	if _, err := service.Relink(nil, RelinkRequest{WorkspaceID: "ws-1", Path: newRoot}); err != nil {
		t.Fatalf("Relink: %v", err)
	}
	after, err := service.store.LoadSettings("ws-1")
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if after.RootID == before.RootID {
		t.Fatal("relinking to a different folder must issue a new folder generation")
	}

	// The old action is still in history, still attributed to the old folder.
	actions, err := service.ListActions("ws-1")
	if err != nil {
		t.Fatalf("ListActions: %v", err)
	}
	if len(actions) != 1 {
		t.Fatalf("relink changed the journal: %d entries", len(actions))
	}
	if actions[0].RootID != before.RootID {
		t.Fatalf("relink rewrote a historical entry's folder: %q", actions[0].RootID)
	}
	if actions[0].BelongsToRoot(after.RootID) {
		t.Fatal("an action from the old folder claims to belong to the new one")
	}

	// And it cannot be undone into the new folder.
	_, err = service.Undo(context.Background(), "ws-1", actions[0].ID, "local")
	if !errors.Is(err, ErrUndoUnavailable) {
		t.Fatalf("undo across a relink must be refused, got %v", err)
	}

	// The old folder's files and Filed/ tree are untouched by the relink.
	filed := filepath.Join(oldRoot, DefaultFilingRootName)
	if _, statErr := os.Stat(filed); statErr != nil {
		t.Fatalf("relink disturbed the old Filed/ tree: %v", statErr)
	}
	entries, err := os.ReadDir(filepath.Join(filed, "Documents"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("the previously filed file is gone from the old folder: %v", err)
	}
}

// TestRelink_ToTheSameFolderKeepsHistoryUndoable is the complement: re-confirming
// the folder the workspace already manages is not a change, so history stays
// continuous and undo keeps working.
func TestRelink_ToTheSameFolderKeepsHistoryUndoable(t *testing.T) {
	service, root, _ := relinkService(t)

	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root}); err != nil {
		t.Fatalf("setup: %v", err)
	}
	before, _ := service.store.LoadSettings("ws-1")
	action := applyOneApprovedMove(t, service, "ws-1", root, "invoice.pdf")

	// Re-confirm the same folder.
	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root}); err != nil {
		t.Fatalf("re-confirm: %v", err)
	}
	after, _ := service.store.LoadSettings("ws-1")
	if after.RootID != before.RootID {
		t.Fatal("re-confirming the same folder must not issue a new generation")
	}

	if _, err := service.Undo(context.Background(), "ws-1", action.ID, "local"); err != nil {
		t.Fatalf("undo should still work after re-confirming the same folder: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "invoice.pdf")); err != nil {
		t.Fatalf("undo did not restore the file: %v", err)
	}
}

// TestBelongsToRoot_TreatsLegacyEntriesAsCurrent guards the upgrade path:
// introducing this check must not retroactively make existing history
// un-undoable, because entries written before the field existed have no id.
func TestBelongsToRoot_TreatsLegacyEntriesAsCurrent(t *testing.T) {
	legacy := FileAction{ID: "a1"}
	if !legacy.BelongsToRoot("root-1") {
		t.Fatal("an entry predating root ids must be treated as belonging to the current folder")
	}

	current := FileAction{ID: "a2", RootID: "root-1"}
	if !current.BelongsToRoot("") {
		t.Fatal("a workspace with no generation id yet must match its own history")
	}
	if !current.BelongsToRoot("root-1") {
		t.Fatal("a matching generation must be undoable")
	}
	if current.BelongsToRoot("root-2") {
		t.Fatal("a different generation must not match")
	}
}
