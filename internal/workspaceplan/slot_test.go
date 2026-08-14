package workspaceplan

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
)

// The slot has two implementations and one guarantee. Running the suite
// against both keeps the in-memory store honest: a looser fake would let a
// concurrency test pass over code that is broken in production.
func forEachSlotStore(t *testing.T, run func(t *testing.T, ctx context.Context, store SlotStore, seed func(workspaceID, planID string))) {
	t.Helper()

	t.Run("memory", func(t *testing.T) {
		ctx := context.Background()
		run(t, ctx, NewMemorySlotStore(), func(string, string) {})
	})

	t.Run("sqlite", func(t *testing.T) {
		ctx := context.Background()
		db := openPlanTestDB(t, ctx)
		seeded := map[string]bool{}
		run(t, ctx, NewSQLiteSlotStore(db), func(workspaceID, planID string) {
			if !seeded[workspaceID] {
				seedTestWorkspace(t, ctx, db, workspaceID)
				seeded[workspaceID] = true
			}
			seedSlotPlan(t, ctx, db, workspaceID, planID)
		})
	})
}

// seedSlotPlan inserts the plan row the slot's foreign key requires.
func seedSlotPlan(t *testing.T, ctx context.Context, db *database.DB, workspaceID, planID string) {
	t.Helper()
	now := time.Now().UTC()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO workspace_plans (id, workspace_id, status, created_at, updated_at, last_activity_at)
		VALUES (?, ?, 'approved', ?, ?, ?)
		ON CONFLICT(id) DO NOTHING
	`, planID, workspaceID, now, now, now); err != nil {
		t.Fatalf("seed plan %s: %v", planID, err)
	}
}

// One workspace runs one plan. A second plan is refused, not queued silently
// beside it (FR-106).
func TestSlotAdmitsExactlyOnePlan(t *testing.T) {
	forEachSlotStore(t, func(t *testing.T, ctx context.Context, store SlotStore, seed func(string, string)) {
		seed("ws-1", "plan-a")
		seed("ws-1", "plan-b")
		now := time.Now().UTC()

		lease, err := store.Acquire(ctx, "ws-1", "plan-a", "owner-1", now)
		if err != nil {
			t.Fatalf("first acquire: %v", err)
		}
		if lease.PlanID != "plan-a" || lease.Generation < 1 {
			t.Fatalf("lease = %+v", lease)
		}

		_, err = store.Acquire(ctx, "ws-1", "plan-b", "owner-2", now)
		if !errors.Is(err, ErrExecutionConflict) {
			t.Fatalf("second acquire error = %v, want ErrExecutionConflict", err)
		}
		// The refusal names the holder, so the UI can say what it waits behind.
		if err != nil && !containsPlan(err.Error(), "plan-a") {
			t.Errorf("the conflict does not name the holder: %v", err)
		}
	})
}

// Acquiring twice is idempotent: a retry must not look like a conflict or burn
// a generation.
func TestSlotAcquireIsIdempotentForTheHolder(t *testing.T) {
	forEachSlotStore(t, func(t *testing.T, ctx context.Context, store SlotStore, seed func(string, string)) {
		seed("ws-1", "plan-a")
		now := time.Now().UTC()

		first, err := store.Acquire(ctx, "ws-1", "plan-a", "owner-1", now)
		if err != nil {
			t.Fatalf("acquire: %v", err)
		}
		again, err := store.Acquire(ctx, "ws-1", "plan-a", "owner-1", now)
		if err != nil {
			t.Fatalf("re-acquire: %v", err)
		}
		if again.Generation != first.Generation {
			t.Errorf("generation changed on re-acquire: %d -> %d", first.Generation, again.Generation)
		}
	})
}

// Many processes racing for a free slot resolve to exactly one winner
// (FR-106, FR-178, SM-15).
func TestSlotResolvesConcurrentClaimsToOneWinner(t *testing.T) {
	forEachSlotStore(t, func(t *testing.T, ctx context.Context, store SlotStore, seed func(string, string)) {
		seed("ws-1", "plan-a")
		const racers = 8
		for i := range racers {
			seed("ws-1", planName(i))
		}

		var (
			wg        sync.WaitGroup
			mu        sync.Mutex
			winners   []string
			conflicts int
		)
		wg.Add(racers)
		for i := range racers {
			go func(i int) {
				defer wg.Done()
				lease, err := store.Acquire(ctx, "ws-1", planName(i), "owner", time.Now().UTC())
				mu.Lock()
				defer mu.Unlock()
				switch {
				case err == nil:
					winners = append(winners, lease.PlanID)
				case errors.Is(err, ErrExecutionConflict):
					conflicts++
				default:
					t.Errorf("unexpected acquire error: %v", err)
				}
			}(i)
		}
		wg.Wait()

		if len(winners) != 1 {
			t.Errorf("winners = %v, want exactly 1", winners)
		}
		if conflicts != racers-1 {
			t.Errorf("conflicts = %d, want %d", conflicts, racers-1)
		}

		lease, err := store.CurrentLease(ctx, "ws-1")
		if err != nil || lease == nil {
			t.Fatalf("current lease = %+v (%v)", lease, err)
		}
		if len(winners) == 1 && lease.PlanID != winners[0] {
			t.Errorf("holder = %q, want the winner %q", lease.PlanID, winners[0])
		}
	})
}

// The fencing token is what makes a reclaimed lease safe: a worker that stalled
// and woke up cannot act on a slot that moved on (FR-106).
func TestStaleGenerationCannotActOnTheSlot(t *testing.T) {
	forEachSlotStore(t, func(t *testing.T, ctx context.Context, store SlotStore, seed func(string, string)) {
		seed("ws-1", "plan-a")
		seed("ws-1", "plan-b")
		now := time.Now().UTC()

		first, err := store.Acquire(ctx, "ws-1", "plan-a", "worker-1", now)
		if err != nil {
			t.Fatalf("acquire: %v", err)
		}
		staleGeneration := first.Generation

		// The slot is released and taken by another plan.
		if err := store.Release(ctx, "ws-1", "plan-a", staleGeneration); err != nil {
			t.Fatalf("release: %v", err)
		}
		second, err := store.Acquire(ctx, "ws-1", "plan-b", "worker-2", now)
		if err != nil {
			t.Fatalf("second acquire: %v", err)
		}
		if second.Generation <= staleGeneration {
			t.Fatalf("generation did not advance: %d then %d", staleGeneration, second.Generation)
		}

		// The stalled worker wakes up holding the old token.
		if err := store.Heartbeat(ctx, "ws-1", "plan-a", staleGeneration, now); err == nil {
			t.Error("a stale worker heartbeat was accepted")
		}
		if err := store.Release(ctx, "ws-1", "plan-a", staleGeneration); !errors.Is(err, ErrExecutionConflict) {
			t.Errorf("a stale worker release error = %v, want a conflict", err)
		}

		// The real holder is untouched.
		lease, err := store.CurrentLease(ctx, "ws-1")
		if err != nil || lease == nil || lease.PlanID != "plan-b" {
			t.Errorf("the stale worker disturbed the holder: %+v (%v)", lease, err)
		}
	})
}

// A generation is never reissued, even across many acquisitions, so an old
// token can never accidentally become valid again.
func TestGenerationsAreMonotonicAcrossReleases(t *testing.T) {
	forEachSlotStore(t, func(t *testing.T, ctx context.Context, store SlotStore, seed func(string, string)) {
		seed("ws-1", "plan-a")
		now := time.Now().UTC()

		var previous int64
		for range 5 {
			lease, err := store.Acquire(ctx, "ws-1", "plan-a", "owner", now)
			if err != nil {
				t.Fatalf("acquire: %v", err)
			}
			if lease.Generation <= previous {
				t.Fatalf("generation did not advance: %d after %d", lease.Generation, previous)
			}
			previous = lease.Generation
			if err := store.Release(ctx, "ws-1", "plan-a", lease.Generation); err != nil {
				t.Fatalf("release: %v", err)
			}
		}
	})
}

func TestSlotQueueKeepsOrderAndIsIdempotent(t *testing.T) {
	forEachSlotStore(t, func(t *testing.T, ctx context.Context, store SlotStore, seed func(string, string)) {
		base := time.Now().UTC()
		for i, plan := range []string{"plan-a", "plan-b", "plan-c"} {
			seed("ws-1", plan)
			if err := store.Enqueue(ctx, "ws-1", plan, base.Add(time.Duration(i)*time.Second)); err != nil {
				t.Fatalf("enqueue %s: %v", plan, err)
			}
		}

		// Asking twice must not send a plan to the back of the line.
		if err := store.Enqueue(ctx, "ws-1", "plan-a", base.Add(time.Hour)); err != nil {
			t.Fatalf("re-enqueue: %v", err)
		}

		queue, err := store.Queue(ctx, "ws-1")
		if err != nil {
			t.Fatalf("queue: %v", err)
		}
		if len(queue) != 3 {
			t.Fatalf("queue = %d entries, want 3", len(queue))
		}
		if queue[0].PlanID != "plan-a" || queue[1].PlanID != "plan-b" || queue[2].PlanID != "plan-c" {
			t.Errorf("queue order = %v", planIDs(queue))
		}
		for i, entry := range queue {
			if entry.Position != i+1 {
				t.Errorf("position = %d at index %d", entry.Position, i)
			}
		}

		if err := store.Dequeue(ctx, "ws-1", "plan-b"); err != nil {
			t.Fatalf("dequeue: %v", err)
		}
		queue, _ = store.Queue(ctx, "ws-1")
		if len(queue) != 2 || queue[1].PlanID != "plan-c" {
			t.Errorf("queue after dequeue = %v", planIDs(queue))
		}
	})
}

// Different workspaces do not contend: one plan per WORKSPACE, not one plan
// globally (FR-106).
func TestSlotsAreIndependentPerWorkspace(t *testing.T) {
	forEachSlotStore(t, func(t *testing.T, ctx context.Context, store SlotStore, seed func(string, string)) {
		seed("ws-1", "plan-a")
		seed("ws-2", "plan-b")
		now := time.Now().UTC()

		if _, err := store.Acquire(ctx, "ws-1", "plan-a", "owner", now); err != nil {
			t.Fatalf("acquire ws-1: %v", err)
		}
		if _, err := store.Acquire(ctx, "ws-2", "plan-b", "owner", now); err != nil {
			t.Errorf("a second workspace was blocked by the first: %v", err)
		}
	})
}

// --- Coordinator -----------------------------------------------------------

func TestClaimQueuesBehindTheHolderAndNamesIt(t *testing.T) {
	ctx := context.Background()
	coordinator := NewSlotCoordinator(NewMemorySlotStore())

	first, err := coordinator.Claim(ctx, "ws-1", "plan-a")
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if !first.Owned() {
		t.Fatal("the first plan did not take a free slot")
	}

	second, err := coordinator.Claim(ctx, "ws-1", "plan-b")
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if second.Owned() {
		t.Fatal("two plans hold the slot")
	}
	if second.Waiting == nil || second.Waiting.Position != 1 {
		t.Errorf("waiting = %+v, want position 1", second.Waiting)
	}
	// The UI can say what it is waiting behind, not merely that it waits.
	if second.HolderPlanID != "plan-a" {
		t.Errorf("holder = %q, want plan-a", second.HolderPlanID)
	}

	waiting, err := coordinator.WaitingForSlot(ctx, "ws-1", "plan-b")
	if err != nil || !waiting {
		t.Errorf("WaitingForSlot = %v (%v), want true", waiting, err)
	}
	holding, err := coordinator.WaitingForSlot(ctx, "ws-1", "plan-a")
	if err != nil || holding {
		t.Errorf("the holder reported itself waiting: %v (%v)", holding, err)
	}
}

// Releasing hands the slot to the next in line, so a released slot with a
// waiting queue does not sit idle.
func TestReleaseNamesTheNextPlanInLine(t *testing.T) {
	ctx := context.Background()
	coordinator := NewSlotCoordinator(NewMemorySlotStore())

	first, err := coordinator.Claim(ctx, "ws-1", "plan-a")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := coordinator.Claim(ctx, "ws-1", "plan-b"); err != nil {
		t.Fatalf("queue plan-b: %v", err)
	}
	if _, err := coordinator.Claim(ctx, "ws-1", "plan-c"); err != nil {
		t.Fatalf("queue plan-c: %v", err)
	}

	next, err := coordinator.Release(ctx, "ws-1", "plan-a", first.Lease.Generation)
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if next != "plan-b" {
		t.Errorf("next = %q, want plan-b (first in line)", next)
	}

	// And it can actually take the slot now.
	claimed, err := coordinator.Claim(ctx, "ws-1", "plan-b")
	if err != nil {
		t.Fatalf("claim after release: %v", err)
	}
	if !claimed.Owned() {
		t.Error("the next plan could not take the released slot")
	}
	// Taking the slot removes it from the queue.
	if waiting, _ := coordinator.WaitingForSlot(ctx, "ws-1", "plan-b"); waiting {
		t.Error("the new holder is still listed as waiting")
	}
}

// A dead holder's lease is reclaimable, but only after a generous interval:
// reclaiming from a merely-slow holder would run two plans at once.
func TestClaimReclaimsAStaleLease(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	clock := func() time.Time { return now }
	store := NewMemorySlotStore()
	coordinator := NewSlotCoordinator(store,
		WithSlotClock(func() time.Time { return clock() }),
		WithLeaseStaleAfter(10*time.Minute))

	if _, err := coordinator.Claim(ctx, "ws-1", "plan-a"); err != nil {
		t.Fatalf("claim: %v", err)
	}

	// Still fresh: the slot is not stolen.
	now = now.Add(5 * time.Minute)
	blocked, err := coordinator.Claim(ctx, "ws-1", "plan-b")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if blocked.Owned() {
		t.Fatal("a live lease was reclaimed")
	}

	// Past the interval with no heartbeat: reclaimable.
	now = now.Add(10 * time.Minute)
	reclaimed, err := coordinator.Claim(ctx, "ws-1", "plan-b")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if !reclaimed.Owned() {
		t.Fatal("a dead lease was not reclaimed")
	}
	// The reclaiming holder has a NEW generation, so the old worker is fenced.
	if reclaimed.Lease.Generation <= 1 {
		t.Errorf("generation = %d, want it advanced past the stale holder", reclaimed.Lease.Generation)
	}
}

// A heartbeat keeps a slow-but-alive holder's slot.
func TestHeartbeatPreventsReclaim(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	store := NewMemorySlotStore()
	coordinator := NewSlotCoordinator(store,
		WithSlotClock(func() time.Time { return now }),
		WithLeaseStaleAfter(10*time.Minute))

	claim, err := coordinator.Claim(ctx, "ws-1", "plan-a")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}

	for range 3 {
		now = now.Add(8 * time.Minute)
		if err := coordinator.Heartbeat(ctx, "ws-1", "plan-a", claim.Lease.Generation); err != nil {
			t.Fatalf("heartbeat: %v", err)
		}
		other, err := coordinator.Claim(ctx, "ws-1", "plan-b")
		if err != nil {
			t.Fatalf("claim: %v", err)
		}
		if other.Owned() {
			t.Fatal("a heartbeating holder lost its slot")
		}
	}
}

// Resuming rejoins the queue rather than displacing the current holder
// (FR-110).
func TestEnqueueDoesNotDisplaceTheHolder(t *testing.T) {
	ctx := context.Background()
	coordinator := NewSlotCoordinator(NewMemorySlotStore())

	if _, err := coordinator.Claim(ctx, "ws-1", "plan-a"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	entry, err := coordinator.Enqueue(ctx, "ws-1", "plan-b")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if entry == nil || entry.Position != 1 {
		t.Errorf("queue entry = %+v, want position 1", entry)
	}

	holder, err := coordinator.Holder(ctx, "ws-1")
	if err != nil {
		t.Fatalf("holder: %v", err)
	}
	if holder != "plan-a" {
		t.Errorf("holder = %q, want plan-a to keep the slot", holder)
	}
}

// The slot survives a restart: a Plan that owned it still owns it, so a
// restart does not silently start a second Plan (FR-106).
func TestSlotSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	dbPath := t.TempDir() + "/slots.db"

	var generation int64
	func() {
		db := openFileTestDB(t, ctx, dbPath)
		seedTestWorkspace(t, ctx, db, "ws-1")
		seedSlotPlan(t, ctx, db, "ws-1", "plan-a")
		seedSlotPlan(t, ctx, db, "ws-1", "plan-b")

		store := NewSQLiteSlotStore(db)
		lease, err := store.Acquire(ctx, "ws-1", "plan-a", "worker-1", time.Now().UTC())
		if err != nil {
			t.Fatalf("acquire: %v", err)
		}
		generation = lease.Generation
		if err := store.Enqueue(ctx, "ws-1", "plan-b", time.Now().UTC()); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}()

	db := openFileTestDB(t, ctx, dbPath)
	store := NewSQLiteSlotStore(db)

	lease, err := store.CurrentLease(ctx, "ws-1")
	if err != nil || lease == nil {
		t.Fatalf("lease after restart = %+v (%v)", lease, err)
	}
	if lease.PlanID != "plan-a" || lease.Generation != generation {
		t.Errorf("lease changed across restart: %+v", lease)
	}

	// Another plan still cannot take it.
	if _, err := store.Acquire(ctx, "ws-1", "plan-b", "worker-2", time.Now().UTC()); !errors.Is(err, ErrExecutionConflict) {
		t.Errorf("a restart released the slot: %v", err)
	}
	// The queue survived too.
	queue, err := store.Queue(ctx, "ws-1")
	if err != nil || len(queue) != 1 || queue[0].PlanID != "plan-b" {
		t.Errorf("queue after restart = %v (%v)", planIDs(queue), err)
	}
}

func planName(i int) string {
	return "plan-" + string(rune('a'+i))
}

func planIDs(queue []QueueEntry) []string {
	out := make([]string, 0, len(queue))
	for _, entry := range queue {
		out = append(out, entry.PlanID)
	}
	return out
}

func containsPlan(message, planID string) bool {
	return strings.Contains(message, planID)
}
