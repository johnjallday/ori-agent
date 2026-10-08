package personalassistant

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
)

// fakeFolderSetup stands in for the host's runner. Run reports "running", waits
// on hold when set, then reports what finish says (done on run "new-run" by
// default), exactly as the real runner reports through req.Update.
type fakeFolderSetup struct {
	mu                sync.Mutex
	lines             []FolderPlanLine
	planErr           error
	destination       *FolderSetupDestination
	destinationErr    error
	destinationChecks int
	runs              int
	entries           []string
	hold              chan struct{}
	finish            func() FolderSetupUpdate
}

func (f *fakeFolderSetup) entryNames() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.entries...)
}

func (f *fakeFolderSetup) Plan(context.Context, FolderSetupRequest) (FolderSetupPlan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	plan := NewFolderSetupPlan(f.lines)
	plan.Destination = f.destination
	return plan.Stamped(), f.planErr
}

func (f *fakeFolderSetup) ReadSetupDestination(_ context.Context, req FolderSetupRequest) (*FolderSetupDestination, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.destinationErr != nil {
		return nil, f.destinationErr
	}
	if f.destination == nil {
		return nil, nil
	}
	copy := *f.destination
	return &copy, nil
}

func (f *fakeFolderSetup) ValidateSetupDestination(_ context.Context, req FolderSetupRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.destinationChecks++
	if req.Plan.Destination == nil {
		return errors.New("missing pinned destination")
	}
	return f.destinationErr
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
	f.entries = append(f.entries, req.EntryName)
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

func TestFolderSetup_AdjustThenSetUpStillRunsTheOneClickPlan(t *testing.T) {
	f := newOneCardFixture(t)
	ctx := context.Background()
	// Adjust… sends the offer to the step-by-step journey: a yes with no run.
	view, err := f.service.Decide(ctx, "local", f.offer.ID, FolderDecisionInput{
		Decision: FolderDecisionYes, Choice: FolderChoiceProject, RequestID: "adjust"})
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != FolderOfferAwaitingOutcome {
		t.Fatalf("status = %s", view.Status)
	}
	// The resumed card keeps the one-click plan beside Continue setup.
	current, err := f.service.Current(ctx, "local")
	if err != nil || current.Offer == nil || current.Offer.Plan == nil || current.Offer.Setup != nil {
		t.Fatalf("an adjusted offer must still carry its plan: %+v, %v", current.Offer, err)
	}
	// A stale digest is refused with the fresh plan, as on a pending offer.
	if _, err := f.service.StartSetup(ctx, "local", f.offer.ID, FolderSetupInput{RequestID: "stale", PlanDigest: "0000"}); !errors.Is(err, ErrFolderPlanChanged) {
		t.Fatalf("stale digest: %v", err)
	}
	if _, err := f.service.StartSetup(ctx, "local", f.offer.ID, FolderSetupInput{RequestID: "go", PlanDigest: current.Offer.Plan.Digest}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the offer to resolve", func() bool { return f.stored(t).Status == FolderOfferResolved })
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

// A folder chosen in the native dialog is held in memory only. After a restart,
// choosing the same folder again returns the same offer and its run, which then
// resumes; nothing is created twice.
func TestFolderSetup_PickingTheSameFolderAgainResumesTheSameRun(t *testing.T) {
	f := newOneCardFixture(t)
	ctx := context.Background()
	f.setup.finish = func() FolderSetupUpdate {
		return FolderSetupUpdate{Status: FolderSetupStopped, StopReason: FolderStopNeedsModel, RunID: "new-run"}
	}
	// The card was made by the dialog, not a chip, so its path is memory only.
	picked, err := f.service.scanSelectedRoot(ctx, "local", f.folder, "", "")
	if err != nil || picked.Plan == nil {
		t.Fatalf("picked offer = %+v, %v", picked, err)
	}
	if _, err := f.service.StartSetup(ctx, "local", picked.ID, FolderSetupInput{RequestID: "r1", PlanDigest: picked.Plan.Digest}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the run to stop", func() bool {
		stored, _ := f.store.Read(ctx, "local")
		o := stored.Offer(picked.ID)
		return o != nil && o.Setup != nil && o.Setup.Status == FolderSetupStopped && !f.service.isRunning(picked.ID)
	})

	restarted := f.newService()
	restarted.deps.Setup = f.setup
	restarted.deps.Journey = &fakeJourneyVerifier{folder: f.folder}
	current, err := restarted.Current(ctx, "local")
	if err != nil || current.Offer == nil || !current.Offer.NeedsPick || current.Offer.Setup == nil {
		t.Fatalf("after a restart the card must ask for the folder: %+v, %v", current.Offer, err)
	}
	digest := current.Offer.Setup.PlanDigest
	if _, err := restarted.StartSetup(ctx, "local", picked.ID, FolderSetupInput{RequestID: "r2", PlanDigest: digest}); !errors.Is(err, ErrFolderPathLost) {
		t.Fatalf("a lost path must not be guessed: %v", err)
	}
	again, err := restarted.scanSelectedRoot(ctx, "local", f.folder, "", "")
	if err != nil || again.ID != picked.ID || again.Setup == nil || again.NeedsPick {
		t.Fatalf("picking the same folder must resume the same offer: %+v, %v", again, err)
	}
	f.setup.finish = nil
	runsBefore := f.setup.runCount()
	if _, err := restarted.StartSetup(ctx, "local", picked.ID, FolderSetupInput{RequestID: "r3", PlanDigest: digest}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the resumed run to resolve", func() bool {
		stored, _ := f.store.Read(ctx, "local")
		o := stored.Offer(picked.ID)
		return o != nil && o.Status == FolderOfferResolved
	})
	if f.setup.runCount() != runsBefore+1 {
		t.Fatalf("runs = %d, want %d", f.setup.runCount(), runsBefore+1)
	}
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

type fakeFirstTaskSeeder struct {
	mu    sync.Mutex
	calls []FolderFirstTaskRequest
	err   error
}

func (f *fakeFirstTaskSeeder) SeedFirstTask(_ context.Context, req FolderFirstTaskRequest) (FolderReceiptRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, req)
	if f.err != nil {
		return FolderReceiptRow{}, f.err
	}
	return FolderReceiptRow{Kind: "task", Name: "Summarize the session", Detail: "Starts when you press Start first look"}, nil
}

func (f *fakeFirstTaskSeeder) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func TestFolderSetup_AVerifiedProjectGetsItsFirstTaskOnTheReceipt(t *testing.T) {
	f := newOneCardFixture(t)
	seeder := &fakeFirstTaskSeeder{}
	f.service.deps.FirstTask = seeder
	ctx := context.Background()
	if _, err := f.service.StartSetup(ctx, "local", f.offer.ID, FolderSetupInput{RequestID: "r1", PlanDigest: f.offer.Plan.Digest}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the offer to resolve", func() bool { return f.stored(t).Status == FolderOfferResolved })
	stored := f.stored(t)
	rows := stored.Outcome.Receipt
	if len(rows) != 1 || rows[0].Kind != "task" || rows[0].Detail != "Starts when you press Start first look" {
		t.Fatalf("receipt = %+v", rows)
	}
	if seeder.callCount() != 1 || seeder.calls[0].WorkspaceID != "quest-project" || seeder.calls[0].UserID != "local" {
		t.Fatalf("seeder calls = %+v", seeder.calls)
	}
	if seeder.calls[0].Shape != folderdigest.Shape(stored.Subject.Shape) {
		t.Fatalf("the task must be the shape of the folder shown: %+v", seeder.calls[0])
	}
}

func TestFolderSetup_AFirstTaskThatCannotBeAddedDoesNotFailTheSetup(t *testing.T) {
	f := newOneCardFixture(t)
	f.service.deps.FirstTask = &fakeFirstTaskSeeder{err: errors.New("tasks are unavailable")}
	if _, err := f.service.StartSetup(context.Background(), "local", f.offer.ID, FolderSetupInput{RequestID: "r1", PlanDigest: f.offer.Plan.Digest}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the offer to resolve", func() bool { return f.stored(t).Status == FolderOfferResolved })
	for _, row := range f.stored(t).Outcome.Receipt {
		if row.Kind == "task" {
			t.Fatalf("a task row appeared for a task that was not added: %+v", row)
		}
	}
}

func TestFolderSetup_NoFirstTaskIsSeededForARunTheVerifierRefuses(t *testing.T) {
	f := newOneCardFixture(t)
	seeder := &fakeFirstTaskSeeder{}
	f.service.deps.FirstTask = seeder
	f.service.deps.Journey = &fakeJourneyVerifier{folder: "/somewhere/else"} // refuses this folder
	if _, err := f.service.StartSetup(context.Background(), "local", f.offer.ID, FolderSetupInput{RequestID: "r1", PlanDigest: f.offer.Plan.Digest}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the run to be refused", func() bool {
		s := f.stored(t).Setup
		return s != nil && s.Status == FolderSetupStopped && !f.service.isRunning(f.offer.ID)
	})
	if seeder.callCount() != 0 || f.stored(t).Status != FolderOfferAwaitingOutcome {
		t.Fatalf("seeded for a run that was never proved: calls=%d status=%s", seeder.callCount(), f.stored(t).Status)
	}
}

// Another offer can be left awaiting (an abandoned Adjust…). It must not hide a
// run the user is watching, and it must not swallow the receipt when that run
// finishes: the card polls its own offer by name.
func TestFolderSetup_AnotherWaitingOfferNeitherHidesARunNorItsReceipt(t *testing.T) {
	f := newOneCardFixture(t)
	ctx := context.Background()
	f.setup.hold = make(chan struct{})
	if _, err := f.service.StartSetup(ctx, "local", f.offer.ID, FolderSetupInput{RequestID: "r1", PlanDigest: f.offer.Plan.Digest}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the run to start", func() bool { return f.setup.runCount() == 1 })

	// A newer offer that is merely waiting, from the step-by-step path.
	other := filepath.Join(f.home, "Other")
	if err := os.MkdirAll(other, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "Other.rpp"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	waiting, err := f.service.scanRoot(ctx, "local", other, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Decide(ctx, "local", waiting.ID, FolderDecisionInput{Decision: FolderDecisionYes, RequestID: "yes-other"}); err != nil {
		t.Fatal(err)
	}
	// Make it unambiguously the newer one without touching the fixture's clock,
	// which the running goroutine reads.
	if _, err := f.store.Mutate(ctx, "local", func(d *FolderDigestDocument) error {
		d.Offer(waiting.ID).CreatedAt = d.Offer(f.offer.ID).CreatedAt.Add(time.Hour)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	current, err := f.service.Current(ctx, "local")
	if err != nil || current.Offer == nil || current.Offer.ID != f.offer.ID {
		t.Fatalf("the offer being run must be the current one: %+v, %v", current.Offer, err)
	}

	close(f.setup.hold)
	waitFor(t, "the run to resolve", func() bool { return f.stored(t).Status == FolderOfferResolved })

	// Now the waiting offer is "current", but the card's own offer still reads as
	// resolved, with its outcome, when asked for by name.
	current, err = f.service.Current(ctx, "local")
	if err != nil || current.Offer == nil || current.Offer.ID != waiting.ID {
		t.Fatalf("current after the run = %+v, %v", current.Offer, err)
	}
	named, err := f.service.CurrentOffer(ctx, "local", f.offer.ID)
	if err != nil || named.Offer == nil || named.Offer.ID != f.offer.ID ||
		named.Offer.Status != FolderOfferResolved || named.Offer.Outcome == nil || named.Offer.Outcome.WorkspaceID != "quest-project" {
		t.Fatalf("the finished run's own receipt = %+v, %v", named.Offer, err)
	}
	if _, err := f.service.CurrentOffer(ctx, "local", "no-such-offer"); !errors.Is(err, ErrFolderOfferNotFound) {
		t.Fatalf("an unknown offer: %v", err)
	}
	if _, err := f.service.CurrentOffer(ctx, "local", "  "); !errors.Is(err, ErrValidation) {
		t.Fatalf("a blank offer id: %v", err)
	}
}

func TestFolderSetup_AFolderWithSeveralProjectFilesAsksWhichOne(t *testing.T) {
	f := newOneCardFixture(t)
	ctx := context.Background()
	f.setup.finish = func() FolderSetupUpdate {
		return FolderSetupUpdate{Status: FolderSetupStopped, StopReason: FolderStopNeedsChoice, RunID: "new-run",
			EntryCandidates: []string{"A.rpp", "B.rpp"}}
	}
	if _, err := f.service.StartSetup(ctx, "local", f.offer.ID, FolderSetupInput{RequestID: "r1", PlanDigest: f.offer.Plan.Digest}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the run to ask", func() bool {
		s := f.stored(t).Setup
		return s != nil && s.StopReason == FolderStopNeedsChoice && !f.service.isRunning(f.offer.ID)
	})
	view, err := f.service.Current(ctx, "local")
	if err != nil || view.Offer == nil || view.Offer.Setup == nil || len(view.Offer.Setup.EntryCandidates) != 2 {
		t.Fatalf("the card must carry the file names: %+v, %v", view.Offer, err)
	}
	digest := f.stored(t).Setup.PlanDigest
	// A name the server did not offer never reaches the run, however it is spelt.
	for _, name := range []string{"C.rpp", "../A.rpp", "dir/A.rpp"} {
		if _, err := f.service.StartSetup(ctx, "local", f.offer.ID, FolderSetupInput{RequestID: "bad-" + name, PlanDigest: digest, EntryName: name}); !errors.Is(err, ErrValidation) {
			t.Errorf("%q: %v", name, err)
		}
	}
	if f.setup.runCount() != 1 {
		t.Fatalf("a refused name started a run (runs = %d)", f.setup.runCount())
	}
	f.setup.finish = nil
	if _, err := f.service.StartSetup(ctx, "local", f.offer.ID, FolderSetupInput{RequestID: "ok", PlanDigest: digest, EntryName: "B.rpp"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the offer to resolve", func() bool { return f.stored(t).Status == FolderOfferResolved })
	if names := f.setup.entryNames(); len(names) != 2 || names[1] != "B.rpp" {
		t.Fatalf("the chosen file did not reach the run: %v", names)
	}
}

func TestFolderSetup_APickedFileIsTheProjectFileWithoutAsking(t *testing.T) {
	f := newFolderDigestFixture(t)
	folder := filepath.Join(f.home, "Desktop")
	if err := os.WriteFile(filepath.Join(folder, "Song.rpp"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	setup := &fakeFolderSetup{lines: oneCardLines()}
	f.service.deps.Setup = setup
	f.service.deps.Journey = &fakeJourneyVerifier{folder: folder}
	offer, err := f.service.scanSelectedRoot(context.Background(), "local", folder, "", "Song.rpp")
	if err != nil || offer.Plan == nil {
		t.Fatalf("offer = %+v, %v", offer, err)
	}
	stored, err := f.store.Read(context.Background(), "local")
	if err != nil || stored.Offer(offer.ID) == nil || stored.Offer(offer.ID).EntryName != "Song.rpp" {
		t.Fatalf("the picked file's base name must be kept: %+v, %v", stored.Offer(offer.ID), err)
	}
	if _, err := f.service.StartSetup(context.Background(), "local", offer.ID, FolderSetupInput{RequestID: "r1", PlanDigest: offer.Plan.Digest}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the run to start", func() bool { return len(setup.entryNames()) == 1 })
	if got := setup.entryNames()[0]; got != "Song.rpp" {
		t.Fatalf("entry = %q, want the picked file", got)
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
	for _, text := range []string{"Links ~/Songs", `Links C:\Songs`, "/Users/me/Songs"} {
		if err := validateFolderSetupRun(FolderSetupRun{Status: FolderSetupRunning, Lines: []FolderPlanLine{{Kind: "folder", Name: text}}}); err == nil {
			t.Errorf("a path-like line %q was accepted", text)
		}
	}
	good := FolderSetupRun{Status: FolderSetupStopped, EntryCandidates: []string{"A.rpp"},
		Lines: []FolderPlanLine{
			{Kind: "workspace", Name: "Creates a workspace named My Song"},
			// A reviewed source label is "owner/repo", not a path.
			{Kind: "integration", Name: "Installs and enables the reviewed REAPER integration 0.9.0", Detail: "From johnjallday/reaper-plugin, checked before it is enabled."},
		}}
	if err := validateFolderSetupRun(good); err != nil {
		t.Fatal(err)
	}
}
