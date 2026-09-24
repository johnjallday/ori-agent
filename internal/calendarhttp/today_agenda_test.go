package calendarhttp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/calendar"
	"github.com/johnjallday/ori-agent/internal/meetingprep"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// todayAgendaNow is the pinned "now" every test below reads: 2026-07-20,
// 08:00 UTC.
var todayAgendaNow = time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC)

func pinTodayAgendaNow(t *testing.T) {
	t.Helper()
	prev := nowUTC
	nowUTC = func() time.Time { return todayAgendaNow }
	t.Cleanup(func() { nowUTC = prev })
}

// agendaClock is an RFC3339 instant on the pinned day (UTC), "HH:MM".
func agendaClock(clock string) string { return "2026-07-20T" + clock + ":00Z" }

// googleItem is one Google-shaped list_events result row.
func googleItem(id, title, start, end string, extra map[string]any) map[string]any {
	item := map[string]any{
		"id": id, "summary": title,
		"start": map[string]any{"dateTime": start},
		"end":   map[string]any{"dateTime": end},
	}
	for k, v := range extra {
		item[k] = v
	}
	return item
}

// newTodayAgendaHandler is newPortalTestHandler with a list_events mapping
// that also resolves every field a real connector returns, so the tests can
// prove which of them the projection drops.
func newTodayAgendaHandler(t *testing.T, calendarIDs []string, displayTZ string) (*Handler, *agentworkspace.Workspace, *recordingToolCaller) {
	t.Helper()
	pinTodayAgendaNow(t)
	h, ws, rec := newPortalTestHandler(t, "local")
	binding, _ := findCalendarBinding(ws)
	mapping, _ := binding.FindCapabilityMapping(calendar.CapabilityKey)
	listEvents := mapping.Operations[calendar.OpListEvents]
	listEvents.Fields = map[string]string{
		"id": "/id", "title": "/summary",
		"start_time": "/start/dateTime", "end_time": "/end/dateTime",
		"location": "/location", "description": "/description",
		"private": "/private", "canceled": "/canceled", "response_status": "/responseStatus",
		"all_day": "/allDay", "attendees": "/attendees",
		"conference_link": "/hangoutLink", "source_link": "/htmlLink",
	}
	mapping.Operations[calendar.OpListEvents] = listEvents
	binding.CapabilityMappings = []agentworkspace.CapabilityMapping{mapping}
	settings := calendar.ReadBindingSettings(binding.Config)
	settings.SelectedCalendarIDs = calendarIDs
	settings.DisplayTimeZone = displayTZ
	binding.Config = calendar.WriteBindingSettings(binding.Config, settings)
	if err := ws.UpsertMCPBinding(*binding); err != nil {
		t.Fatalf("UpsertMCPBinding: %v", err)
	}
	return h, ws, rec
}

// fakeTodayPrepStore is a MeetingPrepStore with canned links per key.
type fakeTodayPrepStore struct {
	links map[meetingprep.Key]*meetingprep.Link
	err   error
}

func (f *fakeTodayPrepStore) GetByKey(_ context.Context, key meetingprep.Key) (*meetingprep.Link, error) {
	if f.err != nil {
		return nil, f.err
	}
	link, ok := f.links[key]
	if !ok {
		return nil, errors.New("not found")
	}
	return link, nil
}

func (f *fakeTodayPrepStore) StartRun(context.Context, meetingprep.Key, string) (*meetingprep.Link, bool, error) {
	return nil, false, errors.New("unused")
}
func (f *fakeTodayPrepStore) MarkReady(context.Context, string, string, string) error { return nil }
func (f *fakeTodayPrepStore) MarkFailed(context.Context, string, string) error        { return nil }

func TestTodayAgenda_NoCalendarOpsWorkspaceIsNotConnected(t *testing.T) {
	h := newActiveWorkspaceTestHandler(nil, nil)
	got, err := h.TodayAgenda(context.Background(), "local", "", 12)
	if err != nil {
		t.Fatalf("TodayAgenda: %v", err)
	}
	if got.State != TodayAgendaNotConnected || got.WorkspaceID != "" || len(got.Meetings) != 0 || got.Partial {
		t.Fatalf("got %+v, want a bare not_connected agenda", got)
	}
}

func TestTodayAgenda_ConnectorNeedingAuthIsNeedsSetup(t *testing.T) {
	h, ws, rec := newTodayAgendaHandler(t, []string{"primary"}, "")
	h.WithConnectorStatusFn(func(string) connectorStatus { return connectorStatus{Present: true, AuthRequired: true} })

	got, err := h.TodayAgenda(context.Background(), "local", "", 12)
	if err != nil {
		t.Fatalf("TodayAgenda: %v", err)
	}
	if got.State != TodayAgendaNeedsSetup || got.SetupState != calendar.SetupAuthRequired {
		t.Fatalf("got state=%q setup=%q, want needs_setup/auth_required", got.State, got.SetupState)
	}
	if got.WorkspaceID != ws.ID || got.WorkspaceSlug != ws.FolderSlug || got.WorkspaceName != ws.Name {
		t.Fatalf("needs_setup must still name the workspace to finish, got %+v", got)
	}
	if rec.callCount() != 0 {
		t.Fatalf("a connector that is not ready must not be called, got %d calls", rec.callCount())
	}
}

func TestTodayAgenda_ReadyOrdersFlagsRedactsAndReportsPrep(t *testing.T) {
	h, ws, rec := newTodayAgendaHandler(t, []string{"primary", "team"}, "")
	binding, _ := findCalendarBinding(ws)
	rec.resultFn = func(_ string, args map[string]any) (any, error) {
		if args["calendarId"] == "team" {
			return map[string]any{"items": []any{
				googleItem("lunch", "Team lunch", agendaClock("12:30"), agendaClock("13:30"), nil),
			}}, nil
		}
		return map[string]any{"items": []any{
			googleItem("budget", "Budget sync", agendaClock("11:30"), agendaClock("12:30"), nil),
			googleItem("design", "Design review", agendaClock("11:00"), agendaClock("12:00"), map[string]any{
				"location":    "Room 4",
				"description": "SECRET-AGENDA-BODY",
				"attendees":   []any{map[string]any{"email": "ada@example.test"}},
				"hangoutLink": "https://meet.example.test/abc",
				"htmlLink":    "https://calendar.example.test/evt",
			}),
			googleItem("standup", "Standup", agendaClock("09:00"), agendaClock("09:30"), nil),
			googleItem("dentist", "Dentist", agendaClock("15:00"), agendaClock("16:00"), map[string]any{
				"private": true, "location": "Clinic on 5th",
			}),
			googleItem("gone", "Canceled sync", agendaClock("10:00"), agendaClock("10:30"), map[string]any{"canceled": true}),
			googleItem("nope", "Declined sync", agendaClock("10:00"), agendaClock("10:30"), map[string]any{"responseStatus": "declined"}),
			// The connector ignored the window: tomorrow's retro must not
			// count as today.
			googleItem("retro", "Retro", "2026-07-21T16:00:00Z", "2026-07-21T17:00:00Z", nil),
		}}, nil
	}
	design := calendar.Event{
		Title: "Design review", StartTime: agendaClock("11:00"), EndTime: agendaClock("12:00"),
		Location: "Room 4", Description: "SECRET-AGENDA-BODY",
	}
	key := func(calID, id string) meetingprep.Key {
		return meetingprep.Key{WorkspaceID: ws.ID, BindingID: binding.ID, CalendarID: calID, EventID: id}
	}
	h.SetMeetingPreps(&fakeTodayPrepStore{links: map[meetingprep.Key]*meetingprep.Link{
		key("primary", "design"): {Status: meetingprep.StatusReady, NoteID: "note-design", EventFingerprint: meetingprep.Fingerprint(meetingprep.FingerprintInput{
			Title: design.Title, StartTime: design.StartTime, EndTime: design.EndTime, Location: design.Location, Description: design.Description,
		})},
		key("primary", "standup"): {Status: meetingprep.StatusReady, NoteID: "note-old", EventFingerprint: "written-before-the-meeting-moved"},
		key("primary", "budget"):  {Status: meetingprep.StatusPending},
		key("team", "lunch"):      {Status: meetingprep.StatusFailed},
	}})

	got, err := h.TodayAgenda(context.Background(), "local", "", 12)
	if err != nil {
		t.Fatalf("TodayAgenda: %v", err)
	}
	if got.State != TodayAgendaReady || got.Partial || got.TimeZone != "UTC" {
		t.Fatalf("got state=%q partial=%v tz=%q, want ready/false/UTC", got.State, got.Partial, got.TimeZone)
	}

	type row struct {
		id, prep, note       string
		conflict, backToBack bool
	}
	want := []row{
		{id: "standup", prep: TodayPrepStale, note: "note-old"},
		{id: "design", prep: TodayPrepReady, note: "note-design", conflict: true},
		{id: "budget", prep: TodayPrepPending, conflict: true, backToBack: true},
		{id: "lunch", prep: TodayPrepFailed, backToBack: true},
		{id: "dentist"},
	}
	if len(got.Meetings) != len(want) || got.Total != len(want) {
		t.Fatalf("got %d meetings (total %d), want %d: %+v", len(got.Meetings), got.Total, len(want), got.Meetings)
	}
	for i, w := range want {
		m := got.Meetings[i]
		if m.ID != w.id || m.PrepStatus != w.prep || m.PrepNoteID != w.note || m.Conflict != w.conflict || m.BackToBack != w.backToBack {
			t.Fatalf("meeting %d = %+v, want %+v", i, m, w)
		}
	}
	if got.Meetings[1].Location != "Room 4" || got.Meetings[1].CalendarID != "primary" || got.Meetings[3].CalendarID != "team" {
		t.Fatalf("location and calendar ids must be carried: %+v", got.Meetings)
	}

	dentist := got.Meetings[4]
	if !dentist.Private || dentist.Title != "" || dentist.Location != "" || dentist.PrepStatus != "" {
		t.Fatalf("a private meeting must keep neither title, location, nor prep: %+v", dentist)
	}

	encoded, _ := json.Marshal(got)
	for _, leaked := range []string{"SECRET-AGENDA-BODY", "ada@example.test", "meet.example.test", "calendar.example.test", "Dentist", "Clinic on 5th"} {
		if strings.Contains(string(encoded), leaked) {
			t.Fatalf("projection leaked %q: %s", leaked, encoded)
		}
	}
}

func TestTodayAgenda_CapsAfterFlaggingAndTruncatesText(t *testing.T) {
	h, _, rec := newTodayAgendaHandler(t, []string{"primary"}, "")
	longTitle := strings.Repeat("é", 250)
	longPlace := strings.Repeat("x", 150)
	rec.resultFn = func(string, map[string]any) (any, error) {
		return map[string]any{"items": []any{
			googleItem("a", longTitle, agendaClock("09:00"), agendaClock("10:00"), map[string]any{"location": longPlace}),
			googleItem("b", "B", agendaClock("11:00"), agendaClock("12:00"), nil),
			// Overlaps b, but falls past the cap of 2: b must still be flagged.
			googleItem("c", "C", agendaClock("11:30"), agendaClock("12:30"), nil),
		}}, nil
	}

	got, err := h.TodayAgenda(context.Background(), "local", "", 2)
	if err != nil {
		t.Fatalf("TodayAgenda: %v", err)
	}
	if len(got.Meetings) != 2 || got.Total != 3 {
		t.Fatalf("got %d meetings of %d, want 2 of 3", len(got.Meetings), got.Total)
	}
	if !got.Meetings[1].Conflict {
		t.Fatal("a meeting overlapping one past the cap must still be flagged as a conflict")
	}
	if n := len([]rune(got.Meetings[0].Title)); n != todayMeetingTitleMaxRunes || !strings.HasSuffix(got.Meetings[0].Title, "…") {
		t.Fatalf("title has %d runes, want %d ending in an ellipsis", n, todayMeetingTitleMaxRunes)
	}
	if n := len([]rune(got.Meetings[0].Location)); n != todayMeetingLocationMaxRunes {
		t.Fatalf("location has %d runes, want %d", n, todayMeetingLocationMaxRunes)
	}
}

func TestTodayAgenda_ReadyButEmptyIsNotPartial(t *testing.T) {
	t.Run("no calendars selected", func(t *testing.T) {
		h, _, rec := newTodayAgendaHandler(t, nil, "")
		got, err := h.TodayAgenda(context.Background(), "local", "", 12)
		if err != nil {
			t.Fatalf("TodayAgenda: %v", err)
		}
		if got.State != TodayAgendaReady || len(got.Meetings) != 0 || got.Partial {
			t.Fatalf("got %+v, want ready, empty, not partial", got)
		}
		if rec.callCount() != 0 {
			t.Fatalf("no selected calendars means no connector calls, got %d", rec.callCount())
		}
	})
	t.Run("nothing scheduled", func(t *testing.T) {
		h, _, rec := newTodayAgendaHandler(t, []string{"primary"}, "")
		rec.resultFn = func(string, map[string]any) (any, error) { return map[string]any{"items": []any{}}, nil }
		got, err := h.TodayAgenda(context.Background(), "local", "", 12)
		if err != nil {
			t.Fatalf("TodayAgenda: %v", err)
		}
		if got.State != TodayAgendaReady || got.Meetings == nil || len(got.Meetings) != 0 || got.Partial {
			t.Fatalf("got %+v, want ready with an empty, non-nil list", got)
		}
	})
}

func TestTodayAgenda_AMeetingOnTwoSelectedCalendarsIsListedOnce(t *testing.T) {
	h, _, rec := newTodayAgendaHandler(t, []string{"primary", "team"}, "")
	rec.resultFn = func(string, map[string]any) (any, error) {
		return map[string]any{"items": []any{
			googleItem("all-hands", "All hands", agendaClock("10:00"), agendaClock("11:00"), nil),
		}}, nil
	}
	got, err := h.TodayAgenda(context.Background(), "local", "", 12)
	if err != nil {
		t.Fatalf("TodayAgenda: %v", err)
	}
	if len(got.Meetings) != 1 || got.Meetings[0].CalendarID != "primary" || got.Meetings[0].Conflict {
		t.Fatalf("got %+v, want one all-hands from the first calendar, not a conflict with itself", got.Meetings)
	}
}

func TestTodayAgenda_OneCalendarFailingKeepsTheOthersAndIsPartial(t *testing.T) {
	h, _, rec := newTodayAgendaHandler(t, []string{"primary", "broken"}, "")
	rec.resultFn = func(_ string, args map[string]any) (any, error) {
		if args["calendarId"] == "broken" {
			return nil, errConnectorBoom
		}
		return map[string]any{"items": []any{
			googleItem("standup", "Standup", agendaClock("09:00"), agendaClock("09:30"), nil),
		}}, nil
	}
	got, err := h.TodayAgenda(context.Background(), "local", "", 12)
	if err != nil {
		t.Fatalf("TodayAgenda: %v", err)
	}
	if !got.Partial || len(got.Meetings) != 1 || got.Meetings[0].ID != "standup" {
		t.Fatalf("got %+v, want the readable calendar's meeting and partial=true", got)
	}
}

func TestTodayAgenda_EveryCalendarFailingIsAFailedReadNotAnEmptyDay(t *testing.T) {
	h, _, rec := newTodayAgendaHandler(t, []string{"primary", "team"}, "")
	rec.resultFn = func(string, map[string]any) (any, error) { return nil, errConnectorBoom }
	_, err := h.TodayAgenda(context.Background(), "local", "", 12)
	if !errors.Is(err, ErrTodayAgendaUnreadable) {
		t.Fatalf("err = %v, want ErrTodayAgendaUnreadable", err)
	}
}

func TestTodayAgenda_ADeadlineDuringPrepLookupsIsATimeoutNotNoPrep(t *testing.T) {
	h, _, rec := newTodayAgendaHandler(t, []string{"primary"}, "")
	rec.resultFn = func(string, map[string]any) (any, error) {
		return map[string]any{"items": []any{googleItem("standup", "Standup", agendaClock("09:00"), agendaClock("09:30"), nil)}}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.SetMeetingPreps(cancelingPrepStore{cancel: cancel})
	if _, err := h.TodayAgenda(ctx, "local", "", 12); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the context's error", err)
	}
}

// cancelingPrepStore ends the caller's context on the first lookup, standing
// in for a deadline that expires mid-agenda.
type cancelingPrepStore struct{ cancel context.CancelFunc }

func (s cancelingPrepStore) GetByKey(ctx context.Context, _ meetingprep.Key) (*meetingprep.Link, error) {
	s.cancel()
	return nil, ctx.Err()
}
func (cancelingPrepStore) StartRun(context.Context, meetingprep.Key, string) (*meetingprep.Link, bool, error) {
	return nil, false, errors.New("unused")
}
func (cancelingPrepStore) MarkReady(context.Context, string, string, string) error { return nil }
func (cancelingPrepStore) MarkFailed(context.Context, string, string) error        { return nil }

func TestTodayAgenda_ReturnsContextErrorWhenTheReadOutlivesItsDeadline(t *testing.T) {
	h, _, _ := newTodayAgendaHandler(t, []string{"primary"}, "")
	h.WithToolCallerFactory(func(string) calendar.ToolCaller {
		return func(ctx context.Context, _ string, _ map[string]any) (any, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := h.TodayAgenda(ctx, "local", "", 12)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("TodayAgenda took %v; it must return once its context ends", elapsed)
	}
}

func TestTodayAgenda_TodayFollowsTheDisplayZoneThenTheFallback(t *testing.T) {
	// The pinned 08:00 UTC is 17:00 in Tokyo and 04:00 in New York: each
	// zone's own midnight starts the window the connector is asked for.
	cases := []struct {
		name, displayTZ, fallbackTZ, wantTZ string
	}{
		{name: "binding zone wins", displayTZ: "Asia/Tokyo", fallbackTZ: "America/New_York", wantTZ: "Asia/Tokyo"},
		{name: "fallback when the binding has none", fallbackTZ: "America/New_York", wantTZ: "America/New_York"},
		{name: "invalid fallback is UTC", fallbackTZ: "Not/AZone", wantTZ: "UTC"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, _, rec := newTodayAgendaHandler(t, []string{"primary"}, tc.displayTZ)
			rec.resultFn = func(string, map[string]any) (any, error) { return map[string]any{"items": []any{}}, nil }
			got, err := h.TodayAgenda(context.Background(), "local", tc.fallbackTZ, 12)
			if err != nil {
				t.Fatalf("TodayAgenda: %v", err)
			}
			if got.TimeZone != tc.wantTZ {
				t.Fatalf("time zone = %q, want %q", got.TimeZone, tc.wantTZ)
			}
			loc, _ := time.LoadLocation(tc.wantTZ)
			wantStart, _ := todayWindow(tc.wantTZ, todayAgendaNow)
			if len(rec.calls) != 1 || rec.calls[0].Args["timeMin"] != wantStart.Format(time.RFC3339) {
				t.Fatalf("connector asked for %v, want the window starting %v in %v", rec.calls, wantStart, loc)
			}
		})
	}
}

func TestTodayAgenda_MeetingTimesAreInTheResolvedZone(t *testing.T) {
	h, _, rec := newTodayAgendaHandler(t, []string{"primary"}, "Asia/Tokyo")
	rec.resultFn = func(string, map[string]any) (any, error) {
		return map[string]any{"items": []any{
			googleItem("late", "Late call", "2026-07-20T10:00:00Z", "2026-07-20T11:00:00Z", nil),
		}}, nil
	}
	got, err := h.TodayAgenda(context.Background(), "local", "", 12)
	if err != nil || len(got.Meetings) != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if got.Meetings[0].StartTime.Location().String() != "Asia/Tokyo" || got.Meetings[0].StartTime.Hour() != 19 {
		t.Fatalf("start = %v, want 19:00 Asia/Tokyo", got.Meetings[0].StartTime)
	}
}

func TestTodayAgenda_PrepLookupFailureNeverFailsTheAgenda(t *testing.T) {
	h, _, rec := newTodayAgendaHandler(t, []string{"primary"}, "")
	rec.resultFn = func(string, map[string]any) (any, error) {
		return map[string]any{"items": []any{googleItem("standup", "Standup", agendaClock("09:00"), agendaClock("09:30"), nil)}}, nil
	}
	h.SetMeetingPreps(&fakeTodayPrepStore{err: errors.New("db locked")})
	got, err := h.TodayAgenda(context.Background(), "local", "", 12)
	if err != nil || len(got.Meetings) != 1 || got.Meetings[0].PrepStatus != "" {
		t.Fatalf("got %+v, %v; want the meeting with no prep status", got, err)
	}
}
