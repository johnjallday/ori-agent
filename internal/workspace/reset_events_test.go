package workspace

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/resetstate"
)

func TestResetAdmissionEventCallbacksRemainTrackedAfterPublishAndShutdown(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	ws := NewWorkspace(CreateWorkspaceParams{Name: "before callback"})
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	g := &resetstate.WorkGate{}
	bus := NewEventBus(4, 4)
	bus.SetAdmissionGate(g)
	t.Cleanup(bus.Shutdown)
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	finish := sync.OnceFunc(func() { close(release) })
	join := sync.OnceFunc(func() {
		if err := resetWait(t, done); err != nil {
			t.Error(err)
		}
	})
	t.Cleanup(func() { finish(); join() })
	var calls atomic.Int32
	bus.Subscribe(func(Event) {
		calls.Add(1)
		close(started)
		<-release
		done <- store.Update(ws.ID, func(fresh *Workspace) error { fresh.Description = "callback saved"; return nil })
	}, nil)
	bus.Publish(Event{Type: EventWorkspaceUpdated})
	if g.Snapshot().Active != 1 {
		t.Fatal("Publish returned before callback registration")
	}
	resetWait(t, started)
	bus.Shutdown() // Still not a callback join; admission must not mistake it for one.
	if err := g.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
		t.Fatalf("live callback after Shutdown: %v", err)
	}
	finish()
	join()
	// The callback reports its save before returning/releasing. Wait for the
	// actual permit rather than equating the report channel with quiescence.
	deadline := time.Now().Add(5 * time.Second)
	for g.Snapshot().Active != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if err := g.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	fresh, err := store.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Description != "callback saved" {
		t.Fatal("active callback was cancelled or lost its save")
	}
	// Shutdown cleared the old subscriptions. Add a new one so refusing a
	// fenced Publish is not made vacuous by an empty subscriber set. Filters
	// run synchronously, so this assertion does not race goroutine scheduling.
	var filters atomic.Int32
	bus.Subscribe(func(Event) { calls.Add(1) }, func(Event) bool { filters.Add(1); return true })
	history := len(bus.GetHistory(nil, 10))
	bus.Publish(Event{Type: EventTaskCreated})
	if calls.Load() != 1 || filters.Load() != 0 || len(bus.GetHistory(nil, 10)) != history {
		t.Fatal("fenced publish changed history or dispatched a callback")
	}
}

func TestResetAdmissionEventSubscriberPanicDoesNotLeakPermit(t *testing.T) {
	g := &resetstate.WorkGate{}
	bus := NewEventBus(4, 4)
	bus.SetAdmissionGate(g)
	t.Cleanup(bus.Shutdown)
	bus.Subscribe(func(Event) { panic("owned subscriber failure") }, nil)
	bus.Publish(Event{Type: EventTaskCreated})
	deadline := time.Now().Add(5 * time.Second)
	for g.Snapshot().Active != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if err := g.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
}
