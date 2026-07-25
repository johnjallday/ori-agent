package connections

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRefreshCoordinator_SingleFlight(t *testing.T) {
	var rc RefreshCoordinator
	var calls int32
	entered := make(chan struct{})
	release := make(chan struct{})
	fn := func(ctx context.Context) (RefreshedToken, error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			close(entered) // signal the first (and only) execution has begun
		}
		<-release
		return RefreshedToken{AccessToken: "tok"}, nil
	}

	const n = 8
	var wg sync.WaitGroup
	results := make([]RefreshedToken, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, _ := rc.Do(context.Background(), "grant-1", fn)
			results[i] = r
		}(i)
	}

	<-entered
	// Let the remaining callers arrive at the coordinator and coalesce onto the
	// single in-flight execution before releasing it.
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("refresh executed %d times, want single-flight (1)", got)
	}
	for i, r := range results {
		if r.AccessToken != "tok" {
			t.Fatalf("caller %d got %+v, want shared result", i, r)
		}
	}
}

func TestRefreshCoordinator_DifferentKeys(t *testing.T) {
	var rc RefreshCoordinator
	var calls int32
	fn := func(ctx context.Context) (RefreshedToken, error) {
		atomic.AddInt32(&calls, 1)
		return RefreshedToken{}, nil
	}
	_, _ = rc.Do(context.Background(), "k1", fn)
	_, _ = rc.Do(context.Background(), "k2", fn)
	if calls != 2 {
		t.Fatalf("distinct keys should each run: calls=%d", calls)
	}
}

func TestRefreshCoordinator_Error(t *testing.T) {
	var rc RefreshCoordinator
	want := errors.New("exchange failed")
	_, err := rc.Do(context.Background(), "k", func(ctx context.Context) (RefreshedToken, error) {
		return RefreshedToken{}, want
	})
	if !errors.Is(err, want) {
		t.Fatalf("want propagated error, got %v", err)
	}
}
