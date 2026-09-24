package personalassistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/progression"
)

type stubTodayMeetings struct {
	read       TodayMeetingsRead
	err        error
	fallbackTZ *string
}

func (s stubTodayMeetings) TodayMeetings(_ context.Context, _ string, fallbackTZ string) (TodayMeetingsRead, error) {
	if s.fallbackTZ != nil {
		*s.fallbackTZ = fallbackTZ
	}
	return s.read, s.err
}

// meetingsTodayNow is 08:00 UTC; the meetings below are on the same day.
var meetingsTodayNow = time.Date(2026, 9, 24, 8, 0, 0, 0, time.UTC)

func meetingAt(clock string) time.Time {
	t, err := time.Parse(time.RFC3339, "2026-09-24T"+clock+":00Z")
	if err != nil {
		panic(err)
	}
	return t
}

// newMeetingsTodayService is a Today with an HQ, no brief, no follow-ups, and
// the given meeting reader, so the only thing that can change Today's state
// is the Meetings section.
func newMeetingsTodayService(t *testing.T, projection *Projection, reader TodayMeetingReader, brief *dailybrief.Revision) *TodayService {
	t.Helper()
	store, _ := newTodayWorkspace(t, meetingsTodayNow)
	briefs := stubTodayBrief{err: dailybrief.ErrRevisionNotFound}
	if brief != nil {
		briefs = stubTodayBrief{revision: brief}
	}
	service := NewTodayService(stubTodayRelationship{projection: projection}, briefs, store, stubTodayFollowUps{})
	service.now = func() time.Time { return meetingsTodayNow }
	if reader != nil {
		service.SetMeetingReader(reader)
	}
	return service
}

func readyMeetings(meetings ...TodayMeeting) TodayMeetingsRead {
	return TodayMeetingsRead{
		State: TodayMeetingsReady, WorkspaceID: "cal-ws", WorkspaceSlug: "calendar-ops", WorkspaceName: "Calendar Ops",
		TimeZone: "UTC", Meetings: meetings, Total: len(meetings),
	}
}

func TestTodayMeetings_NotConnectedWithoutAMeetingsFocusIsAbsent(t *testing.T) {
	without := newMeetingsTodayService(t, baseTodayProjection(), nil, nil)
	want, err := without.Get(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	service := newMeetingsTodayService(t, baseTodayProjection(), stubTodayMeetings{read: TodayMeetingsRead{State: TodayMeetingsNotConnected}}, nil)
	got, err := service.Get(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	if got.Meetings != nil {
		t.Fatalf("no calendar and no meetings focus must leave the section absent, got %+v", got.Meetings)
	}
	if got.State != want.State {
		t.Fatalf("Today state = %q, want %q (unchanged by an absent section)", got.State, want.State)
	}
	encoded, _ := json.Marshal(got)
	if strings.Contains(string(encoded), `"meetings"`) {
		t.Fatalf("absent section must not be serialized: %s", encoded)
	}
}

func TestTodayMeetings_NotConnectedWithAMeetingsFocusNudgesWithoutMakingTodayPartial(t *testing.T) {
	projection := baseTodayProjection()
	projection.FocusAreas = []FocusArea{FocusPrepareForMeetings}
	service := newMeetingsTodayService(t, projection, stubTodayMeetings{read: TodayMeetingsRead{State: TodayMeetingsNotConnected}}, nil)
	got, err := service.Get(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	m := got.Meetings
	if m == nil || m.State != TodayMeetingsNotConnected || m.Health.Status != TodaySectionHealthyEmpty || m.Health.Reason != "not_connected" {
		t.Fatalf("want a not_connected nudge, got %+v", m)
	}
	if m.SetupRoute != progression.CalendarOpsCreateURL || m.SetupLabel != "Connect your calendar" || len(m.Items) != 0 {
		t.Fatalf("nudge must route to Mission 04's Calendar Ops create flow, got %+v", m)
	}
	if got.State == "partial" {
		t.Fatal("a calendar that was never connected must not make Today partial")
	}
}

func TestTodayMeetings_NeedsSetupRoutesToTheWorkspaceToFinish(t *testing.T) {
	read := TodayMeetingsRead{State: TodayMeetingsNeedsSetup, SetupState: "auth_required", WorkspaceID: "cal-ws", WorkspaceSlug: "calendar-ops", WorkspaceName: "Calendar Ops"}
	service := newMeetingsTodayService(t, baseTodayProjection(), stubTodayMeetings{read: read}, nil)
	got, err := service.Get(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	m := got.Meetings
	if m == nil || m.State != TodayMeetingsNeedsSetup || m.Health.Reason != "connection_not_ready" || m.Health.Status != TodaySectionHealthyEmpty {
		t.Fatalf("want needs_setup, got %+v", m)
	}
	if m.SetupRoute != "/workspaces/calendar-ops?panel=calendar" || m.SetupLabel != "Finish Calendar Ops setup" {
		t.Fatalf("want the finish-setup route, got %+v", m)
	}
	if got.State == "partial" {
		t.Fatal("an unfinished connection is a nudge, not a degraded Today")
	}

	read.WorkspaceSlug = "../evil"
	unsafe := newMeetingsTodayService(t, baseTodayProjection(), stubTodayMeetings{read: read}, nil)
	got, _ = unsafe.Get(context.Background(), "local")
	if got.Meetings.SetupRoute != "" {
		t.Fatalf("an unsafe slug must never become a route, got %q", got.Meetings.SetupRoute)
	}
}

func TestTodayMeetings_ReadyRendersRowsStatesRoutesAndRefs(t *testing.T) {
	read := readyMeetings(
		TodayMeeting{ID: "standup", CalendarID: "primary", Title: "Standup", Location: "Room 2", StartTime: meetingAt("09:00"), EndTime: meetingAt("09:30")},
		TodayMeeting{ID: "design", CalendarID: "primary", Title: "Design review", StartTime: meetingAt("11:00"), EndTime: meetingAt("12:00"), Conflict: true, PrepStatus: "ready"},
		TodayMeeting{ID: "budget", CalendarID: "primary", Title: "Budget sync", StartTime: meetingAt("11:30"), EndTime: meetingAt("12:30"), Conflict: true, BackToBack: true},
		TodayMeeting{ID: "lunch", CalendarID: "team", Title: "Team lunch", StartTime: meetingAt("12:30"), EndTime: meetingAt("13:30"), BackToBack: true},
		TodayMeeting{ID: "prepped", CalendarID: "primary", Title: "Board prep", StartTime: meetingAt("14:00"), EndTime: meetingAt("15:00"), PrepStatus: "ready", PrepNoteID: "note-1"},
		TodayMeeting{ID: "running", CalendarID: "primary", Title: "Hiring sync", StartTime: meetingAt("15:00"), EndTime: meetingAt("15:30"), PrepStatus: "pending"},
		TodayMeeting{ID: "private", CalendarID: "primary", Private: true, StartTime: meetingAt("16:00"), EndTime: meetingAt("17:00")},
		TodayMeeting{ID: "offsite", CalendarID: "primary", Title: "Offsite", AllDay: true, StartTime: meetingAt("00:00"), EndTime: meetingAt("00:00").Add(24 * time.Hour)},
		TodayMeeting{ID: "early", CalendarID: "primary", Title: "Gym", StartTime: meetingAt("06:00"), EndTime: meetingAt("07:00")},
	)
	service := newMeetingsTodayService(t, baseTodayProjection(), stubTodayMeetings{read: read}, nil)
	got, err := service.Get(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	m := got.Meetings
	if m == nil || m.State != TodayMeetingsReady || m.Health.Status != TodaySectionAvailable || m.Route != "/workspaces/calendar-ops?panel=calendar" {
		t.Fatalf("want a ready section, got %+v", m)
	}
	want := []struct{ id, title, detail, state string }{
		{"standup", "Standup", "9:00–9:30 AM · Room 2", TodayMeetingNeedsPrep},
		{"design", "Design review", "11:00 AM–12:00 PM", TodayMeetingConflict},
		{"budget", "Budget sync", "11:30 AM–12:30 PM", TodayMeetingConflict},
		{"lunch", "Team lunch", "12:30–1:30 PM", TodayMeetingBackToBack},
		{"prepped", "Board prep", "2:00–3:00 PM", TodayMeetingPrepReady},
		{"running", "Hiring sync", "3:00–3:30 PM", TodayMeetingPrepPending},
		{"private", "Private event", "4:00–5:00 PM", ""},
		{"offsite", "Offsite", "All day", ""},
		// Already over at 08:00: no nudge to prepare for it.
		{"early", "Gym", "6:00–7:00 AM", ""},
	}
	if len(m.Items) != len(want) {
		t.Fatalf("got %d rows, want %d: %+v", len(m.Items), len(want), m.Items)
	}
	for i, w := range want {
		item := m.Items[i]
		if item.ID != w.id || item.Title != w.title || item.Detail != w.detail || item.State != w.state || item.Kind != "meeting" {
			t.Fatalf("row %d = %+v, want %+v", i, item, w)
		}
	}
	lunch := m.Items[3]
	if lunch.Route != "/workspaces/calendar-ops?calendar=team&event=lunch&panel=calendar" {
		t.Fatalf("meeting route = %q", lunch.Route)
	}
	if lunch.Ref.EntityType != dailybrief.EntityCalendarEvent || lunch.Ref.WorkspaceID != "cal-ws" || lunch.Ref.CalendarID != "team" ||
		lunch.Ref.EntityID != "lunch" || !lunch.Ref.Timestamp.Equal(meetingAt("12:30")) || lunch.DueAt == nil {
		t.Fatalf("meeting ref = %+v", lunch.Ref)
	}
	// Needs-prep counts every upcoming unprepared meeting, including the ones
	// whose badge shows the overlap or hand-off instead: standup, budget, lunch.
	if m.ConflictCount != 2 || m.BackToBackCount != 2 || m.NeedsPrepCount != 3 || m.MoreCount != 0 {
		t.Fatalf("counts conflict=%d b2b=%d prep=%d more=%d", m.ConflictCount, m.BackToBackCount, m.NeedsPrepCount, m.MoreCount)
	}
}

func TestTodayMeetings_CapsRowsAndCountsTheRest(t *testing.T) {
	meetings := make([]TodayMeeting, 0, 15)
	for i := 0; i < 15; i++ {
		start := meetingAt("09:00").Add(time.Duration(i) * 30 * time.Minute)
		meetings = append(meetings, TodayMeeting{ID: fmt.Sprintf("m-%02d", i), CalendarID: "primary", Title: "M", StartTime: start, EndTime: start.Add(20 * time.Minute)})
	}
	read := readyMeetings(meetings...)
	read.Total = 18 // the reader itself cut three more
	service := newMeetingsTodayService(t, baseTodayProjection(), stubTodayMeetings{read: read}, nil)
	got, _ := service.Get(context.Background(), "local")
	if len(got.Meetings.Items) != todayMeetingCap || got.Meetings.MoreCount != 18-todayMeetingCap {
		t.Fatalf("rows=%d more=%d, want %d and %d", len(got.Meetings.Items), got.Meetings.MoreCount, todayMeetingCap, 18-todayMeetingCap)
	}
}

func TestTodayMeetings_ReadyButEmptyIsHealthyEmpty(t *testing.T) {
	service := newMeetingsTodayService(t, baseTodayProjection(), stubTodayMeetings{read: readyMeetings()}, nil)
	got, _ := service.Get(context.Background(), "local")
	if got.Meetings == nil || got.Meetings.Health.Status != TodaySectionHealthyEmpty || len(got.Meetings.Items) != 0 || got.State == "partial" {
		t.Fatalf("want a healthy-empty section and a non-partial Today, got %+v (Today %q)", got.Meetings, got.State)
	}
}

func TestTodayMeetings_ReadFailuresAreNamedAndMakeTodayPartial(t *testing.T) {
	cases := []struct {
		name, reason string
		err          error
	}{
		{name: "read failure", reason: "read_failed", err: errors.New("connector exploded: token=abc")},
		{name: "timeout", reason: "timed_out", err: fmt.Errorf("calendar: %w", context.DeadlineExceeded)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service := newMeetingsTodayService(t, baseTodayProjection(), stubTodayMeetings{err: tc.err}, nil)
			got, err := service.Get(context.Background(), "local")
			if err != nil {
				t.Fatal(err)
			}
			m := got.Meetings
			if m == nil || m.Health.Status != TodaySectionUnavailable || m.Health.Reason != tc.reason || len(m.Items) != 0 {
				t.Fatalf("want unavailable/%s, got %+v", tc.reason, m)
			}
			if got.State != "partial" {
				t.Fatalf("a connected calendar that cannot be read must make Today partial, got %q", got.State)
			}
			encoded, _ := json.Marshal(got)
			if strings.Contains(string(encoded), "token=abc") || strings.Contains(string(encoded), "exploded") {
				t.Fatalf("internal error text leaked: %s", encoded)
			}
		})
	}
}

func TestTodayMeetings_SomeCalendarsFailingIsPartial(t *testing.T) {
	read := readyMeetings(TodayMeeting{ID: "standup", CalendarID: "primary", Title: "Standup", StartTime: meetingAt("09:00"), EndTime: meetingAt("09:30")})
	read.Partial = true
	service := newMeetingsTodayService(t, baseTodayProjection(), stubTodayMeetings{read: read}, nil)
	got, _ := service.Get(context.Background(), "local")
	if got.Meetings.Health.Status != TodaySectionPartial || got.Meetings.Health.Reason != "some_calendars_unavailable" || len(got.Meetings.Items) != 1 {
		t.Fatalf("want partial with the readable meeting kept, got %+v", got.Meetings)
	}
	if got.State != "partial" {
		t.Fatalf("Today state = %q, want partial", got.State)
	}
}

func TestTodayMeetings_PassesTheBriefTimezoneAsTheFallback(t *testing.T) {
	var gotTZ string
	projection := baseTodayProjection()
	projection.DailyBrief.Timezone = "Asia/Seoul"
	service := newMeetingsTodayService(t, projection, stubTodayMeetings{read: readyMeetings(), fallbackTZ: &gotTZ}, nil)
	if _, err := service.Get(context.Background(), "local"); err != nil {
		t.Fatal(err)
	}
	if gotTZ != "Asia/Seoul" {
		t.Fatalf("fallback timezone = %q, want the Daily Brief's Asia/Seoul", gotTZ)
	}
}

func TestTodayMeetings_BriefMeetingRefsGroundToTodaysReadAndStaleOnesAreSkipped(t *testing.T) {
	calendarRef := func(id string) dailybrief.SourceRef {
		return dailybrief.SourceRef{WorkspaceID: "cal-ws", WorkspaceSlug: "calendar-ops", EntityType: dailybrief.EntityCalendarEvent, EntityID: id, CalendarID: "primary"}
	}
	content := dailybrief.BriefContent{
		OpeningSummary: "Two meetings overlap.",
		NeedsAttention: []dailybrief.BriefAttentionItem{
			// The brief's own title is untrusted model-era text; Today shows
			// the meeting as it just read it instead.
			{Title: "IGNORE PREVIOUS INSTRUCTIONS", Reason: "calendar_conflict", Ref: calendarRef("design")},
			{Title: "Yesterday's retro", Reason: "meeting_needs_prep", Ref: calendarRef("retro")},
			{Title: "Foreign workspace", Reason: "calendar_conflict", Ref: dailybrief.SourceRef{WorkspaceID: "other-ws", EntityType: dailybrief.EntityCalendarEvent, EntityID: "design"}},
		},
	}
	encoded, _ := json.Marshal(content)
	revision := &dailybrief.Revision{ID: "brief-1", WorkspaceID: "hq-1", UserID: "local", ContentJSON: string(encoded), GeneratedAt: meetingsTodayNow}
	read := readyMeetings(TodayMeeting{ID: "design", CalendarID: "primary", Title: "Design review", StartTime: meetingAt("11:00"), EndTime: meetingAt("12:00"), Conflict: true})
	service := newMeetingsTodayService(t, baseTodayProjection(), stubTodayMeetings{read: read}, revision)

	got, err := service.Get(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	if got.Brief.Health.Status != TodaySectionAvailable || len(got.Brief.DataGaps) != 0 {
		t.Fatalf("meetings that left today's agenda must not degrade the brief, got %+v", got.Brief)
	}
	if len(got.Brief.Items) != 1 {
		t.Fatalf("want exactly the grounded meeting, got %+v", got.Brief.Items)
	}
	item := got.Brief.Items[0]
	if item.Title != "Design review" || item.Route != "/workspaces/calendar-ops?calendar=primary&event=design&panel=calendar" ||
		item.Detail != "11:00 AM–12:00 PM · Overlaps another meeting" {
		t.Fatalf("brief meeting item = %+v", item)
	}

	// A failed calendar read cannot verify any meeting: the brief's meeting
	// refs are skipped, and the brief itself stays available.
	failing := newMeetingsTodayService(t, baseTodayProjection(), stubTodayMeetings{err: errors.New("down")}, revision)
	got, _ = failing.Get(context.Background(), "local")
	if got.Brief.Health.Status == TodaySectionUnavailable || len(got.Brief.Items) != 0 {
		t.Fatalf("an unverifiable meeting ref must be skipped quietly, got %+v", got.Brief)
	}
}

func TestTodayMeetings_RecordsCountsAndStatusesOnly(t *testing.T) {
	var captured []logger.Fields
	original := emitPersonalAssistantEvent
	emitPersonalAssistantEvent = func(event EventType, fields logger.Fields) {
		if event == EventTodayCalendarRead {
			captured = append(captured, fields)
		}
	}
	t.Cleanup(func() { emitPersonalAssistantEvent = original })

	read := readyMeetings(TodayMeeting{ID: "secret-event-id", CalendarID: "secret-cal", Title: "Secret title", StartTime: meetingAt("09:00"), EndTime: meetingAt("09:30")})
	read.Partial = true
	service := newMeetingsTodayService(t, baseTodayProjection(), stubTodayMeetings{read: read}, nil)
	if _, err := service.Get(context.Background(), "local"); err != nil {
		t.Fatal(err)
	}
	if len(captured) != 1 {
		t.Fatalf("want one calendar-read event, got %d", len(captured))
	}
	fields := captured[0]
	if fields["state"] != "ready" || fields["count"] != 1 || fields["reason_code"] != "some_calendars_unavailable" {
		t.Fatalf("event fields = %v", fields)
	}
	encoded, _ := json.Marshal(fields)
	for _, leaked := range []string{"secret-event-id", "secret-cal", "Secret title", "cal-ws"} {
		if strings.Contains(string(encoded), leaked) {
			t.Fatalf("telemetry leaked %q: %s", leaked, encoded)
		}
	}

	// Every state and reason Today can report must be in the closed event
	// vocabulary, or eventFields drops the whole event without a trace.
	withFocus := baseTodayProjection()
	withFocus.FocusAreas = []FocusArea{FocusPrepareForMeetings}
	cases := []struct {
		name, state, reason string
		projection          *Projection
		reader              stubTodayMeetings
	}{
		{"not connected, no nudge", "not_connected", "", baseTodayProjection(), stubTodayMeetings{read: TodayMeetingsRead{State: TodayMeetingsNotConnected}}},
		{"not connected nudge", "not_connected", "not_connected", withFocus, stubTodayMeetings{read: TodayMeetingsRead{State: TodayMeetingsNotConnected}}},
		{"needs setup", "needs_setup", "connection_not_ready", baseTodayProjection(), stubTodayMeetings{read: TodayMeetingsRead{State: TodayMeetingsNeedsSetup, WorkspaceSlug: "calendar-ops"}}},
		{"ready", "ready", "", baseTodayProjection(), stubTodayMeetings{read: readyMeetings()}},
		{"read failed", "unavailable", "read_failed", baseTodayProjection(), stubTodayMeetings{err: errors.New("down")}},
		{"timed out", "unavailable", "timed_out", baseTodayProjection(), stubTodayMeetings{err: context.DeadlineExceeded}},
	}
	for _, tc := range cases {
		captured = nil
		service := newMeetingsTodayService(t, tc.projection, tc.reader, nil)
		_, _ = service.Get(context.Background(), "local")
		if len(captured) != 1 {
			t.Fatalf("%s: want one calendar-read event, got %d (a value outside the closed vocabulary drops it)", tc.name, len(captured))
		}
		reason, _ := captured[0]["reason_code"].(string)
		if captured[0]["state"] != tc.state || reason != tc.reason {
			t.Fatalf("%s: event = %v, want state=%s reason=%q", tc.name, captured[0], tc.state, tc.reason)
		}
	}
}
