package assistantsetup

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
)

// fakeObserver reports a fixed observation. Like the real observer, a profile
// it reports as created carries the claim's reserved provenance ID.
type fakeObserver struct {
	observation WorkspaceObservation
	err         error
	calls       int
}

func (f *fakeObserver) ObserveFileJanitor(_ context.Context, request PrepareRequest) (WorkspaceObservation, error) {
	f.calls++
	observation := f.observation
	if observation.ProfileCreated && observation.ProfileProvenanceID == "" {
		observation.ProfileProvenanceID = request.ProfileProvenanceID
	}
	return observation, f.err
}

func openFileStore(t *testing.T, path string) *SQLiteStore {
	t.Helper()
	db, err := database.Open(context.Background(), &database.Config{Path: path, WALMode: true, BusyTimeout: 5000})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewSQLiteStore(db)
}

func completeResult() WorkspaceResult {
	return WorkspaceResult{AgentInstanceID: "instance-1", ProfileStoreOrigin: "roster", ProfileCreated: true, ConfigurationDigest: "config-digest"}
}

func newObservedService(store Store, preparer WorkspacePreparer, observer WorkspaceObserver) *Service {
	service := newSequenceService(store, preparer)
	service.SetObserver(observer)
	return service
}

func milestoneStatuses(projection *Projection) []MilestoneStatus {
	statuses := make([]MilestoneStatus, 0, len(projection.Milestones))
	for _, milestone := range projection.Milestones {
		statuses = append(statuses, milestone.Status)
	}
	return statuses
}

func milestoneCodes(projection *Projection) []string {
	codes := make([]string, 0, len(projection.Milestones))
	for _, milestone := range projection.Milestones {
		codes = append(codes, milestone.ErrorCode)
	}
	return codes
}

func actionIDs(projection *Projection) []string {
	ids := make([]string, 0, len(projection.Actions))
	for _, action := range projection.Actions {
		ids = append(ids, action.ID)
	}
	return ids
}

func sameList[T comparable](left, right []T) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func countReceipts(t *testing.T, store *SQLiteStore, runID string) map[string]int {
	t.Helper()
	resources, err := store.ListResources(context.Background(), "local", runID)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, resource := range resources {
		counts[resource.Kind]++
	}
	return counts
}

// Two tabs both at `proposed` press Set up for me at the same moment. Before
// any run exists, the per-owner lock makes the second wait for the first and
// then replay the same run instead of racing it into a conflict.
func TestConcurrentFirstAcceptsShareOneRunAndOnePreparation(t *testing.T) {
	store := openTestStore(t)
	preparer := newBlockingPreparer()
	service := newSequenceService(store, preparer)
	ctx := context.Background()
	first, err := service.Get(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Get(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	if first.Proposal.Revision != second.Proposal.Revision {
		t.Fatal("tabs reviewed different proposals")
	}

	type outcome struct {
		projection *Projection
		created    bool
		err        error
	}
	results := make(chan outcome, 2)
	var start sync.WaitGroup
	start.Add(1)
	for range 2 {
		go func() {
			start.Wait()
			projection, created, acceptErr := service.Accept(ctx, "local", first.Proposal.Revision, "")
			results <- outcome{projection, created, acceptErr}
		}()
	}
	start.Done()
	<-preparer.entered
	close(preparer.release)

	runIDs := map[string]bool{}
	createdCount := 0
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatalf("accept: %v", result.err)
		}
		runIDs[result.projection.Run.ID] = true
		if result.created {
			createdCount++
		}
		if got := milestoneStatuses(result.projection); !sameList(got, []MilestoneStatus{MilestoneCreated, MilestoneCreated}) {
			t.Fatalf("milestones = %v", got)
		}
	}
	if len(runIDs) != 1 || createdCount != 1 {
		t.Fatalf("runs=%v created=%d, want one run created once", runIDs, createdCount)
	}
	if preparer.total != 1 || preparer.maxInside != 1 {
		t.Fatalf("preparer entered %d times (max concurrent %d)", preparer.total, preparer.maxInside)
	}
}

// A process stops while preparing. After a restart (a new service on a
// reopened database, so no in-process attempt) the claim is shown as
// interrupted, reading never continues it, and Continue setup finishes it on
// the same reserved IDs.
func TestInterruptedPreparationIsShownAndOnlyResumeContinuesIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "setup.db")
	ctx := context.Background()
	parked := newBlockingPreparer()
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(parked.release) }) }
	before := newSequenceService(openFileStore(t, path), parked)
	proposal, err := before.Get(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	parkedDone := make(chan error, 1)
	go func() {
		_, _, acceptErr := before.Accept(ctx, "local", proposal.Proposal.Revision, "")
		parkedDone <- acceptErr
	}()
	<-parked.entered
	var parkedErr error
	parkedFinished := false
	finishParked := func() {
		if !parkedFinished {
			release()
			parkedErr = <-parkedDone
			parkedFinished = true
		}
	}
	t.Cleanup(finishParked) // let the parked attempt end before the databases close

	restarted := openFileStore(t, path)
	preparer := &fakePreparer{result: completeResult()}
	after := newSequenceService(restarted, preparer)
	for range 2 {
		interrupted, getErr := after.Get(ctx, "local", "")
		if getErr != nil {
			t.Fatal(getErr)
		}
		if interrupted.ViewState != "needs_attention" || !sameList(actionIDs(interrupted), []string{"resume", "finish_later"}) ||
			interrupted.Actions[0].Label != "Continue setup" {
			t.Fatalf("interrupted view = %s %+v", interrupted.ViewState, interrupted.Actions)
		}
		if !sameList(milestoneStatuses(interrupted), []MilestoneStatus{MilestoneNeedsReview, MilestonePending}) ||
			interrupted.Milestones[0].ErrorCode != codePreparationInterrupted {
			t.Fatalf("interrupted milestones = %+v", interrupted.Milestones)
		}
	}
	if preparer.calls != 0 {
		t.Fatalf("reading continued the work: %d preparer calls", preparer.calls)
	}

	run, err := restarted.FindActiveRun(ctx, "local", CapabilityID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := after.ResumeRun(ctx, "local", run.ID, run.Revision+1); !errors.Is(err, ErrStaleRun) {
		t.Fatalf("stale resume err = %v", err)
	}
	resumed, err := after.ResumeRun(ctx, "local", run.ID, run.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.ViewState != "needs_permission" || preparer.calls != 1 {
		t.Fatalf("resumed = %s calls=%d", resumed.ViewState, preparer.calls)
	}
	original, continued := parked.firstRequest(), preparer.requests[0]
	if continued.WorkspaceID != original.WorkspaceID || continued.OperationID != original.OperationID ||
		continued.ProfileProvenanceID != original.ProfileProvenanceID {
		t.Fatalf("resume changed the reserved identity: %+v then %+v", original, continued)
	}

	finishParked()
	if parkedErr != nil {
		t.Fatalf("the late first attempt must replay onto the finished claim, not fail: %v", parkedErr)
	}
	counts := countReceipts(t, restarted, run.ID)
	if counts[ResourceWorkspace] != 1 || counts[ResourceAgentInstance] != 1 || counts[ResourceAgentProfile] != 1 || len(counts) != 3 {
		t.Fatalf("receipts = %v, want exactly one of each", counts)
	}
}

func TestResumeRefusesAWorkspaceClaimThatCannotContinue(t *testing.T) {
	store := openTestStore(t)
	preparer := &fakePreparer{err: errors.New("lost response")}
	service := newSequenceService(store, preparer)
	proposal, err := service.Get(context.Background(), "local", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Accept(context.Background(), "local", proposal.Proposal.Revision, ""); !errors.Is(err, ErrReconcileRequired) {
		t.Fatalf("accept err = %v", err)
	}
	run, err := store.FindActiveRun(context.Background(), "local", CapabilityID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ResumeRun(context.Background(), "local", run.ID, run.Revision); err == nil {
		t.Fatal("an unresolved claim must not resume")
	}
	if preparer.calls != 1 {
		t.Fatalf("a refused resume reached the preparer: %d", preparer.calls)
	}
}

func TestAgentRootUnavailableFailsBeforeWritingAndRetriesTheSameClaim(t *testing.T) {
	store := openTestStore(t)
	preparer := &fakePreparer{result: completeResult(), err: fmt.Errorf("creator: %w", ErrAgentRootUnavailable)}
	observer := &fakeObserver{}
	service := newObservedService(store, preparer, observer)
	ctx := context.Background()
	proposal, err := service.Get(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}

	failed, _, err := service.Accept(ctx, "local", proposal.Proposal.Revision, "")
	if !errors.Is(err, ErrAgentRootUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if failed == nil || failed.ViewState != "needs_attention" || !sameList(actionIDs(failed), []string{"resume", "finish_later"}) ||
		failed.Actions[0].Label != "Try again" {
		t.Fatalf("failed projection = %+v", failed)
	}
	if !sameList(milestoneStatuses(failed), []MilestoneStatus{MilestoneFailed, MilestonePending}) ||
		failed.Milestones[0].ErrorCode != codeAgentRootUnavailable {
		t.Fatalf("failed milestones = %+v", failed.Milestones)
	}
	if failed.Run.Status != RunActive || failed.Run.CurrentStep != StepWorkspace || failed.Run.FailedStep != StepWorkspace {
		t.Fatalf("run = %+v", failed.Run)
	}
	if observer.calls != 1 || len(countReceipts(t, store, failed.Run.ID)) != 0 {
		t.Fatalf("observer calls=%d receipts=%v", observer.calls, countReceipts(t, store, failed.Run.ID))
	}

	preparer.err = nil
	retried, err := service.ResumeRun(ctx, "local", failed.Run.ID, failed.Run.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if retried.ViewState != "needs_permission" || preparer.calls != 2 {
		t.Fatalf("retried = %s calls=%d", retried.ViewState, preparer.calls)
	}
	if preparer.requests[0].OperationID != preparer.requests[1].OperationID || preparer.requests[0].WorkspaceID != preparer.requests[1].WorkspaceID {
		t.Fatalf("retry allocated a new claim: %+v then %+v", preparer.requests[0], preparer.requests[1])
	}
	operation := findWorkspaceOperation(retried.Operations)
	if operation.Status != OperationSucceeded || operation.AttemptCount != 2 || operation.SafeErrorCode != "" {
		t.Fatalf("operation = %+v", operation)
	}
	if !sameList(milestoneStatuses(retried), []MilestoneStatus{MilestoneCreated, MilestoneCreated}) {
		t.Fatalf("retried milestones = %v", milestoneStatuses(retried))
	}
}

func TestPlanChangeFailsBeforeWritingAndAllowsAFreshReview(t *testing.T) {
	store := openTestStore(t)
	preparer := &fakePreparer{result: completeResult(), err: fmt.Errorf("creator: %w", ErrTeamConflict)}
	service := newObservedService(store, preparer, &fakeObserver{})
	ctx := context.Background()
	proposal, err := service.Get(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}

	stopped, _, err := service.Accept(ctx, "local", proposal.Proposal.Revision, "")
	if !errors.Is(err, ErrTeamConflict) {
		t.Fatalf("err = %v", err)
	}
	if stopped == nil || stopped.ViewState != "plan_changed" || stopped.Run.Status != RunInvalidated ||
		!sameList(actionIDs(stopped), []string{"review_again", "manual"}) {
		t.Fatalf("stopped = %+v", stopped)
	}
	if !sameList(milestoneStatuses(stopped), []MilestoneStatus{MilestoneFailed, MilestonePending}) ||
		stopped.Milestones[0].ErrorCode != codeTeamPlanChanged {
		t.Fatalf("stopped milestones = %+v", stopped.Milestones)
	}
	if _, err := service.ResumeRun(ctx, "local", stopped.Run.ID, stopped.Run.Revision); err == nil {
		t.Fatal("a changed plan must not resume on the same claim")
	}

	fresh, err := service.Get(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Proposal == nil || fresh.Run != nil || fresh.ViewState != "proposed" {
		t.Fatalf("a changed plan must offer a fresh review: %+v", fresh)
	}
	preparer.err = nil
	accepted, created, err := service.Accept(ctx, "local", fresh.Proposal.Revision, "")
	if err != nil {
		t.Fatal(err)
	}
	if !created || accepted.Run.ID == stopped.Run.ID || accepted.ViewState != "needs_permission" {
		t.Fatalf("fresh accept created=%v run=%q view=%s", created, accepted.Run.ID, accepted.ViewState)
	}
}

// A refusal is only Failed when nothing from the claim exists. Otherwise the
// outcome is unproven: receipts are kept for exactly what is proven, the first
// unproven milestone needs review, and a profile that exists without being
// attached is never Created.
func TestFailureWithLeftoverStateStaysUnprovenAndKeepsWhatIsProven(t *testing.T) {
	cases := []struct {
		name        string
		err         error
		observation WorkspaceObservation
		observerErr error
		want        []MilestoneStatus
		wantCodes   []string
		wantKinds   map[string]int
	}{
		{
			name: "an earlier attempt created the profile", err: ErrTeamConflict,
			observation: WorkspaceObservation{ProfileCreated: true, ProfileStoreOrigin: "roster"},
			want:        []MilestoneStatus{MilestoneNeedsReview, MilestoneNeedsReview},
			wantCodes:   []string{codeWorkspaceUnresolved, codeAgentNotAttached},
			wantKinds:   map[string]int{ResourceAgentProfile: 1},
		},
		{
			name: "a core-only workspace sits at the reserved id", err: ErrAgentRootUnavailable,
			observation: WorkspaceObservation{WorkspacePresent: true},
			want:        []MilestoneStatus{MilestoneNeedsReview, MilestonePending},
			wantCodes:   []string{codeWorkspaceUnresolved, ""},
			wantKinds:   map[string]int{},
		},
		{
			name: "the observer is unavailable", err: ErrTeamConflict, observerErr: errors.New("store offline"),
			want:      []MilestoneStatus{MilestoneNeedsReview, MilestonePending},
			wantCodes: []string{codeWorkspaceUnresolved, ""},
			wantKinds: map[string]int{},
		},
		{
			name: "a proven workspace whose created profile cannot be proven", err: errors.New("lost response"),
			observation: WorkspaceObservation{WorkspacePresent: true, WorkspaceProven: true, AgentInstanceID: "instance-1"},
			want:        []MilestoneStatus{MilestoneCreated, MilestoneNeedsReview},
			wantCodes:   []string{"", codeWorkspaceUnresolved},
			wantKinds:   map[string]int{ResourceWorkspace: 1},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := openTestStore(t)
			preparer := &fakePreparer{err: tc.err}
			service := newObservedService(store, preparer, &fakeObserver{observation: tc.observation, err: tc.observerErr})
			ctx := context.Background()
			proposal, err := service.Get(ctx, "local", "")
			if err != nil {
				t.Fatal(err)
			}
			stopped, _, err := service.Accept(ctx, "local", proposal.Proposal.Revision, "")
			if !errors.Is(err, ErrReconcileRequired) {
				t.Fatalf("err = %v", err)
			}
			if stopped == nil || stopped.ViewState != "needs_attention" || stopped.Run.Status != RunReconcileRequired {
				t.Fatalf("stopped = %+v", stopped)
			}
			if !sameList(milestoneStatuses(stopped), tc.want) || !sameList(milestoneCodes(stopped), tc.wantCodes) {
				t.Fatalf("milestones = %+v", stopped.Milestones)
			}
			counts := countReceipts(t, store, stopped.Run.ID)
			if len(counts) != len(tc.wantKinds) {
				t.Fatalf("receipts = %v, want %v", counts, tc.wantKinds)
			}
			for kind, count := range tc.wantKinds {
				if counts[kind] != count {
					t.Fatalf("receipts = %v, want %v", counts, tc.wantKinds)
				}
			}
			if preparer.calls != 1 {
				t.Fatalf("a failure was retried automatically: %d calls", preparer.calls)
			}
		})
	}
}

// outcomeWritesFail loses every write that would record a failed attempt.
type outcomeWritesFail struct{ Store }

func (outcomeWritesFail) FailWorkspace(context.Context, string, string, string, string, bool) (*Run, error) {
	return nil, errors.New("disk full")
}

func (outcomeWritesFail) MarkWorkspaceUnresolved(context.Context, string, string, string, string) (*Run, error) {
	return nil, errors.New("disk full")
}

// The attempt that just ended still holds the preparation lock while it builds
// its error response. If its outcome could not be recorded, the claim reads
// running, and the response must say the attempt stopped, never Creating.
func TestAnEndedAttemptWhoseOutcomeWasLostIsNeverShownAsCreating(t *testing.T) {
	preparer := &fakePreparer{err: fmt.Errorf("creator: %w", ErrAgentRootUnavailable)}
	service := newObservedService(outcomeWritesFail{openTestStore(t)}, preparer, &fakeObserver{})
	ctx := context.Background()
	proposal, err := service.Get(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	stopped, _, err := service.Accept(ctx, "local", proposal.Proposal.Revision, "")
	if !errors.Is(err, ErrReconcileRequired) {
		t.Fatalf("err = %v", err)
	}
	if stopped == nil || stopped.ViewState != "needs_attention" || !sameList(actionIDs(stopped), []string{"resume", "finish_later"}) {
		t.Fatalf("stopped = %+v", stopped)
	}
	if !sameList(milestoneStatuses(stopped), []MilestoneStatus{MilestoneNeedsReview, MilestonePending}) ||
		stopped.Milestones[0].ErrorCode != codePreparationInterrupted {
		t.Fatalf("milestones = %+v", stopped.Milestones)
	}
}

func TestFailWorkspaceOnlySettlesAnUnsettledClaim(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	run, operation, _, err := store.Accept(ctx, testAcceptance())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkWorkspaceUnresolved(ctx, "local", run.ID, operation.ID, codeWorkspaceUnresolved); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FailWorkspace(ctx, "local", run.ID, operation.ID, codeAgentRootUnavailable, false); !errors.Is(err, ErrConflict) {
		t.Fatalf("an unresolved claim must never become Failed: %v", err)
	}
	if err := store.RecordWorkspaceObservation(ctx, "local", run.ID, "another-operation", WorkspaceObservation{WorkspaceProven: true}); !errors.Is(err, ErrConflict) {
		t.Fatalf("observation for another operation err = %v", err)
	}
}
