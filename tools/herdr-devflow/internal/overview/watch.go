package overview

import (
	"context"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/github"
)

// remoteClock rate-limits the one network call in the snapshot.
//
// Local evidence is cheap and can be re-read every couple of seconds; a GitHub
// query is neither. The clock lets fast local polling continue while the
// remote result is refreshed no more often than its interval, and it keeps the
// last successful result so a transient failure degrades to "stale" rather
// than blanking every remote column on the board.
type remoteClock struct {
	collector RemoteCollector
	interval  time.Duration

	mu         sync.Mutex
	last       github.Result
	lastAt     time.Time
	haveResult bool
}

func newRemoteClock(collector RemoteCollector, interval time.Duration) *remoteClock {
	if interval < MinRemoteRefreshInterval {
		interval = MinRemoteRefreshInterval
	}
	return &remoteClock{collector: collector, interval: interval}
}

// MinRemoteRefreshInterval is the shortest gap this package will ever allow
// between remote queries, regardless of configuration.
const MinRemoteRefreshInterval = 30 * time.Second

// remoteOutcome is one clock tick's answer.
type remoteOutcome struct {
	result github.Result
	// stale is true when a previous result is being reused because this tick
	// either skipped the query or the query failed.
	stale bool
	// queried is true when a network call actually happened this tick.
	queried bool
	// err is set only when there is no usable result at all.
	err error
}

// get returns remote facts for one collection, querying at most once per
// interval. A failure after at least one success reuses the last good result
// and marks it stale; a failure with nothing cached returns the error.
func (c *remoteClock) get(ctx context.Context, base string, now time.Time, force bool) remoteOutcome {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !force && c.haveResult && now.Sub(c.lastAt) < c.interval {
		return remoteOutcome{result: c.last, stale: true}
	}

	result, err := c.collector.ListPullRequests(ctx, base)
	if err != nil {
		if c.haveResult {
			// Keep showing the last known delivery state, clearly labelled,
			// rather than dropping every PR column on one flaky query.
			return remoteOutcome{result: c.last, stale: true, queried: true, err: err}
		}
		return remoteOutcome{queried: true, err: err}
	}

	c.last = result
	c.lastAt = now
	c.haveResult = true
	return remoteOutcome{result: result, queried: true}
}

// Watch emits a snapshot on every local tick and refreshes remote facts on the
// slower remote clock.
//
// The first complete board requires a successful remote query: until one
// arrives the emitted snapshot is explicitly incomplete, because a board that
// silently omits delivery state is worse than one that admits it does not know.
func (s *Service) Watch(ctx context.Context, interval time.Duration, emit func(Snapshot)) error {
	if emit == nil {
		return nil
	}
	if interval <= 0 {
		interval = 2 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		snapshot, err := s.collectRateLimited(ctx)
		if err != nil {
			return err
		}
		emit(snapshot)

		select {
		case <-ctx.Done():
			// A cancelled watch is a normal exit, not a failure: the caller
			// pressed Ctrl-C or the plugin closed the board.
			return nil
		case <-ticker.C:
		}
	}
}
