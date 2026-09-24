package server

import (
	"context"
	"time"

	"github.com/johnjallday/ori-agent/internal/calendarhttp"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// Today's meetings for Personal Assistant Today (Issue #533), read through the
// Calendar Ops handler's bounded TodayAgenda. The Calendar Ops workspace keeps
// the connection; this adapter only bounds the read in time and maps the
// projection across the package boundary.

// todayCalendarReadTimeout bounds Today's calendar read. Today is a
// synchronous, user-facing read, so it gets a tighter bound than brief
// generation; a slow connector degrades only the Meetings section.
const todayCalendarReadTimeout = 5 * time.Second

// todayCalendarMeetingLimit is how many meetings Today asks for: more than it
// shows (12), so a brief reference to a later meeting still grounds.
const todayCalendarMeetingLimit = 25

// calendarAgendaReader is the one Calendar Ops read these adapters use.
// *calendarhttp.Handler satisfies it.
type calendarAgendaReader interface {
	TodayAgenda(ctx context.Context, userID, fallbackTZ string, limit int) (calendarhttp.TodayAgenda, error)
}

// dailyBriefCalendarSource adapts the Calendar Ops read for Today. Construct it
// only over a non-nil handler: a nil *calendarhttp.Handler in the interface
// would read as configured.
type dailyBriefCalendarSource struct {
	agenda       calendarAgendaReader
	todayTimeout time.Duration
}

func newDailyBriefCalendarSource(agenda calendarAgendaReader) *dailyBriefCalendarSource {
	return &dailyBriefCalendarSource{agenda: agenda, todayTimeout: todayCalendarReadTimeout}
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
