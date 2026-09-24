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

// eligibleWindow is one conflict-eligible event's parsed [start,end) window,
// keyed back to its position in the caller's slice.
type eligibleWindow struct {
	index      int
	start, end time.Time
}

// eligibleWindows returns the windows of every conflict-eligible event with a
// well-formed, positive-length window, ordered by start (then end, then input
// position, so ties never depend on sort stability).
func eligibleWindows(events []Event) []eligibleWindow {
	windows := make([]eligibleWindow, 0, len(events))
	for i, e := range events {
		if !conflictEligible(e) {
			continue
		}
		start, ok1 := parseEventInstant(e.StartTime)
		end, ok2 := parseEventInstant(e.EndTime)
		if !ok1 || !ok2 || !end.After(start) {
			continue
		}
		windows = append(windows, eligibleWindow{index: i, start: start, end: end})
	}
	sort.Slice(windows, func(i, j int) bool {
		a, b := windows[i], windows[j]
		if !a.start.Equal(b.start) {
			return a.start.Before(b.start)
		}
		if !a.end.Equal(b.end) {
			return a.end.Before(b.end)
		}
		return a.index < b.index
	})
	return windows
}

// ConflictFlags reports, for each event in the input (same index), whether it
// overlaps at least one other eligible event. The eligibility and overlap rule
// are CountConflicts', so the number of true flags always equals
// CountConflicts(events).
func ConflictFlags(events []Event) []bool {
	flags := make([]bool, len(events))
	windows := eligibleWindows(events)
	for i := range windows {
		for j := range windows {
			if i == j {
				continue
			}
			if windows[i].start.Before(windows[j].end) && windows[j].start.Before(windows[i].end) {
				flags[windows[i].index] = true
				break
			}
		}
	}
	return flags
}

// BackToBackFlags reports, for each event in the input (same index), whether it
// is part of a back-to-back pair: an eligible event that starts no more than
// maxGap after the eligible schedule before it frees up. "Frees up" is the
// latest end among every earlier-starting eligible event, so a short meeting
// nested inside a long one never hides the long one's hand-off. Both the event
// and the one that kept the user busy until then are flagged. An event that
// starts before that point overlaps it instead; that is a conflict
// (ConflictFlags), not back-to-back. A negative maxGap is treated as zero.
func BackToBackFlags(events []Event, maxGap time.Duration) []bool {
	if maxGap < 0 {
		maxGap = 0
	}
	flags := make([]bool, len(events))
	windows := eligibleWindows(events)
	busyUntil, busyOwner := time.Time{}, -1
	for _, w := range windows {
		if busyOwner >= 0 && !w.start.Before(busyUntil) && w.start.Sub(busyUntil) <= maxGap {
			flags[w.index] = true
			flags[busyOwner] = true
		}
		if busyOwner < 0 || w.end.After(busyUntil) {
			busyUntil, busyOwner = w.end, w.index
		}
	}
	return flags
}
