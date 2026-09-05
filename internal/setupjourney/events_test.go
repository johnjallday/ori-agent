package setupjourney

import (
	"reflect"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/specialistevents"
)

func TestSpecialistEventMappingsAreClosedAndBounded(t *testing.T) {
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

	projection := &JourneyProjection{
		RunKind:   RunKindRoot,
		Lifecycle: LifecycleInProgress,
		Journey: DeclarationProjection{
			ID: "fixture-setup", SchemaVersion: 1, Version: 2,
		},
	}
	emitPresentationEvent(specialistevents.JourneyOpened, projection)
	emitPresentationEvent(specialistevents.JourneyDismissed, projection)
	emitPresentationEvent(specialistevents.JourneyResumed, projection)
	emitReviewEvent(projection, "integration", ActionReviewInstall)
	emitReviewEvent(projection, "project", ActionReviewExistingProject)
	emitReviewEvent(projection, "project", ActionReviewNewProject)
	emitActionOutcome(projection, "integration", ActionInstall, specialistevents.OutcomeSucceeded, "")
	emitActionOutcome(projection, "project", ActionConnectExistingProject, specialistevents.OutcomeSucceeded, "")
	emitActionOutcome(projection, "project", ActionCreateNewProject, specialistevents.OutcomeFailed, ReasonOperationFailed)
	emitActionOutcome(projection, "staffing", ActionAddHomeStaffing, specialistevents.OutcomeSucceeded, "")
	emitActionOutcome(projection, "staffing", ActionAddProjectStaffing, specialistevents.OutcomeReconcileRequired, ReasonOwnerUnavailable)
	emitActionOutcome(projection, "staffing", ActionAddOptionalHomeStaffing, specialistevents.OutcomeSucceeded, "")

	// Mode events come from runtimecapability.SelectMode, the canonical owner,
	// rather than being duplicated by the journey adapter.
	beforeMode := len(events)
	emitActionOutcome(projection, "workspace", ActionSelectFileOnlyMode, specialistevents.OutcomeSucceeded, "")
	if len(events) != beforeMode {
		t.Fatal("journey adapter duplicated the canonical mode event")
	}

	completedAt := time.Now().UTC()
	ready := *projection
	ready.Lifecycle = LifecycleReady
	ready.FirstCompletedAt = &completedAt
	emitProjectionLifecycleTransition(projection, &ready)
	regressed := ready
	regressed.Lifecycle = LifecycleNeedsAttention
	regressed.CurrentStepID = "workspace"
	regressed.Steps = []StepProjection{{ID: "workspace", ReasonCode: ReasonRuntimeNeedsAttention}}
	emitProjectionLifecycleTransition(&ready, &regressed)

	gotNames := make([]specialistevents.Name, len(events))
	for index, event := range events {
		gotNames[index] = event.name
		if event.fields.JourneyID != "fixture-setup" || event.fields.SchemaVersion != 1 || event.fields.DeclarationVersion != 2 {
			t.Fatalf("event %q lost declaration identity: %+v", event.name, event.fields)
		}
	}
	wantNames := []specialistevents.Name{
		specialistevents.JourneyOpened,
		specialistevents.JourneyDismissed,
		specialistevents.JourneyResumed,
		specialistevents.IntegrationReviewOpened,
		specialistevents.ProjectRouteSelected,
		specialistevents.ProjectRouteSelected,
		specialistevents.IntegrationOutcome,
		specialistevents.ProjectOutcome,
		specialistevents.ProjectOutcome,
		specialistevents.HomeRoleOutcome,
		specialistevents.ProjectTeamOutcome,
		specialistevents.SampleAddonOutcome,
		specialistevents.JourneyCompleted,
		specialistevents.JourneyRegressed,
	}
	if !reflect.DeepEqual(gotNames, wantNames) {
		t.Fatalf("events = %v, want %v", gotNames, wantNames)
	}
	if events[4].fields.RouteToken != "existing_project" || events[5].fields.RouteToken != "new_project" {
		t.Fatalf("route tokens = %q, %q", events[4].fields.RouteToken, events[5].fields.RouteToken)
	}
	last := events[len(events)-1]
	if last.fields.ReasonCode != string(ReasonRuntimeNeedsAttention) || last.fields.StepID != "workspace" {
		t.Fatalf("regression event = %+v", last.fields)
	}
}
