package personalassistant

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
)

func TestFolderReviewRepick_PreservesReviewAcrossRootAndChildRename(t *testing.T) {
	for _, status := range []FolderOfferStatus{FolderOfferPending, FolderOfferAwaitingOutcome} {
		for _, move := range []string{"unchanged", "root", "child"} {
			t.Run(string(status)+"/"+move, func(t *testing.T) {
				f, service, target, id, _, _ := reviewedObservation(t, "documents", "Scans")
				doc, err := f.store.Mutate(t.Context(), "local", func(doc *FolderDigestDocument) error {
					offer := doc.Offer(id)
					offer.Status, offer.EntryName = status, "notes.md"
					offer.ConversationReview.WorkspaceName = "My chosen workspace"
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				before := *doc.Offer(id)
				root := filepath.Join(f.home, "Documents")
				child := filepath.Join(root, "Scans")
				if move == "root" {
					if err := os.Rename(root, root+"-renamed"); err != nil {
						t.Fatal(err)
					}
					root += "-renamed"
					child = filepath.Join(root, "Scans")
				} else if move == "child" {
					if err := os.Rename(child, child+"-renamed"); err != nil {
						t.Fatal(err)
					}
					child += "-renamed"
				}
				observed := observeSelected(t, f, service, target, root)
				view, err := service.ResumeReviewPick(t.Context(), target, id, observed.ID)
				if err != nil || view == nil || view.ID != id || view.NeedsPick {
					t.Fatalf("resume: %+v %v", view, err)
				}
				after, err := f.service.StoredOffer(t.Context(), "local", id)
				if err != nil {
					t.Fatal(err)
				}
				if after.FolderKey != FolderKey(root) || after.Subject.Key != FolderKey(child) {
					t.Fatal("keys not relocated")
				}
				before.FolderKey, before.Subject.Key, before.Subject.RelPath = after.FolderKey, after.Subject.Key, after.Subject.RelPath
				if !reflect.DeepEqual(before, after) {
					t.Fatal("re-pick replaced review choices or provenance")
				}
				if _, err := service.Resolve(t.Context(), target, before.ConversationReview.ObservationID); err == nil {
					t.Fatal("old snapshot became fresh")
				}
				// The relocated private paths also survive the next process restart.
				f.service = f.newService()
				service = NewFolderObservationService(f.service)
				service.ConfigureConversationReviews(func(context.Context, FolderOffer) error { return nil }, func(foldercontext.Target) (func(), bool) { return func() {}, true })
				view, err = service.ReadReview(t.Context(), target, id)
				if err != nil || view.NeedsPick {
					t.Fatalf("relocated restart: %+v %v", view, err)
				}
			})
		}
	}
}

func TestFolderReviewRepick_ReplacementAndForeignOwnerNeverInherit(t *testing.T) {
	for _, scenario := range []string{"root", "child", "foreign-owner", "retired", "different"} {
		t.Run(scenario, func(t *testing.T) {
			f, service, target, id, _, active := reviewedObservation(t, "documents", "Scans")
			root := filepath.Join(f.home, "Documents")
			switch scenario {
			case "root", "child":
				path := root
				if scenario == "child" {
					path = filepath.Join(root, "Scans")
				}
				if err := os.Rename(path, path+"-original"); err != nil {
					t.Fatal(err)
				}
				writeFolderFiles(t, path, "notes.md")
			case "foreign-owner":
				target.ConversationID = "other-conversation"
			case "retired":
				*active = ""
			case "different":
				root = filepath.Join(f.home, "Elsewhere")
				writeFolderFiles(t, root, "notes.md")
			}
			before, _ := f.store.Read(t.Context(), "local")
			observed := observeSelected(t, f, service, target, root)
			view, err := service.ResumeReviewPick(t.Context(), target, id, observed.ID)
			if view != nil {
				t.Fatal("adopted another folder/owner")
			}
			if scenario == "root" || scenario == "child" {
				if !errors.Is(err, ErrFolderPathLost) || FolderPickReason(err) != FolderContinuationChanged {
					t.Fatalf("replacement: %v", err)
				}
			}
			after, _ := f.store.Read(t.Context(), "local")
			if !reflect.DeepEqual(before, after) {
				t.Fatal("refusal mutated the old offer")
			}
		})
	}
}

func TestFolderReviewRepick_PreservesStoppedRunAndBrief(t *testing.T) {
	f, service, target, id, _, _ := reviewedObservation(t, "documents", "Album")
	setup := &fakeFolderSetup{lines: oneCardLines(), hire: &FolderHire{Mode: FolderHireCreate, AgentName: "Producer"}, briefDefault: "Understand this album", finish: func() FolderSetupUpdate {
		return FolderSetupUpdate{Status: FolderSetupStopped, StopReason: FolderStopNeedsModel}
	}}
	f.service.deps.Setup = setup
	view, err := service.ReadReview(t.Context(), target, id)
	if err != nil || view.Plan == nil {
		t.Fatal("missing plan", err)
	}
	_, err = f.service.StartSetup(t.Context(), "local", id, FolderSetupInput{RequestID: "start", PlanDigest: view.Plan.Digest, Brief: "Keep my arrangement"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "stopped run", func() bool {
		stored, _ := f.service.StoredOffer(t.Context(), "local", id)
		return stored.Setup != nil && stored.Setup.Status == FolderSetupStopped && !f.service.isRunning(id)
	})
	before, _ := f.service.StoredOffer(t.Context(), "local", id)
	root := filepath.Join(f.home, "Documents")
	if err := os.Rename(root, root+"-renamed"); err != nil {
		t.Fatal(err)
	}
	observed := observeSelected(t, f, service, target, root+"-renamed")
	view, err = service.ResumeReviewPick(t.Context(), target, id, observed.ID)
	if err != nil || view == nil || view.NeedsPick {
		t.Fatalf("resume: %+v %v", view, err)
	}
	after, _ := f.service.StoredOffer(t.Context(), "local", id)
	if !reflect.DeepEqual(before.Setup, after.Setup) || after.Setup.Brief != "Keep my arrangement" || setup.runCount() != 1 {
		t.Fatalf("run preserved=%v, brief=%q, runs=%d", reflect.DeepEqual(before.Setup, after.Setup), after.Setup.Brief, setup.runCount())
	}
}

func TestFolderReviewRepick_ExplicitReviewReturnsSameOfferWithoutNewObservationBinding(t *testing.T) {
	f, service, target, id, _, _ := reviewedObservation(t, "documents", "Scans")
	observed := observeSelected(t, f, service, target, filepath.Join(f.home, "Documents"))
	candidate := ""
	for _, project := range observed.Projects {
		if project.Name == "Scans" {
			candidate = project.ID
		}
	}
	view, err := service.Review(t.Context(), target, target.ConversationID, observed.ID, candidate)
	if err != nil || view.ID != id || view.NeedsPick {
		t.Fatalf("review: %+v %v", view, err)
	}
	doc, _ := f.store.Read(t.Context(), "local")
	if len(doc.Offers) != 1 || doc.Offer(id).ConversationReview.ObservationID == observed.ID {
		t.Fatal("replaced canonical review")
	}
}
