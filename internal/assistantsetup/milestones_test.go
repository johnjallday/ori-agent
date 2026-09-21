package assistantsetup

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

func testAcceptance() Acceptance {
	return Acceptance{
		OwnerUserID: "local", AssistantID: "assistant-1", ProposalRevision: "proposal-1",
		BlueprintID: BlueprintID, BlueprintVersion: 2, BlueprintDigest: "blueprint-digest",
		TeamPlanRevision: "team-revision", TeamRole: testPlan().Roles[0], TargetMode: TargetCreate,
		TargetWorkspaceID: "workspace-1", WorkspaceOperationID: "operation-1",
		ProfileProvenanceID: "provenance-1", ReviewDigest: "proposal-1",
	}
}

func TestStartWorkspaceMovesClaimedToRunningOnceAndIsIdempotent(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	run, operation, _, err := store.Accept(ctx, testAcceptance())
	if err != nil {
		t.Fatal(err)
	}
	if operation.Status != OperationClaimed || operation.StartedAt != nil {
		t.Fatalf("accepted operation = %+v", operation)
	}
	if err := store.StartWorkspace(ctx, "local", run.ID, operation.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.StartWorkspace(ctx, "local", run.ID, operation.ID); err != nil {
		t.Fatal(err)
	}
	operations, err := store.ListOperations(ctx, "local", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := operations[0]; got.Status != OperationRunning || got.AttemptCount != 1 || got.StartedAt == nil {
		t.Fatalf("running operation = %+v", got)
	}
	if err := store.StartWorkspace(ctx, "local", run.ID, "another-operation"); !errors.Is(err, ErrConflict) {
		t.Fatalf("wrong operation id err = %v", err)
	}
	if err := store.StartWorkspace(ctx, "someone-else", run.ID, operation.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other owner err = %v", err)
	}
}

func TestStartWorkspaceLeavesUnresolvedAndSucceededOperationsAlone(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	run, operation, _, err := store.Accept(ctx, testAcceptance())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkWorkspaceUnresolved(ctx, "local", run.ID, operation.ID, "workspace_outcome_unresolved"); err != nil {
		t.Fatal(err)
	}
	if err := store.StartWorkspace(ctx, "local", run.ID, operation.ID); err != nil {
		t.Fatal(err)
	}
	operations, _ := store.ListOperations(ctx, "local", run.ID)
	if operations[0].Status != OperationUnresolved {
		t.Fatalf("unresolved operation was rewritten: %+v", operations[0])
	}
}

func TestListResourcesIsOwnerScopedAndStablyOrdered(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	run, operation, _, err := store.Accept(ctx, testAcceptance())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteWorkspace(ctx, "local", run.ID, operation.ID, WorkspaceResult{
		WorkspaceID: "workspace-1", AgentInstanceID: "instance-1", ProfileProvenanceID: "provenance-1",
		ProfileStoreOrigin: "roster", ProfileCreated: true, ConfigurationDigest: "config-digest",
	}); err != nil {
		t.Fatal(err)
	}
	resources, err := store.ListResources(ctx, "local", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	kinds := make([]string, 0, len(resources))
	for _, resource := range resources {
		kinds = append(kinds, resource.Kind)
	}
	want := []string{ResourceAgentInstance, ResourceAgentProfile, ResourceWorkspace}
	if len(kinds) != len(want) {
		t.Fatalf("kinds = %v", kinds)
	}
	for index := range want {
		if kinds[index] != want[index] {
			t.Fatalf("kinds = %v, want %v", kinds, want)
		}
	}
	other, err := store.ListResources(ctx, "someone-else", run.ID)
	if err != nil || len(other) != 0 {
		t.Fatalf("other owner saw receipts: %v err=%v", other, err)
	}
}

func receipt(operation, kind, id string, ownership ResourceOwnership) Resource {
	return Resource{OperationID: operation, Kind: kind, ResourceID: id, Ownership: ownership, CreatedAt: time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)}
}

func createRun(mode TargetMode) *Run {
	run := &Run{ID: "run-1", Status: RunActive, CurrentStep: StepWorkspace, BlueprintID: BlueprintID, BlueprintVersion: 2,
		TargetMode: mode, TargetWorkspaceID: "workspace-1"}
	if mode == TargetCreate {
		run.TeamRole = testPlan().Roles[0]
	}
	return run
}

func TestDeriveMilestonesTable(t *testing.T) {
	workspaceReceipt := receipt("op-1", ResourceWorkspace, "workspace-1", OwnershipCreated)
	roleReceipt := receipt("op-1", ResourceAgentInstance, "instance-1", OwnershipCreated)
	cases := []struct {
		name      string
		mode      TargetMode
		mutate    func(*milestoneInput)
		want      []MilestoneStatus
		wantCode  string
		wantIDs   []string
		wantModel bool
	}{
		{name: "claimed and idle", mode: TargetCreate, want: []MilestoneStatus{MilestonePending, MilestonePending}, wantIDs: []string{"workspace", "role:file-curator"}},
		{name: "in flight create", mode: TargetCreate, mutate: func(in *milestoneInput) { in.InFlight = true },
			want: []MilestoneStatus{MilestoneCreating, MilestoneCreating}},
		{name: "in flight adopt", mode: TargetAdopt, mutate: func(in *milestoneInput) { in.InFlight = true },
			want: []MilestoneStatus{MilestoneReusing, MilestoneReusing}, wantIDs: []string{"workspace", "existing_team"}},
		{name: "receipted create", mode: TargetCreate, mutate: func(in *milestoneInput) { in.Resources = []Resource{workspaceReceipt, roleReceipt} },
			want: []MilestoneStatus{MilestoneCreated, MilestoneCreated}, wantModel: true},
		{name: "receipts win over an in-flight flag only for receipted milestones", mode: TargetCreate,
			mutate: func(in *milestoneInput) { in.InFlight = true; in.Resources = []Resource{workspaceReceipt} },
			want:   []MilestoneStatus{MilestoneCreated, MilestoneCreating}},
		{name: "reused role", mode: TargetCreate, mutate: func(in *milestoneInput) {
			in.Resources = []Resource{workspaceReceipt, receipt("op-1", ResourceAgentInstance, "instance-1", OwnershipAdopted)}
		}, want: []MilestoneStatus{MilestoneCreated, MilestoneReused}, wantModel: true},
		{name: "adopted workspace", mode: TargetAdopt, mutate: func(in *milestoneInput) {
			in.Resources = []Resource{receipt("op-1", ResourceWorkspace, "workspace-1", OwnershipAdopted), receipt("op-1", ResourceAgentInstance, "instance-1", OwnershipAdopted)}
		}, want: []MilestoneStatus{MilestoneReused, MilestoneReused}},
		{name: "unresolved", mode: TargetCreate, mutate: func(in *milestoneInput) {
			in.Operation.Status = OperationUnresolved
			in.Operation.SafeErrorCode = "workspace_outcome_unresolved"
			in.Run.Status = RunReconcileRequired
		}, want: []MilestoneStatus{MilestoneNeedsReview, MilestonePending}, wantCode: "workspace_outcome_unresolved"},
		{name: "failed", mode: TargetCreate, mutate: func(in *milestoneInput) {
			in.Operation.Status = OperationFailed
			in.Operation.SafeErrorCode = "agent_root_unavailable"
		}, want: []MilestoneStatus{MilestoneFailed, MilestonePending}, wantCode: "agent_root_unavailable"},
		{name: "running with no live attempt", mode: TargetCreate, mutate: func(in *milestoneInput) { in.Operation.Status = OperationRunning },
			want: []MilestoneStatus{MilestoneNeedsReview, MilestonePending}, wantCode: "preparation_interrupted"},
		{name: "succeeded without receipt", mode: TargetCreate, mutate: func(in *milestoneInput) { in.Operation.Status = OperationSucceeded },
			want: []MilestoneStatus{MilestoneNeedsReview, MilestonePending}},
		{name: "receipts of another operation are ignored", mode: TargetCreate, mutate: func(in *milestoneInput) {
			in.Resources = []Resource{receipt("folder-op", ResourceWorkspace, "workspace-1", OwnershipCreated)}
		}, want: []MilestoneStatus{MilestonePending, MilestonePending}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := milestoneInput{Run: createRun(tc.mode), Operation: &Operation{ID: "op-1", Kind: OperationWorkspace, Status: OperationClaimed}}
			if tc.mutate != nil {
				tc.mutate(&in)
			}
			got := deriveMilestones(in)
			if len(got) != len(tc.want) {
				t.Fatalf("milestones = %+v", got)
			}
			for index, status := range tc.want {
				if got[index].Status != status {
					t.Fatalf("milestone %d (%s) = %s, want %s", index, got[index].ID, got[index].Status, status)
				}
			}
			if tc.wantIDs != nil {
				for index, id := range tc.wantIDs {
					if got[index].ID != id {
						t.Fatalf("ids = %+v, want %v", got, tc.wantIDs)
					}
				}
			}
			if tc.wantCode != "" && got[0].ErrorCode != tc.wantCode {
				t.Fatalf("error code = %q, want %q", got[0].ErrorCode, tc.wantCode)
			}
			if tc.wantModel && !got[1].NeedsModel {
				t.Fatalf("receipted role without a model must flag needs_model: %+v", got[1])
			}
		})
	}
}

func TestDeriveMilestonesNeverShowsCreatedWithoutAReceiptAndCarriesReceiptFacts(t *testing.T) {
	run := createRun(TargetCreate)
	run.TeamRole.ModelConfigured = true
	in := milestoneInput{
		Run: run, Operation: &Operation{ID: "op-1", Kind: OperationWorkspace, Status: OperationSucceeded},
		Resources:     []Resource{receipt("op-1", ResourceWorkspace, "workspace-1", OwnershipCreated), receipt("op-1", ResourceAgentInstance, "instance-1", OwnershipCreated)},
		WorkspaceName: "Resolved Name",
	}
	got := deriveMilestones(in)
	if got[0].Name != "Resolved Name" || got[0].ResourceID != "workspace-1" || got[0].Ownership != OwnershipCreated ||
		got[0].RecordedAt == nil || got[0].Placement != PlacementWorkspaceDirectory {
		t.Fatalf("workspace milestone = %+v", got[0])
	}
	if got[1].Name != "File Curator" || got[1].ResourceID != "instance-1" || got[1].NeedsModel || got[1].Placement != PlacementAgentRoster {
		t.Fatalf("role milestone = %+v", got[1])
	}
}

func TestDeriveMilestonesIsListDrivenAndKeysOnRoleIDNotName(t *testing.T) {
	run := createRun(TargetCreate)
	run.TeamRole.Name = "Filing Clerk (renamed)"
	got := deriveMilestones(milestoneInput{Run: run})
	if got[1].ID != "role:file-curator" || got[1].Name != "Filing Clerk (renamed)" {
		t.Fatalf("identity must follow role id, not display name: %+v", got[1])
	}
}

// blockingPreparer parks every preparation until released so a test can observe
// the in-flight state and count how many attempts were admitted.
type blockingPreparer struct {
	fakePreparer
	entered chan struct{}
	release chan struct{}

	mu        sync.Mutex
	inside    int
	maxInside int
	total     int
}

func newBlockingPreparer() *blockingPreparer {
	return &blockingPreparer{entered: make(chan struct{}, 8), release: make(chan struct{})}
}

func (b *blockingPreparer) PrepareFileJanitor(_ context.Context, request PrepareRequest) (WorkspaceResult, error) {
	b.mu.Lock()
	b.total++
	b.inside++
	if b.inside > b.maxInside {
		b.maxInside = b.inside
	}
	b.mu.Unlock()
	b.entered <- struct{}{}
	<-b.release
	b.mu.Lock()
	b.inside--
	b.mu.Unlock()
	return WorkspaceResult{
		WorkspaceID: request.WorkspaceID, AgentInstanceID: "instance-1", ProfileProvenanceID: request.ProfileProvenanceID,
		ProfileStoreOrigin: "roster", ProfileCreated: true, ConfigurationDigest: "config-digest",
	}, nil
}

func newSequenceService(store Store, preparer WorkspacePreparer) *Service {
	return NewService(store, &fakeRelationshipReader{projection: eligibleRelationship(personalassistant.APIStateActive)},
		&fakeResolver{}, &fakePlanSource{plan: testPlan()}, preparer)
}

func TestAcceptedCreateRunProjectsCreatedMilestonesMatchingReceipts(t *testing.T) {
	store := openTestStore(t)
	preparer := &fakePreparer{result: WorkspaceResult{
		AgentInstanceID: "instance-1", ProfileStoreOrigin: "roster", ProfileCreated: true, ConfigurationDigest: "config-digest",
	}}
	service := newSequenceService(store, preparer)
	accepted := acceptedFolderRun(t, service)

	if len(accepted.Milestones) != 2 {
		t.Fatalf("milestones = %+v", accepted.Milestones)
	}
	workspace, role := accepted.Milestones[0], accepted.Milestones[1]
	if workspace.Status != MilestoneCreated || workspace.ResourceID != accepted.Run.TargetWorkspaceID || workspace.Ownership != OwnershipCreated {
		t.Fatalf("workspace = %+v", workspace)
	}
	if role.Status != MilestoneCreated || role.ResourceID != "instance-1" || role.RoleID != "file-curator" || !role.NeedsModel {
		t.Fatalf("role = %+v (no model configured, so chat needs a model)", role)
	}
	receipts, err := store.ListResources(context.Background(), "local", accepted.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for _, item := range receipts {
		seen[item.Kind] = item.ResourceID
	}
	if seen[ResourceWorkspace] != workspace.ResourceID || seen[ResourceAgentInstance] != role.ResourceID {
		t.Fatalf("milestone ids diverge from receipts: %v vs %+v", seen, accepted.Milestones)
	}
	operations := accepted.Operations
	if operations[0].Status != OperationSucceeded || operations[0].StartedAt == nil {
		t.Fatalf("workspace operation never entered running: %+v", operations[0])
	}
}

func TestNeedsModelFollowsReviewedModelConfiguration(t *testing.T) {
	store := openTestStore(t)
	plan := testPlan()
	plan.Roles[0].ModelConfigured = true
	plan.Roles[0].Model = "gpt-test"
	service := NewService(store, &fakeRelationshipReader{projection: eligibleRelationship(personalassistant.APIStateActive)},
		&fakeResolver{}, &fakePlanSource{plan: plan}, &fakePreparer{result: WorkspaceResult{
			AgentInstanceID: "instance-1", ProfileStoreOrigin: "roster", ProfileCreated: true, ConfigurationDigest: "config-digest",
		}})
	accepted := acceptedFolderRun(t, service)
	if accepted.Milestones[1].NeedsModel {
		t.Fatalf("configured model must not be flagged: %+v", accepted.Milestones[1])
	}
}

func TestMilestonesRebuildFromReceiptsWithZeroPreparerCalls(t *testing.T) {
	store := openTestStore(t)
	first := &fakePreparer{result: WorkspaceResult{AgentInstanceID: "instance-1", ProfileStoreOrigin: "roster", ProfileCreated: true, ConfigurationDigest: "config-digest"}}
	accepted := acceptedFolderRun(t, newSequenceService(store, first))

	secondPreparer := &fakePreparer{}
	reopened := newSequenceService(store, secondPreparer)
	projection, err := reopened.Get(context.Background(), "local", "")
	if err != nil {
		t.Fatal(err)
	}
	if secondPreparer.calls != 0 {
		t.Fatalf("Get re-entered the preparer %d times", secondPreparer.calls)
	}
	if len(projection.Milestones) != 2 {
		t.Fatalf("milestones = %+v", projection.Milestones)
	}
	for index := range projection.Milestones {
		if projection.Milestones[index].ResourceID != accepted.Milestones[index].ResourceID ||
			projection.Milestones[index].Status != accepted.Milestones[index].Status {
			t.Fatalf("milestone %d changed across services: %+v vs %+v", index, projection.Milestones[index], accepted.Milestones[index])
		}
	}
}

func TestBlockedPreparationShowsCreatingAndConcurrentAcceptEntersPreparerOnce(t *testing.T) {
	store := openTestStore(t)
	preparer := newBlockingPreparer()
	service := newSequenceService(store, preparer)
	ctx := context.Background()
	proposal, err := service.Get(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}

	type outcome struct {
		projection *Projection
		err        error
	}
	first := make(chan outcome, 1)
	go func() {
		projection, _, acceptErr := service.Accept(ctx, "local", proposal.Proposal.Revision, "")
		first <- outcome{projection, acceptErr}
	}()
	<-preparer.entered

	inFlight, err := service.Get(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	if inFlight.ViewState != "setting_up" || len(inFlight.Milestones) != 2 ||
		inFlight.Milestones[0].Status != MilestoneCreating || inFlight.Milestones[1].Status != MilestoneCreating {
		t.Fatalf("in-flight projection = %s %+v", inFlight.ViewState, inFlight.Milestones)
	}
	if inFlight.Milestones[0].ResourceID != "" {
		t.Fatalf("creating must not carry a resource id: %+v", inFlight.Milestones[0])
	}

	second := make(chan outcome, 1)
	go func() {
		projection, _, acceptErr := service.Accept(ctx, "local", proposal.Proposal.Revision, "")
		second <- outcome{projection, acceptErr}
	}()
	time.Sleep(50 * time.Millisecond)
	close(preparer.release)

	for name, ch := range map[string]chan outcome{"first": first, "second": second} {
		result := <-ch
		if result.err != nil {
			t.Fatalf("%s accept: %v", name, result.err)
		}
		if result.projection.Milestones[0].Status != MilestoneCreated || result.projection.Milestones[1].Status != MilestoneCreated {
			t.Fatalf("%s accept milestones = %+v", name, result.projection.Milestones)
		}
	}
	if preparer.total != 1 || preparer.maxInside != 1 {
		t.Fatalf("preparer entered %d times, max concurrent %d", preparer.total, preparer.maxInside)
	}
}
