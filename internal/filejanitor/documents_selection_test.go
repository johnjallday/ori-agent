//go:build darwin || linux || freebsd

package filejanitor

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/johnjallday/ori-agent/internal/directoryid"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestDocumentsSelectionDoesNotAdoptOrChangeFilingDraft(t *testing.T) {
	s, b, candidates, root := planFixture(t, "estimate.pdf", "other.pdf", "trash.pdf")
	st, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	witness := directoryid.Witness(st)
	ls, err := workspace.ListContainedEntries(root, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]ReviewedLooseFile{}
	for _, e := range ls.Entries {
		if e.Kind == "file" {
			files[e.Name] = ReviewedLooseFile{e.Name, e.Version}
		}
	}
	check := func(want string, fs ...ReviewedLooseFile) {
		t.Helper()
		p, err := s.MatchDocumentsSelection(t.Context(), "local", "ws-1", root, witness, fs)
		if err != nil || p.State != want {
			t.Fatalf("%+v: %v", p, err)
		}
		if want == "matched" && (!reflect.DeepEqual(p.Names, []string{"estimate.pdf"}) || !reflect.DeepEqual(p.CandidateIDs, []string{candidateNamed(t, candidates, "estimate.pdf").ID})) {
			t.Fatalf("unselected candidate in %+v", p)
		}
	}
	original := readPlan(t, s, b.ID, ReviewPlanQuery{})
	check("matched", files["estimate.pdf"])
	if got := readPlan(t, s, b.ID, ReviewPlanQuery{}); !reflect.DeepEqual(got, original) {
		t.Fatal("default Filing intent changed")
	}
	editDraft(t, s, b.ID, "mark_trash", func(e *ReviewDraftEdit) { e.CandidateIDs = []string{candidateNamed(t, candidates, "trash.pdf").ID} })
	before := readPlan(t, s, b.ID, ReviewPlanQuery{})
	check("matched", files["estimate.pdf"])
	check("changed", files["estimate.pdf"], files["trash.pdf"])
	if after := readPlan(t, s, b.ID, ReviewPlanQuery{}); !reflect.DeepEqual(before, after) {
		t.Fatal("Filing edits changed")
	}
	f := files["estimate.pdf"]
	check("changed", f, f)
	nested := f
	nested.Name = "Examples/estimate.pdf"
	check("changed", nested)
	if err := os.Rename(filepath.Join(root, f.Name), filepath.Join(root, "moved.pdf")); err != nil {
		t.Fatal(err)
	}
	check("changed", f)
	if _, err := s.MatchDocumentsSelection(t.Context(), "foreign", "ws-1", root, witness, []ReviewedLooseFile{f}); err == nil {
		t.Fatal("foreign workspace was queried")
	}
	if _, err := s.MatchDocumentsSelection(t.Context(), "local", "ws-1", root, "different physical root", []ReviewedLooseFile{f}); err == nil {
		t.Fatal("root replacement admitted")
	}
}
