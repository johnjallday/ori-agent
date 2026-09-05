package runtimecapability

import (
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/specialistevents"
)

const (
	EventDurableCheckFailed = "runtime_capability.durable_check_failed"
	EventLiveCheckFailed    = "runtime_capability.live_check_failed"
	EventGrantDecision      = "runtime_capability.grant_decision"
	EventRevokeDecision     = "runtime_capability.revoke_decision"
	EventScopeUseDecision   = "runtime_capability.scope_use_decision"
	eventFieldName          = "event"
	eventFieldAdapter       = "adapter"
	eventFieldCategory      = "error_category"
	eventFieldWorkspace     = "workspace_id"
	eventFieldAgent         = "agent_instance_id"
	eventFieldCapability    = "capability"
	eventFieldOutcome       = "outcome"
	eventActionSelectMode   = "select_mode"
	eventActionLiveVerify   = "live_verify"
)

// emitRuntimeEvent is the single structured-log exit point and is indirected
// so redaction tests capture exactly what would be logged. Its field vocabulary
// is fixed to compiled identifiers and safe categories: never workspace IDs,
// paths, ports, filenames, command IDs, adapter errors, or summaries.
var emitRuntimeEvent = func(_ string, fields logger.Fields) {
	logger.Info("Runtime capability event", fields)
}

var recordSpecialistEvent = specialistevents.Record

func runtimeAuditEvent(name, workspaceID, agentInstanceID, capability, outcome string) {
	fields := logger.Fields{eventFieldName: name}
	for key, value := range map[string]string{
		eventFieldWorkspace:  workspaceID,
		eventFieldAgent:      agentInstanceID,
		eventFieldCapability: capability,
		eventFieldOutcome:    outcome,
	} {
		if safe := safeCode(value); safe != "" {
			fields[key] = safe
		}
	}
	emitRuntimeEvent(name, fields)
}

func runtimeFailureEvent(name, adapter, category string) {
	fields := logger.Fields{
		eventFieldName:     name,
		eventFieldCategory: safeCode(category),
	}
	if id := safeCode(adapter); id != "" {
		fields[eventFieldAdapter] = id
	}
	emitRuntimeEvent(name, fields)
}

func recordModeSelection(modeID string) {
	recordSpecialistEvent(specialistevents.ModeSelected, specialistevents.Fields{
		ActionID:  eventActionSelectMode,
		ModeToken: modeID,
		Outcome:   specialistevents.OutcomeSelected,
	})
}

func recordLiveVerification(requirementKey string, succeeded bool, reasonCode string) {
	outcome := specialistevents.OutcomeFailed
	if succeeded {
		outcome = specialistevents.OutcomeSucceeded
		reasonCode = ""
	}
	recordSpecialistEvent(specialistevents.LiveVerifyOutcome, specialistevents.Fields{
		ActionID:   eventActionLiveVerify,
		ResourceID: requirementKey,
		Outcome:    outcome,
		ReasonCode: reasonCode,
	})
}
