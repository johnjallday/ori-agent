package workspaceplan

import (
	"context"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// --- What ages out, and what does not (FR-16) ------------------------------

func agedPlan(status Status, age time.Duration) *Plan {
	return &Plan{
		ID:             "plan-1",
		WorkspaceID:    testWorkspaceID,
		Status:         status,
		LastActivityAt: time.Now().UTC().Add(-age),
	}
}

func TestInactiveDraftsAgeIntoHistory(t *testing.T) {
	now := time.Now().UTC()
	for _, status := range []Status{StatusDraft, StatusNeedsInput} {
		plan := agedPlan(status, InactiveRetention+time.Hour)
		if !ShouldArchiveForInactivity(plan, now) {
			t.Errorf("a %s plan idle past the retention window did not age out", status)
		}
	}
}

func TestRecentDraftsStay(t *testing.T) {
	now := time.Now().UTC()
	plan := agedPlan(StatusDraft, InactiveRetention-time.Hour)
	if ShouldArchiveForInactivity(plan, now) {
		t.Error("a draft inside the retention window was archived")
	}
}

// Only unstarted thinking ages out. A plan that was approved, is executing, or
// has finished is placed by what happened to it — archiving an executing plan
// because a month passed would hide running work.
func TestStartedWorkNeverAgesOut(t *testing.T) {
	now := time.Now().UTC()
	for _, status := range []Status{
		StatusInReview, StatusApproved, StatusExecuting,
		StatusPaused, StatusCompleted, StatusFailed,
	} {
		plan := agedPlan(status, 10*InactiveRetention)
		if ShouldArchiveForInactivity(plan, now) {
			t.Errorf("a %s plan was archived for inactivity", status)
		}
	}
}

// An unset timestamp means "unknown", not "ancient". Treating it as thirty days
// old would archive plans created by a path that forgot to stamp it.
func TestUnknownActivityIsNotTreatedAsStale(t *testing.T) {
	plan := &Plan{ID: "plan-1", Status: StatusDraft}
	if ShouldArchiveForInactivity(plan, time.Now().UTC()) {
		t.Error("a plan with no recorded activity was archived")
	}
}

func TestAlreadyArchivedPlansAreLeftAlone(t *testing.T) {
	archivedAt := time.Now().UTC()
	plan := agedPlan(StatusDraft, 10*InactiveRetention)
	plan.ArchivedAt = &archivedAt
	if ShouldArchiveForInactivity(plan, time.Now().UTC()) {
		t.Error("an archived plan was archived again")
	}
}

// --- Archiving is placement, never deletion --------------------------------

// The whole content survives the move. A user who let a draft go stale for a
// month must find it intact, not discover the app tidied it away.
func TestArchivingKeepsEveryVersionAndApproval(t *testing.T) {
	ctx := context.Background()
	service := reviewService(t)
	plan := newReviewablePlan(t, ctx, service, reviewableContent())
	version, approval := approveNow(t, ctx, service, plan, "keep-1")

	if _, err := service.Archive(ctx, "ws-1", plan.ID, "aging out", "system"); err != nil {
		t.Fatalf("archive: %v", err)
	}

	archived, err := service.Get(ctx, "ws-1", plan.ID)
	if err != nil {
		t.Fatalf("read archived plan: %v", err)
	}
	if archived.ArchivedAt == nil {
		t.Fatal("the plan was not archived")
	}
	// Draft content, versions, and approvals all survive.
	if len(archived.Draft.Groups) == 0 {
		t.Error("archiving lost the draft content")
	}
	if _, err := service.Store().GetVersion(ctx, "ws-1", plan.ID, version.Number); err != nil {
		t.Errorf("archiving lost version %d: %v", version.Number, err)
	}
	if _, err := service.Store().GetApproval(ctx, "ws-1", plan.ID, approval.ID); err != nil {
		t.Errorf("archiving lost the approval: %v", err)
	}
}

// Listing applies retention, so an aged draft leaves the active list without
// any sweep having to run.
func TestListingArchivesAgedDrafts(t *testing.T) {
	ctx := context.Background()
	service := reviewService(t)
	plan := mustCreatePlan(t, ctx, service)

	// Age it past the window by writing the timestamp directly; the point is
	// the retention rule, not how the clock got there.
	stored, err := service.Store().GetPlan(ctx, "ws-1", plan.ID)
	if err != nil {
		t.Fatalf("read plan: %v", err)
	}
	stored.LastActivityAt = time.Now().UTC().Add(-InactiveRetention - time.Hour)

	if n := service.ArchiveInactive(ctx, "ws-1", []*Plan{stored}); n != 1 {
		t.Fatalf("archived %d plan(s), want 1", n)
	}

	active, err := service.List(ctx, "ws-1", ListFilter{Scope: ScopeActive})
	if err != nil {
		t.Fatalf("list active: %v", err)
	}
	for _, listed := range active {
		if listed.ID == plan.ID {
			t.Error("an aged draft is still in the active list")
		}
	}

	history, err := service.List(ctx, "ws-1", ListFilter{Scope: ScopeHistory})
	if err != nil {
		t.Fatalf("list history: %v", err)
	}
	var found bool
	for _, listed := range history {
		if listed.ID == plan.ID {
			found = true
		}
	}
	if !found {
		t.Error("the aged draft is not in history either; it vanished")
	}
}

// --- Cancelled plans go to History immediately (FR-16) ---------------------

func TestCancellingMovesAPlanToHistoryImmediately(t *testing.T) {
	ctx := context.Background()
	service, executor, writer, _, plan := executable(t, ctx)
	_ = writer

	cancelled, err := executor.Cancel(ctx, "ws-1", plan.ID, "changed my mind", "jj")
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if cancelled.Status != StatusCancelled {
		t.Fatalf("status = %q, want cancelled", cancelled.Status)
	}
	if cancelled.ArchivedAt == nil {
		t.Error("a cancelled plan was left in the active list")
	}

	// And its tasks are still there, cancelled rather than deleted.
	reread, err := service.Get(ctx, "ws-1", plan.ID)
	if err != nil {
		t.Fatalf("reread: %v", err)
	}
	if len(reread.TaskLinks) == 0 {
		t.Error("cancelling lost the plan's task links")
	}
	for _, task := range writer.tasks() {
		if task.Status != workspace.TaskStatusCancelled && task.Status != workspace.TaskStatusPending {
			t.Errorf("task %q ended in %q", task.Description, task.Status)
		}
	}
}
