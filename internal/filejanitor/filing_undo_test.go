package filejanitor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// filedFixture files a small scan in one press and returns the service, the filing
// and the folder, ready for Undo all.
func filedFixture(t *testing.T, names ...string) (*Service, JanitorBatch, FilingResult, string, *realMover) {
	t.Helper()
	s, batch, _, root := planFixture(t, names...)
	mover := &realMover{}
	s.SetMover(mover)
	_, filed, err := pressFor(t, s, batch.ID, keyOne, nil)
	if err != nil || filed.FilingID == "" || filed.Filed == 0 {
		t.Fatalf("setup: the press did not file: %+v %v", filed, err)
	}
	return s, batch, filed, root, mover
}

func outcomeNamed(t *testing.T, result FilingUndoResult, name string) FilingUndoOutcome {
	t.Helper()
	for _, outcome := range result.Outcomes {
		if outcome.Name == name {
			return outcome
		}
	}
	t.Fatalf("no outcome for %s in %+v", name, result.Outcomes)
	return FilingUndoOutcome{}
}

// R47: every file of the filing goes back to its original name in the folder.
func TestUndoFiling_PutsEveryFileBackUnderItsOriginalName(t *testing.T) {
	s, _, filed, root, _ := filedFixture(t, "a.png", "b.png", "c.pdf")
	result, err := s.UndoFiling(t.Context(), "local", "ws-1", filed.FilingID)
	if err != nil {
		t.Fatal(err)
	}
	if result.FilingID != filed.FilingID || result.Replayed || result.Interrupted || result.Back != 3 || result.NotBack != 0 || len(result.Outcomes) != 3 {
		t.Fatalf("result: %+v", result)
	}
	for _, name := range []string{"a.png", "b.png", "c.pdf"} {
		if !exists(t, root, name) {
			t.Errorf("%s should be back in the folder", name)
		}
		if got := outcomeNamed(t, result, name); got.Result != UndoOutcomeUndone || got.Message != "" {
			t.Errorf("%s: %+v", name, got)
		}
	}
	if exists(t, root, "Filed", "Images", "a.png") || exists(t, root, "Filed", "Documents", "c.pdf") {
		t.Error("nothing should be left in Filed")
	}
	state, err := s.store.LoadScanState("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range filingActions(state, filed.FilingID) {
		if action.Undo != UndoDone {
			t.Errorf("%s was not journaled as undone: %s", action.SourceName, action.Undo)
		}
	}
	// The receipt knows too, and Undo all is no longer on offer.
	receipt, err := s.ReadFiling(t.Context(), "local", "ws-1", filed.FilingID, 0)
	if err != nil || receipt.UndoAvailable || receipt.Undo == nil || receipt.Undo.Back != 3 || receipt.Undo.State != FilingDone {
		t.Fatalf("receipt after Undo all: %+v %v", receipt, err)
	}
}

// R48: something else now uses the original name, so that file stays where it is,
// is reported with where it is, and the rest come back.
func TestUndoFiling_AnOriginalNameThatIsTakenLeavesThatFileAndReportsIt(t *testing.T) {
	s, _, filed, root, _ := filedFixture(t, "a.png", "b.png", "c.pdf")
	if err := os.WriteFile(filepath.Join(root, "a.png"), []byte("a new file, not the one that was filed"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := s.UndoFiling(t.Context(), "local", "ws-1", filed.FilingID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Back != 2 || result.NotBack != 1 {
		t.Fatalf("two back, one not: %+v", result)
	}
	taken := outcomeNamed(t, result, "a.png")
	if taken.Result != UndoOutcomeFailed || !strings.Contains(taken.Message, "original name") || taken.Location != LocationFiled || taken.Destination != "Filed/Images/a.png" {
		t.Fatalf("the file that could not go back says why and where it is: %+v", taken)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "a.png")); string(got) != "a new file, not the one that was filed" { // #nosec G304 -- test temp dir
		t.Fatalf("the file that took the name was overwritten: %q", got)
	}
	if !exists(t, root, "Filed", "Images", "a.png") {
		t.Fatal("the filed file must be left where it is")
	}
}

// R48: a filed file that changed since is left alone, not put back over a stranger.
func TestUndoFiling_AFiledFileThatChangedIsLeftAndReported(t *testing.T) {
	s, _, filed, root, _ := filedFixture(t, "a.png", "b.png")
	f, err := os.OpenFile(filepath.Join(root, "Filed", "Images", "b.png"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(" edited after filing"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	result, err := s.UndoFiling(t.Context(), "local", "ws-1", filed.FilingID)
	if err != nil {
		t.Fatal(err)
	}
	changed := outcomeNamed(t, result, "b.png")
	if result.Back != 1 || changed.Result != UndoOutcomeFailed || !strings.Contains(changed.Message, "changed") || changed.Location != LocationFiled || exists(t, root, "b.png") {
		t.Fatalf("a changed file is left and reported: %+v %+v", result, changed)
	}
}

// A file already sent to the Trash comes back through the platform Trash; one that
// was emptied since is reported, never silently skipped.
func TestUndoFiling_ATrashThatWasEmptiedIsReported(t *testing.T) {
	s, batch, candidates, root := planFixture(t, "a.png", "b.png")
	trash := newFakeTrash(t)
	s.SetTrash(trash)
	s.SetMover(&realMover{})
	editDraft(t, s, batch.ID, "mark_trash", func(r *ReviewDraftEdit) {
		r.CandidateIDs = []string{candidateNamed(t, candidates, "b.png").ID}
	})
	_, filed, err := pressFor(t, s, batch.ID, keyOne, nil)
	if err != nil || filed.Filed != 1 || filed.Trashed != 1 {
		t.Fatalf("setup: %+v %v", filed, err)
	}
	trash.failRestore = true // the Trash was emptied
	result, err := s.UndoFiling(t.Context(), "local", "ws-1", filed.FilingID)
	if err != nil {
		t.Fatal(err)
	}
	gone := outcomeNamed(t, result, "b.png")
	if result.Back != 1 || result.NotBack != 1 || gone.Result != UndoOutcomeFailed || gone.Message == "" || gone.Operation != OperationTrash {
		t.Fatalf("the emptied Trash is reported: %+v %+v", result, gone)
	}
	if !exists(t, root, "a.png") || exists(t, root, "b.png") {
		t.Fatal("a.png is back; b.png is not")
	}
}

// R50: a second press, or a second window, undoes nothing twice and reports the
// first result.
func TestUndoFiling_ASecondPressReportsTheFirstResult(t *testing.T) {
	s, _, filed, _, mover := filedFixture(t, "a.png", "b.png")
	first, err := s.UndoFiling(t.Context(), "local", "ws-1", filed.FilingID)
	if err != nil || first.Back != 2 {
		t.Fatalf("first: %+v %v", first, err)
	}
	movesAfterFirst := mover.calls
	second, err := s.UndoFiling(t.Context(), "local", "ws-1", filed.FilingID)
	if err != nil || !second.Replayed || second.Back != 2 || second.NotBack != 0 || len(second.Outcomes) != len(first.Outcomes) {
		t.Fatalf("second: %+v %v", second, err)
	}
	if mover.calls != movesAfterFirst {
		t.Fatalf("a second press moved files: %d -> %d", movesAfterFirst, mover.calls)
	}
}

// R51: refused once the folder is not the folder that was filed, with nothing moved.
func TestUndoFiling_IsRefusedAfterTheFolderWasRelinked(t *testing.T) {
	s, _, filed, root, mover := filedFixture(t, "a.png")
	if _, err := s.store.UpdateSettings("ws-1", func(settings *JanitorSettings) error {
		settings.RootID = "replacement-root"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before := mover.calls
	_, err := s.UndoFiling(t.Context(), "local", "ws-1", filed.FilingID)
	if err == nil {
		t.Fatal("Undo all must be refused after a relink")
	}
	if mover.calls != before || !exists(t, root, "Filed", "Images", "a.png") || exists(t, root, "a.png") {
		t.Fatal("a refused Undo all moved a file")
	}
	receipt, rerr := s.ReadFiling(t.Context(), "local", "ws-1", filed.FilingID, 0)
	if rerr != nil || receipt.UndoAvailable {
		t.Fatalf("the receipt must not offer Undo all after a relink: %+v %v", receipt, rerr)
	}
}

func TestUndoFiling_RefusesWhatItCannotDo(t *testing.T) {
	s, batch, filed, _, _ := filedFixture(t, "a.png")
	for name, call := range map[string]func() error{
		"another owner": func() error { _, err := s.UndoFiling(t.Context(), "someone-else", "ws-1", filed.FilingID); return err },
		"unknown filing": func() error {
			_, err := s.UndoFiling(t.Context(), "local", "ws-1", "filing-does-not-exist")
			return err
		},
		"not a filing id": func() error { _, err := s.UndoFiling(t.Context(), "local", "ws-1", "../etc"); return err },
		"a scan id, not a filing": func() error {
			_, err := s.UndoFiling(t.Context(), "local", "ws-1", batch.ID)
			return err
		},
	} {
		if err := call(); err == nil || !strings.Contains(err.Error(), ErrUndoUnavailable.Error()) {
			t.Errorf("%s: got %v", name, err)
		}
	}
	// Nothing was claimed by any of those: the real Undo all still works.
	if result, err := s.UndoFiling(t.Context(), "local", "ws-1", filed.FilingID); err != nil || result.Back != 1 {
		t.Fatalf("after refusals: %+v %v", result, err)
	}
}

// A filing in which nothing moved has nothing to put back.
func TestUndoFiling_NothingToPutBackIsRefused(t *testing.T) {
	s, batch, _, root := planFixture(t, "a.png")
	s.SetMover(&failingMover{})
	_, filed, err := pressFor(t, s, batch.ID, keyOne, nil)
	if err != nil || filed.Filed != 0 || filed.Failed != 1 {
		t.Fatalf("setup: %+v %v", filed, err)
	}
	if _, err := s.UndoFiling(t.Context(), "local", "ws-1", filed.FilingID); err == nil || !strings.Contains(err.Error(), "nothing") {
		t.Fatalf("got %v", err)
	}
	if !exists(t, root, "a.png") {
		t.Fatal("the file never moved and must still be there")
	}
}

// A file already put back by today's per-file Undo is reported as such, not as
// something this run did.
func TestUndoFiling_AFilePutBackEarlierIsSaidSo(t *testing.T) {
	s, _, filed, root, _ := filedFixture(t, "a.png", "b.png")
	state, err := s.store.LoadScanState("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	var early FileAction
	for _, action := range filingActions(state, filed.FilingID) {
		if action.SourceName == "a.png" {
			early = action
		}
	}
	if _, err := s.Undo(t.Context(), "ws-1", early.ID, "local"); err != nil {
		t.Fatal(err)
	}
	result, err := s.UndoFiling(t.Context(), "local", "ws-1", filed.FilingID)
	if err != nil {
		t.Fatal(err)
	}
	if outcomeNamed(t, result, "a.png").Result != UndoOutcomeAlreadyUndone || outcomeNamed(t, result, "b.png").Result != UndoOutcomeUndone || result.Back != 2 {
		t.Fatalf("outcomes: %+v", result.Outcomes)
	}
	if !exists(t, root, "a.png") || !exists(t, root, "b.png") {
		t.Fatal("both should be back")
	}
}

// R52: the files Undo all put back are kept out of the next scan (today's per-file
// rule). "Scan again to review them" first makes Ori forget that, for these files
// only, then scans; this is the forget step: it moves nothing, is idempotent, and is
// refused after a relink.
func TestForgetRestored_ClearsOnlyThisFilingsRestoredFiles(t *testing.T) {
	s, _, filed, root, mover := filedFixture(t, "a.png", "b.png")
	if _, err := s.UndoFiling(t.Context(), "local", "ws-1", filed.FilingID); err != nil {
		t.Fatal(err)
	}
	// Something unrelated Ori was told to leave alone stays left alone.
	unrelated := Fingerprint{Name: "kept.pdf", Size: 7}
	if _, err := s.store.UpdateScanState("ws-1", func(state *ScanState) error {
		MarkSkipped(state, unrelated, s.clock())
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	skipped := func() int {
		state, err := s.store.LoadScanState("ws-1")
		if err != nil {
			t.Fatal(err)
		}
		return len(state.Skipped)
	}
	before := skipped()
	moves := mover.calls
	forgotten, err := s.ForgetRestored(t.Context(), "local", "ws-1", filed.FilingID)
	if err != nil || forgotten != 2 {
		t.Fatalf("two files forgotten: %d %v", forgotten, err)
	}
	if after := skipped(); after >= before || after < 1 {
		t.Fatalf("restored files must leave the skip list and the unrelated entry stay: %d -> %d", before, after)
	}
	state, _ := s.store.LoadScanState("ws-1")
	if !state.IsSkipped(unrelated) {
		t.Fatal("an unrelated skip was cleared")
	}
	if mover.calls != moves || !exists(t, root, "a.png") || !exists(t, root, "b.png") {
		t.Fatal("forgetting moves nothing")
	}
	if again, err := s.ForgetRestored(t.Context(), "local", "ws-1", filed.FilingID); err != nil || again != 2 {
		t.Fatalf("idempotent: %d %v", again, err)
	}
	if _, err := s.ForgetRestored(t.Context(), "someone-else", "ws-1", filed.FilingID); err == nil {
		t.Fatal("another owner must be refused")
	}
}

func TestForgetRestored_IsRefusedAfterARelinkAndBeforeAnUndo(t *testing.T) {
	s, _, filed, _, _ := filedFixture(t, "a.png")
	// Before Undo all there is nothing to forget: it must not clear anything.
	if n, err := s.ForgetRestored(t.Context(), "local", "ws-1", filed.FilingID); err != nil || n != 0 {
		t.Fatalf("before Undo all: %d %v", n, err)
	}
	if _, err := s.UndoFiling(t.Context(), "local", "ws-1", filed.FilingID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.UpdateSettings("ws-1", func(settings *JanitorSettings) error {
		settings.RootID = "replacement-root"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ForgetRestored(t.Context(), "local", "ws-1", filed.FilingID); err == nil {
		t.Fatal("must be refused after a relink")
	}
}
