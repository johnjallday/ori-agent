package dailybrief

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Today's meetings in the Daily Brief (Issue #533).
//
// This deliberately reverses FR54 ("Calendar Ops must never add a live call to
// Daily Brief generation in this release"). The reversal is bounded, and
// calendar_ops_boundary_test.go pins the bound:
//   - one read per generation, through an optional CalendarSource that the
//     server bounds in time (8 seconds, the email source's bound);
//   - today only, at most maxCalendarEventsPerBrief meetings;
//   - per meeting only what CalendarEventSnapshot carries: never a
//     description, an attendee, or a conference or source link;
//   - titles and locations are untrusted third-party text, labelled so in the
//     synthesis prompt, exactly like email subjects;
//   - read-only, through the Calendar Ops workspace's existing connection and
//     scope. Nothing here can write to a calendar.

// maxCalendarEventsPerBrief bounds how many of today's meetings one brief
// carries.
const maxCalendarEventsPerBrief = 8

// Bounds on the untrusted text a meeting carries into the brief. The source
// truncates first; these hold even if it does not.
const (
	maxCalendarTitleRunes    = 200
	maxCalendarLocationRunes = 100
)

// CalendarEventSnapshot is one of today's meetings as the brief sees it. Its
// field list is pinned by TestCalendarContractIsBounded: widening it is a
// contract change, not a refactor.
type CalendarEventSnapshot struct {
	// Ref is a calendar_event ref: WorkspaceID is the Calendar Ops workspace,
	// CalendarID the calendar the meeting was read from.
	Ref SourceRef
	// Title and Location are untrusted. A private meeting has neither.
	Title      string
	Location   string
	StartTime  time.Time
	EndTime    time.Time
	AllDay     bool
	Private    bool
	Conflict   bool
	BackToBack bool
	// PrepStatus is the meeting-prep state: "", pending, ready, stale, failed.
	PrepStatus string
}

// CalendarSource is the narrow calendar contract the brief needs: today's
// meetings for the user, read through the Calendar Ops workspace that owns the
// connection. Canceled and declined meetings are the source's to exclude.
// fallbackTZ is the brief's own timezone, used when the calendar sets none.
//
// It distinguishes five outcomes, extending MailboxSource's three:
//   - (events, nil): a healthy read, possibly empty — a real "no meetings".
//   - (nil, ErrCalendarNotConfigured): no calendar was ever set up. NOT a
//     gap: the brief shows no calendar section at all.
//   - (nil, ErrCalendarNeedsSetup): the user set up a calendar, but its
//     connection is not ready (unfinished, signed out, or unavailable). The
//     brief names that gap rather than implying a meeting-free day.
//   - (events, ErrCalendarPartialRead): some selected calendars failed; the
//     events from the rest are valid and kept, and the brief names a gap.
//   - (nil, other error): the calendar could not be read (including a
//     timeout) — the brief names a gap and every other source is unaffected.
type CalendarSource interface {
	BriefCalendarEvents(ctx context.Context, userID, fallbackTZ string) ([]CalendarEventSnapshot, error)
}

// ErrCalendarNotConfigured signals that the user never set up a calendar, so
// the brief has no calendar section and no gap.
var ErrCalendarNotConfigured = errors.New("dailybrief: no calendar is connected")

// ErrCalendarNeedsSetup signals a calendar the user set up whose connection is
// not ready, so today's meetings are unknown and the brief says so.
var ErrCalendarNeedsSetup = errors.New("dailybrief: the calendar connection needs attention")

// ErrCalendarPartialRead signals that some selected calendars could not be
// read; the events returned alongside it are valid.
var ErrCalendarPartialRead = errors.New("dailybrief: some calendars could not be read")

// Gap text for the calendar failures.
const (
	calendarReadGap       = "today's meetings could not be read"
	calendarPartialGap    = "some calendars could not be read"
	calendarNeedsSetupGap = "the calendar connection needs attention, so today's meetings are not listed"
)

// collectCalendar reads today's meetings into snap. A calendar failure only
// ever adds a gap; it never touches another source.
func collectCalendar(ctx context.Context, source CalendarSource, cfg Config, userID string, snap *Snapshot) {
	if source == nil {
		return
	}
	events, err := source.BriefCalendarEvents(ctx, userID, cfg.Timezone)
	switch {
	case errors.Is(err, ErrCalendarNotConfigured):
		return
	case errors.Is(err, ErrCalendarNeedsSetup):
		snap.Gaps = append(snap.Gaps, calendarNeedsSetupGap)
		return
	case errors.Is(err, ErrCalendarPartialRead):
		snap.Gaps = append(snap.Gaps, calendarPartialGap)
	case err != nil:
		snap.Gaps = append(snap.Gaps, calendarReadGap)
		return
	}
	snap.CalendarConnected = true
	snap.CalendarEvents, snap.CalendarMeetingCount, snap.CalendarOverlapCount = boundedCalendarEvents(events, snap.GeneratedAt)
}

// boundedCalendarEvents keeps well-formed calendar_event snapshots, one per
// ref, in start order, capped, with their untrusted text re-bounded. When the
// day has more meetings than the cap, meetings that have already ended give
// way first: a brief refreshed mid-afternoon must list what is still ahead.
// count and overlaps describe the whole day, before the cap.
func boundedCalendarEvents(events []CalendarEventSnapshot, now time.Time) (kept []CalendarEventSnapshot, count, overlaps int) {
	out := make([]CalendarEventSnapshot, 0, len(events))
	seen := make(map[string]bool, len(events))
	for _, evt := range events {
		evt.Ref.EntityType = EntityCalendarEvent
		if strings.TrimSpace(evt.Ref.EntityID) == "" || strings.TrimSpace(evt.Ref.WorkspaceID) == "" ||
			evt.StartTime.IsZero() || evt.EndTime.Before(evt.StartTime) || seen[evt.Ref.Key()] {
			continue
		}
		seen[evt.Ref.Key()] = true
		if evt.Private {
			evt.Title, evt.Location = "", ""
		}
		evt.Title = boundedRunes(evt.Title, maxCalendarTitleRunes)
		evt.Location = boundedRunes(evt.Location, maxCalendarLocationRunes)
		out = append(out, evt)
	}
	chronological := func(list []CalendarEventSnapshot) {
		sort.SliceStable(list, func(i, j int) bool {
			if !list[i].StartTime.Equal(list[j].StartTime) {
				return list[i].StartTime.Before(list[j].StartTime)
			}
			return list[i].EndTime.Before(list[j].EndTime)
		})
	}
	count = len(out)
	for _, evt := range out {
		if evt.Conflict {
			overlaps++
		}
	}
	if len(out) > maxCalendarEventsPerBrief {
		// Still-ahead meetings first, then the ones already over; each in
		// start order. The kept set is re-sorted chronologically below.
		sort.SliceStable(out, func(i, j int) bool {
			endedI, endedJ := !out[i].EndTime.After(now), !out[j].EndTime.After(now)
			if endedI != endedJ {
				return endedJ
			}
			return out[i].StartTime.Before(out[j].StartTime)
		})
		out = out[:maxCalendarEventsPerBrief]
	}
	chronological(out)
	return out, count, overlaps
}

// calendarDisplayTitle is what the brief calls a meeting when it has to name
// one: a private meeting is never named.
func calendarDisplayTitle(evt CalendarEventSnapshot) string {
	if evt.Private {
		return "Private event"
	}
	if title := strings.TrimSpace(evt.Title); title != "" {
		return title
	}
	return "Untitled event"
}

func boundedRunes(s string, limit int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	return strings.TrimSpace(string([]rune(s)[:limit-1])) + "…"
}
