package workspaceplan

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
)

// Restart and fault behavior (FR-71, FR-89, FR-90, FR-178).
//
// Everything here uses a FILE-backed database and reopens it, because an
// :memory: database dies with its connection and would prove nothing about
// surviving a restart. The question these answer is the one that matters after
// a crash: is the record still true, and can the retry finish the job?

// reopenableStore returns a store over a file-backed database, plus a function
// that closes and reopens it as a fresh process would.
func reopenableStore(t *testing.T, ctx context.Context) (Store, func() Store) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "plans.db")

	// The first open seeds the workspace; a reopen must not, because the row is
	// already there and inserting it again would fail on the primary key.
	db := openFileTestDB(t, ctx, path)
	seedTestWorkspace(t, ctx, db, testWorkspaceID)

	reopen := func() Store {
		return NewSQLiteStore(openFileTestDB(t, ctx, path))
	}
	return NewSQLiteStore(db), reopen
}

// An approval survives a restart. It is the record that authorizes work, and a
// crash between approving and materializing must not lose the authorization
// (FR-71).
func TestAnApprovalSurvivesARestart(t *testing.T) {
	ctx := context.Background()
	store, reopen := reopenableStore(t, ctx)

	if err := store.CreatePlan(ctx, testPlan("plan-1")); err != nil {
		t.Fatalf("create plan: %v", err)
	}
	version := seedVersion(t, ctx, store, "plan-1", "hash-1")
	approval := seedApproval(t, ctx, store, version)

	reopened := reopen()
	recovered, err := reopened.GetApproval(ctx, testWorkspaceID, "plan-1", approval.ID)
	if err != nil {
		t.Fatalf("the approval did not survive a restart: %v", err)
	}
	if recovered.ContentHash != version.ContentHash {
		t.Errorf("content hash = %q, want %q", recovered.ContentHash, version.ContentHash)
	}
	if recovered.Consumed() {
		t.Error("an unspent approval came back consumed")
	}
}

// A consumed approval stays consumed. If it came back unspent, the retry after
// a crash would create a second Task tree — the exact duplication the
// consume-once design exists to prevent (FR-72).
func TestAConsumedApprovalStaysConsumedAcrossARestart(t *testing.T) {
	ctx := context.Background()
	store, reopen := reopenableStore(t, ctx)

	if err := store.CreatePlan(ctx, testPlan("plan-1")); err != nil {
		t.Fatalf("create plan: %v", err)
	}
	version := seedVersion(t, ctx, store, "plan-1", "hash-1")
	approval := seedApproval(t, ctx, store, version)

	result := ApprovalResult{TaskIDs: []string{"task-1", "task-2"}}
	if err := store.ConsumeApproval(ctx, testWorkspaceID, "plan-1", approval.ID,
		result, version.CreatedAt); err != nil {
		t.Fatalf("consume: %v", err)
	}

	reopened := reopen()
	recovered, err := reopened.GetApproval(ctx, testWorkspaceID, "plan-1", approval.ID)
	if err != nil {
		t.Fatalf("read approval: %v", err)
	}
	if !recovered.Consumed() {
		t.Fatal("a consumed approval came back unspent; a retry would duplicate its work")
	}
	// And the result it produced comes back with it, so the retry can replay
	// rather than redo (FR-73).
	if recovered.ConsumedResult == nil || len(recovered.ConsumedResult.TaskIDs) != 2 {
		t.Errorf("the consumed result did not survive: %+v", recovered.ConsumedResult)
	}
}

// Task links survive, so a Plan reopened after a restart still knows which work
// it created. Losing them would orphan real Tasks (FR-89).
func TestTaskLinksSurviveARestart(t *testing.T) {
	ctx := context.Background()
	store, reopen := reopenableStore(t, ctx)

	if err := store.CreatePlan(ctx, testPlan("plan-1")); err != nil {
		t.Fatalf("create plan: %v", err)
	}
	links := []TaskLink{{
		PlanID: "plan-1", WorkspaceID: testWorkspaceID, Version: 1,
		GroupID: "grp-1", ItemID: "itm-1", TaskID: "task-1", Role: LinkRoleItem,
	}}
	if err := store.LinkTasks(ctx, testWorkspaceID, "plan-1", links); err != nil {
		t.Fatalf("link tasks: %v", err)
	}

	reopened := reopen()
	plan, err := reopened.GetPlan(ctx, testWorkspaceID, "plan-1")
	if err != nil {
		t.Fatalf("read plan: %v", err)
	}
	if len(plan.TaskLinks) != 1 || plan.TaskLinks[0].TaskID != "task-1" {
		t.Errorf("task links did not survive: %+v", plan.TaskLinks)
	}
}

// Linking is idempotent, which is what makes the retry after an interrupted
// materialization safe: the same links are written again and nothing
// duplicates (FR-91).
func TestRelinkingAfterAnInterruptionWritesNothingNew(t *testing.T) {
	ctx := context.Background()
	store, _ := reopenableStore(t, ctx)

	if err := store.CreatePlan(ctx, testPlan("plan-1")); err != nil {
		t.Fatalf("create plan: %v", err)
	}
	links := []TaskLink{{
		PlanID: "plan-1", WorkspaceID: testWorkspaceID, Version: 1,
		GroupID: "grp-1", ItemID: "itm-1", TaskID: "task-1", Role: LinkRoleItem,
	}}

	for attempt := range 3 {
		if err := store.LinkTasks(ctx, testWorkspaceID, "plan-1", links); err != nil {
			t.Fatalf("link attempt %d: %v", attempt+1, err)
		}
	}

	plan, err := store.GetPlan(ctx, testWorkspaceID, "plan-1")
	if err != nil {
		t.Fatalf("read plan: %v", err)
	}
	if len(plan.TaskLinks) != 1 {
		t.Errorf("three link attempts produced %d links, want 1", len(plan.TaskLinks))
	}
}

// Two callers racing to spend one approval: exactly one wins, and the loser is
// told the approval is already consumed rather than quietly succeeding
// (FR-72, FR-178).
func TestConcurrentApprovalConsumptionHasOneWinner(t *testing.T) {
	ctx := context.Background()
	store, _ := reopenableStore(t, ctx)

	if err := store.CreatePlan(ctx, testPlan("plan-1")); err != nil {
		t.Fatalf("create plan: %v", err)
	}
	version := seedVersion(t, ctx, store, "plan-1", "hash-1")
	approval := seedApproval(t, ctx, store, version)

	const racers = 8
	var (
		wait    sync.WaitGroup
		mu      sync.Mutex
		winners int
		losers  int
	)
	wait.Add(racers)
	for range racers {
		go func() {
			defer wait.Done()
			err := store.ConsumeApproval(ctx, testWorkspaceID, "plan-1", approval.ID,
				ApprovalResult{TaskIDs: []string{"task-1"}}, version.CreatedAt)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				winners++
			case errors.Is(err, ErrApprovalConsumed):
				losers++
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wait.Wait()

	if winners != 1 {
		t.Errorf("%d callers spent the same approval, want exactly 1", winners)
	}
	if losers != racers-1 {
		t.Errorf("%d losers reported already-consumed, want %d", losers, racers-1)
	}
}

// The execution slot survives a restart with its generation intact, so a worker
// that stalled across the restart still holds a stale token and cannot dispatch
// (FR-106, FR-165).
func TestTheExecutionSlotSurvivesARestartWithItsFence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "slots.db")

	db := openFileTestDB(t, ctx, path)
	seedTestWorkspace(t, ctx, db, testWorkspaceID)
	seedTestPlanRow(t, ctx, db, "plan-1")

	slots := NewSQLiteSlotStore(db)
	lease, err := slots.Acquire(ctx, testWorkspaceID, "plan-1", "owner-1", time.Now().UTC())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	reopened := NewSQLiteSlotStore(openFileTestDB(t, ctx, path))
	current, err := reopened.CurrentLease(ctx, testWorkspaceID)
	if err != nil {
		t.Fatalf("read lease: %v", err)
	}
	if current == nil {
		t.Fatal("the execution slot was lost across a restart")
	}
	if current.PlanID != "plan-1" {
		t.Errorf("holder = %q, want plan-1", current.PlanID)
	}
	if current.Generation != lease.Generation {
		t.Errorf("generation = %d, want %d; a changed fence would let a stale worker back in",
			current.Generation, lease.Generation)
	}
}

// seedTestPlanRow inserts the minimum plan row the slot's foreign keys need.
func seedTestPlanRow(t *testing.T, ctx context.Context, db *database.DB, planID string) {
	t.Helper()
	store := NewSQLiteStore(db)
	if err := store.CreatePlan(ctx, testPlan(planID)); err != nil {
		t.Fatalf("seed plan row: %v", err)
	}
}
