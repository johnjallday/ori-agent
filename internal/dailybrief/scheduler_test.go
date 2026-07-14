package dailybrief

import (
	"context"
	"testing"
	"time"
)

type fixedWorkspaceLister struct {
	workspaces []ScheduledWorkspace
}

func (f *fixedWorkspaceLister) ListScheduledWorkspaces(ctx context.Context) ([]ScheduledWorkspace, error) {
	return f.workspaces, nil
}

func newSchedulerTestService(t *testing.T) (*Service, *fakeGenerator) {
	t.Helper()
	store := newTestStore(t)
	gen := &fakeGenerator{result: GenerationResult{Status: GenerationSucceeded}}
	return NewService(store, gen), gen
}

func setSchedulerConfig(t *testing.T, svc *Service, cfg Config) {
	t.Helper()
	if _, err := svc.UpdateConfig(context.Background(), cfg); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}
}

func newTestScheduler(svc *Service, at time.Time) (*Scheduler, *fixedWorkspaceLister) {
	lister := &fixedWorkspaceLister{workspaces: []ScheduledWorkspace{{WorkspaceID: "ws-1", UserID: "local"}}}
	sched := NewScheduler(svc, lister, time.Minute)
	sched.now = func() time.Time { return at }
	return sched, lister
}

func TestScheduler_DoesNotGenerateBeforeScheduledTime(t *testing.T) {
	svc, gen := newSchedulerTestService(t)
	setSchedulerConfig(t, svc, Config{WorkspaceID: "ws-1", Timezone: "UTC", ScheduleTime: "08:00", ScheduleDays: allDays(), ScheduleEnabled: true})

	sched, _ := newTestScheduler(svc, time.Date(2026, 7, 14, 7, 59, 0, 0, time.UTC))
	sched.Tick()
	if gen.callCount() != 0 {
		t.Fatalf("expected no generation before scheduled time, got %d calls", gen.callCount())
	}
}

func TestScheduler_GeneratesOnceScheduledTimeArrives(t *testing.T) {
	svc, gen := newSchedulerTestService(t)
	setSchedulerConfig(t, svc, Config{WorkspaceID: "ws-1", Timezone: "UTC", ScheduleTime: "08:00", ScheduleDays: allDays(), ScheduleEnabled: true})

	sched, _ := newTestScheduler(svc, time.Date(2026, 7, 14, 8, 0, 0, 0, time.UTC))
	sched.Tick()
	if gen.callCount() != 1 {
		t.Fatalf("expected exactly one generation once due, got %d calls", gen.callCount())
	}
	current, err := svc.GetCurrent(context.Background(), "ws-1")
	if err != nil || current.Trigger != TriggerScheduled {
		t.Fatalf("expected a current scheduled revision, got %#v err=%v", current, err)
	}
}

// TestScheduler_MultipleTicksSameDayGenerateOnlyOnce covers task 5.6: the
// per-workspace/local-date dedup must survive repeated ticks landing on the
// same due occurrence (the normal poll-interval steady state).
func TestScheduler_MultipleTicksSameDayGenerateOnlyOnce(t *testing.T) {
	svc, gen := newSchedulerTestService(t)
	setSchedulerConfig(t, svc, Config{WorkspaceID: "ws-1", Timezone: "UTC", ScheduleTime: "08:00", ScheduleDays: allDays(), ScheduleEnabled: true})

	sched, _ := newTestScheduler(svc, time.Date(2026, 7, 14, 8, 5, 0, 0, time.UTC))
	for i := 0; i < 5; i++ {
		sched.Tick()
	}
	if gen.callCount() != 1 {
		t.Fatalf("expected exactly one generation across 5 ticks the same day, got %d calls", gen.callCount())
	}
}

// TestScheduler_AppDowntimeCatchesUpOnce covers task 5.7/5.8: if the app was
// off across the scheduled time and comes back up later the same local
// date, the scheduler must still generate exactly once (a catch-up), not
// skip the day and not generate repeatedly.
func TestScheduler_AppDowntimeCatchesUpOnce(t *testing.T) {
	svc, gen := newSchedulerTestService(t)
	setSchedulerConfig(t, svc, Config{WorkspaceID: "ws-1", Timezone: "UTC", ScheduleTime: "08:00", ScheduleDays: allDays(), ScheduleEnabled: true})

	// App comes back at 14:00, well after the 08:00 scheduled time.
	sched, _ := newTestScheduler(svc, time.Date(2026, 7, 14, 14, 0, 0, 0, time.UTC))
	sched.Tick()
	if gen.callCount() != 1 {
		t.Fatalf("expected exactly one catch-up generation, got %d calls", gen.callCount())
	}
}

func TestScheduler_SkipsDaysNotInSchedule(t *testing.T) {
	svc, gen := newSchedulerTestService(t)
	// 2026-07-14 is a Tuesday; schedule only Mondays.
	setSchedulerConfig(t, svc, Config{WorkspaceID: "ws-1", Timezone: "UTC", ScheduleTime: "08:00", ScheduleDays: []string{"mon"}, ScheduleEnabled: true})

	sched, _ := newTestScheduler(svc, time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC))
	sched.Tick()
	if gen.callCount() != 0 {
		t.Fatalf("expected no generation on a non-scheduled day, got %d calls", gen.callCount())
	}
}

func TestScheduler_SkipsWhenScheduleDisabled(t *testing.T) {
	svc, gen := newSchedulerTestService(t)
	setSchedulerConfig(t, svc, Config{WorkspaceID: "ws-1", Timezone: "UTC", ScheduleTime: "08:00", ScheduleDays: allDays(), ScheduleEnabled: false})

	sched, _ := newTestScheduler(svc, time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC))
	sched.Tick()
	if gen.callCount() != 0 {
		t.Fatalf("expected no generation when the schedule is disabled, got %d calls", gen.callCount())
	}
}

func TestScheduler_SkipsWorkspaceWithNoConfig(t *testing.T) {
	store := newTestStore(t)
	gen := &fakeGenerator{result: GenerationResult{Status: GenerationSucceeded}}
	svc := NewService(store, gen)
	// No config ever set for ws-1.
	sched, _ := newTestScheduler(svc, time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC))
	sched.Tick()
	if gen.callCount() != 0 {
		t.Fatalf("expected no generation for an unconfigured workspace, got %d calls", gen.callCount())
	}
}

// TestScheduler_StartStopLifecycle proves the poll loop starts and stops
// cleanly without leaking a goroutine or hanging.
func TestScheduler_StartStopLifecycle(t *testing.T) {
	svc, _ := newSchedulerTestService(t)
	sched, _ := newTestScheduler(svc, time.Now())
	sched.pollInterval = time.Millisecond
	sched.Start()
	time.Sleep(20 * time.Millisecond)
	done := make(chan struct{})
	go func() {
		sched.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() did not return in time")
	}
	// Calling Stop again must not panic or hang (sync.Once guard).
	sched.Stop()
}
