package runtimecapability

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/specialistevents"
)

func TestModeAndLiveVerificationEmitAtCanonicalOwners(t *testing.T) {
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

	adapter := &recordingAdapter{
		id:           "runtime_adapter",
		durable:      DurableResult{State: DurableConfigured, Summary: "Configured."},
		live:         LiveResult{State: LiveAvailable, Summary: "Available."},
		verification: VerificationResult{Succeeded: true, LiveState: LiveAvailable},
	}
	registry := NewRegistry()
	if err := registry.Register(adapter); err != nil {
		t.Fatal(err)
	}
	contract := contractWithRequirements("runtime")
	contract.Requirements[0].Adapter = adapter.ID()
	store := &runtimeStore{ws: runtimeWorkspace(contract)}
	service := NewService(store, registry)

	if _, err := service.SelectMode(context.Background(), store.ws.ID, "assisted"); err != nil {
		t.Fatalf("SelectMode: %v", err)
	}
	if _, err := service.Verify(context.Background(), store.ws.ID, "runtime"); err != nil {
		t.Fatalf("Verify success: %v", err)
	}
	adapter.verification = VerificationResult{
		Succeeded: false, LiveState: LiveCheckFailed,
		ReasonCode: "runner_failure", Summary: "The check failed.",
	}
	if _, err := service.Verify(context.Background(), store.ws.ID, "runtime"); err != nil {
		t.Fatalf("expected verification failure status: %v", err)
	}

	if len(events) != 3 {
		t.Fatalf("events = %+v", events)
	}
	if events[0].name != specialistevents.ModeSelected || events[0].fields.ModeToken != "assisted" || events[0].fields.Outcome != specialistevents.OutcomeSelected {
		t.Fatalf("mode event = %+v", events[0])
	}
	if events[1].name != specialistevents.LiveVerifyOutcome || events[1].fields.Outcome != specialistevents.OutcomeSucceeded || events[1].fields.ReasonCode != "" {
		t.Fatalf("successful verification event = %+v", events[1])
	}
	if events[2].name != specialistevents.LiveVerifyOutcome || events[2].fields.Outcome != specialistevents.OutcomeFailed || events[2].fields.ReasonCode != "runner_failure" {
		t.Fatalf("failed verification event = %+v", events[2])
	}
	for _, event := range events[1:] {
		if event.fields.ActionID != eventActionLiveVerify || event.fields.ResourceID != "runtime" {
			t.Fatalf("verification event leaked or lost its bounded identifiers: %+v", event)
		}
	}
}
