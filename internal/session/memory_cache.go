package session

import (
	"container/list"
	"sync"
	"sync/atomic"
)

// MemoryCache is an LRU cache for sessions.
// It provides O(1) access and automatic eviction of least recently used sessions.
type MemoryCache struct {
	mu       sync.RWMutex
	capacity int
	items    map[string]*list.Element
	order    *list.List

	// Statistics
	hits      int64
	misses    int64
	evictions int64
}

// cacheEntry wraps a session with its key for the LRU list.
type cacheEntry struct {
	key     string
	session *Session
}

// NewMemoryCache creates a new LRU cache with the specified capacity.
func NewMemoryCache(capacity int) *MemoryCache {
	if capacity <= 0 {
		capacity = 50 // Default capacity
	}
	return &MemoryCache{
		capacity: capacity,
		items:    make(map[string]*list.Element),
		order:    list.New(),
	}
}

// Get retrieves a session from the cache.
// Returns nil if the session is not cached.
// Accessing a session moves it to the front (most recently used).
func (c *MemoryCache) Get(id string) *Session {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.items[id]
	if !ok {
		atomic.AddInt64(&c.misses, 1)
		return nil
	}

	atomic.AddInt64(&c.hits, 1)

	// Move to front (most recently used)
	c.order.MoveToFront(elem)

	entry := elem.Value.(*cacheEntry)
	return entry.session
}

// Put adds or updates a session in the cache.
// Returns the evicted session if the cache was at capacity, or nil.
func (c *MemoryCache) Put(id string, session *Session) *Session {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if already exists
	if elem, ok := c.items[id]; ok {
		// Update existing entry
		c.order.MoveToFront(elem)
		entry := elem.Value.(*cacheEntry)
		entry.session = session
		return nil
	}

	var evicted *Session

	// Check if we need to evict
	if c.order.Len() >= c.capacity {
		evicted = c.evictOldest()
	}

	// Add new entry
	entry := &cacheEntry{
		key:     id,
		session: session,
	}
	elem := c.order.PushFront(entry)
	c.items[id] = elem

	return evicted
}

// Remove removes a session from the cache.
// Returns true if the session was in the cache.
func (c *MemoryCache) Remove(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.items[id]
	if !ok {
		return false
	}

	c.order.Remove(elem)
	delete(c.items, id)
	return true
}

// Touch moves a session to the front of the LRU list without retrieving it.
// Returns true if the session was in the cache.
func (c *MemoryCache) Touch(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.items[id]
	if !ok {
		return false
	}

	c.order.MoveToFront(elem)
	return true
}

// Contains checks if a session is in the cache without updating LRU order.
func (c *MemoryCache) Contains(id string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	_, ok := c.items[id]
	return ok
}

// Len returns the current number of sessions in the cache.
func (c *MemoryCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.order.Len()
}

// Capacity returns the maximum capacity of the cache.
func (c *MemoryCache) Capacity() int {
	return c.capacity
}

// GetAll returns all cached sessions.
// Sessions are returned in LRU order (most recently used first).
func (c *MemoryCache) GetAll() []*Session {
	c.mu.RLock()
	defer c.mu.RUnlock()

	sessions := make([]*Session, 0, c.order.Len())
	for elem := c.order.Front(); elem != nil; elem = elem.Next() {
		entry := elem.Value.(*cacheEntry)
		sessions = append(sessions, entry.session)
	}
	return sessions
}

// GetIDs returns all cached session IDs.
// IDs are returned in LRU order (most recently used first).
func (c *MemoryCache) GetIDs() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	ids := make([]string, 0, c.order.Len())
	for elem := c.order.Front(); elem != nil; elem = elem.Next() {
		entry := elem.Value.(*cacheEntry)
		ids = append(ids, entry.key)
	}
	return ids
}

// Stats returns cache statistics.
func (c *MemoryCache) Stats() CacheStats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return CacheStats{
		Size:      c.order.Len(),
		MaxSize:   c.capacity,
		Hits:      atomic.LoadInt64(&c.hits),
		Misses:    atomic.LoadInt64(&c.misses),
		Evictions: atomic.LoadInt64(&c.evictions),
	}
}

// Clear removes all sessions from the cache.
func (c *MemoryCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.items = make(map[string]*list.Element)
	c.order = list.New()
}

// evictOldest removes the least recently used session.
// Must be called with the lock held.
func (c *MemoryCache) evictOldest() *Session {
	elem := c.order.Back()
	if elem == nil {
		return nil
	}

	c.order.Remove(elem)
	entry := elem.Value.(*cacheEntry)
	delete(c.items, entry.key)

	atomic.AddInt64(&c.evictions, 1)

	return entry.session
}

// EvictOldest removes and returns the least recently used session.
// This is useful for manually managing eviction to persist data.
func (c *MemoryCache) EvictOldest() *Session {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.evictOldest()
}

// GetOldest returns the least recently used session without removing it.
func (c *MemoryCache) GetOldest() *Session {
	c.mu.RLock()
	defer c.mu.RUnlock()

	elem := c.order.Back()
	if elem == nil {
		return nil
	}

	entry := elem.Value.(*cacheEntry)
	return entry.session
}
