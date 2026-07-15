package progression

import (
	"errors"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/types"
	ws "github.com/johnjallday/ori-agent/internal/workspace"
)

var (
	// ErrQuestNotFound is returned by Skip for an unknown quest ID.
	ErrQuestNotFound = errors.New("progression: quest not found")
	// ErrQuestNotOptional is returned by Skip when the quest is not marked
	// Optional — only optional quests may be skipped.
	ErrQuestNotOptional = errors.New("progression: quest is not optional")
)

// Engine tracks quest completion for a single install. It owns the in-memory
// progression state (the single writer) and persists every change through the
// StateStore. All methods are safe for concurrent use — HandleEvent is called
// from event-bus goroutines.
type Engine struct {
	mu     sync.Mutex
	store  StateStore
	quests []Quest
	state  types.ProgressionState

	// onComplete is called (outside the lock) when a quest completes live
	// (not during backfill). Optional — used for logging/notifications.
	onComplete func(Quest)

	now func() time.Time
}

// New creates an engine backed by store, loading any persisted completions.
func New(store StateStore, opts ...Option) *Engine {
	e := &Engine{
		store:  store,
		quests: BuiltinQuests(),
		now:    time.Now,
	}
	if store != nil {
		e.state = store.GetProgression()
	}
	if e.state.CompletedQuests == nil {
		e.state.CompletedQuests = map[string]time.Time{}
	}
	if e.state.SkippedQuests == nil {
		e.state.SkippedQuests = map[string]time.Time{}
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Option configures an Engine.
type Option func(*Engine)

// WithOnComplete registers a callback fired when a quest completes live.
func WithOnComplete(fn func(Quest)) Option {
	return func(e *Engine) { e.onComplete = fn }
}

// questByID returns the quest with the given ID, or false.
func (e *Engine) questByID(id string) (Quest, bool) {
	for _, q := range e.quests {
		if q.ID == id {
			return q, true
		}
	}
	return Quest{}, false
}

// HandleEvent runs live detection for a single event across ALL quests,
// regardless of the current tier — a user who jumps ahead gets credit
// immediately. Fire-and-forget: never returns an error into the caller's path.
func (e *Engine) HandleEvent(ev ws.Event) {
	var completed []Quest

	e.mu.Lock()
	for _, q := range e.quests {
		if q.Match == nil {
			continue
		}
		if _, done := e.state.CompletedQuests[q.ID]; done {
			continue
		}
		if q.Match(ev) {
			e.markLocked(q.ID)
			completed = append(completed, q)
		}
	}
	dirty := len(completed) > 0
	if dirty {
		e.persistLocked()
	}
	e.mu.Unlock()

	for _, q := range completed {
		if e.onComplete != nil {
			e.onComplete(q)
		}
	}
}

// Complete marks a single quest complete by ID (for non-event code paths, e.g.
// renaming the assistant). Returns true if it was newly completed. Safe to
// call repeatedly — completion is idempotent.
func (e *Engine) Complete(questID string) bool {
	q, ok := e.questByID(questID)
	if !ok {
		return false
	}

	e.mu.Lock()
	if _, done := e.state.CompletedQuests[questID]; done {
		e.mu.Unlock()
		return false
	}
	e.markLocked(questID)
	e.persistLocked()
	e.mu.Unlock()

	if e.onComplete != nil {
		e.onComplete(q)
	}
	return true
}

// Skip marks an optional quest as explicitly skipped. It is idempotent:
// skipping an already-skipped quest is a no-op success, and skipping an
// already-completed quest is a no-op success too (a real completion is never
// downgraded). Returns ErrQuestNotFound for an unknown ID and
// ErrQuestNotOptional when the quest does not allow skipping.
func (e *Engine) Skip(questID string) error {
	q, ok := e.questByID(questID)
	if !ok {
		return ErrQuestNotFound
	}
	if !q.Optional {
		return ErrQuestNotOptional
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if _, done := e.state.CompletedQuests[questID]; done {
		return nil
	}
	if _, skipped := e.state.SkippedQuests[questID]; skipped {
		return nil
	}
	if e.state.SkippedQuests == nil {
		e.state.SkippedQuests = map[string]time.Time{}
	}
	e.state.SkippedQuests[questID] = e.now()
	return e.persistLocked()
}

// Backfill runs the one-time startup scan: any quest already satisfied by
// existing state is marked complete SILENTLY (no onComplete). This grandfathers
// established installs so they never see beginner quests or toasts. It is a
// no-op if the backfill has already run.
func (e *Engine) Backfill(scanner Scanner) error {
	if scanner == nil {
		return nil
	}

	e.mu.Lock()
	if !e.state.BackfilledAt.IsZero() {
		e.mu.Unlock()
		return nil
	}
	e.mu.Unlock()

	snap := scanner.Scan()

	e.mu.Lock()
	defer e.mu.Unlock()
	// Re-check under lock in case a concurrent call won the race.
	if !e.state.BackfilledAt.IsZero() {
		return nil
	}
	for _, q := range e.quests {
		if q.Satisfied == nil {
			continue
		}
		if _, done := e.state.CompletedQuests[q.ID]; done {
			continue
		}
		if q.Satisfied(snap) {
			e.markLocked(q.ID)
		}
	}
	e.state.BackfilledAt = e.now()
	return e.persistLocked()
}

// SetDismissed persists whether the user has hidden the quest-log widget.
func (e *Engine) SetDismissed(dismissed bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.state.Dismissed = dismissed
	return e.persistLocked()
}

// Reset clears all progression state. It marks the backfill as already
// consumed (BackfilledAt = now) so an explicit reset is a blank slate that
// survives restarts — existing workspaces/agents will not silently re-complete
// quests via the startup backfill. Live events still complete quests going
// forward.
func (e *Engine) Reset() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.state = types.ProgressionState{
		CompletedQuests: map[string]time.Time{},
		SkippedQuests:   map[string]time.Time{},
		BackfilledAt:    e.now(),
	}
	return e.persistLocked()
}

// Status returns the full quest graph with derived per-quest status and the
// current tier for the API/UI.
func (e *Engine) Status() Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.statusLocked()
}

// --- lock-held helpers ---

// markLocked records a completion. If the quest was previously skipped, the
// skip is replaced by the real completion (FR: a later observed action always
// wins). Caller must hold the lock.
func (e *Engine) markLocked(questID string) {
	if e.state.CompletedQuests == nil {
		e.state.CompletedQuests = map[string]time.Time{}
	}
	e.state.CompletedQuests[questID] = e.now()
	delete(e.state.SkippedQuests, questID)
}

// persistLocked writes state through the store. Caller must hold the lock.
func (e *Engine) persistLocked() error {
	e.state.UpdatedAt = e.now()
	if e.store == nil {
		return nil
	}
	if err := e.store.SetProgression(e.state); err != nil {
		logger.Error("progression: failed to persist state", logger.Fields{"error": err})
		return err
	}
	return nil
}

// resolvedLocked reports whether a quest is resolved: either completed (its
// action was actually observed) or, for an optional quest, explicitly
// skipped. Caller must hold the lock.
func (e *Engine) resolvedLocked(questID string) bool {
	if _, done := e.state.CompletedQuests[questID]; done {
		return true
	}
	_, skipped := e.state.SkippedQuests[questID]
	return skipped
}

// currentTierLocked returns the lowest tier that is not fully resolved
// (completed or, for optional quests, skipped), or TotalTiers when everything
// is done. A skipped optional quest never keeps a later tier locked. Caller
// must hold the lock.
func (e *Engine) currentTierLocked() int {
	for tier := 1; tier <= TotalTiers; tier++ {
		for _, q := range e.quests {
			if q.Tier != tier {
				continue
			}
			if !e.resolvedLocked(q.ID) {
				return tier
			}
		}
	}
	return TotalTiers
}

// statusLocked builds the API view. Caller must hold the lock.
func (e *Engine) statusLocked() Status {
	current := e.currentTierLocked()

	byTier := map[int]*TierView{}
	order := []int{}
	completedCount := 0
	resolvedCount := 0
	var next *QuestView

	for _, q := range e.quests {
		completedAt, done := e.state.CompletedQuests[q.ID]
		skippedAt, skipped := e.state.SkippedQuests[q.ID]
		resolved := done || skipped

		status := StatusLocked
		switch {
		case done:
			status = StatusCompleted
			completedCount++
			resolvedCount++
		case skipped:
			status = StatusSkipped
			resolvedCount++
		case q.Tier <= current:
			status = StatusAvailable
		}

		qv := QuestView{
			ID: q.ID, Tier: q.Tier, Title: q.Title, Why: q.Why, Status: status,
			ActionURL: q.ActionURL, ActionLabel: q.ActionLabel, Optional: q.Optional,
		}
		if done {
			at := completedAt
			qv.CompletedAt = &at
		}
		if skipped {
			at := skippedAt
			qv.SkippedAt = &at
		}

		tv, ok := byTier[q.Tier]
		if !ok {
			tv = &TierView{Tier: q.Tier, Name: TierName(q.Tier), Complete: true}
			byTier[q.Tier] = tv
			order = append(order, q.Tier)
		}
		if !resolved {
			tv.Complete = false
		}
		tv.Quests = append(tv.Quests, qv)

		if next == nil && status == StatusAvailable {
			nq := qv
			next = &nq
		}
	}

	tiers := make([]TierView, 0, len(order))
	for _, t := range order {
		tiers = append(tiers, *byTier[t])
	}

	total := len(e.quests)
	return Status{
		Tiers:          tiers,
		CurrentTier:    current,
		TotalTiers:     TotalTiers,
		CompletedCount: completedCount,
		ResolvedCount:  resolvedCount,
		TotalCount:     total,
		AllComplete:    resolvedCount == total,
		Dismissed:      e.state.Dismissed,
		NextQuest:      next,
	}
}
