package filejanitor

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	keyOne = "press-one-0123456789"
	keyTwo = "press-two-0123456789"
)

// pressFor works the plan out and presses exactly what it shows, as the picture's
// button does. mutate lets a test change what is sent.
func pressFor(t *testing.T, s *Service, batchID, key string, mutate func(*FileRequest)) (ReviewPlan, FilingResult, error) {
	t.Helper()
	plan := readPlan(t, s, batchID, ReviewPlanQuery{})
	request := FileRequest{
		ExpectedRevision: plan.Revision, ExpectedGroupRevision: plan.GroupRevision,
		PlanDigest: plan.PlanDigest, TrashAcknowledged: plan.TrashCount, ClientKey: key,
	}
	if mutate != nil {
		mutate(&request)
	}
	result, err := s.FileReviewPlan(t.Context(), "local", "ws-1", batchID, request)
	return plan, result, err
}

func exists(t *testing.T, root string, parts ...string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(append([]string{root}, parts...)...))
	return err == nil
}

// R36, R43: a press files exactly the plan, journals every file under one filing
// ID, spends an approval that was never a token, and a retry reports the same
// result without touching a file again.
func TestFileReviewPlan_FilesExactlyThePlanAndAnswersARetryTheSame(t *testing.T) {
	s, batch, _, root := planFixture(t, "a.png", "b.png", "c.pdf")
	mover := &realMover{}
	s.SetMover(mover)

	plan, result, err := pressFor(t, s, batch.ID, keyOne, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Filed != 3 || result.Failed != 0 || result.Stale != 0 || result.Replayed || result.Interrupted || result.NotAttempted != 0 || len(result.Outcomes) != 3 {
		t.Fatalf("result: %+v", result)
	}
	for _, name := range []string{"a.png", "b.png"} {
		if exists(t, root, name) || !exists(t, root, "Filed", "Images", name) {
			t.Fatalf("%s should be in Filed/Images", name)
		}
	}
	if exists(t, root, "c.pdf") || !exists(t, root, "Filed", "Documents", "c.pdf") {
		t.Fatal("c.pdf should be in Filed/Documents")
	}
	if mover.calls != 3 {
		t.Fatalf("moved %d files, want 3", mover.calls)
	}

	state, err := s.store.LoadScanState("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Filings) != 1 {
		t.Fatalf("one press must leave one filing record, got %d", len(state.Filings))
	}
	filing := state.Filings[0]
	if filing.ID != result.FilingID || filing.PlanDigest != plan.PlanDigest || filing.PlannedMoves != 3 || filing.PlannedTrash != 0 ||
		filing.CompletedAt.IsZero() || filing.Interrupted || filing.Owner != "local" || filing.ClientKey != keyOne {
		t.Fatalf("filing record: %+v", filing)
	}
	journaled := 0
	for _, action := range state.Actions {
		if action.IdempotencyKey == filing.ID {
			journaled++
			if action.Result != ResultApplied || !action.Undoable() {
				t.Fatalf("a filed file must be journaled as applied and undoable: %+v", action)
			}
		}
	}
	if journaled != 3 {
		t.Fatalf("the journal holds %d actions under the filing ID, want 3", journaled)
	}
	spent := 0
	for _, approval := range state.Approvals {
		if approval.IdempotencyKey == filing.ID {
			spent++
			if !approval.Consumed() || approval.ReviewRevision != plan.Revision {
				t.Fatalf("the approval must be spent at birth and bound to the revision: %+v", approval)
			}
		} else if !approval.Consumed() && approval.BatchID == batch.ID {
			t.Fatal("an unspent approval for this batch survived the press")
		}
	}
	if spent != 1 {
		t.Fatalf("want one spent approval for the filing, got %d", spent)
	}

	// The ticks never come back for what is left.
	after, err := s.ReadReviewDraft(t.Context(), "local", "ws-1", batch.ID)
	if err != nil || after.State == "default" || len(after.Choices) != 0 {
		t.Fatalf("after filing nothing is ticked on open: %q %+v %v", after.State, after.Choices, err)
	}

	// A retry with the same key answers from the journal and moves nothing.
	_, replay, err := pressFor(t, s, batch.ID, keyOne, func(r *FileRequest) {
		r.PlanDigest, r.ExpectedRevision = plan.PlanDigest, plan.Revision
	})
	if err != nil {
		t.Fatalf("a retry must report the first result: %v", err)
	}
	if !replay.Replayed || replay.FilingID != result.FilingID || replay.Filed != 3 || len(replay.Outcomes) != 3 || mover.calls != 3 {
		t.Fatalf("replay: %+v (moves %d)", replay, mover.calls)
	}
}

// R36: any difference between what the press quotes and what is true now moves
// nothing, writes nothing, and brings back the fresh plan.
func TestFileReviewPlan_RefusesAQuoteThatNoLongerMatches(t *testing.T) {
	for name, mutate := range map[string]func(*FileRequest){
		"an altered digest":     func(r *FileRequest) { r.PlanDigest = strings.Repeat("f", 64) },
		"a stale revision":      func(r *FileRequest) { r.ExpectedRevision = strings.Repeat("e", 64) },
		"a stale group version": func(r *FileRequest) { r.ExpectedGroupRevision = strings.Repeat("d", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			s, batch, _, root := planFixture(t, "a.png", "b.png")
			mover := &realMover{}
			s.SetMover(mover)
			before := scanStateBytes(t, s)
			plan, result, err := pressFor(t, s, batch.ID, keyOne, mutate)
			if !errors.Is(err, ErrReviewPlanChanged) {
				t.Fatalf("got %v", err)
			}
			if result.Plan.PlanDigest != plan.PlanDigest || result.Plan.MoveCount != 2 || len(result.Outcomes) != 0 {
				t.Fatalf("the fresh plan must come back and nothing else: %+v", result)
			}
			if mover.calls != 0 || !exists(t, root, "a.png") || !exists(t, root, "b.png") {
				t.Fatal("a refused press moved a file")
			}
			if !bytes.Equal(before, scanStateBytes(t, s)) {
				t.Fatal("a refused press left an approval or a filing record behind")
			}
		})
	}
}

// R37: files going to the Trash must be acknowledged by count, and Trash stays
// one file at a time inside a press.
func TestFileReviewPlan_TrashNeedsItsCountAcknowledged(t *testing.T) {
	s, batch, candidates, root := planFixture(t, "a.png", "b.png")
	trash := newFakeTrash(t)
	s.SetTrash(trash)
	mover := &realMover{}
	s.SetMover(mover)
	editDraft(t, s, batch.ID, "mark_trash", func(r *ReviewDraftEdit) {
		r.CandidateIDs = []string{candidateNamed(t, candidates, "b.png").ID}
	})
	before := scanStateBytes(t, s)
	for _, acknowledged := range []int{0, 2} {
		_, result, err := pressFor(t, s, batch.ID, keyOne, func(r *FileRequest) { r.TrashAcknowledged = acknowledged })
		if !errors.Is(err, ErrTrashNotAcknowledged) {
			t.Fatalf("acknowledged %d: got %v", acknowledged, err)
		}
		if result.Plan.TrashCount != 1 || mover.calls != 0 || trash.moves != 0 || !exists(t, root, "a.png") || !exists(t, root, "b.png") {
			t.Fatalf("a press without the right count must do nothing: %+v", result)
		}
		if !bytes.Equal(before, scanStateBytes(t, s)) {
			t.Fatal("a refused press wrote state")
		}
	}
	_, result, err := pressFor(t, s, batch.ID, keyOne, nil)
	if err != nil || result.Filed != 1 || result.Trashed != 1 || result.Failed != 0 {
		t.Fatalf("the right count files one and trashes one: %+v %v", result, err)
	}
	if exists(t, root, "a.png") || !exists(t, root, "Filed", "Images", "a.png") || exists(t, root, "b.png") || trash.moves != 1 {
		t.Fatal("a.png should be filed and b.png trashed")
	}
}

func TestFileReviewPlan_RefusesWhatItCannotFile(t *testing.T) {
	t.Run("a retry key that is not a key", func(t *testing.T) {
		s, batch, _, _ := planFixture(t, "a.png")
		s.SetMover(&realMover{})
		for _, key := range []string{"", "short", "has spaces in it 0123456", strings.Repeat("k", 129), "semi;colon0123456789"} {
			if _, _, err := pressFor(t, s, batch.ID, key, nil); !errors.Is(err, ErrInvalidAction) {
				t.Fatalf("key %q: got %v", key, err)
			}
		}
	})
	t.Run("nothing to file", func(t *testing.T) {
		s, batch, _, _ := planFixture(t, "a.png")
		s.SetMover(&realMover{})
		editDraft(t, s, batch.ID, "clear_all", nil)
		if _, _, err := pressFor(t, s, batch.ID, keyOne, nil); !errors.Is(err, ErrInvalidAction) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("latest is not a concrete review", func(t *testing.T) {
		s, _, _, _ := planFixture(t, "a.png")
		s.SetMover(&realMover{})
		_, err := s.FileReviewPlan(t.Context(), "local", "ws-1", "latest", FileRequest{
			ExpectedRevision: strings.Repeat("a", 64), ExpectedGroupRevision: strings.Repeat("b", 64), ClientKey: keyOne,
		})
		if !errors.Is(err, ErrReviewUnavailable) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("no way to move files", func(t *testing.T) {
		s, batch, _, root := planFixture(t, "a.png")
		s.SetMover(nil)
		if _, _, err := pressFor(t, s, batch.ID, keyOne, nil); !errors.Is(err, ErrInvalidAction) {
			t.Fatalf("got %v", err)
		}
		if !exists(t, root, "a.png") {
			t.Fatal("a.png moved")
		}
	})
	t.Run("more than one press may file", func(t *testing.T) {
		names := make([]string, PlanMaxOperations+1)
		for i := range names {
			names[i] = fmt.Sprintf("image-%04d.png", i)
		}
		s, batch, _, root := planFixture(t, names...)
		mover := &realMover{}
		s.SetMover(mover)
		if _, _, err := pressFor(t, s, batch.ID, keyOne, nil); !errors.Is(err, ErrReviewBounds) {
			t.Fatalf("got %v", err)
		}
		if mover.calls != 0 || !exists(t, root, "image-0000.png") {
			t.Fatal("an over-limit press moved a file")
		}
	})
}

// R41: access revoked between the picture and the press means nothing moves.
func TestFileReviewPlan_RevokedAccessBeforeThePressMovesNothing(t *testing.T) {
	s, batch, _, root := planFixture(t, "a.png", "b.png")
	mover := &realMover{}
	s.SetMover(mover)
	plan := readPlan(t, s, batch.ID, ReviewPlanQuery{})
	if _, err := s.RevokeAccess(nil, "ws-1"); err != nil {
		t.Fatal(err)
	}
	_, err := s.FileReviewPlan(t.Context(), "local", "ws-1", batch.ID, FileRequest{
		ExpectedRevision: plan.Revision, ExpectedGroupRevision: plan.GroupRevision,
		PlanDigest: plan.PlanDigest, ClientKey: keyOne,
	})
	if err == nil {
		t.Fatal("a press after the folder was revoked was accepted")
	}
	if mover.calls != 0 || !exists(t, root, "a.png") || !exists(t, root, "b.png") {
		t.Fatal("a file moved after access was revoked")
	}
}

// A7: a folder that was relinked (a different folder, or the same one granted
// again) between the picture and the press is not the folder the person looked at:
// nothing moves, in either folder, and the plan they read cannot be spent on it.
func TestFileReviewPlan_ARelinkedFolderBeforeThePressMovesNothing(t *testing.T) {
	s, batch, _, root := planFixture(t, "a.png", "b.png")
	mover := &realMover{}
	s.SetMover(mover)
	plan := readPlan(t, s, batch.ID, ReviewPlanQuery{})
	folderBefore := exists(t, root, "Filed")
	if _, err := s.store.UpdateSettings("ws-1", func(settings *JanitorSettings) error {
		settings.RootID = "replacement-root"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	beforeRelinkedState := scanStateBytes(t, s)
	_, err := s.FileReviewPlan(t.Context(), "local", "ws-1", batch.ID, FileRequest{
		ExpectedRevision: plan.Revision, ExpectedGroupRevision: plan.GroupRevision,
		PlanDigest: plan.PlanDigest, ClientKey: keyOne,
	})
	if err == nil {
		t.Fatal("a press after the folder was relinked was accepted")
	}
	if mover.calls != 0 || !exists(t, root, "a.png") || !exists(t, root, "b.png") {
		t.Fatal("a file moved after the folder was relinked")
	}
	if exists(t, root, "Filed") != folderBefore {
		t.Fatalf("a refused press changed whether %q exists (was %v)", "Filed", folderBefore)
	}
	if !bytes.Equal(beforeRelinkedState, scanStateBytes(t, s)) {
		t.Fatal("a refused press left an approval or a filing record behind")
	}
}

func TestFileReviewPlan_AnotherPersonsKeyIsAConflictNotAReplay(t *testing.T) {
	s, batch, _, _ := planFixture(t, "a.png")
	s.SetMover(&realMover{})
	if _, _, err := pressFor(t, s, batch.ID, keyOne, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.UpdateScanState("ws-1", func(state *ScanState) error {
		state.Filings[0].Owner = "someone-else"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := pressFor(t, s, batch.ID, keyOne, nil); !errors.Is(err, ErrReviewConflict) {
		t.Fatalf("got %v", err)
	}
}

// After admission the saved choices have changed (the files are filed), so an
// edit that quoted the old revisions loses its compare-and-swap.
func TestFileReviewPlan_AnEditAfterTheFilingIsAConflict(t *testing.T) {
	s, batch, candidates, _ := planFixture(t, "a.png", "b.png")
	s.SetMover(&realMover{})
	view, err := s.ReadReviewDraft(t.Context(), "local", "ws-1", batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := pressFor(t, s, batch.ID, keyOne, nil); err != nil {
		t.Fatal(err)
	}
	_, err = s.EditReviewDraft("local", "ws-1", batch.ID, ReviewDraftEdit{
		ExpectedRevision: view.Revision, ExpectedGroupRevision: view.GroupRevision,
		Action: "exclude_files", CandidateIDs: []string{candidates[0].ID},
	})
	if !errors.Is(err, ErrReviewConflict) {
		t.Fatalf("got %v", err)
	}
}

// A run the process died in the middle of is reported honestly: interrupted,
// with the files that were never attempted counted, and marked at the next press.
func TestFileReviewPlan_AnInterruptedRunIsReportedNotResumed(t *testing.T) {
	s, batch, _, _ := planFixture(t, "a.png", "b.png")
	mover := &realMover{}
	s.SetMover(mover)
	if _, err := s.store.UpdateScanState("ws-1", func(state *ScanState) error {
		state.Filings = append(state.Filings, FilingRecord{
			ID: "filing-crashed", Owner: "local", BatchID: batch.ID, RootID: state.Batches[0].RootID,
			ClientKey: "crashed-key-0123456789", PlanDigest: strings.Repeat("c", 64),
			PlannedMoves: 3, AdmittedAt: time.Now().Add(-time.Hour),
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Asking for the crashed press again reports it and does not run it.
	plan := readPlan(t, s, batch.ID, ReviewPlanQuery{})
	again, err := s.FileReviewPlan(t.Context(), "local", "ws-1", batch.ID, FileRequest{
		ExpectedRevision: plan.Revision, ExpectedGroupRevision: plan.GroupRevision,
		PlanDigest: plan.PlanDigest, ClientKey: "crashed-key-0123456789",
	})
	if err != nil || !again.Replayed || !again.Interrupted || again.NotAttempted != 3 || len(again.Outcomes) != 0 || mover.calls != 0 {
		t.Fatalf("an interrupted run must be reported, not resumed: %+v %v", again, err)
	}
	// A new press marks the old record interrupted for good. (A filing record for
	// the batch ends the on-open ticks, so the person ticks again first.)
	editDraft(t, s, batch.ID, "select_filing_groups", nil)
	if _, _, err := pressFor(t, s, batch.ID, keyTwo, nil); err != nil {
		t.Fatal(err)
	}
	state, _ := s.store.LoadScanState("ws-1")
	marked := false
	for _, filing := range state.Filings {
		if filing.ID == "filing-crashed" {
			marked = filing.Interrupted
		}
	}
	if !marked {
		t.Fatal("the unfinished record was not marked interrupted")
	}
}

// Filing records are bounded and validated like the rest of the scan state.
func TestFilingRecords_AreBoundedAndValidated(t *testing.T) {
	s, batch, _, _ := planFixture(t, "a.png")
	record := func(i int) FilingRecord {
		return FilingRecord{
			ID: fmt.Sprintf("filing-%03d", i), Owner: "local", BatchID: batch.ID, RootID: "root-1",
			ClientKey: fmt.Sprintf("bounded-key-%010d", i), PlanDigest: strings.Repeat("a", 64),
			PlannedMoves: 1, AdmittedAt: time.Now().Add(time.Duration(i) * time.Second),
		}
	}
	if _, err := s.store.UpdateScanState("ws-1", func(state *ScanState) error {
		for i := range MaxRetainedFilings + 5 {
			state.Filings = append(state.Filings, record(i))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	state, _ := s.store.LoadScanState("ws-1")
	if len(state.Filings) != MaxRetainedFilings || state.Filings[0].ID != "filing-005" {
		t.Fatalf("the newest %d filings are kept, oldest dropped first: %d first=%s", MaxRetainedFilings, len(state.Filings), state.Filings[0].ID)
	}
	for name, bad := range map[string]func(*FilingRecord){
		"a key that is not a key": func(f *FilingRecord) { f.ClientKey = "x" },
		"no owner":                func(f *FilingRecord) { f.Owner = "" },
		"more than a press files": func(f *FilingRecord) { f.PlannedMoves = PlanMaxOperations + 1 },
		"no admission time":       func(f *FilingRecord) { f.AdmittedAt = time.Time{} },
	} {
		_, err := s.store.UpdateScanState("ws-1", func(state *ScanState) error {
			broken := record(99)
			bad(&broken)
			state.Filings = append(state.Filings, broken)
			return nil
		})
		if err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
	if _, err := s.store.UpdateScanState("ws-1", func(state *ScanState) error {
		state.Filings = append(state.Filings, record(10)) // an ID that is still retained
		return nil
	}); err == nil {
		t.Fatal("a duplicate filing ID was accepted")
	}
}
