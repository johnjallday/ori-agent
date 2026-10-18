package usagelimits

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/authdiscovery"
)

func TestLiveReaderSingleFlightAndRefreshFloor(t *testing.T) {
	for _, fail := range []bool{false, true} {
		now := time.Now()
		var calls atomic.Int32
		r := &liveReader{now: func() time.Time { return now }, fetch: func(context.Context) (Provider, error) {
			calls.Add(1)
			if fail {
				return Provider{}, errCodexLiveUnavailable
			}
			return Provider{FetchedAt: now}, nil
		}}
		var wg sync.WaitGroup
		for range 20 {
			wg.Go(func() { _, _ = r.read(context.Background()) })
		}
		wg.Wait()
		if calls.Load() != 1 {
			t.Fatalf("concurrent reads=%d", calls.Load())
		}
		now = now.Add(liveRefreshFloor - time.Nanosecond)
		_, _ = r.read(context.Background())
		if calls.Load() != 1 {
			t.Fatal("refresh floor was not enforced")
		}
		now = now.Add(time.Nanosecond)
		_, _ = r.read(context.Background())
		if calls.Load() != 2 {
			t.Fatal("floor boundary did not permit a read")
		}
	}
}

func TestCodexDoesNotRegressToOlderLogAfterLiveRefresh(t *testing.T) {
	for _, state := range []State{StateOK, StateNotSignedIn, StateNoLimits} {
		t.Run(string(state), func(t *testing.T) {
			now := time.Now()
			logTime := now.Add(-33 * time.Minute)
			liveTime := now
			fail := false
			calls := 0
			r := newCodexReader()
			r.now = func() time.Time { return now }
			r.lookPath = func(string) (string, error) { return "/fake/codex", nil }
			r.home = func() string { return "/fake" }
			r.login = func(context.Context, string) (authdiscovery.CodexLoginStatus, error) {
				return authdiscovery.CodexLoginStatus{SignedIn: true}, nil
			}
			r.passive = func(context.Context, string) (Provider, error) {
				return Provider{State: StateOK, Source: SourceCodexSessionLog, FetchedAt: logTime}, nil
			}
			r.live = &liveReader{now: r.now, fetch: func(context.Context) (Provider, error) {
				calls++
				if fail {
					return Provider{}, errCodexLiveUnavailable
				}
				return Provider{State: state, Source: SourceCodexAppServer, FetchedAt: liveTime}, nil
			}}
			r.readWithRefresh(context.Background(), true)
			now = now.Add(snapshotTTL + time.Second)
			got := r.readWithRefresh(context.Background(), false)
			if got.Source != SourceCodexAppServer || got.State != state || !got.FetchedAt.Equal(liveTime) {
				t.Fatalf("normal cache refill regressed: %+v", got)
			}
			if state == StateOK && calls != 1 {
				t.Fatal("normal refill should preserve the newer live reading without spawning again")
			}
			fail = true
			now = now.Add(liveRefreshFloor)
			got = r.readWithRefresh(context.Background(), true)
			if got.Source != SourceCodexAppServer || got.State != state || !got.FetchedAt.Equal(liveTime) {
				t.Fatalf("failed live refresh regressed: %+v", got)
			}
			if state == StateOK && got.Detail == "" {
				t.Fatal("fallback must explain the failed live refresh")
			}
			now = now.Add(snapshotTTL + time.Second)
			got = r.readWithRefresh(context.Background(), false)
			if got.State != state || got.Source != SourceCodexAppServer {
				t.Fatalf("failure erased the last provider result: %+v", got)
			}
			if state == StateOK {
				logTime = now
				got = r.readWithRefresh(context.Background(), false)
				if got.Source != SourceCodexSessionLog || !got.FetchedAt.Equal(logTime) {
					t.Fatalf("genuinely newer session log should win: %+v", got)
				}
			}
		})
	}
}

func TestCodexLivePolicyAndFallback(t *testing.T) {
	for _, tt := range []struct {
		name                   string
		refresh                bool
		age                    time.Duration
		noLog, fail, authError bool
		want                   Source
		state                  State
		calls                  int
	}{
		{name: "fresh passive", age: time.Minute, want: SourceCodexSessionLog, state: StateOK},
		{name: "stale passive", age: time.Hour + time.Second, want: SourceCodexAppServer, state: StateOK, calls: 1},
		{name: "refresh fresh log", refresh: true, age: time.Minute, want: SourceCodexAppServer, state: StateOK, calls: 1},
		{name: "failure uses log", refresh: true, fail: true, want: SourceCodexSessionLog, state: StateOK, calls: 1},
		{name: "missing log", noLog: true, want: SourceCodexAppServer, state: StateOK, calls: 1},
		{name: "neither source", noLog: true, fail: true, state: StateUnavailable, calls: 1},
		{name: "auth failure hides log", refresh: true, authError: true, state: StateNotSignedIn, calls: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			now := time.Now()
			r := newCodexReader()
			r.now = func() time.Time { return now }
			r.home = func() string { return "/fake" }
			r.lookPath = func(string) (string, error) { return "/fake/codex", nil }
			r.login = func(context.Context, string) (authdiscovery.CodexLoginStatus, error) {
				return authdiscovery.CodexLoginStatus{SignedIn: true}, nil
			}
			r.passive = func(context.Context, string) (Provider, error) {
				if tt.noLog {
					return Provider{}, errNoCodexLimits
				}
				return Provider{State: StateOK, Source: SourceCodexSessionLog, FetchedAt: now.Add(-tt.age)}, nil
			}
			calls := 0
			r.live = &liveReader{now: r.now, fetch: func(context.Context) (Provider, error) {
				calls++
				if tt.fail {
					return Provider{}, errors.New("failure")
				}
				if tt.authError {
					return Provider{State: StateNotSignedIn}, nil
				}
				return Provider{State: StateOK, Source: SourceCodexAppServer, FetchedAt: now}, nil
			}}
			p := r.readWithRefresh(context.Background(), tt.refresh)
			if p.Source != tt.want || p.State != tt.state || calls != tt.calls {
				t.Fatalf("got %+v, calls=%d", p, calls)
			}
		})
	}
}
