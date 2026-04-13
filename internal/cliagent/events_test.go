package cliagent

import (
	"sync"
	"testing"
	"time"
)

func TestEventLogger_LogAndGet(t *testing.T) {
	l := NewEventLogger(t.TempDir())

	l.LogEvent("task1", CLIEvent{Type: "start", Timestamp: time.Now()})
	l.LogEvent("task1", CLIEvent{Type: "result", Timestamp: time.Now()})

	events := l.GetEvents("task1")
	if len(events) != 2 {
		t.Errorf("expected 2 events, got %d", len(events))
	}

	// Unknown task returns empty slice
	events = l.GetEvents("unknown")
	if len(events) != 0 {
		t.Errorf("expected 0 events for unknown task, got %d", len(events))
	}
}

func TestEventLogger_LogEvents(t *testing.T) {
	l := NewEventLogger(t.TempDir())

	batch := []CLIEvent{
		{Type: "a", Timestamp: time.Now()},
		{Type: "b", Timestamp: time.Now()},
	}
	l.LogEvents("task1", batch)

	if got := l.GetEvents("task1"); len(got) != 2 {
		t.Errorf("expected 2 events, got %d", len(got))
	}
}

func TestEventLogger_PersistAndLoad(t *testing.T) {
	dir := t.TempDir()
	l := NewEventLogger(dir)

	l.LogEvent("task1", CLIEvent{Type: "result", Content: "hello", Timestamp: time.Now()})

	if err := l.Persist("task1"); err != nil {
		t.Fatalf("persist: %v", err)
	}

	// Load into a fresh logger
	l2 := NewEventLogger(dir)
	events, err := l2.LoadEvents("task1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(events) != 1 {
		t.Errorf("expected 1 event, got %d", len(events))
	}
	if events[0].Content != "hello" {
		t.Errorf("expected content %q, got %q", "hello", events[0].Content)
	}
}

func TestEventLogger_PersistEmpty(t *testing.T) {
	l := NewEventLogger(t.TempDir())
	// Persisting a task with no events should not error
	if err := l.Persist("empty_task"); err != nil {
		t.Fatalf("persist empty: %v", err)
	}
}

func TestEventLogger_LoadMissing(t *testing.T) {
	l := NewEventLogger(t.TempDir())
	_, err := l.LoadEvents("nonexistent")
	if err == nil {
		t.Error("expected error loading nonexistent task")
	}
}

func TestEventLogger_Clear(t *testing.T) {
	l := NewEventLogger(t.TempDir())
	l.LogEvent("task1", CLIEvent{Type: "x"})
	l.Clear("task1")

	if got := l.GetEvents("task1"); len(got) != 0 {
		t.Errorf("expected 0 after clear, got %d", len(got))
	}
}

func TestEventLogger_Concurrent(t *testing.T) {
	l := NewEventLogger(t.TempDir())
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l.LogEvent("task1", CLIEvent{Type: "test", Timestamp: time.Now()})
		}()
	}

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = l.GetEvents("task1")
		}()
	}

	wg.Wait()

	events := l.GetEvents("task1")
	if len(events) != 50 {
		t.Errorf("expected 50 events after concurrent writes, got %d", len(events))
	}
}
