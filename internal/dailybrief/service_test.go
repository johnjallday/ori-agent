package dailybrief

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeGenerator is a controllable Generator for service tests.
type fakeGenerator struct {
	mu         sync.Mutex
	calls      int
	result     GenerationResult
	err        error
	block      chan struct{} // if non-nil, Generate waits on this before returning
	onGenerate func()
}

func (f *fakeGenerator) Generate(ctx context.Context, req GenerationRequest, cfg Config) (GenerationResult, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if f.onGenerate != nil {
		f.onGenerate()
	}
	if f.block != nil {
		<-f.block
	}
	return f.result, f.err
}

func (f *fakeGenerator) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func newServiceTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	return newTestStore(t)
}

func seedConfig(t *testing.T, store Store, workspaceID string) Config {
	t.Helper()
	cfg, err := NormalizeConfig(Config{WorkspaceID: workspaceID, UserID: "local", Timezone: "UTC"})
	if err != nil {
		t.Fatalf("NormalizeConfig: %v", err)
	}
	if err := store.UpsertConfig(context.Background(), &cfg); err != nil {
		t.Fatalf("UpsertConfig: %v", err)
	}
	got, err := store.GetConfig(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	return *got
}

func TestService_UpdateConfigValidatesAndPersists(t *testing.T) {
	store := newServiceTestStore(t)
	svc := NewService(store, nil)
	ctx := context.Background()

	_, err := svc.UpdateConfig(ctx, Config{WorkspaceID: "ws-1", Timezone: "Not/AZone"})
	if !errors.Is(err, ErrInvalidTimezone) {
		t.Fatalf("expected ErrInvalidTimezone, got %v", err)
	}

	got, err := svc.UpdateConfig(ctx, Config{WorkspaceID: "ws-1", Timezone: "America/New_York"})
	if err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}
	if got.Timezone != "America/New_York" {
		t.Fatalf("timezone = %q", got.Timezone)
	}
}

func TestService_RequestGeneration_FirstOpenSucceedsAndBecomesCurrent(t *testing.T) {
	store := newServiceTestStore(t)
	gen := &fakeGenerator{result: GenerationResult{Status: GenerationSucceeded, ContentJSON: `{"summary":"hi"}`}}
	svc := NewService(store, gen)
	ctx := context.Background()
	cfg := seedConfig(t, store, "ws-1")

	rev, err := svc.RequestGenerationNow(ctx, "ws-1", "local", TriggerFirstOpen)
	if err != nil {
		t.Fatalf("RequestGenerationNow: %v", err)
	}
	if rev.Status != GenerationSucceeded || !rev.IsCurrent {
		t.Fatalf("expected succeeded+current revision, got %#v", rev)
	}
	if gen.callCount() != 1 {
		t.Fatalf("expected generator called once, got %d", gen.callCount())
	}

	current, err := svc.GetCurrent(ctx, "ws-1")
	if err != nil || current.ID != rev.ID {
		t.Fatalf("GetCurrent: %#v err=%v", current, err)
	}
	_ = cfg
}

// TestService_RequestGeneration_ScheduledThenFirstOpenDedupes covers PRD
// FR55/FR59: when a scheduled brief already exists for today, first-open
// must show it rather than generating another one.
func TestService_RequestGeneration_ScheduledThenFirstOpenDedupes(t *testing.T) {
	store := newServiceTestStore(t)
	gen := &fakeGenerator{result: GenerationResult{Status: GenerationSucceeded, ContentJSON: "v1"}}
	svc := NewService(store, gen)
	ctx := context.Background()
	seedConfig(t, store, "ws-1")

	first, err := svc.RequestGenerationNow(ctx, "ws-1", "local", TriggerScheduled)
	if err != nil {
		t.Fatalf("scheduled request: %v", err)
	}
	second, err := svc.RequestGenerationNow(ctx, "ws-1", "local", TriggerFirstOpen)
	if err != nil {
		t.Fatalf("first-open request: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("expected first-open to return the same revision as the scheduled one, got %s vs %s", second.ID, first.ID)
	}
	if gen.callCount() != 1 {
		t.Fatalf("expected the generator to run exactly once, got %d", gen.callCount())
	}
}

// TestService_RequestGeneration_ManualAlwaysCreatesNewRevision covers PRD
// FR58: manual refresh creates a new same-day revision and replaces current.
func TestService_RequestGeneration_ManualAlwaysCreatesNewRevision(t *testing.T) {
	store := newServiceTestStore(t)
	gen := &fakeGenerator{result: GenerationResult{Status: GenerationSucceeded, ContentJSON: "v1"}}
	svc := NewService(store, gen)
	ctx := context.Background()
	seedConfig(t, store, "ws-1")

	first, err := svc.RequestGenerationNow(ctx, "ws-1", "local", TriggerFirstOpen)
	if err != nil {
		t.Fatalf("first request: %v", err)
	}
	gen.result = GenerationResult{Status: GenerationSucceeded, ContentJSON: "v2"}
	second, err := svc.RequestGenerationNow(ctx, "ws-1", "local", TriggerManual)
	if err != nil {
		t.Fatalf("manual request: %v", err)
	}
	if second.ID == first.ID {
		t.Fatal("expected manual refresh to create a distinct revision")
	}
	if second.RevisionNumber != first.RevisionNumber+1 {
		t.Fatalf("expected revision number to increment, got %d then %d", first.RevisionNumber, second.RevisionNumber)
	}
	current, err := svc.GetCurrent(ctx, "ws-1")
	if err != nil || current.ID != second.ID {
		t.Fatalf("expected the manual revision to become current, got %#v err=%v", current, err)
	}
	if gen.callCount() != 2 {
		t.Fatalf("expected the generator to run twice, got %d", gen.callCount())
	}
}

// TestService_RequestGeneration_FailurePreservesLastSuccessfulCurrent covers
// PRD 5.12: a failed generation must not erase the last successful brief.
func TestService_RequestGeneration_FailurePreservesLastSuccessfulCurrent(t *testing.T) {
	store := newServiceTestStore(t)
	gen := &fakeGenerator{result: GenerationResult{Status: GenerationSucceeded, ContentJSON: "v1"}}
	svc := NewService(store, gen)
	ctx := context.Background()
	seedConfig(t, store, "ws-1")

	good, err := svc.RequestGenerationNow(ctx, "ws-1", "local", TriggerFirstOpen)
	if err != nil {
		t.Fatalf("first request: %v", err)
	}

	gen.err = errors.New("model unavailable")
	if _, err := svc.RequestGenerationNow(ctx, "ws-1", "local", TriggerManual); err == nil {
		t.Fatal("expected the manual refresh to report the generator's error")
	}

	current, err := svc.GetCurrent(ctx, "ws-1")
	if err != nil {
		t.Fatalf("GetCurrent after failure: %v", err)
	}
	if current.ID != good.ID {
		t.Fatalf("expected the last successful revision to remain current, got %#v", current)
	}
}

// TestService_RequestGeneration_ConcurrentCallsSerializePerWorkspace covers
// task 5.10: two concurrent calls for the same workspace must not both
// invoke the (potentially slow) generator at once.
func TestService_RequestGeneration_ConcurrentCallsSerializePerWorkspace(t *testing.T) {
	store := newServiceTestStore(t)
	block := make(chan struct{})
	started := make(chan struct{}, 1)
	gen := &fakeGenerator{
		result:     GenerationResult{Status: GenerationSucceeded},
		block:      block,
		onGenerate: func() { started <- struct{}{} },
	}
	svc := NewService(store, gen)
	ctx := context.Background()
	seedConfig(t, store, "ws-1")

	var wg sync.WaitGroup
	errs := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := svc.RequestGenerationNow(ctx, "ws-1", "local", TriggerManual)
		errs <- err
	}()

	<-started // wait until the first call is inside the generator

	_, err := svc.RequestGenerationNow(ctx, "ws-1", "local", TriggerManual)
	if !errors.Is(err, ErrGenerationInProgress) {
		t.Fatalf("expected ErrGenerationInProgress for the concurrent call, got %v", err)
	}

	close(block)
	wg.Wait()
	if firstErr := <-errs; firstErr != nil {
		t.Fatalf("first call should have succeeded once unblocked: %v", firstErr)
	}
}

func TestService_RecordNotificationIfEnabled_OnlyForScheduledAndOptedIn(t *testing.T) {
	store := newServiceTestStore(t)
	svc := NewService(store, nil)
	ctx := context.Background()

	rev := &Revision{ID: "rev-1", WorkspaceID: "ws-1", Trigger: TriggerScheduled, Status: GenerationSucceeded}

	// Not opted in: no notification.
	created, err := svc.RecordNotificationIfEnabled(ctx, Config{WorkspaceID: "ws-1", NotifyOnReady: false}, rev)
	if err != nil || created {
		t.Fatalf("expected no notification when not opted in, got created=%v err=%v", created, err)
	}

	// Opted in but manual trigger: no notification (FR63).
	manualRev := &Revision{ID: "rev-2", WorkspaceID: "ws-1", Trigger: TriggerManual, Status: GenerationSucceeded}
	created, err = svc.RecordNotificationIfEnabled(ctx, Config{WorkspaceID: "ws-1", NotifyOnReady: true}, manualRev)
	if err != nil || created {
		t.Fatalf("expected no notification for a manual trigger, got created=%v err=%v", created, err)
	}

	// Opted in + scheduled + succeeded: notification created, then idempotent.
	created, err = svc.RecordNotificationIfEnabled(ctx, Config{WorkspaceID: "ws-1", NotifyOnReady: true}, rev)
	if err != nil || !created {
		t.Fatalf("expected a notification to be created, got created=%v err=%v", created, err)
	}
	created, err = svc.RecordNotificationIfEnabled(ctx, Config{WorkspaceID: "ws-1", NotifyOnReady: true}, rev)
	if err != nil || created {
		t.Fatalf("expected the second call for the same revision to be a no-op, got created=%v err=%v", created, err)
	}
}

func TestService_PruneHistoryDelegatesWithMinRetention(t *testing.T) {
	store := newServiceTestStore(t)
	svc := NewService(store, nil)
	ctx := context.Background()

	for i := 0; i < 40; i++ {
		date := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, i).Format("2006-01-02")
		if err := store.CreateRevision(ctx, &Revision{
			ID: "rev-" + date, WorkspaceID: "ws-1", UserID: "local", LocalDate: date, RevisionNumber: 1,
			Trigger: TriggerScheduled, Status: GenerationSucceeded,
		}); err != nil {
			t.Fatalf("CreateRevision: %v", err)
		}
	}
	if err := svc.PruneHistory(ctx, "ws-1"); err != nil {
		t.Fatalf("PruneHistory: %v", err)
	}
	history, err := svc.GetHistory(ctx, "ws-1", 100)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(history) != MinRetentionDays {
		t.Fatalf("expected exactly %d retained days, got %d", MinRetentionDays, len(history))
	}
}

func TestService_RequestGeneration_NoGeneratorConfiguredFailsClaim(t *testing.T) {
	store := newServiceTestStore(t)
	svc := NewService(store, nil)
	ctx := context.Background()
	seedConfig(t, store, "ws-1")

	if _, err := svc.RequestGenerationNow(ctx, "ws-1", "local", TriggerFirstOpen); err == nil {
		t.Fatal("expected an error when no generator is configured")
	}
}
