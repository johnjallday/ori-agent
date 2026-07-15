package mailbox

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Cache bounds (contract §6): short TTL so the brief and agent tools reuse a
// read within one generation without staleness, capped entry count so a busy
// mailbox can never grow the cache unboundedly.
const (
	DefaultCacheTTL      = 90 * time.Second
	DefaultCacheMaxItems = 256
)

// CachingProvider wraps a MailboxProvider with a bounded, TTL'd, account-isolated
// read cache. Only successful reads are cached; errors are never cached (a
// transient failure must not stick). Entries are keyed with the account ID so
// one account can never read another's cached data, and InvalidateAccount drops
// an account's entries on disconnect (contract §6 retention/deletion).
type CachingProvider struct {
	inner    MailboxProvider
	ttl      time.Duration
	maxItems int
	now      func() time.Time

	mu      sync.Mutex
	entries map[string]cacheEntry
}

type cacheEntry struct {
	value     any
	expiresAt time.Time
}

// NewCachingProvider wraps inner with default bounds. A nil inner yields a nil
// wrapper so callers can wrap unconditionally.
func NewCachingProvider(inner MailboxProvider) *CachingProvider {
	if inner == nil {
		return nil
	}
	return &CachingProvider{
		inner:    inner,
		ttl:      DefaultCacheTTL,
		maxItems: DefaultCacheMaxItems,
		now:      time.Now,
		entries:  make(map[string]cacheEntry),
	}
}

var _ MailboxProvider = (*CachingProvider)(nil)

func (c *CachingProvider) SearchThreads(ctx context.Context, account Account, q Query) (ThreadPage, error) {
	q = q.Normalized()
	key := "search|" + account.ID + "|" + searchKey(q)
	if v, ok := c.get(key); ok {
		return v.(ThreadPage), nil
	}
	page, err := c.inner.SearchThreads(ctx, account, q)
	if err != nil {
		return ThreadPage{}, err
	}
	c.put(key, page)
	return page, nil
}

func (c *CachingProvider) GetThread(ctx context.Context, account Account, threadID string) (Thread, error) {
	key := "thread|" + account.ID + "|" + threadID
	if v, ok := c.get(key); ok {
		return v.(Thread), nil
	}
	thread, err := c.inner.GetThread(ctx, account, threadID)
	if err != nil {
		return Thread{}, err
	}
	c.put(key, thread)
	return thread, nil
}

// InvalidateAccount drops all cached entries for an account, e.g. when the user
// disconnects it (contract §6). Safe to call for an unknown account.
func (c *CachingProvider) InvalidateAccount(accountID string) {
	if c == nil {
		return
	}
	prefix := "|" + accountID + "|"
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.entries {
		if strings.Contains(k, prefix) {
			delete(c.entries, k)
		}
	}
}

func (c *CachingProvider) get(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	if !c.now().Before(e.expiresAt) {
		delete(c.entries, key)
		return nil, false
	}
	return e.value, true
}

func (c *CachingProvider) put(key string, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Bound the cache: when full, drop expired entries first, then evict
	// arbitrary entries until under the cap. A read cache with a short TTL does
	// not need LRU precision — staying bounded is what matters.
	if len(c.entries) >= c.maxItems {
		c.evictLocked()
	}
	c.entries[key] = cacheEntry{value: value, expiresAt: c.now().Add(c.ttl)}
}

func (c *CachingProvider) evictLocked() {
	now := c.now()
	for k, e := range c.entries {
		if !now.Before(e.expiresAt) {
			delete(c.entries, k)
		}
	}
	// Still over cap after removing expired entries: drop arbitrary keys.
	for k := range c.entries {
		if len(c.entries) < c.maxItems {
			break
		}
		delete(c.entries, k)
	}
}

// searchKey renders a stable cache key for a normalized query.
func searchKey(q Query) string {
	var b strings.Builder
	b.WriteString(strconv.Itoa(q.MaxResults))
	b.WriteByte('|')
	b.WriteString(strconv.Itoa(q.LookbackDays))
	b.WriteByte('|')
	b.WriteString(strconv.FormatBool(q.WaitingOnUserOnly))
	b.WriteByte('|')
	b.WriteString(q.PageToken)
	b.WriteByte('|')
	b.WriteString(strings.Join(q.Labels, ","))
	return b.String()
}
