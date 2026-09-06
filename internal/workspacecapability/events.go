package workspacecapability

import (
	"github.com/johnjallday/ori-agent/internal/specialistevents"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const (
	eventActionInstallCapability = "install_capability"
	eventActionRemoveCapability  = "remove_capability"
)

var recordSpecialistEvent = specialistevents.Record

func recordCapabilityOutcome(capabilityID, action string, err error, count int) {
	if workspace.NormalizeCapabilityID(capabilityID) != workspace.CapabilitySampleLibrary {
		return
	}
	outcome := specialistevents.OutcomeSucceeded
	reasonCode := ""
	if err != nil {
		outcome = specialistevents.OutcomeFailed
		reasonCode = "capability_operation_failed"
	} else if action == eventActionRemoveCapability {
		outcome = specialistevents.OutcomeRevoked
	}
	recordSpecialistEvent(specialistevents.SampleCapabilityOutcome, specialistevents.Fields{
		ActionID:   action,
		Outcome:    outcome,
		ReasonCode: reasonCode,
		Count:      count,
	})
}
