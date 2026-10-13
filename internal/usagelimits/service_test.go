package usagelimits

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestServiceReadsProvidersConcurrently(t *testing.T) {
	s := NewService()
	codexStarted, claudeStarted := make(chan struct{}), make(chan struct{})
	s.codex = func(ctx context.Context, _ bool) *Provider {
		close(codexStarted)
		select {
		case <-claudeStarted:
		case <-ctx.Done():
			t.Error("providers ran sequentially")
		}
		p := codexProvider()
		p.State = StateOK
		return &p
	}
	s.claude = func(ctx context.Context, _ bool) *Provider {
		close(claudeStarted)
		select {
		case <-codexStarted:
		case <-ctx.Done():
			t.Error("providers ran sequentially")
		}
		p := claudeProvider()
		p.State = StateOK
		return &p
	}
	got := s.Snapshot(context.Background(), true)
	if len(got.Providers) != 2 || got.Providers[0].Provider != "codex" || got.Providers[1].Provider != "claude_code" {
		t.Fatalf("got %+v", got)
	}
}

func TestServiceCacheAndRefresh(t *testing.T) {
	now := time.Date(2026, 10, 13, 1, 0, 0, 0, time.UTC)
	calls := 0
	s := NewService()
	s.claude = nil
	s.now = func() time.Time { return now }
	s.codex = func(context.Context, bool) *Provider {
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
	s.claude = nil
	s.codex = func(context.Context, bool) *Provider {
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

func TestServiceRefreshDuringPassiveReadStillRequestsLive(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	s := NewService()
	s.claude = nil
	s.codex = func(_ context.Context, refresh bool) *Provider {
		if !refresh {
			close(started)
			<-release
			return &Provider{Source: SourceCodexSessionLog}
		}
		return &Provider{Source: SourceCodexAppServer}
	}
	first := make(chan Snapshot, 1)
	go func() { first <- s.Snapshot(context.Background(), false) }()
	<-started
	live := make(chan Snapshot, 1)
	go func() { live <- s.Snapshot(context.Background(), true) }()
	close(release)
	<-first
	if result := <-live; result.Providers[0].Source != SourceCodexAppServer {
		t.Fatalf("refresh shared passive-only result: %+v", result)
	}
}

func TestServiceCanceledWaitDoesNotCancelSharedRead(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	s := NewService()
	s.claude = nil
	s.codex = func(ctx context.Context, _ bool) *Provider {
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
