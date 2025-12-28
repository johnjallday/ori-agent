package filewatcher

import (
	"sync"
	"testing"
	"time"
)

func TestDebouncer_Trigger(t *testing.T) {
	var triggered []string
	var mu sync.Mutex

	d := NewDebouncer(50*time.Millisecond, func(key string) {
		mu.Lock()
		triggered = append(triggered, key)
		mu.Unlock()
	})

	// Trigger event
	d.Trigger("key1")

	// Wait for debounce
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	if len(triggered) != 1 {
		t.Errorf("expected 1 trigger, got %d", len(triggered))
	}
	if triggered[0] != "key1" {
		t.Errorf("expected key 'key1', got '%s'", triggered[0])
	}
	mu.Unlock()
}

func TestDebouncer_MultipleTriggersCoalesce(t *testing.T) {
	var triggered []string
	var mu sync.Mutex

	d := NewDebouncer(50*time.Millisecond, func(key string) {
		mu.Lock()
		triggered = append(triggered, key)
		mu.Unlock()
	})

	// Trigger multiple times rapidly
	d.Trigger("key1")
	time.Sleep(10 * time.Millisecond)
	d.Trigger("key1")
	time.Sleep(10 * time.Millisecond)
	d.Trigger("key1")

	// Wait for debounce
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	if len(triggered) != 1 {
		t.Errorf("expected 1 trigger (coalesced), got %d", len(triggered))
	}
	mu.Unlock()
}

func TestDebouncer_DifferentKeys(t *testing.T) {
	var triggered []string
	var mu sync.Mutex

	d := NewDebouncer(50*time.Millisecond, func(key string) {
		mu.Lock()
		triggered = append(triggered, key)
		mu.Unlock()
	})

	// Trigger different keys
	d.Trigger("key1")
	d.Trigger("key2")
	d.Trigger("key3")

	// Wait for debounce
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	if len(triggered) != 3 {
		t.Errorf("expected 3 triggers, got %d", len(triggered))
	}
	mu.Unlock()
}

func TestDebouncer_Cancel(t *testing.T) {
	var triggered []string
	var mu sync.Mutex

	d := NewDebouncer(50*time.Millisecond, func(key string) {
		mu.Lock()
		triggered = append(triggered, key)
		mu.Unlock()
	})

	// Trigger and cancel
	d.Trigger("key1")
	d.Cancel("key1")

	// Wait for potential debounce
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	if len(triggered) != 0 {
		t.Errorf("expected 0 triggers after cancel, got %d", len(triggered))
	}
	mu.Unlock()
}

func TestDebouncer_CancelAll(t *testing.T) {
	var triggered []string
	var mu sync.Mutex

	d := NewDebouncer(50*time.Millisecond, func(key string) {
		mu.Lock()
		triggered = append(triggered, key)
		mu.Unlock()
	})

	// Trigger multiple keys
	d.Trigger("key1")
	d.Trigger("key2")
	d.Trigger("key3")
	d.CancelAll()

	// Wait for potential debounce
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	if len(triggered) != 0 {
		t.Errorf("expected 0 triggers after cancel all, got %d", len(triggered))
	}
	mu.Unlock()
}

func TestDebouncer_PendingCount(t *testing.T) {
	d := NewDebouncer(100*time.Millisecond, func(key string) {})

	if d.PendingCount() != 0 {
		t.Errorf("expected 0 pending, got %d", d.PendingCount())
	}

	d.Trigger("key1")
	d.Trigger("key2")

	if d.PendingCount() != 2 {
		t.Errorf("expected 2 pending, got %d", d.PendingCount())
	}

	d.Cancel("key1")

	if d.PendingCount() != 1 {
		t.Errorf("expected 1 pending after cancel, got %d", d.PendingCount())
	}
}

func TestEventDebouncer_Add(t *testing.T) {
	d := NewEventDebouncer(50*time.Millisecond, 10)
	defer d.Close()

	event := WatchEvent{
		SessionID: "session-1",
		Type:      EventCreate,
		FilePath:  "/path/to/file.txt",
		FileName:  "file.txt",
		Timestamp: time.Now(),
	}

	d.Add(event)

	// Wait for debounce and receive event
	select {
	case received := <-d.Events():
		if received.SessionID != event.SessionID {
			t.Errorf("expected session ID '%s', got '%s'", event.SessionID, received.SessionID)
		}
		if received.Type != event.Type {
			t.Errorf("expected type '%s', got '%s'", event.Type, received.Type)
		}
	case <-time.After(200 * time.Millisecond):
		t.Error("timeout waiting for event")
	}
}

func TestEventDebouncer_CoalesceEvents(t *testing.T) {
	d := NewEventDebouncer(50*time.Millisecond, 10)
	defer d.Close()

	// Add multiple events for same file rapidly
	for i := 0; i < 5; i++ {
		d.Add(WatchEvent{
			SessionID: "session-1",
			Type:      EventModify,
			FilePath:  "/path/to/file.txt",
			FileName:  "file.txt",
			Timestamp: time.Now(),
		})
		time.Sleep(10 * time.Millisecond)
	}

	// Should only receive one event
	select {
	case <-d.Events():
		// Good, received one event
	case <-time.After(200 * time.Millisecond):
		t.Error("timeout waiting for event")
	}

	// Should not receive more events
	select {
	case <-d.Events():
		t.Error("should not receive additional events")
	case <-time.After(100 * time.Millisecond):
		// Good, no more events
	}
}

func TestEventDebouncer_MergeEvents_RemoveWins(t *testing.T) {
	d := NewEventDebouncer(50*time.Millisecond, 10)
	defer d.Close()

	// Create then remove
	d.Add(WatchEvent{
		SessionID: "session-1",
		Type:      EventCreate,
		FilePath:  "/path/to/file.txt",
	})
	d.Add(WatchEvent{
		SessionID: "session-1",
		Type:      EventRemove,
		FilePath:  "/path/to/file.txt",
	})

	select {
	case received := <-d.Events():
		if received.Type != EventRemove {
			t.Errorf("expected type 'remove', got '%s'", received.Type)
		}
	case <-time.After(200 * time.Millisecond):
		t.Error("timeout waiting for event")
	}
}

func TestEventDebouncer_MergeEvents_CreatePreserved(t *testing.T) {
	d := NewEventDebouncer(50*time.Millisecond, 10)
	defer d.Close()

	// Create then modify
	d.Add(WatchEvent{
		SessionID: "session-1",
		Type:      EventCreate,
		FilePath:  "/path/to/file.txt",
	})
	d.Add(WatchEvent{
		SessionID: "session-1",
		Type:      EventModify,
		FilePath:  "/path/to/file.txt",
	})

	select {
	case received := <-d.Events():
		if received.Type != EventCreate {
			t.Errorf("expected type 'create' (preserved), got '%s'", received.Type)
		}
	case <-time.After(200 * time.Millisecond):
		t.Error("timeout waiting for event")
	}
}

func TestEventDebouncer_Close(t *testing.T) {
	d := NewEventDebouncer(100*time.Millisecond, 10)

	// Add event
	d.Add(WatchEvent{
		SessionID: "session-1",
		Type:      EventCreate,
		FilePath:  "/path/to/file.txt",
	})

	// Close immediately (before debounce fires)
	d.Close()

	// Channel should be closed
	select {
	case _, ok := <-d.Events():
		if ok {
			t.Error("expected channel to be closed")
		}
	case <-time.After(200 * time.Millisecond):
		t.Error("timeout waiting for channel close")
	}
}
