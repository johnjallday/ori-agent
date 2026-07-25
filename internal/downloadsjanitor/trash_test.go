package downloadsjanitor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeTrash stands in for the system Trash: it moves files into a holding
// directory and can move them back, so tests exercise the real code path
// without touching the developer's actual Trash.
type fakeTrash struct {
	dir         string
	supported   bool
	failMove    bool
	failRestore bool
	// silentNoop reports success without removing anything — the failure mode
	// that would otherwise be reported to the user as "removed".
	silentNoop bool
	moves      int
	restores   int
	items      map[string]string // token -> path inside the holding directory
}

func newFakeTrash(t *testing.T) *fakeTrash {
	t.Helper()
	return &fakeTrash{dir: t.TempDir(), supported: true, items: map[string]string{}}
}

func (f *fakeTrash) Supported() bool { return f.supported }

func (f *fakeTrash) MoveToTrash(path string) (string, error) {
	f.moves++
	if f.failMove {
		return "", errors.New("trash unavailable")
	}
	if f.silentNoop {
		return "token-noop", nil
	}
	token := "token-" + filepath.Base(path)
	held := filepath.Join(f.dir, token)
	if err := os.Rename(path, held); err != nil {
		return "", err
	}
	f.items[token] = held
	return token, nil
}

func (f *fakeTrash) RestoreFromTrash(originalPath, token string) error {
	f.restores++
	if f.failRestore {
		return errors.New("the item is no longer in the Trash")
	}
	held, ok := f.items[token]
	if !ok {
		return errors.New("unknown restore token")
	}
	if _, err := os.Lstat(originalPath); err == nil {
		return errors.New("original path is occupied")
	}
	if err := os.Rename(held, originalPath); err != nil {
		return err
	}
	delete(f.items, token)
	return nil
}

// trashItems builds a Trash-decision plan for the given candidates.
func trashItems(candidates []JanitorCandidate) []PreviewRequestItem {
	items := make([]PreviewRequestItem, 0, len(candidates))
	for _, candidate := range candidates {
		items = append(items, PreviewRequestItem{CandidateID: candidate.ID, Operation: OperationTrash})
	}
	return items
}

func approveAndConfirmItems(t *testing.T, service *Service, items []PreviewRequestItem) ApplyResult {
	t.Helper()
	preview, err := service.PreviewMoves(PreviewRequest{WorkspaceID: "ws-1", UserID: "user-1", Items: items})
	if err != nil {
		t.Fatalf("PreviewMoves: %v", err)
	}
	result, err := service.ConfirmMoves(context.Background(), ConfirmRequest{
		WorkspaceID: "ws-1", UserID: "user-1", BatchID: preview.BatchID,
		Token: preview.Token, Items: items,
	})
	if err != nil {
		t.Fatalf("ConfirmMoves: %v", err)
	}
	return result
}

func TestTrash_MovesTheFileToTheSystemTrashAndRecordsItsRestoreToken(t *testing.T) {
	service, root, candidates := reviewFixture(t, "ad.pdf")
	trash := newFakeTrash(t)
	service.SetTrash(trash)
	service.SetMover(&realMover{})

	result := approveAndConfirmItems(t, service, trashItems(candidates))
	if result.Applied != 1 {
		t.Fatalf("result = %+v", result)
	}
	if trash.moves != 1 {
		t.Fatalf("expected exactly one Trash call, got %d", trash.moves)
	}
	if _, err := os.Stat(filepath.Join(root, "ad.pdf")); !os.IsNotExist(err) {
		t.Fatalf("the file should have left the folder: %v", err)
	}

	actions, err := service.ListActions("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	action := actions[0]
	if action.Operation != OperationTrash || action.Result != ResultApplied {
		t.Fatalf("journal entry = %+v", action)
	}
	// Without the restore token there is no way back from inside Ori.
	if action.TrashRestoreToken == "" {
		t.Fatal("a Trash action must record its restore token")
	}
	if !action.Undoable() {
		t.Fatal("a completed Trash action should offer restore")
	}
	// A Trash action has no destination inside the folder.
	if action.DestinationRelative != "" {
		t.Fatalf("Trash must not record a destination: %q", action.DestinationRelative)
	}
}

// A platform with no recoverable Trash gets no removal at all. There is no
// fallback to deletion: os.Remove is a different, irreversible operation.
func TestTrash_FailsClosedWithoutPlatformSupport(t *testing.T) {
	service, root, candidates := reviewFixture(t, "ad.pdf")
	trash := newFakeTrash(t)
	trash.supported = false
	service.SetTrash(trash)
	service.SetMover(&realMover{})

	result := approveAndConfirmItems(t, service, trashItems(candidates))
	if result.Applied != 0 || result.Failed != 1 {
		t.Fatalf("result = %+v", result)
	}
	if trash.moves != 0 {
		t.Fatal("nothing may be attempted on a system with no Trash")
	}
	if _, err := os.Stat(filepath.Join(root, "ad.pdf")); err != nil {
		t.Fatalf("the file must still be there: %v", err)
	}
	if !strings.Contains(strings.ToLower(result.Outcomes[0].Message), "no recoverable trash") {
		t.Fatalf("the user should be told why: %q", result.Outcomes[0].Message)
	}
}

// A Trash mechanism that claims success without removing the file must not be
// believed — the user would be told a file was removed when it was not.
func TestTrash_DoesNotTrustASilentNoop(t *testing.T) {
	service, root, candidates := reviewFixture(t, "ad.pdf")
	trash := newFakeTrash(t)
	trash.silentNoop = true
	service.SetTrash(trash)
	service.SetMover(&realMover{})

	result := approveAndConfirmItems(t, service, trashItems(candidates))
	if result.Applied != 0 || result.Failed != 1 {
		t.Fatalf("an unverified removal must not count as applied: %+v", result)
	}
	if _, err := os.Stat(filepath.Join(root, "ad.pdf")); err != nil {
		t.Fatalf("the file is still there, which is what should be reported: %v", err)
	}
}

// Trash runs the same pre-mutation checks as a move.
func TestTrash_RefusesAChangedSource(t *testing.T) {
	service, root, candidates := reviewFixture(t, "ad.pdf")
	trash := newFakeTrash(t)
	service.SetTrash(trash)
	service.SetMover(&realMover{})

	preview, err := service.PreviewMoves(PreviewRequest{
		WorkspaceID: "ws-1", UserID: "user-1", Items: trashItems(candidates),
	})
	if err != nil {
		t.Fatalf("PreviewMoves: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "ad.pdf"), []byte("changed after approval"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := service.ConfirmMoves(context.Background(), ConfirmRequest{
		WorkspaceID: "ws-1", UserID: "user-1", BatchID: preview.BatchID,
		Token: preview.Token, Items: trashItems(candidates),
	})
	if err != nil {
		t.Fatalf("ConfirmMoves: %v", err)
	}
	if result.Stale != 1 || trash.moves != 0 {
		t.Fatalf("a changed file must not be trashed: %+v (trash calls %d)", result, trash.moves)
	}
	if _, err := os.Stat(filepath.Join(root, "ad.pdf")); err != nil {
		t.Fatalf("the file must be left alone: %v", err)
	}
}

// An approval given for moves cannot be spent on a removal: the plan hash
// covers the operation, so swapping one in invalidates the whole approval.
func TestTrash_CannotInheritAMoveApproval(t *testing.T) {
	service, root, candidates := reviewFixture(t, "ad.pdf")
	trash := newFakeTrash(t)
	service.SetTrash(trash)
	service.SetMover(&realMover{})

	preview, err := service.PreviewMoves(PreviewRequest{
		WorkspaceID: "ws-1", UserID: "user-1", Items: moveItems(candidates, ""),
	})
	if err != nil {
		t.Fatalf("PreviewMoves: %v", err)
	}
	// Same files, same approval — but now asking for Trash.
	_, err = service.ConfirmMoves(context.Background(), ConfirmRequest{
		WorkspaceID: "ws-1", UserID: "user-1", BatchID: preview.BatchID,
		Token: preview.Token, Items: trashItems(candidates),
	})
	if !errors.Is(err, ErrApprovalInvalid) {
		t.Fatalf("a move approval must not authorize a removal, got %v", err)
	}
	if trash.moves != 0 {
		t.Fatal("nothing may be trashed under a move approval")
	}
	if _, statErr := os.Stat(filepath.Join(root, "ad.pdf")); statErr != nil {
		t.Fatalf("the file must be untouched: %v", statErr)
	}
}

// A mixed batch applies each item as what it was approved as.
func TestTrash_MixedBatchAppliesEachItemAsApproved(t *testing.T) {
	service, root, candidates := reviewFixture(t, "keep.pdf", "junk.pdf")
	trash := newFakeTrash(t)
	service.SetTrash(trash)
	service.SetMover(&realMover{})

	var items []PreviewRequestItem
	for _, candidate := range candidates {
		if candidate.Name == "junk.pdf" {
			items = append(items, PreviewRequestItem{CandidateID: candidate.ID, Operation: OperationTrash})
			continue
		}
		items = append(items, PreviewRequestItem{CandidateID: candidate.ID, Operation: OperationMove, Category: "documents"})
	}

	preview, err := service.PreviewMoves(PreviewRequest{WorkspaceID: "ws-1", UserID: "user-1", Items: items})
	if err != nil {
		t.Fatalf("PreviewMoves: %v", err)
	}
	// The preview states each count separately so the confirmation can too.
	if preview.MoveCount != 1 || preview.TrashCount != 1 {
		t.Fatalf("preview counts = %d moves / %d trash", preview.MoveCount, preview.TrashCount)
	}

	result, err := service.ConfirmMoves(context.Background(), ConfirmRequest{
		WorkspaceID: "ws-1", UserID: "user-1", BatchID: preview.BatchID,
		Token: preview.Token, Items: items,
	})
	if err != nil {
		t.Fatalf("ConfirmMoves: %v", err)
	}
	if result.Applied != 2 {
		t.Fatalf("result = %+v", result)
	}
	if _, err := os.Stat(filepath.Join(root, "Filed", "Documents", "keep.pdf")); err != nil {
		t.Fatalf("the moved file should be filed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "junk.pdf")); !os.IsNotExist(err) {
		t.Fatalf("the trashed file should be gone: %v", err)
	}
	if trash.moves != 1 {
		t.Fatalf("exactly one file should have been trashed, got %d", trash.moves)
	}
}

// ------------------------------------------------------------------- undo

func TestUndo_PutsAMovedFileBack(t *testing.T) {
	service, root, candidates := reviewFixture(t, "report.pdf")
	service.SetMover(&realMover{})
	service.SetTrash(newFakeTrash(t))

	result := approveAndConfirm(t, service, candidates, "")
	actionID := result.Outcomes[0].ActionID

	undo, err := service.Undo(context.Background(), "ws-1", actionID, "user-1")
	if err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if undo.Result != "undone" {
		t.Fatalf("undo = %+v", undo)
	}
	if _, err := os.Stat(filepath.Join(root, "report.pdf")); err != nil {
		t.Fatalf("the file should be back in the folder: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "Filed", "Documents", "report.pdf")); !os.IsNotExist(err) {
		t.Fatalf("the filed copy should be gone: %v", err)
	}

	// The journal records the reversal rather than erasing the original action.
	actions, _ := service.ListActions("ws-1")
	if actions[0].Undo != UndoDone || actions[0].Result != ResultApplied {
		t.Fatalf("the journal must keep the action and record the undo: %+v", actions[0])
	}
}

// Undoing into an occupied name would destroy whatever now lives there.
func TestUndo_RefusesToOverwriteAnOccupiedOriginalName(t *testing.T) {
	service, root, candidates := reviewFixture(t, "report.pdf")
	service.SetMover(&realMover{})

	result := approveAndConfirm(t, service, candidates, "")
	actionID := result.Outcomes[0].ActionID

	// Something else takes the original name while the file is filed away.
	if err := os.WriteFile(filepath.Join(root, "report.pdf"), []byte("a different file entirely"), 0o600); err != nil {
		t.Fatal(err)
	}

	undo, err := service.Undo(context.Background(), "ws-1", actionID, "user-1")
	if err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if undo.Result != "failed" {
		t.Fatalf("undo should refuse: %+v", undo)
	}
	if !strings.Contains(strings.ToLower(undo.Message), "already using") {
		t.Fatalf("the user should be told why: %q", undo.Message)
	}
	// Both files survive: the occupier and the filed copy.
	data, err := os.ReadFile(filepath.Join(root, "report.pdf"))
	if err != nil || string(data) != "a different file entirely" {
		t.Fatalf("the occupying file must survive untouched: %q %v", string(data), err)
	}
	if _, err := os.Stat(filepath.Join(root, "Filed", "Documents", "report.pdf")); err != nil {
		t.Fatalf("the filed copy must stay where it is: %v", err)
	}
}

// A filed file changed since Ori moved it is not moved back: the user may have
// edited it in place, and undo is not a licence to relocate arbitrary files.
func TestUndo_RefusesWhenTheFiledCopyChanged(t *testing.T) {
	service, root, candidates := reviewFixture(t, "report.pdf")
	service.SetMover(&realMover{})
	result := approveAndConfirm(t, service, candidates, "")

	filed := filepath.Join(root, "Filed", "Documents", "report.pdf")
	if err := os.WriteFile(filed, []byte("edited in place after filing"), 0o600); err != nil {
		t.Fatal(err)
	}

	undo, err := service.Undo(context.Background(), "ws-1", result.Outcomes[0].ActionID, "user-1")
	if err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if undo.Result != "failed" || !strings.Contains(strings.ToLower(undo.Message), "changed") {
		t.Fatalf("a changed filed copy must stop the undo: %+v", undo)
	}
	if _, err := os.Stat(filed); err != nil {
		t.Fatalf("the filed copy must be left alone: %v", err)
	}
}

func TestUndo_RestoresATrashedFile(t *testing.T) {
	service, root, candidates := reviewFixture(t, "ad.pdf")
	trash := newFakeTrash(t)
	service.SetTrash(trash)
	service.SetMover(&realMover{})

	result := approveAndConfirmItems(t, service, trashItems(candidates))
	undo, err := service.Undo(context.Background(), "ws-1", result.Outcomes[0].ActionID, "user-1")
	if err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if undo.Result != "undone" {
		t.Fatalf("undo = %+v", undo)
	}
	if trash.restores != 1 {
		t.Fatalf("expected one restore, got %d", trash.restores)
	}
	if _, err := os.Stat(filepath.Join(root, "ad.pdf")); err != nil {
		t.Fatalf("the file should be back: %v", err)
	}
}

// An emptied Trash is a normal outcome, not a crash: the journal survives and
// the user is told what happened.
func TestUndo_EmptiedTrashIsExplainedAndTheJournalSurvives(t *testing.T) {
	service, _, candidates := reviewFixture(t, "ad.pdf")
	trash := newFakeTrash(t)
	service.SetTrash(trash)
	service.SetMover(&realMover{})

	result := approveAndConfirmItems(t, service, trashItems(candidates))
	trash.failRestore = true

	undo, err := service.Undo(context.Background(), "ws-1", result.Outcomes[0].ActionID, "user-1")
	if err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if undo.Result != "failed" {
		t.Fatalf("undo = %+v", undo)
	}
	if !strings.Contains(strings.ToLower(undo.Message), "trash") {
		t.Fatalf("the message should mention the Trash: %q", undo.Message)
	}
	actions, _ := service.ListActions("ws-1")
	if len(actions) != 1 || actions[0].Result != ResultApplied {
		t.Fatalf("the journal entry must survive a failed restore: %+v", actions)
	}
	if actions[0].Undo != UndoFailed || actions[0].UndoError == "" {
		t.Fatalf("the failed attempt must be recorded: %+v", actions[0])
	}
}

// A Trash action with no restore token cannot be restored from inside Ori, and
// says so — the file may still be recoverable from the system Trash by hand.
func TestUndo_MissingRestoreTokenIsExplained(t *testing.T) {
	service, _, candidates := reviewFixture(t, "ad.pdf")
	trash := newFakeTrash(t)
	service.SetTrash(trash)
	service.SetMover(&realMover{})

	result := approveAndConfirmItems(t, service, trashItems(candidates))
	actionID := result.Outcomes[0].ActionID
	if _, err := service.store.UpdateScanState("ws-1", func(state *ScanState) error {
		for i := range state.Actions {
			if state.Actions[i].ID == actionID {
				state.Actions[i].TrashRestoreToken = ""
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	undo, err := service.Undo(context.Background(), "ws-1", actionID, "user-1")
	if err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if undo.Result != "failed" || !strings.Contains(strings.ToLower(undo.Message), "system trash") {
		t.Fatalf("the user should be pointed at their system Trash: %+v", undo)
	}
}

// Undo is single-use: a second attempt is refused rather than performed again.
func TestUndo_IsSingleUse(t *testing.T) {
	service, _, candidates := reviewFixture(t, "report.pdf")
	mover := &realMover{}
	service.SetMover(mover)

	result := approveAndConfirm(t, service, candidates, "")
	actionID := result.Outcomes[0].ActionID

	if _, err := service.Undo(context.Background(), "ws-1", actionID, "user-1"); err != nil {
		t.Fatalf("Undo: %v", err)
	}
	callsAfterFirst := mover.calls

	_, err := service.Undo(context.Background(), "ws-1", actionID, "user-1")
	if !errors.Is(err, ErrUndoUnavailable) {
		t.Fatalf("a second undo must be refused, got %v", err)
	}
	if mover.calls != callsAfterFirst {
		t.Fatal("a repeated undo must not move anything again")
	}
}

func TestUndo_RejectsUnknownActionsAndOtherWorkspaces(t *testing.T) {
	service, _, candidates := reviewFixture(t, "report.pdf")
	service.SetMover(&realMover{})
	result := approveAndConfirm(t, service, candidates, "")

	if _, err := service.Undo(context.Background(), "ws-1", "ghost", "user-1"); !errors.Is(err, ErrUndoUnavailable) {
		t.Fatalf("expected ErrUndoUnavailable, got %v", err)
	}
	// ws-2 has no Janitor setup at all, so the undo cannot even resolve a root.
	if _, err := service.Undo(context.Background(), "ws-2", result.Outcomes[0].ActionID, "user-1"); err == nil {
		t.Fatal("another workspace must not undo this action")
	}
}

// A file the user just recovered must not be immediately re-proposed by the
// next scan — that would undo their undo.
func TestUndo_RestoredFileIsNotImmediatelyReproposed(t *testing.T) {
	service, root, candidates := reviewFixture(t, "report.pdf")
	service.SetMover(&realMover{})

	result := approveAndConfirm(t, service, candidates, "")
	if _, err := service.Undo(context.Background(), "ws-1", result.Outcomes[0].ActionID, "user-1"); err != nil {
		t.Fatalf("Undo: %v", err)
	}

	_, created, err := service.ScanNow("ws-1", ScanSourceDaily)
	if err != nil {
		t.Fatalf("ScanNow: %v", err)
	}
	if created {
		t.Fatal("a file the user just put back must not be proposed again straight away")
	}

	// It comes back once the user explicitly resets skipped items.
	if err := service.ResetSkipped("ws-1", ""); err != nil {
		t.Fatalf("ResetSkipped: %v", err)
	}
	if _, created, err := service.ScanNow("ws-1", ScanSourceDaily); err != nil || !created {
		t.Fatalf("after a reset it should be proposable again: created=%v err=%v", created, err)
	}
	if _, err := os.Stat(filepath.Join(root, "report.pdf")); err != nil {
		t.Fatalf("the file should still be in the folder: %v", err)
	}
}

// Undoing a failed or stale action is meaningless and must be refused.
func TestUndo_OnlyCompletedActionsAreReversible(t *testing.T) {
	service, _, candidates := reviewFixture(t, "report.pdf")
	service.SetMover(&failingMover{})

	result := approveAndConfirm(t, service, candidates, "")
	if result.Failed != 1 {
		t.Fatalf("expected the move to fail: %+v", result)
	}
	if _, err := service.Undo(context.Background(), "ws-1", result.Outcomes[0].ActionID, "user-1"); !errors.Is(err, ErrUndoUnavailable) {
		t.Fatalf("a failed action must not be undoable, got %v", err)
	}
}
