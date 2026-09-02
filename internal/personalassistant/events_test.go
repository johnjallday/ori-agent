package personalassistant

import (
	"fmt"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/logger"
)

func TestPersonalAssistantEventsUseClosedPrivacySafeSchema(t *testing.T) {
	all := []EventType{
		EventStateViewed, EventHireStarted, EventHireCompleted, EventPreviewCreated,
		EventFirstResultDone, EventTodayViewed, EventPaused, EventResumed, EventRecoverableFailure,
		EventHQQuestStarted, EventHQQuestDeferred, EventHQSetupStarted, EventHQActivated,
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

func TestHQQuestEventsCarryNoNamesScheduleOrQuestCopy(t *testing.T) {
	var captured []logger.Fields
	original := emitPersonalAssistantEvent
	emitPersonalAssistantEvent = func(_ EventType, fields logger.Fields) {
		captured = append(captured, fields)
	}
	t.Cleanup(func() { emitPersonalAssistantEvent = original })

	RecordHQQuestStarted("assistant-1", "hq_not_built")
	RecordHQQuestDeferred("assistant-1")
	RecordHQSetupStarted("assistant-1")
	RecordHQActivated("assistant-1", "ws-1", 420)

	if len(captured) != 4 {
		t.Fatalf("emitted %d events; want 4", len(captured))
	}
	// Everything a quest could plausibly leak: the assistant's chosen name, the
	// HQ name, a schedule, the mandate, a path, and the on-screen copy.
	forbidden := []string{
		"Atlas", "Personal HQ", "08:00", "mon", "America/New_York",
		"home base", "Let's give", "/Users/", "Build My HQ",
	}
	for _, fields := range captured {
		for key, value := range fields {
			if !eventFieldAllowlist[key] {
				t.Fatalf("non-allowlisted key %q", key)
			}
			rendered := fmt.Sprint(value)
			for _, leak := range forbidden {
				if strings.Contains(rendered, leak) {
					t.Fatalf("event field %s=%q leaked %q", key, rendered, leak)
				}
			}
		}
	}

	states := []string{
		fmt.Sprint(captured[0][eventFieldState]),
		fmt.Sprint(captured[1][eventFieldState]),
		fmt.Sprint(captured[2][eventFieldState]),
		fmt.Sprint(captured[3][eventFieldState]),
	}
	want := []string{"awaiting_hq", "awaiting_hq", "provisioning_hq", "active"}
	for i, state := range states {
		if state != want[i] {
			t.Fatalf("event %d state = %q; want %q", i, state, want[i])
		}
	}
	if captured[3][eventFieldWorkspaceID] != "ws-1" || captured[3][eventFieldDurationMS] != int64(420) {
		t.Fatalf("activation event = %v", captured[3])
	}
	if captured[1][eventFieldReasonCode] != "user_deferred" || captured[1][eventFieldRecoverable] != true {
		t.Fatalf("defer event = %v", captured[1])
	}
}
