package followup

import (
	"context"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
)

func newTestService(t *testing.T) (*Service, *SQLiteStore) {
	t.Helper()
	db, err := database.Open(context.Background(), &database.Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := NewSQLiteStore(db)
	return NewService(store), store
}

func emailInput(userID, threadID, title string) CaptureInput {
	return CaptureInput{
		UserID: userID, WorkspaceID: "hq-1", Category: CategoryWaitingOn, Direction: DirectionInbound,
		Title: title, Source: SourceRef{Type: "email_thread", ID: threadID, AccountID: "acct-1"},
		Provenance: ProvenanceExplicit, Confidence: ConfidenceHigh,
	}
}

func TestCaptureDeduplicatesBySource(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	f1, err := svc.Capture(ctx, emailInput("u1", "t1", "Waiting on Dana's quote"))
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if f1.Status != StatusActive {
		t.Fatalf("explicit high-confidence should be active, got %v", f1.Status)
	}

	// Reprocess the same thread with a refreshed title → updates in place.
	f2, err := svc.Capture(ctx, emailInput("u1", "t1", "Waiting on Dana's revised quote"))
	if err != nil {
		t.Fatalf("recapture: %v", err)
	}
	if f2.ID != f1.ID {
		t.Fatal("reprocessing the same source must not create a second follow-up")
	}
	items, _ := store.List(ctx, Filter{UserID: "u1"})
	if len(items) != 1 || items[0].Title != "Waiting on Dana's revised quote" {
		t.Fatalf("expected one updated follow-up, got %+v", items)
	}
}

func TestInferredBelowThresholdIsCandidate(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	in := emailInput("u1", "t2", "Maybe reply to Sam")
	in.Provenance = ProvenanceInferred
	in.Confidence = ConfidenceMedium
	f, err := svc.Capture(ctx, in)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if f.Status != StatusCandidate {
		t.Fatalf("inferred medium-confidence must enter as a candidate, got %v", f.Status)
	}
	// High-confidence inference activates automatically.
	in2 := emailInput("u1", "t3", "Send the deck")
	in2.Provenance = ProvenanceInferred
	in2.Confidence = ConfidenceHigh
	f2, _ := svc.Capture(ctx, in2)
	if f2.Status != StatusActive {
		t.Fatalf("high-confidence inference should be active, got %v", f2.Status)
	}
}

func TestLifecycleTransitions(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	in := emailInput("u1", "t4", "Owe Maya a proposal")
	in.Provenance = ProvenanceInferred
	in.Confidence = ConfidenceLow
	f, _ := svc.Capture(ctx, in) // candidate

	if _, err := svc.ConfirmCandidate(ctx, "u1", f.ID); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if got, _ := svc.Snooze(ctx, "u1", f.ID, time.Now().Add(time.Hour)); got.Status != StatusSnoozed {
		t.Fatalf("snooze failed: %v", got.Status)
	}
	if got, _ := svc.Complete(ctx, "u1", f.ID); got.Status != StatusCompleted || got.CompletedAt == nil {
		t.Fatalf("complete failed: %+v", got)
	}
	if got, _ := svc.Reopen(ctx, "u1", f.ID); got.Status != StatusActive || got.CompletedAt != nil {
		t.Fatalf("reopen failed: %+v", got)
	}
	if got, _ := svc.Dismiss(ctx, "u1", f.ID); got.Status != StatusDismissed {
		t.Fatalf("dismiss failed: %v", got.Status)
	}
}

func TestHomeProjectionStaleFirstAndCapped(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()
	base := time.Now().UTC()
	svc.now = func() time.Time { return base }

	// One stale (old update, no due date) and one fresh active.
	stale, _ := svc.Capture(ctx, emailInput("u1", "stale", "old loop"))
	fresh, _ := svc.Capture(ctx, emailInput("u1", "fresh", "new loop"))
	// Backdate the stale one's updated_at beyond the window.
	stale.UpdatedAt = base.Add(-StalenessWindow - time.Hour)
	_ = store.Update(ctx, stale)

	proj, err := svc.HomeProjection(ctx, "u1")
	if err != nil {
		t.Fatalf("projection: %v", err)
	}
	if len(proj) != 2 || proj[0].ID != stale.ID {
		t.Fatalf("stale item must project first, got %+v", proj)
	}
	_ = fresh
}

func TestDueForNudgeIsIdempotentPerWindow(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()
	base := time.Now().UTC()
	svc.now = func() time.Time { return base }

	f, _ := svc.Capture(ctx, emailInput("u1", "t9", "stale loop"))
	f.UpdatedAt = base.Add(-StalenessWindow - time.Hour)
	_ = store.Update(ctx, f)

	due, _ := svc.DueForNudge(ctx, "u1")
	if len(due) != 1 {
		t.Fatalf("expected one due nudge, got %d", len(due))
	}
	if err := svc.MarkNudged(ctx, "u1", f.ID); err != nil {
		t.Fatalf("mark nudged: %v", err)
	}
	// Within the window, no repeat nudge.
	if due, _ := svc.DueForNudge(ctx, "u1"); len(due) != 0 {
		t.Fatalf("must not re-nudge within the window, got %d", len(due))
	}
}

func TestWakeReactivatesElapsedSnooze(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	base := time.Now().UTC()
	svc.now = func() time.Time { return base }
	f, _ := svc.Capture(ctx, emailInput("u1", "t10", "snoozed loop"))
	_, _ = svc.Snooze(ctx, "u1", f.ID, base.Add(time.Hour))

	svc.now = func() time.Time { return base.Add(2 * time.Hour) }
	woken, err := svc.Wake(ctx)
	if err != nil || woken != 1 {
		t.Fatalf("expected 1 woken, got %d err=%v", woken, err)
	}
	got, _ := svc.Get(ctx, "u1", f.ID)
	if got.Status != StatusActive {
		t.Fatalf("snooze should have elapsed to active, got %v", got.Status)
	}
}
