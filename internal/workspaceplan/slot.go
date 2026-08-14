package workspaceplan

import (
	"context"
	"fmt"
	"time"
)

// The workspace execution slot.
//
// A workspace runs one Plan at a time. Many Plans may be drafted, reviewed, and
// approved concurrently; only one may be executing, and the others wait
// visibly rather than silently interleaving (FR-106, FR-107).
//
// Two properties make this safe under concurrency and restarts:
//
//   - Ownership is a primary key. One row per workspace means a second Plan
//     physically cannot hold the slot, whatever the application forgets.
//   - Every acquisition carries a monotonically increasing generation. A worker
//     that acquired the slot, stalled, and woke up after the lease moved on
//     still holds the old generation, so its dispatch is refused. A timestamp
//     alone would not catch that.
//
// The slot arbitrates PLANS. Standalone Tasks keep their own scheduler, global
// maximum, and provider limits entirely untouched — the slot sits above that
// machinery, not inside it (FR-100).

// Lease is a Plan's claim on its workspace's execution slot.
type Lease struct {
	WorkspaceID string `json:"studio_id"`
	PlanID      string `json:"plan_id"`
	// Generation is the fencing token. Every operation that acts on the slot
	// presents it, and a stale one is refused.
	Generation int64 `json:"generation"`
	// Owner names the process or session that acquired it, for diagnostics.
	Owner       string    `json:"owner,omitempty"`
	AcquiredAt  time.Time `json:"acquired_at"`
	HeartbeatAt time.Time `json:"heartbeat_at"`
}

// QueueEntry is a Plan waiting for the slot.
type QueueEntry struct {
	WorkspaceID string    `json:"studio_id"`
	PlanID      string    `json:"plan_id"`
	QueuedAt    time.Time `json:"queued_at"`
	// Position is 1-based, so the UI can say "2nd in line" rather than
	// leaving the user to count (FR-107).
	Position int `json:"position"`
}

// SlotStore persists the execution slot and its queue.
//
// Implementations must make Acquire atomic: two callers racing for a free slot
// must resolve to exactly one winner, with the loser told who holds it.
type SlotStore interface {
	// Acquire claims the slot for a Plan. It returns the lease when the Plan
	// now owns the slot — including when it already did, which makes acquiring
	// idempotent — and ErrExecutionConflict when another Plan holds it.
	Acquire(ctx context.Context, workspaceID, planID, owner string, at time.Time) (*Lease, error)
	// Release gives up the slot. The generation must match the current lease,
	// so a stale worker cannot release someone else's claim.
	Release(ctx context.Context, workspaceID, planID string, generation int64) error
	// CurrentLease returns the workspace's active lease, or nil when the slot
	// is free.
	CurrentLease(ctx context.Context, workspaceID string) (*Lease, error)
	// Heartbeat records that the holder is still alive.
	Heartbeat(ctx context.Context, workspaceID, planID string, generation int64, at time.Time) error

	// Enqueue records a Plan as waiting for the slot. It is idempotent: a Plan
	// already in line keeps its original position rather than going to the
	// back for asking twice.
	Enqueue(ctx context.Context, workspaceID, planID string, at time.Time) error
	// Dequeue removes a Plan from the queue.
	Dequeue(ctx context.Context, workspaceID, planID string) error
	// Queue returns the waiting Plans in order.
	Queue(ctx context.Context, workspaceID string) ([]QueueEntry, error)
}

// SlotCoordinator is the policy over the store: who may take the slot, who
// waits, and what happens when a holder lets go.
type SlotCoordinator struct {
	store SlotStore
	now   func() time.Time
	owner string
	// staleAfter is how long a lease may go without a heartbeat before another
	// Plan may take the slot.
	//
	// It exists for one failure only: a process that died holding the slot. It
	// is deliberately generous, because reclaiming a slot from a holder that is
	// merely slow would run two Plans at once — the exact thing the slot
	// prevents. The fencing generation is what makes reclaiming safe even when
	// this guess is wrong.
	staleAfter time.Duration
}

// DefaultLeaseStaleAfter is how long a lease survives without a heartbeat.
const DefaultLeaseStaleAfter = 15 * time.Minute

// NewSlotCoordinator returns a coordinator over the given store.
func NewSlotCoordinator(store SlotStore, opts ...SlotOption) *SlotCoordinator {
	coordinator := &SlotCoordinator{
		store:      store,
		now:        func() time.Time { return time.Now().UTC() },
		staleAfter: DefaultLeaseStaleAfter,
	}
	for _, opt := range opts {
		opt(coordinator)
	}
	return coordinator
}

// SlotOption configures a SlotCoordinator.
type SlotOption func(*SlotCoordinator)

// WithSlotClock replaces the coordinator clock.
func WithSlotClock(now func() time.Time) SlotOption {
	return func(c *SlotCoordinator) {
		if now != nil {
			c.now = now
		}
	}
}

// WithSlotOwner names the acquiring process, for diagnostics.
func WithSlotOwner(owner string) SlotOption {
	return func(c *SlotCoordinator) { c.owner = owner }
}

// WithLeaseStaleAfter overrides how long a lease survives without a heartbeat.
func WithLeaseStaleAfter(d time.Duration) SlotOption {
	return func(c *SlotCoordinator) {
		if d > 0 {
			c.staleAfter = d
		}
	}
}

// ClaimResult reports the outcome of trying to take the slot.
type ClaimResult struct {
	// Lease is set when the Plan owns the slot.
	Lease *Lease `json:"lease,omitempty"`
	// Waiting is set when another Plan holds it and this one is queued.
	Waiting *QueueEntry `json:"waiting,omitempty"`
	// HolderPlanID names the Plan currently executing, so the UI can say what
	// this Plan is waiting behind rather than only that it waits.
	HolderPlanID string `json:"holder_plan_id,omitempty"`
}

// Owned reports whether the claim succeeded.
func (r ClaimResult) Owned() bool { return r.Lease != nil }

// Claim tries to take the workspace's execution slot, and queues the Plan if
// another one holds it (FR-106).
//
// Queuing rather than failing is the product decision: an approved automatic
// Plan should wait its turn visibly, not require the user to notice a rejection
// and retry.
func (c *SlotCoordinator) Claim(ctx context.Context, workspaceID, planID string) (*ClaimResult, error) {
	now := c.now()

	// Reclaim a lease whose holder stopped heartbeating. The fencing
	// generation is what makes this safe: if the old holder is actually alive
	// and slow, its next write is refused rather than racing ours.
	if current, err := c.store.CurrentLease(ctx, workspaceID); err == nil && current != nil {
		if current.PlanID != planID && now.Sub(current.HeartbeatAt) > c.staleAfter {
			if err := c.store.Release(ctx, workspaceID, current.PlanID, current.Generation); err != nil {
				return nil, err
			}
		}
	}

	lease, err := c.store.Acquire(ctx, workspaceID, planID, c.owner, now)
	if err == nil {
		// Owning the slot means no longer waiting for it.
		if err := c.store.Dequeue(ctx, workspaceID, planID); err != nil {
			return nil, err
		}
		return &ClaimResult{Lease: lease}, nil
	}
	if !isExecutionConflict(err) {
		return nil, err
	}

	if err := c.store.Enqueue(ctx, workspaceID, planID, now); err != nil {
		return nil, err
	}
	entry, err := c.position(ctx, workspaceID, planID)
	if err != nil {
		return nil, err
	}
	result := &ClaimResult{Waiting: entry}
	if current, err := c.store.CurrentLease(ctx, workspaceID); err == nil && current != nil {
		result.HolderPlanID = current.PlanID
	}
	return result, nil
}

// Release gives up the slot and hands it to the next Plan in line.
//
// The next holder is returned so the caller can start it, rather than leaving
// the queue to be noticed by a later poll — a released slot with a waiting
// queue should not sit idle.
func (c *SlotCoordinator) Release(ctx context.Context, workspaceID, planID string, generation int64) (string, error) {
	if err := c.store.Release(ctx, workspaceID, planID, generation); err != nil {
		return "", err
	}
	if err := c.store.Dequeue(ctx, workspaceID, planID); err != nil {
		return "", err
	}

	queue, err := c.store.Queue(ctx, workspaceID)
	if err != nil {
		return "", err
	}
	if len(queue) == 0 {
		return "", nil
	}
	return queue[0].PlanID, nil
}

// Holder returns the Plan currently executing in this workspace, or empty when
// the slot is free.
func (c *SlotCoordinator) Holder(ctx context.Context, workspaceID string) (string, error) {
	lease, err := c.store.CurrentLease(ctx, workspaceID)
	if err != nil || lease == nil {
		return "", err
	}
	return lease.PlanID, nil
}

// WaitingForSlot reports whether a Plan is queued behind another. It is the
// SlotReporter the progress derivation uses (FR-107).
func (c *SlotCoordinator) WaitingForSlot(ctx context.Context, workspaceID, planID string) (bool, error) {
	entry, err := c.position(ctx, workspaceID, planID)
	if err != nil {
		return false, err
	}
	return entry != nil, nil
}

// Position returns a Plan's place in line, or nil when it is not waiting.
func (c *SlotCoordinator) Position(ctx context.Context, workspaceID, planID string) (*QueueEntry, error) {
	return c.position(ctx, workspaceID, planID)
}

func (c *SlotCoordinator) position(ctx context.Context, workspaceID, planID string) (*QueueEntry, error) {
	queue, err := c.store.Queue(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	for i, entry := range queue {
		if entry.PlanID == planID {
			entry.Position = i + 1
			return &entry, nil
		}
	}
	return nil, nil
}

// Heartbeat records that the holder is still working, so the lease is not
// reclaimed out from under it.
func (c *SlotCoordinator) Heartbeat(ctx context.Context, workspaceID, planID string, generation int64) error {
	return c.store.Heartbeat(ctx, workspaceID, planID, generation, c.now())
}

// Enqueue records a Plan as waiting without attempting to take the slot, which
// is what resuming does: a resumed Plan rejoins the queue rather than
// displacing the current holder (FR-110).
func (c *SlotCoordinator) Enqueue(ctx context.Context, workspaceID, planID string) (*QueueEntry, error) {
	if err := c.store.Enqueue(ctx, workspaceID, planID, c.now()); err != nil {
		return nil, err
	}
	return c.position(ctx, workspaceID, planID)
}

// Dequeue removes a Plan from the queue, for a Plan that was cancelled or
// completed while waiting.
func (c *SlotCoordinator) Dequeue(ctx context.Context, workspaceID, planID string) error {
	return c.store.Dequeue(ctx, workspaceID, planID)
}

func isExecutionConflict(err error) bool {
	return err != nil && CodeFor(err) == CodeExecutionConflict
}

// ErrStaleGeneration is returned when an operation presents a fencing token
// that is no longer current — the lease moved on while this worker was away.
// It is a distinct condition from "someone else holds the slot", because the
// caller may well have believed it held the slot itself.
var ErrStaleGeneration = fmt.Errorf("%w: the execution slot moved on", ErrExecutionConflict)
