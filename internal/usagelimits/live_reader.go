package usagelimits

import (
	"context"
	"sync"
	"time"
)

const liveRefreshFloor = 10 * time.Second

// liveReader shares in-flight work and successful or failed attempts per provider.
// Caching errors too prevents a down service from spawning a process per click.
type liveReader struct {
	mu     sync.Mutex
	fetch  func(context.Context) (Provider, error)
	now    func() time.Time
	lastAt time.Time
	last   Provider
	err    error
	flight chan struct{}
}

func (r *liveReader) read(ctx context.Context) (Provider, error) {
	r.mu.Lock()
	if !r.lastAt.IsZero() && r.now().Sub(r.lastAt) < liveRefreshFloor {
		p, err := r.last, r.err
		r.mu.Unlock()
		return p, err
	}
	if r.flight == nil {
		r.flight = make(chan struct{})
		go r.run(context.WithoutCancel(ctx), r.flight)
	}
	done := r.flight
	r.mu.Unlock()
	select {
	case <-done:
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.last, r.err
	case <-ctx.Done():
		return Provider{}, ctx.Err()
	}
}

func (r *liveReader) run(ctx context.Context, done chan struct{}) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	p, err := r.fetch(ctx)
	r.mu.Lock()
	r.last, r.err, r.lastAt = p, err, r.now()
	r.flight = nil
	close(done)
	r.mu.Unlock()
}
