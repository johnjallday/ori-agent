// Today's meetings for HQ surfaces (Issue #533): the one bounded, read-only
// projection that Personal Assistant Today and the Daily Brief share. It goes
// through the same FR49 resolver and FR25 gateway checkpoint as the Home
// portal, so the Calendar Ops workspace keeps ownership of the connection and
// HQ only ever sees this projection.
//
// What a meeting carries is deliberately small: identity, a truncated title
// and location, times, flags, and the meeting-prep status. The description,
// attendees, and conference/source links are never copied, and a private
// event keeps neither its title nor its location, matching what the Calendar
// console itself shows for one.
package calendarhttp

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/calendar"
	"github.com/johnjallday/ori-agent/internal/meetingprep"
)

// TodayAgenda states.
const (
	// TodayAgendaNotConnected: the user has no Calendar Ops workspace this
	// user can read.
	TodayAgendaNotConnected = "not_connected"
	// TodayAgendaNeedsSetup: a Calendar Ops workspace exists but its
	// connector is not ready (TodayAgenda.SetupState says why).
	TodayAgendaNeedsSetup = "needs_setup"
	// TodayAgendaReady: the connector was read (possibly partially).
	TodayAgendaReady = "ready"
)

// TodayMeeting prep statuses. The empty string means no prep has been started
// for the meeting (or the meeting cannot be prepared, such as a private one).
const (
	TodayPrepPending = "pending"
	TodayPrepReady   = "ready"
	// TodayPrepStale: a prep note exists, but the meeting changed after it
	// was written.
	TodayPrepStale  = "stale"
	TodayPrepFailed = "failed"
)

const (
	todayMeetingTitleMaxRunes    = 200
	todayMeetingLocationMaxRunes = 100
	// todayBackToBackGap is the largest gap between one meeting ending and the
	// next starting that still reads as back-to-back.
	todayBackToBackGap = 5 * time.Minute
	// maxTodayAgendaMeetings caps what any caller can ask for.
	maxTodayAgendaMeetings = 25
)

// TodayMeeting is one meeting on today's agenda. Title and Location are
// untrusted third-party text: sanitized upstream, truncated here, and always
// data, never instructions.
type TodayMeeting struct {
	ID             string    `json:"id"`
	CalendarID     string    `json:"calendar_id"`
	Title          string    `json:"title,omitempty"`
	StartTime      time.Time `json:"start_time"`
	EndTime        time.Time `json:"end_time"`
	AllDay         bool      `json:"all_day,omitempty"`
	Private        bool      `json:"private,omitempty"`
	ResponseStatus string    `json:"response_status,omitempty"`
	Location       string    `json:"location,omitempty"`
	Conflict       bool      `json:"conflict,omitempty"`
	BackToBack     bool      `json:"back_to_back,omitempty"`
	PrepStatus     string    `json:"prep_status,omitempty"`
	PrepNoteID     string    `json:"prep_note_id,omitempty"`
}

// TodayAgenda is the result of Handler.TodayAgenda.
type TodayAgenda struct {
	State         string              `json:"state"`
	SetupState    calendar.SetupState `json:"setup_state,omitempty"`
	WorkspaceID   string              `json:"workspace_id,omitempty"`
	WorkspaceSlug string              `json:"workspace_slug,omitempty"`
	WorkspaceName string              `json:"workspace_name,omitempty"`
	// TimeZone is the zone "today" was computed in: the binding's display
	// timezone, else the caller's fallback, else UTC.
	TimeZone string         `json:"time_zone,omitempty"`
	Meetings []TodayMeeting `json:"meetings"`
	// Total is how many meetings today has before the caller's limit.
	Total int `json:"total"`
	// Partial reports that at least one selected calendar could not be read;
	// Meetings holds what the others returned.
	Partial bool `json:"partial,omitempty"`
}

// TodayAgenda reads today's meetings for userID across the selected calendars
// of their active Calendar Ops workspace. It never writes and never changes a
// connector's scope.
//
// Meetings are the timed and all-day events that overlap today's window,
// sorted by start, with canceled and declined events removed and a meeting
// shared across selected calendars listed once (its event id is its identity). Conflict and
// back-to-back flags are computed over the whole day before the list is cut to
// limit, so a meeting overlapping one past the cut is still flagged.
//
// A missing or unreadable workspace is a state, not an error. The error
// return is reserved for failures the caller must report as a gap: an
// internal failure resolving the gateway, or ctx ending before the read
// finished (callers bound this read with a timeout and rely on getting
// ctx.Err() back rather than a silently empty agenda).
func (h *Handler) TodayAgenda(ctx context.Context, userID, fallbackTZ string, limit int) (TodayAgenda, error) {
	out := TodayAgenda{State: TodayAgendaNotConnected, Meetings: []TodayMeeting{}}
	if h == nil {
		return out, nil
	}
	if limit <= 0 || limit > maxTodayAgendaMeetings {
		limit = maxTodayAgendaMeetings
	}

	ws, ok := h.ActiveWorkspace(ctx, userID)
	if !ok {
		return out, ctx.Err()
	}
	out.WorkspaceID, out.WorkspaceSlug, out.WorkspaceName = ws.ID, ws.FolderSlug, ws.Name

	gw, gerr := h.resolveGateway(ctx, ws.ID)
	if gerr != nil {
		switch gerr.status {
		case http.StatusConflict, http.StatusServiceUnavailable:
			out.State = TodayAgendaNeedsSetup
			out.SetupState = calendar.SetupState(gerr.code)
			return out, nil
		case http.StatusNotFound, http.StatusForbidden:
			// The workspace vanished or changed owner between the two reads.
			return TodayAgenda{State: TodayAgendaNotConnected, Meetings: []TodayMeeting{}}, nil
		default:
			return out, gerr
		}
	}
	out.State = TodayAgendaReady

	settings := calendar.ReadBindingSettings(gw.Binding.Config)
	tz, loc := resolveTodayZone(settings.DisplayTimeZone, fallbackTZ)
	out.TimeZone = tz
	start, end := todayWindow(tz, nowUTC())

	events, partial := h.loadWindowEvents(ctx, gw, start, end)
	if err := ctx.Err(); err != nil {
		return out, err
	}
	out.Partial = partial

	type dayEvent struct {
		event      calendar.Event
		start, end time.Time
	}
	day := make([]dayEvent, 0, len(events))
	seen := make(map[string]bool, len(events))
	for _, evt := range events {
		if evt.Canceled || strings.EqualFold(strings.TrimSpace(evt.ResponseStatus), "declined") {
			continue
		}
		// One meeting on two selected calendars (a shared event) arrives
		// twice with the same id. Keep the first calendar's copy: showing it
		// twice would also flag it as conflicting with itself.
		if seen[evt.ID] {
			continue
		}
		evtStart, err1 := time.Parse(time.RFC3339, evt.StartTime)
		evtEnd, err2 := time.Parse(time.RFC3339, evt.EndTime)
		if err1 != nil || err2 != nil || evtEnd.Before(evtStart) {
			continue
		}
		// Connectors are asked for today's window but are not trusted to
		// honour it: anything that does not overlap today is dropped here.
		if !evtEnd.After(start) || !evtStart.Before(end) {
			continue
		}
		seen[evt.ID] = true
		day = append(day, dayEvent{event: evt, start: evtStart, end: evtEnd})
	}
	sort.SliceStable(day, func(i, j int) bool {
		a, b := day[i], day[j]
		switch {
		case !a.start.Equal(b.start):
			return a.start.Before(b.start)
		case !a.end.Equal(b.end):
			return a.end.Before(b.end)
		case a.event.CalendarID != b.event.CalendarID:
			return a.event.CalendarID < b.event.CalendarID
		default:
			return a.event.ID < b.event.ID
		}
	})

	flat := make([]calendar.Event, len(day))
	for i := range day {
		flat[i] = day[i].event
	}
	conflicts := calendar.ConflictFlags(flat)
	backToBack := calendar.BackToBackFlags(flat, todayBackToBackGap)

	out.Total = len(day)
	if len(day) > limit {
		day = day[:limit]
	}
	for i, d := range day {
		evt := d.event
		meeting := TodayMeeting{
			ID:             evt.ID,
			CalendarID:     evt.CalendarID,
			StartTime:      d.start.In(loc),
			EndTime:        d.end.In(loc),
			AllDay:         evt.AllDay,
			Private:        evt.Private,
			ResponseStatus: evt.ResponseStatus,
			Conflict:       conflicts[i],
			BackToBack:     backToBack[i],
		}
		if !evt.Private {
			meeting.Title = truncateRunes(evt.Title, todayMeetingTitleMaxRunes)
			meeting.Location = truncateRunes(evt.Location, todayMeetingLocationMaxRunes)
			meeting.PrepStatus, meeting.PrepNoteID = h.todayPrepStatus(ctx, gw, evt)
		}
		out.Meetings = append(out.Meetings, meeting)
	}
	return out, nil
}

// todayPrepStatus reports the meeting-prep state for evt, keyed exactly as
// Prepare keys it. A missing store, a lookup error, or no link all read as ""
// (not started): a prep lookup must never fail the agenda.
func (h *Handler) todayPrepStatus(ctx context.Context, gw *gatewayContext, evt calendar.Event) (status, noteID string) {
	if h.meetingPreps == nil {
		return "", ""
	}
	key := meetingprep.Key{
		WorkspaceID: gw.Workspace.ID,
		BindingID:   gw.Binding.ID,
		CalendarID:  strings.TrimSpace(evt.CalendarID),
		EventID:     evt.ID,
	}
	if key.CalendarID == "" {
		key.CalendarID = "default"
	}
	link, err := h.meetingPreps.GetByKey(ctx, key)
	if err != nil || link == nil {
		return "", ""
	}
	switch link.Status {
	case meetingprep.StatusPending:
		return TodayPrepPending, ""
	case meetingprep.StatusFailed:
		return TodayPrepFailed, ""
	case meetingprep.StatusReady:
		// The same fields Prepare fingerprints when it saves the note. The
		// description takes part in the hash only; it never leaves here.
		current := meetingprep.Fingerprint(meetingprep.FingerprintInput{
			Title: evt.Title, StartTime: evt.StartTime, EndTime: evt.EndTime,
			Location: evt.Location, Description: evt.Description,
		})
		if current != link.EventFingerprint {
			return TodayPrepStale, link.NoteID
		}
		return TodayPrepReady, link.NoteID
	default:
		return "", ""
	}
}

// resolveTodayZone picks the zone "today" is computed in: the binding's
// display timezone, else fallback, else UTC. Invalid names are skipped.
func resolveTodayZone(displayTZ, fallbackTZ string) (string, *time.Location) {
	for _, name := range []string{displayTZ, fallbackTZ} {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if loc, err := time.LoadLocation(name); err == nil {
			return name, loc
		}
	}
	return "UTC", time.UTC
}

// truncateRunes cuts s to at most max runes, marking a cut with an ellipsis.
func truncateRunes(s string, max int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return strings.TrimSpace(string(runes[:max-1])) + "…"
}
