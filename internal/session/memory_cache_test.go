package session

import (
	"testing"
	"time"
)

func TestMemoryCache_BasicOperations(t *testing.T) {
	cache := NewMemoryCache(3)

	// Test Put and Get
	session := &Session{ID: "s1", Title: "Session 1"}
	evicted := cache.Put("s1", session)

	if evicted != nil {
		t.Error("Expected no eviction on first put")
	}

	got := cache.Get("s1")
	if got == nil {
		t.Fatal("Expected to get session s1")
		return
	}
	if got.Title != "Session 1" {
		t.Errorf("Expected title 'Session 1', got %s", got.Title)
	}

	// Test Contains
	if !cache.Contains("s1") {
		t.Error("Expected cache to contain s1")
	}
	if cache.Contains("nonexistent") {
		t.Error("Expected cache to not contain nonexistent")
	}

	// Test Get for nonexistent
	if cache.Get("nonexistent") != nil {
		t.Error("Expected nil for nonexistent session")
	}
}

func TestMemoryCache_LRUEviction(t *testing.T) {
	cache := NewMemoryCache(3)

	// Fill cache
	cache.Put("s1", &Session{ID: "s1", Title: "Session 1"})
	cache.Put("s2", &Session{ID: "s2", Title: "Session 2"})
	cache.Put("s3", &Session{ID: "s3", Title: "Session 3"})

	if cache.Len() != 3 {
		t.Errorf("Expected cache length 3, got %d", cache.Len())
	}

	// Access s1 to make it recently used
	cache.Get("s1")

	// Add s4 - should evict s2 (least recently used)
	evicted := cache.Put("s4", &Session{ID: "s4", Title: "Session 4"})

	if evicted == nil {
		t.Fatal("Expected eviction")
		return
	}
	if evicted.ID != "s2" {
		t.Errorf("Expected s2 to be evicted, got %s", evicted.ID)
	}

	// Verify s2 is no longer in cache
	if cache.Contains("s2") {
		t.Error("Expected s2 to be evicted")
	}

	// Verify s1, s3, s4 are still there
	if !cache.Contains("s1") || !cache.Contains("s3") || !cache.Contains("s4") {
		t.Error("Expected s1, s3, s4 to still be in cache")
	}
}

func TestMemoryCache_UpdateExisting(t *testing.T) {
	cache := NewMemoryCache(3)

	cache.Put("s1", &Session{ID: "s1", Title: "Original"})

	// Update s1
	evicted := cache.Put("s1", &Session{ID: "s1", Title: "Updated"})

	if evicted != nil {
		t.Error("Expected no eviction when updating existing")
	}

	got := cache.Get("s1")
	if got.Title != "Updated" {
		t.Errorf("Expected title 'Updated', got %s", got.Title)
	}

	// Should still be just 1 item
	if cache.Len() != 1 {
		t.Errorf("Expected cache length 1, got %d", cache.Len())
	}
}

func TestMemoryCache_Remove(t *testing.T) {
	cache := NewMemoryCache(3)

	cache.Put("s1", &Session{ID: "s1"})
	cache.Put("s2", &Session{ID: "s2"})

	// Remove existing
	removed := cache.Remove("s1")
	if !removed {
		t.Error("Expected Remove to return true for existing session")
	}

	if cache.Contains("s1") {
		t.Error("Expected s1 to be removed")
	}

	// Remove nonexistent
	removed = cache.Remove("nonexistent")
	if removed {
		t.Error("Expected Remove to return false for nonexistent session")
	}
}

func TestMemoryCache_Touch(t *testing.T) {
	cache := NewMemoryCache(3)

	cache.Put("s1", &Session{ID: "s1"})
	cache.Put("s2", &Session{ID: "s2"})
	cache.Put("s3", &Session{ID: "s3"})

	// Touch s1 to make it most recently used
	touched := cache.Touch("s1")
	if !touched {
		t.Error("Expected Touch to return true")
	}

	// Add s4 - should evict s2 (least recently used now)
	evicted := cache.Put("s4", &Session{ID: "s4"})
	if evicted.ID != "s2" {
		t.Errorf("Expected s2 to be evicted after touching s1, got %s", evicted.ID)
	}

	// Touch nonexistent
	touched = cache.Touch("nonexistent")
	if touched {
		t.Error("Expected Touch to return false for nonexistent")
	}
}

func TestMemoryCache_GetAll(t *testing.T) {
	cache := NewMemoryCache(3)

	cache.Put("s1", &Session{ID: "s1"})
	cache.Put("s2", &Session{ID: "s2"})
	cache.Put("s3", &Session{ID: "s3"})

	// Access s1 to change order
	cache.Get("s1")

	sessions := cache.GetAll()
	if len(sessions) != 3 {
		t.Fatalf("Expected 3 sessions, got %d", len(sessions))
	}

	// Should be in LRU order: s1 (most recent), s3, s2 (least recent)
	if sessions[0].ID != "s1" {
		t.Errorf("Expected first session to be s1, got %s", sessions[0].ID)
	}
}

func TestMemoryCache_Stats(t *testing.T) {
	cache := NewMemoryCache(3)

	cache.Put("s1", &Session{ID: "s1"})
	cache.Put("s2", &Session{ID: "s2"})

	// Hits
	cache.Get("s1")
	cache.Get("s1")

	// Misses
	cache.Get("nonexistent")

	// Eviction
	cache.Put("s3", &Session{ID: "s3"})
	cache.Put("s4", &Session{ID: "s4"}) // Evicts s2

	stats := cache.Stats()

	if stats.Size != 3 {
		t.Errorf("Expected size 3, got %d", stats.Size)
	}
	if stats.MaxSize != 3 {
		t.Errorf("Expected max size 3, got %d", stats.MaxSize)
	}
	if stats.Hits != 2 {
		t.Errorf("Expected 2 hits, got %d", stats.Hits)
	}
	if stats.Misses != 1 {
		t.Errorf("Expected 1 miss, got %d", stats.Misses)
	}
	if stats.Evictions != 1 {
		t.Errorf("Expected 1 eviction, got %d", stats.Evictions)
	}
}

func TestMemoryCache_Clear(t *testing.T) {
	cache := NewMemoryCache(3)

	cache.Put("s1", &Session{ID: "s1"})
	cache.Put("s2", &Session{ID: "s2"})

	cache.Clear()

	if cache.Len() != 0 {
		t.Errorf("Expected empty cache after clear, got %d", cache.Len())
	}
	if cache.Contains("s1") {
		t.Error("Expected s1 to be cleared")
	}
}

func TestMemoryCache_Concurrency(t *testing.T) {
	cache := NewMemoryCache(100)

	done := make(chan bool)

	// Writer goroutine
	go func() {
		for i := 0; i < 1000; i++ {
			session := &Session{
				ID:        "s" + string(rune(i%100)),
				Title:     "Session",
				UpdatedAt: time.Now(),
			}
			cache.Put(session.ID, session)
		}
		done <- true
	}()

	// Reader goroutine
	go func() {
		for i := 0; i < 1000; i++ {
			cache.Get("s" + string(rune(i%100)))
		}
		done <- true
	}()

	// Touch goroutine
	go func() {
		for i := 0; i < 1000; i++ {
			cache.Touch("s" + string(rune(i%100)))
		}
		done <- true
	}()

	// Wait for all goroutines
	<-done
	<-done
	<-done

	// Should not panic or deadlock
	stats := cache.Stats()
	if stats.Size < 0 || stats.Size > 100 {
		t.Errorf("Unexpected cache size: %d", stats.Size)
	}
}

func TestMemoryCache_EvictOldest(t *testing.T) {
	cache := NewMemoryCache(3)

	cache.Put("s1", &Session{ID: "s1"})
	cache.Put("s2", &Session{ID: "s2"})
	cache.Put("s3", &Session{ID: "s3"})

	// Manually evict oldest
	evicted := cache.EvictOldest()
	if evicted == nil {
		t.Fatal("Expected to evict oldest")
		return
	}
	if evicted.ID != "s1" {
		t.Errorf("Expected s1 to be evicted, got %s", evicted.ID)
	}

	if cache.Len() != 2 {
		t.Errorf("Expected cache length 2, got %d", cache.Len())
	}
}

func TestMemoryCache_GetOldest(t *testing.T) {
	cache := NewMemoryCache(3)

	// Empty cache
	oldest := cache.GetOldest()
	if oldest != nil {
		t.Error("Expected nil for empty cache")
	}

	cache.Put("s1", &Session{ID: "s1"})
	cache.Put("s2", &Session{ID: "s2"})

	oldest = cache.GetOldest()
	if oldest == nil {
		t.Fatal("Expected oldest session")
		return
	}
	if oldest.ID != "s1" {
		t.Errorf("Expected oldest to be s1, got %s", oldest.ID)
	}

	// Should still be in cache (not removed)
	if !cache.Contains("s1") {
		t.Error("GetOldest should not remove the session")
	}
}

func TestMemoryCache_ZeroCapacity(t *testing.T) {
	// Zero capacity should default to 50
	cache := NewMemoryCache(0)

	if cache.Capacity() != 50 {
		t.Errorf("Expected default capacity 50, got %d", cache.Capacity())
	}
}

func TestMemoryCache_NegativeCapacity(t *testing.T) {
	// Negative capacity should default to 50
	cache := NewMemoryCache(-10)

	if cache.Capacity() != 50 {
		t.Errorf("Expected default capacity 50, got %d", cache.Capacity())
	}
}
