package utilitytelemetry

import (
	"testing"
	"time"
)

func TestTracker_SnapshotAggregatesMetrics(t *testing.T) {
	tracker := NewTracker(16)

	tracker.RecordRouteDecision("utility_direct", "matched utility intent")
	tracker.RecordDelegationEvent("workspace_task", "needs workspace context", "Ori")
	tracker.RecordToolInvocation("time", "system-clock")
	tracker.RecordToolResult("time", "system-clock", true, 40*time.Millisecond, "")
	tracker.RecordToolInvocation("weather", "open-meteo.com")
	tracker.RecordToolResult("weather", "open-meteo.com", false, 85*time.Millisecond, "provider timeout")

	snapshot := tracker.Snapshot()

	if snapshot.Totals.Calls != 2 {
		t.Fatalf("expected 2 calls, got %d", snapshot.Totals.Calls)
	}
	if snapshot.Totals.Successes != 1 {
		t.Fatalf("expected 1 success, got %d", snapshot.Totals.Successes)
	}
	if snapshot.Totals.Failures != 1 {
		t.Fatalf("expected 1 failure, got %d", snapshot.Totals.Failures)
	}
	if snapshot.EventCounts[eventTypeRoute] != 1 {
		t.Fatalf("expected 1 route decision event, got %d", snapshot.EventCounts[eventTypeRoute])
	}
	if snapshot.EventCounts[eventTypeDelegate] != 1 {
		t.Fatalf("expected 1 delegation event, got %d", snapshot.EventCounts[eventTypeDelegate])
	}
	if snapshot.EventCounts[eventTypeInvoke] != 2 {
		t.Fatalf("expected 2 tool invocation events, got %d", snapshot.EventCounts[eventTypeInvoke])
	}
	if snapshot.EventCounts[eventTypeResult] != 2 {
		t.Fatalf("expected 2 tool result events, got %d", snapshot.EventCounts[eventTypeResult])
	}
	if snapshot.RouteCounts["utility_direct"] != 1 {
		t.Fatalf("expected route count for utility_direct, got %d", snapshot.RouteCounts["utility_direct"])
	}

	timeTool, ok := snapshot.Tools["time"]
	if !ok {
		t.Fatalf("expected time tool metrics to exist")
	}
	if timeTool.Calls != 1 || timeTool.Successes != 1 || timeTool.Failures != 0 {
		t.Fatalf("unexpected time tool metrics: %+v", timeTool)
	}
	if timeTool.Provider != "system-clock" {
		t.Fatalf("expected provider system-clock, got %q", timeTool.Provider)
	}

	weatherTool, ok := snapshot.Tools["weather"]
	if !ok {
		t.Fatalf("expected weather tool metrics to exist")
	}
	if weatherTool.Calls != 1 || weatherTool.Successes != 0 || weatherTool.Failures != 1 {
		t.Fatalf("unexpected weather tool metrics: %+v", weatherTool)
	}
	if weatherTool.LastError != "provider timeout" {
		t.Fatalf("expected last error to be provider timeout, got %q", weatherTool.LastError)
	}
}

func TestTracker_EventBufferBounded(t *testing.T) {
	tracker := NewTracker(3)
	for i := 0; i < 5; i++ {
		tracker.RecordRouteDecision("assistant_chat", "event")
	}
	snapshot := tracker.Snapshot()
	if len(snapshot.RecentEvents) != 3 {
		t.Fatalf("expected bounded event count 3, got %d", len(snapshot.RecentEvents))
	}
}
