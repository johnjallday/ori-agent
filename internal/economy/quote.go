package economy

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// The three things a schedule save can be (FR27). `none` covers everything that
// is free: a decrease, no cadence change at all, a non-cadence edit, and
// re-enabling a schedule on a task that already paid its build.
const (
	ActionNone    = "none"
	ActionBuild   = "build"
	ActionUpgrade = "upgrade"
)

// Quote is what a save would charge, before it charges it (FR27). The editor
// renders the price line from this, and the save path charges from the same
// function — so what the user was shown and what they are charged cannot drift
// apart except by another client spending in between.
type Quote struct {
	Action       string `json:"action"`
	Resource     string `json:"resource,omitempty"`
	Cost         int64  `json:"cost"`
	Balance      int64  `json:"balance"`
	Affordable   bool   `json:"affordable"`
	FromTier     int    `json:"from_tier,omitempty"`
	FromTierName string `json:"from_tier_name,omitempty"`
	ToTier       int    `json:"to_tier,omitempty"`
	ToTierName   string `json:"to_tier_name,omitempty"`
	// Runs per day before and after, so an upgrade's price is shown next to
	// what it actually buys (FR39).
	RunsPerDayBefore float64 `json:"runs_per_day_before,omitempty"`
	RunsPerDayAfter  float64 `json:"runs_per_day_after,omitempty"`
	CreativeMode     bool    `json:"creative_mode"`
}

// ScheduleChange is the save being priced: which task, and what its schedule and
// enabled flag would be afterwards. An empty TaskID means a task being created.
type ScheduleChange struct {
	WorkspaceID     string
	TaskID          string
	Schedule        *workspace.ScheduleConfig
	ScheduleEnabled bool
}

// Quote prices a schedule change without charging for it.
//
// The rules, in the order they are decided:
//
//   - The result is not a Farm: free. Turning a Farm off costs nothing, and
//     neither does editing a task that never had a cadence.
//   - The task has never paid its build: it costs FarmBuildCost in Craft, once,
//     at any tier (FR17, FR18). Disabling and re-enabling a schedule is
//     therefore free forever after — the build entry is what is remembered, not
//     the enabled flag.
//   - It was already a Farm and the new cadence is faster: it costs the summed
//     upgrade steps in Harvest (FR19).
//   - Anything else — slower, unchanged, or a non-cadence edit: free (FR20).
//
// Creative mode zeroes the cost but still reports the action, so the editor can
// say "Build Farm" with the price struck through rather than showing nothing and
// leaving the user unsure whether anything happened (FR24, FR39).
func (s *Service) Quote(ctx context.Context, change ScheduleChange) (Quote, error) {
	if !s.Available() {
		return Quote{}, ErrStoreUnavailable
	}
	creative := s.CreativeMode()
	balances, err := s.store.Balances(ctx)
	if err != nil {
		return Quote{}, err
	}

	free := Quote{Action: ActionNone, CreativeMode: creative, Affordable: true}

	if !IsFarm(change.Schedule, change.ScheduleEnabled) {
		return free, nil
	}

	now := s.clock()
	toTier := TierFor(change.Schedule, now)

	taskID := strings.TrimSpace(change.TaskID)
	built := false
	if taskID != "" {
		built, err = s.store.HasEntry(ctx, ResourceCraft, RefKindBuild, taskID)
		if err != nil {
			return Quote{}, fmt.Errorf("check whether %s is already built: %w", taskID, err)
		}
	}

	if !built {
		return s.price(Quote{
			Action:          ActionBuild,
			Resource:        ResourceCraft,
			Cost:            FarmBuildCost,
			Balance:         balances.Craft,
			ToTier:          toTier,
			ToTierName:      TierName(toTier),
			RunsPerDayAfter: RunsPerDay(toTier),
			CreativeMode:    creative,
		}), nil
	}

	// An already-built task: the price depends on where its cadence is today.
	// A task whose schedule is currently off has no current tier to step up
	// from, so re-enabling it is free at any cadence (FR17).
	fromTier := 0
	if s.tasks != nil {
		if current, ok := s.tasks.Task(change.WorkspaceID, taskID); ok {
			if IsFarm(current.Schedule, current.ScheduleEnabled) {
				fromTier = TierFor(current.Schedule, now)
			}
		}
	}
	if fromTier == 0 || toTier <= fromTier {
		return free, nil
	}

	return s.price(Quote{
		Action:           ActionUpgrade,
		Resource:         ResourceHarvest,
		Cost:             UpgradeCost(fromTier, toTier),
		Balance:          balances.Harvest,
		FromTier:         fromTier,
		FromTierName:     TierName(fromTier),
		ToTier:           toTier,
		ToTierName:       TierName(toTier),
		RunsPerDayBefore: RunsPerDay(fromTier),
		RunsPerDayAfter:  RunsPerDay(toTier),
		CreativeMode:     creative,
	}), nil
}

// price applies creative mode and settles affordability. Creative mode zeroes
// the cost rather than skipping the quote, so the action and the tiers still
// travel to the editor.
func (s *Service) price(quote Quote) Quote {
	if quote.CreativeMode {
		quote.Cost = 0
	}
	quote.Affordable = quote.Cost <= quote.Balance
	return quote
}

// InsufficientResourcesError is the refusal a save gets when the cost exceeds
// the balance (FR22). It carries exactly the fields the 409 body needs, so the
// handler serializes it rather than rebuilding it.
type InsufficientResourcesError struct {
	Resource string `json:"resource"`
	Required int64  `json:"required"`
	Balance  int64  `json:"balance"`
	Action   string `json:"action"`
}

func (e *InsufficientResourcesError) Error() string {
	return fmt.Sprintf("insufficient %s: %s costs %d, balance is %d",
		e.Resource, e.Action, e.Required, e.Balance)
}

// Charge writes the debit for a schedule change that has already been saved.
//
// It is deliberately split from Quote and called AFTER the task persists
// (FR23): a user must never be charged for a save that did not happen. The cost
// of that ordering is that a debit can fail after a successful save, which is
// logged and accepted — the alternative is charging for nothing.
//
// The reference makes the write idempotent: a build is keyed by task id, so a
// task can only ever be built once, and an upgrade is keyed by task, tier, and
// time, so two upgrades to the same tier are two separate charges while a
// retried write of one is not.
func (s *Service) Charge(ctx context.Context, change ScheduleChange, quote Quote) error {
	if !s.Available() {
		return ErrStoreUnavailable
	}
	if quote.Action == ActionNone {
		return nil
	}
	taskID := strings.TrimSpace(change.TaskID)
	if taskID == "" {
		return fmt.Errorf("charging a schedule change needs a task id")
	}

	now := s.clock()
	entry := Entry{
		Resource:    quote.Resource,
		Amount:      quote.Cost,
		WorkspaceID: change.WorkspaceID,
	}
	switch quote.Action {
	case ActionBuild:
		entry.Reason = ReasonBuild
		entry.RefKind = RefKindBuild
		entry.RefID = taskID
	case ActionUpgrade:
		entry.Reason = ReasonUpgrade
		entry.RefKind = RefKindUpgrade
		entry.RefID = fmt.Sprintf("%s@%d@%s", taskID, quote.ToTier, now.UTC().Format(time.RFC3339Nano))
	default:
		return fmt.Errorf("unknown economy action %q", quote.Action)
	}

	// A zero-cost build still writes its entry: in creative mode that entry is
	// what stops the task being charged if creative mode is later switched off,
	// which is the same promise grandfathering makes (FR33).
	_, err := s.store.Debit(ctx, entry, now)
	return err
}

// PriceScheduleChange is the one call a save path makes.
//
// It quotes, refuses when the quote is unaffordable, and hands back the quote so
// the caller can charge it after the task persists. Refusal happens BEFORE the
// task is touched, which is what makes FR22's "the task must not be modified in
// any way" true rather than merely intended.
func (s *Service) PriceScheduleChange(ctx context.Context, change ScheduleChange) (Quote, error) {
	quote, err := s.Quote(ctx, change)
	if err != nil {
		return Quote{}, err
	}
	if quote.Action == ActionNone || quote.Affordable {
		return quote, nil
	}
	return quote, &InsufficientResourcesError{
		Resource: quote.Resource,
		Required: quote.Cost,
		Balance:  quote.Balance,
		Action:   quote.Action,
	}
}
