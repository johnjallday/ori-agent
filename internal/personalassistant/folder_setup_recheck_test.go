package personalassistant

import "testing"

type recheckSetup struct {
	*fakeFolderSetup
	invalidations int
}

func (s *recheckSetup) InvalidateSetupReadiness(string) { s.invalidations++ }

func TestFolderRecheckInvalidatesOnceWithoutChangingAcceptedWork(t *testing.T) {
	fx := newBuildSetupFixture(t)
	setup := &recheckSetup{fakeFolderSetup: fx.setup}
	fx.folderDigestFixture.service.deps.Setup = setup
	view, err := fx.service.ReviewBuild(t.Context(), fx.draft, "session-1", fx.observation.ID, fx.candidate, "Session")
	if err != nil || view.Plan == nil {
		t.Fatalf("review: %+v %v", view, err)
	}
	offer, err := fx.service.digest.StoredOffer(t.Context(), "local", view.ID)
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithFolderSetupRecheck(t.Context())
	fx.service.recheckSetupFacts(ctx, fx.review, offer)
	fx.service.recheckSetupFacts(ctx, fx.review, offer)
	if setup.invalidations != 1 || setup.runs != 0 {
		t.Fatal("recheck repeated or executed setup")
	}
	if _, err := fx.service.StartBuildSetup(t.Context(), "local", view.ID, view.Plan.Digest, "accepted", "", ""); err != nil {
		t.Fatal(err)
	}
	accepted := fx.waitSettled(t)
	fx.service.recheckSetupFacts(WithFolderSetupRecheck(t.Context()), fx.review, accepted)
	stored, err := fx.service.digest.StoredOffer(t.Context(), "local", view.ID)
	if err != nil || setup.invalidations != 1 || stored.Setup.PlanDigest != accepted.Setup.PlanDigest || stored.Setup.RunID != accepted.Setup.RunID {
		t.Fatal("recheck refreshed accepted work", err)
	}
}
