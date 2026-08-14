package workspaceplan

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// fakeTaskWriter is an in-memory workspace store. It records how many times it
// saved, so a test can tell "wrote once" from "wrote twice with the same
// result".
type fakeTaskWriter struct {
	mu        sync.Mutex
	workspace *workspace.Workspace
	saves     int
	failSave  error
}

// testWorkspaceID is the one workspace these tests operate in. Cross-workspace
// isolation is covered by the store contract, which builds its own IDs.
const (
	testWorkspaceID = "ws-1"
	// testPlanID is the plan the store-contract seeds attach to.
	testPlanID = "plan-1"
)

func newFakeTaskWriter() *fakeTaskWriter {
	return &fakeTaskWriter{workspace: &workspace.Workspace{ID: testWorkspaceID}}
}

func (f *fakeTaskWriter) Get(id string) (*workspace.Workspace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.workspace == nil || f.workspace.ID != id {
		return nil, fmt.Errorf("workspace %s not found", id)
	}
	return f.workspace, nil
}

// Update models the real store's canonical read-modify-write: it holds the
// lock across the whole callback. A fake that did not would let a concurrency
// test pass against code that is broken in production.
func (f *fakeTaskWriter) Update(id string, fn func(*workspace.Workspace) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.workspace == nil || f.workspace.ID != id {
		return fmt.Errorf("workspace %s not found", id)
	}
	if err := fn(f.workspace); err != nil {
		return err
	}
	if f.failSave != nil {
		return f.failSave
	}
	f.saves++
	return nil
}

func (f *fakeTaskWriter) tasks() []workspace.Task {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]workspace.Task(nil), f.workspace.Tasks...)
}

// fakeArtifactWriter records writes and removals so compensation can be
// asserted.
type fakeArtifactWriter struct {
	mu       sync.Mutex
	written  map[string][]byte
	removed  []string
	failOn   string
	failWith error
}

func newFakeArtifactWriter() *fakeArtifactWriter {
	return &fakeArtifactWriter{written: map[string][]byte{}}
}

func (f *fakeArtifactWriter) WriteArtifact(_ context.Context, _ string, path string, content []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failOn != "" && strings.Contains(path, f.failOn) {
		if f.failWith != nil {
			return f.failWith
		}
		return errors.New("disk full")
	}
	f.written[path] = content
	return nil
}

func (f *fakeArtifactWriter) RemoveArtifact(_ context.Context, _ string, path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.written, path)
	f.removed = append(f.removed, path)
	return nil
}

func (f *fakeArtifactWriter) paths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	paths := make([]string, 0, len(f.written))
	for path := range f.written {
		paths = append(paths, path)
	}
	sortStrings(paths)
	return paths
}

func mustTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return parsed
}

// materializable builds a plan approved and ready to materialize.
func materializable(t *testing.T, ctx context.Context, content PlanContent, opts ...MaterializerOption) (
	*Service, *Materializer, *fakeTaskWriter, *Plan, *Approval) {
	t.Helper()

	service := reviewService(t)
	writer := newFakeTaskWriter()
	materializer := NewMaterializer(service, writer, opts...)

	plan := newReviewablePlan(t, ctx, service, content)
	_, approval := approveNow(t, ctx, service, plan, "key-1")
	return service, materializer, writer, plan, approval
}

func TestMaterializeCreatesTheApprovedTaskTree(t *testing.T) {
	ctx := context.Background()
	service, materializer, writer, plan, approval := materializable(t, ctx, reviewableContent())

	result, err := materializer.Materialize(ctx, "ws-1", plan.ID, MaterializeInput{ApprovalID: approval.ID})
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}

	// One group task plus two item tasks.
	if len(result.TaskIDs) != 3 {
		t.Fatalf("task ids = %d, want 3", len(result.TaskIDs))
	}
	tasks := writer.tasks()
	if len(tasks) != 3 {
		t.Fatalf("persisted tasks = %d, want 3", len(tasks))
	}
	if writer.saves != 1 {
		t.Errorf("saves = %d, want 1", writer.saves)
	}

	byDescription := map[string]workspace.Task{}
	for _, task := range tasks {
		byDescription[task.Description] = task
	}

	group := byDescription["Prepare"]
	if group.ID == "" || group.ParentTaskID != "" {
		t.Errorf("group task = %+v, want a root task", group)
	}
	snapshot := byDescription["Snapshot staging"]
	if snapshot.ParentTaskID != group.ID {
		t.Errorf("item is not parented to its group: %+v", snapshot)
	}
	if snapshot.To != "builder" {
		t.Errorf("assignee = %q, want builder", snapshot.To)
	}

	// The dependency became an input edge, which is the Task model's way of
	// saying "needs that result first" (FR-84).
	verify := byDescription["Verify checksums"]
	if len(verify.InputTaskIDs) != 1 || verify.InputTaskIDs[0] != snapshot.ID {
		t.Errorf("dependency did not compile to an input edge: %+v", verify.InputTaskIDs)
	}

	// An unassigned item stays unassigned rather than being given an agent
	// nobody approved (FR-86).
	if verify.To != "" {
		t.Errorf("unassigned item was assigned to %q", verify.To)
	}

	// The plan reaches approved only after the work is durable (FR-94).
	after, err := service.Get(ctx, "ws-1", plan.ID)
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	if after.Status != StatusApproved {
		t.Errorf("status = %q, want approved", after.Status)
	}
	if len(after.TaskLinks) != 3 {
		t.Errorf("task links = %d, want 3", len(after.TaskLinks))
	}
}

// Task provenance names the plan, version, item, approval, and approver, in
// both directions (FR-10, FR-87, FR-88).
func TestMaterializedTasksCarryPlanProvenance(t *testing.T) {
	ctx := context.Background()
	_, materializer, writer, plan, approval := materializable(t, ctx, reviewableContent())

	if _, err := materializer.Materialize(ctx, "ws-1", plan.ID,
		MaterializeInput{ApprovalID: approval.ID}); err != nil {
		t.Fatalf("materialize: %v", err)
	}

	for _, task := range writer.tasks() {
		provenance, ok := ProvenanceFromTaskContext(task.Context)
		if !ok {
			t.Fatalf("task %q carries no plan provenance", task.Description)
		}
		if provenance.PlanID != plan.ID || provenance.Version != 1 {
			t.Errorf("provenance = %+v, want plan %s v1", provenance, plan.ID)
		}
		if provenance.ApprovalID != approval.ID {
			t.Errorf("provenance approval = %q, want %q", provenance.ApprovalID, approval.ID)
		}
		if provenance.ApprovedBy != "jj" {
			t.Errorf("provenance approver = %q, want jj", provenance.ApprovedBy)
		}
		// Assignment provenance says the assignment came from an approved plan
		// version, whether the item was assigned or deliberately left
		// unassigned (FR-87).
		if !strings.Contains(task.AssignmentReason, "approved plan version 1") {
			t.Errorf("assignment reason = %q, want it to name the approved version", task.AssignmentReason)
		}
		if task.AssignedBy != "jj" {
			t.Errorf("assigned by = %q, want the approving user", task.AssignedBy)
		}
	}
}

// The same approval materialized twice produces one Task tree and replays the
// original result (FR-72, FR-73, FR-91, SM-2).
func TestMaterializeIsIdempotentOnRetry(t *testing.T) {
	ctx := context.Background()
	_, materializer, writer, plan, approval := materializable(t, ctx, reviewableContent())

	first, err := materializer.Materialize(ctx, "ws-1", plan.ID, MaterializeInput{ApprovalID: approval.ID})
	if err != nil {
		t.Fatalf("first materialize: %v", err)
	}
	second, err := materializer.Materialize(ctx, "ws-1", plan.ID, MaterializeInput{ApprovalID: approval.ID})
	if err != nil {
		t.Fatalf("retried materialize: %v", err)
	}

	if !second.Replayed {
		t.Error("the retry did the work again instead of replaying")
	}
	if len(second.TaskIDs) != len(first.TaskIDs) {
		t.Errorf("replayed task ids = %d, want %d", len(second.TaskIDs), len(first.TaskIDs))
	}
	for i := range first.TaskIDs {
		if second.TaskIDs[i] != first.TaskIDs[i] {
			t.Errorf("replayed task id %d = %q, want %q", i, second.TaskIDs[i], first.TaskIDs[i])
		}
	}
	if got := len(writer.tasks()); got != 3 {
		t.Errorf("tasks after retry = %d, want 3 — the retry duplicated work", got)
	}
	if writer.saves != 1 {
		t.Errorf("saves = %d, want 1 — the retry wrote again", writer.saves)
	}
}

// Concurrent materializations of one approval produce one Task tree (FR-178,
// SM-2).
func TestConcurrentMaterializationsProduceOneTaskTree(t *testing.T) {
	ctx := context.Background()
	_, materializer, writer, plan, approval := materializable(t, ctx, reviewableContent())

	const racers = 8
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		results  []*MaterializeResult
		failures []error
	)
	wg.Add(racers)
	for range racers {
		go func() {
			defer wg.Done()
			result, err := materializer.Materialize(ctx, "ws-1", plan.ID,
				MaterializeInput{ApprovalID: approval.ID})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failures = append(failures, err)
				return
			}
			results = append(results, result)
		}()
	}
	wg.Wait()

	if len(failures) > 0 {
		t.Fatalf("concurrent materializations failed: %v", failures)
	}
	if len(results) != racers {
		t.Fatalf("results = %d, want %d", len(results), racers)
	}

	// Every caller got the same answer.
	for _, result := range results {
		if len(result.TaskIDs) != 3 {
			t.Errorf("a caller got %d task ids, want 3", len(result.TaskIDs))
		}
	}
	// And exactly one tree exists.
	if got := len(writer.tasks()); got != 3 {
		t.Errorf("tasks = %d, want 3 — concurrency duplicated work", got)
	}
}

// Deterministic IDs are what make the retry safe, so they are asserted
// directly (FR-91).
func TestDeterministicTaskIDsDependOnlyOnApprovedIdentity(t *testing.T) {
	first := DeterministicTaskID("plan-1", 1, LinkRoleItem, "grp-1", "itm-1")
	again := DeterministicTaskID("plan-1", 1, LinkRoleItem, "grp-1", "itm-1")
	if first != again {
		t.Errorf("the same identity produced different ids: %s vs %s", first, again)
	}

	for _, other := range []string{
		DeterministicTaskID("plan-2", 1, LinkRoleItem, "grp-1", "itm-1"),
		DeterministicTaskID("plan-1", 2, LinkRoleItem, "grp-1", "itm-1"),
		DeterministicTaskID("plan-1", 1, LinkRoleGroup, "grp-1", "itm-1"),
		DeterministicTaskID("plan-1", 1, LinkRoleItem, "grp-2", "itm-1"),
		DeterministicTaskID("plan-1", 1, LinkRoleItem, "grp-1", "itm-2"),
	} {
		if other == first {
			t.Errorf("a different identity produced the same id: %s", other)
		}
	}
}

// Availability at review time is not availability at materialization time
// (FR-85).
func TestMaterializeRevalidatesAssigneesImmediatelyBeforeWriting(t *testing.T) {
	ctx := context.Background()
	_, materializer, writer, plan, approval := materializable(t, ctx, reviewableContent())

	// The plan assigns "builder"; by now the workspace only has someone else.
	_, err := materializer.Materialize(ctx, "ws-1", plan.ID, MaterializeInput{
		ApprovalID: approval.ID,
		Validation: ValidationContext{AvailableAgents: []string{"someone-else"}},
	})
	if !errors.Is(err, ErrUnavailableCapability) {
		t.Fatalf("error = %v, want ErrUnavailableCapability", err)
	}
	if len(writer.tasks()) != 0 {
		t.Error("tasks were created despite an unavailable assignee")
	}
}

// A failed write must never be reported as created work (FR-99).
func TestMaterializeReportsAFailedPersistRatherThanClaimingSuccess(t *testing.T) {
	ctx := context.Background()
	service, materializer, writer, plan, approval := materializable(t, ctx, reviewableContent())
	writer.failSave = errors.New("disk exploded")

	_, err := materializer.Materialize(ctx, "ws-1", plan.ID, MaterializeInput{ApprovalID: approval.ID})
	if err == nil {
		t.Fatal("a failed persist was reported as success")
	}
	if !strings.Contains(err.Error(), "disk exploded") {
		t.Errorf("error does not name the cause: %v", err)
	}

	// The approval is still usable, so the user has a retry path (FR-99).
	stored, getErr := service.Store().GetApproval(ctx, "ws-1", plan.ID, approval.ID)
	if getErr != nil {
		t.Fatalf("get approval: %v", getErr)
	}
	if stored.Consumed() {
		t.Error("a failed materialization consumed the approval")
	}
	after, _ := service.Get(ctx, "ws-1", plan.ID)
	if after.Status == StatusApproved {
		t.Error("the plan moved to approved despite a failed write")
	}

	// Retrying after the fault clears succeeds.
	writer.failSave = nil
	result, err := materializer.Materialize(ctx, "ws-1", plan.ID, MaterializeInput{ApprovalID: approval.ID})
	if err != nil {
		t.Fatalf("retry after failure: %v", err)
	}
	if len(result.TaskIDs) != 3 {
		t.Errorf("retry produced %d tasks, want 3", len(result.TaskIDs))
	}
}

// --- Artifacts (FR-95 through FR-98) ---------------------------------------

func contentWithArtifacts() PlanContent {
	content := reviewableContent()
	content.Artifacts = []ProposedArtifact{
		{ID: "art-1", Kind: ArtifactPRD, Path: "tasks/prd-migration.md", Enabled: true},
		{ID: "art-2", Kind: ArtifactTaskList, Path: "tasks/tasks-migration.md", Enabled: true},
		{ID: "art-3", Kind: ArtifactNote, Path: "notes/skipped.md", Enabled: false},
	}
	return content
}

func TestMaterializeWritesEnabledArtifactsOnly(t *testing.T) {
	ctx := context.Background()
	artifacts := newFakeArtifactWriter()
	_, materializer, _, plan, approval := materializable(t, ctx, contentWithArtifacts(),
		WithArtifactWriter(artifacts))

	result, err := materializer.Materialize(ctx, "ws-1", plan.ID, MaterializeInput{ApprovalID: approval.ID})
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}

	written := artifacts.paths()
	if len(written) != 2 {
		t.Fatalf("written artifacts = %v, want the two enabled ones", written)
	}
	if len(result.ArtifactPaths) != 2 {
		t.Errorf("result paths = %v", result.ArtifactPaths)
	}
	// A disabled artifact is not written.
	for _, path := range written {
		if strings.Contains(path, "skipped") {
			t.Errorf("a disabled artifact was written: %s", path)
		}
	}

	// The PRD is a faithful projection of the approved version: objective,
	// scope, and the exact version it came from.
	prd := string(artifacts.written["tasks/prd-migration.md"])
	for _, want := range []string{"Migrate reporting safely", plan.ID, "reporting", "billing"} {
		if !strings.Contains(prd, want) {
			t.Errorf("PRD omits %q", want)
		}
	}
	// The task breakdown belongs to the task-list artifact, not the PRD.
	taskList := string(artifacts.written["tasks/tasks-migration.md"])
	for _, want := range []string{"Snapshot staging", "Verify checksums", "builder"} {
		if !strings.Contains(taskList, want) {
			t.Errorf("task list omits %q", want)
		}
	}

	// Both say plainly that editing them does not change the plan, because a
	// generated projection that reads like a source of truth invites edits
	// that silently go nowhere.
	for name, content := range map[string]string{"PRD": prd, "task list": taskList} {
		if !strings.Contains(content, "do not change the plan") {
			t.Errorf("the %s does not disclaim that edits feed back into the plan", name)
		}
		// Each names the exact version it projects, so a file on disk can be
		// traced back to what was approved.
		if !strings.Contains(content, "**Version:** 1") {
			t.Errorf("the %s does not name the version it came from", name)
		}
	}
}

// The same approved version always renders byte-identical documents, so a
// retried materialization cannot produce a different file (FR-96).
func TestArtifactRenderingIsDeterministic(t *testing.T) {
	ctx := context.Background()
	service := reviewService(t)
	plan := newReviewablePlan(t, ctx, service, contentWithArtifacts())
	version, _ := service.RequestReview(ctx, "ws-1", plan.ID, ReviewInput{Actor: "jj"})

	renderer := DefaultArtifactRenderer{}
	for _, artifact := range version.Content.EnabledArtifacts() {
		first, err := renderer.Render(artifact, plan, version)
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		for range 5 {
			again, err := renderer.Render(artifact, plan, version)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			if string(again) != string(first) {
				t.Fatalf("artifact %s rendered differently between runs", artifact.Path)
			}
		}
	}
}

// A partial document set must not survive a failed materialization (FR-90).
func TestArtifactFailureCompensatesWhatWasWritten(t *testing.T) {
	ctx := context.Background()
	artifacts := newFakeArtifactWriter()
	artifacts.failOn = "tasks-migration"
	service, materializer, _, plan, approval := materializable(t, ctx, contentWithArtifacts(),
		WithArtifactWriter(artifacts))

	_, err := materializer.Materialize(ctx, "ws-1", plan.ID, MaterializeInput{ApprovalID: approval.ID})
	if err == nil {
		t.Fatal("a failed artifact write was reported as success")
	}

	if len(artifacts.paths()) != 0 {
		t.Errorf("a partial document set survived: %v", artifacts.paths())
	}
	if len(artifacts.removed) == 0 {
		t.Error("the successfully written artifact was not compensated")
	}
	// The approval stays usable so the user can retry (FR-99).
	stored, _ := service.Store().GetApproval(ctx, "ws-1", plan.ID, approval.ID)
	if stored.Consumed() {
		t.Error("a failed materialization consumed the approval")
	}
}

// Approving "write the PRD" and getting no PRD is not a success.
func TestMaterializeRefusesEnabledArtifactsWithNoWriter(t *testing.T) {
	ctx := context.Background()
	_, materializer, writer, plan, approval := materializable(t, ctx, contentWithArtifacts())

	_, err := materializer.Materialize(ctx, "ws-1", plan.ID, MaterializeInput{ApprovalID: approval.ID})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("error = %v, want ErrValidation", err)
	}
	if len(writer.tasks()) != 0 {
		t.Error("tasks were created for a plan whose artifacts could not be written")
	}
}

// An unsafe path fails before anything is committed (FR-97).
func TestMaterializeRefusesAnUnsafeArtifactPathBeforeWriting(t *testing.T) {
	ctx := context.Background()
	content := reviewableContent()
	content.Artifacts = []ProposedArtifact{{
		ID: "art-1", Kind: ArtifactNote, Path: "notes/plan.md", Enabled: true,
	}}

	service := reviewService(t)
	writer := newFakeTaskWriter()
	artifacts := newFakeArtifactWriter()
	materializer := NewMaterializer(service, writer, WithArtifactWriter(artifacts))

	plan := newReviewablePlan(t, ctx, service, content)
	version, err := service.RequestReview(ctx, "ws-1", plan.ID, ReviewInput{Actor: "jj"})
	if err != nil {
		t.Fatalf("request review: %v", err)
	}

	// Corrupt the stored version's path the way a compromised or buggy writer
	// might, to prove the write boundary re-checks rather than trusting review.
	version.Content.Artifacts[0].Path = "../../escape.md"
	staged, stageErr := materializer.stageArtifacts(plan, version)
	if !errors.Is(stageErr, ErrUnsafePath) {
		t.Fatalf("staging error = %v, want ErrUnsafePath", stageErr)
	}
	if len(staged) != 0 {
		t.Error("an unsafe artifact was staged")
	}
	if len(artifacts.paths()) != 0 {
		t.Error("an unsafe artifact was written")
	}
}

func TestNormalizeArtifactPathRejectsEscapes(t *testing.T) {
	for _, path := range []string{
		"../outside.md", "docs/../../outside.md", "/etc/passwd", "", "docs/",
	} {
		if _, err := NormalizeArtifactPath(path); err == nil {
			t.Errorf("unsafe path %q was normalized rather than refused", path)
		}
	}

	cleaned, err := NormalizeArtifactPath("tasks/./prd.md")
	if err != nil {
		t.Fatalf("safe path refused: %v", err)
	}
	if cleaned != "tasks/prd.md" {
		t.Errorf("normalized = %q, want tasks/prd.md", cleaned)
	}
}

// --- Approval state --------------------------------------------------------

func TestMaterializeRefusesAnInvalidatedApproval(t *testing.T) {
	ctx := context.Background()
	service, materializer, writer, plan, approval := materializable(t, ctx, reviewableContent())

	if err := service.InvalidateOutstandingApprovals(ctx, "ws-1", plan.ID, "scope changed"); err != nil {
		t.Fatalf("invalidate: %v", err)
	}

	_, err := materializer.Materialize(ctx, "ws-1", plan.ID, MaterializeInput{ApprovalID: approval.ID})
	if !errors.Is(err, ErrApprovalMismatch) {
		t.Fatalf("error = %v, want ErrApprovalMismatch", err)
	}
	if len(writer.tasks()) != 0 {
		t.Error("an invalidated approval created work")
	}
}

// The move to approved needs an actual consumed approval, not a claim of user
// authority. This is what keeps compiled service code from being able to
// approve a plan by asserting it is a user (FR-59, FR-94).
func TestApprovalTransitionRequiresEvidenceNotAClaim(t *testing.T) {
	consumedAt := mustTime("2026-08-13T10:00:00Z")
	consumed := &Approval{
		ID: "apr-1", PlanID: "plan-1", Version: 1,
		Effect: EffectCreateTasks, ConsumedAt: &consumedAt,
	}

	// A real consumed approval authorizes the move.
	if err := ValidateApprovalTransition(StatusInReview, StatusApproved, consumed, "plan-1", 1); err != nil {
		t.Errorf("a consumed approval was refused: %v", err)
	}

	// Nothing else does.
	cases := map[string]*Approval{
		"no approval at all": nil,
		"an unconsumed approval": {
			ID: "apr-2", PlanID: "plan-1", Version: 1, Effect: EffectCreateTasks,
		},
		"an approval for another plan": {
			ID: "apr-3", PlanID: "plan-other", Version: 1,
			Effect: EffectCreateTasks, ConsumedAt: &consumedAt,
		},
		"an approval for another version": {
			ID: "apr-4", PlanID: "plan-1", Version: 2,
			Effect: EffectCreateTasks, ConsumedAt: &consumedAt,
		},
	}
	for name, approval := range cases {
		if err := ValidateApprovalTransition(StatusInReview, StatusApproved, approval, "plan-1", 1); err == nil {
			t.Errorf("%s authorized the approval transition", name)
		}
	}

	// An invalidated approval, even consumed, does not authorize it.
	invalidatedAt := consumedAt
	invalidated := &Approval{
		ID: "apr-5", PlanID: "plan-1", Version: 1, Effect: EffectCreateTasks,
		ConsumedAt: &consumedAt, InvalidatedAt: &invalidatedAt,
	}
	if err := ValidateApprovalTransition(StatusInReview, StatusApproved, invalidated, "plan-1", 1); err == nil {
		t.Error("an invalidated approval authorized the transition")
	}

	// Non-approval edges are unaffected and still go through the normal table.
	if err := ValidateApprovalTransition(StatusApproved, StatusExecuting, nil, "plan-1", 1); err != nil {
		t.Errorf("an ordinary transition was refused: %v", err)
	}
	if err := ValidateApprovalTransition(StatusDraft, StatusExecuting, nil, "plan-1", 1); err == nil {
		t.Error("an edge outside the table was allowed")
	}
}

// A plan cancelled mid-materialization reports the conflict rather than
// silently leaving the caller believing the plan is approved.
func TestMaterializeReportsWhenThePlanMovedUnderIt(t *testing.T) {
	ctx := context.Background()
	service, materializer, writer, plan, approval := materializable(t, ctx, reviewableContent())

	// Cancel the plan after approval but before materialization.
	if _, err := service.Transition(ctx, "ws-1", plan.ID, TransitionInput{
		To: StatusCancelled, Source: SourceUser, Actor: "jj",
	}); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	_, err := materializer.Materialize(ctx, "ws-1", plan.ID, MaterializeInput{ApprovalID: approval.ID})
	if err == nil {
		t.Fatal("materializing into a cancelled plan reported success")
	}
	if !strings.Contains(err.Error(), "could not move the plan to approved") {
		t.Errorf("error does not explain the conflict: %v", err)
	}
	// The work and the spent approval are real; only the status move failed.
	if len(writer.tasks()) != 3 {
		t.Errorf("tasks = %d, want the created work retained for inspection", len(writer.tasks()))
	}
}

func TestMaterializeRecordsConsumptionInActivity(t *testing.T) {
	ctx := context.Background()
	service, materializer, _, plan, approval := materializable(t, ctx, reviewableContent())

	if _, err := materializer.Materialize(ctx, "ws-1", plan.ID,
		MaterializeInput{ApprovalID: approval.ID}); err != nil {
		t.Fatalf("materialize: %v", err)
	}

	entries, err := service.Activity(ctx, "ws-1", plan.ID, 0)
	if err != nil {
		t.Fatalf("activity: %v", err)
	}
	seen := map[ActivityKind]bool{}
	for _, entry := range entries {
		seen[entry.Kind] = true
	}
	for _, kind := range []ActivityKind{ActivityApprovalConsumed, ActivityMaterialized} {
		if !seen[kind] {
			t.Errorf("activity is missing a %s entry", kind)
		}
	}
}

// An auto plan's result says work should start; a step-through plan's does not
// (FR-102, FR-103).
func TestMaterializeReportsWhetherExecutionShouldStart(t *testing.T) {
	ctx := context.Background()

	stepThrough := reviewableContent()
	_, materializer, _, plan, approval := materializable(t, ctx, stepThrough)
	result, err := materializer.Materialize(ctx, "ws-1", plan.ID, MaterializeInput{ApprovalID: approval.ID})
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if result.StartExecution {
		t.Error("a step_through plan asked to start execution")
	}

	auto := reviewableContent()
	auto.Execution.Mode = ExecutionAuto
	service := reviewService(t)
	writer := newFakeTaskWriter()
	autoMaterializer := NewMaterializer(service, writer)
	autoPlan := newReviewablePlan(t, ctx, service, auto)
	version, err := service.RequestReview(ctx, "ws-1", autoPlan.ID, ReviewInput{Actor: "jj"})
	if err != nil {
		t.Fatalf("request review: %v", err)
	}
	autoApproval, err := service.Approve(ctx, "ws-1", autoPlan.ID, ApprovalRequest{
		Version: version.Number, ContentHash: version.ContentHash,
		Effect: EffectCreateTasksAndStart, UserName: "jj", IdempotencyKey: "auto-key",
	})
	if err != nil {
		t.Fatalf("approve auto plan: %v", err)
	}

	autoResult, err := autoMaterializer.Materialize(ctx, "ws-1", autoPlan.ID,
		MaterializeInput{ApprovalID: autoApproval.ID})
	if err != nil {
		t.Fatalf("materialize auto plan: %v", err)
	}
	if !autoResult.StartExecution {
		t.Error("an auto plan did not ask to start execution")
	}
}

// Group dependencies compile to edges the Task graph validator understands, so
// a cyclic plan cannot reach Tasks even if it somehow reached approval.
func TestMaterializeCompilesGroupDependencies(t *testing.T) {
	ctx := context.Background()
	content := reviewableContent()
	content.Groups = append(content.Groups, TaskGroup{
		ID: "grp-2", Title: "Cut over", DependsOn: []string{"grp-1"},
		Items: []TaskItem{{ID: "itm-3", Description: "Switch traffic"}},
	})

	_, materializer, writer, plan, approval := materializable(t, ctx, content)
	if _, err := materializer.Materialize(ctx, "ws-1", plan.ID,
		MaterializeInput{ApprovalID: approval.ID}); err != nil {
		t.Fatalf("materialize: %v", err)
	}

	var cutOver, prepare workspace.Task
	for _, task := range writer.tasks() {
		switch task.Description {
		case "Cut over":
			cutOver = task
		case "Prepare":
			prepare = task
		}
	}
	if len(cutOver.InputTaskIDs) != 1 || cutOver.InputTaskIDs[0] != prepare.ID {
		t.Errorf("group dependency did not compile: %+v", cutOver.InputTaskIDs)
	}
}
