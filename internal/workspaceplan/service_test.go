package workspaceplan

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func newTestService(t *testing.T) (*Service, Store) {
	t.Helper()
	store := NewMemoryStore()
	return NewService(store), store
}

func TestServiceCreatePreservesTheExactInitiatingRequest(t *testing.T) {
	ctx := context.Background()
	service, _ := newTestService(t)

	request := "Plan the Q3 migration.\nIt must not touch billing."
	plan, err := service.Create(ctx, "ws-1", CreateInput{
		Request: request,
		Origin:  Origin{Kind: OriginUser, Actor: "jj"},
	})
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}

	if plan.OriginalRequest != request {
		t.Errorf("original request = %q, want the exact initiating text (FR-21)", plan.OriginalRequest)
	}
	if plan.Status != StatusDraft {
		t.Errorf("status = %q, want draft", plan.Status)
	}
	// A Plan existing is not a Plan being approved: nothing was materialized
	// and nothing was authorized (FR-20).
	if plan.ApprovedVersion != 0 || plan.CurrentVersion != 0 {
		t.Errorf("new plan already has versions: current=%d approved=%d", plan.CurrentVersion, plan.ApprovedVersion)
	}
	if len(plan.TaskLinks) != 0 || len(plan.RunLinks) != 0 {
		t.Error("creating a plan created linked work")
	}
	// The derived title comes from the first line and never replaces the
	// request itself.
	if plan.Title != "Plan the Q3 migration." {
		t.Errorf("derived title = %q", plan.Title)
	}
}

func TestServiceCreateRejectsAnEmptyRequestOrWorkspace(t *testing.T) {
	ctx := context.Background()
	service, _ := newTestService(t)

	if _, err := service.Create(ctx, "", CreateInput{Request: "something"}); !errors.Is(err, ErrValidation) {
		t.Errorf("create without workspace error = %v, want ErrValidation", err)
	}
	if _, err := service.Create(ctx, "ws-1", CreateInput{Request: "   "}); !errors.Is(err, ErrValidation) {
		t.Errorf("create without request error = %v, want ErrValidation", err)
	}
}

func TestServiceCreateDerivesATitleForALongRequest(t *testing.T) {
	ctx := context.Background()
	service, _ := newTestService(t)

	long := strings.TrimSpace(strings.Repeat("a very long request ", 20))
	plan, err := service.Create(ctx, "ws-1", CreateInput{Request: long})
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	if len([]rune(plan.Title)) > 81 {
		t.Errorf("derived title is %d runes, want it truncated", len([]rune(plan.Title)))
	}
	if plan.OriginalRequest != long {
		t.Error("truncating the title truncated the stored request")
	}
}

// Surrounding whitespace is the only normalization applied to the initiating
// request. Everything inside it — line breaks, indentation, the user's exact
// wording — is stored as written (FR-21).
func TestServiceCreateNormalizesOnlySurroundingWhitespace(t *testing.T) {
	ctx := context.Background()
	service, _ := newTestService(t)

	body := "Plan this.\n\n  - keep this indentation\n  - and this line break"
	plan, err := service.Create(ctx, "ws-1", CreateInput{Request: "\n\t" + body + "  \n"})
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	if plan.OriginalRequest != body {
		t.Errorf("original request = %q, want the interior preserved exactly", plan.OriginalRequest)
	}
}

func TestServiceTransitionValidatesAgainstTheTransitionTable(t *testing.T) {
	ctx := context.Background()
	service, _ := newTestService(t)
	plan := mustCreatePlan(t, ctx, service)

	// draft -> executing is not an edge: work cannot start without review and
	// approval (FR-14).
	_, err := service.Transition(ctx, "ws-1", plan.ID, TransitionInput{
		To: StatusExecuting, Source: SourceService,
	})
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("draft -> executing error = %v, want ErrInvalidTransition", err)
	}

	moved, err := service.Transition(ctx, "ws-1", plan.ID, TransitionInput{
		To: StatusInReview, Source: SourceUser, Actor: "jj",
	})
	if err != nil {
		t.Fatalf("draft -> in_review: %v", err)
	}
	if moved.Status != StatusInReview {
		t.Errorf("status = %q, want in_review", moved.Status)
	}
}

// Approval is a user action. No service, model, or execution source can reach
// the approved status, whatever it claims (FR-59, FR-60).
func TestServiceApprovalTransitionRequiresAnExplicitUserAction(t *testing.T) {
	ctx := context.Background()
	service, _ := newTestService(t)
	plan := mustCreatePlan(t, ctx, service)

	if _, err := service.Transition(ctx, "ws-1", plan.ID, TransitionInput{
		To: StatusInReview, Source: SourceUser, Actor: "jj",
	}); err != nil {
		t.Fatalf("move to review: %v", err)
	}

	for _, source := range []TransitionSource{SourceModel, SourceService, SourceExecution, SourceRetention} {
		_, err := service.Transition(ctx, "ws-1", plan.ID, TransitionInput{
			To: StatusApproved, Source: source, Actor: "an agent",
		})
		if !errors.Is(err, ErrApprovalAuthority) {
			t.Errorf("approval from source %q error = %v, want ErrApprovalAuthority", source, err)
		}
	}

	approved, err := service.Transition(ctx, "ws-1", plan.ID, TransitionInput{
		To: StatusApproved, Source: SourceUser, Actor: "jj",
	})
	if err != nil {
		t.Fatalf("user approval transition: %v", err)
	}
	if approved.Status != StatusApproved {
		t.Errorf("status = %q, want approved", approved.Status)
	}
}

func TestServiceTransitionsAppendToHistory(t *testing.T) {
	ctx := context.Background()
	service, _ := newTestService(t)
	plan := mustCreatePlan(t, ctx, service)

	if _, err := service.Transition(ctx, "ws-1", plan.ID, TransitionInput{
		To: StatusNeedsInput, Source: SourceModel, Reason: "missing target environment",
	}); err != nil {
		t.Fatalf("transition: %v", err)
	}

	entries, err := service.Activity(ctx, "ws-1", plan.ID, 0)
	if err != nil {
		t.Fatalf("activity: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("activity entries = %d, want 2 (created plus one transition)", len(entries))
	}
	if entries[0].Kind != ActivityCreated {
		t.Errorf("first entry kind = %q, want created", entries[0].Kind)
	}
	change := entries[1]
	if change.From != StatusDraft || change.To != StatusNeedsInput {
		t.Errorf("transition = %s -> %s, want draft -> needs_input", change.From, change.To)
	}
	if change.Source != SourceModel || change.Reason != "missing target environment" {
		t.Errorf("transition metadata not recorded: %+v", change)
	}
}

func TestServiceArchiveKeepsEffectsAndReopenRestores(t *testing.T) {
	ctx := context.Background()
	service, store := newTestService(t)
	plan := mustCreatePlan(t, ctx, service)

	version, err := store.CreateVersion(ctx, &Version{
		PlanID: plan.ID, WorkspaceID: "ws-1", ContentHash: "hash-1",
		Status: VersionInReview, CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("create version: %v", err)
	}

	archived, err := service.Archive(ctx, "ws-1", plan.ID, "inactive_30d", "system")
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if archived.ArchivedAt == nil || archived.ArchiveReason != "inactive_30d" {
		t.Fatalf("archive metadata = %+v", archived)
	}

	versions, err := store.ListVersions(ctx, "ws-1", plan.ID)
	if err != nil || len(versions) != 1 || versions[0].Number != version.Number {
		t.Fatalf("archiving lost review history: %v %+v", err, versions)
	}

	reopened, err := service.Reopen(ctx, "ws-1", plan.ID, "jj")
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if reopened.ArchivedAt != nil {
		t.Error("reopened plan is still archived")
	}
}

// A terminal Plan stays in History: reopening one would imply its old approval
// still authorizes work (FR-38, FR-74).
func TestServiceReopenRefusesTerminalPlans(t *testing.T) {
	ctx := context.Background()
	service, _ := newTestService(t)
	plan := mustCreatePlan(t, ctx, service)

	if _, err := service.Transition(ctx, "ws-1", plan.ID, TransitionInput{
		To: StatusCancelled, Source: SourceUser, Actor: "jj",
	}); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if _, err := service.Archive(ctx, "ws-1", plan.ID, "cancelled", "jj"); err != nil {
		t.Fatalf("archive: %v", err)
	}

	_, err := service.Reopen(ctx, "ws-1", plan.ID, "jj")
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("reopen cancelled plan error = %v, want ErrInvalidTransition", err)
	}
}

func TestServiceDeleteIsRefusedOnceWorkExists(t *testing.T) {
	ctx := context.Background()
	service, store := newTestService(t)
	plan := mustCreatePlan(t, ctx, service)

	if err := store.LinkTasks(ctx, "ws-1", plan.ID, []TaskLink{{
		PlanID: plan.ID, WorkspaceID: "ws-1", TaskID: "task-1", Version: 1,
		GroupID: "grp-1", ItemID: "itm-1", Role: LinkRoleItem, CreatedAt: time.Now().UTC(),
	}}); err != nil {
		t.Fatalf("link task: %v", err)
	}

	if err := service.Delete(ctx, "ws-1", plan.ID); !errors.Is(err, ErrPlanNotDeletable) {
		t.Fatalf("delete error = %v, want ErrPlanNotDeletable", err)
	}
	if _, err := service.Get(ctx, "ws-1", plan.ID); err != nil {
		t.Errorf("plan with materialized work was removed: %v", err)
	}
}

// Progress is derived, never persisted. A failing source degrades the read to
// "no summary" rather than to an error or a stale stored copy (FR-12).
func TestServiceProgressIsDerivedAndOptional(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	service := NewService(store, WithProgressSource(stubProgress{progress: Progress{Total: 3, Completed: 1}}))
	plan := mustCreatePlan(t, ctx, service)

	loaded, err := service.Get(ctx, "ws-1", plan.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if loaded.Progress == nil || loaded.Progress.Total != 3 || loaded.Progress.Completed != 1 {
		t.Fatalf("derived progress = %+v, want the source's values", loaded.Progress)
	}

	failing := NewService(store, WithProgressSource(stubProgress{err: errors.New("task store offline")}))
	degraded, err := failing.Get(ctx, "ws-1", plan.ID)
	if err != nil {
		t.Fatalf("get with failing progress source: %v", err)
	}
	if degraded.Progress != nil {
		t.Error("a failing progress source produced a progress value anyway")
	}
}

type stubProgress struct {
	progress Progress
	err      error
}

func (s stubProgress) PlanProgress(context.Context, *Plan) (Progress, error) {
	return s.progress, s.err
}

func mustCreatePlan(t *testing.T, ctx context.Context, service *Service) *Plan {
	t.Helper()
	plan, err := service.Create(ctx, "ws-1", CreateInput{
		Request: "Plan the migration",
		Origin:  Origin{Kind: OriginUser, Actor: "jj"},
	})
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	return plan
}
