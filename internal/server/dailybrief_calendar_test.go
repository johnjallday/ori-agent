package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/calendar"
	"github.com/johnjallday/ori-agent/internal/calendarhttp"
	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

type fakeCalendarAgenda struct {
	agenda    calendarhttp.TodayAgenda
	err       error
	block     bool
	gotLimit  int
	gotTZ     string
	gotUserID string
}

func (f *fakeCalendarAgenda) TodayAgenda(ctx context.Context, userID, fallbackTZ string, limit int) (calendarhttp.TodayAgenda, error) {
	f.gotUserID, f.gotTZ, f.gotLimit = userID, fallbackTZ, limit
	if f.block {
		<-ctx.Done()
		return calendarhttp.TodayAgenda{}, ctx.Err()
	}
	return f.agenda, f.err
}

func TestDailyBriefCalendarSource_TodayWithoutAHandlerIsNotConnected(t *testing.T) {
	for _, source := range []*dailyBriefCalendarSource{nil, newDailyBriefCalendarSource(nil)} {
		got, err := source.TodayMeetings(context.Background(), "local", "UTC")
		if err != nil || got.State != personalassistant.TodayMeetingsNotConnected {
			t.Fatalf("got %+v, %v; want not_connected", got, err)
		}
	}
}

func TestDailyBriefCalendarSource_TodayReadIsBoundedInTime(t *testing.T) {
	agenda := &fakeCalendarAgenda{block: true}
	source := newDailyBriefCalendarSource(agenda)
	source.todayTimeout = 20 * time.Millisecond

	started := time.Now()
	_, err := source.TodayMeetings(context.Background(), "local", "UTC")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("a hung connector held Today for %v", elapsed)
	}
	if todayCalendarReadTimeout != 5*time.Second {
		t.Fatalf("Today's calendar bound is %v; Decision 2 fixes it at 5s", todayCalendarReadTimeout)
	}
}

func TestDailyBriefCalendarSource_BriefOutcomes(t *testing.T) {
	start := time.Date(2026, 9, 24, 9, 0, 0, 0, time.UTC)
	ready := calendarhttp.TodayAgenda{
		State: calendarhttp.TodayAgendaReady, WorkspaceID: "cal-ws", WorkspaceSlug: "calendar-ops",
		Meetings: []calendarhttp.TodayMeeting{{
			ID: "design", CalendarID: "primary", Title: "Design review", Location: "Room 4",
			StartTime: start, EndTime: start.Add(time.Hour), Conflict: true, PrepStatus: calendarhttp.TodayPrepStale,
		}},
	}

	t.Run("no handler or no calendar is not configured; a broken connection needs setup", func(t *testing.T) {
		if _, err := newDailyBriefCalendarSource(nil).BriefCalendarEvents(context.Background(), "local", "UTC"); !errors.Is(err, dailybrief.ErrCalendarNotConfigured) {
			t.Fatalf("nil handler: err = %v", err)
		}
		cases := map[string]error{
			calendarhttp.TodayAgendaNotConnected: dailybrief.ErrCalendarNotConfigured,
			calendarhttp.TodayAgendaNeedsSetup:   dailybrief.ErrCalendarNeedsSetup,
		}
		for state, want := range cases {
			source := newDailyBriefCalendarSource(&fakeCalendarAgenda{agenda: calendarhttp.TodayAgenda{State: state}})
			if _, err := source.BriefCalendarEvents(context.Background(), "local", "UTC"); !errors.Is(err, want) {
				t.Fatalf("%s: err = %v, want %v", state, err, want)
			}
		}
	})

	t.Run("ready maps a bounded calendar_event snapshot", func(t *testing.T) {
		agenda := &fakeCalendarAgenda{agenda: ready}
		events, err := newDailyBriefCalendarSource(agenda).BriefCalendarEvents(context.Background(), "local", "Asia/Seoul")
		if err != nil || len(events) != 1 {
			t.Fatalf("got %+v, %v", events, err)
		}
		if agenda.gotLimit != briefCalendarMeetingLimit || agenda.gotTZ != "Asia/Seoul" {
			t.Fatalf("agenda asked for limit=%d tz=%q", agenda.gotLimit, agenda.gotTZ)
		}
		want := dailybrief.CalendarEventSnapshot{
			Ref: dailybrief.SourceRef{
				WorkspaceID: "cal-ws", WorkspaceSlug: "calendar-ops", EntityType: dailybrief.EntityCalendarEvent,
				EntityID: "design", CalendarID: "primary", Timestamp: start,
			},
			Title: "Design review", Location: "Room 4", StartTime: start, EndTime: start.Add(time.Hour),
			Conflict: true, PrepStatus: "stale",
		}
		if events[0] != want {
			t.Fatalf("snapshot = %+v\nwant       %+v", events[0], want)
		}
	})

	t.Run("partial keeps the events and says so", func(t *testing.T) {
		partial := ready
		partial.Partial = true
		events, err := newDailyBriefCalendarSource(&fakeCalendarAgenda{agenda: partial}).BriefCalendarEvents(context.Background(), "local", "UTC")
		if !errors.Is(err, dailybrief.ErrCalendarPartialRead) || len(events) != 1 {
			t.Fatalf("got %d events, err %v", len(events), err)
		}
	})

	t.Run("a hung connector is a timeout within the bound", func(t *testing.T) {
		source := newDailyBriefCalendarSource(&fakeCalendarAgenda{block: true})
		source.briefTimeout = 20 * time.Millisecond
		started := time.Now()
		_, err := source.BriefCalendarEvents(context.Background(), "local", "UTC")
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 2*time.Second {
			t.Fatalf("err = %v after %v", err, time.Since(started))
		}
		bound, emailBound := briefCalendarReadTimeout, briefEmailReadTimeout
		if bound != 8*time.Second {
			t.Fatalf("the brief's calendar bound is %v; Issue #533 fixes it at 8s", bound)
		}
		if bound != emailBound {
			t.Fatalf("the brief's calendar bound (%v) must match email's (%v)", bound, emailBound)
		}
	})
}

func TestDailyBriefCalendarSource_TodayMapsTheAgendaFieldForField(t *testing.T) {
	start := time.Date(2026, 9, 24, 9, 0, 0, 0, time.UTC)
	agenda := &fakeCalendarAgenda{agenda: calendarhttp.TodayAgenda{
		State: calendarhttp.TodayAgendaReady, SetupState: calendar.SetupReady,
		WorkspaceID: "cal-ws", WorkspaceSlug: "calendar-ops", WorkspaceName: "Calendar Ops",
		TimeZone: "Asia/Seoul", Total: 3, Partial: true,
		Meetings: []calendarhttp.TodayMeeting{{
			ID: "standup", CalendarID: "primary", Title: "Standup", Location: "Room 2",
			StartTime: start, EndTime: start.Add(30 * time.Minute), ResponseStatus: "accepted",
			Conflict: true, BackToBack: true, PrepStatus: calendarhttp.TodayPrepReady, PrepNoteID: "note-1",
		}},
	}}
	got, err := newDailyBriefCalendarSource(agenda).TodayMeetings(context.Background(), "local", "Asia/Seoul")
	if err != nil {
		t.Fatal(err)
	}
	if agenda.gotUserID != "local" || agenda.gotTZ != "Asia/Seoul" || agenda.gotLimit != todayCalendarMeetingLimit {
		t.Fatalf("agenda called with user=%q tz=%q limit=%d", agenda.gotUserID, agenda.gotTZ, agenda.gotLimit)
	}
	want := personalassistant.TodayMeetingsRead{
		State: "ready", SetupState: "ready", WorkspaceID: "cal-ws", WorkspaceSlug: "calendar-ops", WorkspaceName: "Calendar Ops",
		TimeZone: "Asia/Seoul", Total: 3, Partial: true,
		Meetings: []personalassistant.TodayMeeting{{
			ID: "standup", CalendarID: "primary", Title: "Standup", Location: "Room 2",
			StartTime: start, EndTime: start.Add(30 * time.Minute), ResponseStatus: "accepted",
			Conflict: true, BackToBack: true, PrepStatus: "ready", PrepNoteID: "note-1",
		}},
	}
	if got.State != want.State || got.SetupState != want.SetupState || got.WorkspaceID != want.WorkspaceID ||
		got.WorkspaceSlug != want.WorkspaceSlug || got.WorkspaceName != want.WorkspaceName || got.TimeZone != want.TimeZone ||
		got.Total != want.Total || got.Partial != want.Partial || len(got.Meetings) != 1 || got.Meetings[0] != want.Meetings[0] {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}
