package samplelibrary

import (
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/specialistevents"
)

func TestSampleEventsContainOnlyClosedOutcomeData(t *testing.T) {
	original := recordSpecialistEvent
	t.Cleanup(func() { recordSpecialistEvent = original })
	type recorded struct {
		name   specialistevents.Name
		fields specialistevents.Fields
	}
	var events []recorded
	recordSpecialistEvent = func(name specialistevents.Name, fields specialistevents.Fields) {
		events = append(events, recorded{name: name, fields: fields})
	}

	recordSampleEvent(specialistevents.SampleHandoffOutcome, eventActionCopyToProject, specialistevents.OutcomeSucceeded, 2)
	recordSampleFailure(specialistevents.SampleRootOutcome, eventActionIndexRoot, errors.New("/private/audio.wav token=secret"))
	if len(events) != 2 {
		t.Fatalf("sample events = %+v", events)
	}
	if events[0].name != specialistevents.SampleHandoffOutcome || events[0].fields.ActionID != eventActionCopyToProject || events[0].fields.Outcome != specialistevents.OutcomeSucceeded || events[0].fields.Count != 2 {
		t.Fatalf("sample event = %+v", events[0])
	}
	if events[1].name != specialistevents.SampleRootOutcome || events[1].fields.Outcome != specialistevents.OutcomeFailed || events[1].fields.ReasonCode != "sample_operation_failed" {
		t.Fatalf("sample failure event = %+v", events[1])
	}
	for _, event := range events {
		if event.fields.ResourceID != "" || event.fields.RoleID != "" || event.fields.JourneyID != "" {
			t.Fatalf("sample event included unrelated identity: %+v", event.fields)
		}
	}
}
