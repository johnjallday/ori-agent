package dailybrief

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
)

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	db, err := database.Open(context.Background(), &database.Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatalf("database.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewSQLiteStore(db)
}

func TestSQLiteStore_ConfigNotFoundInitially(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.GetConfig(context.Background(), "ws-1"); !errors.Is(err, ErrConfigNotFound) {
		t.Fatalf("expected ErrConfigNotFound, got %v", err)
	}
}

func TestSQLiteStore_UpsertConfigRoundTripsAndIncrementsRevision(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	cfg := &Config{
		WorkspaceID:             "ws-1",
		UserID:                  "local",
		Timezone:                "America/New_York",
		ScheduleDays:            []string{"mon", "wed"},
		ScheduleTime:            "08:30",
		ScheduleEnabled:         true,
		Scope:                   ScopeSelected,
		SelectedWorkspaceIDs:    []string{"ws-2"},
		IncludeFutureWorkspaces: false,
		NotifyOnReady:           true,
	}
	if err := store.UpsertConfig(ctx, cfg); err != nil {
		t.Fatalf("UpsertConfig: %v", err)
	}
	got, err := store.GetConfig(ctx, "ws-1")
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if got.ConfigRevision != 1 {
		t.Fatalf("expected initial config_revision 1, got %d", got.ConfigRevision)
	}
	if got.Timezone != "America/New_York" || got.ScheduleTime != "08:30" || !got.NotifyOnReady {
		t.Fatalf("config did not round-trip: %#v", got)
	}
	if len(got.ScheduleDays) != 2 || len(got.SelectedWorkspaceIDs) != 1 {
		t.Fatalf("slices did not round-trip: %#v", got)
	}

	// A second upsert must bump the revision, not reset it.
	cfg.Timezone = "UTC"
	if err := store.UpsertConfig(ctx, cfg); err != nil {
		t.Fatalf("second UpsertConfig: %v", err)
	}
	got, err = store.GetConfig(ctx, "ws-1")
	if err != nil {
		t.Fatalf("GetConfig after update: %v", err)
	}
	if got.ConfigRevision != 2 {
		t.Fatalf("expected config_revision 2 after update, got %d", got.ConfigRevision)
	}
	if got.Timezone != "UTC" {
		t.Fatalf("expected updated timezone, got %q", got.Timezone)
	}
}

func TestSQLiteStore_ClaimGenerationDedupesFirstOpenAndScheduled(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	first, isNew, err := store.ClaimGeneration(ctx, &GenerationRequest{
		WorkspaceID: "ws-1", UserID: "local", LocalDate: "2026-07-14", Trigger: TriggerScheduled,
	})
	if err != nil || !isNew {
		t.Fatalf("first claim: isNew=%v err=%v", isNew, err)
	}

	second, isNew, err := store.ClaimGeneration(ctx, &GenerationRequest{
		WorkspaceID: "ws-1", UserID: "local", LocalDate: "2026-07-14", Trigger: TriggerFirstOpen,
	})
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if isNew {
		t.Fatal("expected the second first_open/scheduled claim for the same date to reuse the existing one")
	}
	if second.ID != first.ID {
		t.Fatalf("expected the same claim id returned, got %s vs %s", second.ID, first.ID)
	}
}

func TestSQLiteStore_ClaimGenerationNeverDedupesManual(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		_, isNew, err := store.ClaimGeneration(ctx, &GenerationRequest{
			WorkspaceID: "ws-1", UserID: "local", LocalDate: "2026-07-14", Trigger: TriggerManual,
		})
		if err != nil {
			t.Fatalf("manual claim %d: %v", i, err)
		}
		if !isNew {
			t.Fatalf("manual claim %d must always be new, got isNew=false", i)
		}
	}
}

// TestSQLiteStore_FailedClaimAllowsRetry covers PRD 5.12: a failed scheduled
// attempt must not permanently block a later first-open/scheduled retry for
// the same date.
func TestSQLiteStore_FailedClaimAllowsRetry(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	first, _, err := store.ClaimGeneration(ctx, &GenerationRequest{
		WorkspaceID: "ws-1", UserID: "local", LocalDate: "2026-07-14", Trigger: TriggerScheduled,
	})
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if err := store.UpdateGenerationStatus(ctx, first.ID, GenerationFailed, "", "boom"); err != nil {
		t.Fatalf("UpdateGenerationStatus: %v", err)
	}

	retry, isNew, err := store.ClaimGeneration(ctx, &GenerationRequest{
		WorkspaceID: "ws-1", UserID: "local", LocalDate: "2026-07-14", Trigger: TriggerFirstOpen,
	})
	if err != nil {
		t.Fatalf("retry claim: %v", err)
	}
	if !isNew {
		t.Fatal("expected a retry claim after a failure to be allowed (isNew=true)")
	}
	if retry.ID == first.ID {
		t.Fatal("expected a distinct claim id for the retry")
	}
}

func TestSQLiteStore_GetActiveClaimIgnoresManual(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	if _, _, err := store.ClaimGeneration(ctx, &GenerationRequest{
		WorkspaceID: "ws-1", UserID: "local", LocalDate: "2026-07-14", Trigger: TriggerManual,
	}); err != nil {
		t.Fatalf("manual claim: %v", err)
	}
	active, err := store.GetActiveClaim(ctx, "ws-1", "2026-07-14")
	if err != nil {
		t.Fatalf("GetActiveClaim: %v", err)
	}
	if active != nil {
		t.Fatalf("expected no active (non-manual) claim, got %#v", active)
	}
}

func TestSQLiteStore_RevisionNumberIncrementsPerDate(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	n, err := store.NextRevisionNumber(ctx, "ws-1", "2026-07-14")
	if err != nil || n != 1 {
		t.Fatalf("expected first revision number 1, got %d err=%v", n, err)
	}
	if err := store.CreateRevision(ctx, &Revision{
		WorkspaceID: "ws-1", UserID: "local", LocalDate: "2026-07-14", RevisionNumber: 1,
		Trigger: TriggerScheduled, Status: GenerationSucceeded,
	}); err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}
	n, err = store.NextRevisionNumber(ctx, "ws-1", "2026-07-14")
	if err != nil || n != 2 {
		t.Fatalf("expected next revision number 2, got %d err=%v", n, err)
	}
	// A different date starts back at 1.
	n, err = store.NextRevisionNumber(ctx, "ws-1", "2026-07-15")
	if err != nil || n != 1 {
		t.Fatalf("expected revision number 1 for a new date, got %d err=%v", n, err)
	}
}

func TestSQLiteStore_SetCurrentRevisionIsAtomicSingleWinner(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	rev1 := &Revision{ID: "rev-1", WorkspaceID: "ws-1", UserID: "local", LocalDate: "2026-07-14", RevisionNumber: 1, Trigger: TriggerScheduled, Status: GenerationSucceeded}
	rev2 := &Revision{ID: "rev-2", WorkspaceID: "ws-1", UserID: "local", LocalDate: "2026-07-14", RevisionNumber: 2, Trigger: TriggerManual, Status: GenerationSucceeded}
	if err := store.CreateRevision(ctx, rev1); err != nil {
		t.Fatalf("CreateRevision rev1: %v", err)
	}
	if err := store.CreateRevision(ctx, rev2); err != nil {
		t.Fatalf("CreateRevision rev2: %v", err)
	}

	if err := store.SetCurrentRevision(ctx, "ws-1", "rev-1"); err != nil {
		t.Fatalf("SetCurrentRevision rev-1: %v", err)
	}
	current, err := store.GetCurrentRevision(ctx, "ws-1")
	if err != nil || current.ID != "rev-1" {
		t.Fatalf("expected rev-1 current, got %#v err=%v", current, err)
	}

	// Manual refresh: a new revision becomes current, the old one is cleared.
	if err := store.SetCurrentRevision(ctx, "ws-1", "rev-2"); err != nil {
		t.Fatalf("SetCurrentRevision rev-2: %v", err)
	}
	current, err = store.GetCurrentRevision(ctx, "ws-1")
	if err != nil || current.ID != "rev-2" {
		t.Fatalf("expected rev-2 current, got %#v err=%v", current, err)
	}
	old, err := store.GetRevision(ctx, "rev-1")
	if err != nil {
		t.Fatalf("GetRevision rev-1: %v", err)
	}
	if old.IsCurrent {
		t.Fatal("expected rev-1 to no longer be current")
	}
}

func TestSQLiteStore_SetCurrentRevisionUnknownIDErrors(t *testing.T) {
	store := newTestStore(t)
	if err := store.SetCurrentRevision(context.Background(), "ws-1", "does-not-exist"); !errors.Is(err, ErrRevisionNotFound) {
		t.Fatalf("expected ErrRevisionNotFound, got %v", err)
	}
}

func TestSQLiteStore_GetCurrentRevisionNotFoundWhenNeverSet(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.GetCurrentRevision(context.Background(), "ws-1"); !errors.Is(err, ErrRevisionNotFound) {
		t.Fatalf("expected ErrRevisionNotFound, got %v", err)
	}
}

// TestSQLiteStore_ListHistoryCollapsesSameDayRevisions covers task 5.11.
func TestSQLiteStore_ListHistoryCollapsesSameDayRevisions(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	for i, id := range []string{"rev-1", "rev-2"} {
		if err := store.CreateRevision(ctx, &Revision{
			ID: id, WorkspaceID: "ws-1", UserID: "local", LocalDate: "2026-07-14", RevisionNumber: i + 1,
			Trigger: TriggerManual, Status: GenerationSucceeded, GeneratedAt: time.Now(),
		}); err != nil {
			t.Fatalf("CreateRevision %s: %v", id, err)
		}
	}
	if err := store.SetCurrentRevision(ctx, "ws-1", "rev-2"); err != nil {
		t.Fatalf("SetCurrentRevision: %v", err)
	}
	if err := store.CreateRevision(ctx, &Revision{
		ID: "rev-3", WorkspaceID: "ws-1", UserID: "local", LocalDate: "2026-07-13", RevisionNumber: 1,
		Trigger: TriggerScheduled, Status: GenerationSucceeded, GeneratedAt: time.Now(),
	}); err != nil {
		t.Fatalf("CreateRevision rev-3: %v", err)
	}

	history, err := store.ListHistory(ctx, "ws-1", 30)
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("expected 2 collapsed days, got %d: %#v", len(history), history)
	}
	if history[0].LocalDate != "2026-07-14" {
		t.Fatalf("expected most recent date first, got %+v", history[0])
	}
	if history[0].RevisionCount != 2 {
		t.Fatalf("expected 2026-07-14 to collapse 2 revisions, got %d", history[0].RevisionCount)
	}
	if history[0].CurrentRevisionID != "rev-2" {
		t.Fatalf("expected current revision id rev-2, got %q", history[0].CurrentRevisionID)
	}
}

// TestSQLiteStore_PruneHistoryNeverTouchesOtherWorkspaceOrCurrent covers
// task 5.11: pruning is scoped to one workspace and never deletes the
// current revision even if it falls outside the retention window.
func TestSQLiteStore_PruneHistoryNeverTouchesOtherWorkspaceOrCurrent(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// ws-1: 5 distinct dates, keep only 2 -> 3 pruned, but not the current one.
	for i := 0; i < 5; i++ {
		date := time.Date(2026, 7, 10+i, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
		id := "ws1-rev-" + date
		if err := store.CreateRevision(ctx, &Revision{
			ID: id, WorkspaceID: "ws-1", UserID: "local", LocalDate: date, RevisionNumber: 1,
			Trigger: TriggerScheduled, Status: GenerationSucceeded,
		}); err != nil {
			t.Fatalf("CreateRevision %s: %v", id, err)
		}
	}
	// Mark the OLDEST as current, to prove it survives pruning anyway.
	if err := store.SetCurrentRevision(ctx, "ws-1", "ws1-rev-2026-07-10"); err != nil {
		t.Fatalf("SetCurrentRevision: %v", err)
	}

	// ws-2: one revision, must be completely unaffected by pruning ws-1.
	if err := store.CreateRevision(ctx, &Revision{
		ID: "ws2-rev-1", WorkspaceID: "ws-2", UserID: "local", LocalDate: "2026-01-01", RevisionNumber: 1,
		Trigger: TriggerScheduled, Status: GenerationSucceeded,
	}); err != nil {
		t.Fatalf("CreateRevision ws-2: %v", err)
	}

	if err := store.PruneHistory(ctx, "ws-1", 2); err != nil {
		t.Fatalf("PruneHistory: %v", err)
	}

	history, err := store.ListHistory(ctx, "ws-1", 30)
	if err != nil {
		t.Fatalf("ListHistory ws-1: %v", err)
	}
	// The current revision's date survives even though it's outside the
	// 2-day retention window: 3 non-current dates pruned, 2 kept + 1 current.
	foundCurrentDate := false
	for _, h := range history {
		if h.LocalDate == "2026-07-10" {
			foundCurrentDate = true
		}
	}
	if !foundCurrentDate {
		t.Fatal("expected the current revision's date to survive pruning")
	}

	other, err := store.ListHistory(ctx, "ws-2", 30)
	if err != nil {
		t.Fatalf("ListHistory ws-2: %v", err)
	}
	if len(other) != 1 {
		t.Fatalf("expected ws-2 history untouched by pruning ws-1, got %d entries", len(other))
	}
}

// TestSQLiteStore_ConcurrentClaimsYieldExactlyOneWinner covers task
// 5.10/5.14: concurrent first-open and scheduled claims for the same
// workspace/date racing against each other must still produce exactly one
// authoritative claim at the database boundary — run with -race.
func TestSQLiteStore_ConcurrentClaimsYieldExactlyOneWinner(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	const attempts = 8
	results := make(chan bool, attempts)
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		trigger := TriggerFirstOpen
		if i%2 == 0 {
			trigger = TriggerScheduled
		}
		wg.Add(1)
		go func(trig Trigger) {
			defer wg.Done()
			_, isNew, err := store.ClaimGeneration(ctx, &GenerationRequest{
				WorkspaceID: "ws-1", UserID: "local", LocalDate: "2026-07-14", Trigger: trig,
			})
			if err != nil {
				t.Errorf("ClaimGeneration: %v", err)
				return
			}
			results <- isNew
		}(trigger)
	}
	wg.Wait()
	close(results)

	newCount := 0
	for isNew := range results {
		if isNew {
			newCount++
		}
	}
	if newCount != 1 {
		t.Fatalf("expected exactly one winning claim out of %d concurrent attempts, got %d", attempts, newCount)
	}
}

// TestSQLiteStore_RecordNotificationIsIdempotent covers PRD FR65.
func TestSQLiteStore_RecordNotificationIsIdempotent(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	created, err := store.RecordNotification(ctx, "rev-1", "ws-1")
	if err != nil || !created {
		t.Fatalf("first RecordNotification: created=%v err=%v", created, err)
	}
	created, err = store.RecordNotification(ctx, "rev-1", "ws-1")
	if err != nil {
		t.Fatalf("second RecordNotification: %v", err)
	}
	if created {
		t.Fatal("expected the second RecordNotification for the same revision to be a no-op")
	}
}
