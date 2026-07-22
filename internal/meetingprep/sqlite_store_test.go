package meetingprep

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
)

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	db, err := database.Open(context.Background(), &database.Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewSQLiteStore(db)
}

func testKey() Key {
	return Key{WorkspaceID: "ws-1", BindingID: "b-1", CalendarID: "primary", EventID: "evt-1"}
}

func TestStartRun_FirstRunCreatesPendingLink(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	link, already, err := store.StartRun(ctx, testKey(), "task-1")
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if already {
		t.Fatal("first run must not be reported as already running")
	}
	if link.Status != StatusPending {
		t.Fatalf("status = %q, want pending", link.Status)
	}
	if link.ID == "" {
		t.Fatal("expected a generated id")
	}
}

func TestStartRun_ConcurrentSecondCallDedupesToSamePendingLink(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	key := testKey()

	const attempts = 20
	var wg sync.WaitGroup
	var mu sync.Mutex
	var links []*Link
	var alreadyCount int

	wg.Add(attempts)
	for i := 0; i < attempts; i++ {
		go func() {
			defer wg.Done()
			link, already, err := store.StartRun(ctx, key, "task-x")
			if err != nil {
				t.Errorf("StartRun: %v", err)
				return
			}
			mu.Lock()
			links = append(links, link)
			if already {
				alreadyCount++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()

	if len(links) != attempts {
		t.Fatalf("expected %d results, got %d", attempts, len(links))
	}
	firstID := links[0].ID
	for i, l := range links {
		if l.ID != firstID {
			t.Fatalf("result %d has a different link id (%s vs %s); concurrent calls must dedupe to one link", i, l.ID, firstID)
		}
	}
	if alreadyCount != attempts-1 {
		t.Fatalf("expected exactly 1 winner and %d already-running, got %d already-running", attempts-1, alreadyCount)
	}
}

func TestStartRun_RerunAfterReadyResetsToPendingPreservingNoteID(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	key := testKey()

	first, _, err := store.StartRun(ctx, key, "task-1")
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if err := store.MarkReady(ctx, first.ID, "note-1", "fp-1"); err != nil {
		t.Fatalf("MarkReady: %v", err)
	}

	rerun, already, err := store.StartRun(ctx, key, "task-2")
	if err != nil {
		t.Fatalf("StartRun (rerun): %v", err)
	}
	if already {
		t.Fatal("a rerun after Ready must not be reported as already running")
	}
	if rerun.ID != first.ID {
		t.Fatalf("rerun must reuse the same link id, got %s want %s", rerun.ID, first.ID)
	}
	if rerun.Status != StatusPending {
		t.Fatalf("status = %q, want pending", rerun.Status)
	}
	if rerun.NoteID != "note-1" {
		t.Fatalf("rerun must preserve the prior note id until the new run completes, got %q", rerun.NoteID)
	}
}

func TestStartRun_RerunAfterFailedResetsToPending(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	key := testKey()

	first, _, err := store.StartRun(ctx, key, "task-1")
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if err := store.MarkFailed(ctx, first.ID, "connector timed out"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	retry, already, err := store.StartRun(ctx, key, "task-2")
	if err != nil {
		t.Fatalf("StartRun (retry): %v", err)
	}
	if already {
		t.Fatal("a retry after Failed must not be reported as already running")
	}
	if retry.Status != StatusPending || retry.Error != "" {
		t.Fatalf("retry should clear the error and reset to pending, got status=%q error=%q", retry.Status, retry.Error)
	}
}

func TestMarkReadyAndMarkFailed(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	link, _, err := store.StartRun(ctx, testKey(), "task-1")
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if err := store.MarkReady(ctx, link.ID, "note-abc", "fingerprint-xyz"); err != nil {
		t.Fatalf("MarkReady: %v", err)
	}
	got, err := store.GetByID(ctx, link.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Status != StatusReady || got.NoteID != "note-abc" || got.EventFingerprint != "fingerprint-xyz" {
		t.Fatalf("unexpected link after MarkReady: %+v", got)
	}

	if err := store.MarkFailed(ctx, link.ID, "boom"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	got, err = store.GetByID(ctx, link.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Status != StatusFailed || got.Error != "boom" {
		t.Fatalf("unexpected link after MarkFailed: %+v", got)
	}
}

func TestMarkReady_UnknownIDReturnsNotFound(t *testing.T) {
	store := newTestStore(t)
	if err := store.MarkReady(context.Background(), "does-not-exist", "note-1", "fp"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestGetByKey_EventIDCollisionAcrossCalendarsAndBindingsDoesNotAlias(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Same raw event_id "evt-1", different calendar and different binding --
	// these must be entirely independent links (task 6.7's collision case).
	keyA := Key{WorkspaceID: "ws-1", BindingID: "b-1", CalendarID: "cal-a", EventID: "evt-1"}
	keyB := Key{WorkspaceID: "ws-1", BindingID: "b-1", CalendarID: "cal-b", EventID: "evt-1"}
	keyC := Key{WorkspaceID: "ws-1", BindingID: "b-2", CalendarID: "cal-a", EventID: "evt-1"}

	linkA, _, err := store.StartRun(ctx, keyA, "task-a")
	if err != nil {
		t.Fatalf("StartRun A: %v", err)
	}
	linkB, _, err := store.StartRun(ctx, keyB, "task-b")
	if err != nil {
		t.Fatalf("StartRun B: %v", err)
	}
	linkC, _, err := store.StartRun(ctx, keyC, "task-c")
	if err != nil {
		t.Fatalf("StartRun C: %v", err)
	}

	if linkA.ID == linkB.ID || linkA.ID == linkC.ID || linkB.ID == linkC.ID {
		t.Fatalf("colliding event ids across calendars/bindings must not alias to the same link: A=%s B=%s C=%s", linkA.ID, linkB.ID, linkC.ID)
	}

	if err := store.MarkReady(ctx, linkA.ID, "note-a", "fp-a"); err != nil {
		t.Fatalf("MarkReady A: %v", err)
	}
	gotB, err := store.GetByKey(ctx, keyB)
	if err != nil {
		t.Fatalf("GetByKey B: %v", err)
	}
	if gotB.Status != StatusPending || gotB.NoteID != "" {
		t.Fatalf("marking A ready must not affect B, got %+v", gotB)
	}
}

func TestGetByKey_NotFound(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.GetByKey(context.Background(), testKey()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestFingerprint_DeterministicAndSensitiveToChange(t *testing.T) {
	a := FingerprintInput{Title: "Sync", StartTime: "2026-07-20T10:00:00Z", EndTime: "2026-07-20T11:00:00Z"}
	b := a
	if Fingerprint(a) != Fingerprint(b) {
		t.Fatal("identical inputs must fingerprint identically")
	}
	b.Title = "Different"
	if Fingerprint(a) == Fingerprint(b) {
		t.Fatal("a changed title must change the fingerprint")
	}
}
