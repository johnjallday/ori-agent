package calendar

import (
	"testing"
	"time"
)

func mkEvent(id, start, end string) Event {
	return Event{ID: id, Title: id, StartTime: start, EndTime: end}
}

func mustParseTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("failed to parse time %q: %v", s, err)
	}
	return ts
}

func TestCountConflicts_NoEventsNoConflicts(t *testing.T) {
	if got := CountConflicts(nil); got != 0 {
		t.Fatalf("got %d, want 0", got)
	}
}

func TestCountConflicts_NonOverlappingEventsAreNotConflicts(t *testing.T) {
	events := []Event{
		mkEvent("a", "2026-01-01T09:00:00Z", "2026-01-01T10:00:00Z"),
		mkEvent("b", "2026-01-01T10:00:00Z", "2026-01-01T11:00:00Z"),
	}
	if got := CountConflicts(events); got != 0 {
		t.Fatalf("got %d, want 0 (back-to-back events do not overlap)", got)
	}
}

func TestCountConflicts_OverlappingEventsAreBothCounted(t *testing.T) {
	events := []Event{
		mkEvent("a", "2026-01-01T09:00:00Z", "2026-01-01T10:00:00Z"),
		mkEvent("b", "2026-01-01T09:30:00Z", "2026-01-01T10:30:00Z"),
	}
	if got := CountConflicts(events); got != 2 {
		t.Fatalf("got %d, want 2", got)
	}
}

func TestCountConflicts_DeterministicRegardlessOfInputOrder(t *testing.T) {
	forward := []Event{
		mkEvent("a", "2026-01-01T09:00:00Z", "2026-01-01T10:00:00Z"),
		mkEvent("b", "2026-01-01T09:30:00Z", "2026-01-01T10:30:00Z"),
		mkEvent("c", "2026-01-01T13:00:00Z", "2026-01-01T14:00:00Z"),
	}
	reversed := []Event{forward[2], forward[1], forward[0]}
	got1 := CountConflicts(forward)
	got2 := CountConflicts(reversed)
	if got1 != got2 {
		t.Fatalf("order dependent: forward=%d reversed=%d", got1, got2)
	}
	if got1 != 2 {
		t.Fatalf("got %d, want 2", got1)
	}
}

func TestCountConflicts_ExcludesAllDayEvents(t *testing.T) {
	allDay := mkEvent("a", "2026-01-01T00:00:00Z", "2026-01-02T00:00:00Z")
	allDay.AllDay = true
	timed := mkEvent("b", "2026-01-01T09:00:00Z", "2026-01-01T10:00:00Z")
	if got := CountConflicts([]Event{allDay, timed}); got != 0 {
		t.Fatalf("got %d, want 0 (all-day events are excluded)", got)
	}
}

func TestCountConflicts_ExcludesDeclinedEvents(t *testing.T) {
	a := mkEvent("a", "2026-01-01T09:00:00Z", "2026-01-01T10:00:00Z")
	a.ResponseStatus = "declined"
	b := mkEvent("b", "2026-01-01T09:30:00Z", "2026-01-01T10:30:00Z")
	if got := CountConflicts([]Event{a, b}); got != 0 {
		t.Fatalf("got %d, want 0 (declined events are excluded)", got)
	}
}

func TestCountConflicts_ExcludesCanceledEvents(t *testing.T) {
	a := mkEvent("a", "2026-01-01T09:00:00Z", "2026-01-01T10:00:00Z")
	a.Canceled = true
	b := mkEvent("b", "2026-01-01T09:30:00Z", "2026-01-01T10:30:00Z")
	if got := CountConflicts([]Event{a, b}); got != 0 {
		t.Fatalf("got %d, want 0 (canceled events are excluded)", got)
	}
}

func TestNextMeeting_ReturnsEarliestUpcomingEvent(t *testing.T) {
	now := mustParseTime(t, "2026-01-01T08:00:00Z")
	events := []Event{
		mkEvent("late", "2026-01-01T15:00:00Z", "2026-01-01T16:00:00Z"),
		mkEvent("early", "2026-01-01T09:00:00Z", "2026-01-01T10:00:00Z"),
	}
	got := NextMeeting(events, now)
	if got == nil || got.ID != "early" {
		t.Fatalf("got %+v, want early", got)
	}
}

func TestNextMeeting_ExcludesPastEvents(t *testing.T) {
	now := mustParseTime(t, "2026-01-01T12:00:00Z")
	events := []Event{
		mkEvent("past", "2026-01-01T09:00:00Z", "2026-01-01T10:00:00Z"),
	}
	if got := NextMeeting(events, now); got != nil {
		t.Fatalf("got %+v, want nil", got)
	}
}

func TestNextMeeting_ExcludesDeclinedAndAllDay(t *testing.T) {
	now := mustParseTime(t, "2026-01-01T08:00:00Z")
	declined := mkEvent("declined", "2026-01-01T09:00:00Z", "2026-01-01T10:00:00Z")
	declined.ResponseStatus = "declined"
	allDay := mkEvent("allday", "2026-01-01T00:00:00Z", "2026-01-02T00:00:00Z")
	allDay.AllDay = true
	real := mkEvent("real", "2026-01-01T11:00:00Z", "2026-01-01T12:00:00Z")
	got := NextMeeting([]Event{declined, allDay, real}, now)
	if got == nil || got.ID != "real" {
		t.Fatalf("got %+v, want real", got)
	}
}

func TestNextMeeting_NoEligibleEventsReturnsNil(t *testing.T) {
	now := mustParseTime(t, "2026-01-01T08:00:00Z")
	if got := NextMeeting(nil, now); got != nil {
		t.Fatalf("got %+v, want nil", got)
	}
}
