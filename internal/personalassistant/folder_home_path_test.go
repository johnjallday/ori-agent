package personalassistant

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestFolderHomePath_RestartAndRenamePreserveOpenReview(t *testing.T) {
	for _, status := range []FolderOfferStatus{FolderOfferPending, FolderOfferAwaitingOutcome} {
		for _, renamed := range []bool{false, true} {
			name := string(status)
			if renamed {
				name += "/renamed"
			}
			t.Run(name, func(t *testing.T) {
				f := newFolderDigestFixture(t)
				root := filepath.Join(f.home, "Documents", "Thesis")
				first, err := f.service.scanRoot(t.Context(), "local", root, "")
				if err != nil {
					t.Fatal(err)
				}
				doc, err := f.store.Mutate(t.Context(), "local", func(doc *FolderDigestDocument) error {
					doc.Offer(first.ID).Status = status
					doc.Offer(first.ID).EntryName = "main.tex"
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				before := *doc.Offer(first.ID)
				if before.RootIdentity == "" || before.SubjectIdentity == "" {
					t.Fatal("Home scan lacks witnesses")
				}
				f.service = f.newService()
				view, err := f.service.Current(t.Context(), "local")
				if err != nil || view.Offer == nil || view.Offer.ID != first.ID || view.Offer.NeedsPick {
					t.Fatalf("restart: %+v %v", view.Offer, err)
				}
				if renamed {
					if err := os.Rename(root, root+"-renamed"); err != nil {
						t.Fatal(err)
					}
					root += "-renamed"
					view, err = f.service.Current(t.Context(), "local")
					if err != nil || !view.Offer.NeedsPick || view.Offer.NeedsPickReason != FolderContinuationMoved {
						t.Fatalf("moved: %+v %v", view.Offer, err)
					}
				}
				resumed, err := f.service.scanRoot(t.Context(), "local", root, "")
				if err != nil || resumed.ID != first.ID || resumed.NeedsPick {
					t.Fatalf("re-pick: %+v %v", resumed, err)
				}
				after, _ := f.service.StoredOffer(t.Context(), "local", first.ID)
				before.FolderKey, before.Subject.Key = FolderKey(root), FolderKey(root)
				if !reflect.DeepEqual(before, after) {
					t.Fatal("Home re-pick reset choices/run")
				}
				f.service = f.newService()
				if path, err := f.service.SubjectPath(t.Context(), "local", first.ID); err != nil || path != root {
					t.Fatal("relocated restart lost folder", err)
				}
				encoded, _ := json.Marshal(resumed)
				if strings.Contains(string(encoded), f.home) {
					t.Fatal("private path leaked")
				}
			})
		}
	}
}

func TestFolderHomePath_ReplacementCannotInheritAndAnotherFolderCanOpen(t *testing.T) {
	f := newFolderDigestFixture(t)
	root := filepath.Join(f.home, "Documents", "Thesis")
	first, err := f.service.scanRoot(t.Context(), "local", root, "")
	if err != nil {
		t.Fatal(err)
	}
	before, _ := f.store.Read(t.Context(), "local")
	if err := os.Rename(root, root+"-original"); err != nil {
		t.Fatal(err)
	}
	writeFolderFiles(t, root, "main.tex")
	if _, err := f.service.scanRoot(t.Context(), "local", root, ""); !errors.Is(err, ErrFolderPathLost) || FolderPickReason(err) != FolderContinuationChanged {
		t.Fatal("replacement adopted", err)
	}
	after, _ := f.store.Read(t.Context(), "local")
	if !reflect.DeepEqual(before, after) {
		t.Fatal("refusal changed original review")
	}
	next, err := f.service.scanRoot(t.Context(), "local", filepath.Join(f.home, "Documents", "Album"), "")
	if err != nil || next.ID == first.ID {
		t.Fatalf("different folder: %+v %v", next, err)
	}
	old, _ := f.service.StoredOffer(t.Context(), "local", first.ID)
	if old.Status != FolderOfferLater {
		t.Fatal("old Home review not retired")
	}
}

func TestFolderHomePath_DoesNotStealConversationReview(t *testing.T) {
	f, _, _, id, _, _ := reviewedObservation(t, "documents", "Album")
	if _, err := f.service.ScanChip(t.Context(), "local", "documents"); !errors.Is(err, ErrFolderReviewPending) {
		t.Fatal("Home replaced conversation review", err)
	}
	stored, _ := f.service.StoredOffer(t.Context(), "local", id)
	if stored.Status != FolderOfferPending {
		t.Fatal("conversation review changed")
	}
}
