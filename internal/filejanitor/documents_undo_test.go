//go:build darwin || linux

package filejanitor

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// filedPair files two documents into Taxes and returns what the fixture needs to
// put them back.
type filedPair struct {
	result     DocumentsFilingResult
	content    map[string]string
	inode      map[string]string
	actionByID map[string]string // file name -> action ID
}

func (f *docsFixture) filePair(dest DocumentsDestination) filedPair {
	f.t.Helper()
	p := filedPair{content: map[string]string{}, inode: map[string]string{}, actionByID: map[string]string{}}
	for _, name := range []string{"estimate.pdf", "other.pdf"} {
		p.content[name] = f.read(name)
		p.inode[name] = f.inode(name)
	}
	p.result = f.mustPress(dest, keyA, "estimate.pdf", "other.pdf")
	if p.result.Filed != 2 {
		f.t.Fatalf("setup: %+v", p.result)
	}
	for _, o := range p.result.Outcomes {
		p.actionByID[o.Name] = o.ActionID
	}
	return p
}

func (f *docsFixture) requireBack(p filedPair, names ...string) {
	f.t.Helper()
	for _, name := range names {
		if f.read(name) != p.content[name] || f.inode(name) != p.inode[name] {
			f.t.Fatalf("%s is not the same file back where it was", name)
		}
	}
}

func (f *docsFixture) action(id string) FileAction {
	f.t.Helper()
	actions, err := f.s.ListActions("ws-1")
	if err != nil {
		f.t.Fatal(err)
	}
	for _, a := range actions {
		if a.ID == id {
			return a
		}
	}
	f.t.Fatalf("no action %s", id)
	return FileAction{}
}

func TestDocumentsUndo_AllPutsEveryFileBackAndAnswersARepeatFromTheJournal(t *testing.T) {
	f := newDocsFixture(t, "estimate.pdf", "other.pdf")
	f.mkdir("Taxes")
	p := f.filePair(DocumentsDestination{Folder: "Taxes"})
	movesAfterFiling, _ := f.mover.counts()

	undo, err := f.s.UndoFiling(t.Context(), "local", "ws-1", p.result.FilingID)
	if err != nil || undo.Back != 2 || undo.NotBack != 0 || undo.Interrupted || undo.Replayed {
		t.Fatalf("undo: %+v %v", undo, err)
	}
	f.requireBack(p, "estimate.pdf", "other.pdf")
	if f.exists("Taxes/estimate.pdf") || f.exists("Taxes/other.pdf") {
		t.Fatal("a file is still in the folder it was filed into")
	}
	if !f.exists("Taxes") {
		t.Fatal("Ori must not remove a folder")
	}
	for _, o := range undo.Outcomes {
		if o.Result != UndoOutcomeUndone {
			t.Fatalf("outcome: %+v", o)
		}
	}

	again, err := f.s.UndoFiling(t.Context(), "local", "ws-1", p.result.FilingID)
	if err != nil || !again.Replayed || again.Back != 2 {
		t.Fatalf("a repeat must report the first run: %+v %v", again, err)
	}
	if moves, _ := f.mover.counts(); moves != movesAfterFiling+2 {
		t.Fatalf("each file goes back once, got %d moves", moves-movesAfterFiling)
	}
	receipt, err := f.s.ReadFiling(t.Context(), "local", "ws-1", p.result.FilingID, 0)
	if err != nil || receipt.UndoAvailable {
		t.Fatalf("nothing is left to put back: %+v %v", receipt, err)
	}
	standing, err := f.s.DocumentsFilingStanding("local", "ws-1", p.result.FilingID)
	if err != nil || standing.Filed != 0 || standing.Back != 2 || standing.UndoAvailable {
		t.Fatalf("history must say both are back: %+v %v", standing, err)
	}
}

// Ori's own move and its undo must never be learned later as the person's manual
// placements: the files' identities stay in the journal, so the observer keeps
// excluding them after they are put back.
func TestDocumentsUndo_APutBackFileIsNotLearnedAsAManualPlacement(t *testing.T) {
	f := newDocsFixture(t, "estimate.pdf", "other.pdf")
	f.mkdir("Taxes")
	since := time.Now().Add(-time.Hour)
	p := f.filePair(DocumentsDestination{Folder: "Taxes"})
	if _, err := f.s.UndoFiling(t.Context(), "local", "ws-1", p.result.FilingID); err != nil {
		t.Fatal(err)
	}
	hidden, err := f.s.ObservedActionFiles("ws-1", since)
	if err != nil {
		t.Fatal(err)
	}
	for name, inode := range p.inode {
		if !hidden[inode] {
			t.Fatalf("%s: Ori's own move and undo must stay out of observation", name)
		}
	}
	standing, err := f.s.DocumentsFilingStanding("local", "ws-1", p.result.FilingID)
	if err != nil || standing.Back != 2 {
		t.Fatalf("standing: %+v %v", standing, err)
	}
	if _, err := f.s.DocumentsFilingStanding("stranger", "ws-1", p.result.FilingID); !errors.Is(err, ErrUndoUnavailable) {
		t.Fatalf("another owner must not read a filing's standing, got %v", err)
	}
	if _, err := f.s.UndoPlacedFile(t.Context(), "stranger", "ws-1", p.result.FilingID, p.actionByID["estimate.pdf"]); !errors.Is(err, ErrUndoUnavailable) {
		t.Fatalf("another owner must not put a file back, got %v", err)
	}
	if _, err := f.s.UndoPlacedFile(t.Context(), "local", "ws-1", "filing-nope", p.actionByID["estimate.pdf"]); !errors.Is(err, ErrUndoUnavailable) {
		t.Fatalf("a file must belong to the filing it is put back through, got %v", err)
	}
}

func TestDocumentsUndo_OneFileAtATimeThenTheRest(t *testing.T) {
	f := newDocsFixture(t, "estimate.pdf", "other.pdf")
	f.mkdir("Taxes/2024")
	p := f.filePair(DocumentsDestination{Folder: "Taxes/2024"})

	one, err := f.s.Undo(t.Context(), "ws-1", p.actionByID["estimate.pdf"], "local")
	if err != nil || one.Result != "undone" || one.RestoredTo != "estimate.pdf" {
		t.Fatalf("per-file undo: %+v %v", one, err)
	}
	f.requireBack(p, "estimate.pdf")
	if !f.exists("Taxes/2024/other.pdf") || f.exists("other.pdf") {
		t.Fatal("only the chosen file may move")
	}
	if _, err := f.s.Undo(t.Context(), "ws-1", p.actionByID["estimate.pdf"], "local"); !errors.Is(err, ErrUndoUnavailable) {
		t.Fatalf("a second undo of the same file must be refused, got %v", err)
	}

	rest, err := f.s.UndoFiling(t.Context(), "local", "ws-1", p.result.FilingID)
	if err != nil || rest.Back != 2 || rest.NotBack != 0 {
		t.Fatalf("the rest: %+v %v", rest, err)
	}
	var already int
	for _, o := range rest.Outcomes {
		if o.Result == UndoOutcomeAlreadyUndone {
			already++
		}
	}
	if already != 1 {
		t.Fatalf("the file put back earlier is reported as already back, not counted twice: %+v", rest.Outcomes)
	}
	f.requireBack(p, "estimate.pdf", "other.pdf")
}

func TestDocumentsUndo_NeverOverwritesAndSaysWhy(t *testing.T) {
	f := newDocsFixture(t, "estimate.pdf", "other.pdf")
	f.mkdir("Taxes")
	p := f.filePair(DocumentsDestination{Folder: "Taxes"})
	f.write("estimate.pdf", "a new file took the original name")

	undo, err := f.s.UndoFiling(t.Context(), "local", "ws-1", p.result.FilingID)
	if err != nil || undo.Back != 1 || undo.NotBack != 1 {
		t.Fatalf("undo: %+v %v", undo, err)
	}
	if f.read("estimate.pdf") != "a new file took the original name" || f.read("Taxes/estimate.pdf") != p.content["estimate.pdf"] {
		t.Fatal("a file was replaced or lost")
	}
	var refused FilingUndoOutcome
	for _, o := range undo.Outcomes {
		if o.Name == "estimate.pdf" {
			refused = o
		}
	}
	if refused.Result != UndoOutcomeFailed || !strings.Contains(refused.Message, "did not overwrite") || refused.Location != LocationFiled || refused.Destination != "Taxes/estimate.pdf" {
		t.Fatalf("it must say what it did not do and where the file is: %+v", refused)
	}
	f.requireBack(p, "other.pdf")
}

func TestDocumentsUndo_RefusesWhatChangedSinceFiling(t *testing.T) {
	t.Run("the filed copy was edited", func(t *testing.T) {
		f := newDocsFixture(t, "estimate.pdf")
		f.mkdir("Taxes")
		p := f.mustPress(DocumentsDestination{Folder: "Taxes"}, keyA, "estimate.pdf")
		f.write("Taxes/estimate.pdf", "edited since it was filed, and longer")
		result, err := f.s.Undo(t.Context(), "ws-1", p.Outcomes[0].ActionID, "local")
		if err != nil || result.Result != "failed" || !strings.Contains(result.Message, "changed") {
			t.Fatalf("undo: %+v %v", result, err)
		}
		if f.read("Taxes/estimate.pdf") != "edited since it was filed, and longer" || f.exists("estimate.pdf") {
			t.Fatal("an edited file must be left alone")
		}
	})
	t.Run("the filed copy was replaced by a different file", func(t *testing.T) {
		f := newDocsFixture(t, "estimate.pdf")
		f.mkdir("Taxes")
		f.mustPress(DocumentsDestination{Folder: "Taxes"}, keyA, "estimate.pdf")
		original, _ := f.s.ListActions("ws-1")
		f.replaceFiledCopy("Taxes/estimate.pdf")
		result, err := f.s.Undo(t.Context(), "ws-1", original[0].ID, "local")
		if err != nil || result.Result != "failed" {
			t.Fatalf("undo: %+v %v", result, err)
		}
		if f.exists("estimate.pdf") {
			t.Fatal("a different file with the same size and time must not be taken for the filed one")
		}
	})
	t.Run("the filed copy is gone", func(t *testing.T) {
		f := newDocsFixture(t, "estimate.pdf")
		f.mkdir("Taxes")
		p := f.mustPress(DocumentsDestination{Folder: "Taxes"}, keyA, "estimate.pdf")
		if err := os.Remove(f.abs("Taxes/estimate.pdf")); err != nil {
			t.Fatal(err)
		}
		result, err := f.s.Undo(t.Context(), "ws-1", p.Outcomes[0].ActionID, "local")
		if err != nil || result.Result != "failed" {
			t.Fatalf("undo: %+v %v", result, err)
		}
	})
	t.Run("the folder it was filed into was replaced", func(t *testing.T) {
		f := newDocsFixture(t, "estimate.pdf")
		f.mkdir("Taxes")
		p := f.mustPress(DocumentsDestination{Folder: "Taxes"}, keyA, "estimate.pdf")
		if err := os.Mkdir(f.abs("Taxes.fresh"), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(f.abs("Taxes"), f.abs("Taxes.old")); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(f.abs("Taxes.fresh"), f.abs("Taxes")); err != nil {
			t.Fatal(err)
		}
		f.write("Taxes/estimate.pdf", "a different file in the new folder")
		result, err := f.s.Undo(t.Context(), "ws-1", p.Outcomes[0].ActionID, "local")
		if err != nil || result.Result != "failed" || !strings.Contains(result.Message, "folder") {
			t.Fatalf("undo: %+v %v", result, err)
		}
		if f.read("Taxes/estimate.pdf") != "a different file in the new folder" || f.exists("estimate.pdf") || !f.exists("Taxes.old/estimate.pdf") {
			t.Fatal("nothing may move when the folder is not the one it was filed into")
		}
	})
}

// replaceFiledCopy puts a different file (same size, same time, new inode) at rel.
func (f *docsFixture) replaceFiledCopy(rel string) {
	f.t.Helper()
	original, err := os.Lstat(f.abs(rel))
	if err != nil {
		f.t.Fatal(err)
	}
	tmp := f.abs(rel) + ".impostor"
	content := strings.Repeat("x", int(original.Size()))
	if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
		f.t.Fatal(err)
	}
	if err := os.Chtimes(tmp, original.ModTime(), original.ModTime()); err != nil {
		f.t.Fatal(err)
	}
	if err := os.Rename(tmp, f.abs(rel)); err != nil {
		f.t.Fatal(err)
	}
}

func TestDocumentsUndo_ASwapAtTheLastInstantIsMovedBack(t *testing.T) {
	f := newDocsFixture(t, "estimate.pdf")
	f.mkdir("Taxes")
	p := f.mustPress(DocumentsDestination{Folder: "Taxes"}, keyA, "estimate.pdf")
	f.real.hook = func(stage moveStage) {
		if stage == stageBeforeRename {
			f.replaceFiledCopy("Taxes/estimate.pdf")
		}
	}
	result, err := f.s.Undo(t.Context(), "ws-1", p.Outcomes[0].ActionID, "local")
	if err != nil || result.Result != "failed" {
		t.Fatalf("undo: %+v %v", result, err)
	}
	if f.exists("estimate.pdf") || !f.exists("Taxes/estimate.pdf") {
		t.Fatal("the impostor must be moved back, and nothing left at the original name")
	}
}

func TestDocumentsUndo_AnInterruptedUndoIsSettledFromTheDisk(t *testing.T) {
	t.Run("it dies after the file was put back", func(t *testing.T) {
		f := newDocsFixture(t, "estimate.pdf")
		f.mkdir("Taxes")
		content := f.read("estimate.pdf")
		result := f.mustPress(DocumentsDestination{Folder: "Taxes"}, keyA, "estimate.pdf")
		actionID := result.Outcomes[0].ActionID
		f.mover.exitAfterMove = true
		runInterrupted(t, func() { _, _ = f.s.UndoFiling(t.Context(), "local", "ws-1", result.FilingID) })
		f.mover.exitAfterMove = false
		if got := f.action(actionID).Undo; got != UndoInProgress {
			t.Fatalf("the record was cut short, got %q", got)
		}
		movesBefore, _ := f.mover.counts()

		// The next undo settles the record from the disk instead of staying stuck.
		if _, err := f.s.Undo(t.Context(), "ws-1", actionID, "local"); !errors.Is(err, ErrUndoUnavailable) {
			t.Fatalf("the file is already back, so there is nothing more to undo: %v", err)
		}
		if got := f.action(actionID).Undo; got != UndoDone {
			t.Fatalf("a file that is back must be recorded as undone, got %q", got)
		}
		if f.read("estimate.pdf") != content {
			t.Fatal("the file is not back")
		}
		if moves, _ := f.mover.counts(); moves != movesBefore {
			t.Fatal("settling must never call the mover")
		}
		replay, err := f.s.UndoFiling(t.Context(), "local", "ws-1", result.FilingID)
		if err != nil || replay.Back != 1 {
			t.Fatalf("the bulk answer must now count it back: %+v %v", replay, err)
		}
	})
	t.Run("it dies before the file was moved", func(t *testing.T) {
		f := newDocsFixture(t, "estimate.pdf")
		f.mkdir("Taxes")
		content := f.read("estimate.pdf")
		result := f.mustPress(DocumentsDestination{Folder: "Taxes"}, keyA, "estimate.pdf")
		actionID := result.Outcomes[0].ActionID
		f.mover.exitBeforeMove = true
		runInterrupted(t, func() { _, _ = f.s.UndoFiling(t.Context(), "local", "ws-1", result.FilingID) })
		f.mover.exitBeforeMove = false

		// Nothing moved, so it is safe to try again, and the retry works.
		retry, err := f.s.Undo(t.Context(), "ws-1", actionID, "local")
		if err != nil || retry.Result != "undone" {
			t.Fatalf("retry: %+v %v", retry, err)
		}
		if f.read("estimate.pdf") != content || f.exists("Taxes/estimate.pdf") {
			t.Fatal("the retry did not put the file back")
		}
	})
}

func TestDocumentsUndo_FailsClosedWhenTheMoverIsUnavailable(t *testing.T) {
	f := newDocsFixture(t, "estimate.pdf")
	f.mkdir("Taxes")
	result := f.mustPress(DocumentsDestination{Folder: "Taxes"}, keyA, "estimate.pdf")
	f.s.SetConditionalMover(refusingMover{})
	undo, err := f.s.Undo(t.Context(), "ws-1", result.Outcomes[0].ActionID, "local")
	if err != nil || undo.Result != "failed" || !strings.Contains(undo.Message, "safely") {
		t.Fatalf("undo: %+v %v", undo, err)
	}
	if !f.exists("Taxes/estimate.pdf") || f.exists("estimate.pdf") {
		t.Fatal("something moved")
	}
}

func TestDocumentsUndo_AnOlderBuildsCopyOfAPlacementCannotBeUndone(t *testing.T) {
	f := newDocsFixture(t, "estimate.pdf")
	f.mkdir("Taxes")
	result := f.mustPress(DocumentsDestination{Folder: "Taxes"}, keyA, "estimate.pdf")
	if _, err := f.s.store.UpdateScanState("ws-1", func(state *ScanState) error {
		for i := range state.Actions {
			state.Actions[i].Place = nil // what an older build's rewrite leaves
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Undo(t.Context(), "ws-1", result.Outcomes[0].ActionID, "local"); !errors.Is(err, ErrUndoUnavailable) {
		t.Fatalf("want unavailable, got %v", err)
	}
	if _, err := f.s.UndoFiling(t.Context(), "local", "ws-1", result.FilingID); !errors.Is(err, ErrUndoUnavailable) {
		t.Fatalf("want unavailable, got %v", err)
	}
	if !f.exists("Taxes/estimate.pdf") {
		t.Fatal("something moved")
	}
}

// A placement is not a category move: the assistant's category knowledge cites
// none, so neither filing nor undoing one tells that observer anything.
func TestDocumentsUndo_NotifiesNoCategoryKnowledgeObserver(t *testing.T) {
	f := newDocsFixture(t, "estimate.pdf", "other.pdf")
	f.mkdir("Taxes")
	observer := &journalKnowledgeObserver{t: t, service: f.s}
	f.s.SetReviewedKnowledgeObserver(observer)
	p := f.filePair(DocumentsDestination{Folder: "Taxes"})
	if _, err := f.s.UndoFiling(t.Context(), "local", "ws-1", p.result.FilingID); err != nil {
		t.Fatal(err)
	}
	if len(observer.moves) != 0 || len(observer.undos) != 0 {
		t.Fatalf("placements notified the category observer: %+v", observer)
	}
}
