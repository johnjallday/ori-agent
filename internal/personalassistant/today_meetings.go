package personalassistant

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/progression"
)

// Today's Meetings (Issue #533): what is on the user's calendar today, read
// through the Calendar Ops workspace that owns the connection. Today never
// holds a calendar connection of its own and never writes to one; it projects
// the bounded read and links into the existing meeting-prep flow.

// TodayMeetingsRead states, mirroring calendarhttp.TodayAgenda.
const (
	TodayMeetingsNotConnected = "not_connected"
	TodayMeetingsNeedsSetup   = "needs_setup"
	TodayMeetingsReady        = "ready"
	// todayMeetingsUnavailable is Today's own state for a read that failed or
	// timed out; the reader never returns it.
	todayMeetingsUnavailable = "unavailable"
)

// Meeting prep statuses, mirroring calendarhttp.TodayMeeting.PrepStatus.
const (
	todayMeetingPrepPending = "pending"
	todayMeetingPrepReady   = "ready"
	todayMeetingPrepStale   = "stale"
	todayMeetingPrepFailed  = "failed"
)

// Meeting row states. A conflict outranks everything else on the row.
const (
	TodayMeetingConflict    = "conflict"
	TodayMeetingBackToBack  = "back_to_back"
	TodayMeetingPrepReady   = "prep_ready"
	TodayMeetingPrepPending = "prep_pending"
	TodayMeetingNeedsPrep   = "needs_prep"
)

const (
	todayMeetingCap        = 12
	todayMeetingsKind      = "meeting"
	todayMeetingsSetupCopy = "Finish Calendar Ops setup"
	todayMeetingsConnect   = "Connect your calendar"
)

// TodayMeetingsRead is one bounded read of today's meetings. It carries no
// description, attendee, or link: the reader never has them to give.
type TodayMeetingsRead struct {
	State         string
	SetupState    string
	WorkspaceID   string
	WorkspaceSlug string
	WorkspaceName string
	// TimeZone is the IANA zone "today" was computed in.
	TimeZone string
	Meetings []TodayMeeting
	// Total is how many meetings today has before the reader's cap.
	Total int
	// Partial: some selected calendars could not be read.
	Partial bool
}

// TodayMeeting is one meeting in a TodayMeetingsRead. Title and Location are
// untrusted third-party text; a private meeting has neither.
type TodayMeeting struct {
	ID             string
	CalendarID     string
	Title          string
	StartTime      time.Time
	EndTime        time.Time
	AllDay         bool
	Private        bool
	ResponseStatus string
	Location       string
	Conflict       bool
	BackToBack     bool
	PrepStatus     string
	PrepNoteID     string
}

// TodayMeetingReader reads today's meetings for a user. Implemented in the
// server over the Calendar Ops handler with a hard timeout, so this package
// never depends on calendarhttp. fallbackTZ is used when the calendar binding
// sets no display timezone.
type TodayMeetingReader interface {
	TodayMeetings(ctx context.Context, userID, fallbackTZ string) (TodayMeetingsRead, error)
}

// TodayMeetingsProjection is Today's Meetings section. It is absent (nil)
// when there is nothing honest to say: no reader wired, or no calendar and no
// reason to suggest one.
type TodayMeetingsProjection struct {
	Health        TodaySourceHealth `json:"health"`
	State         string            `json:"state"`
	WorkspaceName string            `json:"workspace_name,omitempty"`
	// Route opens the Calendar Ops console on today.
	Route string `json:"route,omitempty"`
	// SetupRoute and SetupLabel are the one next step when the calendar is
	// not connected or not ready.
	SetupRoute      string      `json:"setup_route,omitempty"`
	SetupLabel      string      `json:"setup_label,omitempty"`
	TimeZone        string      `json:"time_zone,omitempty"`
	ConflictCount   int         `json:"conflict_count"`
	BackToBackCount int         `json:"back_to_back_count"`
	NeedsPrepCount  int         `json:"needs_prep_count"`
	MoreCount       int         `json:"more_count"`
	Items           []TodayItem `json:"items"`
}

// SetMeetingReader adds today's meetings to Today. Unset, Today is exactly
// what it was without it. Startup wiring only.
func (s *TodayService) SetMeetingReader(reader TodayMeetingReader) {
	if s != nil {
		s.meetings = reader
	}
}

// loadMeetings fills out.Meetings and returns every meeting the read
// returned, keyed by SourceRef.Key(), for grounding the brief's meeting
// references. The map is nil when no meeting could be verified (no reader, no
// calendar, or a failed read); the caller treats that as "cannot check", not
// as "every meeting is gone".
func (s *TodayService) loadMeetings(ctx context.Context, userID string, relationship *Projection, now time.Time, out *TodayProjection) map[string]TodayItem {
	if s == nil || s.meetings == nil || relationship == nil {
		return nil
	}
	fallbackTZ := ""
	if relationship.DailyBrief != nil {
		fallbackTZ = relationship.DailyBrief.Timezone
	}
	read, err := s.meetings.TodayMeetings(ctx, strings.TrimSpace(userID), fallbackTZ)
	projection, grounded := todayMeetingsProjection(read, err, slices.Contains(relationship.FocusAreas, FocusPrepareForMeetings), now)
	out.Meetings = projection

	event := EventData{State: read.State}
	if err != nil {
		event.State = todayMeetingsUnavailable
	}
	if projection != nil {
		event.Count = len(projection.Items)
		event.ReasonCode = projection.Health.Reason
	}
	recordEvent(EventTodayCalendarRead, event)
	return grounded
}

// todayMeetingsProjection maps one read to Today's section. Presence rules:
// not connected is shown only as a nudge to users who asked to be prepared for
// meetings, needs-setup is always shown, and neither makes Today "partial".
func todayMeetingsProjection(read TodayMeetingsRead, err error, wantsMeetings bool, now time.Time) (*TodayMeetingsProjection, map[string]TodayItem) {
	if err != nil {
		reason := "read_failed"
		if errors.Is(err, context.DeadlineExceeded) {
			reason = "timed_out"
		}
		return &TodayMeetingsProjection{Health: todayUnavailable(reason), State: todayMeetingsUnavailable, Items: []TodayItem{}}, nil
	}
	slug := strings.TrimSpace(read.WorkspaceSlug)
	consoleRoute := ""
	if todaySafeSlug.MatchString(slug) {
		consoleRoute = "/workspaces/" + url.PathEscape(slug) + "?panel=calendar"
	}

	switch read.State {
	case TodayMeetingsNeedsSetup:
		return &TodayMeetingsProjection{
			Health: TodaySourceHealth{Status: TodaySectionHealthyEmpty, Reason: "connection_not_ready"},
			State:  TodayMeetingsNeedsSetup, WorkspaceName: truncateRunes(read.WorkspaceName, 100),
			SetupRoute: consoleRoute, SetupLabel: todayMeetingsSetupCopy, Items: []TodayItem{},
		}, nil
	case TodayMeetingsReady:
	default:
		if !wantsMeetings {
			return nil, nil
		}
		return &TodayMeetingsProjection{
			Health: TodaySourceHealth{Status: TodaySectionHealthyEmpty, Reason: "not_connected"},
			State:  TodayMeetingsNotConnected, SetupRoute: progression.CalendarOpsCreateURL,
			SetupLabel: todayMeetingsConnect, Items: []TodayItem{},
		}, nil
	}

	loc := time.UTC
	if zone, zoneErr := time.LoadLocation(strings.TrimSpace(read.TimeZone)); zoneErr == nil && strings.TrimSpace(read.TimeZone) != "" {
		loc = zone
	}
	projection := &TodayMeetingsProjection{
		State: TodayMeetingsReady, WorkspaceName: truncateRunes(read.WorkspaceName, 100),
		Route: consoleRoute, TimeZone: loc.String(), Items: []TodayItem{},
	}
	grounded := make(map[string]TodayItem, len(read.Meetings))
	for _, meeting := range read.Meetings {
		if strings.TrimSpace(meeting.ID) == "" || strings.EqualFold(strings.TrimSpace(meeting.ResponseStatus), "declined") {
			continue
		}
		item, needsPrep := todayMeetingItem(meeting, read, slug, consoleRoute, loc, now)
		grounded[item.Ref.Key()] = item
		if len(projection.Items) >= todayMeetingCap {
			continue
		}
		projection.Items = append(projection.Items, item)
		if meeting.Conflict {
			projection.ConflictCount++
		}
		if meeting.BackToBack {
			projection.BackToBackCount++
		}
		if needsPrep {
			projection.NeedsPrepCount++
		}
	}
	projection.MoreCount = max(0, max(read.Total, len(grounded))-len(projection.Items))

	projection.Health = todayHealthForItems(projection.Items, now)
	if read.Partial {
		projection.Health = TodaySourceHealth{Status: TodaySectionPartial, Reason: "some_calendars_unavailable", UpdatedAt: now}
	}
	return projection, grounded
}

// todayMeetingItem renders one meeting row. needsPrep reports a timed,
// non-private meeting that has not ended and has no usable prep note, which is
// what the "Prepare" badge and the section's count mean.
func todayMeetingItem(meeting TodayMeeting, read TodayMeetingsRead, slug, consoleRoute string, loc *time.Location, now time.Time) (TodayItem, bool) {
	start, end := meeting.StartTime.In(loc), meeting.EndTime.In(loc)
	title := truncateRunes(meeting.Title, 200)
	if meeting.Private {
		title = "Private event"
	} else if title == "" {
		title = "Untitled event"
	}
	detail := todayMeetingTimes(meeting.AllDay, start, end)
	if location := truncateRunes(meeting.Location, 100); location != "" && !meeting.Private {
		detail += " · " + location
	}

	needsPrep := false
	state := ""
	switch meeting.PrepStatus {
	case todayMeetingPrepReady:
		state = TodayMeetingPrepReady
	case todayMeetingPrepPending:
		state = TodayMeetingPrepPending
	case "", todayMeetingPrepStale, todayMeetingPrepFailed:
		needsPrep = !meeting.Private && !meeting.AllDay && end.After(now)
		if needsPrep {
			state = TodayMeetingNeedsPrep
		}
	}
	if meeting.BackToBack {
		state = TodayMeetingBackToBack
	}
	if meeting.Conflict {
		state = TodayMeetingConflict
	}

	route := ""
	if consoleRoute != "" {
		values := url.Values{}
		values.Set("panel", "calendar")
		values.Set("event", meeting.ID)
		if calendarID := strings.TrimSpace(meeting.CalendarID); calendarID != "" {
			values.Set("calendar", calendarID)
		}
		route = "/workspaces/" + url.PathEscape(slug) + "?" + values.Encode()
	}
	due := start
	return TodayItem{
		ID: meeting.ID, Kind: todayMeetingsKind, Title: title, Detail: detail, State: state, Route: route,
		Ref: dailybrief.SourceRef{
			WorkspaceID: read.WorkspaceID, WorkspaceSlug: slug, EntityType: dailybrief.EntityCalendarEvent,
			EntityID: meeting.ID, CalendarID: strings.TrimSpace(meeting.CalendarID), Timestamp: start,
		},
		DueAt: &due, SourceAt: start,
	}, needsPrep
}

// todayMeetingTimes formats "9:00–9:30 AM", "11:30 AM–12:30 PM", or "All day".
func todayMeetingTimes(allDay bool, start, end time.Time) string {
	if allDay {
		return "All day"
	}
	if start.Format("PM") == end.Format("PM") && start.YearDay() == end.YearDay() {
		return start.Format("3:04") + "–" + end.Format("3:04 PM")
	}
	return start.Format("3:04 PM") + "–" + end.Format("3:04 PM")
}

// groundedMeetingBriefItem grounds a brief item that references a meeting to
// the meeting as Today just read it: title and route come from that read,
// never from the brief text.
func groundedMeetingBriefItem(reason string, ref dailybrief.SourceRef, meetings map[string]TodayItem) (TodayItem, bool) {
	meeting, ok := meetings[ref.Key()]
	if !ok || meeting.Ref.WorkspaceID != ref.WorkspaceID || meeting.ID != ref.EntityID {
		return TodayItem{}, false
	}
	detail := meeting.Detail
	switch strings.TrimSpace(reason) {
	case "calendar_conflict":
		detail += " · Overlaps another meeting"
	case "meeting_needs_prep":
		detail += " · No prep note yet"
	}
	return TodayItem{
		ID: meeting.ID, Kind: "brief", Title: meeting.Title, Detail: truncateRunes(detail, 300),
		State: meeting.State, Route: meeting.Route, Ref: meeting.Ref, SourceAt: meeting.SourceAt,
	}, true
}
