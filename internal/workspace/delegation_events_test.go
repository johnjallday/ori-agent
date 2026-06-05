package workspace

import (
	"context"
	"testing"
	"time"
)

type fakeDelegationTelemetry struct {
	calls []string // "mode|target"
}

func (f *fakeDelegationTelemetry) RecordDelegationEvent(mode, _, target string) {
	f.calls = append(f.calls, mode+"|"+target)
}

func eventTypesInHistory(bus *EventBus) map[EventType]bool {
	seen := map[EventType]bool{}
	for _, e := range bus.GetHistory(nil, 100) {
		seen[e.Type] = true
	}
	return seen
}

func TestDelegationLoopEmitsLifecycleEvents(t *testing.T) {
	store := NewInMemoryStore()
	if err := store.Save(loopWorkspace(Task{ID: "sub1", WorkspaceID: "ws", To: "Writer", Description: "w"})); err != nil {
		t.Fatalf("save: %v", err)
	}
	bus := NewEventBus(10, 100)
	tel := &fakeDelegationTelemetry{}

	loop := NewDelegationLoop(store,
		&fakeExecutor{results: map[string]string{"sub1": "done"}},
		&fakeAdapter{steps: []CoordinatorAdaptResult{
			{DelegatedTaskIDs: []string{"sub1"}},
			{Resolved: true},
		}},
		DelegationCaps{})
	loop.SetEventBus(bus)
	loop.SetTelemetry(tel)

	if _, err := loop.Run(context.Background(), "ws", Task{ID: "f1", Description: "do x"}, DelegationTrigger{Trigger: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	seen := eventTypesInHistory(bus)
	if !seen[EventDelegationStarted] || !seen[EventDelegationCompleted] {
		t.Fatalf("expected started+completed events, got %v", seen)
	}
	if !seen[EventTaskAssigned] {
		t.Fatalf("expected a task.assigned event for the delegated subtask, got %v", seen)
	}
	if len(tel.calls) != 1 || tel.calls[0] != "dynamic_delegation|Writer" {
		t.Fatalf("expected one delegation telemetry record for Writer, got %v", tel.calls)
	}
}

func TestDelegationLoopEmitsCapHit(t *testing.T) {
	store := NewInMemoryStore()
	if err := store.Save(loopWorkspace()); err != nil {
		t.Fatalf("save: %v", err)
	}
	bus := NewEventBus(10, 100)
	loop := NewDelegationLoop(store, &fakeExecutor{}, &fakeAdapter{}, // never resolves
		DelegationCaps{MaxIterations: 1, Timeout: time.Minute})
	loop.SetEventBus(bus)

	if _, err := loop.Run(context.Background(), "ws", Task{ID: "f1", Description: "do x"}, DelegationTrigger{Trigger: true}); err == nil {
		t.Fatal("expected cap-hit error")
	}
	if !eventTypesInHistory(bus)[EventDelegationCapHit] {
		t.Fatal("expected a delegation.cap_hit event")
	}
}
