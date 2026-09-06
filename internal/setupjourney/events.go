package setupjourney

import (
	"github.com/johnjallday/ori-agent/internal/specialist"
	"github.com/johnjallday/ori-agent/internal/specialistevents"
)

var recordSpecialistEvent = specialistevents.Record

func journeyEventFields(projection *JourneyProjection) specialistevents.Fields {
	if projection == nil {
		return specialistevents.Fields{}
	}
	return specialistevents.Fields{
		JourneyID: projection.Journey.ID, RunKind: string(projection.RunKind),
		Lifecycle: string(projection.Lifecycle), SchemaVersion: projection.Journey.SchemaVersion,
		DeclarationVersion: projection.Journey.Version,
	}
}

func runEventFields(run *Run, declaration *specialist.SetupJourney) specialistevents.Fields {
	if run == nil || declaration == nil {
		return specialistevents.Fields{}
	}
	return specialistevents.Fields{
		JourneyID: declaration.ID, RunKind: string(run.Kind), Lifecycle: string(run.Lifecycle),
		SchemaVersion: declaration.SchemaVersion, DeclarationVersion: declaration.Version,
	}
}

func emitPresentationEvent(name specialistevents.Name, projection *JourneyProjection) {
	recordSpecialistEvent(name, journeyEventFields(projection))
}

func emitReviewEvent(projection *JourneyProjection, stepID string, action ActionID) {
	fields := journeyEventFields(projection)
	fields.StepID = stepID
	fields.ActionID = string(action)
	fields.Outcome = specialistevents.OutcomeReviewOpened
	switch action {
	case ActionReviewInstall, ActionReviewEnable, ActionReviewUpdate:
		recordSpecialistEvent(specialistevents.IntegrationReviewOpened, fields)
	case ActionReviewExistingProject:
		fields.RouteToken = "existing_project"
		fields.Outcome = specialistevents.OutcomeSelected
		recordSpecialistEvent(specialistevents.ProjectRouteSelected, fields)
	case ActionReviewNewProject:
		fields.RouteToken = "new_project"
		fields.Outcome = specialistevents.OutcomeSelected
		recordSpecialistEvent(specialistevents.ProjectRouteSelected, fields)
	}
}

func emitActionOutcome(projection *JourneyProjection, stepID string, action ActionID, outcome specialistevents.Outcome, reason ReasonCode) {
	fields := journeyEventFields(projection)
	fields.StepID = stepID
	fields.ActionID = string(action)
	fields.Outcome = outcome
	fields.ReasonCode = string(reason)
	switch action {
	case ActionInstall, ActionEnable, ActionUpdate:
		recordSpecialistEvent(specialistevents.IntegrationOutcome, fields)
	case ActionConnectExistingProject:
		fields.RouteToken = "existing_project"
		recordSpecialistEvent(specialistevents.ProjectOutcome, fields)
	case ActionCreateNewProject:
		fields.RouteToken = "new_project"
		recordSpecialistEvent(specialistevents.ProjectOutcome, fields)
	case ActionSelectFileOnlyMode:
		// runtimecapability.SelectMode is the canonical event owner.
	case ActionAddHomeStaffing:
		recordSpecialistEvent(specialistevents.HomeRoleOutcome, fields)
	case ActionAddProjectStaffing:
		recordSpecialistEvent(specialistevents.ProjectTeamOutcome, fields)
	case ActionAddOptionalHomeStaffing:
		recordSpecialistEvent(specialistevents.SampleAddonOutcome, fields)
	}
}

func emitProjectionLifecycleTransition(before, after *JourneyProjection) {
	if before == nil || after == nil {
		return
	}
	fields := journeyEventFields(after)
	switch {
	case before.FirstCompletedAt == nil && after.FirstCompletedAt != nil:
		fields.Outcome = specialistevents.OutcomeSucceeded
		recordSpecialistEvent(specialistevents.JourneyCompleted, fields)
	case before.Lifecycle == LifecycleReady && after.Lifecycle == LifecycleNeedsAttention:
		for _, current := range after.Steps {
			if current.ID == after.CurrentStepID {
				fields.StepID = current.ID
				fields.ReasonCode = string(current.ReasonCode)
				break
			}
		}
		fields.Outcome = specialistevents.OutcomeFailed
		recordSpecialistEvent(specialistevents.JourneyRegressed, fields)
	}
}

func emitLifecycleTransition(declaration *specialist.SetupJourney, before, after *Run) {
	if declaration == nil || before == nil || after == nil {
		return
	}
	fields := runEventFields(after, declaration)
	switch {
	case before.FirstCompletedAt == nil && after.FirstCompletedAt != nil:
		fields.Outcome = specialistevents.OutcomeSucceeded
		recordSpecialistEvent(specialistevents.JourneyCompleted, fields)
	case before.Lifecycle == LifecycleReady && after.Lifecycle == LifecycleNeedsAttention:
		for _, current := range after.StepStates {
			if current.StepID == after.CurrentStepID {
				fields.StepID = current.StepID
				fields.ReasonCode = string(current.ReasonCode)
				break
			}
		}
		fields.Outcome = specialistevents.OutcomeFailed
		recordSpecialistEvent(specialistevents.JourneyRegressed, fields)
	}
}
