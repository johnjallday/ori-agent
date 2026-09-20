package assistantsetup

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

type fakeRelationshipReader struct {
	projection *personalassistant.Projection
	err        error
	calls      int
}

func (f *fakeRelationshipReader) Get(context.Context, string) (*personalassistant.Projection, error) {
	f.calls++
	return f.projection, f.err
}

type fakeResolver struct {
	targets []Target
	err     error
	calls   int
}

func (f *fakeResolver) Resolve(context.Context, string) ([]Target, error) {
	f.calls++
	return append([]Target(nil), f.targets...), f.err
}
func (f *fakeResolver) ResolveOne(_ context.Context, _ string, id string) (*Target, error) {
	f.calls++
	for _, target := range f.targets {
		if target.WorkspaceID == id {
			copy := target
			return &copy, nil
		}
	}
	return nil, ErrNotFound
}

type fakePlanSource struct {
	plan  TeamPlan
	err   error
	calls int
}

func (f *fakePlanSource) ReviewFileJanitorPlan(context.Context, string) (TeamPlan, error) {
	f.calls++
	return f.plan, f.err
}

type fakeRecommendations struct {
	deferred   bool
	readErr    error
	deferErr   error
	readCalls  int
	deferCalls int
}

func (f *fakeRecommendations) FileJanitorRecommendationDeferred(context.Context, string) (bool, error) {
	f.readCalls++
	return f.deferred, f.readErr
}

func (f *fakeRecommendations) DeferFileJanitorRecommendation(context.Context, string) error {
	f.deferCalls++
	if f.deferErr == nil {
		f.deferred = true
	}
	return f.deferErr
}

type fakePreparer struct {
	result   WorkspaceResult
	err      error
	calls    int
	requests []PrepareRequest
}

func (f *fakePreparer) PrepareFileJanitor(_ context.Context, request PrepareRequest) (WorkspaceResult, error) {
	f.calls++
	f.requests = append(f.requests, request)
	result := f.result
	if result.WorkspaceID == "" {
		result.WorkspaceID = request.WorkspaceID
	}
	if result.ProfileProvenanceID == "" {
		result.ProfileProvenanceID = request.ProfileProvenanceID
	}
	return result, f.err
}

func testPlan() TeamPlan {
	return TeamPlan{
		BlueprintID: BlueprintID, BlueprintVersion: 2, BlueprintDigest: "blueprint-digest",
		PlanRevision: "team-revision", Roles: []TeamRole{{
			RoleID: "file-curator", Name: "File Curator", Action: "create",
			Provider: "openai", Model: "", ModelConfigured: false, ConfigDigest: "config-digest",
		}},
	}
}

func openTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	db, err := database.Open(context.Background(), &database.Config{InMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewSQLiteStore(db)
}

func eligibleRelationship(state personalassistant.APIState) *personalassistant.Projection {
	return &personalassistant.Projection{State: state, AssistantID: "assistant-1", DisplayName: "Ari"}
}

func TestGetProposalIsReadOnlyAcrossEligibleRelationshipStates(t *testing.T) {
	for _, state := range []personalassistant.APIState{
		personalassistant.APIStateNeedsHQ,
		personalassistant.APIStateProvisioningHQ,
		personalassistant.APIStateActive,
		personalassistant.APIStatePaused,
	} {
		t.Run(string(state), func(t *testing.T) {
			store := openTestStore(t)
			relationship := &fakeRelationshipReader{projection: eligibleRelationship(state)}
			resolver := &fakeResolver{}
			plans := &fakePlanSource{plan: testPlan()}
			preparer := &fakePreparer{}
			service := NewService(store, relationship, resolver, plans, preparer)

			projection, err := service.Get(context.Background(), "local", "")
			if err != nil {
				t.Fatal(err)
			}
			if projection.ViewState != "proposed" || projection.Proposal == nil || projection.Proposal.Mode != TargetCreate {
				t.Fatalf("projection = %+v", projection)
			}
			if state == personalassistant.APIStatePaused && projection.Relationship.Proactive {
				t.Fatal("paused relationship was made proactive")
			}
			if preparer.calls != 0 {
				t.Fatalf("read-only proposal called preparer %d times", preparer.calls)
			}
			if _, err := store.FindActiveRun(context.Background(), "local", CapabilityID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("proposal allocated a run: %v", err)
			}
		})
	}
}

func TestGetProposalFailsClosedForUntrustedRelationshipStates(t *testing.T) {
	cases := []struct {
		state personalassistant.APIState
		want  error
	}{
		{personalassistant.APIStateNeedsHire, ErrAssistantNotReady},
		{personalassistant.APIStateHiring, ErrAssistantNotReady},
		{personalassistant.APIStateRepairNeeded, ErrAssistantRepair},
	}
	for _, test := range cases {
		t.Run(string(test.state), func(t *testing.T) {
			service := NewService(openTestStore(t), &fakeRelationshipReader{projection: eligibleRelationship(test.state)}, &fakeResolver{}, &fakePlanSource{plan: testPlan()}, &fakePreparer{})
			_, err := service.Get(context.Background(), "local", "")
			if !errors.Is(err, test.want) {
				t.Fatalf("err = %v, want %v", err, test.want)
			}
		})
	}
	service := NewService(openTestStore(t), &fakeRelationshipReader{err: errors.New("read failed")}, &fakeResolver{}, &fakePlanSource{plan: testPlan()}, &fakePreparer{})
	if _, err := service.Get(context.Background(), "local", ""); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unavailable relationship err = %v", err)
	}
}

func TestGetProposalResolvesZeroOneAndSeveralTargetsWithoutGuessing(t *testing.T) {
	tests := []struct {
		name          string
		targets       []Target
		selected      string
		want          TargetMode
		wantPlanCalls int
	}{
		{name: "zero creates", want: TargetCreate, wantPlanCalls: 1},
		{name: "one adopts", targets: []Target{{WorkspaceID: "workspace-1", Name: "Inbox", Supported: true}}, want: TargetAdopt},
		{name: "several require choice", targets: []Target{{WorkspaceID: "workspace-2", Supported: true}, {WorkspaceID: "workspace-1", Supported: true}}, want: TargetChoose},
		{name: "selected exact target", targets: []Target{{WorkspaceID: "workspace-2", Supported: true}, {WorkspaceID: "workspace-1", Supported: true}}, selected: "workspace-2", want: TargetAdopt},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plans := &fakePlanSource{plan: testPlan()}
			service := NewService(openTestStore(t), &fakeRelationshipReader{projection: eligibleRelationship(personalassistant.APIStateActive)}, &fakeResolver{targets: test.targets}, plans, &fakePreparer{})
			projection, err := service.Get(context.Background(), "local", test.selected)
			if err != nil {
				t.Fatal(err)
			}
			if projection.Proposal.Mode != test.want {
				t.Fatalf("mode = %q, want %q", projection.Proposal.Mode, test.want)
			}
			if plans.calls != test.wantPlanCalls {
				t.Fatalf("plan calls = %d, want %d", plans.calls, test.wantPlanCalls)
			}
		})
	}
}

func TestRecommendationDeferralPersistsWithoutCreatingRun(t *testing.T) {
	store := openTestStore(t)
	recommendations := &fakeRecommendations{}
	service := NewService(store, &fakeRelationshipReader{projection: eligibleRelationship(personalassistant.APIStateNeedsHQ)}, &fakeResolver{}, &fakePlanSource{plan: testPlan()}, &fakePreparer{})
	service.SetRecommendationService(recommendations)
	proposal, err := service.Get(context.Background(), "local", "")
	if err != nil {
		t.Fatal(err)
	}
	deferred, err := service.DeferRecommendation(context.Background(), "local", proposal.Proposal.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if deferred.ViewState != "recommendation_deferred" || recommendations.deferCalls != 1 {
		t.Fatalf("deferred = %+v calls=%d", deferred, recommendations.deferCalls)
	}
	if _, err := store.FindActiveRun(context.Background(), "local", CapabilityID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deferral allocated run: %v", err)
	}
	reloaded, err := service.Get(context.Background(), "local", "")
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.ViewState != "recommendation_deferred" || reloaded.Proposal == nil {
		t.Fatalf("reloaded = %+v", reloaded)
	}
}

func TestAcceptPersistsBeforeOneReviewedPreparationAndReplays(t *testing.T) {
	store := openTestStore(t)
	plans := &fakePlanSource{plan: testPlan()}
	preparer := &fakePreparer{result: WorkspaceResult{
		AgentInstanceID: "instance-1", ProfileStoreOrigin: "root", ProfileCreated: true,
		ConfigurationDigest: "config-digest",
	}}
	service := NewService(store, &fakeRelationshipReader{projection: eligibleRelationship(personalassistant.APIStateNeedsHQ)}, &fakeResolver{}, plans, preparer)
	proposal, err := service.Get(context.Background(), "local", "")
	if err != nil {
		t.Fatal(err)
	}

	accepted, created, err := service.Accept(context.Background(), "local", proposal.Proposal.Revision, "")
	if err != nil {
		t.Fatal(err)
	}
	if !created || accepted.Run == nil || accepted.Run.CurrentStep != StepFolder || accepted.ViewState != "needs_permission" {
		t.Fatalf("accepted = %+v created=%v", accepted, created)
	}
	if preparer.calls != 1 {
		t.Fatalf("preparer calls = %d", preparer.calls)
	}
	request := preparer.requests[0]
	if request.RunID == "" || request.OperationID == "" || request.WorkspaceID == "" || request.ProfileProvenanceID == "" {
		t.Fatalf("preparation was not preallocated: %+v", request)
	}

	replayed, replayCreated, err := service.Accept(context.Background(), "local", proposal.Proposal.Revision, "")
	if err != nil {
		t.Fatal(err)
	}
	if replayCreated || replayed.Run.ID != accepted.Run.ID || preparer.calls != 1 {
		t.Fatalf("replay created=%v run=%q calls=%d", replayCreated, replayed.Run.ID, preparer.calls)
	}
}

func TestAcceptPreparationFailureStopsUnresolvedWithoutSecondResource(t *testing.T) {
	store := openTestStore(t)
	preparer := &fakePreparer{err: errors.New("lost response")}
	service := NewService(store, &fakeRelationshipReader{projection: eligibleRelationship(personalassistant.APIStateActive)}, &fakeResolver{}, &fakePlanSource{plan: testPlan()}, preparer)
	proposal, err := service.Get(context.Background(), "local", "")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = service.Accept(context.Background(), "local", proposal.Proposal.Revision, "")
	if !errors.Is(err, ErrReconcileRequired) {
		t.Fatalf("err = %v", err)
	}
	run, err := store.FindActiveRun(context.Background(), "local", CapabilityID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != RunReconcileRequired {
		t.Fatalf("status = %q", run.Status)
	}
	if preparer.calls != 1 {
		t.Fatalf("calls = %d", preparer.calls)
	}
	if _, _, err := service.Accept(context.Background(), "local", proposal.Proposal.Revision, ""); !errors.Is(err, ErrReconcileRequired) {
		t.Fatalf("replay err = %v", err)
	}
	if preparer.calls != 2 {
		// An unresolved operation is observed by the same exact reserved IDs; it
		// may call the idempotent preparer again, but can never allocate another.
		t.Fatalf("calls = %d, want one exact reconciliation call", preparer.calls)
	}
	if preparer.requests[0].WorkspaceID != preparer.requests[1].WorkspaceID || preparer.requests[0].OperationID != preparer.requests[1].OperationID {
		t.Fatalf("reconcile changed identity: %+v then %+v", preparer.requests[0], preparer.requests[1])
	}
}
