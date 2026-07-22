package calendar

import (
	"sort"
	"strings"
	"time"
)

// conflictEligible reports whether an event participates in conflict/next-
// meeting reasoning at all. An all-day block, a canceled event, or one the
// user declined isn't a real scheduling commitment, so it's excluded from
// both CountConflicts and NextMeeting rather than silently inflating either.
func conflictEligible(e Event) bool {
	if e.AllDay || e.Canceled {
		return false
	}
	return !strings.EqualFold(strings.TrimSpace(e.ResponseStatus), "declined")
}

func parseEventInstant(s string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(s))
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// CountConflicts returns the number of eligible events whose scheduled
// [start,end) window overlaps at least one other eligible event's window.
// Deterministic: the result depends only on the input events, never on their
// order in the slice.
func CountConflicts(events []Event) int {
	type window struct{ start, end time.Time }
	windows := make([]window, 0, len(events))
	for _, e := range events {
		if !conflictEligible(e) {
			continue
		}
		start, ok1 := parseEventInstant(e.StartTime)
		end, ok2 := parseEventInstant(e.EndTime)
		if !ok1 || !ok2 || !end.After(start) {
			continue
		}
		windows = append(windows, window{start, end})
	}
	sort.Slice(windows, func(i, j int) bool { return windows[i].start.Before(windows[j].start) })

	conflicted := 0
	for i := range windows {
		for j := range windows {
			if i == j {
				continue
			}
			if windows[i].start.Before(windows[j].end) && windows[j].start.Before(windows[i].end) {
				conflicted++
				break
			}
		}
	}
	return conflicted
}

// NextMeeting returns the earliest eligible event starting at or after now,
// or nil if there is none. "Eligible" matches CountConflicts (no all-day,
// canceled, or declined events) plus FR50's "timed" requirement, which
// all-day exclusion already satisfies.
func NextMeeting(events []Event, now time.Time) *Event {
	var best *Event
	var bestStart time.Time
	for i := range events {
		e := events[i]
		if !conflictEligible(e) {
			continue
		}
		start, ok := parseEventInstant(e.StartTime)
		if !ok || start.Before(now) {
			continue
		}
		if best == nil || start.Before(bestStart) {
			cp := e
			best = &cp
			bestStart = start
		}
	}
	return best
}
