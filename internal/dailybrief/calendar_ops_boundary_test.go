package dailybrief

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// TestCalendarContractIsBounded pins the calendar contract of Daily Brief
// generation.
//
// This file used to hold the FR54 characterization guard: "Calendar Ops must
// never add a live call to Daily Brief generation in this release", enforced
// by failing the build if SnapshotSources or Snapshot ever gained a calendar
// field. Issue #533 reversed FR54 deliberately, so the brief can prepare the
// user for today's meetings (Mission 04's calendar promise). The guard was
// rewritten rather than deleted: what it now fails on is any change that
// widens the bound that reversal agreed to:
//
//   - calendar data enters only through the allowlisted fields below, and the
//     old check (no other field mentioning "calendar") still runs;
//   - the calendar source is an optional, single-method interface read once
//     per generation; nil means no call, no section, and no gap;
//   - each meeting carries exactly CalendarEventSnapshot's fields (names and
//     types): never a description, attendees, or a conference or source link;
//   - at most maxCalendarEventsPerBrief meetings;
//   - every meeting is a calendar_event ref in the synthesis allowlist.
//
// The time bound (8 seconds per read) lives with the server adapter and is
// pinned there: see internal/server/dailybrief_calendar_test.go.
func TestCalendarContractIsBounded(t *testing.T) {
	t.Run("calendar enters the brief only through the agreed fields", func(t *testing.T) {
		// The old guard's check, kept with an allowlist: a second calendar
		// source or another calendar-derived snapshot field is a contract
		// change, not a quiet addition.
		assertOnlyAllowedCalendarFields(t, reflect.TypeFor[SnapshotSources](), "Calendar")
		assertOnlyAllowedCalendarFields(t, reflect.TypeFor[Snapshot](),
			"CalendarEvents", "CalendarConnected", "CalendarMeetingCount", "CalendarOverlapCount")
		assertOnlyAllowedCalendarFields(t, reflect.TypeFor[BriefContent](),
			"CalendarConnected")
	})

	t.Run("the source is optional, read once, and nil means no call, no section, no gap", func(t *testing.T) {
		field, ok := reflect.TypeFor[SnapshotSources]().FieldByName("Calendar")
		if !ok || field.Type.Kind() != reflect.Interface {
			t.Fatalf("SnapshotSources.Calendar must be an optional interface, got %v", field.Type)
		}
		source := reflect.TypeFor[CalendarSource]()
		if source.NumMethod() != 1 || source.Method(0).Name != "BriefCalendarEvents" {
			t.Fatalf("CalendarSource must stay the one read method, got %d methods", source.NumMethod())
		}
		snap := BuildSnapshot(context.Background(), SnapshotSources{Workspaces: workspace.NewInMemoryStore()},
			Config{Scope: ScopeAll}, "local", time.Now())
		if snap.CalendarConnected || len(snap.CalendarEvents) != 0 || len(snap.Gaps) != 0 {
			t.Fatalf("a nil calendar source must leave no trace, got connected=%v events=%d gaps=%v",
				snap.CalendarConnected, len(snap.CalendarEvents), snap.Gaps)
		}
		calls := 0
		BuildSnapshot(context.Background(), calendarSources(fakeCalendar{calls: &calls}), Config{Scope: ScopeAll}, "local", time.Now())
		if calls != 1 {
			t.Fatalf("one brief generation reads the calendar %d times, want exactly once", calls)
		}
	})

	t.Run("a meeting carries exactly the bounded fields", func(t *testing.T) {
		// MeetingItem is a conversion of CalendarEventSnapshot, so the compiler
		// pins it to this list too.
		assertExactFields(t, reflect.TypeFor[CalendarEventSnapshot](), map[string]string{
			"Ref": "dailybrief.SourceRef", "Title": "string", "Location": "string",
			"StartTime": "time.Time", "EndTime": "time.Time", "AllDay": "bool", "Private": "bool",
			"Conflict": "bool", "BackToBack": "bool", "PrepStatus": "string",
		})
		assertExactFields(t, reflect.TypeFor[BriefMeetingItem](), map[string]string{
			"Ref": "dailybrief.SourceRef", "Title": "string", "StartTime": "string", "EndTime": "string",
			"AllDay": "bool", "Private": "bool", "Location": "string", "Conflict": "bool",
			"BackToBack": "bool", "PrepStatus": "string", "WhyPrepare": "string",
		})
		// A meeting's ref may name its calendar, never more of the event.
		assertExactFields(t, reflect.TypeFor[SourceRef](), map[string]string{
			"WorkspaceID": "string", "WorkspaceSlug": "string", "EntityType": "string", "EntityID": "string",
			"Timestamp": "time.Time", "AccountID": "string", "CalendarID": "string",
		})
	})

	t.Run("the meeting count is capped", func(t *testing.T) {
		start := time.Date(2026, 9, 24, 8, 0, 0, 0, time.UTC)
		events := make([]CalendarEventSnapshot, 0, maxCalendarEventsPerBrief+4)
		for i := 0; i < maxCalendarEventsPerBrief+4; i++ {
			at := start.Add(time.Duration(i) * 30 * time.Minute)
			events = append(events, CalendarEventSnapshot{
				Ref:   SourceRef{WorkspaceID: "cal-ws", EntityType: EntityCalendarEvent, EntityID: fmt.Sprintf("m-%02d", i), CalendarID: "primary"},
				Title: "M", StartTime: at, EndTime: at.Add(20 * time.Minute),
			})
		}
		snap := BuildSnapshot(context.Background(), calendarSources(fakeCalendar{events: events}),
			Config{Scope: ScopeAll}, "local", start)
		if len(snap.CalendarEvents) != maxCalendarEventsPerBrief {
			t.Fatalf("got %d meetings, want the cap of %d", len(snap.CalendarEvents), maxCalendarEventsPerBrief)
		}
		if maxCalendarEventsPerBrief != 8 {
			t.Fatalf("maxCalendarEventsPerBrief = %d; Issue #533 fixed it at 8", maxCalendarEventsPerBrief)
		}
	})

	t.Run("every meeting is in the synthesis allowlist", func(t *testing.T) {
		start := time.Date(2026, 9, 24, 9, 0, 0, 0, time.UTC)
		snap := BuildSnapshot(context.Background(), calendarSources(fakeCalendar{events: []CalendarEventSnapshot{
			calendarEvent("standup", start, 30*time.Minute),
			calendarEvent("design", start.Add(2*time.Hour), time.Hour),
		}}), Config{Scope: ScopeAll}, "local", start)
		allowed := snap.AllRefs()
		for _, evt := range snap.CalendarEvents {
			if ref, ok := allowed[evt.Ref.Key()]; !ok || ref.EntityType != EntityCalendarEvent || ref.CalendarID != "primary" {
				t.Fatalf("meeting %q missing from the allowlist or altered: %+v", evt.Ref.EntityID, ref)
			}
		}
	})
}

// assertOnlyAllowedCalendarFields fails on any field of typ whose name
// mentions "calendar" and is not in allowed.
func assertOnlyAllowedCalendarFields(t *testing.T, typ reflect.Type, allowed ...string) {
	t.Helper()
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		if strings.Contains(strings.ToLower(name), "calendar") && !slices.Contains(allowed, name) {
			t.Fatalf("%s.%s: a calendar-derived field outside the bounded contract (Issue #533). "+
				"Extend the contract deliberately, and this guard with it.", typ.Name(), name)
		}
	}
}

// assertExactFields pins typ's field names and types.
func assertExactFields(t *testing.T, typ reflect.Type, want map[string]string) {
	t.Helper()
	got := make(map[string]string, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		got[typ.Field(i).Name] = typ.Field(i).Type.String()
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s fields = %v\nwant %v\n"+
			"Widening what a meeting carries into the brief (a description, attendees, "+
			"a conference or source link) is a contract change: see Issue #533.", typ.Name(), got, want)
	}
}
