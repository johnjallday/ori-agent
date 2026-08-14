package workspaceplan

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
)

// The Plan store has two implementations and one set of guarantees. Running the
// same suite against both is what keeps the in-memory store honest: a unit test
// that passes against a looser fake would be proving nothing about the store
// that actually ships.
func forEachStore(t *testing.T, run func(t *testing.T, ctx context.Context, store Store, seedWorkspace func(id string))) {
	t.Helper()

	t.Run("memory", func(t *testing.T) {
		ctx := context.Background()
		run(t, ctx, NewMemoryStore(), func(string) {})
	})

	t.Run("sqlite", func(t *testing.T) {
		ctx := context.Background()
		db := openPlanTestDB(t, ctx)
		run(t, ctx, NewSQLiteStore(db), func(id string) {
			seedTestWorkspace(t, ctx, db, id)
		})
	})
}

// openFileTestDB opens a file-backed database, which is the only way to prove
// something survives a restart: an :memory: database dies with its connection.
func openFileTestDB(t *testing.T, ctx context.Context, path string) *database.DB {
	t.Helper()
	db, err := database.Open(ctx, &database.Config{Path: path, WALMode: false})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func openPlanTestDB(t *testing.T, ctx context.Context) *database.DB {
	t.Helper()
	db, err := database.Open(ctx, &database.Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func seedTestWorkspace(t *testing.T, ctx context.Context, db *database.DB, id string) {
	t.Helper()
	now := time.Now().UTC()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO workspaces (id, name, created_at, updated_at)
		VALUES (?, ?, ?, ?)
	`, id, "Test Workspace", now, now); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
}

func testPlan(planID string) *Plan {
	now := time.Now().UTC().Truncate(time.Millisecond)
	return &Plan{
		ID:              planID,
		WorkspaceID:     testWorkspaceID,
		Title:           "Ship the thing",
		OriginalRequest: "Please plan how we ship the thing.",
		Objective:       "Ship the thing safely",
		Status:          StatusDraft,
		Origin:          Origin{Kind: OriginUser, Actor: "jj"},
		CreatedAt:       now,
		UpdatedAt:       now,
		LastActivityAt:  now,
		Draft: PlanContent{
			InScope:   []string{"the thing"},
			NonGoals:  []string{"the other thing"},
			Execution: ExecutionPolicy{Mode: ExecutionStepThrough},
			Groups: []TaskGroup{{
				ID:      "grp-1",
				Title:   "Build",
				Outcome: "The thing exists",
				Items: []TaskItem{{
					ID:             "itm-1",
					Description:    "Write the code",
					ExpectedResult: "Tests pass",
				}},
			}},
		},
	}
}

func TestStoreCreateGetListRoundTrip(t *testing.T) {
	forEachStore(t, func(t *testing.T, ctx context.Context, store Store, seed func(string)) {
		seed("ws-1")
		plan := testPlan("plan-1")
		if err := store.CreatePlan(ctx, plan); err != nil {
			t.Fatalf("create plan: %v", err)
		}

		loaded, err := store.GetPlan(ctx, "ws-1", "plan-1")
		if err != nil {
			t.Fatalf("get plan: %v", err)
		}
		// The exact initiating request survives verbatim; nothing summarizes
		// over it (FR-21).
		if loaded.OriginalRequest != plan.OriginalRequest {
			t.Errorf("original request = %q, want %q", loaded.OriginalRequest, plan.OriginalRequest)
		}
		if loaded.Status != StatusDraft {
			t.Errorf("status = %q, want draft", loaded.Status)
		}
		if len(loaded.Draft.Groups) != 1 || loaded.Draft.Groups[0].Items[0].ID != "itm-1" {
			t.Fatalf("draft content did not round-trip: %+v", loaded.Draft.Groups)
		}
		if loaded.Progress != nil {
			t.Error("progress was persisted; it must be derived from live Tasks and Runs (FR-12)")
		}

		active, err := store.ListPlans(ctx, "ws-1", ListFilter{})
		if err != nil {
			t.Fatalf("list plans: %v", err)
		}
		if len(active) != 1 {
			t.Fatalf("active plans = %d, want 1", len(active))
		}
	})
}

// A Plan ID from another workspace must read as missing, not as forbidden, so
// one workspace cannot probe another's ID space (FR-163, FR-167).
func TestStoreScopesEveryReadToTheOwningWorkspace(t *testing.T) {
	forEachStore(t, func(t *testing.T, ctx context.Context, store Store, seed func(string)) {
		seed("ws-1")
		seed("ws-2")
		if err := store.CreatePlan(ctx, testPlan("plan-1")); err != nil {
			t.Fatalf("create plan: %v", err)
		}

		if _, err := store.GetPlan(ctx, "ws-2", "plan-1"); !errors.Is(err, ErrPlanNotFound) {
			t.Errorf("cross-workspace get error = %v, want ErrPlanNotFound", err)
		}
		if err := store.ArchivePlan(ctx, "ws-2", "plan-1", "nope", time.Now().UTC()); !errors.Is(err, ErrPlanNotFound) {
			t.Errorf("cross-workspace archive error = %v, want ErrPlanNotFound", err)
		}
		if _, err := store.UpdatePlanDraft(ctx, "ws-2", "plan-1", 0, DraftUpdate{}); !errors.Is(err, ErrPlanNotFound) {
			t.Errorf("cross-workspace draft write error = %v, want ErrPlanNotFound", err)
		}
		plans, err := store.ListPlans(ctx, "ws-2", ListFilter{Scope: ScopeAll})
		if err != nil {
			t.Fatalf("list plans: %v", err)
		}
		if len(plans) != 0 {
			t.Errorf("other workspace listed %d plans, want 0", len(plans))
		}
	})
}

// Every dependent read must distinguish "there is no such Plan here" from
// "that Plan has nothing yet". Returning an empty list for an unknown or
// cross-workspace ID answers a question that was not asked and quietly confirms
// nothing is wrong (FR-163, FR-167).
func TestStoreDependentReadsRejectUnknownAndCrossWorkspacePlans(t *testing.T) {
	forEachStore(t, func(t *testing.T, ctx context.Context, store Store, seed func(string)) {
		seed("ws-1")
		seed("ws-2")
		if err := store.CreatePlan(ctx, testPlan("plan-1")); err != nil {
			t.Fatalf("create plan: %v", err)
		}

		reads := map[string]func(workspaceID, planID string) error{
			"GetPlan": func(workspaceID, planID string) error {
				_, err := store.GetPlan(ctx, workspaceID, planID)
				return err
			},
			"ListVersions": func(workspaceID, planID string) error {
				_, err := store.ListVersions(ctx, workspaceID, planID)
				return err
			},
			"ListApprovals": func(workspaceID, planID string) error {
				_, err := store.ListApprovals(ctx, workspaceID, planID)
				return err
			},
			"ListActivity": func(workspaceID, planID string) error {
				_, err := store.ListActivity(ctx, workspaceID, planID, 0)
				return err
			},
			"ListDraftSnapshots": func(workspaceID, planID string) error {
				_, err := store.ListDraftSnapshots(ctx, workspaceID, planID)
				return err
			},
		}

		for name, read := range reads {
			if err := read("ws-1", "no-such-plan"); !errors.Is(err, ErrPlanNotFound) {
				t.Errorf("%s on an unknown plan returned %v, want ErrPlanNotFound", name, err)
			}
			if err := read("ws-2", "plan-1"); !errors.Is(err, ErrPlanNotFound) {
				t.Errorf("%s across workspaces returned %v, want ErrPlanNotFound", name, err)
			}
		}
	})
}

// Two browser sessions editing the same draft: the second write is refused
// rather than silently overwriting the first (FR-30).
func TestStoreDraftWritesUseOptimisticConcurrency(t *testing.T) {
	forEachStore(t, func(t *testing.T, ctx context.Context, store Store, seed func(string)) {
		seed("ws-1")
		if err := store.CreatePlan(ctx, testPlan("plan-1")); err != nil {
			t.Fatalf("create plan: %v", err)
		}

		now := time.Now().UTC()
		revision, err := store.UpdatePlanDraft(ctx, "ws-1", "plan-1", 0, DraftUpdate{
			Title:     "First edit",
			Objective: "First objective",
			Content:   PlanContent{Execution: ExecutionPolicy{Mode: ExecutionStepThrough}},
			UpdatedAt: now,
		})
		if err != nil {
			t.Fatalf("first draft write: %v", err)
		}
		if revision != 1 {
			t.Fatalf("revision after first write = %d, want 1", revision)
		}

		// The stale session still believes the draft is at revision 0.
		_, err = store.UpdatePlanDraft(ctx, "ws-1", "plan-1", 0, DraftUpdate{
			Title:     "Second edit",
			UpdatedAt: now,
		})
		if !errors.Is(err, ErrStaleDraft) {
			t.Fatalf("stale draft write error = %v, want ErrStaleDraft", err)
		}

		loaded, err := store.GetPlan(ctx, "ws-1", "plan-1")
		if err != nil {
			t.Fatalf("get plan: %v", err)
		}
		if loaded.Title != "First edit" {
			t.Errorf("title = %q, want the first edit to have survived", loaded.Title)
		}
	})
}

// The structural half of FR-25: a regenerated draft rewrites the question, and
// the answer the user authored stays exactly as they wrote it.
func TestStoreDraftWriteCannotOverwriteAnAuthoredAnswer(t *testing.T) {
	forEachStore(t, func(t *testing.T, ctx context.Context, store Store, seed func(string)) {
		seed("ws-1")
		plan := testPlan("plan-1")
		plan.Draft.Clarifications = []Clarification{{
			ID:        "clr-1",
			Prompt:    "Which environment?",
			Required:  true,
			Status:    ClarificationOpen,
			CreatedAt: time.Now().UTC(),
		}}
		if err := store.CreatePlan(ctx, plan); err != nil {
			t.Fatalf("create plan: %v", err)
		}

		answeredAt := time.Now().UTC()
		if err := store.AnswerClarification(ctx, "ws-1", "plan-1", "clr-1", ClarificationAnswer{
			Answered:   true,
			Answer:     "Staging only, never production.",
			AnsweredBy: "jj",
			At:         answeredAt,
		}); err != nil {
			t.Fatalf("answer clarification: %v", err)
		}

		// A regenerated draft carries a rewritten question and a fabricated
		// answer. The question may change; the answer may not.
		if _, err := store.UpdatePlanDraft(ctx, "ws-1", "plan-1", 0, DraftUpdate{
			Title: "Regenerated",
			Content: PlanContent{
				Clarifications: []Clarification{{
					ID:     "clr-1",
					Prompt: "Which environment? (regenerated)",
					Status: ClarificationOpen,
					Answer: "The model's guess",
				}},
			},
			UpdatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("draft write: %v", err)
		}
		if err := store.PutClarifications(ctx, "ws-1", "plan-1", []Clarification{{
			ID:        "clr-1",
			Prompt:    "Which environment? (regenerated)",
			Required:  true,
			Status:    ClarificationOpen,
			Answer:    "The model's guess",
			CreatedAt: time.Now().UTC(),
		}}); err != nil {
			t.Fatalf("put clarifications: %v", err)
		}

		loaded, err := store.GetPlan(ctx, "ws-1", "plan-1")
		if err != nil {
			t.Fatalf("get plan: %v", err)
		}
		if len(loaded.Draft.Clarifications) != 1 {
			t.Fatalf("clarifications = %d, want 1", len(loaded.Draft.Clarifications))
		}
		question := loaded.Draft.Clarifications[0]
		if question.Answer != "Staging only, never production." {
			t.Errorf("answer = %q, want the user's authored answer to survive regeneration", question.Answer)
		}
		if question.Status != ClarificationAnswered {
			t.Errorf("status = %q, want answered", question.Status)
		}
		if question.Prompt != "Which environment? (regenerated)" {
			t.Errorf("prompt = %q, want the reworded question", question.Prompt)
		}
	})
}

// Skipping an optional question records the skip rather than an answer (FR-28).
func TestStoreRecordsSkippedClarifications(t *testing.T) {
	forEachStore(t, func(t *testing.T, ctx context.Context, store Store, seed func(string)) {
		seed("ws-1")
		plan := testPlan("plan-1")
		plan.Draft.Clarifications = []Clarification{{
			ID:        "clr-1",
			Prompt:    "Any deadline?",
			Status:    ClarificationOpen,
			CreatedAt: time.Now().UTC(),
		}}
		if err := store.CreatePlan(ctx, plan); err != nil {
			t.Fatalf("create plan: %v", err)
		}
		if err := store.AnswerClarification(ctx, "ws-1", "plan-1", "clr-1", ClarificationAnswer{
			Answered:   false,
			SkipReason: "No deadline yet",
			AnsweredBy: "jj",
			At:         time.Now().UTC(),
		}); err != nil {
			t.Fatalf("skip clarification: %v", err)
		}

		loaded, err := store.GetPlan(ctx, "ws-1", "plan-1")
		if err != nil {
			t.Fatalf("get plan: %v", err)
		}
		question := loaded.Draft.Clarifications[0]
		if question.Status != ClarificationSkipped {
			t.Errorf("status = %q, want skipped", question.Status)
		}
		if question.Answer != "" {
			t.Errorf("answer = %q, want empty for a skip", question.Answer)
		}
		if question.SkipReason != "No deadline yet" {
			t.Errorf("skip reason = %q, want the authored reason", question.SkipReason)
		}
	})
}

func TestStoreArchiveReopenAndListScopes(t *testing.T) {
	forEachStore(t, func(t *testing.T, ctx context.Context, store Store, seed func(string)) {
		seed("ws-1")
		if err := store.CreatePlan(ctx, testPlan("plan-1")); err != nil {
			t.Fatalf("create plan: %v", err)
		}

		if err := store.ArchivePlan(ctx, "ws-1", "plan-1", "cancelled", time.Now().UTC()); err != nil {
			t.Fatalf("archive plan: %v", err)
		}
		active, err := store.ListPlans(ctx, "ws-1", ListFilter{Scope: ScopeActive})
		if err != nil {
			t.Fatalf("list active: %v", err)
		}
		if len(active) != 0 {
			t.Errorf("active plans = %d, want 0 after archiving", len(active))
		}
		history, err := store.ListPlans(ctx, "ws-1", ListFilter{Scope: ScopeHistory})
		if err != nil {
			t.Fatalf("list history: %v", err)
		}
		if len(history) != 1 || history[0].ArchiveReason != "cancelled" {
			t.Fatalf("history = %+v, want the archived plan with its reason", history)
		}

		if err := store.ReopenPlan(ctx, "ws-1", "plan-1"); err != nil {
			t.Fatalf("reopen plan: %v", err)
		}
		reopened, err := store.GetPlan(ctx, "ws-1", "plan-1")
		if err != nil {
			t.Fatalf("get plan: %v", err)
		}
		if reopened.ArchivedAt != nil {
			t.Error("reopened plan is still archived")
		}
	})
}

// Archiving must never remove the record of what a Plan produced (FR-16).
func TestStoreArchivePreservesVersionsApprovalsAndLinks(t *testing.T) {
	forEachStore(t, func(t *testing.T, ctx context.Context, store Store, seed func(string)) {
		seed("ws-1")
		if err := store.CreatePlan(ctx, testPlan("plan-1")); err != nil {
			t.Fatalf("create plan: %v", err)
		}
		version := seedVersion(t, ctx, store, "plan-1", "hash-1")
		approval := seedApproval(t, ctx, store, version)
		if err := store.LinkTasks(ctx, "ws-1", "plan-1", []TaskLink{{
			PlanID: "plan-1", WorkspaceID: "ws-1", TaskID: "task-1",
			Version: version.Number, ApprovalID: approval.ID,
			GroupID: "grp-1", ItemID: "itm-1", Role: LinkRoleItem,
			CreatedAt: time.Now().UTC(),
		}}); err != nil {
			t.Fatalf("link tasks: %v", err)
		}

		if err := store.ArchivePlan(ctx, "ws-1", "plan-1", "inactive_30d", time.Now().UTC()); err != nil {
			t.Fatalf("archive plan: %v", err)
		}

		versions, err := store.ListVersions(ctx, "ws-1", "plan-1")
		if err != nil || len(versions) != 1 {
			t.Fatalf("versions after archive = %d (%v), want 1", len(versions), err)
		}
		approvals, err := store.ListApprovals(ctx, "ws-1", "plan-1")
		if err != nil || len(approvals) != 1 {
			t.Fatalf("approvals after archive = %d (%v), want 1", len(approvals), err)
		}
		archived, err := store.GetPlan(ctx, "ws-1", "plan-1")
		if err != nil {
			t.Fatalf("get plan: %v", err)
		}
		if len(archived.TaskLinks) != 1 {
			t.Fatalf("task links after archive = %d, want 1", len(archived.TaskLinks))
		}
	})
}

// Only a Plan that never produced anything may be hard-deleted (FR-17).
func TestStoreHardDeleteOnlyForPlansWithNoEffects(t *testing.T) {
	forEachStore(t, func(t *testing.T, ctx context.Context, store Store, seed func(string)) {
		seed("ws-1")
		if err := store.CreatePlan(ctx, testPlan("plan-clean")); err != nil {
			t.Fatalf("create clean plan: %v", err)
		}
		if err := store.DeletePlan(ctx, "ws-1", "plan-clean"); err != nil {
			t.Fatalf("delete never-approved plan: %v", err)
		}
		if _, err := store.GetPlan(ctx, "ws-1", "plan-clean"); !errors.Is(err, ErrPlanNotFound) {
			t.Errorf("plan still readable after delete: %v", err)
		}

		if err := store.CreatePlan(ctx, testPlan("plan-linked")); err != nil {
			t.Fatalf("create linked plan: %v", err)
		}
		version := seedVersion(t, ctx, store, "plan-linked", "hash-1")
		if err := store.LinkTasks(ctx, "ws-1", "plan-linked", []TaskLink{{
			PlanID: "plan-linked", WorkspaceID: "ws-1", TaskID: "task-1",
			Version: version.Number, GroupID: "grp-1", ItemID: "itm-1",
			Role: LinkRoleItem, CreatedAt: time.Now().UTC(),
		}}); err != nil {
			t.Fatalf("link tasks: %v", err)
		}

		err := store.DeletePlan(ctx, "ws-1", "plan-linked")
		if !errors.Is(err, ErrPlanNotDeletable) {
			t.Fatalf("delete plan with linked tasks error = %v, want ErrPlanNotDeletable", err)
		}
		if _, err := store.GetPlan(ctx, "ws-1", "plan-linked"); err != nil {
			t.Errorf("plan with materialized work was removed anyway: %v", err)
		}
	})
}

func TestStoreVersionNumbersAreMonotonicAndImmutable(t *testing.T) {
	forEachStore(t, func(t *testing.T, ctx context.Context, store Store, seed func(string)) {
		seed("ws-1")
		if err := store.CreatePlan(ctx, testPlan("plan-1")); err != nil {
			t.Fatalf("create plan: %v", err)
		}

		first := seedVersion(t, ctx, store, "plan-1", "hash-1")
		second := seedVersion(t, ctx, store, "plan-1", "hash-2")
		if first.Number != 1 || second.Number != 2 {
			t.Fatalf("version numbers = %d, %d; want 1, 2", first.Number, second.Number)
		}

		// A rejection records the decision and leaves the snapshot alone.
		if err := store.SetVersionDecision(ctx, "ws-1", "plan-1", 1, VersionRejected, "jj", "wrong scope", time.Now().UTC()); err != nil {
			t.Fatalf("set version decision: %v", err)
		}
		reloaded, err := store.GetVersion(ctx, "ws-1", "plan-1", 1)
		if err != nil {
			t.Fatalf("get version: %v", err)
		}
		if reloaded.ContentHash != "hash-1" {
			t.Errorf("content hash = %q, want the reviewed hash to be immutable", reloaded.ContentHash)
		}
		if reloaded.Status != VersionRejected || reloaded.DecisionReason != "wrong scope" {
			t.Errorf("decision not recorded: status=%q reason=%q", reloaded.Status, reloaded.DecisionReason)
		}
		if reloaded.Status.Approvable() {
			t.Error("a rejected version is still reported as approvable (FR-74)")
		}
	})
}

// A retried approval request returns the original record rather than a second
// authorization (FR-73).
func TestStoreApprovalCreationIsIdempotent(t *testing.T) {
	forEachStore(t, func(t *testing.T, ctx context.Context, store Store, seed func(string)) {
		seed("ws-1")
		if err := store.CreatePlan(ctx, testPlan("plan-1")); err != nil {
			t.Fatalf("create plan: %v", err)
		}
		version := seedVersion(t, ctx, store, "plan-1", "hash-1")

		first := seedApproval(t, ctx, store, version)
		second := seedApproval(t, ctx, store, version)
		if first.ID != second.ID {
			t.Errorf("retried approval created a second record: %s vs %s", first.ID, second.ID)
		}

		approvals, err := store.ListApprovals(ctx, "ws-1", "plan-1")
		if err != nil {
			t.Fatalf("list approvals: %v", err)
		}
		if len(approvals) != 1 {
			t.Errorf("approvals = %d, want 1", len(approvals))
		}
	})
}

// An approval is spendable exactly once, however many callers race for it
// (FR-72, FR-178, SM-2).
func TestStoreApprovalIsConsumableExactlyOnce(t *testing.T) {
	forEachStore(t, func(t *testing.T, ctx context.Context, store Store, seed func(string)) {
		seed("ws-1")
		if err := store.CreatePlan(ctx, testPlan("plan-1")); err != nil {
			t.Fatalf("create plan: %v", err)
		}
		version := seedVersion(t, ctx, store, "plan-1", "hash-1")
		approval := seedApproval(t, ctx, store, version)

		const racers = 8
		var (
			wg        sync.WaitGroup
			mu        sync.Mutex
			succeeded int
			conflicts int
		)
		wg.Add(racers)
		for i := range racers {
			go func(i int) {
				defer wg.Done()
				err := store.ConsumeApproval(ctx, "ws-1", "plan-1", approval.ID, ApprovalResult{
					TaskIDs:     []string{fmt.Sprintf("task-%d", i)},
					CompletedAt: time.Now().UTC(),
				}, time.Now().UTC())
				mu.Lock()
				defer mu.Unlock()
				switch {
				case err == nil:
					succeeded++
				case errors.Is(err, ErrApprovalConsumed):
					conflicts++
				default:
					t.Errorf("unexpected consume error: %v", err)
				}
			}(i)
		}
		wg.Wait()

		if succeeded != 1 {
			t.Errorf("successful consumptions = %d, want exactly 1", succeeded)
		}
		if conflicts != racers-1 {
			t.Errorf("conflicts = %d, want %d", conflicts, racers-1)
		}

		reloaded, err := store.GetApproval(ctx, "ws-1", "plan-1", approval.ID)
		if err != nil {
			t.Fatalf("get approval: %v", err)
		}
		if !reloaded.Consumed() {
			t.Error("approval is not marked consumed")
		}
		if reloaded.ConsumedResult == nil || len(reloaded.ConsumedResult.TaskIDs) != 1 {
			t.Errorf("consumed result = %+v, want exactly one recorded task tree", reloaded.ConsumedResult)
		}
	})
}

// An invalidated approval can never be spent afterwards (FR-68).
func TestStoreInvalidatedApprovalCannotBeConsumed(t *testing.T) {
	forEachStore(t, func(t *testing.T, ctx context.Context, store Store, seed func(string)) {
		seed("ws-1")
		if err := store.CreatePlan(ctx, testPlan("plan-1")); err != nil {
			t.Fatalf("create plan: %v", err)
		}
		version := seedVersion(t, ctx, store, "plan-1", "hash-1")
		approval := seedApproval(t, ctx, store, version)

		if err := store.InvalidateApprovals(ctx, "ws-1", "plan-1", version.Number, "scope changed", time.Now().UTC()); err != nil {
			t.Fatalf("invalidate approvals: %v", err)
		}
		err := store.ConsumeApproval(ctx, "ws-1", "plan-1", approval.ID, ApprovalResult{}, time.Now().UTC())
		if !errors.Is(err, ErrApprovalMismatch) {
			t.Fatalf("consume invalidated approval error = %v, want ErrApprovalMismatch", err)
		}
	})
}

// Repeating a materialization must not produce a second Task for an approved
// item, while corrective follow-ups stay allowed (FR-78, FR-91).
func TestStoreTaskLinkageIsIdempotentExceptForFollowUps(t *testing.T) {
	forEachStore(t, func(t *testing.T, ctx context.Context, store Store, seed func(string)) {
		seed("ws-1")
		if err := store.CreatePlan(ctx, testPlan("plan-1")); err != nil {
			t.Fatalf("create plan: %v", err)
		}

		link := TaskLink{
			PlanID: "plan-1", WorkspaceID: "ws-1", TaskID: "task-1", Version: 1,
			GroupID: "grp-1", ItemID: "itm-1", Role: LinkRoleItem, CreatedAt: time.Now().UTC(),
		}
		if err := store.LinkTasks(ctx, "ws-1", "plan-1", []TaskLink{link}); err != nil {
			t.Fatalf("first link: %v", err)
		}
		duplicate := link
		duplicate.TaskID = "task-2"
		if err := store.LinkTasks(ctx, "ws-1", "plan-1", []TaskLink{link, duplicate}); err != nil {
			t.Fatalf("repeat link: %v", err)
		}

		followUp := link
		followUp.TaskID = "task-3"
		followUp.Role = LinkRoleFollowUp
		if err := store.LinkTasks(ctx, "ws-1", "plan-1", []TaskLink{followUp}); err != nil {
			t.Fatalf("follow-up link: %v", err)
		}

		loaded, err := store.GetPlan(ctx, "ws-1", "plan-1")
		if err != nil {
			t.Fatalf("get plan: %v", err)
		}
		if len(loaded.TaskLinks) != 2 {
			t.Fatalf("task links = %d, want 2 (one materialized item plus one follow-up)", len(loaded.TaskLinks))
		}
	})
}

func TestStoreReverseLookupsResolveTheOriginatingPlan(t *testing.T) {
	forEachStore(t, func(t *testing.T, ctx context.Context, store Store, seed func(string)) {
		seed("ws-1")
		if err := store.CreatePlan(ctx, testPlan("plan-1")); err != nil {
			t.Fatalf("create plan: %v", err)
		}
		now := time.Now().UTC()
		if err := store.LinkTasks(ctx, "ws-1", "plan-1", []TaskLink{{
			PlanID: "plan-1", WorkspaceID: "ws-1", TaskID: "task-1", Version: 1,
			GroupID: "grp-1", ItemID: "itm-1", Role: LinkRoleItem, CreatedAt: now,
		}}); err != nil {
			t.Fatalf("link tasks: %v", err)
		}
		if err := store.LinkRun(ctx, "ws-1", "plan-1", RunLink{
			PlanID: "plan-1", WorkspaceID: "ws-1", RunID: "run-1", TaskID: "task-1",
			Version: 1, GroupID: "grp-1", ItemID: "itm-1", CreatedAt: now,
		}); err != nil {
			t.Fatalf("link run: %v", err)
		}

		taskLink, err := store.PlanForTask(ctx, "ws-1", "task-1")
		if err != nil {
			t.Fatalf("plan for task: %v", err)
		}
		if taskLink.PlanID != "plan-1" || taskLink.ItemID != "itm-1" || taskLink.Version != 1 {
			t.Errorf("task provenance = %+v, want plan-1/itm-1/v1", taskLink)
		}
		runLink, err := store.PlanForRun(ctx, "ws-1", "run-1")
		if err != nil {
			t.Fatalf("plan for run: %v", err)
		}
		if runLink.PlanID != "plan-1" || runLink.TaskID != "task-1" {
			t.Errorf("run provenance = %+v, want plan-1/task-1", runLink)
		}

		// The reverse lookup is workspace-scoped too.
		if _, err := store.PlanForTask(ctx, "ws-other", "task-1"); !errors.Is(err, ErrPlanNotFound) {
			t.Errorf("cross-workspace task lookup error = %v, want ErrPlanNotFound", err)
		}
	})
}

func TestStoreActivityIsAppendOnlyAndSequenced(t *testing.T) {
	forEachStore(t, func(t *testing.T, ctx context.Context, store Store, seed func(string)) {
		seed("ws-1")
		plan := testPlan("plan-1")
		if err := store.CreatePlan(ctx, plan); err != nil {
			t.Fatalf("create plan: %v", err)
		}

		for _, to := range []Status{StatusNeedsInput, StatusDraft, StatusInReview} {
			current, err := store.GetPlan(ctx, "ws-1", "plan-1")
			if err != nil {
				t.Fatalf("get plan: %v", err)
			}
			change := NewStatusChange(current, to, SourceUser, "jj", "moving along")
			if err := store.SetPlanStatus(ctx, "ws-1", "plan-1", to, change); err != nil {
				t.Fatalf("set status %s: %v", to, err)
			}
		}

		entries, err := store.ListActivity(ctx, "ws-1", "plan-1", 0)
		if err != nil {
			t.Fatalf("list activity: %v", err)
		}
		if len(entries) != 3 {
			t.Fatalf("activity entries = %d, want 3", len(entries))
		}
		for i, entry := range entries {
			if entry.Sequence != int64(i+1) {
				t.Errorf("entry %d sequence = %d, want %d", i, entry.Sequence, i+1)
			}
		}
		if entries[0].From != StatusDraft || entries[0].To != StatusNeedsInput {
			t.Errorf("first transition = %s -> %s, want draft -> needs_input", entries[0].From, entries[0].To)
		}
		if entries[2].Actor != "jj" || entries[2].Source != SourceUser {
			t.Errorf("actor/source not recorded: %+v", entries[2])
		}
	})
}

// Recovery snapshots are pruned to the newest ten and never counted as review
// versions (FR-30, FR-31).
func TestStoreDraftSnapshotsPruneWithoutTouchingVersions(t *testing.T) {
	forEachStore(t, func(t *testing.T, ctx context.Context, store Store, seed func(string)) {
		seed("ws-1")
		if err := store.CreatePlan(ctx, testPlan("plan-1")); err != nil {
			t.Fatalf("create plan: %v", err)
		}
		seedVersion(t, ctx, store, "plan-1", "hash-1")

		base := time.Now().UTC().Add(-time.Hour)
		for i := range 14 {
			if err := store.PutDraftSnapshot(ctx, &DraftSnapshot{
				PlanID:        "plan-1",
				WorkspaceID:   "ws-1",
				DraftRevision: int64(i),
				Title:         fmt.Sprintf("Draft %d", i),
				CreatedAt:     base.Add(time.Duration(i) * time.Minute),
			}, 10); err != nil {
				t.Fatalf("put snapshot %d: %v", i, err)
			}
		}

		snapshots, err := store.ListDraftSnapshots(ctx, "ws-1", "plan-1")
		if err != nil {
			t.Fatalf("list snapshots: %v", err)
		}
		if len(snapshots) != 10 {
			t.Fatalf("snapshots = %d, want 10", len(snapshots))
		}
		if snapshots[0].Title != "Draft 13" {
			t.Errorf("newest snapshot = %q, want Draft 13", snapshots[0].Title)
		}

		versions, err := store.ListVersions(ctx, "ws-1", "plan-1")
		if err != nil {
			t.Fatalf("list versions: %v", err)
		}
		if len(versions) != 1 {
			t.Errorf("versions = %d, want 1 (snapshot pruning must not touch review history)", len(versions))
		}
	})
}

func TestStoreDraftSnapshotsExpireByAge(t *testing.T) {
	forEachStore(t, func(t *testing.T, ctx context.Context, store Store, seed func(string)) {
		seed("ws-1")
		if err := store.CreatePlan(ctx, testPlan("plan-1")); err != nil {
			t.Fatalf("create plan: %v", err)
		}

		now := time.Now().UTC()
		for _, age := range []time.Duration{40 * 24 * time.Hour, 10 * 24 * time.Hour} {
			if err := store.PutDraftSnapshot(ctx, &DraftSnapshot{
				PlanID:      "plan-1",
				WorkspaceID: "ws-1",
				CreatedAt:   now.Add(-age),
			}, 10); err != nil {
				t.Fatalf("put snapshot: %v", err)
			}
		}

		removed, err := store.PruneDraftSnapshots(ctx, "ws-1", "plan-1", 10, now.Add(-30*24*time.Hour))
		if err != nil {
			t.Fatalf("prune snapshots: %v", err)
		}
		if removed != 1 {
			t.Errorf("removed = %d, want 1 (only the 40-day-old snapshot)", removed)
		}
		snapshots, err := store.ListDraftSnapshots(ctx, "ws-1", "plan-1")
		if err != nil {
			t.Fatalf("list snapshots: %v", err)
		}
		if len(snapshots) != 1 {
			t.Errorf("snapshots = %d, want 1", len(snapshots))
		}
	})
}

// Callers get clones: mutating what a store returned must not reach persisted
// state.
func TestStoreReturnsClones(t *testing.T) {
	forEachStore(t, func(t *testing.T, ctx context.Context, store Store, seed func(string)) {
		seed("ws-1")
		plan := testPlan("plan-1")
		if err := store.CreatePlan(ctx, plan); err != nil {
			t.Fatalf("create plan: %v", err)
		}
		// Mutating the struct handed to CreatePlan must not reach storage.
		plan.Title = "Mutated by the caller"
		plan.Draft.Groups[0].Items[0].Description = "Mutated too"

		loaded, err := store.GetPlan(ctx, "ws-1", "plan-1")
		if err != nil {
			t.Fatalf("get plan: %v", err)
		}
		if loaded.Title != "Ship the thing" {
			t.Errorf("title = %q, want the stored value", loaded.Title)
		}
		if loaded.Draft.Groups[0].Items[0].Description != "Write the code" {
			t.Errorf("item description = %q, want the stored value", loaded.Draft.Groups[0].Items[0].Description)
		}

		// Mutating a returned Plan must not reach storage either.
		loaded.Draft.Groups[0].Title = "Mutated after read"
		again, err := store.GetPlan(ctx, "ws-1", "plan-1")
		if err != nil {
			t.Fatalf("get plan again: %v", err)
		}
		if again.Draft.Groups[0].Title != "Build" {
			t.Errorf("group title = %q, want the stored value", again.Draft.Groups[0].Title)
		}
	})
}

// Approval records must survive server restart (FR-71), and so must the Plan,
// its versions, its clarification answers, and its provenance links (FR-16).
// This is the one test that closes the database and opens it again: every other
// SQLite test runs against :memory:, which cannot tell durable from cached.
func TestSQLiteStoreSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "plans.db")

	// First process: create everything.
	func() {
		db, err := database.Open(ctx, &database.Config{Path: dbPath, WALMode: false})
		if err != nil {
			t.Fatalf("open db: %v", err)
		}
		defer func() { _ = db.Close() }()
		seedTestWorkspace(t, ctx, db, "ws-1")

		store := NewSQLiteStore(db)
		plan := testPlan("plan-1")
		plan.Draft.Clarifications = []Clarification{{
			ID: "clr-1", Prompt: "Which environment?", Required: true,
			Status: ClarificationOpen, CreatedAt: time.Now().UTC(),
		}}
		if err := store.CreatePlan(ctx, plan); err != nil {
			t.Fatalf("create plan: %v", err)
		}
		if err := store.AnswerClarification(ctx, "ws-1", "plan-1", "clr-1", ClarificationAnswer{
			Answered: true, Answer: "Staging only", AnsweredBy: "jj", At: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("answer clarification: %v", err)
		}
		version := seedVersion(t, ctx, store, "plan-1", "hash-1")
		approval := seedApproval(t, ctx, store, version)
		if err := store.ConsumeApproval(ctx, "ws-1", "plan-1", approval.ID, ApprovalResult{
			TaskIDs: []string{"task-1"}, CompletedAt: time.Now().UTC(),
		}, time.Now().UTC()); err != nil {
			t.Fatalf("consume approval: %v", err)
		}
		if err := store.LinkTasks(ctx, "ws-1", "plan-1", []TaskLink{{
			PlanID: "plan-1", WorkspaceID: "ws-1", TaskID: "task-1", Version: version.Number,
			ApprovalID: approval.ID, GroupID: "grp-1", ItemID: "itm-1",
			Role: LinkRoleItem, CreatedAt: time.Now().UTC(),
		}}); err != nil {
			t.Fatalf("link tasks: %v", err)
		}
	}()

	// Second process: everything is still there, and the approval is still spent.
	db, err := database.Open(ctx, &database.Config{Path: dbPath, WALMode: false})
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	defer func() { _ = db.Close() }()
	store := NewSQLiteStore(db)

	plan, err := store.GetPlan(ctx, "ws-1", "plan-1")
	if err != nil {
		t.Fatalf("get plan after restart: %v", err)
	}
	if plan.OriginalRequest != "Please plan how we ship the thing." {
		t.Errorf("original request = %q after restart", plan.OriginalRequest)
	}
	if len(plan.Draft.Groups) != 1 || plan.Draft.Groups[0].Items[0].ID != "itm-1" {
		t.Errorf("draft content did not survive restart: %+v", plan.Draft.Groups)
	}
	if len(plan.Draft.Clarifications) != 1 || plan.Draft.Clarifications[0].Answer != "Staging only" {
		t.Errorf("authored answer did not survive restart: %+v", plan.Draft.Clarifications)
	}
	if len(plan.TaskLinks) != 1 || plan.TaskLinks[0].TaskID != "task-1" {
		t.Errorf("task provenance did not survive restart: %+v", plan.TaskLinks)
	}

	approvals, err := store.ListApprovals(ctx, "ws-1", "plan-1")
	if err != nil || len(approvals) != 1 {
		t.Fatalf("approvals after restart = %d (%v), want 1", len(approvals), err)
	}
	if !approvals[0].Consumed() {
		t.Error("approval is not marked consumed after restart")
	}
	// A restart must not hand out a second use of an already-spent approval.
	err = store.ConsumeApproval(ctx, "ws-1", "plan-1", approvals[0].ID, ApprovalResult{}, time.Now().UTC())
	if !errors.Is(err, ErrApprovalConsumed) {
		t.Errorf("re-consume after restart error = %v, want ErrApprovalConsumed", err)
	}

	versions, err := store.ListVersions(ctx, "ws-1", "plan-1")
	if err != nil || len(versions) != 1 || versions[0].ContentHash != "hash-1" {
		t.Fatalf("versions after restart = %+v (%v)", versions, err)
	}
}

func seedVersion(t *testing.T, ctx context.Context, store Store, planID, hash string) *Version {
	t.Helper()
	version, err := store.CreateVersion(ctx, &Version{
		PlanID:      planID,
		WorkspaceID: testWorkspaceID,
		Title:       "Ship the thing",
		Objective:   "Ship the thing safely",
		Content:     PlanContent{Execution: ExecutionPolicy{Mode: ExecutionStepThrough}},
		ContentHash: hash,
		Status:      VersionInReview,
		CreatedAt:   time.Now().UTC(),
		CreatedBy:   Origin{Kind: OriginUser, Actor: "jj"},
	})
	if err != nil {
		t.Fatalf("create version: %v", err)
	}
	return version
}

func seedApproval(t *testing.T, ctx context.Context, store Store, version *Version) *Approval {
	t.Helper()
	// The approval takes its version and hash FROM the version it approves.
	// Passing them separately would let a test seed an approval that could not
	// exist, and pass against a store that should have rejected it.
	approval, err := store.CreateApproval(ctx, &Approval{
		PlanID:         testPlanID,
		WorkspaceID:    testWorkspaceID,
		Version:        version.Number,
		ContentHash:    version.ContentHash,
		Effect:         EffectCreateTasks,
		UserID:         "user-1",
		UserName:       "jj",
		IdempotencyKey: "key-1",
		CreatedAt:      time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}
	return approval
}
