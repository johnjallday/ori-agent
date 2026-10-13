package usagelimits

import (
	"context"
	"sync"
	"time"
)

const (
	snapshotTTL     = 60 * time.Second
	snapshotTimeout = 4900 * time.Millisecond
	readerTimeout   = 4500 * time.Millisecond
)

// Service caches readings on demand. It never polls in the background.
type Service struct {
	mu              sync.Mutex
	cached          Snapshot
	inFlight        chan struct{}
	inFlightRefresh bool
	now             func() time.Time
	codex           func(context.Context, bool) *Provider
	claude          func(context.Context, bool) *Provider
}

func NewService() *Service {
	return &Service{now: time.Now, codex: newCodexReader().readWithRefresh, claude: newClaudeReader().read}
}

// Snapshot shares a cache refill across callers. A canceled request stops waiting
// without canceling work another caller needs; the shared read has its own bound.
func (s *Service) Snapshot(ctx context.Context, refresh bool) Snapshot {
	ctx, cancel := context.WithTimeout(ctx, snapshotTimeout)
	defer cancel()
	s.mu.Lock()
	if !refresh && !s.cached.GeneratedAt.IsZero() && s.now().Sub(s.cached.GeneratedAt) < snapshotTTL {
		result := cloneSnapshot(s.cached)
		s.mu.Unlock()
		return result
	}
	if s.inFlight == nil {
		s.inFlight = make(chan struct{})
		s.inFlightRefresh = refresh
		go s.refill(context.WithoutCancel(ctx), s.inFlight, refresh)
	}
	done := s.inFlight
	liveRequested := s.inFlightRefresh
	s.mu.Unlock()
	select {
	case <-done:
		if refresh && !liveRequested {
			// A manual refresh arriving during a passive refill still needs a
			// live attempt. The recursive wait inherits this request's deadline.
			return s.Snapshot(ctx, true)
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		return cloneSnapshot(s.cached)
	case <-ctx.Done():
		p := codexProvider()
		p.State, p.Detail = StateUnavailable, "Codex usage limits could not be read in time"
		return Snapshot{GeneratedAt: s.now().UTC(), Providers: []Provider{p}}
	}
}

func (s *Service) refill(ctx context.Context, done chan struct{}, refresh bool) {
	// Leave the caller time to receive the local fallback after a live timeout.
	ctx, cancel := context.WithTimeout(ctx, readerTimeout)
	defer cancel()
	result := Snapshot{Providers: []Provider{}}
	// Run providers concurrently: two four-second live reads must not add up
	// to an eight-second endpoint. Buffered channels also tolerate canceled waits.
	codex, claude := make(chan *Provider, 1), make(chan *Provider, 1)
	go func() { codex <- s.codex(ctx, refresh) }()
	go func() {
		if s.claude == nil {
			claude <- nil
			return
		}
		claude <- s.claude(ctx, refresh)
	}()
	for _, ch := range []chan *Provider{codex, claude} {
		if p := <-ch; p != nil {
			result.Providers = append(result.Providers, *p)
		}
	}
	result.GeneratedAt = s.now().UTC()
	s.mu.Lock()
	s.cached = result
	s.inFlight = nil
	close(done)
	s.mu.Unlock()
}

func cloneSnapshot(snapshot Snapshot) Snapshot {
	result := snapshot
	result.Providers = make([]Provider, len(snapshot.Providers))
	for i, p := range snapshot.Providers {
		result.Providers[i] = p
		result.Providers[i].Windows = append([]Window{}, p.Windows...)
		if p.Credits != nil {
			credits := *p.Credits
			result.Providers[i].Credits = &credits
		}
		if p.Mirror != nil {
			mirror := *p.Mirror
			result.Providers[i].Mirror = &mirror
		}
	}
	return result
}
