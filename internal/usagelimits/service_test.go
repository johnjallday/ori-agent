package usagelimits

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestServiceCacheAndRefresh(t *testing.T) {
	now := time.Date(2026, 10, 13, 1, 0, 0, 0, time.UTC)
	calls := 0
	s := NewService()
	s.now = func() time.Time { return now }
	s.codex = func(context.Context) *Provider {
		calls++
		return &Provider{Provider: "codex", State: StateOK, Windows: []Window{{UsedPercent: 16}}}
	}
	first := s.Snapshot(context.Background(), false)
	first.Providers[0].Windows[0].UsedPercent = 999
	second := s.Snapshot(context.Background(), false)
	if calls != 1 || second.Providers[0].Windows[0].UsedPercent != 16 || !second.GeneratedAt.Equal(now) {
		t.Fatalf("cache: calls=%d, snapshot=%+v", calls, second)
	}
	now = now.Add(snapshotTTL)
	s.Snapshot(context.Background(), false)
	if calls != 2 {
		t.Fatalf("expired cache calls=%d", calls)
	}
	s.Snapshot(context.Background(), true)
	if calls != 3 {
		t.Fatalf("refresh calls=%d", calls)
	}
}

func TestServiceConcurrentRequestsShareRead(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	s := NewService()
	s.codex = func(context.Context) *Provider {
		calls.Add(1)
		close(started)
		<-release
		return nil
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			got := s.Snapshot(context.Background(), false)
			if got.Providers == nil || len(got.Providers) != 0 {
				t.Errorf("empty provider list = %+v", got)
			}
		})
	}
	<-started
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("reader called %d times", calls.Load())
	}
}

func TestServiceCanceledWaitDoesNotCancelSharedRead(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	s := NewService()
	s.codex = func(ctx context.Context) *Provider {
		close(started)
		<-release
		if ctx.Err() != nil {
			t.Errorf("shared read was canceled: %v", ctx.Err())
		}
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan Snapshot, 1)
	go func() { got <- s.Snapshot(ctx, false) }()
	<-started
	cancel()
	if p := (<-got).Providers[0]; p.State != StateUnavailable {
		t.Fatalf("got %+v", p)
	}
	close(release)
	if result := s.Snapshot(context.Background(), false); len(result.Providers) != 0 {
		t.Fatalf("got %+v", result)
	}
}
