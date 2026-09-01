package personalassistant

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/sensitive"
)

// EventType is the closed, local-only PAF lifecycle vocabulary. Events are
// structured logs, not an external analytics pipeline.
type EventType string

const (
	EventStateViewed        EventType = "personal_assistant.state_viewed"
	EventHireStarted        EventType = "personal_assistant.hire_started"
	EventHireCompleted      EventType = "personal_assistant.hire_completed"
	EventPreviewCreated     EventType = "personal_assistant.preview_created"
	EventFirstResultDone    EventType = "personal_assistant.first_result_completed"
	EventTodayViewed        EventType = "personal_assistant.today_viewed"
	EventPaused             EventType = "personal_assistant.paused"
	EventResumed            EventType = "personal_assistant.resumed"
	EventRecoverableFailure EventType = "personal_assistant.recoverable_failure"
)

const (
	eventFieldName        = "event"
	eventFieldAssistantID = "assistant_id"
	eventFieldWorkspaceID = "workspace_id"
	eventFieldState       = "state"
	eventFieldCount       = "count"
	eventFieldDurationMS  = "duration_ms"
	eventFieldRecoverable = "recoverable"
	eventFieldReasonCode  = "reason_code"
)

var (
	eventTokenPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
	eventTypes        = map[EventType]bool{
		EventStateViewed: true, EventHireStarted: true, EventHireCompleted: true,
		EventPreviewCreated: true, EventFirstResultDone: true, EventTodayViewed: true,
		EventPaused: true, EventResumed: true, EventRecoverableFailure: true,
	}
	eventFieldAllowlist = map[string]bool{
		eventFieldName: true, eventFieldAssistantID: true, eventFieldWorkspaceID: true,
		eventFieldState: true, eventFieldCount: true, eventFieldDurationMS: true,
		eventFieldRecoverable: true, eventFieldReasonCode: true,
	}
	eventStates = map[string]bool{
		"needs_hire": true, "not_hired": true, "hiring": true,
		"active": true, "paused": true, "repair_needed": true, "not_started": true,
		"previewed": true, "applying": true, "completed": true, "failed": true,
		"superseded": true,
	}
	eventReasonCodes = map[string]bool{
		"assignment_partial": true, "hq_creation": true, "designation": true,
		"daily_brief_config": true, "relationship_finalization": true,
	}
)

// EventData deliberately has no names, prose, prompts, paths, content, or raw
// source payloads. Zero values are omitted.
type EventData struct {
	AssistantID string
	WorkspaceID string
	State       string
	Count       int
	DurationMS  int64
	Recoverable bool
	ReasonCode  string
}

var emitPersonalAssistantEvent = func(_ EventType, fields logger.Fields) {
	logger.Info("Personal assistant event", fields)
}

// RecordStateViewed is called only by the user-facing state HTTP read, not by
// internal projections that happen to reuse Service.Get.
func RecordStateViewed(projection *Projection) {
	if projection == nil {
		return
	}
	recordEvent(EventStateViewed, EventData{
		AssistantID: projection.AssistantID, WorkspaceID: projection.HQWorkspaceID, State: string(projection.State),
	})
}

func recordEvent(event EventType, data EventData) {
	fields, err := eventFields(event, data)
	if err != nil {
		return
	}
	emitPersonalAssistantEvent(event, fields)
}

func eventFields(event EventType, data EventData) (logger.Fields, error) {
	if !eventTypes[event] {
		return nil, fmt.Errorf("personal assistant: unsupported event %q", event)
	}
	fields := logger.Fields{eventFieldName: string(event)}
	for key, value := range map[string]string{
		eventFieldAssistantID: data.AssistantID,
		eventFieldWorkspaceID: data.WorkspaceID,
		eventFieldState:       data.State,
		eventFieldReasonCode:  data.ReasonCode,
	} {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if !eventTokenPattern.MatchString(value) || sensitive.ContainsSecretLikeText(value) {
			return nil, fmt.Errorf("personal assistant: unsafe event field %s", key)
		}
		fields[key] = value
	}
	if data.State != "" && !eventStates[strings.TrimSpace(data.State)] {
		return nil, errors.New("personal assistant: event state is not a closed value")
	}
	if data.ReasonCode != "" && !eventReasonCodes[strings.TrimSpace(data.ReasonCode)] {
		return nil, errors.New("personal assistant: event reason is not a closed value")
	}
	if data.Count < 0 || data.DurationMS < 0 {
		return nil, errors.New("personal assistant: event counts and durations cannot be negative")
	}
	if data.Count > 0 {
		fields[eventFieldCount] = data.Count
	}
	if data.DurationMS > 0 {
		fields[eventFieldDurationMS] = data.DurationMS
	}
	if data.Recoverable {
		fields[eventFieldRecoverable] = true
	}
	return fields, nil
}
