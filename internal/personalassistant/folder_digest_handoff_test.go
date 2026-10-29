package personalassistant

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

type fakeHandoffVerifier struct {
	folder string
	calls  int
	refuse bool
}

func (v *fakeHandoffVerifier) VerifiedHandoff(_ context.Context, userID, offerID, homeID, folderPath string, after time.Time) (FolderCreateResult, error) {
	v.calls++
	want, _ := filepath.EvalSymlinks(v.folder)
	got, _ := filepath.EvalSymlinks(folderPath)
	if v.refuse || userID != "local" || offerID == "" || homeID != "home-1" || got == "" || got != want || after.IsZero() {
		return FolderCreateResult{}, ErrFolderWorkspaceRefused
	}
	return FolderCreateResult{WorkspaceID: "song-5", Route: "/workspaces/desktop", HomeRoute: "/workspaces/music-home/assistant#projectLibraryPanel",
		Receipt:  []FolderReceiptRow{{Kind: "workspace", Name: "Desktop", Route: "/workspaces/desktop"}},
		OpenedBy: "Portfolio Manager"}, nil
}

// handoffCardFixture is the one-card fixture whose plan is a single song its
// Home's Portfolio Manager opens, and whose run finishes with the Home's ID.
func handoffCardFixture(t *testing.T) (*oneCardFixture, *fakeHandoffVerifier) {
	t.Helper()
	f := newOneCardFixture(t)
	f.setup.intent = FolderSetupIntent{Portfolio: true, Placement: "grouped", OpensProjects: []string{"Desktop"}}
	f.setup.finish = func() FolderSetupUpdate {
		return FolderSetupUpdate{Status: FolderSetupDone, RunID: "new-run", HomeID: "home-1"}
	}
	verifier := &fakeHandoffVerifier{folder: f.folder}
	f.service.deps.Handoff = verifier
	current, err := f.service.Current(context.Background(), "local")
	if err != nil || current.Offer == nil || current.Offer.Plan == nil {
		t.Fatalf("current = %+v %v", current.Offer, err)
	}
	f.offer = *current.Offer
	return f, verifier
}

// FR16: a single song's run that finished resolves to the song, proved by the
// host, as a project outcome that names who opened it; no plugin quest is read.
func TestFolderHandoff_ADoneRunResolvesToTheVerifiedSong(t *testing.T) {
	f, verifier := handoffCardFixture(t)
	if _, err := f.service.StartSetup(context.Background(), "local", f.offer.ID, FolderSetupInput{RequestID: "r1", PlanDigest: f.offer.Plan.Digest}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the song to resolve", func() bool { return f.stored(t).Status == FolderOfferResolved })
	stored := f.stored(t)
	outcome := stored.Outcome
	if outcome == nil || outcome.Kind != FolderChoiceProject || outcome.WorkspaceID != "song-5" || outcome.Route != "/workspaces/desktop" ||
		outcome.OpenedBy != "Portfolio Manager" || outcome.HomeRoute == "" || outcome.Blueprint != "reaper-song" {
		t.Fatalf("outcome = %+v", outcome)
	}
	if verifier.calls != 1 {
		t.Fatalf("verifier calls = %d", verifier.calls)
	}
	if journey := f.service.deps.Journey.(*fakeJourneyVerifier); journey.calls != 0 {
		t.Fatal("a single song's setup read a plugin quest run")
	}
	// A browser proof of any kind resolves only through the host's proof.
	view, err := f.service.Resolve(context.Background(), "local", f.offer.ID, FolderResolveInput{WorkspaceID: "someone-elses", RequestID: "late"})
	if err == nil && view.Outcome != nil && view.Outcome.WorkspaceID != "song-5" {
		t.Fatalf("a browser-named workspace replaced the song: %+v", view.Outcome)
	}
}

// FR14/AC5: a run that stopped for a model after building its Home resumes
// with that Home: the resumed pass is handed the recorded Home ID, so the
// Home it made is found again rather than refused as a stranger's.
func TestFolderHandoff_AResumeKeepsTheHomeTheRunBuilt(t *testing.T) {
	f, _ := handoffCardFixture(t)
	f.setup.finish = func() FolderSetupUpdate {
		return FolderSetupUpdate{Status: FolderSetupStopped, StopReason: FolderStopNeedsModel, RunID: "new-run", HomeID: "home-1"}
	}
	ctx := context.Background()
	if _, err := f.service.StartSetup(ctx, "local", f.offer.ID, FolderSetupInput{RequestID: "r1", PlanDigest: f.offer.Plan.Digest}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the run to stop", func() bool {
		stored := f.stored(t)
		return stored.Setup != nil && stored.Setup.Status == FolderSetupStopped && stored.Setup.HomeID == "home-1" &&
			!f.service.isRunning(f.offer.ID)
	})
	f.setup.finish = func() FolderSetupUpdate {
		return FolderSetupUpdate{Status: FolderSetupDone, RunID: "new-run", HomeID: "home-1"}
	}
	if _, err := f.service.StartSetup(ctx, "local", f.offer.ID, FolderSetupInput{RequestID: "r2", PlanDigest: f.offer.Plan.Digest}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the resumed run", func() bool { return f.setup.runCount() == 2 })
	f.setup.mu.Lock()
	resumed := f.setup.ran[1]
	f.setup.mu.Unlock()
	if resumed.Offer.Setup == nil || resumed.Offer.Setup.HomeID != "home-1" || resumed.Offer.Setup.RunID != "new-run" {
		t.Fatalf("the resumed pass lost its Home: %+v", resumed.Offer.Setup)
	}
	if id, err := f.service.SetupHomeIdentity(ctx, "local", f.offer.ID, f.offer.Plan.Digest); err != nil || id != "home-1" {
		t.Fatalf("the Home identity on resume = %q %v", id, err)
	}
}

// A run whose song cannot be proved stops failed and records no outcome.
func TestFolderHandoff_AnUnprovedSongIsNotRecorded(t *testing.T) {
	f, verifier := handoffCardFixture(t)
	verifier.refuse = true
	if _, err := f.service.StartSetup(context.Background(), "local", f.offer.ID, FolderSetupInput{RequestID: "r1", PlanDigest: f.offer.Plan.Digest}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the run to stop", func() bool {
		stored := f.stored(t)
		return stored.Setup != nil && stored.Setup.Status == FolderSetupStopped
	})
	stored := f.stored(t)
	if (stored.Outcome != nil && stored.Outcome.WorkspaceID != "") || stored.Status == FolderOfferResolved || stored.Setup.StopReason != FolderStopFailed {
		t.Fatalf("stored = %+v setup = %+v", stored.Outcome, stored.Setup)
	}
	// Browser proofs cannot resolve it either.
	for _, input := range []FolderResolveInput{{WorkspaceID: "song-5", RequestID: "w"}, {HomeID: "home-1", RequestID: "h"}} {
		if _, err := f.service.Resolve(context.Background(), "local", f.offer.ID, input); err == nil {
			t.Fatalf("browser proof %+v resolved an unproved song", input)
		}
	}
	if _, err := f.service.ResolveJourney(context.Background(), "local", f.offer.ID, FolderJourneyInput{RunID: "new-run", RequestID: "j"}); err == nil {
		t.Fatal("a journey run resolved a single song's setup")
	}
}
