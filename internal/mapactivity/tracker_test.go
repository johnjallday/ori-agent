package mapactivity

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// taskEvent builds a bus event the way the executor does, stamped by the clock
// so ordering in a test is explicit.
func taskEvent(clock *fakeClock, eventType workspace.EventType, data map[string]any) workspace.Event {
	ev := workspace.NewTaskEvent(eventType, "ws-1", "task-1", "Theo", data)
	ev.Timestamp = clock.Now()
	return ev
}

func newTestTracker(clock *fakeClock) *Tracker {
	return NewTracker(nil, WithClock(clock.Now))
}

func drain(t *testing.T, ch <-chan StreamMessage) []ActivityEvent {
	t.Helper()
	var out []ActivityEvent
	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				return out
			}
			ev, isActivity := msg.Payload.(ActivityEvent)
			if !isActivity {
				t.Fatalf("message %q payload = %T, want ActivityEvent", msg.Name, msg.Payload)
			}
			if msg.Name != "activity" {
				t.Fatalf("message name = %q, want activity", msg.Name)
			}
			out = append(out, ev)
		default:
			return out
		}
	}
}

func TestTracker_MapsEveryBusEventToItsPhase(t *testing.T) {
	cases := []struct {
		name      string
		eventType workspace.EventType
		data      map[string]any
		phase     Phase
		stepType  StepType
		toolName  string
		outcome   Outcome
	}{
		{name: "started", eventType: workspace.EventTaskStarted, phase: PhaseStarted},
		{name: "thinking", eventType: workspace.EventTaskThinking, phase: PhaseStep, stepType: StepThinking},
		{name: "tool call", eventType: workspace.EventTaskToolCall, data: map[string]any{"tool_name": "read_file"}, phase: PhaseStep, stepType: StepToolCall, toolName: "read_file"},
		{name: "tool result", eventType: workspace.EventTaskToolResult, data: map[string]any{"tool_name": "read_file", "success": true}, phase: PhaseStep, stepType: StepToolResult, toolName: "read_file"},
		{name: "delegation", eventType: workspace.EventDelegationStarted, phase: PhaseStep, stepType: StepDelegation},
		{name: "blocked", eventType: workspace.EventTaskBlocked, phase: PhaseBlocked},
		{name: "resumed", eventType: workspace.EventTaskResumed, phase: PhaseResumed},
		{name: "completed", eventType: workspace.EventTaskCompleted, phase: PhaseFinished, outcome: OutcomeSucceeded},
		{name: "failed", eventType: workspace.EventTaskFailed, phase: PhaseFinished, outcome: OutcomeFailed},
		{name: "timeout", eventType: workspace.EventTaskTimeout, phase: PhaseFinished, outcome: OutcomeTimeout},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clock := newFakeClock()
			tracker := newTestTracker(clock)
			// Start the run first so a delegation step (which never lights a
			// building by itself) has an activity to attach to.
			tracker.HandleEvent(taskEvent(clock, workspace.EventTaskStarted, nil))
			ch, unsubscribe := tracker.Subscribe()
			defer unsubscribe()

			clock.Advance(time.Second)
			tracker.HandleEvent(taskEvent(clock, tc.eventType, tc.data))

			events := drain(t, ch)
			if len(events) != 1 {
				t.Fatalf("got %d messages, want 1: %+v", len(events), events)
			}
			got := events[0]
			if got.Phase != tc.phase {
				t.Errorf("phase = %q, want %q", got.Phase, tc.phase)
			}
			if got.Kind != KindTask || got.ActivityID != "task:ws-1:task-1" || got.WorkspaceID != "ws-1" || got.AgentName != "Theo" || got.TaskID != "task-1" {
				t.Errorf("identity = %+v, want task:ws-1:task-1 / Theo", got)
			}
			if got.Outcome != tc.outcome {
				t.Errorf("outcome = %q, want %q", got.Outcome, tc.outcome)
			}
			if tc.stepType == "" {
				if got.Step != nil {
					t.Errorf("step = %+v, want none", got.Step)
				}
			} else {
				if got.Step == nil || got.Step.Type != tc.stepType || got.Step.ToolName != tc.toolName {
					t.Errorf("step = %+v, want type %q tool %q", got.Step, tc.stepType, tc.toolName)
				}
			}
			if !got.At.Equal(clock.Now()) {
				t.Errorf("at = %v, want %v", got.At, clock.Now())
			}
		})
	}
}

func TestTracker_HeartbeatRefreshesLastSeenAndSendsNothing(t *testing.T) {
	clock := newFakeClock()
	tracker := NewTracker(nil, WithClock(clock.Now), WithStaleAfter(10*time.Minute))
	tracker.HandleEvent(taskEvent(clock, workspace.EventTaskStarted, nil))
	ch, unsubscribe := tracker.Subscribe()
	defer unsubscribe()

	clock.Advance(9 * time.Minute)
	tracker.HandleEvent(taskEvent(clock, workspace.EventTaskHeartbeat, nil))
	clock.Advance(9 * time.Minute)
	tracker.SweepStale()

	if events := drain(t, ch); len(events) != 0 {
		t.Fatalf("heartbeat and sweep sent %d messages, want none: %+v", len(events), events)
	}
	if running := tracker.Snapshot().Running; len(running) != 1 {
		t.Fatalf("running = %d, want the heartbeating run to survive", len(running))
	}
}

func TestTracker_PayloadsNeverCarryArgumentsDescriptionsResultsOrErrors(t *testing.T) {
	clock := newFakeClock()
	tracker := newTestTracker(clock)
	ch, unsubscribe := tracker.Subscribe()
	defer unsubscribe()

	secret := "TOP-SECRET-CONTENT"
	tracker.HandleEvent(taskEvent(clock, workspace.EventTaskStarted, map[string]any{"description": secret, "priority": 3}))
	tracker.HandleEvent(taskEvent(clock, workspace.EventTaskThinking, map[string]any{"message": secret}))
	tracker.HandleEvent(taskEvent(clock, workspace.EventTaskToolCall, map[string]any{
		"tool_name": "read_file",
		"arguments": map[string]any{"path": secret},
	}))
	tracker.HandleEvent(taskEvent(clock, workspace.EventTaskToolResult, map[string]any{
		"tool_name": "read_file", "success": false, "error": secret, "result_preview": secret,
	}))
	clock.Advance(time.Second)
	tracker.HandleEvent(taskEvent(clock, workspace.EventTaskCompleted, map[string]any{
		"description": secret, "result": secret, "run_id": "run-1", "scheduled": false,
	}))

	events := drain(t, ch)
	if len(events) != 5 {
		t.Fatalf("got %d messages, want 5", len(events))
	}
	for _, ev := range events {
		raw, err := json.Marshal(ev)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		text := string(raw)
		for _, banned := range []string{secret, `"arguments"`, `"description"`, `"result"`, `"error"`, `"message"`, `"result_preview"`} {
			if strings.Contains(text, banned) {
				t.Errorf("payload %s contains %s", text, banned)
			}
		}
	}
	snapshotRaw, _ := json.Marshal(tracker.Snapshot())
	if strings.Contains(string(snapshotRaw), secret) {
		t.Errorf("snapshot %s leaks event content", snapshotRaw)
	}
}

func TestTracker_ToolResultReportsWhetherItWorked(t *testing.T) {
	clock := newFakeClock()
	tracker := newTestTracker(clock)
	ch, unsubscribe := tracker.Subscribe()
	defer unsubscribe()

	tracker.HandleEvent(taskEvent(clock, workspace.EventTaskToolResult, map[string]any{"tool_name": "web_search", "success": false}))
	events := drain(t, ch)
	if len(events) != 1 || events[0].Step == nil || events[0].Step.OK == nil || *events[0].Step.OK {
		t.Fatalf("events = %+v, want one tool_result step with ok=false", events)
	}
}

func TestTracker_BlockedResumedFinished(t *testing.T) {
	clock := newFakeClock()
	tracker := newTestTracker(clock)

	tracker.HandleEvent(taskEvent(clock, workspace.EventTaskStarted, nil))
	clock.Advance(time.Second)
	tracker.HandleEvent(taskEvent(clock, workspace.EventTaskBlocked, map[string]any{"error": "needs input"}))
	if running := tracker.Snapshot().Running; len(running) != 1 || !running[0].Blocked {
		t.Fatalf("after blocked running = %+v, want one blocked activity", running)
	}

	clock.Advance(time.Second)
	tracker.HandleEvent(taskEvent(clock, workspace.EventTaskResumed, nil))
	if running := tracker.Snapshot().Running; len(running) != 1 || running[0].Blocked {
		t.Fatalf("after resumed running = %+v, want one unblocked activity", running)
	}

	clock.Advance(time.Second)
	tracker.HandleEvent(taskEvent(clock, workspace.EventTaskCompleted, nil))
	if running := tracker.Snapshot().Running; len(running) != 0 {
		t.Fatalf("after completed running = %+v, want none", running)
	}
}

func TestTracker_StaleSweepDropsSilentRunsWithNoOutcome(t *testing.T) {
	clock := newFakeClock()
	tracker := NewTracker(nil, WithClock(clock.Now), WithStaleAfter(10*time.Minute))
	tracker.HandleEvent(taskEvent(clock, workspace.EventTaskStarted, nil))
	ch, unsubscribe := tracker.Subscribe()
	defer unsubscribe()

	clock.Advance(9*time.Minute + 59*time.Second)
	tracker.SweepStale()
	if events := drain(t, ch); len(events) != 0 {
		t.Fatalf("swept too early: %+v", events)
	}

	clock.Advance(2 * time.Second)
	tracker.SweepStale()
	events := drain(t, ch)
	if len(events) != 1 {
		t.Fatalf("got %d messages, want one finished", len(events))
	}
	got := events[0]
	if got.Phase != PhaseFinished || got.Outcome != OutcomeNone || got.ParcelID != "" || got.AgentName != "Theo" {
		t.Fatalf("finished = %+v, want phase finished with an empty outcome and no parcel", got)
	}
	if running := tracker.Snapshot().Running; len(running) != 0 {
		t.Fatalf("running = %+v, want the stale run dropped", running)
	}
}

func TestTracker_StaleSweepLeavesBlockedRunsAlone(t *testing.T) {
	clock := newFakeClock()
	tracker := NewTracker(nil, WithClock(clock.Now), WithStaleAfter(10*time.Minute))
	tracker.HandleEvent(taskEvent(clock, workspace.EventTaskBlocked, nil))

	clock.Advance(3 * time.Hour)
	tracker.SweepStale()
	if running := tracker.Snapshot().Running; len(running) != 1 {
		t.Fatalf("running = %+v, want the blocked run kept until resumed or finished", running)
	}
}

func TestTracker_LateStepAfterFinishDoesNotRelight(t *testing.T) {
	clock := newFakeClock()
	tracker := newTestTracker(clock)

	stepAt := clock.Now()
	tracker.HandleEvent(taskEvent(clock, workspace.EventTaskStarted, nil))
	clock.Advance(time.Second)
	tracker.HandleEvent(taskEvent(clock, workspace.EventTaskCompleted, nil))

	late := workspace.NewTaskEvent(workspace.EventTaskThinking, "ws-1", "task-1", "Theo", nil)
	late.Timestamp = stepAt
	tracker.HandleEvent(late)

	if running := tracker.Snapshot().Running; len(running) != 0 {
		t.Fatalf("running = %+v, want a late step to stay dark", running)
	}

	// A genuine rerun after the finish lights it again.
	clock.Advance(time.Second)
	tracker.HandleEvent(taskEvent(clock, workspace.EventTaskStarted, nil))
	if running := tracker.Snapshot().Running; len(running) != 1 {
		t.Fatalf("running = %+v, want the rerun lit", running)
	}
}

func TestTracker_DelegationNeverLightsAnIdleBuilding(t *testing.T) {
	clock := newFakeClock()
	tracker := newTestTracker(clock)
	tracker.HandleEvent(taskEvent(clock, workspace.EventDelegationStarted, nil))
	if running := tracker.Snapshot().Running; len(running) != 0 {
		t.Fatalf("running = %+v, want none", running)
	}
}

func TestTracker_CancelledOrDeletedRunGoesDarkWithoutAnOutcome(t *testing.T) {
	for name, ev := range map[string]workspace.Event{
		"cancelled": workspace.NewWorkspaceEvent(workspace.EventWorkspaceUpdated, "ws-1", "manual-execution-cancelled", map[string]any{
			"task_id": "task-1", "status": workspace.TaskStatusCancelled,
		}),
		"deleted": workspace.NewTaskEvent(workspace.EventTaskDeleted, "ws-1", "task-1", "", nil),
	} {
		t.Run(name, func(t *testing.T) {
			clock := newFakeClock()
			tracker := newTestTracker(clock)
			tracker.HandleEvent(taskEvent(clock, workspace.EventTaskStarted, nil))
			ch, unsubscribe := tracker.Subscribe()
			defer unsubscribe()

			clock.Advance(time.Second)
			ev.Timestamp = clock.Now()
			tracker.HandleEvent(ev)

			events := drain(t, ch)
			if len(events) != 1 || events[0].Phase != PhaseFinished || events[0].Outcome != OutcomeNone {
				t.Fatalf("events = %+v, want one finished with no outcome", events)
			}
			if running := tracker.Snapshot().Running; len(running) != 0 {
				t.Fatalf("running = %+v, want none", running)
			}
		})
	}
}

func TestTracker_CompletedStatusUpdateDoesNotEndTheRun(t *testing.T) {
	clock := newFakeClock()
	tracker := newTestTracker(clock)
	tracker.HandleEvent(taskEvent(clock, workspace.EventTaskStarted, nil))
	tracker.HandleEvent(workspace.NewWorkspaceEvent(workspace.EventWorkspaceUpdated, "ws-1", "task-executor", map[string]any{
		"task_id": "task-1", "status": workspace.TaskStatusCompleted,
	}))
	if running := tracker.Snapshot().Running; len(running) != 1 {
		t.Fatalf("running = %+v, want the run kept until its own task.completed", running)
	}
}

func TestTracker_SnapshotCarriesTheLatestStep(t *testing.T) {
	clock := newFakeClock()
	tracker := newTestTracker(clock)
	tracker.HandleEvent(taskEvent(clock, workspace.EventTaskStarted, nil))
	clock.Advance(time.Second)
	tracker.HandleEvent(taskEvent(clock, workspace.EventTaskToolCall, map[string]any{"tool_name": "web_search"}))

	snapshot := tracker.Snapshot()
	if len(snapshot.Running) != 1 {
		t.Fatalf("running = %+v, want one", snapshot.Running)
	}
	act := snapshot.Running[0]
	if act.Step == nil || act.Step.ToolName != "web_search" || act.AgentName != "Theo" || act.WorkspaceID != "ws-1" {
		t.Fatalf("running[0] = %+v, want Theo on web_search in ws-1", act)
	}
	if snapshot.Parcels == nil {
		t.Fatal("parcels is null, want an empty list the client can iterate")
	}
}

func TestTracker_TwoSubscribersBothReceive(t *testing.T) {
	clock := newFakeClock()
	tracker := newTestTracker(clock)
	first, unsubscribeFirst := tracker.Subscribe()
	defer unsubscribeFirst()
	second, unsubscribeSecond := tracker.Subscribe()
	defer unsubscribeSecond()

	tracker.HandleEvent(taskEvent(clock, workspace.EventTaskStarted, nil))

	if got := len(drain(t, first)); got != 1 {
		t.Errorf("first subscriber got %d messages, want 1", got)
	}
	if got := len(drain(t, second)); got != 1 {
		t.Errorf("second subscriber got %d messages, want 1", got)
	}
}

func TestTracker_UnsubscribeStopsDeliveryAndRestoresTheCount(t *testing.T) {
	clock := newFakeClock()
	tracker := newTestTracker(clock)
	baseline := tracker.SubscriberCount()

	ch, unsubscribe := tracker.Subscribe()
	if tracker.SubscriberCount() != baseline+1 {
		t.Fatalf("SubscriberCount = %d, want %d", tracker.SubscriberCount(), baseline+1)
	}
	unsubscribe()
	unsubscribe() // safe twice

	if tracker.SubscriberCount() != baseline {
		t.Fatalf("SubscriberCount = %d, want baseline %d", tracker.SubscriberCount(), baseline)
	}
	tracker.HandleEvent(taskEvent(clock, workspace.EventTaskStarted, nil))
	if _, open := <-ch; open {
		t.Fatal("channel still delivers after unsubscribe")
	}
}

func TestTracker_FullSubscriberDropsInsteadOfBlocking(t *testing.T) {
	clock := newFakeClock()
	tracker := newTestTracker(clock)
	_, unsubscribe := tracker.Subscribe() // never read
	defer unsubscribe()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < subscriberBuffer*3; i++ {
			tracker.HandleEvent(taskEvent(clock, workspace.EventTaskThinking, nil))
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("HandleEvent blocked on a full subscriber")
	}
}

func TestTracker_ThroughTheRealBus(t *testing.T) {
	bus := workspace.NewEventBus(10, 10)
	baseline := bus.SubscriberCount()
	tracker := NewTracker(bus)
	tracker.Start()

	ch, unsubscribe := tracker.Subscribe()
	defer unsubscribe()
	bus.Publish(workspace.NewTaskEvent(workspace.EventTaskStarted, "ws-9", "task-9", "Ada", nil))

	select {
	case msg := <-ch:
		ev := msg.Payload.(ActivityEvent)
		if ev.Phase != PhaseStarted || ev.WorkspaceID != "ws-9" || ev.AgentName != "Ada" {
			t.Fatalf("event = %+v, want Ada started in ws-9", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no message from the bus")
	}

	tracker.Stop()
	if bus.SubscriberCount() != baseline {
		t.Fatalf("bus SubscriberCount = %d, want baseline %d after Stop", bus.SubscriberCount(), baseline)
	}
	if _, open := <-ch; open {
		t.Fatal("Stop left a subscriber channel open")
	}
	if tracker.SubscriberCount() != 0 {
		t.Fatalf("SubscriberCount = %d after Stop, want 0", tracker.SubscriberCount())
	}
}
