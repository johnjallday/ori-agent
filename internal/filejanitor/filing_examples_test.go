//go:build darwin || linux

package filejanitor

import (
	"github.com/johnjallday/ori-agent/internal/workspace"
	"os"
	"path/filepath"
	"testing"
)

func TestMatchingExamplesDoNotChooseBetweenDifferentDestinations(t *testing.T) {
	e := savedExample{a: FileAction{ID: "a", SourceName: "hotel-receipt.pdf", Delivery: &DeliveredTo{Key: "first"}}, words: []string{"hotel", "receipt"}, intact: true}
	other := e
	other.a.ID = "b"
	other.a.Delivery = &DeliveredTo{Key: "second"}
	if _, ok := matchExample([]savedExample{e, other}, "hotel-receipt-may.pdf"); ok {
		t.Fatal("ambiguous destination suggested")
	}
	if _, ok := matchExample([]savedExample{e}, "signed-lease.pdf"); ok {
		t.Fatal("extension alone suggested a folder")
	}
}

func TestExternalExampleRequiresOptInExactIdentityAndSeparatePress(t *testing.T) {
	s, b, cs, root := planFixture(t, "hotel-receipt.pdf")
	s.SetExampleMemory(workspace.NewMemoryStore(s.store.resolver))
	p, _ := s.ReadReviewPicture(t.Context(), "local", "ws-1", b.ID)
	sel := ChosenSelection{Revision: p.Revision, DraftRevision: p.DraftRevision, CandidateID: cs[0].ID}
	dest, _ := filepath.EvalSymlinks(t.TempDir())
	prev, _ := s.PreviewChosenFolder(t.Context(), "local", "ws-1", b.ID, dest, sel)
	r, err := s.FileChosenFolder(t.Context(), "local", "ws-1", b.ID, dest, sel, prev.Digest, keyOne)
	if err != nil {
		t.Fatal(err)
	}
	a := r.Outcomes[0].ActionID
	rc, err := s.ReadFiling(t.Context(), "local", "ws-1", r.FilingID, 0)
	if err != nil || len(rc.ExampleOffers) != 1 {
		t.Fatal(rc, err)
	}
	agedFile(t, root, "hotel-receipt-may.pdf", 100)
	agedFile(t, root, "signed-lease.pdf", 120)
	batch, created, err := s.ScanNow("ws-1", ScanSourceManual)
	if !created || err != nil {
		t.Fatal(batch, err)
	}
	pic := func() ReviewPicture {
		v, e := s.ReadReviewPicture(t.Context(), "local", "ws-1", batch.ID)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	if len(pic().FolderSuggestions) != 0 {
		t.Fatal("learned without opt-in")
	}
	if err = s.EditFilingExample("local", "ws-1", a, false); err != nil {
		t.Fatal(err)
	}
	p = pic()
	if len(p.FolderSuggestions) != 1 || p.FolderSuggestions[0].CandidateName != "hotel-receipt-may.pdf" || !exists(t, root, "signed-lease.pdf") || !exists(t, root, "hotel-receipt-may.pdf") {
		t.Fatal(p.FolderSuggestions)
	}
	sel = ChosenSelection{Revision: p.Revision, DraftRevision: p.DraftRevision, CandidateID: p.FolderSuggestions[0].CandidateID, Example: a}
	prev, err = s.PreviewChosenFolder(t.Context(), "local", "ws-1", batch.ID, dest, sel)
	if err != nil || prev.Digest == "" {
		t.Fatal(prev, err)
	}
	if err = os.Rename(dest, dest+"-moved"); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(dest, 0700); err != nil {
		t.Fatal(err)
	}
	if len(pic().FolderSuggestions) != 0 {
		t.Fatal("suggested replacement")
	}
	if err = os.Remove(dest); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(dest+"-moved", dest); err != nil {
		t.Fatal(err)
	}
	if err = s.EditFilingExample("local", "ws-1", a, true); err != nil {
		t.Fatal(err)
	}
	if len(pic().FolderSuggestions) != 0 {
		t.Fatal("forgotten example returned")
	}
	if _, err = s.FileChosenFolder(t.Context(), "local", "ws-1", batch.ID, dest, sel, prev.Digest, keyTwo); err == nil || !exists(t, root, "hotel-receipt-may.pdf") {
		t.Fatal("spent forgotten suggestion", err)
	}
}
