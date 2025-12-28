package filewatcher

import (
	"sync"
	"time"
)

// Debouncer collects events and emits them after a quiet period
type Debouncer struct {
	duration time.Duration
	timers   map[string]*time.Timer
	mu       sync.Mutex
	callback func(key string)
}

// NewDebouncer creates a new debouncer with the specified duration
func NewDebouncer(duration time.Duration, callback func(key string)) *Debouncer {
	return &Debouncer{
		duration: duration,
		timers:   make(map[string]*time.Timer),
		callback: callback,
	}
}

// Trigger schedules a callback for the given key after the debounce duration
// If called again for the same key before the duration expires, the timer resets
func (d *Debouncer) Trigger(key string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Cancel existing timer for this key
	if timer, exists := d.timers[key]; exists {
		timer.Stop()
	}

	// Create new timer
	d.timers[key] = time.AfterFunc(d.duration, func() {
		d.mu.Lock()
		delete(d.timers, key)
		d.mu.Unlock()

		if d.callback != nil {
			d.callback(key)
		}
	})
}

// Cancel cancels any pending callback for the given key
func (d *Debouncer) Cancel(key string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if timer, exists := d.timers[key]; exists {
		timer.Stop()
		delete(d.timers, key)
	}
}

// CancelAll cancels all pending callbacks
func (d *Debouncer) CancelAll() {
	d.mu.Lock()
	defer d.mu.Unlock()

	for key, timer := range d.timers {
		timer.Stop()
		delete(d.timers, key)
	}
}

// PendingCount returns the number of pending debounced events
func (d *Debouncer) PendingCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.timers)
}

// EventDebouncer debounces WatchEvents by file path
type EventDebouncer struct {
	duration time.Duration
	events   map[string]*debouncedEvent
	mu       sync.Mutex
	output   chan WatchEvent
	done     chan struct{}
}

type debouncedEvent struct {
	event WatchEvent
	timer *time.Timer
}

// NewEventDebouncer creates a new event debouncer
func NewEventDebouncer(duration time.Duration, bufferSize int) *EventDebouncer {
	return &EventDebouncer{
		duration: duration,
		events:   make(map[string]*debouncedEvent),
		output:   make(chan WatchEvent, bufferSize),
		done:     make(chan struct{}),
	}
}

// Add adds or updates an event for debouncing
// Events with the same file path will be coalesced
func (d *EventDebouncer) Add(event WatchEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()

	select {
	case <-d.done:
		return // Debouncer is closed
	default:
	}

	key := event.SessionID + ":" + event.FilePath

	// Cancel existing timer for this path
	if existing, exists := d.events[key]; exists {
		existing.timer.Stop()
		// Upgrade event type if needed (e.g., Create + Write = Create)
		event = mergeEvents(existing.event, event)
	}

	// Create new timer
	timer := time.AfterFunc(d.duration, func() {
		d.mu.Lock()
		de, exists := d.events[key]
		if exists {
			delete(d.events, key)
		}
		d.mu.Unlock()

		if exists {
			select {
			case d.output <- de.event:
			case <-d.done:
			}
		}
	})

	d.events[key] = &debouncedEvent{
		event: event,
		timer: timer,
	}
}

// Events returns the channel for receiving debounced events
func (d *EventDebouncer) Events() <-chan WatchEvent {
	return d.output
}

// Close stops the debouncer and closes the output channel
func (d *EventDebouncer) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()

	select {
	case <-d.done:
		return // Already closed
	default:
		close(d.done)
	}

	// Cancel all pending timers
	for key, de := range d.events {
		de.timer.Stop()
		delete(d.events, key)
	}

	close(d.output)
}

// mergeEvents combines two events for the same file
// Priority: Remove > Create > Modify
func mergeEvents(old, new WatchEvent) WatchEvent {
	// If either is a remove, the result is remove
	if new.Type == EventRemove || old.Type == EventRemove {
		new.Type = EventRemove
		return new
	}

	// If old was create, keep it as create (even if new is modify)
	if old.Type == EventCreate {
		new.Type = EventCreate
		return new
	}

	// Otherwise use the new event type
	return new
}
