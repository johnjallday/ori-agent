package trigger

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// dispatchRecorder collects dispatched fires; optionally blocks each dispatch
// until released so tests can simulate an in-flight run.
type dispatchRecorder struct {
	mu      sync.Mutex
	fires   []PendingFire
	release chan struct{} // nil = don't block
	done    chan struct{} // signaled after every dispatch returns
}

func newDispatchRecorder(blocking bool) *dispatchRecorder {
	r := &dispatchRecorder{done: make(chan struct{}, 16)}
	if blocking {
		r.release = make(chan struct{})
	}
	return r
}

func (r *dispatchRecorder) dispatch(t Trigger, fire PendingFire) {
	if r.release != nil {
		<-r.release
	}
	r.mu.Lock()
	r.fires = append(r.fires, fire)
	r.mu.Unlock()
	r.done <- struct{}{}
}

func (r *dispatchRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.fires)
}

func (r *dispatchRecorder) fire(i int) PendingFire {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.fires[i]
}

func (r *dispatchRecorder) waitDispatch(t *testing.T) {
	t.Helper()
	select {
	case <-r.done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for dispatch")
	}
}

const testDebounce = 50 * time.Millisecond

func newTestCoalescer(t *testing.T, rec *dispatchRecorder) (*Coalescer, *Store, Trigger) {
	t.Helper()
	store, _ := newTestStore(t, "ws1")
	created, err := store.Create(webhookTrigger("ws1"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	c := NewCoalescer(store, rec.dispatch)
	c.debounceFor = func(Trigger) time.Duration { return testDebounce }
	t.Cleanup(func() {
		c.Close()
		c.WaitIdle() // don't race temp-dir cleanup against the final store write
	})
	return c, store, created
}

func TestCoalescerBurstProducesOneFire(t *testing.T) {
	rec := newDispatchRecorder(false)
	c, _, trg := newTestCoalescer(t, rec)

	var firstID string
	for i := 0; i < 10; i++ {
		id := c.Observe(trg, Event{Kind: "file", FileEvent: "create", FileName: fmt.Sprintf("f%d.txt", i), Timestamp: time.Now()})
		if firstID == "" {
			firstID = id
		} else if id != firstID {
			t.Errorf("event %d got fire ID %q, want shared %q", i, id, firstID)
		}
	}

	rec.waitDispatch(t)
	if rec.count() != 1 {
		t.Fatalf("dispatches = %d, want 1", rec.count())
	}
	fire := rec.fire(0)
	if fire.FireID != firstID {
		t.Errorf("dispatched fire ID %q, want %q", fire.FireID, firstID)
	}
	if len(fire.Events) != 10 {
		t.Errorf("coalesced events = %d, want 10", len(fire.Events))
	}
}

func TestCoalescerInFlightMergesToSinglePending(t *testing.T) {
	rec := newDispatchRecorder(true)
	c, store, trg := newTestCoalescer(t, rec)

	// First event → window closes → dispatch blocks (in flight).
	c.Observe(trg, Event{Kind: "file", FileEvent: "create", FileName: "a.txt", Timestamp: time.Now()})
	time.Sleep(2 * testDebounce) // let the window close and dispatch start

	// Two more windows' worth of events while in flight.
	c.Observe(trg, Event{Kind: "file", FileEvent: "create", FileName: "b.txt", Timestamp: time.Now()})
	time.Sleep(2 * testDebounce)
	c.Observe(trg, Event{Kind: "file", FileEvent: "create", FileName: "c.txt", Timestamp: time.Now()})
	time.Sleep(2 * testDebounce)

	// The pending fire must be persisted (single slot, merged).
	got, _ := store.Get("ws1", trg.ID)
	if got.PendingFire == nil {
		t.Fatal("pending fire not persisted while in flight")
	}
	if len(got.PendingFire.Events) != 2 {
		t.Errorf("pending events = %d, want 2 (merged)", len(got.PendingFire.Events))
	}

	// Release: first dispatch completes, pending fire dispatches next.
	close(rec.release)
	rec.waitDispatch(t)
	rec.waitDispatch(t)

	if rec.count() != 2 {
		t.Fatalf("dispatches = %d, want 2 (one run + one merged follow-up)", rec.count())
	}
	if n := len(rec.fire(1).Events); n != 2 {
		t.Errorf("follow-up fire events = %d, want 2", n)
	}

	// Pending slot must be cleared afterwards.
	got, _ = store.Get("ws1", trg.ID)
	if got.PendingFire != nil {
		t.Error("pending fire not cleared after execution")
	}
}

func TestCoalescerRestorePending(t *testing.T) {
	rec := newDispatchRecorder(false)
	c, store, trg := newTestCoalescer(t, rec)

	// Simulate a fire persisted before a restart.
	if err := store.SetPendingFire("ws1", trg.ID, &PendingFire{
		FireID:    "fire-before-restart",
		Events:    []Event{{Kind: "webhook", Body: "{}", Timestamp: time.Now()}},
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("SetPendingFire: %v", err)
	}

	c.RestorePending()
	rec.waitDispatch(t)

	if rec.count() != 1 {
		t.Fatalf("dispatches = %d, want 1", rec.count())
	}
	if rec.fire(0).FireID != "fire-before-restart" {
		t.Errorf("restored fire ID = %q", rec.fire(0).FireID)
	}
	got, _ := store.Get("ws1", trg.ID)
	if got.PendingFire != nil {
		t.Error("pending fire not cleared after restore")
	}
}

func TestCoalescerDropDiscardsWindow(t *testing.T) {
	rec := newDispatchRecorder(false)
	c, _, trg := newTestCoalescer(t, rec)

	c.Observe(trg, Event{Kind: "file", FileEvent: "create", FileName: "a.txt", Timestamp: time.Now()})
	c.Drop(trg.ID)

	time.Sleep(3 * testDebounce)
	if rec.count() != 0 {
		t.Errorf("dispatches after Drop = %d, want 0", rec.count())
	}
}

func TestCoalescerDisabledTriggerDropsFire(t *testing.T) {
	rec := newDispatchRecorder(false)
	c, store, trg := newTestCoalescer(t, rec)

	c.Observe(trg, Event{Kind: "file", FileEvent: "create", FileName: "a.txt", Timestamp: time.Now()})
	// Disable before the window closes: the fire must be dropped at dispatch.
	if _, err := store.Update("ws1", trg.ID, func(tr *Trigger) error {
		tr.Enabled = false
		return nil
	}); err != nil {
		t.Fatalf("disable: %v", err)
	}

	time.Sleep(3 * testDebounce)
	if rec.count() != 0 {
		t.Errorf("disabled trigger dispatched %d fires, want 0", rec.count())
	}
}

func TestAppendEventCaps(t *testing.T) {
	fire := &PendingFire{FireID: "f"}
	for i := 0; i < maxAccumulatedEvents+10; i++ {
		appendEvent(fire, Event{Kind: "file", FileName: "x"})
	}
	if len(fire.Events) != maxAccumulatedEvents {
		t.Errorf("events = %d, want cap %d", len(fire.Events), maxAccumulatedEvents)
	}
	if fire.DroppedEvents != 10 {
		t.Errorf("dropped = %d, want 10", fire.DroppedEvents)
	}
	if fire.EventCount() != maxAccumulatedEvents+10 {
		t.Errorf("EventCount = %d", fire.EventCount())
	}

	// Payload cap: oversized bodies are blanked, not appended whole.
	big := &PendingFire{FireID: "g"}
	appendEvent(big, Event{Kind: "webhook", Body: string(make([]byte, MaxPayloadBytes-10))})
	appendEvent(big, Event{Kind: "webhook", Body: "this pushes past the cap"})
	if !big.Events[1].Truncated || big.Events[1].Body != "" {
		t.Error("second body should be dropped once cap is reached")
	}
}
