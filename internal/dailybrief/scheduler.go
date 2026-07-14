package dailybrief

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
)

// ScheduledWorkspace is one HQ workspace/user pair the scheduler should
// check each tick.
type ScheduledWorkspace struct {
	WorkspaceID string
	UserID      string
}

// WorkspaceLister supplies the set of workspaces to check each tick — in
// v1, the single designated Personal HQ (if any), expressed as a list for
// forward compatibility. Implemented by the server wiring layer (which
// knows about internal/personalhq), not this package.
type WorkspaceLister interface {
	ListScheduledWorkspaces(ctx context.Context) ([]ScheduledWorkspace, error)
}

// Scheduler polls at a fixed interval and requests a scheduled generation
// for each workspace whose configured schedule is due. It has no claim
// table of its own — Service.RequestGeneration's existing store-level dedup
// already guarantees at most one scheduled/first-open generation per
// workspace/local-date, including across app restarts and multiple ticks
// landing on the same due occurrence (PRD 5.6-5.8).
type Scheduler struct {
	svc          *Service
	workspaces   WorkspaceLister
	pollInterval time.Duration
	now          func() time.Time // overridable in tests; defaults to time.Now

	stopChan chan struct{}
	wg       sync.WaitGroup
	stopOnce sync.Once
}

// NewScheduler constructs a Daily Brief scheduler. pollInterval defaults to
// one minute when zero/negative.
func NewScheduler(svc *Service, workspaces WorkspaceLister, pollInterval time.Duration) *Scheduler {
	if pollInterval <= 0 {
		pollInterval = time.Minute
	}
	return &Scheduler{
		svc:          svc,
		workspaces:   workspaces,
		pollInterval: pollInterval,
		now:          time.Now,
		stopChan:     make(chan struct{}),
	}
}

// Start begins the poll loop in a background goroutine.
func (s *Scheduler) Start() {
	s.wg.Add(1)
	go s.pollLoop()
}

// Stop signals the poll loop to exit and waits for it to finish. Safe to
// call multiple times.
func (s *Scheduler) Stop() {
	s.stopOnce.Do(func() { close(s.stopChan) })
	s.wg.Wait()
}

func (s *Scheduler) pollLoop() {
	defer s.wg.Done()
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()
	s.Tick() // run immediately on start, so app downtime catches up promptly
	for {
		select {
		case <-s.stopChan:
			return
		case <-ticker.C:
			s.Tick()
		}
	}
}

// Tick runs one scheduling pass. Exported so tests (and a manual/debug
// trigger) can drive it directly without waiting on the poll interval.
func (s *Scheduler) Tick() {
	ctx := context.Background()
	if s.workspaces == nil {
		return
	}
	workspaces, err := s.workspaces.ListScheduledWorkspaces(ctx)
	if err != nil {
		logger.Warn("dailybrief scheduler: failed to list scheduled workspaces", logger.Fields{"error": err})
		return
	}
	for _, w := range workspaces {
		s.checkOne(ctx, w)
	}
}

func (s *Scheduler) checkOne(ctx context.Context, w ScheduledWorkspace) {
	cfg, err := s.svc.GetConfig(ctx, w.WorkspaceID)
	if err != nil {
		return // no Daily Brief config yet; nothing to schedule
	}
	if !cfg.ScheduleEnabled {
		return
	}
	loc, err := ResolveTimezone(cfg.Timezone)
	if err != nil {
		logger.Warn("dailybrief scheduler: invalid timezone", logger.Fields{"workspace_id": w.WorkspaceID, "error": err})
		return
	}
	now := s.now().In(loc)
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)

	// The occurrence for TODAY, if the schedule has one: ask for the first
	// candidate strictly after a moment just before today started. If app
	// downtime spans the configured time, this still resolves to today's
	// occurrence (already in the past relative to `now`), producing exactly
	// one catch-up generation via the store's dedup rather than one per
	// missed tick.
	occurrence, ok, err := NextOccurrence(*cfg, todayStart.Add(-time.Nanosecond))
	if err != nil || !ok {
		return
	}
	if !sameLocalDate(occurrence.In(loc), now) {
		return // today isn't a scheduled day (or the schedule was just disabled)
	}
	if now.Before(occurrence) {
		return // not due yet
	}

	localDate := LocalDateKey(now)
	if _, err := s.svc.RequestGeneration(ctx, *cfg, w.UserID, TriggerScheduled, localDate); err != nil {
		if !errors.Is(err, ErrGenerationInProgress) {
			logger.Warn("dailybrief scheduler: scheduled generation failed", logger.Fields{"workspace_id": w.WorkspaceID, "error": err})
		}
	}
}

func sameLocalDate(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}
