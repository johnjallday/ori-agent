package trigger

import (
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/logger"
)

// maxAccumulatedEvents caps how many raw events a single fire carries.
// Further events within the same fire are counted in DroppedEvents and
// represented only by the count, bounding both memory and prompt size.
const maxAccumulatedEvents = 100

// DispatchFunc executes one coalesced fire. Implemented by the Dispatcher;
// injected so the coalescer is unit-testable without LLM machinery. Called
// on its own goroutine, never under the coalescer lock.
type DispatchFunc func(t Trigger, fire PendingFire)

// Coalescer turns raw trigger events into bounded fire decisions (PRD #19–21):
//
//   - Events within a trigger's debounce window (fixed-length, opened by the
//     first event) coalesce into one fire.
//   - At most one fire is in flight per trigger; events arriving meanwhile
//     merge into a single pending fire, persisted via the store so it
//     survives restarts.
//   - When the in-flight fire completes, the pending fire (if any) executes
//     with its accumulated context.
type Coalescer struct {
	store    *Store
	dispatch DispatchFunc
	// debounceFor resolves a trigger's window length; defaults to
	// Trigger.Debounce. Overridable so tests run on millisecond windows.
	debounceFor func(Trigger) time.Duration

	mu     sync.Mutex
	states map[string]*coalesceState // trigger ID → state
	closed bool
	// runs tracks live run goroutines so WaitIdle can drain them (tests,
	// graceful teardown diagnostics). Close intentionally does not wait: a
	// dispatch may be a multi-minute LLM run and shutdown must not hang on it.
	runs sync.WaitGroup
}

type coalesceState struct {
	window   *PendingFire // open debounce window accumulating events
	timer    *time.Timer
	inFlight bool
}

// NewCoalescer creates a coalescer that persists pending fires through store
// and executes fires through dispatch.
func NewCoalescer(store *Store, dispatch DispatchFunc) *Coalescer {
	return &Coalescer{
		store:       store,
		dispatch:    dispatch,
		debounceFor: func(t Trigger) time.Duration { return t.Debounce() },
		states:      make(map[string]*coalesceState),
	}
}

// Observe records a raw event for a trigger and returns the fire ID the
// event was folded into (a new window, the open window, or the queued
// pending fire). The fire ID is what webhook callers receive in the 202
// response for later correlation.
//
// The debounce window is fixed-length from the first event (not sliding), so
// a continuous event stream still produces a fire every window instead of
// starving forever.
func (c *Coalescer) Observe(t Trigger, ev Event) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ""
	}

	st := c.states[t.ID]
	if st == nil {
		st = &coalesceState{}
		c.states[t.ID] = st
	}

	if st.window != nil {
		appendEvent(st.window, ev)
		return st.window.FireID
	}

	st.window = &PendingFire{
		FireID:    newFireID(),
		Events:    []Event{ev},
		CreatedAt: time.Now(),
	}
	triggerID, wsID := t.ID, t.WorkspaceID
	st.timer = time.AfterFunc(c.debounceFor(t), func() {
		c.closeWindow(wsID, triggerID)
	})
	return st.window.FireID
}

// closeWindow fires when a trigger's debounce window elapses: the window
// either dispatches immediately or merges into the persisted pending fire if
// a run is already in flight.
func (c *Coalescer) closeWindow(wsID, triggerID string) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	st := c.states[triggerID]
	if st == nil || st.window == nil {
		c.mu.Unlock()
		return
	}
	fire := st.window
	st.window = nil
	st.timer = nil

	if st.inFlight {
		// Merge into the single pending slot and persist it (PRD #20–21).
		// The merged fire keeps the earlier fire ID so callers holding it
		// still correlate.
		c.mu.Unlock()
		_, err := c.store.Update(wsID, triggerID, func(t *Trigger) error {
			t.PendingFire = mergeFires(t.PendingFire, fire)
			return nil
		})
		if err != nil {
			logger.Warn("trigger coalescer: persist pending fire", logger.Fields{
				"trigger_id": triggerID, "workspace_id": wsID, "error": err,
			})
		}
		return
	}

	st.inFlight = true
	c.runs.Add(1)
	c.mu.Unlock()
	go c.run(wsID, triggerID, *fire)
}

// run executes one fire, then drains the pending slot until empty.
func (c *Coalescer) run(wsID, triggerID string, fire PendingFire) {
	defer c.runs.Done()
	for {
		t, err := c.store.Get(wsID, triggerID)
		if err != nil {
			// Trigger deleted while a fire was queued — drop it.
			logger.Debug("trigger coalescer: trigger gone, dropping fire", logger.Fields{
				"trigger_id": triggerID, "workspace_id": wsID,
			})
			c.clearInFlight(triggerID)
			return
		}
		if t.Enabled {
			c.dispatch(t, fire)
		} else {
			logger.Debug("trigger coalescer: trigger disabled, dropping fire", logger.Fields{
				"trigger_id": triggerID, "workspace_id": wsID,
			})
		}

		next := c.takePending(wsID, triggerID)
		if next == nil {
			c.clearInFlight(triggerID)
			return
		}
		fire = *next
	}
}

// takePending atomically claims and clears the persisted pending fire.
func (c *Coalescer) takePending(wsID, triggerID string) *PendingFire {
	var pf *PendingFire
	_, err := c.store.Update(wsID, triggerID, func(t *Trigger) error {
		pf = t.PendingFire
		t.PendingFire = nil
		return nil
	})
	if err != nil {
		if err != ErrNotFound {
			logger.Warn("trigger coalescer: claim pending fire", logger.Fields{
				"trigger_id": triggerID, "workspace_id": wsID, "error": err,
			})
		}
		return nil
	}
	return pf
}

// clearInFlight releases the in-flight marker so a future event can start a
// new run for this trigger.
func (c *Coalescer) clearInFlight(triggerID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if st := c.states[triggerID]; st != nil {
		st.inFlight = false
	}
}

// RestorePending dispatches fires that were persisted before a restart
// (PRD #21). Call once at startup after the store is loaded and the
// dispatcher is ready.
func (c *Coalescer) RestorePending() {
	for _, t := range c.store.ListAll() {
		if t.PendingFire == nil {
			continue
		}
		c.mu.Lock()
		st := c.states[t.ID]
		if st == nil {
			st = &coalesceState{}
			c.states[t.ID] = st
		}
		if st.inFlight {
			c.mu.Unlock()
			continue
		}
		st.inFlight = true
		c.mu.Unlock()

		fire := c.takePending(t.WorkspaceID, t.ID)
		if fire == nil {
			c.clearInFlight(t.ID)
			continue
		}
		logger.Info("trigger coalescer: restoring pending fire from before restart", logger.Fields{
			"trigger_id": t.ID, "workspace_id": t.WorkspaceID, "fire_id": fire.FireID,
		})
		c.runs.Add(1)
		go c.run(t.WorkspaceID, t.ID, *fire)
	}
}

// Drop discards any open window for a trigger (used when a trigger is
// disabled or deleted). In-flight runs complete; the persisted pending fire
// is the caller's responsibility (deletion removes it with the trigger).
func (c *Coalescer) Drop(triggerID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := c.states[triggerID]
	if st == nil {
		return
	}
	if st.timer != nil {
		st.timer.Stop()
		st.timer = nil
	}
	st.window = nil
}

// WaitIdle blocks until every run goroutine has finished. Useful in tests to
// avoid racing temp-dir cleanup against the final pending-slot store write.
func (c *Coalescer) WaitIdle() { c.runs.Wait() }

// Close stops accepting events and cancels open windows. In-flight
// dispatches finish on their own goroutines; queued pending fires stay
// persisted for the next startup.
func (c *Coalescer) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	for _, st := range c.states {
		if st.timer != nil {
			st.timer.Stop()
			st.timer = nil
		}
		st.window = nil
	}
}

// appendEvent adds an event to a fire, enforcing the accumulation caps: at
// most maxAccumulatedEvents events and MaxPayloadBytes of total body payload;
// beyond either, the event is only counted.
func appendEvent(fire *PendingFire, ev Event) {
	if len(fire.Events) >= maxAccumulatedEvents {
		fire.DroppedEvents++
		return
	}
	total := 0
	for _, e := range fire.Events {
		total += len(e.Body)
	}
	if total+len(ev.Body) > MaxPayloadBytes {
		ev.Body = ""
		ev.Truncated = true
	}
	fire.Events = append(fire.Events, ev)
}

// mergeFires folds a closed window into the existing pending fire (if any),
// keeping the earlier fire's ID and creation time.
func mergeFires(pending, window *PendingFire) *PendingFire {
	if pending == nil {
		return window
	}
	for _, ev := range window.Events {
		appendEvent(pending, ev)
	}
	pending.DroppedEvents += window.DroppedEvents
	return pending
}

// newFireID returns a fresh fire correlation ID.
func newFireID() string { return "fire-" + uuid.NewString() }
