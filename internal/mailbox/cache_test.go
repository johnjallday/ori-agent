package mailbox

import (
	"context"
	"errors"
	"testing"
	"time"
)

// countingProvider counts calls so cache hits/misses are observable.
type countingProvider struct {
	searches int
	gets     int
	err      error
	page     ThreadPage
}

func (c *countingProvider) SearchThreads(ctx context.Context, a Account, q Query) (ThreadPage, error) {
	c.searches++
	if c.err != nil {
		return ThreadPage{}, c.err
	}
	return c.page, nil
}
func (c *countingProvider) GetThread(ctx context.Context, a Account, id string) (Thread, error) {
	c.gets++
	if c.err != nil {
		return Thread{}, c.err
	}
	return Thread{ID: id}, nil
}

func newTestCache(inner MailboxProvider, now func() time.Time) *CachingProvider {
	c := NewCachingProvider(inner)
	c.now = now
	return c
}

func TestCacheServesRepeatReadsWithinTTL(t *testing.T) {
	inner := &countingProvider{page: ThreadPage{Threads: []Thread{{ID: "t1"}}}}
	c := newTestCache(inner, func() time.Time { return time.Unix(0, 0) })
	acc := Account{ID: "a1"}

	for range 3 {
		if _, err := c.SearchThreads(context.Background(), acc, Query{}); err != nil {
			t.Fatalf("search: %v", err)
		}
	}
	if inner.searches != 1 {
		t.Fatalf("expected 1 underlying search (cached), got %d", inner.searches)
	}
}

func TestCacheExpiresAfterTTL(t *testing.T) {
	inner := &countingProvider{}
	now := time.Unix(0, 0)
	c := newTestCache(inner, func() time.Time { return now })
	acc := Account{ID: "a1"}

	_, _ = c.GetThread(context.Background(), acc, "t1")
	now = now.Add(DefaultCacheTTL + time.Second)
	_, _ = c.GetThread(context.Background(), acc, "t1")
	if inner.gets != 2 {
		t.Fatalf("expected re-fetch after TTL, got %d gets", inner.gets)
	}
}

func TestCacheIsolatesAccounts(t *testing.T) {
	inner := &countingProvider{}
	c := newTestCache(inner, func() time.Time { return time.Unix(0, 0) })
	_, _ = c.GetThread(context.Background(), Account{ID: "a1"}, "t1")
	_, _ = c.GetThread(context.Background(), Account{ID: "a2"}, "t1")
	// Same thread ID, different accounts → two distinct fetches (no cross-account leak).
	if inner.gets != 2 {
		t.Fatalf("accounts must not share cache entries, got %d gets", inner.gets)
	}
}

func TestCacheNeverCachesErrors(t *testing.T) {
	inner := &countingProvider{err: ErrRateLimited}
	c := newTestCache(inner, func() time.Time { return time.Unix(0, 0) })
	acc := Account{ID: "a1"}
	for range 2 {
		if _, err := c.SearchThreads(context.Background(), acc, Query{}); !errors.Is(err, ErrRateLimited) {
			t.Fatalf("expected error passthrough, got %v", err)
		}
	}
	if inner.searches != 2 {
		t.Fatalf("errors must not be cached; expected 2 calls, got %d", inner.searches)
	}
}

func TestCacheInvalidateAccountDropsEntries(t *testing.T) {
	inner := &countingProvider{}
	c := newTestCache(inner, func() time.Time { return time.Unix(0, 0) })
	acc := Account{ID: "a1"}
	_, _ = c.GetThread(context.Background(), acc, "t1")
	c.InvalidateAccount("a1")
	_, _ = c.GetThread(context.Background(), acc, "t1")
	if inner.gets != 2 {
		t.Fatalf("invalidated account must re-fetch, got %d gets", inner.gets)
	}
}
