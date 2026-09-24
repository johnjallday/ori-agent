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

// at builds a 2026-01-01 UTC instant from an "HH:MM" clock time.
func at(clock string) string { return "2026-01-01T" + clock + ":00Z" }

func withEvent(e Event, edit func(*Event)) Event {
	edit(&e)
	return e
}

func TestConflictFlags(t *testing.T) {
	cases := []struct {
		name   string
		events []Event
		want   []bool
	}{
		{name: "no events", events: nil, want: []bool{}},
		{
			name: "back-to-back is not an overlap",
			events: []Event{
				mkEvent("a", at("09:00"), at("10:00")),
				mkEvent("b", at("10:00"), at("11:00")),
			},
			want: []bool{false, false},
		},
		{
			name: "overlapping pair flags both, input order kept",
			events: []Event{
				mkEvent("late", at("09:30"), at("10:30")),
				mkEvent("free", at("12:00"), at("13:00")),
				mkEvent("early", at("09:00"), at("10:00")),
			},
			want: []bool{true, false, true},
		},
		{
			name: "all-day, canceled, declined, and malformed events never conflict",
			events: []Event{
				mkEvent("a", at("09:00"), at("10:00")),
				withEvent(mkEvent("allday", at("00:00"), "2026-01-02T00:00:00Z"), func(e *Event) { e.AllDay = true }),
				withEvent(mkEvent("canceled", at("09:00"), at("10:00")), func(e *Event) { e.Canceled = true }),
				withEvent(mkEvent("declined", at("09:00"), at("10:00")), func(e *Event) { e.ResponseStatus = "Declined" }),
				mkEvent("malformed", "not-a-time", at("10:00")),
				mkEvent("zero-length", at("09:30"), at("09:30")),
			},
			want: []bool{false, false, false, false, false, false},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ConflictFlags(tc.events)
			if len(got) != len(tc.want) {
				t.Fatalf("len = %d, want %d", len(got), len(tc.want))
			}
			flagged := 0
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("flags = %v, want %v", got, tc.want)
				}
				if got[i] {
					flagged++
				}
			}
			if count := CountConflicts(tc.events); count != flagged {
				t.Fatalf("CountConflicts = %d but %d events flagged; the two must agree", count, flagged)
			}
		})
	}
}

func TestBackToBackFlags(t *testing.T) {
	gap := 5 * time.Minute
	cases := []struct {
		name   string
		events []Event
		maxGap time.Duration
		want   []bool
	}{
		{name: "no events", events: nil, maxGap: gap, want: []bool{}},
		{
			name: "touching pair flags both",
			events: []Event{
				mkEvent("b", at("10:00"), at("11:00")),
				mkEvent("a", at("09:00"), at("10:00")),
			},
			maxGap: gap,
			want:   []bool{true, true},
		},
		{
			name: "gap within the bound counts, gap beyond it does not",
			events: []Event{
				mkEvent("a", at("09:00"), at("10:00")),
				mkEvent("b", at("10:05"), at("11:00")),
				mkEvent("c", at("11:06"), at("12:00")),
			},
			maxGap: gap,
			want:   []bool{true, true, false},
		},
		{
			name: "an overlap is a conflict, not back-to-back",
			events: []Event{
				mkEvent("a", at("09:00"), at("10:00")),
				mkEvent("b", at("09:30"), at("10:30")),
			},
			maxGap: gap,
			want:   []bool{false, false},
		},
		{
			name: "overlapping pair hands off to the next meeting from the later end",
			events: []Event{
				mkEvent("design", at("11:00"), at("12:00")),
				mkEvent("budget", at("11:30"), at("12:30")),
				mkEvent("lunch", at("12:30"), at("13:30")),
			},
			maxGap: gap,
			want:   []bool{false, true, true},
		},
		{
			name: "a nested short meeting does not hide the long one's hand-off",
			events: []Event{
				mkEvent("long", at("09:00"), at("12:00")),
				mkEvent("nested", at("10:00"), at("10:30")),
				mkEvent("next", at("12:00"), at("13:00")),
			},
			maxGap: gap,
			want:   []bool{true, false, true},
		},
		{
			name: "ineligible events neither flag nor keep the user busy",
			events: []Event{
				mkEvent("a", at("09:00"), at("10:00")),
				withEvent(mkEvent("declined", at("10:00"), at("11:00")), func(e *Event) { e.ResponseStatus = "declined" }),
				mkEvent("c", at("11:00"), at("12:00")),
			},
			maxGap: gap,
			want:   []bool{false, false, false},
		},
		{
			name: "negative gap is treated as zero",
			events: []Event{
				mkEvent("a", at("09:00"), at("10:00")),
				mkEvent("b", at("10:00"), at("11:00")),
				mkEvent("c", at("11:01"), at("12:00")),
			},
			maxGap: -time.Minute,
			want:   []bool{true, true, false},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := BackToBackFlags(tc.events, tc.maxGap)
			if len(got) != len(tc.want) {
				t.Fatalf("len = %d, want %d", len(got), len(tc.want))
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("flags = %v, want %v", got, tc.want)
				}
			}
		})
	}
}
