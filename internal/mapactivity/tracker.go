// Package mapactivity turns the workspace event bus into the maps' live activity
// feed: which workspaces are working right now, which agent, and what step it
// is on (tasks/prd-task-run-show.md §4.1).
//
// The tracker keeps running activities in memory only. A restart forgets them,
// which is honest: a run the new process never saw start is not shown as
// running. Everything it streams is built from an allowlist of event fields, so
// tool arguments, prompts, task descriptions, results and error text never
// reach the browser (FR61).
package mapactivity

import (
	"sort"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const (
	// DefaultStaleAfter is how long a running activity may stay silent before
	// it is treated as lost (FR6). Task runs heartbeat every 5 seconds while
	// they execute (taskHeartbeatInterval), so this is 120 missed heartbeats.
	DefaultStaleAfter = 10 * time.Minute

	defaultSweepInterval = 30 * time.Second

	// subscriberBuffer bounds how far one slow stream can fall behind before
	// its events are dropped. A dropped event never blocks the bus.
	subscriberBuffer = 64

	// tombstoneTTL is how long a finished activity is remembered, so a step
	// event delivered out of order after its run's ending cannot light the
	// building again. The bus delivers each event on its own goroutine, so
	// ordering between two events of one run is not guaranteed.
	tombstoneTTL = 2 * time.Minute

	maxToolNameLength = 80
)

// EventSource is the part of the workspace event bus the tracker needs.
type EventSource interface {
	SubscribeToEventTypes(eventTypes []workspace.EventType, subscriber workspace.EventSubscriber) string
	Unsubscribe(subscriptionID string)
}

// SubscribedEventTypes lists every bus event the tracker reads.
func SubscribedEventTypes() []workspace.EventType {
	return []workspace.EventType{
		workspace.EventTaskStarted,
		workspace.EventTaskThinking,
		workspace.EventTaskToolCall,
		workspace.EventTaskToolResult,
		workspace.EventTaskHeartbeat,
		workspace.EventTaskBlocked,
		workspace.EventTaskResumed,
		workspace.EventTaskCompleted,
		workspace.EventTaskFailed,
		workspace.EventTaskTimeout,
		workspace.EventDelegationStarted,
		workspace.EventTaskDeleted,
		workspace.EventWorkspaceUpdated,
	}
}

// Option configures a Tracker.
type Option func(*Tracker)

// WithClock injects the clock used for staleness (tests).
func WithClock(now func() time.Time) Option {
	return func(t *Tracker) {
		if now != nil {
			t.now = now
		}
	}
}

// WithStaleAfter overrides DefaultStaleAfter (tests).
func WithStaleAfter(d time.Duration) Option {
	return func(t *Tracker) {
		if d > 0 {
			t.staleAfter = d
		}
	}
}

// WithSweepInterval overrides how often the stale sweep runs (tests).
func WithSweepInterval(d time.Duration) Option {
	return func(t *Tracker) {
		if d > 0 {
			t.sweepInterval = d
		}
	}
}

// Tracker keeps the running set and fans activity messages out to streams.
type Tracker struct {
	bus           EventSource
	now           func() time.Time
	staleAfter    time.Duration
	sweepInterval time.Duration

	// parcels turns finished runs into waiting results. Set once, before
	// Start, by SetParcels; the zero value creates no parcels.
	parcels             ParcelOptions
	parcelSweepInterval time.Duration

	mu       sync.Mutex
	running  map[string]*RunningActivity
	finished map[string]time.Time
	subs     map[uint64]*subscriber
	nextSub  uint64
	busSubID string
	stopCh   chan struct{}
	stopped  bool
	wg       sync.WaitGroup
}

type subscriber struct {
	ch     chan StreamMessage
	warned bool
}

// NewTracker builds a tracker over the bus. A nil bus is allowed: the tracker
// then only sees events handed to HandleEvent directly.
func NewTracker(bus EventSource, opts ...Option) *Tracker {
	t := &Tracker{
		bus:                 bus,
		now:                 time.Now,
		staleAfter:          DefaultStaleAfter,
		sweepInterval:       defaultSweepInterval,
		parcelSweepInterval: defaultParcelSweepInterval,
		running:             make(map[string]*RunningActivity),
		finished:            make(map[string]time.Time),
		subs:                make(map[uint64]*subscriber),
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

// Start subscribes to the bus and starts the stale sweep. It is safe to call
// once; later calls do nothing.
func (t *Tracker) Start() {
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.stopCh != nil || t.stopped {
		t.mu.Unlock()
		return
	}
	t.stopCh = make(chan struct{})
	stopCh := t.stopCh
	t.mu.Unlock()

	if t.bus != nil {
		id := t.bus.SubscribeToEventTypes(SubscribedEventTypes(), t.HandleEvent)
		t.mu.Lock()
		t.busSubID = id
		t.mu.Unlock()
	}

	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		ticker := time.NewTicker(t.sweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				t.SweepStale()
			}
		}
	}()

	if t.parcels.Store != nil {
		t.wg.Add(1)
		go func() {
			defer t.wg.Done()
			t.runParcelSweeps(stopCh)
		}()
	}
}

// Stop unsubscribes from the bus, stops the sweep, and closes every stream
// subscription so open streams end instead of holding the HTTP server's
// graceful shutdown open.
func (t *Tracker) Stop() {
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.stopped {
		t.mu.Unlock()
		return
	}
	t.stopped = true
	stopCh := t.stopCh
	busSubID := t.busSubID
	for id, sub := range t.subs {
		close(sub.ch)
		delete(t.subs, id)
	}
	t.mu.Unlock()

	if busSubID != "" && t.bus != nil {
		t.bus.Unsubscribe(busSubID)
	}
	if stopCh != nil {
		close(stopCh)
	}
	t.wg.Wait()
}

// HandleEvent applies one bus event. It never blocks on a slow stream.
func (t *Tracker) HandleEvent(ev workspace.Event) {
	if t == nil {
		return
	}
	switch ev.Type {
	case workspace.EventTaskDeleted:
		t.endTask(ev, OutcomeNone)
		t.deleteTaskParcels(ev)
		return
	case workspace.EventWorkspaceUpdated:
		// A cancelled manual run publishes no task.failed, only this status
		// update. It proves the run stopped, so the building goes dark — with
		// no outcome and no parcel, because nothing finished.
		if status, _ := ev.Data["status"].(workspace.TaskStatus); status == workspace.TaskStatusCancelled {
			t.endTask(ev, OutcomeNone)
		} else if stringField(ev.Data, "status") == string(workspace.TaskStatusCancelled) {
			t.endTask(ev, OutcomeNone)
		}
		return
	case workspace.EventTaskHeartbeat:
		t.heartbeat(ev)
		return
	}

	phase, ok := taskPhaseFor(ev.Type)
	if !ok {
		return
	}
	workspaceID := ev.WorkspaceID
	taskID := stringField(ev.Data, "task_id")
	if workspaceID == "" || taskID == "" {
		return
	}
	id := TaskActivityID(workspaceID, taskID)
	at := t.eventTime(ev)
	agent := stringField(ev.Data, "agent")

	// A finished run's parcel is written before the lock is taken, so a database
	// write never holds up the stream. Creation is idempotent, so a replayed
	// event that is then dropped below costs nothing.
	var parcelID string
	var newParcel *ParcelSummary
	if phase == PhaseFinished {
		parcelID, newParcel = t.createTaskParcel(ev, workspaceID, taskID, agent, at)
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped {
		return
	}
	if ended, ok := t.finished[id]; ok && !at.After(ended) {
		return // a late event from a run that already ended
	}

	act := t.running[id]
	message := ActivityEvent{
		Kind:        KindTask,
		Phase:       phase,
		ActivityID:  id,
		WorkspaceID: workspaceID,
		TaskID:      taskID,
		At:          at,
	}

	switch phase {
	case PhaseStarted:
		if act != nil && act.At.After(at) {
			// The run's start arrived after one of its own steps. The
			// activity is already lit; only fill in what the step lacked.
			if act.AgentName == "" {
				act.AgentName = agent
			}
			return
		}
		if act == nil {
			act = t.addRunning(id, workspaceID, taskID, at)
		}
		act.Blocked = false
		act.Step = nil
	case PhaseStep:
		step := stepFor(ev)
		if step != nil && len(step.ToolName) > maxToolNameLength {
			step.ToolName = step.ToolName[:maxToolNameLength]
		}
		if act == nil {
			if step == nil || step.Type == StepDelegation {
				// Delegation follows a run's failure; it never lights a
				// building that is not already working.
				return
			}
			act = t.addRunning(id, workspaceID, taskID, at)
		}
		act.Step = step
		message.Step = cloneStep(step)
	case PhaseBlocked:
		if act == nil {
			act = t.addRunning(id, workspaceID, taskID, at)
		}
		act.Blocked = true
	case PhaseResumed:
		if act == nil {
			act = t.addRunning(id, workspaceID, taskID, at)
		}
		act.Blocked = false
	case PhaseFinished:
		if act != nil && agent == "" {
			agent = act.AgentName
		}
		delete(t.running, id)
		t.finished[id] = at
		message.Outcome = taskOutcomeFor(ev.Type)
		message.AgentName = agent
		message.ParcelID = parcelID
		t.broadcastLocked(StreamMessage{Name: "activity", Payload: message})
		if newParcel != nil {
			t.broadcastLocked(StreamMessage{Name: "parcel", Payload: ParcelEvent{ParcelSummary: *newParcel}})
		}
		return
	}

	if agent != "" {
		act.AgentName = agent
	}
	act.At = at
	act.lastSeen = t.now()
	message.AgentName = act.AgentName
	t.broadcastLocked(StreamMessage{Name: "activity", Payload: message})
}

func (t *Tracker) addRunning(id, workspaceID, taskID string, at time.Time) *RunningActivity {
	act := &RunningActivity{
		Kind:        KindTask,
		ActivityID:  id,
		WorkspaceID: workspaceID,
		TaskID:      taskID,
		StartedAt:   at,
		At:          at,
		lastSeen:    t.now(),
	}
	t.running[id] = act
	delete(t.finished, id)
	return act
}

func (t *Tracker) heartbeat(ev workspace.Event) {
	taskID := stringField(ev.Data, "task_id")
	if ev.WorkspaceID == "" || taskID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if act := t.running[TaskActivityID(ev.WorkspaceID, taskID)]; act != nil {
		act.lastSeen = t.now()
	}
}

// endTask drops a running task activity without claiming an outcome.
func (t *Tracker) endTask(ev workspace.Event, outcome Outcome) {
	taskID := stringField(ev.Data, "task_id")
	if ev.WorkspaceID == "" || taskID == "" {
		return
	}
	id := TaskActivityID(ev.WorkspaceID, taskID)
	at := t.eventTime(ev)

	t.mu.Lock()
	defer t.mu.Unlock()
	act := t.running[id]
	if act == nil || t.stopped {
		return
	}
	delete(t.running, id)
	t.finished[id] = at
	t.broadcastLocked(StreamMessage{Name: "activity", Payload: finishedMessage(act, outcome, at)})
}

// SweepStale drops running activities that have been silent for longer than the
// stale window, and forgets old tombstones (FR6). A blocked activity is waiting
// on the user rather than working, so it publishes nothing by design; it stays
// until it is resumed, finished, cancelled or deleted (FR27).
func (t *Tracker) SweepStale() {
	if t == nil {
		return
	}
	now := t.now()
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped {
		return
	}
	for id, act := range t.running {
		if act.Blocked || now.Sub(act.lastSeen) < t.staleAfter {
			continue
		}
		delete(t.running, id)
		t.finished[id] = now
		t.broadcastLocked(StreamMessage{Name: "activity", Payload: finishedMessage(act, OutcomeNone, now)})
	}
	for id, ended := range t.finished {
		if now.Sub(ended) > tombstoneTTL {
			delete(t.finished, id)
		}
	}
}

func finishedMessage(act *RunningActivity, outcome Outcome, at time.Time) ActivityEvent {
	return ActivityEvent{
		Kind:        act.Kind,
		Phase:       PhaseFinished,
		ActivityID:  act.ActivityID,
		WorkspaceID: act.WorkspaceID,
		AgentName:   act.AgentName,
		TaskID:      act.TaskID,
		Outcome:     outcome,
		At:          at,
	}
}

// Snapshot returns the running activities, oldest first, and the unopened
// parcels.
func (t *Tracker) Snapshot() Snapshot {
	if t == nil {
		return Snapshot{Running: []RunningActivity{}, Parcels: []ParcelSummary{}}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.snapshotLocked()
}

func (t *Tracker) snapshotLocked() Snapshot {
	running := make([]RunningActivity, 0, len(t.running))
	for _, act := range t.running {
		copied := *act
		copied.Step = cloneStep(act.Step)
		running = append(running, copied)
	}
	sort.Slice(running, func(i, j int) bool {
		if !running[i].StartedAt.Equal(running[j].StartedAt) {
			return running[i].StartedAt.Before(running[j].StartedAt)
		}
		return running[i].ActivityID < running[j].ActivityID
	})
	return Snapshot{Running: running, Parcels: t.unopenedParcelRows()}
}

// Subscribe registers a stream subscriber. The returned channel is buffered;
// when it is full, new messages are dropped with one warning rather than
// blocking the bus. The returned function unsubscribes and is safe to call more
// than once.
func (t *Tracker) Subscribe() (<-chan StreamMessage, func()) {
	_, ch, unsubscribe := t.SnapshotAndSubscribe()
	return ch, unsubscribe
}

// SnapshotAndSubscribe takes the snapshot and registers the subscriber under one
// lock, so a stream that writes the snapshot first and then relays the channel
// neither misses nor repeats a change.
func (t *Tracker) SnapshotAndSubscribe() (Snapshot, <-chan StreamMessage, func()) {
	ch := make(chan StreamMessage, subscriberBuffer)
	if t == nil {
		close(ch)
		return Snapshot{Running: []RunningActivity{}, Parcels: []ParcelSummary{}}, ch, func() {}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	snapshot := t.snapshotLocked()
	if t.stopped {
		close(ch)
		return snapshot, ch, func() {}
	}
	t.nextSub++
	id := t.nextSub
	t.subs[id] = &subscriber{ch: ch}

	var once sync.Once
	return snapshot, ch, func() {
		once.Do(func() {
			t.mu.Lock()
			defer t.mu.Unlock()
			if sub, ok := t.subs[id]; ok {
				close(sub.ch)
				delete(t.subs, id)
			}
		})
	}
}

// SubscriberCount reports how many streams are subscribed.
func (t *Tracker) SubscriberCount() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.subs)
}

func (t *Tracker) broadcastLocked(message StreamMessage) {
	for id, sub := range t.subs {
		select {
		case sub.ch <- message:
		default:
			if !sub.warned {
				sub.warned = true
				logger.Warn("Map activity stream is not keeping up; dropping events for it", logger.Fields{"subscriber": id})
			}
		}
	}
}

func (t *Tracker) eventTime(ev workspace.Event) time.Time {
	if ev.Timestamp.IsZero() {
		return t.now().UTC()
	}
	return ev.Timestamp.UTC()
}
