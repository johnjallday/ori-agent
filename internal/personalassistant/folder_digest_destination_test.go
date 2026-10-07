package personalassistant

import (
	"context"
	"errors"
	"testing"
)

func TestFolderSetupDestination_StaleIdentityDoesNotConfirmOrStart(t *testing.T) {
	f := newOneCardFixture(t)
	destination := FolderSetupDestination{Status: "existing", WorkspaceID: "home-a", Name: "Music Home", Kind: "home", OwnerUserID: "local", RecordVersion: 4}
	f.setup.destination = &destination
	shown := f.service.viewFor(context.Background(), "local", f.stored(t), false)
	if shown.Plan == nil || shown.Plan.Destination == nil {
		t.Fatal(shown)
	}
	// A same-name different Home cannot reuse the displayed confirmation.
	f.setup.destination = &FolderSetupDestination{Status: "existing", WorkspaceID: "home-b", Name: "Music Home", Kind: "home", OwnerUserID: "local", RecordVersion: 4}
	_, err := f.service.StartSetup(context.Background(), "local", f.offer.ID, FolderSetupInput{RequestID: "stale-home", PlanDigest: shown.Plan.Digest})
	if !errors.Is(err, ErrFolderPlanChanged) {
		t.Fatal("stale Home identity accepted", err)
	}
	doc, err := f.store.Read(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	if offer := doc.Offer(f.offer.ID); offer == nil || offer.Setup != nil || offer.Status != FolderOfferPending || doc.Receipt("stale-home") != nil || f.setup.runCount() != 0 {
		t.Fatal("rejected confirmation changed canonical review or ran setup", offer)
	}
}

func TestFolderSetupDestination_ResumeRevalidatesPinnedIdentity(t *testing.T) {
	f := newOneCardFixture(t)
	f.setup.destination = &FolderSetupDestination{Status: "existing", WorkspaceID: "home-a", Name: "Music Home", Kind: "home", OwnerUserID: "local", RecordVersion: 4}
	f.setup.finish = func() FolderSetupUpdate {
		return FolderSetupUpdate{Status: FolderSetupStopped, StopReason: FolderStopFailed, RunID: "run-1"}
	}
	shown := f.service.viewFor(context.Background(), "local", f.stored(t), false)
	if shown.Plan == nil {
		t.Fatal(shown)
	}
	_, err := f.service.StartSetup(context.Background(), "local", f.offer.ID, FolderSetupInput{RequestID: "confirm", PlanDigest: shown.Plan.Digest})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "stopped", func() bool { return !f.service.isRunning(f.offer.ID) })
	doc, err := f.store.Read(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	stored := doc.Offer(f.offer.ID)
	if stored.Setup == nil || stored.Setup.Intent.Destination == nil || stored.Setup.Intent.Destination.WorkspaceID != "home-a" {
		t.Fatal("run lost destination witness", stored)
	}
	f.setup.mu.Lock()
	f.setup.destinationErr = ErrFolderPlanChanged
	f.setup.mu.Unlock()
	_, err = f.service.StartSetup(context.Background(), "local", f.offer.ID, FolderSetupInput{RequestID: "resume", PlanDigest: shown.Plan.Digest})
	if !errors.Is(err, ErrFolderPlanChanged) {
		t.Fatal("resume ignored destination revocation", err)
	}
	if f.setup.runCount() != 1 || f.setup.destinationChecks != 1 {
		t.Fatal("resume ran setup or did not validate destination")
	}
	doc, err = f.store.Read(context.Background(), "local")
	if err != nil || doc.Receipt("resume") != nil || doc.Offer(f.offer.ID).Setup.Status != FolderSetupStopped {
		t.Fatal("failed resume wrote state", err)
	}
}
