package calendarhttp

import (
	"strings"
	"testing"
)

func validCreateRequest() mutationRequest {
	return mutationRequest{
		WorkspaceID: "ws-cal",
		Operation:   "create_event",
		CalendarID:  "primary",
		Title:       "Team Sync",
		StartTime:   "2026-07-20T10:00:00Z",
		EndTime:     "2026-07-20T11:00:00Z",
		TimeZone:    "America/New_York",
		Attendees:   []mutationAttendee{{Email: "alice@example.com", DisplayName: "Alice"}},
	}
}

func TestValidateAndNormalizeMutation_AcceptsValidCreate(t *testing.T) {
	payload, errs := validateAndNormalizeMutation(validCreateRequest())
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
	if payload.Operation != "create_event" || payload.Title != "Team Sync" {
		t.Fatalf("unexpected normalized payload: %+v", payload)
	}
}

func TestValidateAndNormalizeMutation_RejectsUnsupportedOperations(t *testing.T) {
	for _, op := range []string{"delete_event", "rsvp", "recurring_update", "connect_account", ""} {
		req := validCreateRequest()
		req.Operation = op
		_, errs := validateAndNormalizeMutation(req)
		if len(errs) == 0 {
			t.Errorf("operation %q must be rejected", op)
		}
	}
}

func TestValidateAndNormalizeMutation_UpdateRequiresEventID(t *testing.T) {
	req := validCreateRequest()
	req.Operation = "update_event"
	// no EventID set
	_, errs := validateAndNormalizeMutation(req)
	if !containsSubstring(errs, "event_id is required") {
		t.Fatalf("expected an event_id-required error, got %v", errs)
	}
}

func TestValidateAndNormalizeMutation_CreateRejectsEventID(t *testing.T) {
	req := validCreateRequest()
	req.EventID = "evt-123"
	_, errs := validateAndNormalizeMutation(req)
	if !containsSubstring(errs, "must not be set") {
		t.Fatalf("expected create_event to reject an event_id, got %v", errs)
	}
}

func TestValidateAndNormalizeMutation_RejectsInvalidTimeOrdering(t *testing.T) {
	req := validCreateRequest()
	req.StartTime, req.EndTime = "2026-07-20T11:00:00Z", "2026-07-20T10:00:00Z" // end before start
	_, errs := validateAndNormalizeMutation(req)
	if !containsSubstring(errs, "end_time must be strictly after start_time") {
		t.Fatalf("expected an ordering error, got %v", errs)
	}
}

func TestValidateAndNormalizeMutation_RejectsEqualStartEnd(t *testing.T) {
	req := validCreateRequest()
	req.StartTime, req.EndTime = "2026-07-20T10:00:00Z", "2026-07-20T10:00:00Z"
	_, errs := validateAndNormalizeMutation(req)
	if !containsSubstring(errs, "end_time must be strictly after start_time") {
		t.Fatalf("expected an ordering error for equal start/end, got %v", errs)
	}
}

func TestValidateAndNormalizeMutation_RejectsMalformedTimes(t *testing.T) {
	req := validCreateRequest()
	req.StartTime = "not-a-time"
	_, errs := validateAndNormalizeMutation(req)
	if !containsSubstring(errs, "start_time") {
		t.Fatalf("expected a start_time error, got %v", errs)
	}
}

func TestValidateAndNormalizeMutation_RejectsInvalidTimeZone(t *testing.T) {
	req := validCreateRequest()
	req.TimeZone = "Not/AZone"
	_, errs := validateAndNormalizeMutation(req)
	if !containsSubstring(errs, "not a recognized IANA zone") {
		t.Fatalf("expected a timezone error, got %v", errs)
	}
}

func TestValidateAndNormalizeMutation_RejectsMissingTitle(t *testing.T) {
	req := validCreateRequest()
	req.Title = "   "
	_, errs := validateAndNormalizeMutation(req)
	if !containsSubstring(errs, "title is required") {
		t.Fatalf("expected a title error, got %v", errs)
	}
}

func TestValidateAndNormalizeMutation_RejectsMissingCalendarID(t *testing.T) {
	req := validCreateRequest()
	req.CalendarID = ""
	_, errs := validateAndNormalizeMutation(req)
	if !containsSubstring(errs, "calendar_id is required") {
		t.Fatalf("expected a calendar_id error, got %v", errs)
	}
}

func TestValidateAndNormalizeMutation_RejectsAttendeeWithNoDisplay(t *testing.T) {
	req := validCreateRequest()
	req.Attendees = []mutationAttendee{{}} // neither email nor display name
	_, errs := validateAndNormalizeMutation(req)
	if !containsSubstring(errs, "must have an email or a display name") {
		t.Fatalf("expected an attendee display error, got %v", errs)
	}
}

func TestValidateAndNormalizeMutation_RejectsMalformedAttendeeEmail(t *testing.T) {
	req := validCreateRequest()
	req.Attendees = []mutationAttendee{{Email: "not-an-email", DisplayName: "Bob"}}
	_, errs := validateAndNormalizeMutation(req)
	if !containsSubstring(errs, "is not valid") {
		t.Fatalf("expected an invalid-email error, got %v", errs)
	}
}

func TestValidateAndNormalizeMutation_AcceptsDisplayNameOnlyAttendee(t *testing.T) {
	req := validCreateRequest()
	req.Attendees = []mutationAttendee{{DisplayName: "Anonymous Room"}}
	_, errs := validateAndNormalizeMutation(req)
	if len(errs) != 0 {
		t.Fatalf("a display-name-only attendee should be valid, got %v", errs)
	}
}

func TestValidateAndNormalizeMutation_AttendeeOrderDoesNotAffectPayload(t *testing.T) {
	req1 := validCreateRequest()
	req1.Attendees = []mutationAttendee{{Email: "b@example.com"}, {Email: "a@example.com"}}
	req2 := validCreateRequest()
	req2.Attendees = []mutationAttendee{{Email: "a@example.com"}, {Email: "b@example.com"}}

	p1, errs1 := validateAndNormalizeMutation(req1)
	p2, errs2 := validateAndNormalizeMutation(req2)
	if len(errs1) != 0 || len(errs2) != 0 {
		t.Fatalf("unexpected errors: %v / %v", errs1, errs2)
	}
	if hashMutationPayload(p1) != hashMutationPayload(p2) {
		t.Fatal("attendee submission order must not affect the normalized payload/hash")
	}
}

func containsSubstring(errs []string, needle string) bool {
	for _, e := range errs {
		if strings.Contains(e, needle) {
			return true
		}
	}
	return false
}
