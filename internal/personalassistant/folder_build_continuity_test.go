package personalassistant

import (
	"context"
	"os"
	"testing"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
)

func TestFolderBuildContinuity_RestartAndRenameKeepCanonicalDraftAuthority(t *testing.T) {
	fx := newBuildSetupFixture(t)
	view, err := fx.service.ReviewBuild(t.Context(), fx.draft, "session-1", fx.observation.ID, fx.candidate, "My song")
	if err != nil {
		t.Fatal(err)
	}
	digest := fx.newService()
	digest.deps.Setup = fx.setup
	digest.deps.ScanObservation = fx.folderDigestFixture.service.deps.ScanObservation
	fx.folderDigestFixture.service = digest
	service := NewFolderObservationService(digest)
	alive := true
	service.ConfigureBuildReviews(func(_ context.Context, offer FolderOffer) error {
		if !alive || offer.ConversationReview.Target != fx.review || offer.ConversationReview.ObservationID != fx.observation.ID {
			return ErrBuildSetupNotOwned
		}
		return nil
	}, func(target foldercontext.Target) (func(), bool) { return func() {}, target == fx.review })
	assertReady := func(want string) {
		t.Helper()
		if root, err := service.ResolveRoot(t.Context(), fx.draft, fx.observation.ID); err != nil || root != want {
			t.Fatalf("draft Create root: %q %v", root, err)
		}
		if plan, err := service.BuildPlan(t.Context(), fx.draft, fx.observation.ID, fx.candidate, "My song"); err != nil || plan == nil || !plan.Ready {
			t.Fatalf("draft plan: %+v %v", plan, err)
		}
		if _, err := service.Resolve(t.Context(), fx.draft, fx.observation.ID); err == nil {
			t.Fatal("draft became fresh discussion evidence")
		}
	}
	assertReady(fx.song)
	if err := os.Rename(fx.song, fx.song+"-renamed"); err != nil {
		t.Fatal(err)
	}
	picked := observeSelected(t, fx.folderDigestFixture, service, fx.draft, fx.song+"-renamed")
	resumed, err := service.ResumeBuildFolderPick(t.Context(), fx.draft, "session-1", fx.observation.ID, picked.ID)
	if err != nil || resumed == nil || resumed.ID != view.ID || resumed.NeedsPick {
		t.Fatalf("build re-pick: %+v %v", resumed, err)
	}
	assertReady(fx.song + "-renamed")
	foreign := fx.draft
	foreign.DraftID = BuildReviewPrefix + "another-session"
	if _, err := service.ResolveRoot(t.Context(), foreign, fx.observation.ID); err == nil {
		t.Fatal("another draft borrowed the review")
	}
	alive = false
	if _, err := service.ResolveRoot(t.Context(), fx.draft, fx.observation.ID); err == nil {
		t.Fatal("discarded draft retained authority")
	}
	if err := service.CloseBuildFolderReviews(t.Context(), "local", "session-1", fx.observation.ID); err != nil {
		t.Fatal(err)
	}
	stored, _ := digest.StoredOffer(t.Context(), "local", view.ID)
	if stored.Status != FolderOfferClosed {
		t.Fatal("discarded draft kept its review")
	}
	if _, found, err := fx.store.readFolderPath(t.Context(), "local", stored); err != nil || found {
		t.Fatal("discarded draft kept its private path", err)
	}
}
