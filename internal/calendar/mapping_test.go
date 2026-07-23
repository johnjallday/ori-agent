package calendar

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func googleShapedMapping() workspace.CapabilityMapping {
	return workspace.CapabilityMapping{
		Capability: CapabilityKey,
		Operations: map[string]workspace.OperationMapping{
			OpListCalendars: {
				Tool:             "calendars_list",
				ResultCollection: "/items",
				Fields: map[string]string{
					"id":        "/id",
					"name":      "/summary",
					"primary":   "/primary",
					"time_zone": "/timeZone",
				},
			},
			OpListEvents: {
				Tool:             "events_list",
				ResultCollection: "/items",
				Fields: map[string]string{
					"id":              "/id",
					"title":           "/summary",
					"description":     "/description",
					"location":        "/location",
					"start_time":      "/start/dateTime",
					"end_time":        "/end/dateTime",
					"time_zone":       "/start/timeZone",
					"conference_link": "/hangoutLink",
					"source_link":     "/htmlLink",
				},
			},
			OpCreateEvent: {
				Tool: "events_insert",
				Arguments: map[string]string{
					"calendar_id": "/calendarId",
					"title":       "/summary",
					"start_time":  "/start/dateTime",
					"end_time":    "/end/dateTime",
				},
			},
		},
	}
}

func TestValidateMapping_GoogleShapedSucceeds(t *testing.T) {
	if err := ValidateMapping(googleShapedMapping()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateMapping_AlternateShapedSucceeds(t *testing.T) {
	mapping := workspace.CapabilityMapping{
		Capability: CapabilityKey,
		Operations: map[string]workspace.OperationMapping{
			OpListCalendars: {
				Tool:             "get_calendars",
				ResultCollection: "/data/calendars",
				Fields:           map[string]string{"id": "/calId", "name": "/label"},
			},
			OpListEvents: {
				Tool:             "get_events",
				ResultCollection: "/results",
				Fields: map[string]string{
					"id":         "/eventId",
					"title":      "/name",
					"start_time": "/when/begins",
					"end_time":   "/when/ends",
				},
			},
		},
	}
	if err := ValidateMapping(mapping); err != nil {
		t.Fatalf("unexpected error for a differently-shaped connector: %v", err)
	}
}

func TestValidateMapping_WrongCapabilityKey(t *testing.T) {
	mapping := googleShapedMapping()
	mapping.Capability = "email"
	if err := ValidateMapping(mapping); err == nil {
		t.Fatal("expected error for wrong capability key")
	}
}

func TestValidateMapping_MissingRequiredOperation(t *testing.T) {
	mapping := googleShapedMapping()
	delete(mapping.Operations, OpListEvents)
	if err := ValidateMapping(mapping); err == nil {
		t.Fatal("expected error for missing required operation")
	}
}

func TestValidateMapping_UnknownOperationRejected(t *testing.T) {
	mapping := googleShapedMapping()
	mapping.Operations["delete_event"] = workspace.OperationMapping{Tool: "events_delete"}
	if err := ValidateMapping(mapping); err == nil {
		t.Fatal("expected error for an unrecognized operation name")
	}
}

func TestValidateMapping_MalformedPointerRejected(t *testing.T) {
	mapping := googleShapedMapping()
	op := mapping.Operations[OpListEvents]
	op.Fields["title"] = "summary" // missing leading '/'
	mapping.Operations[OpListEvents] = op
	if err := ValidateMapping(mapping); err == nil {
		t.Fatal("expected error for a malformed json pointer")
	}
}

func TestValidateMapping_MissingRequiredReadFieldRejected(t *testing.T) {
	mapping := googleShapedMapping()
	op := mapping.Operations[OpListEvents]
	delete(op.Fields, "title") // "title" is required per the calendar contract
	mapping.Operations[OpListEvents] = op
	if err := ValidateMapping(mapping); err == nil {
		t.Fatal("expected error for a missing required output field mapping")
	}
}

func TestValidateMapping_MissingRequiredWriteArgumentRejected(t *testing.T) {
	mapping := googleShapedMapping()
	op := mapping.Operations[OpCreateEvent]
	delete(op.Arguments, "calendar_id") // required per the calendar contract
	mapping.Operations[OpCreateEvent] = op
	if err := ValidateMapping(mapping); err == nil {
		t.Fatal("expected error for a missing required argument mapping")
	}
}

func TestValidateMapping_EmptyToolNameRejected(t *testing.T) {
	mapping := googleShapedMapping()
	op := mapping.Operations[OpListCalendars]
	op.Tool = ""
	mapping.Operations[OpListCalendars] = op
	if err := ValidateMapping(mapping); err == nil {
		t.Fatal("expected error for an operation with no tool name")
	}
}

func TestRequiredFieldsFor_UnknownOperation(t *testing.T) {
	if _, _, ok := RequiredFieldsFor("not_a_real_operation"); ok {
		t.Fatal("expected ok=false for an unrecognized operation")
	}
}

func TestIsCollectionOperation(t *testing.T) {
	if !IsCollectionOperation(OpListEvents) {
		t.Fatal("list_events should be a collection operation")
	}
	if IsCollectionOperation(OpCreateEvent) {
		t.Fatal("create_event should not be a collection operation")
	}
}
