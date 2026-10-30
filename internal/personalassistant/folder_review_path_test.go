package personalassistant

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/folderdigest"
)

func TestFolderReviewPath_RestartAndExpiryRestoreOnlyReviewAuthority(t *testing.T) {
	for _, status := range []FolderOfferStatus{FolderOfferPending, FolderOfferAwaitingOutcome} {
		for _, restart := range []bool{false, true} {
			name := string(status) + "/expiry"
			if restart {
				name = string(status) + "/restart"
			}
			t.Run(name, func(t *testing.T) {
				f, observations, target, id, creator, _ := reviewedObservation(t, "documents", "Scans")
				ctx := context.Background()
				doc, err := f.store.Mutate(ctx, "local", func(doc *FolderDigestDocument) error { doc.Offer(id).Status = status; return nil })
				if err != nil {
					t.Fatal(err)
				}
				offer := *doc.Offer(id)
				original := observations.selections[offer.ConversationReview.ObservationID].observation
				if restart {
					f.service = f.newService()
					f.service.deps.Creator = creator
					observations = NewFolderObservationService(f.service)
					observations.ConfigureConversationReviews(func(context.Context, FolderOffer) error { return nil }, func(foldercontext.Target) (func(), bool) { return func() {}, true })
				} else {
					f.now = f.now.Add(31 * time.Minute)
				}
				f.service.deps.ScanObservation = func(string) (result folderdigest.Result, err error) { t.Fatal("restoration rescanned"); return }
				view, err := observations.ReadReview(ctx, target, id)
				if err != nil || view.NeedsPick || view.ID != id {
					t.Fatalf("review not restored: %+v %v", view, err)
				}
				root, err := observations.ResolveRoot(ctx, target, original.ID)
				if err != nil || root != filepath.Join(f.home, "Documents") {
					t.Fatalf("root unavailable: %v", err)
				}
				if _, err := observations.Resolve(ctx, target, original.ID); err == nil {
					t.Fatal("review hold became discussion evidence")
				}
				if observations.Status(ctx, target, original) == "" {
					t.Fatal("snapshot became fresh")
				}
				held := observations.selections[original.ID]
				if held.reheld == nil || held.observation.Tree != nil || len(held.result.Candidates) != 0 {
					t.Fatal("reconstructed scan")
				}
				if _, found := observedReviewCandidate(held, "candidate-999"); found {
					t.Fatal("another candidate authorized")
				}
				if status == FolderOfferPending {
					reviewed, err := observations.Review(ctx, target, target.ConversationID, original.ID, offer.ConversationReview.CandidateID)
					if err != nil || reviewed.ID != id {
						t.Fatalf("Review again lost the review: %v", err)
					}
					if _, err := f.service.Decide(ctx, "local", id, FolderDecisionInput{Decision: FolderDecisionYes, Create: true, RequestID: "restored", ReviewDigest: view.ReviewDigest}); err != nil {
						t.Fatal(err)
					}
					if len(creator.requests) != 1 {
						t.Fatal("restored review did not create")
					}
				}
			})
		}
	}
}

func TestFolderReviewPath_RejectsMovedReplacedAndLegacyPaths(t *testing.T) {
	for _, scenario := range []string{"root-moved", "child-moved", "root-replaced", "child-replaced", "legacy"} {
		t.Run(scenario, func(t *testing.T) {
			f, observations, target, id, _, _ := reviewedObservation(t, "documents", "Scans")
			doc, err := f.store.Read(t.Context(), "local")
			if err != nil {
				t.Fatal(err)
			}
			review := doc.Offer(id).ConversationReview
			want := FolderContinuationMoved
			path := filepath.Join(f.home, "Documents")
			if scenario == "child-moved" || scenario == "child-replaced" {
				path = filepath.Join(path, "Scans")
			}
			if scenario == "legacy" {
				if err := os.Remove(filepath.Join(f.folder.path, ".ori", folderPathsFileName)); err != nil {
					t.Fatal(err)
				}
				want = FolderContinuationLost
			} else {
				// Vacate, don't remove: prevents Linux inode reuse from faking identity.
				if err := os.Rename(path, path+"-original"); err != nil {
					t.Fatal(err)
				}
				if scenario == "root-replaced" || scenario == "child-replaced" {
					if err := os.Mkdir(path, 0750); err != nil {
						t.Fatal(err)
					}
					want = FolderContinuationChanged
				}
			}
			observations.selections = make(map[string]heldFolderObservation)
			if _, reason, err := observations.resolve(t.Context(), target, review.ObservationID); err == nil || reason != want {
				t.Fatalf("got %s %v, want %s", reason, err, want)
			}
			view, err := observations.ReadReview(t.Context(), target, id)
			if err != nil || !view.NeedsPick || view.NeedsPickReason != want {
				t.Fatalf("view reason: %+v %v", view, err)
			}
			_, err = f.service.SubjectPath(t.Context(), "local", id)
			if FolderPickReason(err) != want {
				t.Fatalf("action reason: %s", FolderPickReason(err))
			}
		})
	}
}

func TestFolderReviewPath_SetupAndRetryAfterRestart(t *testing.T) {
	f, _, target, id, _, _ := reviewedObservation(t, "documents", "Album")
	setup := &fakeFolderSetup{lines: oneCardLines(), finish: func() FolderSetupUpdate {
		return FolderSetupUpdate{Status: FolderSetupStopped, StopReason: FolderStopNeedsPick}
	}}
	f.service.deps.Setup = setup
	f.service = func() *FolderDigestService {
		restarted := f.newService()
		restarted.deps.Setup = setup
		return restarted
	}()
	observations := NewFolderObservationService(f.service)
	observations.ConfigureConversationReviews(func(context.Context, FolderOffer) error { return nil }, func(foldercontext.Target) (func(), bool) { return func() {}, true })
	view, err := observations.ReadReview(t.Context(), target, id)
	if err != nil || view.Plan == nil || view.NeedsPick {
		t.Fatalf("restored plan: %+v %v", view, err)
	}
	if _, err := f.service.StartSetup(t.Context(), "local", id, FolderSetupInput{RequestID: "start", PlanDigest: view.Plan.Digest}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "stopped setup", func() bool {
		stored, err := f.service.StoredOffer(t.Context(), "local", id)
		return err == nil && stored.Setup != nil && stored.Setup.Status == FolderSetupStopped && !f.service.isRunning(id)
	})
	// Reconstruct an awaiting run on a second restart, including its Retry path.
	f.service = f.newService()
	f.service.deps.Setup = setup
	observations = NewFolderObservationService(f.service)
	observations.ConfigureConversationReviews(func(context.Context, FolderOffer) error { return nil }, func(foldercontext.Target) (func(), bool) { return func() {}, true })
	view, err = observations.ReadReview(t.Context(), target, id)
	if err != nil || view.NeedsPick || view.Setup == nil || view.Setup.StopReason != FolderStopInterrupted {
		t.Fatalf("restored stopped run: %+v %v", view, err)
	}
	if _, err := f.service.StartSetup(t.Context(), "local", id, FolderSetupInput{RequestID: "retry", PlanDigest: view.Setup.PlanDigest}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "retried setup", func() bool { return setup.runCount() == 2 && !f.service.isRunning(id) })
}

func TestFolderReviewPath_NeverReplacesAnotherOwnersSelection(t *testing.T) {
	f, observations, target, id, _, _ := reviewedObservation(t, "documents", "Scans")
	doc, err := f.store.Read(t.Context(), "local")
	if err != nil {
		t.Fatal(err)
	}
	observationID := doc.Offer(id).ConversationReview.ObservationID
	held := observations.selections[observationID]
	held.target.ConversationID = "other-owner"
	observations.selections[observationID] = held
	if _, _, err := observations.resolve(t.Context(), target, observationID); err == nil {
		t.Fatal("stole another owner's hold")
	}
	if observations.selections[observationID].target != held.target {
		t.Fatal("overwrote another owner")
	}
}
