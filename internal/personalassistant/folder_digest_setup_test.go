package personalassistant

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// fakeFolderSetup stands in for the host's runner. Run reports "running", waits
// on hold when set, then reports what finish says (done on run "new-run" by
// default), exactly as the real runner reports through req.Update.
type fakeFolderSetup struct {
	mu      sync.Mutex
	lines   []FolderPlanLine
	planErr error
	runs    int
	hold    chan struct{}
	finish  func() FolderSetupUpdate
}

func (f *fakeFolderSetup) Plan(context.Context, FolderSetupRequest) (FolderSetupPlan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return NewFolderSetupPlan(f.lines), f.planErr
}

func (f *fakeFolderSetup) setLines(lines []FolderPlanLine) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lines = lines
}

func (f *fakeFolderSetup) runCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runs
}

func (f *fakeFolderSetup) Run(ctx context.Context, req FolderSetupRequest) error {
	f.mu.Lock()
	f.runs++
	hold, finish := f.hold, f.finish
	f.mu.Unlock()
	lines := append([]FolderPlanLine(nil), req.Plan.Lines...)
	if err := req.Update(ctx, FolderSetupUpdate{Lines: lines, Status: FolderSetupRunning, RunID: "new-run"}); err != nil {
		return err
	}
	if hold != nil {
		<-hold
	}
	update := FolderSetupUpdate{Lines: lines, Status: FolderSetupDone, RunID: "new-run"}
	if finish != nil {
		update = finish()
		update.Lines = lines
	}
	return req.Update(ctx, update)
}

func oneCardLines() []FolderPlanLine {
	return []FolderPlanLine{
		{Kind: FolderPlanIntegration, Name: "Installs the reviewed REAPER integration 0.9.0"},
		{Kind: FolderPlanWorkspace, Name: "Creates a REAPER Song workspace named Desktop"},
		{Kind: FolderPlanTask, Name: "Queues a first read-only task"},
	}
}

type oneCardFixture struct {
	*folderDigestFixture
	setup  *fakeFolderSetup
	folder string
	offer  FolderOfferView
}

func newOneCardFixture(t *testing.T) *oneCardFixture {
	t.Helper()
	f := newFolderDigestFixture(t)
	folder := filepath.Join(f.home, "Desktop")
	if err := os.WriteFile(filepath.Join(folder, "Song.rpp"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	setup := &fakeFolderSetup{lines: oneCardLines()}
	f.service.deps.Setup = setup
	f.service.deps.Journey = &fakeJourneyVerifier{folder: folder}
	offer, err := f.service.scanRoot(context.Background(), "local", folder, "")
	if err != nil {
		t.Fatal(err)
	}
	return &oneCardFixture{folderDigestFixture: f, setup: setup, folder: folder, offer: offer}
}

func waitFor(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if done() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func (f *oneCardFixture) stored(t *testing.T) FolderOffer {
	t.Helper()
	doc, err := f.store.Read(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	offer := doc.Offer(f.offer.ID)
	if offer == nil {
		t.Fatal("offer missing")
	}
	return *offer
}

func TestFolderSetup_PendingCapabilityOfferCarriesItsPlan(t *testing.T) {
	f := newOneCardFixture(t)
	if f.offer.Plan == nil || f.offer.Plan.Digest != FolderPlanDigest(oneCardLines()) || len(f.offer.Plan.Lines) != 3 {
		t.Fatalf("plan = %+v", f.offer.Plan)
	}
	if f.offer.Setup != nil {
		t.Fatalf("a pending offer has no run: %+v", f.offer.Setup)
	}
	f.setup.planErr = errors.New("cannot read the plugin")
	current, err := f.service.Current(context.Background(), "local")
	if err != nil || current.Offer == nil || current.Offer.Plan != nil {
		t.Fatalf("an unplannable offer must fall back to the journey, got %+v, %v", current.Offer, err)
	}
	// A host without a runner, and a folder with no recognized project, never plan.
	f.setup.planErr = nil
	f.service.deps.Setup = nil
	if current, _ := f.service.Current(context.Background(), "local"); current.Offer.Plan != nil {
		t.Fatal("no runner, no plan")
	}
}

func TestFolderSetup_StaleDigestIsRefusedWithTheFreshPlan(t *testing.T) {
	f := newOneCardFixture(t)
	f.setup.setLines(append(oneCardLines(), FolderPlanLine{Kind: FolderPlanMode, Name: "Uses File-only mode"}))
	view, err := f.service.StartSetup(context.Background(), "local", f.offer.ID,
		FolderSetupInput{RequestID: "r1", PlanDigest: f.offer.Plan.Digest})
	if !errors.Is(err, ErrFolderPlanChanged) {
		t.Fatalf("err = %v", err)
	}
	if view.Plan == nil || len(view.Plan.Lines) != 4 || view.Plan.Digest == f.offer.Plan.Digest {
		t.Fatalf("the refusal must carry the fresh plan: %+v", view.Plan)
	}
	if f.setup.runCount() != 0 || f.stored(t).Status != FolderOfferPending || f.stored(t).Setup != nil {
		t.Fatal("a refused click must change nothing")
	}
}

func TestFolderSetup_ClickRecordsTheYesRunsAndResolves(t *testing.T) {
	f := newOneCardFixture(t)
	ctx := context.Background()
	view, err := f.service.StartSetup(ctx, "local", f.offer.ID, FolderSetupInput{RequestID: "r1", PlanDigest: f.offer.Plan.Digest})
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != FolderOfferAwaitingOutcome && view.Status != FolderOfferResolved {
		t.Fatalf("status = %s", view.Status)
	}
	waitFor(t, "the offer to resolve", func() bool { return f.stored(t).Status == FolderOfferResolved })
	stored := f.stored(t)
	if stored.Outcome == nil || stored.Outcome.WorkspaceID != "quest-project" || stored.Decision != FolderDecisionYes ||
		stored.Choice != FolderChoiceProject || stored.Setup == nil || stored.Setup.RunID != "new-run" {
		t.Fatalf("stored offer = %+v", stored)
	}
	// The same click again is a replay, not a second run.
	if _, err := f.service.StartSetup(ctx, "local", f.offer.ID, FolderSetupInput{RequestID: "r1", PlanDigest: f.offer.Plan.Digest}); err != nil {
		t.Fatal(err)
	}
	if f.setup.runCount() != 1 {
		t.Fatalf("runs = %d", f.setup.runCount())
	}
}

func TestFolderSetup_TwoClicksStartOneRun(t *testing.T) {
	f := newOneCardFixture(t)
	f.setup.hold = make(chan struct{})
	ctx := context.Background()
	if _, err := f.service.StartSetup(ctx, "local", f.offer.ID, FolderSetupInput{RequestID: "tab-1", PlanDigest: f.offer.Plan.Digest}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the run to start", func() bool { return f.setup.runCount() == 1 })
	view, err := f.service.StartSetup(ctx, "local", f.offer.ID, FolderSetupInput{RequestID: "tab-2", PlanDigest: f.offer.Plan.Digest})
	if err != nil || view.Setup == nil || view.Setup.Status != FolderSetupRunning {
		t.Fatalf("second click = %+v, %v", view, err)
	}
	close(f.setup.hold)
	waitFor(t, "the offer to resolve", func() bool { return f.stored(t).Status == FolderOfferResolved })
	if f.setup.runCount() != 1 {
		t.Fatalf("runs = %d, want 1", f.setup.runCount())
	}
}

func TestFolderSetup_ARunLostToARestartIsReportedInterruptedAndResumes(t *testing.T) {
	f := newOneCardFixture(t)
	f.setup.hold = make(chan struct{})
	ctx := context.Background()
	if _, err := f.service.StartSetup(ctx, "local", f.offer.ID, FolderSetupInput{RequestID: "r1", PlanDigest: f.offer.Plan.Digest}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the run to start", func() bool { return f.setup.runCount() == 1 })

	// A restarted server has the stored "running" but no live run, and has
	// forgotten the picked folder.
	restarted := f.newService()
	restarted.deps.Setup = f.setup
	restarted.deps.Journey = &fakeJourneyVerifier{folder: f.folder}
	current, err := restarted.Current(ctx, "local")
	if err != nil || current.Offer == nil || current.Offer.Setup == nil {
		t.Fatalf("current = %+v, %v", current.Offer, err)
	}
	if got := current.Offer.Setup; got.Status != FolderSetupStopped || got.StopReason != FolderStopInterrupted {
		t.Fatalf("setup = %+v", got)
	}
	if _, err := restarted.StartSetup(ctx, "local", f.offer.ID, FolderSetupInput{RequestID: "r2", PlanDigest: f.stored(t).Setup.PlanDigest}); !errors.Is(err, ErrFolderPathLost) {
		t.Fatalf("a lost path must ask for the folder again: %v", err)
	}
	close(f.setup.hold)
}

func TestFolderSetup_AStoppedRunResumesOnItsConfirmedPlan(t *testing.T) {
	f := newOneCardFixture(t)
	f.setup.finish = func() FolderSetupUpdate {
		return FolderSetupUpdate{Status: FolderSetupStopped, StopReason: FolderStopNeedsModel, RunID: "new-run"}
	}
	ctx := context.Background()
	if _, err := f.service.StartSetup(ctx, "local", f.offer.ID, FolderSetupInput{RequestID: "r1", PlanDigest: f.offer.Plan.Digest}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the run to stop", func() bool {
		s := f.stored(t).Setup
		return s != nil && s.Status == FolderSetupStopped && !f.service.isRunning(f.offer.ID)
	})
	if f.stored(t).Status != FolderOfferAwaitingOutcome {
		t.Fatal("a stopped run must leave the offer awaiting its outcome")
	}
	// The plan shrinks as steps finish, so a resume is held to the plan it was
	// confirmed on, not to a recomputed one.
	f.setup.setLines(oneCardLines()[1:])
	if _, err := f.service.StartSetup(ctx, "local", f.offer.ID, FolderSetupInput{RequestID: "r2", PlanDigest: "not-the-confirmed-plan"}); !errors.Is(err, ErrFolderPlanChanged) {
		t.Fatalf("resume on another digest: %v", err)
	}
	f.setup.finish = nil
	if _, err := f.service.StartSetup(ctx, "local", f.offer.ID, FolderSetupInput{RequestID: "r3", PlanDigest: f.stored(t).Setup.PlanDigest}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the resumed run to resolve", func() bool { return f.stored(t).Status == FolderOfferResolved })
	if f.setup.runCount() != 2 {
		t.Fatalf("runs = %d", f.setup.runCount())
	}
}

func TestFolderSetup_RefusesWhatItCannotRun(t *testing.T) {
	f := newOneCardFixture(t)
	ctx := context.Background()
	for _, name := range []string{"../Other.rpp", "dir/Song.rpp", "bad\nname.rpp"} {
		if _, err := f.service.StartSetup(ctx, "local", f.offer.ID, FolderSetupInput{RequestID: "r-" + name, PlanDigest: f.offer.Plan.Digest, EntryName: name}); !errors.Is(err, ErrValidation) {
			t.Errorf("entry %q: %v", name, err)
		}
	}
	if _, err := f.service.StartSetup(ctx, "local", f.offer.ID, FolderSetupInput{RequestID: "", PlanDigest: f.offer.Plan.Digest}); !errors.Is(err, ErrValidation) {
		t.Errorf("missing request id: %v", err)
	}
	if _, err := f.service.StartSetup(ctx, "local", "no-such-offer", FolderSetupInput{RequestID: "x", PlanDigest: "d"}); !errors.Is(err, ErrFolderOfferNotFound) {
		t.Errorf("unknown offer: %v", err)
	}
	// A restart forgets a picked folder; the click then asks for it again.
	restarted := f.newService()
	restarted.deps.Setup = f.setup
	if _, err := restarted.StartSetup(ctx, "local", f.offer.ID, FolderSetupInput{RequestID: "r-lost", PlanDigest: f.offer.Plan.Digest}); !errors.Is(err, ErrFolderPathLost) {
		t.Errorf("lost path: %v", err)
	}
	if f.setup.runCount() != 0 || f.stored(t).Status != FolderOfferPending {
		t.Fatal("a refused click must change nothing")
	}
}

func TestFolderSetup_RunStateStaysBoundedAndPathFree(t *testing.T) {
	bad := []FolderSetupRun{
		{Status: "sideways"},
		{Status: FolderSetupRunning, Lines: []FolderPlanLine{{Kind: "workspace", Name: "Links /Users/me/Songs"}}},
		{Status: FolderSetupStopped, EntryCandidates: []string{"../A.rpp"}},
		{Status: FolderSetupRunning, Lines: make([]FolderPlanLine, folderSetupMaxLines+1)},
	}
	for i, run := range bad {
		if err := validateFolderSetupRun(run); err == nil {
			t.Errorf("case %d accepted: %+v", i, run)
		}
	}
	good := FolderSetupRun{Status: FolderSetupStopped, EntryCandidates: []string{"A.rpp"},
		Lines: []FolderPlanLine{{Kind: "workspace", Name: "Creates a workspace named My Song"}}}
	if err := validateFolderSetupRun(good); err != nil {
		t.Fatal(err)
	}
}
