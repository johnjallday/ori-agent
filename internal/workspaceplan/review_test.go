package workspaceplan

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// reviewableContent is a complete, valid plan — the kind that may become a
// version. Unlike a work-in-progress draft, it has an objective, a group, and
// an item, because a version is what a user will be asked to approve.
func reviewableContent() PlanContent {
	return PlanContent{
		InScope:   []string{"reporting"},
		NonGoals:  []string{"billing"},
		Execution: ExecutionPolicy{Mode: ExecutionStepThrough},
		Groups: []TaskGroup{{
			ID: "grp-1", Title: "Prepare", Outcome: "Ready",
			Items: []TaskItem{
				{ID: "itm-1", Description: "Snapshot staging", Assignee: "builder"},
				{ID: "itm-2", Description: "Verify checksums", DependsOn: []string{"itm-1"}},
			},
		}},
	}
}

// newReviewablePlan creates a plan whose draft is ready for review.
func newReviewablePlan(t *testing.T, ctx context.Context, service *Service, content PlanContent) *Plan {
	t.Helper()
	plan := mustCreatePlan(t, ctx, service)
	if _, err := service.Store().UpdatePlanDraft(ctx, "ws-1", plan.ID, 0, DraftUpdate{
		Title:     "Ship the migration",
		Objective: "Migrate reporting safely",
		Content:   content,
		UpdatedAt: service.Now(),
	}); err != nil {
		t.Fatalf("seed draft: %v", err)
	}
	// Clarifications live in their own table — a draft write deliberately
	// cannot carry them — so they are seeded through their own path.
	if len(content.Clarifications) > 0 {
		if err := service.Store().PutClarifications(ctx, "ws-1", plan.ID, content.Clarifications); err != nil {
			t.Fatalf("seed clarifications: %v", err)
		}
	}
	reloaded, err := service.Get(ctx, "ws-1", plan.ID)
	if err != nil {
		t.Fatalf("read plan: %v", err)
	}
	return reloaded
}

func reviewService(t *testing.T) *Service {
	t.Helper()
	return NewService(NewMemoryStore())
}

// approveNow drives a plan all the way to an approval, for tests about what
// happens afterwards.
func approveNow(t *testing.T, ctx context.Context, service *Service, plan *Plan, key string) (*Version, *Approval) {
	t.Helper()
	version, err := service.RequestReview(ctx, "ws-1", plan.ID, ReviewInput{Actor: "jj"})
	if err != nil {
		t.Fatalf("request review: %v", err)
	}
	approval, err := service.Approve(ctx, "ws-1", plan.ID, ApprovalRequest{
		Version:        version.Number,
		ContentHash:    version.ContentHash,
		Effect:         EffectCreateTasks,
		UserName:       "jj",
		IdempotencyKey: key,
	})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	return version, approval
}

// --- Review snapshots (FR-31) ---------------------------------------------

func TestRequestReviewSnapshotsAnImmutableVersion(t *testing.T) {
	ctx := context.Background()
	service := reviewService(t)
	plan := newReviewablePlan(t, ctx, service, reviewableContent())

	version, err := service.RequestReview(ctx, "ws-1", plan.ID, ReviewInput{Actor: "jj"})
	if err != nil {
		t.Fatalf("request review: %v", err)
	}
	if version.Number != 1 {
		t.Errorf("version number = %d, want 1", version.Number)
	}
	if version.ContentHash == "" {
		t.Error("version has no content hash")
	}
	if version.Status != VersionInReview {
		t.Errorf("version status = %q", version.Status)
	}

	reloaded, err := service.Get(ctx, "ws-1", plan.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if reloaded.Status != StatusInReview || reloaded.CurrentVersion != 1 {
		t.Errorf("plan = %s v%d, want in_review v1", reloaded.Status, reloaded.CurrentVersion)
	}

	// Editing the draft afterwards cannot change the snapshot.
	stored, err := service.Store().GetVersion(ctx, "ws-1", plan.ID, 1)
	if err != nil {
		t.Fatalf("get version: %v", err)
	}
	if stored.ContentHash != version.ContentHash {
		t.Error("the stored version differs from the one returned")
	}
}

// A version is what a user will be asked to approve, so it cannot contain an
// empty group or a dangling dependency — unlike a work-in-progress draft.
func TestRequestReviewRefusesAnIncompletePlan(t *testing.T) {
	ctx := context.Background()
	service := reviewService(t)

	incomplete := PlanContent{Execution: ExecutionPolicy{Mode: ExecutionStepThrough}}
	plan := newReviewablePlan(t, ctx, service, incomplete)

	_, err := service.RequestReview(ctx, "ws-1", plan.ID, ReviewInput{Actor: "jj"})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("error = %v, want ErrValidation", err)
	}
	after, _ := service.Get(ctx, "ws-1", plan.ID)
	if after.Status != StatusDraft {
		t.Errorf("status = %q, want the plan left as a draft", after.Status)
	}
}

func TestRequestReviewRefusesWhileRequiredQuestionsAreOpen(t *testing.T) {
	ctx := context.Background()
	service := reviewService(t)
	content := reviewableContent()
	content.Clarifications = []Clarification{{
		ID: "clr-1", Prompt: "Which environment?", Required: true, Status: ClarificationOpen,
	}}
	plan := newReviewablePlan(t, ctx, service, content)

	_, err := service.RequestReview(ctx, "ws-1", plan.ID, ReviewInput{Actor: "jj"})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("error = %v, want ErrValidation", err)
	}
}

// The cap offers split or supersession; it never deletes history to make room
// (FR-31).
func TestReviewVersionCapOffersSplitRatherThanDeletingHistory(t *testing.T) {
	ctx := context.Background()
	service := reviewService(t)
	plan := newReviewablePlan(t, ctx, service, reviewableContent())

	for i := range MaxReviewVersions {
		if _, err := service.RequestReview(ctx, "ws-1", plan.ID, ReviewInput{Actor: "jj"}); err != nil {
			t.Fatalf("review %d: %v", i+1, err)
		}
		if _, err := service.RequestChanges(ctx, "ws-1", plan.ID, DecisionInput{Actor: "jj"}); err != nil {
			t.Fatalf("request changes %d: %v", i+1, err)
		}
	}

	_, err := service.RequestReview(ctx, "ws-1", plan.ID, ReviewInput{Actor: "jj"})
	if !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("error = %v, want ErrLimitExceeded", err)
	}
	if !strings.Contains(err.Error(), "Split") || !strings.Contains(err.Error(), "supersede") {
		t.Errorf("limit message does not offer split or supersession: %v", err)
	}

	versions, err := service.Versions(ctx, "ws-1", plan.ID)
	if err != nil {
		t.Fatalf("versions: %v", err)
	}
	if len(versions) != MaxReviewVersions {
		t.Errorf("versions = %d, want %d retained", len(versions), MaxReviewVersions)
	}
}

// --- Decisions (FR-37, FR-66, FR-67) --------------------------------------

func TestRequestChangesRetainsTheReviewedVersion(t *testing.T) {
	ctx := context.Background()
	service := reviewService(t)
	plan := newReviewablePlan(t, ctx, service, reviewableContent())

	version, err := service.RequestReview(ctx, "ws-1", plan.ID, ReviewInput{Actor: "jj"})
	if err != nil {
		t.Fatalf("request review: %v", err)
	}

	updated, err := service.RequestChanges(ctx, "ws-1", plan.ID, DecisionInput{
		Actor: "jj", Version: version.Number, Reason: "scope too wide",
	})
	if err != nil {
		t.Fatalf("request changes: %v", err)
	}
	if updated.Status != StatusDraft {
		t.Errorf("status = %q, want draft", updated.Status)
	}

	retained, err := service.Store().GetVersion(ctx, "ws-1", plan.ID, version.Number)
	if err != nil {
		t.Fatalf("the reviewed version was not retained: %v", err)
	}
	if retained.Status != VersionChangesRequested || retained.DecisionReason != "scope too wide" {
		t.Errorf("decision not recorded: status=%q reason=%q", retained.Status, retained.DecisionReason)
	}
	if retained.ContentHash != version.ContentHash {
		t.Error("the decision changed the version's content hash")
	}
}

func TestRejectRetainsTheVersionAndItsReason(t *testing.T) {
	ctx := context.Background()
	service := reviewService(t)
	plan := newReviewablePlan(t, ctx, service, reviewableContent())

	version, _ := service.RequestReview(ctx, "ws-1", plan.ID, ReviewInput{Actor: "jj"})
	if _, err := service.Reject(ctx, "ws-1", plan.ID, DecisionInput{
		Actor: "jj", Version: version.Number, Reason: "wrong approach",
	}); err != nil {
		t.Fatalf("reject: %v", err)
	}

	rejected, _ := service.Store().GetVersion(ctx, "ws-1", plan.ID, version.Number)
	if rejected.Status != VersionRejected || rejected.DecisionReason != "wrong approach" {
		t.Errorf("rejection not recorded: %+v", rejected)
	}
	// A rejected version can never be approved afterwards (FR-74).
	if rejected.Status.Approvable() {
		t.Error("a rejected version reports itself approvable")
	}
}

func TestDecidingOnAStaleVersionIsRefused(t *testing.T) {
	ctx := context.Background()
	service := reviewService(t)
	plan := newReviewablePlan(t, ctx, service, reviewableContent())
	if _, err := service.RequestReview(ctx, "ws-1", plan.ID, ReviewInput{Actor: "jj"}); err != nil {
		t.Fatalf("request review: %v", err)
	}

	_, err := service.RequestChanges(ctx, "ws-1", plan.ID, DecisionInput{Actor: "jj", Version: 99})
	if !errors.Is(err, ErrStaleVersion) {
		t.Fatalf("error = %v, want ErrStaleVersion", err)
	}
}

// The review view is read-only until the reviewer requests changes, which
// retains the version they were looking at (FR-152).
func TestEditingIsRefusedWhileUnderReview(t *testing.T) {
	ctx := context.Background()
	service := reviewService(t)
	plan := newReviewablePlan(t, ctx, service, reviewableContent())
	if _, err := service.RequestReview(ctx, "ws-1", plan.ID, ReviewInput{Actor: "jj"}); err != nil {
		t.Fatalf("request review: %v", err)
	}

	_, err := service.Edit(ctx, "ws-1", plan.ID, EditInput{
		Objective: "sneaky change", ExpectedRevision: 1,
	})
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("error = %v, want ErrInvalidTransition", err)
	}
	if !strings.Contains(err.Error(), "Request changes") {
		t.Errorf("the refusal does not point at request-changes: %v", err)
	}
}

// --- Approval (FR-59, FR-60, FR-69 through FR-75) --------------------------

func TestApproveBindsToTheExactVersionAndHash(t *testing.T) {
	ctx := context.Background()
	service := reviewService(t)
	plan := newReviewablePlan(t, ctx, service, reviewableContent())
	version, approval := approveNow(t, ctx, service, plan, "key-1")

	if approval.Version != version.Number || approval.ContentHash != version.ContentHash {
		t.Errorf("approval did not bind to the version: %+v", approval)
	}
	if approval.Effect != EffectCreateTasks {
		t.Errorf("effect = %q", approval.Effect)
	}
	if approval.Consumed() {
		t.Error("a fresh approval reports itself consumed")
	}
	// Approval authorizes; it does not itself create work.
	after, _ := service.Get(ctx, "ws-1", plan.ID)
	if len(after.TaskLinks) != 0 {
		t.Error("approving created tasks")
	}
}

// The hash check is what makes "the version you read is the version you
// approved" true (FR-68, FR-69).
func TestApproveRefusesAMismatchedHash(t *testing.T) {
	ctx := context.Background()
	service := reviewService(t)
	plan := newReviewablePlan(t, ctx, service, reviewableContent())
	version, _ := service.RequestReview(ctx, "ws-1", plan.ID, ReviewInput{Actor: "jj"})

	_, err := service.Approve(ctx, "ws-1", plan.ID, ApprovalRequest{
		Version:        version.Number,
		ContentHash:    "a-hash-from-a-stale-browser-tab",
		Effect:         EffectCreateTasks,
		IdempotencyKey: "key-1",
	})
	if !errors.Is(err, ErrApprovalMismatch) {
		t.Fatalf("error = %v, want ErrApprovalMismatch", err)
	}
	if !strings.Contains(err.Error(), "changed since you reviewed it") {
		t.Errorf("the refusal does not explain itself: %v", err)
	}
}

// The label a user clicked must be the behavior they get (FR-63, FR-64).
func TestApproveRefusesAnEffectTheVersionDoesNotDeclare(t *testing.T) {
	ctx := context.Background()
	service := reviewService(t)
	plan := newReviewablePlan(t, ctx, service, reviewableContent()) // step_through
	version, _ := service.RequestReview(ctx, "ws-1", plan.ID, ReviewInput{Actor: "jj"})

	_, err := service.Approve(ctx, "ws-1", plan.ID, ApprovalRequest{
		Version:        version.Number,
		ContentHash:    version.ContentHash,
		Effect:         EffectCreateTasksAndStart, // asks for more than the version declares
		IdempotencyKey: "key-1",
	})
	if !errors.Is(err, ErrApprovalMismatch) {
		t.Fatalf("error = %v, want ErrApprovalMismatch", err)
	}
}

func TestApproveRefusesAStaleOrDecidedVersion(t *testing.T) {
	ctx := context.Background()
	service := reviewService(t)
	plan := newReviewablePlan(t, ctx, service, reviewableContent())

	first, _ := service.RequestReview(ctx, "ws-1", plan.ID, ReviewInput{Actor: "jj"})
	if _, err := service.RequestChanges(ctx, "ws-1", plan.ID, DecisionInput{Actor: "jj"}); err != nil {
		t.Fatalf("request changes: %v", err)
	}
	second, _ := service.RequestReview(ctx, "ws-1", plan.ID, ReviewInput{Actor: "jj"})

	// The superseded first version cannot be approved, even with its own hash.
	_, err := service.Approve(ctx, "ws-1", plan.ID, ApprovalRequest{
		Version:        first.Number,
		ContentHash:    first.ContentHash,
		Effect:         EffectCreateTasks,
		IdempotencyKey: "key-old",
	})
	if !errors.Is(err, ErrStaleVersion) {
		t.Fatalf("approving a decided version error = %v, want ErrStaleVersion", err)
	}

	// The current one can.
	if _, err := service.Approve(ctx, "ws-1", plan.ID, ApprovalRequest{
		Version:        second.Number,
		ContentHash:    second.ContentHash,
		Effect:         EffectCreateTasks,
		IdempotencyKey: "key-new",
	}); err != nil {
		t.Fatalf("approving the current version: %v", err)
	}
}

// Approval is a user action. Nothing else can reach it — not a model, not a
// service, not an execution callback (FR-59, FR-60).
func TestApprovalCannotBeReachedByAnythingButAUser(t *testing.T) {
	ctx := context.Background()
	service := reviewService(t)
	plan := newReviewablePlan(t, ctx, service, reviewableContent())
	if _, err := service.RequestReview(ctx, "ws-1", plan.ID, ReviewInput{Actor: "jj"}); err != nil {
		t.Fatalf("request review: %v", err)
	}

	for _, source := range []TransitionSource{SourceModel, SourceService, SourceExecution, SourceRetention} {
		_, err := service.Transition(ctx, "ws-1", plan.ID, TransitionInput{
			To: StatusApproved, Source: source, Actor: "an agent",
		})
		if !errors.Is(err, ErrApprovalAuthority) {
			t.Errorf("source %q reached approval: %v", source, err)
		}
	}
}

// A retried approval returns the original rather than a second authorization
// (FR-73).
func TestApprovalIsIdempotentOnItsKey(t *testing.T) {
	ctx := context.Background()
	service := reviewService(t)
	plan := newReviewablePlan(t, ctx, service, reviewableContent())
	version, first := approveNow(t, ctx, service, plan, "same-key")

	second, err := service.Approve(ctx, "ws-1", plan.ID, ApprovalRequest{
		Version:        version.Number,
		ContentHash:    version.ContentHash,
		Effect:         EffectCreateTasks,
		IdempotencyKey: "same-key",
	})
	if err != nil {
		t.Fatalf("retried approval: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("retry created a second approval: %s vs %s", first.ID, second.ID)
	}

	approvals, _ := service.Approvals(ctx, "ws-1", plan.ID)
	if len(approvals) != 1 {
		t.Errorf("approvals = %d, want 1", len(approvals))
	}
}

func TestApprovalRequiresAnIdempotencyKey(t *testing.T) {
	ctx := context.Background()
	service := reviewService(t)
	plan := newReviewablePlan(t, ctx, service, reviewableContent())
	version, _ := service.RequestReview(ctx, "ws-1", plan.ID, ReviewInput{Actor: "jj"})

	_, err := service.Approve(ctx, "ws-1", plan.ID, ApprovalRequest{
		Version: version.Number, ContentHash: version.ContentHash, Effect: EffectCreateTasks,
	})
	if !errors.Is(err, ErrValidation) {
		t.Errorf("error = %v, want ErrValidation", err)
	}
}

// Concurrent approvals of the same version resolve to one record (FR-178).
func TestConcurrentApprovalsOfTheSameVersionResolveToOne(t *testing.T) {
	ctx := context.Background()
	service := reviewService(t)
	plan := newReviewablePlan(t, ctx, service, reviewableContent())
	version, _ := service.RequestReview(ctx, "ws-1", plan.ID, ReviewInput{Actor: "jj"})

	const racers = 8
	var (
		wg  sync.WaitGroup
		mu  sync.Mutex
		ids = map[string]struct{}{}
	)
	wg.Add(racers)
	for range racers {
		go func() {
			defer wg.Done()
			approval, err := service.Approve(ctx, "ws-1", plan.ID, ApprovalRequest{
				Version:        version.Number,
				ContentHash:    version.ContentHash,
				Effect:         EffectCreateTasks,
				IdempotencyKey: "shared-key",
			})
			if err != nil {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			ids[approval.ID] = struct{}{}
		}()
	}
	wg.Wait()

	if len(ids) != 1 {
		t.Errorf("distinct approvals = %d, want 1", len(ids))
	}
	approvals, _ := service.Approvals(ctx, "ws-1", plan.ID)
	if len(approvals) != 1 {
		t.Errorf("stored approvals = %d, want 1", len(approvals))
	}
}

func TestInvalidatedApprovalsAreNoLongerUsable(t *testing.T) {
	ctx := context.Background()
	service := reviewService(t)
	plan := newReviewablePlan(t, ctx, service, reviewableContent())
	version, approval := approveNow(t, ctx, service, plan, "key-1")

	if err := service.InvalidateOutstandingApprovals(ctx, "ws-1", plan.ID, "scope changed"); err != nil {
		t.Fatalf("invalidate: %v", err)
	}

	usable, err := service.UsableApproval(ctx, "ws-1", plan.ID, version.Number)
	if err != nil {
		t.Fatalf("usable approval: %v", err)
	}
	if usable != nil {
		t.Errorf("an invalidated approval is still usable: %+v", usable)
	}

	stored, _ := service.Store().GetApproval(ctx, "ws-1", plan.ID, approval.ID)
	if !stored.Invalidated() || stored.InvalidatedReason != "scope changed" {
		t.Errorf("invalidation not recorded: %+v", stored)
	}
}

// --- Review contract (FR-62 through FR-65) ---------------------------------

func TestReviewContractShowsEverythingApprovalAuthorizes(t *testing.T) {
	ctx := context.Background()
	service := reviewService(t)

	content := reviewableContent()
	content.Artifacts = []ProposedArtifact{
		{ID: "art-1", Kind: ArtifactPRD, Path: "tasks/prd.md", Enabled: true},
		{ID: "art-2", Kind: ArtifactNote, Path: "notes/plan.md", Enabled: false},
	}
	content.Execution.Preconditions = []string{"repo_scan"}
	content.Groups[0].Items[1].RequiredCapabilities = []string{"email"}

	plan := newReviewablePlan(t, ctx, service, content)
	version, _ := service.RequestReview(ctx, "ws-1", plan.ID, ReviewInput{Actor: "jj"})

	contract, err := service.BuildReviewContract(ctx, "ws-1", plan.ID, version.Number, ValidationContext{})
	if err != nil {
		t.Fatalf("build contract: %v", err)
	}

	if contract.Version != version.Number || contract.ContentHash != version.ContentHash {
		t.Errorf("contract does not identify the exact version: %+v", contract)
	}
	if contract.TaskCount != 2 || contract.GroupCount != 1 {
		t.Errorf("counts = %d tasks in %d groups, want 2 in 1", contract.TaskCount, contract.GroupCount)
	}
	if len(contract.Assignees) != 1 || contract.Assignees[0] != "builder" {
		t.Errorf("assignees = %v", contract.Assignees)
	}
	if contract.Unassigned != 1 {
		t.Errorf("unassigned = %d, want 1", contract.Unassigned)
	}
	if len(contract.Capabilities) != 1 || contract.Capabilities[0] != "email" {
		t.Errorf("capabilities = %v", contract.Capabilities)
	}
	if contract.Dependencies != 1 {
		t.Errorf("dependency count = %d, want 1", contract.Dependencies)
	}
	if contract.OriginalRequest == "" {
		t.Error("contract omits the original request")
	}

	// Every side effect is stated in plain language (FR-63).
	joined := strings.Join(contract.Effects, " | ")
	for _, want := range []string{"Create 2 workspace task(s)", "tasks/prd.md", "repo_scan", "Nothing starts running"} {
		if !strings.Contains(joined, want) {
			t.Errorf("effects omit %q: %s", want, joined)
		}
	}
	// A disabled artifact is not listed as something that will be written.
	if strings.Contains(joined, "notes/plan.md") {
		t.Errorf("a disabled artifact was listed as a write: %s", joined)
	}
}

// The primary action says what it does. A side-effecting approval never hides
// behind a generic label (FR-64, FR-65).
func TestReviewContractLabelsTheActionByItsEffect(t *testing.T) {
	ctx := context.Background()

	for _, tc := range []struct {
		mode  ExecutionMode
		label string
		start bool
	}{
		{ExecutionStepThrough, "Approve and Create Tasks", false},
		{ExecutionAuto, "Approve and Start", true},
	} {
		service := reviewService(t)
		content := reviewableContent()
		content.Execution.Mode = tc.mode
		plan := newReviewablePlan(t, ctx, service, content)
		version, err := service.RequestReview(ctx, "ws-1", plan.ID, ReviewInput{Actor: "jj"})
		if err != nil {
			t.Fatalf("request review: %v", err)
		}

		contract, err := service.BuildReviewContract(ctx, "ws-1", plan.ID, version.Number, ValidationContext{})
		if err != nil {
			t.Fatalf("build contract: %v", err)
		}
		if contract.ActionLabel != tc.label {
			t.Errorf("mode %q action label = %q, want %q", tc.mode, contract.ActionLabel, tc.label)
		}
		if contract.StartsExecution != tc.start {
			t.Errorf("mode %q starts execution = %v, want %v", tc.mode, contract.StartsExecution, tc.start)
		}
		if tc.start && !strings.Contains(strings.Join(contract.Effects, " "), "Start running") {
			t.Errorf("an auto plan does not say work will start: %v", contract.Effects)
		}
	}
}

// An assignee that disappeared blocks approval until it is resolved, and the
// action is disabled with a reason rather than failing on click (FR-48).
func TestReviewContractBlocksApprovalForAnUnavailableAssignee(t *testing.T) {
	ctx := context.Background()
	service := reviewService(t)
	plan := newReviewablePlan(t, ctx, service, reviewableContent())
	version, _ := service.RequestReview(ctx, "ws-1", plan.ID, ReviewInput{Actor: "jj"})

	contract, err := service.BuildReviewContract(ctx, "ws-1", plan.ID, version.Number,
		ValidationContext{AvailableAgents: []string{"someone-else"}})
	if err != nil {
		t.Fatalf("build contract: %v", err)
	}
	if contract.Approvable {
		t.Error("a plan assigned to a missing agent reported itself approvable")
	}
	if len(contract.Blockers) == 0 || !strings.Contains(contract.Blockers[0], "builder") {
		t.Errorf("blockers do not name the missing agent: %v", contract.Blockers)
	}

	// With the agent present, it is approvable.
	ok, err := service.BuildReviewContract(ctx, "ws-1", plan.ID, version.Number,
		ValidationContext{AvailableAgents: []string{"builder"}})
	if err != nil {
		t.Fatalf("build contract: %v", err)
	}
	if !ok.Approvable || len(ok.Blockers) != 0 {
		t.Errorf("an available assignee still blocked approval: %v", ok.Blockers)
	}
}

// --- Revising approved work (FR-38, FR-39) ---------------------------------

func TestEditApprovedStartsAClassifiedDraftWithoutTouchingTheVersion(t *testing.T) {
	ctx := context.Background()
	service := reviewService(t)
	plan := newReviewablePlan(t, ctx, service, reviewableContent())
	version, _ := approveNow(t, ctx, service, plan, "key-1")

	// Mark the plan approved the way materialization would.
	if err := service.Store().SetVersionDecision(ctx, "ws-1", plan.ID, version.Number,
		VersionApproved, "jj", "", service.Now()); err != nil {
		t.Fatalf("mark approved: %v", err)
	}
	if _, err := service.Transition(ctx, "ws-1", plan.ID, TransitionInput{
		To: StatusApproved, Source: SourceUser, Actor: "jj",
	}); err != nil {
		t.Fatalf("transition to approved: %v", err)
	}

	// A revision must declare its intent (FR-39).
	if _, err := service.EditApproved(ctx, "ws-1", plan.ID, "", "jj"); !errors.Is(err, ErrValidation) {
		t.Errorf("an unclassified revision was accepted: %v", err)
	}

	revised, err := service.EditApproved(ctx, "ws-1", plan.ID, RevisionAdditive, "jj")
	if err != nil {
		t.Fatalf("edit approved: %v", err)
	}
	if revised.Status != StatusDraft {
		t.Errorf("status = %q, want draft", revised.Status)
	}
	if revised.DraftIntent != RevisionAdditive {
		t.Errorf("draft intent = %q, want additive", revised.DraftIntent)
	}
	// The approved version is untouched (FR-38).
	stored, _ := service.Store().GetVersion(ctx, "ws-1", plan.ID, version.Number)
	if stored.ContentHash != version.ContentHash || stored.Status != VersionApproved {
		t.Errorf("the approved version changed: %+v", stored)
	}
	if revised.ApprovedVersion != version.Number {
		t.Errorf("approved version = %d, want it retained", revised.ApprovedVersion)
	}
}

func TestEditApprovedRefusesAPlanThatWasNeverApproved(t *testing.T) {
	ctx := context.Background()
	service := reviewService(t)
	plan := newReviewablePlan(t, ctx, service, reviewableContent())

	_, err := service.EditApproved(ctx, "ws-1", plan.ID, RevisionAdditive, "jj")
	if !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("error = %v, want ErrInvalidTransition", err)
	}
}

// --- Activity (FR-80) ------------------------------------------------------

func TestReviewLifecycleIsRecordedInActivity(t *testing.T) {
	ctx := context.Background()
	service := reviewService(t)
	plan := newReviewablePlan(t, ctx, service, reviewableContent())

	version, _ := service.RequestReview(ctx, "ws-1", plan.ID, ReviewInput{Actor: "jj"})
	if _, err := service.RequestChanges(ctx, "ws-1", plan.ID, DecisionInput{Actor: "jj", Reason: "again"}); err != nil {
		t.Fatalf("request changes: %v", err)
	}
	second, _ := service.RequestReview(ctx, "ws-1", plan.ID, ReviewInput{Actor: "jj"})
	if _, err := service.Reject(ctx, "ws-1", plan.ID, DecisionInput{Actor: "jj", Reason: "no"}); err != nil {
		t.Fatalf("reject: %v", err)
	}
	third, _ := service.RequestReview(ctx, "ws-1", plan.ID, ReviewInput{Actor: "jj"})
	if _, err := service.Approve(ctx, "ws-1", plan.ID, ApprovalRequest{
		Version: third.Number, ContentHash: third.ContentHash,
		Effect: EffectCreateTasks, UserName: "jj", IdempotencyKey: "key-1",
	}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if err := service.InvalidateOutstandingApprovals(ctx, "ws-1", plan.ID, "later edit"); err != nil {
		t.Fatalf("invalidate: %v", err)
	}

	entries, err := service.Activity(ctx, "ws-1", plan.ID, 0)
	if err != nil {
		t.Fatalf("activity: %v", err)
	}

	seen := map[ActivityKind]int{}
	for _, entry := range entries {
		seen[entry.Kind]++
	}
	for _, kind := range []ActivityKind{
		ActivityReviewRequested, ActivityChangesRequested, ActivityRejected,
		ActivityApproved, ActivityApprovalInvalidated,
	} {
		if seen[kind] == 0 {
			t.Errorf("activity is missing a %s entry: %v", kind, seen)
		}
	}
	if seen[ActivityReviewRequested] != 3 {
		t.Errorf("review-requested entries = %d, want 3", seen[ActivityReviewRequested])
	}
	_ = version
	_ = second
}

// --- Restart (FR-71) -------------------------------------------------------

func TestApprovalRecordsSurviveRestart(t *testing.T) {
	ctx := context.Background()
	dbPath := fmt.Sprintf("%s/plans.db", t.TempDir())

	var (
		planID      string
		versionHash string
		approvalID  string
	)

	func() {
		db := openFileTestDB(t, ctx, dbPath)
		seedTestWorkspace(t, ctx, db, "ws-1")
		service := NewService(NewSQLiteStore(db))

		plan := newReviewablePlan(t, ctx, service, reviewableContent())
		planID = plan.ID
		version, approval := approveNow(t, ctx, service, plan, "key-1")
		versionHash = version.ContentHash
		approvalID = approval.ID
	}()

	db := openFileTestDB(t, ctx, dbPath)
	service := NewService(NewSQLiteStore(db))

	version, err := service.Store().GetVersion(ctx, "ws-1", planID, 1)
	if err != nil {
		t.Fatalf("version after restart: %v", err)
	}
	if version.ContentHash != versionHash {
		t.Errorf("content hash changed across restart: %q vs %q", version.ContentHash, versionHash)
	}

	approval, err := service.Store().GetApproval(ctx, "ws-1", planID, approvalID)
	if err != nil {
		t.Fatalf("approval after restart: %v", err)
	}
	if approval.ContentHash != versionHash || approval.Effect != EffectCreateTasks {
		t.Errorf("approval did not survive intact: %+v", approval)
	}
	if !approval.Usable() {
		t.Error("an unconsumed approval became unusable across restart")
	}

	// The contract rebuilt after restart still binds to the same hash.
	contract, err := service.BuildReviewContract(ctx, "ws-1", planID, 1, ValidationContext{})
	if err != nil {
		t.Fatalf("build contract after restart: %v", err)
	}
	if contract.ContentHash != versionHash {
		t.Errorf("contract hash changed across restart: %q", contract.ContentHash)
	}
}
