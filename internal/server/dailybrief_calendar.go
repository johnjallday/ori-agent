package server

import (
	"context"
	"errors"
	"time"

	"github.com/johnjallday/ori-agent/internal/calendarhttp"
	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// Today's meetings for Personal Assistant Today and the Daily Brief (Issue
// #533), read through the Calendar Ops handler's bounded TodayAgenda. The
// Calendar Ops workspace keeps the connection; this adapter only bounds each
// read in time and maps the projection across the package boundary.

// todayCalendarReadTimeout bounds Today's calendar read. Today is a
// synchronous, user-facing read, so it gets a tighter bound than brief
// generation; a slow connector degrades only the Meetings section.
const todayCalendarReadTimeout = 5 * time.Second

// todayCalendarMeetingLimit is how many meetings Today asks for: more than it
// shows (12), so a brief reference to a later meeting still grounds.
const todayCalendarMeetingLimit = 25

// briefCalendarReadTimeout bounds the calendar read during brief generation,
// matching briefEmailReadTimeout: a slow connector becomes a named gap, never
// a slow or failed brief.
const briefCalendarReadTimeout = 8 * time.Second

// briefCalendarMeetingLimit is how many meetings the brief asks for: more
// than it lists (dailybrief caps at 8), so the brief can count the whole day
// and, on a busy afternoon, list what is still ahead rather than the earliest.
const briefCalendarMeetingLimit = 25

// calendarAgendaReader is the one Calendar Ops read these adapters use.
// *calendarhttp.Handler satisfies it.
type calendarAgendaReader interface {
	TodayAgenda(ctx context.Context, userID, fallbackTZ string, limit int) (calendarhttp.TodayAgenda, error)
}

// dailyBriefCalendarSource adapts the Calendar Ops read for Today
// (personalassistant.TodayMeetingReader) and for brief generation
// (dailybrief.CalendarSource). Construct it only over a non-nil handler: a nil
// *calendarhttp.Handler in the interface would read as configured.
type dailyBriefCalendarSource struct {
	agenda       calendarAgendaReader
	todayTimeout time.Duration
	briefTimeout time.Duration
}

var (
	_ personalassistant.TodayMeetingReader = (*dailyBriefCalendarSource)(nil)
	_ dailybrief.CalendarSource            = (*dailyBriefCalendarSource)(nil)
)

func newDailyBriefCalendarSource(agenda calendarAgendaReader) *dailyBriefCalendarSource {
	return &dailyBriefCalendarSource{agenda: agenda, todayTimeout: todayCalendarReadTimeout, briefTimeout: briefCalendarReadTimeout}
}

// BriefCalendarEvents implements dailybrief.CalendarSource. No calendar is
// ErrCalendarNotConfigured (no section, no gap); a Calendar Ops workspace
// whose connection is not ready is ErrCalendarNeedsSetup (a named gap); some
// calendars failing is ErrCalendarPartialRead alongside the rest; any other
// failure, an expired bound included, is returned so the brief names a gap.
func (s *dailyBriefCalendarSource) BriefCalendarEvents(ctx context.Context, userID, fallbackTZ string) ([]dailybrief.CalendarEventSnapshot, error) {
	if s == nil || s.agenda == nil {
		return nil, dailybrief.ErrCalendarNotConfigured
	}
	ctx, cancel := context.WithTimeout(ctx, s.briefTimeout)
	defer cancel()
	started := time.Now()
	agenda, err := s.agenda.TodayAgenda(ctx, userID, fallbackTZ, briefCalendarMeetingLimit)
	if err != nil {
		logger.Warn("dailybrief: calendar read failed", logger.Fields{
			"status": "error", "timed_out": errors.Is(err, context.DeadlineExceeded),
			"duration_ms": time.Since(started).Milliseconds(),
		})
		return nil, err
	}
	switch agenda.State {
	case calendarhttp.TodayAgendaReady:
	case calendarhttp.TodayAgendaNeedsSetup:
		logger.Info("dailybrief: calendar read", logger.Fields{"status": agenda.State, "setup_state": string(agenda.SetupState)})
		return nil, dailybrief.ErrCalendarNeedsSetup
	default:
		logger.Info("dailybrief: calendar read", logger.Fields{"status": agenda.State})
		return nil, dailybrief.ErrCalendarNotConfigured
	}
	events := make([]dailybrief.CalendarEventSnapshot, 0, len(agenda.Meetings))
	for _, m := range agenda.Meetings {
		events = append(events, dailybrief.CalendarEventSnapshot{
			Ref: dailybrief.SourceRef{
				WorkspaceID: agenda.WorkspaceID, WorkspaceSlug: agenda.WorkspaceSlug,
				EntityType: dailybrief.EntityCalendarEvent, EntityID: m.ID, CalendarID: m.CalendarID,
				Timestamp: m.StartTime,
			},
			Title: m.Title, Location: m.Location, StartTime: m.StartTime, EndTime: m.EndTime,
			AllDay: m.AllDay, Private: m.Private, Conflict: m.Conflict, BackToBack: m.BackToBack,
			PrepStatus: m.PrepStatus,
		})
	}
	// Counts and statuses only: never a title, an event id, or a calendar id.
	logger.Info("dailybrief: calendar read", logger.Fields{
		"status": agenda.State, "meetings": len(events), "total": agenda.Total,
		"partial": agenda.Partial, "duration_ms": time.Since(started).Milliseconds(),
	})
	if agenda.Partial {
		return events, dailybrief.ErrCalendarPartialRead
	}
	return events, nil
}

// TodayMeetings implements personalassistant.TodayMeetingReader. An expired
// bound comes back as context.DeadlineExceeded, which Today reports as
// "timed_out".
func (s *dailyBriefCalendarSource) TodayMeetings(ctx context.Context, userID, fallbackTZ string) (personalassistant.TodayMeetingsRead, error) {
	if s == nil || s.agenda == nil {
		return personalassistant.TodayMeetingsRead{State: personalassistant.TodayMeetingsNotConnected}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, s.todayTimeout)
	defer cancel()
	agenda, err := s.agenda.TodayAgenda(ctx, userID, fallbackTZ, todayCalendarMeetingLimit)
	if err != nil {
		return personalassistant.TodayMeetingsRead{}, err
	}
	return todayMeetingsReadFromAgenda(agenda), nil
}

func todayMeetingsReadFromAgenda(agenda calendarhttp.TodayAgenda) personalassistant.TodayMeetingsRead {
	out := personalassistant.TodayMeetingsRead{
		State: agenda.State, SetupState: string(agenda.SetupState),
		WorkspaceID: agenda.WorkspaceID, WorkspaceSlug: agenda.WorkspaceSlug, WorkspaceName: agenda.WorkspaceName,
		TimeZone: agenda.TimeZone, Total: agenda.Total, Partial: agenda.Partial,
		Meetings: make([]personalassistant.TodayMeeting, 0, len(agenda.Meetings)),
	}
	for _, m := range agenda.Meetings {
		out.Meetings = append(out.Meetings, personalassistant.TodayMeeting{
			ID: m.ID, CalendarID: m.CalendarID, Title: m.Title,
			StartTime: m.StartTime, EndTime: m.EndTime, AllDay: m.AllDay, Private: m.Private,
			ResponseStatus: m.ResponseStatus, Location: m.Location,
			Conflict: m.Conflict, BackToBack: m.BackToBack,
			PrepStatus: m.PrepStatus, PrepNoteID: m.PrepNoteID,
		})
	}
	return out
}
