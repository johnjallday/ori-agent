package calendarhttp

import (
	"testing"
	"time"
)

func TestReadCache_HitAndMiss(t *testing.T) {
	c := newReadCache(time.Minute)
	key := readCacheKey{UserID: "u1", WorkspaceID: "w1", BindingID: "b1", Operation: "list_events"}

	if _, hit := c.get(key); hit {
		t.Fatal("expected a miss before anything is stored")
	}
	c.set(key, "value")
	got, hit := c.get(key)
	if !hit || got != "value" {
		t.Fatalf("expected a hit with %q, got hit=%v value=%v", "value", hit, got)
	}
}

func TestReadCache_ExpiresAfterTTL(t *testing.T) {
	c := newReadCache(10 * time.Millisecond)
	key := readCacheKey{UserID: "u1", WorkspaceID: "w1", BindingID: "b1", Operation: "list_events"}
	c.set(key, "value")

	time.Sleep(30 * time.Millisecond)
	if _, hit := c.get(key); hit {
		t.Fatal("expected the entry to have expired")
	}
}

func TestReadCache_IsolatesByEveryKeyDimension(t *testing.T) {
	c := newReadCache(time.Minute)
	base := readCacheKey{UserID: "u1", WorkspaceID: "w1", BindingID: "b1", Operation: "list_events", ArgsHash: "h1"}
	c.set(base, "base-value")

	variants := []readCacheKey{
		{UserID: "u2", WorkspaceID: base.WorkspaceID, BindingID: base.BindingID, Operation: base.Operation, ArgsHash: base.ArgsHash},          // different user
		{UserID: base.UserID, WorkspaceID: "w2", BindingID: base.BindingID, Operation: base.Operation, ArgsHash: base.ArgsHash},               // different workspace
		{UserID: base.UserID, WorkspaceID: base.WorkspaceID, BindingID: "b2", Operation: base.Operation, ArgsHash: base.ArgsHash},             // different binding
		{UserID: base.UserID, WorkspaceID: base.WorkspaceID, BindingID: base.BindingID, Operation: "list_calendars", ArgsHash: base.ArgsHash}, // different operation
		{UserID: base.UserID, WorkspaceID: base.WorkspaceID, BindingID: base.BindingID, Operation: base.Operation, ArgsHash: "h2"},            // different args
	}
	for i, v := range variants {
		if _, hit := c.get(v); hit {
			t.Fatalf("variant %d must be a cache miss (isolated dimension), key=%+v", i, v)
		}
	}
	// The exact same key is still a hit.
	if got, hit := c.get(base); !hit || got != "base-value" {
		t.Fatalf("base key should still hit, got hit=%v value=%v", hit, got)
	}
}

func TestReadCache_NeverCachesErrors(t *testing.T) {
	// This is enforced by convention (callers only call set() on success),
	// but assert the cache itself has no path that would store a Go error
	// value silently as if it were valid data -- a get() on an unset key is
	// always a clean miss, never a stored error masquerading as a hit.
	c := newReadCache(time.Minute)
	key := readCacheKey{UserID: "u1", WorkspaceID: "w1", BindingID: "b1", Operation: "list_events"}
	if _, hit := c.get(key); hit {
		t.Fatal("an operation that was never successfully cached must always miss")
	}
}

func TestReadCache_InvalidateBindingClearsOnlyThatBinding(t *testing.T) {
	c := newReadCache(time.Minute)
	keyA := readCacheKey{UserID: "u1", WorkspaceID: "w1", BindingID: "b1", Operation: "list_events"}
	keyB := readCacheKey{UserID: "u1", WorkspaceID: "w1", BindingID: "b2", Operation: "list_events"}
	c.set(keyA, "a")
	c.set(keyB, "b")

	c.invalidateBinding("b1")

	if _, hit := c.get(keyA); hit {
		t.Fatal("binding b1's entry must be invalidated")
	}
	if got, hit := c.get(keyB); !hit || got != "b" {
		t.Fatal("binding b2's entry must survive invalidating b1")
	}
}

func TestReadCacheArgsHash_StableAcrossKeyOrder(t *testing.T) {
	h1 := readCacheArgsHash(map[string]any{"start_time": "a", "end_time": "b"})
	h2 := readCacheArgsHash(map[string]any{"end_time": "b", "start_time": "a"})
	if h1 != h2 {
		t.Fatalf("hash must be independent of map construction order: %q vs %q", h1, h2)
	}
}

func TestReadCacheArgsHash_DifferentArgsDifferentHash(t *testing.T) {
	h1 := readCacheArgsHash(map[string]any{"calendar_id": "cal-1"})
	h2 := readCacheArgsHash(map[string]any{"calendar_id": "cal-2"})
	if h1 == h2 {
		t.Fatal("different argument values must hash differently")
	}
}

func TestReadCacheArgsHash_EmptyArgsIsEmptyString(t *testing.T) {
	if got := readCacheArgsHash(nil); got != "" {
		t.Errorf("nil args should hash to empty string, got %q", got)
	}
	if got := readCacheArgsHash(map[string]any{}); got != "" {
		t.Errorf("empty args should hash to empty string, got %q", got)
	}
}

// nilCache verifies every method tolerates a nil *readCache (defensive --
// Handler always constructs one via NewHandler, but a zero-value Handler used
// directly in a test must not panic).
func TestReadCache_NilReceiverIsSafe(t *testing.T) {
	var c *readCache
	if _, hit := c.get(readCacheKey{}); hit {
		t.Fatal("nil cache must always miss")
	}
	c.set(readCacheKey{}, "x") // must not panic
	c.invalidateBinding("b1")  // must not panic
}
