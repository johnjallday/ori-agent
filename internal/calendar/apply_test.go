package calendar

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestApplyEvent_GoogleShaped(t *testing.T) {
	result := map[string]any{
		"items": []any{
			map[string]any{
				"id":          "evt123",
				"summary":     "Team Sync",
				"description": "Weekly sync",
				"location":    "Room 1",
				"start":       map[string]any{"dateTime": "2026-07-20T10:00:00Z", "timeZone": "UTC"},
				"end":         map[string]any{"dateTime": "2026-07-20T10:30:00Z", "timeZone": "UTC"},
				"hangoutLink": "https://meet.google.com/xyz",
				"htmlLink":    "https://calendar.google.com/event?eid=abc",
				"attendees": []any{
					map[string]any{"email": "a@example.com", "display_name": "A", "response_status": "accepted", "organizer": true},
				},
			},
		},
	}

	mapping := googleShapedMapping()
	op := mapping.Operations[OpListEvents]
	op.Fields["attendees"] = "/attendees"
	mapping.Operations[OpListEvents] = op

	items, err := Collection(result, op)
	if err != nil {
		t.Fatalf("Collection error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	event := ApplyEvent(items[0], op)
	if event.ID != "evt123" || event.Title != "Team Sync" || event.StartTime != "2026-07-20T10:00:00Z" || event.EndTime != "2026-07-20T10:30:00Z" {
		t.Fatalf("unexpected event: %+v", event)
	}
	if event.ConferenceLink != "https://meet.google.com/xyz" || event.SourceLink != "https://calendar.google.com/event?eid=abc" {
		t.Fatalf("unexpected links: %+v", event)
	}
	if len(event.Attendees) != 1 || event.Attendees[0].Email != "a@example.com" || !event.Attendees[0].Organizer {
		t.Fatalf("unexpected attendees: %+v", event.Attendees)
	}
}

func TestApplyEvent_AlternateShaped(t *testing.T) {
	result := map[string]any{
		"results": []any{
			map[string]any{
				"eventId": "e-42",
				"name":    "Sprint Planning",
				"when":    map[string]any{"begins": "2026-07-21T15:00:00Z", "ends": "2026-07-21T16:00:00Z"},
				"allDay":  false,
				"private": true,
			},
		},
	}

	op := workspace.OperationMapping{
		Tool:             "get_events",
		ResultCollection: "/results",
		Fields: map[string]string{
			"id":         "/eventId",
			"title":      "/name",
			"start_time": "/when/begins",
			"end_time":   "/when/ends",
			"all_day":    "/allDay",
			"private":    "/private",
		},
	}

	items, err := Collection(result, op)
	if err != nil {
		t.Fatalf("Collection error: %v", err)
	}
	event := ApplyEvent(items[0], op)
	if event.ID != "e-42" || event.Title != "Sprint Planning" {
		t.Fatalf("unexpected event: %+v", event)
	}
	if event.StartTime != "2026-07-21T15:00:00Z" || event.EndTime != "2026-07-21T16:00:00Z" {
		t.Fatalf("unexpected times: %+v", event)
	}
	if event.AllDay || !event.Private {
		t.Fatalf("unexpected flags: %+v", event)
	}
}

func TestApplyEvent_MissingOrWrongTypeFieldsStayZero(t *testing.T) {
	// The connector's "status" field is a string ("confirmed"), not a JSON
	// boolean -- ApplyEvent must never coerce or reinterpret it, only accept a
	// literal JSON bool. This is the "no LLM reinterpretation" guarantee.
	item := map[string]any{
		"id":     "evt-1",
		"title":  "Untitled",
		"status": "confirmed",
	}
	op := workspace.OperationMapping{
		Fields: map[string]string{
			"id":       "/id",
			"title":    "/title",
			"canceled": "/status", // wrong type on purpose
		},
	}
	event := ApplyEvent(item, op)
	if event.Canceled != false {
		t.Fatalf("expected Canceled to stay false for a non-boolean source value, got %v", event.Canceled)
	}
	if event.StartTime != "" || event.EndTime != "" {
		t.Fatalf("expected unmapped fields to stay zero-valued, got: %+v", event)
	}
}

func TestApplyCalendar(t *testing.T) {
	item := map[string]any{"id": "primary", "summary": "user@example.com", "primary": true, "timeZone": "America/New_York"}
	op := workspace.OperationMapping{
		Fields: map[string]string{"id": "/id", "name": "/summary", "primary": "/primary", "time_zone": "/timeZone"},
	}
	cal := ApplyCalendar(item, op)
	if cal.ID != "primary" || cal.Name != "user@example.com" || !cal.Primary || cal.TimeZone != "America/New_York" {
		t.Fatalf("unexpected calendar: %+v", cal)
	}
}

func TestCollection_NonArrayResultCollectionErrors(t *testing.T) {
	result := map[string]any{"items": "not-an-array"}
	op := workspace.OperationMapping{ResultCollection: "/items"}
	if _, err := Collection(result, op); err == nil {
		t.Fatal("expected error when result_collection does not resolve to an array")
	}
}

func TestCollection_MissingResultCollectionErrors(t *testing.T) {
	result := map[string]any{"other": []any{}}
	op := workspace.OperationMapping{ResultCollection: "/items"}
	if _, err := Collection(result, op); err == nil {
		t.Fatal("expected error when result_collection does not resolve at all")
	}
}

func TestBuildArguments(t *testing.T) {
	op := workspace.OperationMapping{
		Arguments: map[string]string{
			"calendar_id": "/calendarId",
			"title":       "/summary",
			"start_time":  "/start/dateTime",
		},
	}
	input := map[string]any{
		"calendar_id": "primary",
		"title":       "Standup",
		"start_time":  "2026-07-20T10:00:00Z",
		"unused":      "ignored",
	}
	args, err := BuildArguments(input, op)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if args["calendarId"] != "primary" || args["summary"] != "Standup" {
		t.Fatalf("unexpected args: %+v", args)
	}
	startBlock, ok := args["start"].(map[string]any)
	if !ok || startBlock["dateTime"] != "2026-07-20T10:00:00Z" {
		t.Fatalf("unexpected nested args: %+v", args)
	}
	if _, present := args["unused"]; present {
		t.Fatal("expected an unmapped canonical field to be dropped, not guessed at")
	}
}
