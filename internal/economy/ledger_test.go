package economy

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
)

func newLedger(t *testing.T) *SQLiteStore {
	t.Helper()
	db, err := database.Open(context.Background(), &database.Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewSQLiteStore(db)
}

func craftEntry(refID string, amount int64) Entry {
	return Entry{
		Resource:    ResourceCraft,
		Amount:      amount,
		Reason:      ReasonChat,
		RefKind:     RefKindMessage,
		RefID:       refID,
		WorkspaceID: "ws1",
	}
}

func TestLedgerBalanceIsTheSumOfDeltas(t *testing.T) {
	ctx := context.Background()
	store := newLedger(t)
	now := time.Now()

	for i, amount := range []int64{5, 3, 12} {
		if _, err := store.Credit(ctx, craftEntry(string(rune('a'+i)), amount), now); err != nil {
			t.Fatalf("credit %d: %v", amount, err)
		}
	}
	if _, err := store.Debit(ctx, Entry{
		Resource: ResourceCraft, Amount: 6, Reason: ReasonBuild, RefKind: RefKindBuild, RefID: "task-1",
	}, now); err != nil {
		t.Fatalf("debit: %v", err)
	}

	balances, err := store.Balances(ctx)
	if err != nil {
		t.Fatalf("balances: %v", err)
	}
	if balances.Craft != 14 {
		t.Fatalf("craft = %d, want 14 (5+3+12-6)", balances.Craft)
	}
	if balances.Harvest != 0 {
		t.Fatalf("harvest = %d, want 0 — nothing credited it", balances.Harvest)
	}
}

// Replaying an event or retrying a save must not pay twice (FR3).
func TestLedgerIgnoresARepeatedReference(t *testing.T) {
	ctx := context.Background()
	store := newLedger(t)
	now := time.Now()

	inserted, err := store.Credit(ctx, craftEntry("event-1", 5), now)
	if err != nil || !inserted {
		t.Fatalf("first credit inserted=%v err=%v", inserted, err)
	}
	inserted, err = store.Credit(ctx, craftEntry("event-1", 5), now.Add(time.Minute))
	if err != nil {
		t.Fatalf("replayed credit: %v", err)
	}
	if inserted {
		t.Fatal("replaying the same reference reported a new entry")
	}

	balances, err := store.Balances(ctx)
	if err != nil {
		t.Fatalf("balances: %v", err)
	}
	if balances.Craft != 5 {
		t.Fatalf("craft = %d after replaying one event, want 5", balances.Craft)
	}
}

// The same reference under a DIFFERENT resource is a different thing and is
// recorded: the key is (resource, ref_kind, ref_id), not the reference alone.
func TestLedgerKeysIdempotencyByResourceToo(t *testing.T) {
	ctx := context.Background()
	store := newLedger(t)
	now := time.Now()

	if _, err := store.Credit(ctx, Entry{
		Resource: ResourceCraft, Amount: 5, Reason: ReasonManualTask, RefKind: RefKindTask, RefID: "task-1",
	}, now); err != nil {
		t.Fatalf("craft credit: %v", err)
	}
	inserted, err := store.Credit(ctx, Entry{
		Resource: ResourceHarvest, Amount: 1, Reason: ReasonHarvest, RefKind: RefKindTask, RefID: "task-1",
	}, now)
	if err != nil || !inserted {
		t.Fatalf("harvest credit inserted=%v err=%v", inserted, err)
	}

	balances, err := store.Balances(ctx)
	if err != nil {
		t.Fatalf("balances: %v", err)
	}
	if balances.Craft != 5 || balances.Harvest != 1 {
		t.Fatalf("balances = %+v, want craft 5 harvest 1", balances)
	}
}

// A balance is never negative (FR1): the debit is refused, not clamped.
func TestLedgerRefusesADebitBelowZero(t *testing.T) {
	ctx := context.Background()
	store := newLedger(t)
	now := time.Now()

	if _, err := store.Credit(ctx, craftEntry("event-1", 12), now); err != nil {
		t.Fatalf("credit: %v", err)
	}

	_, err := store.Debit(ctx, Entry{
		Resource: ResourceCraft, Amount: FarmBuildCost, Reason: ReasonBuild, RefKind: RefKindBuild, RefID: "task-1",
	}, now)
	if !errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("debit error = %v, want ErrInsufficientBalance", err)
	}

	balances, err := store.Balances(ctx)
	if err != nil {
		t.Fatalf("balances: %v", err)
	}
	if balances.Craft != 12 {
		t.Fatalf("craft = %d after a refused debit, want 12 untouched", balances.Craft)
	}
	// The refused debit must leave no trace to block a later, affordable one.
	has, err := store.HasEntry(ctx, ResourceCraft, RefKindBuild, "task-1")
	if err != nil {
		t.Fatalf("has entry: %v", err)
	}
	if has {
		t.Fatal("a refused debit left a ledger entry behind")
	}
}

// A debit that takes the balance to exactly zero is allowed.
func TestLedgerAllowsADebitToExactlyZero(t *testing.T) {
	ctx := context.Background()
	store := newLedger(t)
	now := time.Now()

	if _, err := store.Credit(ctx, craftEntry("event-1", FarmBuildCost), now); err != nil {
		t.Fatalf("credit: %v", err)
	}
	if _, err := store.Debit(ctx, Entry{
		Resource: ResourceCraft, Amount: FarmBuildCost, Reason: ReasonBuild, RefKind: RefKindBuild, RefID: "task-1",
	}, now); err != nil {
		t.Fatalf("debit to zero: %v", err)
	}

	balances, err := store.Balances(ctx)
	if err != nil {
		t.Fatalf("balances: %v", err)
	}
	if balances.Craft != 0 {
		t.Fatalf("craft = %d, want 0", balances.Craft)
	}
}

func TestLedgerHasReasonDrivesTheBackfillGuard(t *testing.T) {
	ctx := context.Background()
	store := newLedger(t)
	now := time.Now()

	has, err := store.HasReason(ctx, ReasonBackfill)
	if err != nil {
		t.Fatalf("has reason: %v", err)
	}
	if has {
		t.Fatal("a fresh ledger reports a backfill entry")
	}

	if _, err := store.Credit(ctx, Entry{
		Resource: ResourceCraft, Amount: 40, Reason: ReasonBackfill, RefKind: RefKindBackfill, RefID: "install",
	}, now); err != nil {
		t.Fatalf("backfill credit: %v", err)
	}

	has, err = store.HasReason(ctx, ReasonBackfill)
	if err != nil {
		t.Fatalf("has reason: %v", err)
	}
	if !has {
		t.Fatal("the backfill entry was not found by reason")
	}
}

// A zero-amount entry is a real record: grandfathering writes one so an existing
// Farm shows its badge and is never charged a build (FR33).
func TestLedgerRecordsAZeroAmountEntry(t *testing.T) {
	ctx := context.Background()
	store := newLedger(t)

	inserted, err := store.Credit(ctx, Entry{
		Resource: ResourceCraft, Amount: 0, Reason: ReasonGrandfathered, RefKind: RefKindBuild, RefID: "task-1",
	}, time.Now())
	if err != nil || !inserted {
		t.Fatalf("grandfathered entry inserted=%v err=%v", inserted, err)
	}

	has, err := store.HasEntry(ctx, ResourceCraft, RefKindBuild, "task-1")
	if err != nil {
		t.Fatalf("has entry: %v", err)
	}
	if !has {
		t.Fatal("the grandfathered build entry is not visible to the build-once check")
	}
	balances, err := store.Balances(ctx)
	if err != nil {
		t.Fatalf("balances: %v", err)
	}
	if balances.Craft != 0 {
		t.Fatalf("craft = %d, want 0 — grandfathering grants nothing", balances.Craft)
	}
}

func TestLedgerRejectsMalformedEntries(t *testing.T) {
	ctx := context.Background()
	store := newLedger(t)
	now := time.Now()

	cases := []struct {
		name  string
		entry Entry
	}{
		{"unknown resource", Entry{Resource: "insight", Amount: 1, Reason: ReasonChat, RefKind: RefKindMessage, RefID: "a"}},
		{"no reason", Entry{Resource: ResourceCraft, Amount: 1, RefKind: RefKindMessage, RefID: "a"}},
		{"no reference kind", Entry{Resource: ResourceCraft, Amount: 1, Reason: ReasonChat, RefID: "a"}},
		{"no reference id", Entry{Resource: ResourceCraft, Amount: 1, Reason: ReasonChat, RefKind: RefKindMessage}},
		{"negative amount", Entry{Resource: ResourceCraft, Amount: -1, Reason: ReasonChat, RefKind: RefKindMessage, RefID: "a"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := store.Credit(ctx, tc.entry, now); err == nil {
				t.Fatalf("credit(%s) succeeded, want a rejection", tc.name)
			}
		})
	}
}

func pending(taskID, workspaceID, runKey string) PendingRun {
	return PendingRun{TaskID: taskID, WorkspaceID: workspaceID, RunKey: runKey, ProducedAt: time.Now()}
}

func TestPendingHarvestCountsAndBanks(t *testing.T) {
	ctx := context.Background()
	store := newLedger(t)
	now := time.Now()

	for _, run := range []PendingRun{
		pending("task-1", "ws1", "run-1"),
		pending("task-1", "ws1", "run-2"),
		pending("task-2", "ws1", "run-3"),
		pending("task-3", "ws2", "run-4"),
	} {
		if inserted, err := store.AddPending(ctx, run); err != nil || !inserted {
			t.Fatalf("add pending %s: inserted=%v err=%v", run.RunKey, inserted, err)
		}
	}

	byWorkspace, err := store.PendingByWorkspace(ctx)
	if err != nil {
		t.Fatalf("pending by workspace: %v", err)
	}
	if byWorkspace["ws1"] != 3 || byWorkspace["ws2"] != 1 {
		t.Fatalf("pending by workspace = %v, want ws1:3 ws2:1", byWorkspace)
	}
	byTask, err := store.PendingByTask(ctx)
	if err != nil {
		t.Fatalf("pending by task: %v", err)
	}
	if byTask["task-1"] != 2 {
		t.Fatalf("pending for task-1 = %d, want 2", byTask["task-1"])
	}

	banked, err := store.BankPending(ctx, "ws1", "task-1", now)
	if err != nil {
		t.Fatalf("bank: %v", err)
	}
	if banked != 2 {
		t.Fatalf("banked = %d, want 2", banked)
	}

	balances, err := store.Balances(ctx)
	if err != nil {
		t.Fatalf("balances: %v", err)
	}
	if balances.Harvest != 2*HarvestPerFarmRun {
		t.Fatalf("harvest = %d, want %d", balances.Harvest, 2*HarvestPerFarmRun)
	}

	// Banking one task must not touch another task's pile, even in the same
	// workspace.
	byWorkspace, err = store.PendingByWorkspace(ctx)
	if err != nil {
		t.Fatalf("pending by workspace after banking: %v", err)
	}
	if byWorkspace["ws1"] != 1 {
		t.Fatalf("ws1 pending after banking task-1 = %d, want 1 (task-2 survives)", byWorkspace["ws1"])
	}
	if byWorkspace["ws2"] != 1 {
		t.Fatalf("ws2 pending = %d, want 1 — a bank in another workspace touched it", byWorkspace["ws2"])
	}
}

// Opening a result you have already read banks nothing and pays nothing (FR26).
func TestBankingTwiceIsANoOp(t *testing.T) {
	ctx := context.Background()
	store := newLedger(t)

	if _, err := store.AddPending(ctx, pending("task-1", "ws1", "run-1")); err != nil {
		t.Fatalf("add pending: %v", err)
	}
	if banked, err := store.BankPending(ctx, "ws1", "task-1", time.Now()); err != nil || banked != 1 {
		t.Fatalf("first bank = %d, err %v", banked, err)
	}
	banked, err := store.BankPending(ctx, "ws1", "task-1", time.Now().Add(time.Second))
	if err != nil {
		t.Fatalf("second bank: %v", err)
	}
	if banked != 0 {
		t.Fatalf("second bank = %d, want 0", banked)
	}

	balances, err := store.Balances(ctx)
	if err != nil {
		t.Fatalf("balances: %v", err)
	}
	if balances.Harvest != HarvestPerFarmRun {
		t.Fatalf("harvest = %d after banking twice, want %d", balances.Harvest, HarvestPerFarmRun)
	}
}

// The same run delivered twice is one pending Harvest (FR10).
func TestAddPendingIgnoresADuplicateRunKey(t *testing.T) {
	ctx := context.Background()
	store := newLedger(t)

	if inserted, err := store.AddPending(ctx, pending("task-1", "ws1", "run-1")); err != nil || !inserted {
		t.Fatalf("first add inserted=%v err=%v", inserted, err)
	}
	inserted, err := store.AddPending(ctx, pending("task-1", "ws1", "run-1"))
	if err != nil {
		t.Fatalf("duplicate add: %v", err)
	}
	if inserted {
		t.Fatal("a duplicate run key reported a new pending harvest")
	}

	byTask, err := store.PendingByTask(ctx)
	if err != nil {
		t.Fatalf("pending by task: %v", err)
	}
	if byTask["task-1"] != 1 {
		t.Fatalf("pending for task-1 = %d, want 1", byTask["task-1"])
	}
}

// A deleted task's unopened output was never reviewed, so it is dropped rather
// than banked (FR12).
func TestDeletePendingForTaskBanksNothing(t *testing.T) {
	ctx := context.Background()
	store := newLedger(t)

	for _, run := range []PendingRun{
		pending("task-1", "ws1", "run-1"),
		pending("task-2", "ws1", "run-2"),
	} {
		if _, err := store.AddPending(ctx, run); err != nil {
			t.Fatalf("add pending: %v", err)
		}
	}

	if err := store.DeletePendingForTask(ctx, "task-1"); err != nil {
		t.Fatalf("delete pending: %v", err)
	}

	byTask, err := store.PendingByTask(ctx)
	if err != nil {
		t.Fatalf("pending by task: %v", err)
	}
	if _, still := byTask["task-1"]; still {
		t.Fatalf("task-1 still has pending runs: %v", byTask)
	}
	if byTask["task-2"] != 1 {
		t.Fatalf("task-2 pending = %d, want 1 — an unrelated task lost its pile", byTask["task-2"])
	}
	balances, err := store.Balances(ctx)
	if err != nil {
		t.Fatalf("balances: %v", err)
	}
	if balances.Harvest != 0 {
		t.Fatalf("harvest = %d, want 0 — deleting a task paid out", balances.Harvest)
	}
}

func TestCountUserChatMessagesIgnoresAssistantReplies(t *testing.T) {
	ctx := context.Background()
	store := newLedger(t)

	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO sessions (id, title, agent_name, message_count, created_at, updated_at)
		VALUES ('s1', 'chat', 'Atlas', 0, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	for _, row := range []struct{ id, role string }{
		{"m1", "user"}, {"m2", "assistant"}, {"m3", "user"},
	} {
		if _, err := store.db.ExecContext(ctx, `
			INSERT INTO messages (id, session_id, role, content, created_at)
			VALUES (?, 's1', ?, 'hi', CURRENT_TIMESTAMP)
		`, row.id, row.role); err != nil {
			t.Fatalf("seed message %s: %v", row.id, err)
		}
	}

	count, err := store.CountUserChatMessages(ctx)
	if err != nil {
		t.Fatalf("count chat messages: %v", err)
	}
	if count != 2 {
		t.Fatalf("user chat messages = %d, want 2", count)
	}
}

// Without storage the ledger is inert rather than panicking: this is the same
// shape the feature flag produces, and every caller already treats it as "the
// economy is not available".
func TestLedgerWithoutStorageIsInert(t *testing.T) {
	ctx := context.Background()
	var store *SQLiteStore

	if _, err := store.Credit(ctx, craftEntry("event-1", 1), time.Now()); !errors.Is(err, ErrStoreUnavailable) {
		t.Fatalf("credit error = %v, want ErrStoreUnavailable", err)
	}
	if _, err := store.Balances(ctx); !errors.Is(err, ErrStoreUnavailable) {
		t.Fatalf("balances error = %v, want ErrStoreUnavailable", err)
	}
	if _, err := store.BankPending(ctx, "ws1", "task-1", time.Now()); !errors.Is(err, ErrStoreUnavailable) {
		t.Fatalf("bank error = %v, want ErrStoreUnavailable", err)
	}
}
