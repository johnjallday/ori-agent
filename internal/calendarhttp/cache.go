package calendarhttp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"
)

// readCacheTTL bounds how long an identical read request's result is reused.
// Short enough that a real change on the connector is visible within one
// interaction, long enough to collapse the handful of duplicate fetches a
// single agenda render (day view + week view + drawer open) tends to issue.
// FR34 requires this stay in-memory and short-lived -- no persistent event
// cache.
const readCacheTTL = 15 * time.Second

// readCacheKey scopes a cached read to exactly the request that produced it:
// a different user, workspace, binding, operation, or argument set is always
// a cache miss. Binding (not just workspace) is included because a workspace
// could in principle be repointed at a different connector binding without
// changing id.
type readCacheKey struct {
	UserID      string
	WorkspaceID string
	BindingID   string
	Operation   string
	ArgsHash    string
}

type readCacheEntry struct {
	value  any
	expiry time.Time
}

// readCache is a short-TTL in-memory cache for gateway read results. It never
// stores errors (a failed call is never cached, so the next request retries
// against the connector) and holds no persistent rows -- entries live only in
// process memory and expire on their own even if never invalidated.
type readCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[readCacheKey]readCacheEntry
}

func newReadCache(ttl time.Duration) *readCache {
	return &readCache{ttl: ttl, entries: make(map[readCacheKey]readCacheEntry)}
}

func (c *readCache) get(key readCacheKey) (any, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	if time.Now().After(entry.expiry) {
		delete(c.entries, key)
		return nil, false
	}
	return entry.value, true
}

// set stores value for key. Callers must never call this with an error
// result -- only a fully decoded, sanitized success value belongs in the
// cache.
func (c *readCache) set(key readCacheKey, value any) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = readCacheEntry{value: value, expiry: time.Now().Add(c.ttl)}
}

// invalidateBinding drops every cached read scoped to bindingID, regardless
// of which user/operation/args produced it. Called after a successful
// mutation confirm so the next agenda read reflects the write rather than a
// stale cached range (FR34).
func (c *readCache) invalidateBinding(bindingID string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for key := range c.entries {
		if key.BindingID == bindingID {
			delete(c.entries, key)
		}
	}
}

// readCacheArgsHash deterministically hashes a read operation's arguments so
// they can be used as a fixed-size map key. json.Marshal on map[string]any
// sorts object keys alphabetically, so this is stable across calls with the
// same logical arguments regardless of construction order.
func readCacheArgsHash(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	b, err := json.Marshal(args)
	if err != nil {
		// Unmarshalable args (shouldn't happen -- these are always
		// JSON-safe scalars/slices/maps built by BuildArguments) must never
		// silently collide into the same cache key; fall back to a
		// deterministic-but-argument-blind key so at worst caching is
		// skipped-by-collision-avoidance, never wrongly shared.
		keys := make([]string, 0, len(args))
		for k := range args {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return strings.Join(keys, ",")
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
