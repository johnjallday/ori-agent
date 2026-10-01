package personalassistant

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// portfolioSetupLines is a collection plan as the host words it.
func portfolioSetupLines() []FolderPlanLine {
	return []FolderPlanLine{
		{Kind: FolderPlanProvider, Name: "Uses the installed reviewed Music Project Management plugin"},
		{Kind: FolderPlanHome, Name: "Creates your Music Production Home"},
		{Kind: FolderPlanLibrary, Name: "Lists the 5 music projects in Songs"},
		{Kind: FolderPlanAssistant, Name: "Your project assistant joins each song you open"},
	}
}

// portfolioReceipt is what a finished collection run reads back.
func portfolioReceipt() []FolderReceiptRow {
	return []FolderReceiptRow{
		{Kind: "home", Name: "Music Production Home", Detail: "created", Route: "/workspaces/music-home/assistant#projectLibraryPanel"},
		{Kind: "library", Name: "Listed 5 music projects in Songs", Detail: "names and project files only"},
	}
}

type portfolioSetupFixture struct {
	*folderDigestFixture
	setup *fakeFolderSetup
	offer *FolderOfferView
	root  string
}

// newPortfolioSetupFixture is a pending five-song collection whose host can plan
// and run it; its run builds "new-home" and reports the receipt.
func newPortfolioSetupFixture(t *testing.T, existingHome bool) *portfolioSetupFixture {
	t.Helper()
	f := newFolderDigestFixture(t)
	root := filepath.Join(f.home, "Songs")
	for i := range 5 {
		sub := filepath.Join(root, fmt.Sprintf("Song %d", i+1))
		if err := os.MkdirAll(sub, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sub, "Song.rpp"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	f.service.deps.HomeExists = func(context.Context, string, string) (bool, error) { return existingHome, nil }
	f.service.deps.ExistingHome = func(context.Context, string, string) (FolderCreateResult, error) { return existingHomeResult() }
	f.service.deps.HomeJourney = &fakeHomeVerifier{}
	f.service.deps.Picker = fakeFolderPicker{path: root, chosen: true}
	homeID := "new-home"
	if existingHome {
		homeID = "music-home"
	}
	setup := &fakeFolderSetup{lines: portfolioSetupLines(), finish: func() FolderSetupUpdate {
		return FolderSetupUpdate{Status: FolderSetupDone, HomeID: homeID, Receipt: portfolioReceipt()}
	}}
	f.service.deps.Setup = setup
	offer, err := f.service.ScanPicked(context.Background(), "local")
	if err != nil || offer == nil || offer.Portfolio == nil {
		t.Fatalf("collection offer = %+v, %v", offer, err)
	}
	return &portfolioSetupFixture{folderDigestFixture: f, setup: setup, offer: offer, root: root}
}

func existingHomeResult() (FolderCreateResult, error) {
	return FolderCreateResult{WorkspaceID: "music-home", Route: "/workspaces/music-home"}, nil
}

func (f *portfolioSetupFixture) stored(t *testing.T) FolderOffer {
	t.Helper()
	doc, err := f.store.Read(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	return *doc.Offer(f.offer.ID)
}

func TestPortfolioSetup_ACollectionOfferCarriesItsPlanAndItsSongCount(t *testing.T) {
	f := newPortfolioSetupFixture(t, false)
	if f.offer.Plan == nil || f.offer.Plan.Digest != FolderPlanDigest(portfolioSetupLines()) {
		t.Fatalf("plan = %+v", f.offer.Plan)
	}
	// Every project folder holds a project file the reviewed integration opens.
	if f.offer.Portfolio.IntegrationKey == "" || f.offer.Portfolio.IntegrationProjects != 5 || f.offer.Portfolio.Projects != 5 {
		t.Fatalf("portfolio = %+v", f.offer.Portfolio)
	}
}

func TestPortfolioSetup_AStalePlanIsRefusedAndNothingRuns(t *testing.T) {
	f := newPortfolioSetupFixture(t, false)
	f.setup.setLines(append(portfolioSetupLines(), FolderPlanLine{Kind: FolderPlanAgents, Name: "Adds the agents the Home requires"}))
	view, err := f.service.StartSetup(context.Background(), "local", f.offer.ID, FolderSetupInput{RequestID: "r1", PlanDigest: f.offer.Plan.Digest})
	if !errors.Is(err, ErrFolderPlanChanged) || view.Plan == nil || view.Plan.Digest == f.offer.Plan.Digest {
		t.Fatalf("err = %v plan = %+v", err, view.Plan)
	}
	if f.setup.runCount() != 0 || f.stored(t).Status != FolderOfferPending {
		t.Fatal("a refused click must change nothing")
	}
}

func TestPortfolioSetup_SetUpBuildsTheHomeAndResolvesWithTheReceipt(t *testing.T) {
	f := newPortfolioSetupFixture(t, false)
	if _, err := f.service.StartSetup(context.Background(), "local", f.offer.ID, FolderSetupInput{RequestID: "r1", PlanDigest: f.offer.Plan.Digest}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the collection to resolve", func() bool { return f.stored(t).Status == FolderOfferResolved })
	stored := f.stored(t)
	if stored.Outcome == nil || stored.Outcome.Kind != FolderChoiceHome || stored.Outcome.WorkspaceID != "new-home" || stored.Outcome.Existing {
		t.Fatalf("outcome = %+v", stored.Outcome)
	}
	if len(stored.Outcome.Receipt) != 2 || stored.Outcome.Receipt[0].Kind != "home" || stored.Outcome.Receipt[1].Kind != "library" {
		t.Fatalf("the run's receipt must reach the outcome: %+v", stored.Outcome.Receipt)
	}
	if stored.Decision != FolderDecisionYes || stored.Setup == nil || stored.Setup.HomeID != "new-home" {
		t.Fatalf("stored = %+v", stored)
	}
}

func TestPortfolioSetup_AnExistingHomeResolvesToThatHome(t *testing.T) {
	f := newPortfolioSetupFixture(t, true)
	if !f.offer.Portfolio.ExistingHome {
		t.Fatal("the offer must know the Home exists")
	}
	if _, err := f.service.StartSetup(context.Background(), "local", f.offer.ID, FolderSetupInput{RequestID: "r1", PlanDigest: f.offer.Plan.Digest}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the collection to resolve", func() bool { return f.stored(t).Status == FolderOfferResolved })
	if outcome := f.stored(t).Outcome; outcome == nil || outcome.WorkspaceID != "music-home" || !outcome.Existing || len(outcome.Receipt) != 2 {
		t.Fatalf("outcome = %+v", outcome)
	}
}

// A run that reports another Home than the one it built never resolves the offer.
func TestPortfolioSetup_ARunThatCannotBeVerifiedStops(t *testing.T) {
	f := newPortfolioSetupFixture(t, false)
	f.setup.finish = func() FolderSetupUpdate { return FolderSetupUpdate{Status: FolderSetupDone, HomeID: "somebody-elses"} }
	if _, err := f.service.StartSetup(context.Background(), "local", f.offer.ID, FolderSetupInput{RequestID: "r1", PlanDigest: f.offer.Plan.Digest}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the run to stop", func() bool {
		s := f.stored(t).Setup
		return s != nil && s.Status == FolderSetupStopped
	})
	if stored := f.stored(t); stored.Status != FolderOfferAwaitingOutcome || stored.Setup.StopReason != FolderStopFailed {
		t.Fatalf("stored = %+v", stored)
	}
}

// The library may connect the collection folder only for the run that is
// building this Home, while it runs.
func TestPortfolioSetup_TheFolderIsProvedOnlyToItsOwnRun(t *testing.T) {
	f := newPortfolioSetupFixture(t, false)
	ctx := context.Background()
	if _, _, err := f.service.PortfolioSetupRoot(ctx, "local", f.offer.ID, "new-home"); !errors.Is(err, ErrFolderWorkspaceRefused) {
		t.Fatalf("before Set up: %v", err)
	}
	hold := make(chan struct{})
	f.setup.hold = hold
	if _, err := f.service.StartSetup(ctx, "local", f.offer.ID, FolderSetupInput{RequestID: "r1", PlanDigest: f.offer.Plan.Digest}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the run to start", func() bool { return f.setup.runCount() == 1 })
	// Record the Home the way the run does before it connects the folder.
	if err := f.service.recordSetup(ctx, "local", f.offer.ID, FolderSetupUpdate{Status: FolderSetupRunning, HomeID: "new-home", Lines: portfolioSetupLines()}); err != nil {
		t.Fatal(err)
	}
	path, identity, err := f.service.PortfolioSetupRoot(ctx, "local", f.offer.ID, "new-home")
	if err != nil || filepath.Clean(path) != filepath.Clean(f.root) || identity == "" {
		t.Fatalf("during its run = %q %q %v", path, identity, err)
	}
	if _, _, err := f.service.PortfolioSetupRoot(ctx, "local", f.offer.ID, "another-home"); !errors.Is(err, ErrFolderWorkspaceRefused) {
		t.Fatalf("another Home: %v", err)
	}
	close(hold)
	waitFor(t, "the collection to resolve", func() bool { return f.stored(t).Status == FolderOfferResolved })
	if _, _, err := f.service.PortfolioSetupRoot(ctx, "local", f.offer.ID, "new-home"); !errors.Is(err, ErrFolderWorkspaceRefused) {
		t.Fatalf("after the run: %v", err)
	}
}
