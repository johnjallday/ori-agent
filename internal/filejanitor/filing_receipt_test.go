package filejanitor

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func destinationCounts(receipt FilingReceipt) map[string]int {
	counts := map[string]int{}
	for _, destination := range receipt.ByDestination {
		counts[destination.Destination] = destination.Count
	}
	return counts
}

// R43, R44: after a full success every number is what the journal says, and the
// receipt reads the same however many times it is asked.
func TestReadFiling_TotalsMatchTheJournalAfterAFullSuccess(t *testing.T) {
	s, _, filed, _, _ := filedFixture(t, "a.png", "b.png", "c.pdf", "passport-a.pdf")
	receipt, err := s.ReadFiling(t.Context(), "local", "ws-1", filed.FilingID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.State != FilingDone || receipt.Filed != 3 || receipt.Trashed != 0 || receipt.NotMovedTotal != 0 || len(receipt.NotMoved) != 0 || receipt.NotAttempted != 0 || receipt.HistoryTrimmed {
		t.Fatalf("receipt: %+v", receipt)
	}
	if got := destinationCounts(receipt); !reflect.DeepEqual(got, map[string]int{"Filed/Images": 2, "Filed/Documents": 1}) {
		t.Fatalf("one row per destination: %v", got)
	}
	// The sensitive-looking file was never ticked: it stays loose, and the receipt says so.
	if receipt.LooseNow != 1 || !receipt.UndoAvailable || receipt.Undo != nil || receipt.CompletedAt.IsZero() {
		t.Fatalf("loose now, Undo all on offer, not undone: %+v", receipt)
	}
	// Every number agrees with the engine's own journal.
	state, _ := s.store.LoadScanState("ws-1")
	applied := 0
	for _, action := range filingActions(state, filed.FilingID) {
		if action.Result == ResultApplied {
			applied++
		}
	}
	if applied != receipt.Filed+receipt.Trashed || filed.Filed != receipt.Filed {
		t.Fatalf("the journal says %d, the receipt %d, the press %d", applied, receipt.Filed+receipt.Trashed, filed.Filed)
	}
	again, err := s.ReadFiling(t.Context(), "local", "ws-1", filed.FilingID, 0)
	if err != nil || !reflect.DeepEqual(receipt, again) {
		t.Fatalf("a repeated request must read the same: %+v vs %+v (%v)", receipt, again, err)
	}
}

// R44: a file that did not move is not counted as filed, and is listed with its reason.
func TestReadFiling_APartialFailureIsCountedAndExplained(t *testing.T) {
	s, batch, _, root := planFixture(t, "a.png", "b.png", "c.pdf")
	s.SetMover(&selectiveMover{failFor: "b.png"}) // a partly successful press, as a real folder can give
	_, filed, err := pressFor(t, s, batch.ID, keyOne, nil)
	if err != nil || filed.Filed != 2 || filed.Failed != 1 {
		t.Fatalf("setup: %+v %v", filed, err)
	}
	receipt, err := s.ReadFiling(t.Context(), "local", "ws-1", filed.FilingID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Filed != 2 || receipt.NotMovedTotal != 1 || len(receipt.NotMoved) != 1 || !receipt.UndoAvailable {
		t.Fatalf("receipt: %+v", receipt)
	}
	notMoved := receipt.NotMoved[0]
	if notMoved.Name != "b.png" || notMoved.Result != ResultFailed || notMoved.Reason == "" || notMoved.Operation != OperationMove {
		t.Fatalf("the file that did not move says why: %+v", notMoved)
	}
	sum := 0
	for _, destination := range receipt.ByDestination {
		sum += destination.Count
	}
	if sum != receipt.Filed || !exists(t, root, "b.png") {
		t.Fatalf("the destinations add up to what was filed (%d vs %d), and b.png stayed", sum, receipt.Filed)
	}
}

// R44: Trash is counted on its own line, not as filed.
func TestReadFiling_TrashIsCountedApart(t *testing.T) {
	s, batch, candidates, _ := planFixture(t, "a.png", "b.png")
	s.SetTrash(newFakeTrash(t))
	s.SetMover(&realMover{})
	editDraft(t, s, batch.ID, "mark_trash", func(r *ReviewDraftEdit) {
		r.CandidateIDs = []string{candidateNamed(t, candidates, "b.png").ID}
	})
	_, filed, err := pressFor(t, s, batch.ID, keyOne, nil)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := s.ReadFiling(t.Context(), "local", "ws-1", filed.FilingID, 0)
	if err != nil || receipt.Filed != 1 || receipt.Trashed != 1 || receipt.NotMovedTotal != 0 {
		t.Fatalf("receipt: %+v %v", receipt, err)
	}
	if got := destinationCounts(receipt); !reflect.DeepEqual(got, map[string]int{"Filed/Images": 1}) {
		t.Fatalf("a trashed file is not a destination: %v", got)
	}
}

// A replayed press and the receipt tell the same story as the first answer.
func TestReadFiling_AgreesWithTheFirstAnswerAndItsReplay(t *testing.T) {
	s, batch, _, _ := planFixture(t, "a.png", "b.png")
	s.SetMover(&realMover{})
	_, first, err := pressFor(t, s, batch.ID, keyOne, nil)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.FileReviewPlan(t.Context(), "local", "ws-1", batch.ID, FileRequest{
		ClientKey: keyOne, ExpectedRevision: strings.Repeat("0", 64), ExpectedGroupRevision: strings.Repeat("0", 64),
	})
	if err != nil || !replay.Replayed || replay.FilingID != first.FilingID {
		t.Fatalf("replay: %+v %v", replay, err)
	}
	receipt, err := s.ReadFiling(t.Context(), "local", "ws-1", first.FilingID, 0)
	if err != nil || receipt.Filed != first.Filed || receipt.Filed != replay.Filed {
		t.Fatalf("receipt %+v, first %+v, replay %+v", receipt, first, replay)
	}
}

// A run that stopped before finishing says so, and counts what it never reached.
func TestReadFiling_AnInterruptedRunIsNotCalledDone(t *testing.T) {
	s, _, filed, _, _ := filedFixture(t, "a.png", "b.png", "c.png")
	if _, err := s.store.UpdateScanState("ws-1", func(state *ScanState) error {
		for i := range state.Filings {
			state.Filings[i].CompletedAt = time.Time{}
			state.Filings[i].PlannedMoves = 5 // the plan had five; only three were reached
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	receipt, err := s.ReadFiling(t.Context(), "local", "ws-1", filed.FilingID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.State != FilingInterrupted || receipt.NotAttempted != 2 || receipt.Filed != 3 || !receipt.CompletedAt.IsZero() {
		t.Fatalf("an unfinished run: %+v", receipt)
	}
}

// Older journal entries can be pruned; the receipt says it cannot account for every
// file rather than inventing a total.
func TestReadFiling_PrunedHistoryIsSaidSo(t *testing.T) {
	s, _, filed, _, _ := filedFixture(t, "a.png", "b.png", "c.png")
	if _, err := s.store.UpdateScanState("ws-1", func(state *ScanState) error {
		kept := state.Actions[:0:0]
		for _, action := range state.Actions {
			if action.SourceName != "a.png" {
				kept = append(kept, action)
			}
		}
		state.Actions = kept
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	receipt, err := s.ReadFiling(t.Context(), "local", "ws-1", filed.FilingID, 0)
	if err != nil || !receipt.HistoryTrimmed || receipt.Filed != 2 {
		t.Fatalf("receipt: %+v %v", receipt, err)
	}
}

func TestReadFiling_RefusesWhatIsNotYours(t *testing.T) {
	s, _, filed, _, _ := filedFixture(t, "a.png")
	for name, call := range map[string]func() error{
		"another owner": func() error {
			_, err := s.ReadFiling(t.Context(), "someone-else", "ws-1", filed.FilingID, 0)
			return err
		},
		"unknown":        func() error { _, err := s.ReadFiling(t.Context(), "local", "ws-1", "filing-nope", 0); return err },
		"not an id":      func() error { _, err := s.ReadFiling(t.Context(), "local", "ws-1", "../x", 0); return err },
		"negative start": func() error { _, err := s.ReadFiling(t.Context(), "local", "ws-1", filed.FilingID, -1); return err },
	} {
		if err := call(); !errors.Is(err, ErrFilingNotFound) {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

// The list feeds History's "Last filing" line: this owner's filings, newest first,
// with whether Undo all is still on offer.
func TestListFilings_CarriesTheUndoState(t *testing.T) {
	s, _, filed, _, _ := filedFixture(t, "a.png", "b.png")
	list, err := s.ListFilings(t.Context(), "local", "ws-1", 5)
	if err != nil || len(list) != 1 || list[0].FilingID != filed.FilingID || list[0].Undone || !list[0].UndoAvailable || list[0].Filed != 2 || list[0].State != FilingDone {
		t.Fatalf("before Undo all: %+v %v", list, err)
	}
	if _, err := s.UndoFiling(t.Context(), "local", "ws-1", filed.FilingID); err != nil {
		t.Fatal(err)
	}
	list, err = s.ListFilings(t.Context(), "local", "ws-1", 5)
	if err != nil || len(list) != 1 || !list[0].Undone || list[0].UndoAvailable {
		t.Fatalf("after Undo all: %+v %v", list, err)
	}
	if other, err := s.ListFilings(t.Context(), "someone-else", "ws-1", 5); err != nil || len(other) != 0 {
		t.Fatalf("another owner sees nothing: %+v %v", other, err)
	}
}

// Two filings are listed newest first.
func TestListFilings_NewestFirst(t *testing.T) {
	s, batch, filed, root, _ := filedFixture(t, "a.png", "b.png")
	if _, err := s.UndoFiling(t.Context(), "local", "ws-1", filed.FilingID); err != nil {
		t.Fatal(err)
	}
	// The files are back and kept out of a scan until Ori is told to forget that
	// (R52): a scan now proposes nothing, and after forgetting it proposes them.
	if quiet, created, err := s.ScanNow("ws-1", ScanSourceManual); err != nil || created {
		t.Fatalf("restored files must not be proposed straight away: %+v %v %v", quiet, created, err)
	}
	if _, err := s.ForgetRestored(t.Context(), "local", "ws-1", filed.FilingID); err != nil {
		t.Fatal(err)
	}
	next, created, err := s.ScanNow("ws-1", ScanSourceManual)
	if err != nil || !created || next.ID == batch.ID {
		t.Fatalf("scan after forgetting: %v %v", created, err)
	}
	_, second, err := pressFor(t, s, next.ID, keyTwo, nil)
	if err != nil || second.Filed == 0 {
		t.Fatalf("second press: %+v %v", second, err)
	}
	list, err := s.ListFilings(t.Context(), "local", "ws-1", 5)
	if err != nil || len(list) != 2 || list[0].FilingID != second.FilingID || list[1].FilingID != filed.FilingID {
		t.Fatalf("newest first: %+v %v", list, err)
	}
	if limited, _ := s.ListFilings(t.Context(), "local", "ws-1", 1); len(limited) != 1 || limited[0].FilingID != second.FilingID {
		t.Fatalf("a limit keeps the newest: %+v", limited)
	}
	_ = root
}
