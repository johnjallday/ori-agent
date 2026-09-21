package resetstate

import (
	"context"
	"errors"
	"sync"
)

var (
	ErrWorkActive    = errors.New("active work prevents reset; let it finish and review again")
	ErrWorkFenced    = errors.New("reset is pending; ordinary work is fenced until full process recovery")
	ErrWorkUntracked = errors.New("runtime work admission is not configured")
)

// WorkGate atomically arbitrates cooperating work against an irreversible reset
// fence. Its zero value is open. It tracks finite operations, lifetime owners,
// and revocable read-only streams, but DOES NOT implement a reset Lifecycle: an
// idle gate is not evidence that every writer or external child is owned. The
// host must establish coverage separately.
//
// Acquire a permit BEFORE touching owners or launching a goroutine, and retain
// it through final persistence and callbacks owned by that operation. Detached
// children need their own permit acquired before the parent releases its own.
// EnterStream returns a child of the supplied parent context, counts the stream
// separately from active operations and lifetime owners, and returns an
// idempotent, concurrency-safe release. Streams never block TryFence; every
// successful fence cancels their contexts after releasing the gate mutex. A
// refused fence changes no state and cancels nothing. On a nil gate, EnterStream
// preserves the parent context and returns a no-op release.
//
// Never copy a gate after use or replace one while old owners can still write.
// There is intentionally no Unfence method.
type WorkGate struct {
	mu            sync.Mutex
	active        int
	owners        int
	streams       int
	streamCancels map[*streamPermit]context.CancelFunc
	fenced        bool
}

type streamPermit byte

// WorkSnapshot counts finite instrumented operations separately from registered
// lifetime owners and revocable stream observers. Nested operations may hold
// separate permits. Unknown (an unwired gate) is different from idle.
type WorkSnapshot struct {
	Known   bool
	Active  int // admitted finite operations
	Owners  int // registered long-lived services/processes awaiting Drain
	Streams int // admitted read-only observers cancelled by a successful fence
	Fenced  bool
}

// Enter admits one operation. The returned release is concurrency-safe and
// idempotent. A nil gate preserves ordinary behavior for non-reset callers, but
// cannot be inspected or fenced as if it supplied reset readiness.
func (g *WorkGate) Enter() (release func(), err error) {
	return g.enter(false)
}

// EnterLifetime registers an idle-capable long-lived service or child process.
// Lifetime ownership blocks neither a non-cancelling fence nor finite work by
// itself: after TryFence rejects all new Enter calls, Lifecycle.Drain must stop
// these owners and require every returned release. An ambiguous stop keeps the
// owner visible and makes Drain fail rather than pretending the process exited.
func (g *WorkGate) EnterLifetime() (release func(), err error) {
	return g.enter(true)
}

// EnterStream admits a revocable read-only observer. The returned context is a
// child of parent, so either a client disconnect or a successful TryFence ends
// the stream. The release is concurrency-safe and idempotent. A nil gate returns
// parent unchanged with a no-op release.
func (g *WorkGate) EnterStream(parent context.Context) (ctx context.Context, release func(), err error) {
	if g == nil {
		return parent, func() {}, nil
	}

	g.mu.Lock()
	if g.fenced {
		g.mu.Unlock()
		return nil, nil, ErrWorkFenced
	}
	ctx, cancel := context.WithCancel(parent)
	permit := new(streamPermit)
	if g.streamCancels == nil {
		g.streamCancels = make(map[*streamPermit]context.CancelFunc)
	}
	g.streamCancels[permit] = cancel
	g.streams++
	g.mu.Unlock()

	var once sync.Once
	return ctx, func() {
		once.Do(func() {
			g.mu.Lock()
			delete(g.streamCancels, permit)
			g.streams--
			g.mu.Unlock()
			cancel()
		})
	}, nil
}

func (g *WorkGate) enter(lifetime bool) (release func(), err error) {
	if g == nil {
		return func() {}, nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.fenced {
		return nil, ErrWorkFenced
	}
	if lifetime {
		g.owners++
	} else {
		g.active++
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			defer g.mu.Unlock()
			if lifetime {
				g.owners--
			} else {
				g.active--
			}
		})
	}, nil
}

// TryFence succeeds only when finite admitted work is idle. Registered lifetime
// owners remain for Lifecycle.Drain; they are not active user work. A refusal
// changes no state and does not cancel any context. Every success permanently
// rejects new permits and cancels all admitted stream contexts after unlocking.
func (g *WorkGate) TryFence(ctx context.Context) error {
	if g == nil {
		return ErrWorkUntracked
	}
	g.mu.Lock()
	if err := ctx.Err(); err != nil {
		g.mu.Unlock()
		return err
	}
	if g.active != 0 {
		g.mu.Unlock()
		return ErrWorkActive
	}
	g.fenced = true
	streamCancels := make([]context.CancelFunc, 0, len(g.streamCancels))
	for _, cancel := range g.streamCancels {
		streamCancels = append(streamCancels, cancel)
	}
	g.mu.Unlock()

	for _, cancel := range streamCancels {
		cancel()
	}
	return nil
}

// blockNewWork is the lease's uncertainty path, not normal reset admission.
// Existing operations and streams retain their permits and may finish; no
// context is cancelled. TryFence still refuses while finite operations are
// active, and a later successful TryFence cancels streams even though the gate
// was already fenced.
func (g *WorkGate) blockNewWork() {
	g.mu.Lock()
	g.fenced = true
	g.mu.Unlock()
}

func (g *WorkGate) Snapshot() WorkSnapshot {
	if g == nil {
		return WorkSnapshot{}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return WorkSnapshot{
		Known:   true,
		Active:  g.active,
		Owners:  g.owners,
		Streams: g.streams,
		Fenced:  g.fenced,
	}
}
