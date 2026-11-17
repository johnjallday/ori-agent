package filejanitor

import (
	"errors"
	"testing"
)

// Group 3.1 (decision b): one menu choice on a question row sends every file of
// the row the person was shown to one folder. The tests below cover what makes that
// safe: the same edit rules as every other edit, the files filed are the files
// chosen, and a file that needs review still never moves on a guess.

func placeRow(t *testing.T, s *Service, batchID, key, category string) ReviewDraftView {
	t.Helper()
	return editDraft(t, s, batchID, "select_group", func(r *ReviewDraftEdit) {
		r.GroupID, r.Category = reviewGroupID(key), category
	})
}

// A stale view is refused whole: the row choice quotes the revisions the person
// saw, so a change made after that cannot be carried by it.
func TestQuestionRow_AStaleViewRejectsTheWholeChoice(t *testing.T) {
	s, batch, _, _ := planFixture(t, "passport-a.pdf", "passport-b.pdf", "one.png")
	before, err := s.ReadReviewDraft(t.Context(), "local", "ws-1", batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	for name, edit := range map[string]ReviewDraftEdit{
		"older choices": {ExpectedRevision: "0" + before.Revision[1:], ExpectedGroupRevision: before.GroupRevision},
		"older members": {ExpectedRevision: before.Revision, ExpectedGroupRevision: "0" + before.GroupRevision[1:]},
	} {
		edit.Action, edit.GroupID, edit.Category = "select_group", reviewGroupID("sensitive"), "documents"
		if _, err := s.EditReviewDraft("local", "ws-1", batch.ID, edit); !errors.Is(err, ErrReviewConflict) {
			t.Fatalf("%s: expected a conflict, got %v", name, err)
		}
	}
	after, err := s.ReadReviewDraft(t.Context(), "local", "ws-1", batch.ID)
	if err != nil || after.Revision != before.Revision {
		t.Fatalf("a refused row choice must store nothing: %v", err)
	}
}

// A file that arrives in a later scan is a new picture: the row choice made on an
// older scan binds only the files of that scan.
func TestQuestionRow_ALaterArrivalJoinsNothing(t *testing.T) {
	s, batch, candidates, root := planFixture(t, "passport-a.pdf", "one.png")
	placed := placeRow(t, s, batch.ID, "sensitive", "documents")
	agedFile(t, root, "passport-later.pdf", 10)
	newBatch, created, err := s.ScanNow("ws-1", ScanSourceManual)
	if err != nil || !created || newBatch.ID == batch.ID {
		t.Fatalf("second scan: %v %v", created, err)
	}
	view, err := s.ReadReviewDraft(t.Context(), "local", "ws-1", batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Revision != placed.Revision || len(view.Choices) != len(placed.Choices) {
		t.Fatalf("the old scan's choices changed when a new scan arrived: %+v", view)
	}
	for _, choice := range view.Choices {
		if choice.CandidateID != candidateNamed(t, candidates, "passport-a.pdf").ID &&
			choice.CandidateID != candidateNamed(t, candidates, "one.png").ID {
			t.Fatalf("a later arrival joined the old scan's choices: %+v", choice)
		}
	}
}

// The press files exactly what the row placed, and the execution fence agrees with
// the edit: a file that needs review (here an unknown type) moves only because the
// person answered for it, into the folder they chose.
func TestQuestionRow_PlacedFilesAreFiledWhereTheRowSaidAndNothingElseMoves(t *testing.T) {
	s, batch, _, root := planFixture(t, "passport-a.pdf", "passport-b.pdf", "mystery.qqq", "one.png")
	s.SetMover(&realMover{})
	placeRow(t, s, batch.ID, "sensitive", "documents")
	placeRow(t, s, batch.ID, "unresolved", "archives")

	_, result, err := pressFor(t, s, batch.ID, keyOne, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Filed != 4 || result.Failed != 0 || result.Stale != 0 {
		t.Fatalf("result: %+v", result)
	}
	for _, placed := range [][]string{
		{"passport-a.pdf", "Filed", "Documents"}, {"passport-b.pdf", "Filed", "Documents"},
		{"mystery.qqq", "Filed", "Archives"}, {"one.png", "Filed", "Images"},
	} {
		if exists(t, root, placed[0]) || !exists(t, root, append(placed[1:], placed[0])...) {
			t.Fatalf("%s should have moved to %v", placed[0], placed[1:])
		}
	}
}

// An older folder-level answer must not move a file that newly needs review: only
// the person's own answers (for the file, or for a question row) count.
func TestQuestionRow_AFolderLevelSuggestionStillDoesNotMoveAFileThatNeedsReview(t *testing.T) {
	s, batch, candidates, _ := planFixture(t, "one.png")
	editDraft(t, s, batch.ID, "select_group", func(r *ReviewDraftEdit) { r.GroupID = reviewGroupID("filing-images") })
	editDraft(t, s, batch.ID, "set_category", func(r *ReviewDraftEdit) {
		r.GroupID, r.Category = reviewGroupID("filing-images"), "documents"
	})
	if _, err := s.store.UpdateScanState("ws-1", func(state *ScanState) error {
		for i := range state.Candidates {
			if state.Candidates[i].ID == candidates[0].ID {
				state.Candidates[i].NeedsReview = true
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := s.ReadReviewDraft(t.Context(), "local", "ws-1", batch.ID)
	if err != nil || len(view.Choices) != 0 {
		t.Fatalf("a folder-level answer carried a file that needs review: %+v %v", view, err)
	}
}

func candidateState(t *testing.T, s *Service, name string) JanitorCandidate {
	t.Helper()
	state, err := s.store.LoadScanState("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range state.Candidates {
		if candidate.Name == name {
			return candidate
		}
	}
	t.Fatalf("no candidate named %s", name)
	return JanitorCandidate{}
}

// Choosing another folder for a row changes the row's own files and nothing the
// person chose for a single file.
func TestQuestionRow_ChoosingAnotherFolderRewritesOnlyTheRowsOwnChoices(t *testing.T) {
	s, batch, candidates, _ := planFixture(t, "passport-a.pdf", "passport-b.pdf", "tax-return.pdf")
	handPlaced := candidateNamed(t, candidates, "passport-b.pdf")
	editDraft(t, s, batch.ID, "select_files", func(r *ReviewDraftEdit) {
		r.CandidateIDs, r.Category = []string{handPlaced.ID}, "images"
	})
	editDraft(t, s, batch.ID, "mark_trash", func(r *ReviewDraftEdit) {
		r.CandidateIDs = []string{candidateNamed(t, candidates, "tax-return.pdf").ID}
	})
	placeRow(t, s, batch.ID, "sensitive", "documents")
	moved := placeRow(t, s, batch.ID, "sensitive", "archives")

	byID := choiceIDs(moved)
	if choice := byID[candidateNamed(t, candidates, "passport-a.pdf").ID]; choice.Category != CategoryArchives || choice.SourceGroupID != reviewGroupID("sensitive") {
		t.Fatalf("the row's own file should follow the new choice: %+v", choice)
	}
	if choice := byID[handPlaced.ID]; choice.Category != CategoryImages || choice.SourceGroupID != "" {
		t.Fatalf("a file the person placed by hand must keep its own folder: %+v", choice)
	}
	if choice := byID[candidateNamed(t, candidates, "tax-return.pdf").ID]; choice.Operation != OperationTrash {
		t.Fatalf("the Trash mark must stay: %+v", choice)
	}
}

// "Leave here" remembers nothing, so the folder the row recorded on its files goes
// with it: an old row answer must not later count as the person's answer.
func TestQuestionRow_LeaveHereForgetsTheRowsDestination(t *testing.T) {
	s, batch, _, _ := planFixture(t, "passport-a.pdf", "passport-b.pdf")
	placeRow(t, s, batch.ID, "sensitive", "documents")
	if got := candidateState(t, s, "passport-a.pdf"); got.DecisionCategory != CategoryDocuments || got.DecisionGroupID != reviewGroupID("sensitive") {
		t.Fatalf("placing the row records its destination: %+v", got)
	}
	editDraft(t, s, batch.ID, "clear_group", func(r *ReviewDraftEdit) { r.GroupID = reviewGroupID("sensitive") })
	for _, name := range []string{"passport-a.pdf", "passport-b.pdf"} {
		got := candidateState(t, s, name)
		if got.DecisionCategory != "" || got.DecisionGroupID != "" || got.Decision != DecisionNone || !got.DecidedAt.IsZero() || got.State != CandidatePending {
			t.Fatalf("%s should be waiting with nothing remembered: %+v", name, got)
		}
	}
}

// "Leave here, stop asking" covers the files still drawn in the row. A file with a
// Trash mark or a choice of its own is drawn elsewhere and is left as it is.
func TestQuestionRow_StopAskingSkipsFilesDrawnElsewhere(t *testing.T) {
	s, batch, candidates, _ := planFixture(t, "passport-a.pdf", "passport-b.pdf", "tax-return.pdf")
	editDraft(t, s, batch.ID, "mark_trash", func(r *ReviewDraftEdit) {
		r.CandidateIDs = []string{candidateNamed(t, candidates, "tax-return.pdf").ID}
	})
	editDraft(t, s, batch.ID, "select_files", func(r *ReviewDraftEdit) {
		r.CandidateIDs, r.Category = []string{candidateNamed(t, candidates, "passport-b.pdf").ID}, "documents"
	})
	view := editDraft(t, s, batch.ID, "keep_group", func(r *ReviewDraftEdit) { r.GroupID = reviewGroupID("sensitive") })

	if got := candidateState(t, s, "passport-a.pdf"); got.State != CandidateSkipped {
		t.Fatalf("the row's own file should be kept here for good: %+v", got)
	}
	for _, name := range []string{"passport-b.pdf", "tax-return.pdf"} {
		if got := candidateState(t, s, name); got.State != CandidatePending {
			t.Fatalf("%s is drawn elsewhere and must stay waiting: %+v", name, got)
		}
	}
	got := tickedNames(view, candidates)
	if len(got) != 2 || !got["passport-b.pdf"] || !got["tax-return.pdf"] {
		t.Fatalf("the other files keep their choices, got %v", got)
	}
}

// The row's menu reports a file placed one at a time as placed in the row, so the
// row's "Leave here" has to take it back too, or the menu would say Leave here while
// the file stayed in its folder.
func TestQuestionRow_LeaveHereAlsoTakesBackFilesPlacedOneAtATime(t *testing.T) {
	s, batch, candidates, _ := planFixture(t, "passport-a.pdf", "passport-b.pdf")
	handPlaced := candidateNamed(t, candidates, "passport-b.pdf")
	editDraft(t, s, batch.ID, "select_files", func(r *ReviewDraftEdit) {
		r.CandidateIDs, r.Category = []string{handPlaced.ID}, "archives"
	})
	placeRow(t, s, batch.ID, "sensitive", "documents")
	cleared := editDraft(t, s, batch.ID, "clear_group", func(r *ReviewDraftEdit) { r.GroupID = reviewGroupID("sensitive") })
	if len(cleared.Choices) != 0 {
		t.Fatalf("both files should be waiting again, got %v", tickedNames(cleared, candidates))
	}
	got := candidateState(t, s, "passport-b.pdf")
	if got.DecisionCategory != "" || got.Decision != DecisionNone || got.State != CandidatePending {
		t.Fatalf("a placement made one file at a time must not be remembered either: %+v", got)
	}
}

// Leaving a row where it was and starting over: clear_group keeps a Trash mark.
func TestQuestionRow_LeaveHereRemovesOnlyTheRowsChoices(t *testing.T) {
	s, batch, candidates, _ := planFixture(t, "passport-a.pdf", "passport-b.pdf", "tax-return.pdf")
	editDraft(t, s, batch.ID, "mark_trash", func(r *ReviewDraftEdit) {
		r.CandidateIDs = []string{candidateNamed(t, candidates, "tax-return.pdf").ID}
	})
	placeRow(t, s, batch.ID, "sensitive", "documents")
	cleared := editDraft(t, s, batch.ID, "clear_group", func(r *ReviewDraftEdit) { r.GroupID = reviewGroupID("sensitive") })
	got := tickedNames(cleared, candidates)
	if len(got) != 1 || !got["tax-return.pdf"] {
		t.Fatalf("expected only the Trash mark to remain, got %v", got)
	}
	if choice := choiceIDs(cleared)[candidateNamed(t, candidates, "tax-return.pdf").ID]; choice.Operation != OperationTrash {
		t.Fatalf("the Trash mark was lost: %+v", choice)
	}
}
