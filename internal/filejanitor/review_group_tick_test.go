package filejanitor

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// changeOnDisk makes a file differ from the one that was scanned.
func changeOnDisk(t *testing.T, root, name string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(root, name), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(" changed"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func tickedNames(view ReviewDraftView, candidates []JanitorCandidate) map[string]bool {
	names := map[string]string{}
	for _, c := range candidates {
		names[c.ID] = c.Name
	}
	out := map[string]bool{}
	for _, choice := range view.Choices {
		out[names[choice.CandidateID]] = true
	}
	return out
}

// A folder with one changed file can still be ticked: the changed file stays
// unticked and everything else is ticked. Refusing the whole folder because of one
// file left the person with a tick that never worked.
func TestReviewDraftEdits_TickingAFolderLeavesAChangedFileUnticked(t *testing.T) {
	s, batch, candidates, root := planFixture(t, "a.png", "b.png", "c.png")
	// The folder opens ticked; unticking it stores a draft with nothing chosen.
	editDraft(t, s, batch.ID, "deselect_group", func(r *ReviewDraftEdit) { r.GroupID = reviewGroupID("filing-images") })
	changeOnDisk(t, root, "b.png")

	view := editDraft(t, s, batch.ID, "select_group", func(r *ReviewDraftEdit) { r.GroupID = reviewGroupID("filing-images") })
	got := tickedNames(view, candidates)
	if len(got) != 2 || !got["a.png"] || !got["c.png"] || got["b.png"] {
		t.Fatalf("expected a.png and c.png ticked and b.png left alone, got %v", got)
	}
}

// When nothing in the folder could be ticked because every file changed, the tick
// says why instead of silently doing nothing, and stores nothing.
func TestReviewDraftEdits_TickingAFolderOfOnlyChangedFilesIsRefusedWithAReason(t *testing.T) {
	s, batch, _, root := planFixture(t, "a.png", "b.png")
	editDraft(t, s, batch.ID, "deselect_group", func(r *ReviewDraftEdit) { r.GroupID = reviewGroupID("filing-images") })
	changeOnDisk(t, root, "a.png")
	changeOnDisk(t, root, "b.png")
	before, err := s.ReadReviewDraft(t.Context(), "local", "ws-1", batch.ID)
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.EditReviewDraft("local", "ws-1", batch.ID, ReviewDraftEdit{
		ExpectedRevision: before.Revision, ExpectedGroupRevision: before.GroupRevision,
		Action: "select_group", GroupID: reviewGroupID("filing-images"),
	})
	if !errors.Is(err, ErrCandidateNotActionable) || !errors.Is(err, errMemberChanged) {
		t.Fatalf("expected the changed-since-the-scan refusal, got %v", err)
	}
	after, err := s.ReadReviewDraft(t.Context(), "local", "ws-1", batch.ID)
	if err != nil || after.Revision != before.Revision || len(after.Choices) != 0 {
		t.Fatalf("a refused tick must store nothing: %v %+v", err, after)
	}
}

// Selecting every filing folder at once is a convenience, not a promise about each
// file: a changed file in one folder never blocks the others.
func TestReviewDraftEdits_SelectAllFilingGroupsSkipsChangedFiles(t *testing.T) {
	s, batch, candidates, root := planFixture(t, "a.png", "b.png", "c.pdf")
	editDraft(t, s, batch.ID, "clear_all", nil)
	changeOnDisk(t, root, "a.png")
	changeOnDisk(t, root, "b.png")

	view := editDraft(t, s, batch.ID, "select_filing_groups", nil)
	got := tickedNames(view, candidates)
	if len(got) != 1 || !got["c.pdf"] {
		t.Fatalf("expected only the unchanged c.pdf ticked, got %v", got)
	}
}
