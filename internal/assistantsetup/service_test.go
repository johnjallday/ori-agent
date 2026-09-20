package assistantsetup

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/resetstate"
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
	result         WorkspaceResult
	err            error
	calls          int
	requests       []PrepareRequest
	folderResult   FolderGrantResult
	folderErr      error
	folderCalls    int
	folderRequests []FolderGrantAuthorization
	onFolder       func(FolderGrantResult)
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
	if result.AgentInstanceID == "" {
		result.AgentInstanceID = "instance-1"
	}
	return result, f.err
}

func (f *fakePreparer) ConfirmFileJanitorFolder(request FolderGrantAuthorization) (FolderGrantResult, error) {
	f.folderCalls++
	f.folderRequests = append(f.folderRequests, request)
	result := f.folderResult
	if result.RootGenerationID == "" {
		result.RootGenerationID = "root-1"
	}
	if result.DirectoryReference == "" {
		result.DirectoryReference = "directory-1"
	}
	if f.folderErr == nil && f.onFolder != nil {
		f.onFolder(result)
	}
	return result, f.folderErr
}

type fakeProgressor struct {
	facts          FileJanitorFacts
	factsErr       error
	review         MonitoringFacts
	reviewErr      error
	prepare        PrepareReviewResult
	prepareErr     error
	prepareCalls   int
	prepareRequest PrepareReviewRequest
	onPrepare      func(PrepareReviewRequest)
}

func (f *fakeProgressor) Facts(context.Context, string, string, string) (FileJanitorFacts, error) {
	return f.facts, f.factsErr
}
func (f *fakeProgressor) ReviewMonitoring(context.Context, string, string) (MonitoringFacts, error) {
	return f.review, f.reviewErr
}
func (f *fakeProgressor) PrepareFirstReview(_ context.Context, request PrepareReviewRequest) (PrepareReviewResult, error) {
	f.prepareCalls++
	f.prepareRequest = request
	if f.onPrepare != nil {
		f.onPrepare(request)
	}
	return f.prepare, f.prepareErr
}

func reflectGrantedFolder(preparer *fakePreparer, progressor *fakeProgressor) {
	preparer.onFolder = func(result FolderGrantResult) {
		progressor.facts.FolderReady = true
		progressor.facts.RootGenerationID = result.RootGenerationID
		progressor.facts.DirectoryReference = result.DirectoryReference
		progressor.facts.Readiness = "automation_paused"
		progressor.facts.Paused = true
		progressor.facts.PrivacyMode = "metadata_only"
	}
}

func standardMonitoringFacts() MonitoringFacts {
	return MonitoringFacts{
		RootGenerationID: "root-1", SettingsRevision: "settings-1", AuthorityRevision: "authority-1", PrivacyMode: "metadata_only",
		WatchEvents: []string{"create", "rename"}, DebounceSeconds: 120, SettlingSeconds: 300,
		DailyScanLocalTime: "09:00", Timezone: "America/New_York", ExcludesFiled: true,
	}
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

func TestConsequentialSetupRefusesAfterResetFenceWithoutPreparation(t *testing.T) {
	preparer := &fakePreparer{}
	service := NewService(openTestStore(t), &fakeRelationshipReader{projection: eligibleRelationship(personalassistant.APIStateActive)},
		&fakeResolver{}, &fakePlanSource{plan: testPlan()}, preparer)
	gate := &resetstate.WorkGate{}
	service.SetAdmissionGate(gate)
	proposal, err := service.Get(context.Background(), "local", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := gate.TryFence(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Accept(context.Background(), "local", proposal.Proposal.Revision, ""); !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatalf("err = %v", err)
	}
	if preparer.calls != 0 {
		t.Fatalf("preparer calls = %d", preparer.calls)
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

func acceptedFolderRun(t *testing.T, service *Service) *Projection {
	t.Helper()
	proposal, err := service.Get(context.Background(), "local", "")
	if err != nil {
		t.Fatal(err)
	}
	accepted, _, err := service.Accept(context.Background(), "local", proposal.Proposal.Revision, "")
	if err != nil {
		t.Fatalf("accept: %v (proposal=%+v)", err, proposal)
	}
	if accepted.Run == nil {
		t.Fatalf("accepted without run: %+v", accepted)
	}
	return accepted
}

func TestReadyExistingWorkspaceIsReadOnlyCurrentStatus(t *testing.T) {
	store := openTestStore(t)
	preparer := &fakePreparer{}
	progressor := &fakeProgressor{facts: FileJanitorFacts{
		FolderReady: true, Readiness: "ready", MonitoringApproved: true, MonitoringActive: true,
		PrivacyMode: "metadata_only", RootGenerationID: "root-existing", DirectoryReference: "directory-existing",
	}}
	service := NewService(store, &fakeRelationshipReader{projection: eligibleRelationship(personalassistant.APIStateActive)},
		&fakeResolver{targets: []Target{{WorkspaceID: "workspace-1", Supported: true, Route: "/workspaces/one?panel=file-janitor"}}},
		&fakePlanSource{plan: testPlan()}, preparer)
	service.SetProgressor(progressor)
	projection, err := service.Get(context.Background(), "local", "")
	if err != nil || projection.ViewState != "current_status" || projection.Proposal != nil || projection.Run != nil {
		t.Fatalf("projection=%+v err=%v", projection, err)
	}
	if len(projection.Actions) != 1 || projection.Actions[0].ID != "open_workspace" || preparer.calls != 0 || progressor.prepareCalls != 0 {
		t.Fatalf("actions=%+v preparer=%d scans=%d", projection.Actions, preparer.calls, progressor.prepareCalls)
	}
}

func TestCustomizedPrivacyRoutesToManualReviewWithoutResettingIt(t *testing.T) {
	store := openTestStore(t)
	preparer := &fakePreparer{}
	progressor := &fakeProgressor{facts: FileJanitorFacts{
		FolderReady: true, Readiness: "ready", MonitoringApproved: true,
		PrivacyMode: "content_local", RootGenerationID: "root-existing", DirectoryReference: "directory-existing",
	}}
	service := NewService(store, &fakeRelationshipReader{projection: eligibleRelationship(personalassistant.APIStateActive)},
		&fakeResolver{targets: []Target{{WorkspaceID: "workspace-1", Supported: true, Route: "/workspaces/one?panel=file-janitor"}}},
		&fakePlanSource{plan: testPlan()}, preparer)
	service.SetProgressor(progressor)
	projection, err := service.Get(context.Background(), "local", "")
	if err != nil || projection.ViewState != "manual_required" || projection.Proposal == nil {
		t.Fatalf("projection=%+v err=%v", projection, err)
	}
	if projection.Target == nil || projection.Target.Reason != "privacy_customized" ||
		!strings.Contains(projection.Proposal.MetadataDisclosure, "customized privacy") || len(projection.Actions) != 1 || projection.Actions[0].ID != "manual" {
		t.Fatalf("projection=%+v", projection)
	}
	if preparer.calls != 0 || progressor.prepareCalls != 0 {
		t.Fatalf("custom privacy triggered setup: preparer=%d progress=%d", preparer.calls, progressor.prepareCalls)
	}
}

func TestAcceptAdoptsVerifiedFolderAndPreservesExistingMonitoringState(t *testing.T) {
	store := openTestStore(t)
	preparer := &fakePreparer{}
	progressor := &fakeProgressor{
		facts: FileJanitorFacts{
			FolderReady: true, Readiness: "needs_attention", MonitoringApproved: true, MonitoringActive: true,
			PrivacyMode: "metadata_only", RootGenerationID: "root-existing", DirectoryReference: "directory-existing",
		},
		review: MonitoringFacts{
			RootGenerationID: "root-existing", SettingsRevision: "settings-existing", AuthorityRevision: "authority-existing", PrivacyMode: "metadata_only",
			WatchEvents: []string{"create", "rename"}, DebounceSeconds: 300, SettlingSeconds: 30,
			DailyScanLocalTime: "09:00", Timezone: "UTC", ExcludesFiled: true,
		},
	}
	service := NewService(store, &fakeRelationshipReader{projection: eligibleRelationship(personalassistant.APIStateNeedsHQ)},
		&fakeResolver{targets: []Target{{WorkspaceID: "workspace-1", Supported: true}}}, &fakePlanSource{plan: testPlan()}, preparer)
	service.SetProgressor(progressor)
	accepted := acceptedFolderRun(t, service)
	if accepted.Run.CurrentStep != StepMonitoring || accepted.ViewState != "needs_decision" || accepted.Health == nil ||
		!accepted.Health.MonitoringApproved || !accepted.Health.MonitoringActive {
		t.Fatalf("accepted = %+v", accepted)
	}
	if preparer.folderCalls != 0 {
		t.Fatalf("adoption requested folder access: %d", preparer.folderCalls)
	}
	operations, err := store.ListOperations(context.Background(), "local", accepted.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, operation := range operations {
		if operation.Kind == OperationFolderGrant {
			found = operation.Status == OperationSucceeded && operation.SafeOutcomeCode == "folder_adopted"
		}
	}
	if !found {
		t.Fatalf("operations = %+v", operations)
	}
}

func TestFolderIntentIsOpaqueOwnerBoundExpiringAndCommitsPaused(t *testing.T) {
	store := openTestStore(t)
	preparer := &fakePreparer{}
	progressor := &fakeProgressor{facts: FileJanitorFacts{Readiness: "setup_required"}, review: standardMonitoringFacts()}
	reflectGrantedFolder(preparer, progressor)
	service := NewService(store, &fakeRelationshipReader{projection: eligibleRelationship(personalassistant.APIStateActive)},
		&fakeResolver{targets: []Target{{WorkspaceID: "workspace-1", Supported: true, Route: "/workspaces/one?panel=file-janitor"}}},
		&fakePlanSource{plan: testPlan()}, preparer)
	service.SetProgressor(progressor)
	accepted := acceptedFolderRun(t, service)
	_, token, err := service.BeginFolderIntent(context.Background(), "local", accepted.Run.ID, accepted.Run.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if token == "" || token == accepted.Run.ID || token == accepted.Run.TargetWorkspaceID {
		t.Fatalf("token is not opaque: %q", token)
	}
	if _, err := service.CommitFolderGrant(context.Background(), "other-owner", "workspace-1", token, preparer.ConfirmFileJanitorFolder); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner commit err = %v", err)
	}
	projection, err := service.CommitFolderGrant(context.Background(), "local", "workspace-1", token, preparer.ConfirmFileJanitorFolder)
	if err != nil {
		t.Fatal(err)
	}
	if projection.Run.CurrentStep != StepMonitoring || projection.ViewState != "needs_decision" || projection.Monitoring == nil {
		t.Fatalf("projection = %+v", projection)
	}
	if preparer.folderCalls != 1 || preparer.folderRequests[0].OperationID == "" {
		t.Fatalf("folder requests = %+v", preparer.folderRequests)
	}
	if projection.Health == nil || !projection.Health.Paused || projection.Health.MonitoringApproved {
		t.Fatalf("health = %+v", projection.Health)
	}
	replayed, err := service.CommitFolderGrant(context.Background(), "local", "workspace-1", token, preparer.ConfirmFileJanitorFolder)
	if err != nil || replayed.Run.ID != projection.Run.ID || preparer.folderCalls != 1 {
		t.Fatalf("lost-response replay=%+v calls=%d err=%v", replayed, preparer.folderCalls, err)
	}
}

func TestDeferredRunReloadsWithoutContinuingAndManualRelinkNeedsFreshReview(t *testing.T) {
	store := openTestStore(t)
	preparer := &fakePreparer{}
	resolver := &fakeResolver{targets: []Target{{WorkspaceID: "workspace-1", Supported: true, Route: "/workspaces/one?panel=file-janitor"}}}
	progressor := &fakeProgressor{facts: FileJanitorFacts{Readiness: "setup_required"}, review: standardMonitoringFacts()}
	reflectGrantedFolder(preparer, progressor)
	service := NewService(store, &fakeRelationshipReader{projection: eligibleRelationship(personalassistant.APIStateActive)},
		resolver, &fakePlanSource{plan: testPlan()}, preparer)
	service.SetProgressor(progressor)
	monitoring := monitoringRun(t, service, preparer)
	deferred, err := service.DeferRun(context.Background(), "local", monitoring.Run.ID, monitoring.Run.Revision)
	if err != nil || deferred.ViewState != "saved_for_later" {
		t.Fatalf("deferred=%+v err=%v", deferred, err)
	}
	reloaded, err := service.Get(context.Background(), "local", "")
	if err != nil || reloaded.ViewState != "saved_for_later" || reloaded.Run.Revision != deferred.Run.Revision {
		t.Fatalf("reloaded=%+v err=%v", reloaded, err)
	}
	if progressor.prepareCalls != 0 || preparer.folderCalls != 1 {
		t.Fatalf("deferred setup continued: prepare=%d folder=%d", progressor.prepareCalls, preparer.folderCalls)
	}
	resumed, err := service.ResumeRun(context.Background(), "local", deferred.Run.ID, deferred.Run.Revision)
	if err != nil {
		t.Fatal(err)
	}
	oldReview := resumed.Monitoring.Revision
	progressor.facts.RootGenerationID = "root-2"
	progressor.facts.DirectoryReference = "directory-2"
	progressor.review.RootGenerationID = "root-2"
	progressor.review.SettingsRevision = "settings-2"
	relinked, err := service.Get(context.Background(), "local", "")
	if err != nil || relinked.Run.Revision == resumed.Run.Revision || relinked.Monitoring.Revision == oldReview {
		t.Fatalf("relinked=%+v err=%v", relinked, err)
	}
	if _, err := service.PrepareReview(context.Background(), "local", relinked.Run.ID, relinked.Run.Revision, oldReview); !errors.Is(err, ErrStaleRun) {
		t.Fatalf("old review err = %v", err)
	}
}

func TestResumeReconcilesSavedScanReceiptWithoutRepeatingDomainWork(t *testing.T) {
	store := openTestStore(t)
	preparer := &fakePreparer{}
	progressor := &fakeProgressor{facts: FileJanitorFacts{Readiness: "setup_required"}, review: standardMonitoringFacts()}
	reflectGrantedFolder(preparer, progressor)
	service := NewService(store, &fakeRelationshipReader{projection: eligibleRelationship(personalassistant.APIStateActive)},
		&fakeResolver{targets: []Target{{WorkspaceID: "workspace-1", Supported: true, Route: "/workspaces/one?panel=file-janitor"}}},
		&fakePlanSource{plan: testPlan()}, preparer)
	service.SetProgressor(progressor)
	monitoring := monitoringRun(t, service, preparer)
	if _, _, _, err := store.ClaimPrepareReview(context.Background(), "local", monitoring.Run.ID, monitoring.Run.Revision, monitoring.Monitoring.Revision); err != nil {
		t.Fatal(err)
	}
	deferred, err := service.DeferRun(context.Background(), "local", monitoring.Run.ID, monitoring.Run.Revision)
	if err != nil {
		t.Fatal(err)
	}
	completedAt := time.Now()
	progressor.facts = FileJanitorFacts{
		FolderReady: true, Readiness: "ready", MonitoringApproved: true, MonitoringActive: true,
		PrivacyMode: "metadata_only", RootGenerationID: "root-1", DirectoryReference: "directory-1",
		FirstOutcome: "no_eligible", FirstCompletedAt: completedAt,
	}
	resumed, err := service.ResumeRun(context.Background(), "local", deferred.Run.ID, deferred.Run.Revision)
	if err != nil || resumed.ViewState != "no_new_files" || resumed.Run.Status != RunFirstResult {
		t.Fatalf("resumed=%+v err=%v", resumed, err)
	}
	if progressor.prepareCalls != 0 {
		t.Fatalf("receipt reconciliation repeated domain work: %d", progressor.prepareCalls)
	}
}

func TestDeferredRunSurvivesDatabaseReopenWithoutContinuing(t *testing.T) {
	path := t.TempDir() + "/assistant-setup.db"
	db, err := database.Open(context.Background(), &database.Config{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	preparer := &fakePreparer{}
	resolver := &fakeResolver{targets: []Target{{WorkspaceID: "workspace-1", Supported: true, Route: "/workspaces/one?panel=file-janitor"}}}
	relationships := &fakeRelationshipReader{projection: eligibleRelationship(personalassistant.APIStateActive)}
	service := NewService(NewSQLiteStore(db), relationships, resolver, &fakePlanSource{plan: testPlan()}, preparer)
	accepted := acceptedFolderRun(t, service)
	deferred, err := service.DeferRun(context.Background(), "local", accepted.Run.ID, accepted.Run.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := database.Open(context.Background(), &database.Config{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	resumedService := NewService(NewSQLiteStore(reopened), relationships, resolver, &fakePlanSource{plan: testPlan()}, preparer)
	projection, err := resumedService.Get(context.Background(), "local", "")
	if err != nil || projection.ViewState != "saved_for_later" || projection.Run.ID != deferred.Run.ID || projection.Run.Revision != deferred.Run.Revision {
		t.Fatalf("projection=%+v err=%v", projection, err)
	}
	if preparer.calls != 1 || preparer.folderCalls != 0 {
		t.Fatalf("reopen continued setup: workspace=%d folder=%d", preparer.calls, preparer.folderCalls)
	}
}

func TestRevokedFolderIsNotRegrantedAndAssistantIdentityLossInvalidates(t *testing.T) {
	store := openTestStore(t)
	preparer := &fakePreparer{}
	resolver := &fakeResolver{targets: []Target{{WorkspaceID: "workspace-1", Supported: true, Route: "/workspaces/one?panel=file-janitor"}}}
	progressor := &fakeProgressor{facts: FileJanitorFacts{Readiness: "setup_required"}, review: standardMonitoringFacts()}
	reflectGrantedFolder(preparer, progressor)
	relationships := &fakeRelationshipReader{projection: eligibleRelationship(personalassistant.APIStateActive)}
	service := NewService(store, relationships, resolver, &fakePlanSource{plan: testPlan()}, preparer)
	service.SetProgressor(progressor)
	monitoring := monitoringRun(t, service, preparer)
	progressor.facts = FileJanitorFacts{Readiness: "setup_required", PrivacyMode: "metadata_only"}
	revoked, err := service.Get(context.Background(), "local", "")
	if err != nil || revoked.ViewState != "needs_attention" || preparer.folderCalls != 1 || progressor.prepareCalls != 0 {
		t.Fatalf("revoked=%+v folder=%d prepare=%d err=%v", revoked, preparer.folderCalls, progressor.prepareCalls, err)
	}
	relationships.projection = &personalassistant.Projection{State: personalassistant.APIStateNeedsHire}
	if _, err := service.Get(context.Background(), "local", ""); !errors.Is(err, ErrAssistantNotReady) {
		t.Fatalf("identity loss err = %v", err)
	}
	relationships.projection = eligibleRelationship(personalassistant.APIStateActive)
	invalidated, err := service.Get(context.Background(), "local", "")
	if err != nil || invalidated.Run.Status != RunInvalidated || invalidated.Run.ID != monitoring.Run.ID {
		t.Fatalf("invalidated=%+v err=%v", invalidated, err)
	}
}

func TestRemovedWorkspaceInvalidatesWithoutRecreation(t *testing.T) {
	store := openTestStore(t)
	preparer := &fakePreparer{}
	resolver := &fakeResolver{targets: []Target{{WorkspaceID: "workspace-1", Supported: true}}}
	service := NewService(store, &fakeRelationshipReader{projection: eligibleRelationship(personalassistant.APIStateActive)},
		resolver, &fakePlanSource{plan: testPlan()}, preparer)
	accepted := acceptedFolderRun(t, service)
	resolver.targets = nil
	invalidated, err := service.Get(context.Background(), "local", "")
	if err != nil || invalidated.ViewState != "no_longer_available" || invalidated.Run.Status != RunInvalidated {
		t.Fatalf("invalidated=%+v err=%v", invalidated, err)
	}
	reloaded, err := service.Get(context.Background(), "local", "")
	if err != nil || reloaded.ViewState != "no_longer_available" || preparer.calls != 1 {
		t.Fatalf("reloaded=%+v prepare=%d err=%v", reloaded, preparer.calls, err)
	}
	if accepted.Run.TargetWorkspaceID == "" {
		t.Fatal("test did not create a target")
	}
}

func TestFolderIntentExpiresBeforeAnyGrant(t *testing.T) {
	store := openTestStore(t)
	preparer := &fakePreparer{}
	service := NewService(store, &fakeRelationshipReader{projection: eligibleRelationship(personalassistant.APIStateActive)},
		&fakeResolver{targets: []Target{{WorkspaceID: "workspace-1", Supported: true}}}, &fakePlanSource{plan: testPlan()}, preparer)
	accepted := acceptedFolderRun(t, service)
	now := service.folderIntents.now()
	service.folderIntents.now = func() time.Time { return now }
	_, token, err := service.BeginFolderIntent(context.Background(), "local", accepted.Run.ID, accepted.Run.Revision)
	if err != nil {
		t.Fatal(err)
	}
	service.folderIntents.now = func() time.Time { return now.Add(folderIntentTTL + time.Second) }
	if _, err := service.CommitFolderGrant(context.Background(), "local", "workspace-1", token, preparer.ConfirmFileJanitorFolder); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if preparer.folderCalls != 0 {
		t.Fatalf("folder calls = %d", preparer.folderCalls)
	}
}

func monitoringRun(t *testing.T, service *Service, preparer *fakePreparer) *Projection {
	t.Helper()
	accepted := acceptedFolderRun(t, service)
	_, token, err := service.BeginFolderIntent(context.Background(), "local", accepted.Run.ID, accepted.Run.Revision)
	if err != nil {
		t.Fatal(err)
	}
	monitoring, err := service.CommitFolderGrant(context.Background(), "local", "workspace-1", token, preparer.ConfirmFileJanitorFolder)
	if err != nil {
		t.Fatal(err)
	}
	return monitoring
}

func TestFailedInitialScanIsPausedAndRetryUsesSameReviewedOperations(t *testing.T) {
	store := openTestStore(t)
	preparer := &fakePreparer{}
	progressor := &fakeProgressor{
		facts:   FileJanitorFacts{Readiness: "setup_required"},
		review:  standardMonitoringFacts(),
		prepare: PrepareReviewResult{ScanAttempted: true}, prepareErr: errors.New("scan unavailable"),
	}
	reflectGrantedFolder(preparer, progressor)
	service := NewService(store, &fakeRelationshipReader{projection: eligibleRelationship(personalassistant.APIStateNeedsHQ)},
		&fakeResolver{targets: []Target{{WorkspaceID: "workspace-1", Supported: true}}}, &fakePlanSource{plan: testPlan()}, preparer)
	service.SetProgressor(progressor)
	monitoring := monitoringRun(t, service, preparer)
	failed, err := service.PrepareReview(context.Background(), "local", monitoring.Run.ID, monitoring.Run.Revision, monitoring.Monitoring.Revision)
	if err == nil || failed == nil || failed.ViewState != "needs_attention" || failed.Run.FailedStep != StepInitialScan {
		t.Fatalf("failed projection=%+v err=%v", failed, err)
	}
	completedAt := time.Now()
	progressor.prepareErr = nil
	progressor.prepare = PrepareReviewResult{
		Facts:             FileJanitorFacts{Readiness: "ready", PrivacyMode: "metadata_only", RootGenerationID: "root-1", FirstOutcome: "no_eligible", FirstCompletedAt: completedAt},
		MonitoringApplied: true, ScanAttempted: true, ScanOutcome: "no_eligible", CompletedAt: completedAt,
	}
	progressor.facts = progressor.prepare.Facts
	retried, err := service.PrepareReview(context.Background(), "local", failed.Run.ID, failed.Run.Revision, failed.Monitoring.Revision)
	if err != nil || retried.ViewState != "no_new_files" || progressor.prepareCalls != 2 {
		t.Fatalf("retry projection=%+v calls=%d err=%v", retried, progressor.prepareCalls, err)
	}
}

func TestPrepareReviewAutomaticRetriesAreBoundedAndKeepExactAuthority(t *testing.T) {
	store := openTestStore(t)
	now := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	preparer := &fakePreparer{}
	progressor := &fakeProgressor{
		facts:  FileJanitorFacts{Readiness: "setup_required"},
		review: standardMonitoringFacts(), prepareErr: errors.New("temporarily unavailable"),
	}
	progressor.onPrepare = func(request PrepareReviewRequest) {
		// Match the real adapter: approval survives, rollback pauses, and the
		// path-free operation receipt is the only automatic-retry authority.
		progressor.prepare.MonitoringRetryBound = true
		progressor.review.SettingsRevision = "settings-after-owned-rollback"
		progressor.review.WasApproved = true
		progressor.review.WasPaused = true
		progressor.review.WasMonitoringActive = false
		progressor.review.RetryRunID = request.RunID
		progressor.review.RetryOperationID = request.MonitoringOperation
		progressor.review.RetryReviewRevision = request.Review.Revision
		progressor.review.RetryAuthorityRevision = request.Review.AuthorityRevision
		progressor.review.RetryState = "failed_paused"
	}
	reflectGrantedFolder(preparer, progressor)
	service := NewService(store, &fakeRelationshipReader{projection: eligibleRelationship(personalassistant.APIStateActive)},
		&fakeResolver{targets: []Target{{WorkspaceID: "workspace-1", Supported: true}}}, &fakePlanSource{plan: testPlan()}, preparer)
	service.SetProgressor(progressor)
	monitoring := monitoringRun(t, service, preparer)
	failed, err := service.PrepareReview(context.Background(), "local", monitoring.Run.ID, monitoring.Run.Revision, monitoring.Monitoring.Revision)
	if err == nil || failed == nil {
		t.Fatalf("initial failure projection=%+v err=%v", failed, err)
	}
	if attempted, err := service.RetryDue(context.Background(), now.Add(4*time.Second), 10); err != nil || attempted != 0 {
		t.Fatalf("early retry attempted=%d err=%v", attempted, err)
	}
	now = now.Add(5 * time.Second)
	if attempted, err := service.RetryDue(context.Background(), now, 10); err != nil || attempted != 1 {
		t.Fatalf("first retry attempted=%d err=%v", attempted, err)
	}
	now = now.Add(30 * time.Second)
	if attempted, err := service.RetryDue(context.Background(), now, 10); err != nil || attempted != 1 {
		t.Fatalf("second retry attempted=%d err=%v", attempted, err)
	}
	now = now.Add(time.Hour)
	if attempted, err := service.RetryDue(context.Background(), now, 10); err != nil || attempted != 0 {
		t.Fatalf("exhausted retry attempted=%d err=%v", attempted, err)
	}
	if progressor.prepareCalls != 3 {
		t.Fatalf("prepare calls = %d, want initial plus two automatic retries", progressor.prepareCalls)
	}
}

func TestPrepareReviewAutomaticRetryStopsAfterManualPauseClearsReceipt(t *testing.T) {
	store := openTestStore(t)
	now := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	preparer := &fakePreparer{}
	progressor := &fakeProgressor{
		facts: FileJanitorFacts{Readiness: "setup_required"}, review: standardMonitoringFacts(),
		prepareErr: errors.New("temporarily unavailable"),
	}
	progressor.onPrepare = func(request PrepareReviewRequest) {
		progressor.prepare.MonitoringRetryBound = true
		progressor.review.SettingsRevision = "settings-after-owned-rollback"
		progressor.review.WasApproved = true
		progressor.review.WasPaused = true
		progressor.review.RetryRunID = request.RunID
		progressor.review.RetryOperationID = request.MonitoringOperation
		progressor.review.RetryReviewRevision = request.Review.Revision
		progressor.review.RetryAuthorityRevision = request.Review.AuthorityRevision
		progressor.review.RetryState = "failed_paused"
	}
	reflectGrantedFolder(preparer, progressor)
	service := NewService(store, &fakeRelationshipReader{projection: eligibleRelationship(personalassistant.APIStateActive)},
		&fakeResolver{targets: []Target{{WorkspaceID: "workspace-1", Supported: true}}}, &fakePlanSource{plan: testPlan()}, preparer)
	service.SetProgressor(progressor)
	monitoring := monitoringRun(t, service, preparer)
	if _, err := service.PrepareReview(context.Background(), "local", monitoring.Run.ID, monitoring.Run.Revision, monitoring.Monitoring.Revision); err == nil {
		t.Fatal("initial attempt unexpectedly succeeded")
	}
	// The domain clears this marker for every manual pause/settings action.
	progressor.review.RetryRunID = ""
	progressor.review.RetryOperationID = ""
	progressor.review.RetryReviewRevision = ""
	progressor.review.RetryAuthorityRevision = ""
	progressor.review.RetryState = ""
	now = now.Add(5 * time.Second)
	if attempted, err := service.RetryDue(context.Background(), now, 10); err != nil || attempted != 0 {
		t.Fatalf("retry after manual pause attempted=%d err=%v", attempted, err)
	}
	if progressor.prepareCalls != 1 {
		t.Fatalf("prepare calls = %d, want only the initial attempt", progressor.prepareCalls)
	}
}

func TestPrepareReviewUsesExactReviewAndProjectsNoEligibleCompletion(t *testing.T) {
	store := openTestStore(t)
	preparer := &fakePreparer{}
	progressor := &fakeProgressor{
		facts:  FileJanitorFacts{Readiness: "setup_required"},
		review: standardMonitoringFacts(),
		prepare: PrepareReviewResult{
			Facts:     FileJanitorFacts{Readiness: "ready", MonitoringApproved: true, MonitoringActive: true, PrivacyMode: "metadata_only", RootGenerationID: "root-1", FirstOutcome: "no_eligible", FirstCompletedAt: time.Now()},
			WatcherID: "watcher-1", ScheduleID: "daily-1", MonitoringApplied: true,
			ScanOutcome: "no_eligible", IneligibleCount: 2, CompletedAt: time.Now(),
		},
	}
	reflectGrantedFolder(preparer, progressor)
	service := NewService(store, &fakeRelationshipReader{projection: eligibleRelationship(personalassistant.APIStateNeedsHQ)},
		&fakeResolver{targets: []Target{{WorkspaceID: "workspace-1", Supported: true, Route: "/workspaces/one?panel=file-janitor"}}},
		&fakePlanSource{plan: testPlan()}, preparer)
	service.SetProgressor(progressor)
	monitoring := monitoringRun(t, service, preparer)
	if _, err := service.PrepareReview(context.Background(), "local", monitoring.Run.ID, monitoring.Run.Revision, "stale-review"); !errors.Is(err, ErrStaleRun) {
		t.Fatalf("stale review err = %v", err)
	}
	if progressor.prepareCalls != 0 {
		t.Fatal("stale review reached File Janitor")
	}
	progressor.facts = progressor.prepare.Facts
	projection, err := service.PrepareReview(context.Background(), "local", monitoring.Run.ID, monitoring.Run.Revision, monitoring.Monitoring.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if projection.ViewState != "no_new_files" || projection.FirstResult == nil || projection.FirstResult.Outcome != "no_eligible" {
		t.Fatalf("projection = %+v", projection)
	}
	if progressor.prepareCalls != 1 || progressor.prepareRequest.ScanOperation == "" || progressor.prepareRequest.MonitoringOperation == "" {
		t.Fatalf("prepare request = %+v", progressor.prepareRequest)
	}
	reloaded, err := service.Get(context.Background(), "local", "")
	if err != nil || reloaded.ViewState != "no_new_files" || reloaded.Run.ID != projection.Run.ID {
		t.Fatalf("reloaded = %+v, err = %v", reloaded, err)
	}
	replayed, err := service.PrepareReview(context.Background(), "local", monitoring.Run.ID, monitoring.Run.Revision, monitoring.Monitoring.Revision)
	if err != nil || replayed.ViewState != "no_new_files" || progressor.prepareCalls != 1 {
		t.Fatalf("lost-response replay=%+v calls=%d err=%v", replayed, progressor.prepareCalls, err)
	}
	operations, err := store.ListOperations(context.Background(), "local", monitoring.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(operations) != 4 {
		t.Fatalf("operations = %+v", operations)
	}
	for _, operation := range operations {
		if (operation.Kind == OperationMonitoring || operation.Kind == OperationInitialScan) && operation.Status != OperationSucceeded {
			t.Fatalf("operation = %+v", operation)
		}
	}
}
