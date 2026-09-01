package personalassistant

import (
	"fmt"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/logger"
)

func TestPersonalAssistantEventsUseClosedPrivacySafeSchema(t *testing.T) {
	all := []EventType{
		EventEligibleViewed, EventHireStarted, EventHireCompleted, EventPreviewCreated,
		EventFirstResultDone, EventTodayViewed, EventPaused, EventResumed, EventRecoverableFailure,
	}
	for _, event := range all {
		fields, err := eventFields(event, EventData{
			AssistantID: "assistant-123", WorkspaceID: "hq-123", State: "active",
			Count: 2, DurationMS: 15, Recoverable: true, ReasonCode: "assignment_partial",
		})
		if err != nil {
			t.Fatalf("event %s: %v", event, err)
		}
		for key := range fields {
			if !eventFieldAllowlist[key] {
				t.Fatalf("event %s emitted non-allowlisted key %q", event, key)
			}
		}
	}
}

func TestPersonalAssistantEventsRejectFreeTextAndUnknownEvents(t *testing.T) {
	for _, unsafe := range []EventData{
		{ReasonCode: "remember that the launch is Friday"},
		{ReasonCode: "/Users/person/Documents/private.txt"},
		{ReasonCode: "person@example.com"},
		{ReasonCode: "sk-abcdefghijklmnopqrstuvwxyz"},
		{ReasonCode: "user_authored_word"},
		{State: "user_authored_word"},
	} {
		if _, err := eventFields(EventRecoverableFailure, unsafe); err == nil {
			t.Fatalf("secret/free text was accepted as event metadata: %+v", unsafe)
		}
	}
	if _, err := eventFields(EventType("personal_assistant.arbitrary"), EventData{}); err == nil {
		t.Fatal("unknown event type was accepted")
	}
}

func TestRecordEventEmitsOnlySanitizedFields(t *testing.T) {
	var captured logger.Fields
	original := emitPersonalAssistantEvent
	emitPersonalAssistantEvent = func(_ EventType, fields logger.Fields) { captured = fields }
	t.Cleanup(func() { emitPersonalAssistantEvent = original })
	recordEvent(EventPaused, EventData{AssistantID: "assistant-1", State: "paused"})
	if captured[eventFieldName] != string(EventPaused) || captured[eventFieldState] != "paused" {
		t.Fatalf("captured fields=%v", captured)
	}
	for key, value := range captured {
		if !eventFieldAllowlist[key] || strings.Contains(fmt.Sprint(value), "remember that") {
			t.Fatalf("unsafe event field %s=%v", key, value)
		}
	}
}
