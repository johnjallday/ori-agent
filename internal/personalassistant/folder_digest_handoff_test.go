package personalassistant

import (
	"context"
	"path/filepath"
	"reflect"
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
	f.setup.intent = FolderSetupIntent{Portfolio: true, Placement: "grouped", OpensProjects: []string{"Desktop"}, CreatesHome: true, StaffsHome: true}
	f.setup.lines = append(f.setup.lines, FolderPlanLine{Kind: FolderPlanHandoff, Name: "Your Portfolio Manager then suggests opening Desktop"})
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

// Two presses: the first Set up never opens the song, even if asked; the Open
// press does, and every later pass keeps opening.
func TestFolderHandoff_OpenIsItsOwnPressAndSticks(t *testing.T) {
	f, _ := handoffCardFixture(t)
	ctx := context.Background()
	stopWith := func(reason string) {
		f.setup.finish = func() FolderSetupUpdate {
			return FolderSetupUpdate{Status: FolderSetupStopped, StopReason: reason, RunID: "new-run", HomeID: "home-1"}
		}
	}
	press := func(id string, input FolderSetupInput, runs int) FolderSetupRequest {
		t.Helper()
		input.RequestID, input.PlanDigest = id, f.offer.Plan.Digest
		if _, err := f.service.StartSetup(ctx, "local", f.offer.ID, input); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "the run "+id, func() bool {
			stored := f.stored(t)
			return f.setup.runCount() == runs && stored.Setup != nil && stored.Setup.Status == FolderSetupStopped && !f.service.isRunning(f.offer.ID)
		})
		f.setup.mu.Lock()
		defer f.setup.mu.Unlock()
		return f.setup.ran[runs-1]
	}
	stopWith(FolderStopHandoffReady)
	if first := press("r1", FolderSetupInput{Open: true}, 1); first.Open || f.stored(t).Setup.Opening {
		t.Fatal("the first Set up opened the song")
	}
	if again := press("r2", FolderSetupInput{}, 2); again.Open {
		t.Fatal("a resume without Open opened the song")
	}
	stopWith(FolderStopNeedsModel)
	if opened := press("r3", FolderSetupInput{Open: true}, 3); !opened.Open || !f.stored(t).Setup.Opening {
		t.Fatal("Open did not open the song")
	}
	if retried := press("r4", FolderSetupInput{}, 4); !retried.Open {
		t.Fatal("Try again after Open forgot to open")
	}
}

// Home first: a song whose Set up made the Home and stopped at the hand-off is
// the Home's suggestion, and no other Home's.
func TestFolderHandoff_TheStoppedSongIsItsHomesSuggestion(t *testing.T) {
	f, _ := handoffCardFixture(t)
	ctx := context.Background()
	f.setup.finish = func() FolderSetupUpdate {
		return FolderSetupUpdate{Status: FolderSetupStopped, StopReason: FolderStopHandoffReady, RunID: "new-run", HomeID: "home-1"}
	}
	if got, err := f.service.HomeSuggestions(ctx, "local", "home-1"); err != nil || len(got) != 0 {
		t.Fatalf("a song not yet set up was suggested by a Home it does not join: %+v %v", got, err)
	}
	if _, err := f.service.StartSetup(ctx, "local", f.offer.ID, FolderSetupInput{RequestID: "r1", PlanDigest: f.offer.Plan.Digest}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the hand-off stop", func() bool {
		stored := f.stored(t)
		return stored.Setup != nil && stored.Setup.StopReason == FolderStopHandoffReady && !f.service.isRunning(f.offer.ID)
	})
	got, err := f.service.HomeSuggestions(ctx, "local", "home-1")
	if err != nil || len(got) != 1 || got[0].ID != f.offer.ID || got[0].Setup == nil || got[0].Setup.PlanDigest != f.offer.Plan.Digest {
		t.Fatalf("Home suggestions = %+v %v", got, err)
	}
	before := f.stored(t)
	for i := 0; i < 3; i++ {
		view, err := f.service.Current(ctx, "local")
		if err != nil || view.Offer == nil || view.Offer.Setup == nil || view.Offer.Setup.StopReason != FolderStopHandoffReady {
			t.Fatalf("Home-ready observation = %+v, %v", view, err)
		}
	}
	if after := f.stored(t); !reflect.DeepEqual(before, after) || f.setup.runCount() != 1 {
		t.Fatal("reading Home-ready status changed the run or continued setup")
	}
	if other, err := f.service.HomeSuggestions(ctx, "local", "someone-elses"); err != nil || len(other) != 0 {
		t.Fatalf("another Home suggested the song: %+v %v", other, err)
	}
}

// A later song, into a Home that is ready and staffed, is the Home's own
// suggestion before anything is pressed, and its one press opens it.
func TestFolderHandoff_ALaterSongOpensInOnePressFromItsHome(t *testing.T) {
	f, _ := handoffCardFixture(t)
	ctx := context.Background()
	f.setup.intent.CreatesHome, f.setup.intent.StaffsHome = false, false
	f.setup.destination = &FolderSetupDestination{Status: "existing", WorkspaceID: "home-1", Name: "Music Production Home", OwnerUserID: "local"}
	current, err := f.service.Current(ctx, "local")
	if err != nil || current.Offer == nil || current.Offer.Plan == nil {
		t.Fatalf("current = %+v %v", current.Offer, err)
	}
	got, err := f.service.HomeSuggestions(ctx, "local", "home-1")
	if err != nil || len(got) != 1 || got[0].Plan == nil || got[0].Plan.Digest != current.Offer.Plan.Digest {
		t.Fatalf("Home suggestions = %+v %v", got, err)
	}
	if _, err := f.service.StartSetup(ctx, "local", f.offer.ID, FolderSetupInput{RequestID: "r1", PlanDigest: current.Offer.Plan.Digest, Open: true}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the run", func() bool { return f.setup.runCount() == 1 && !f.service.isRunning(f.offer.ID) })
	f.setup.mu.Lock()
	ran := f.setup.ran[0]
	f.setup.mu.Unlock()
	if !ran.Open || !f.stored(t).Setup.Opening {
		t.Fatal("the Home's one press did not open the song")
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
