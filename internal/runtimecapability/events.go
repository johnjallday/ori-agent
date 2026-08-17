package runtimecapability

import "github.com/johnjallday/ori-agent/internal/logger"

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
)

// emitRuntimeEvent is the single structured-log exit point and is indirected
// so redaction tests capture exactly what would be logged. Its field vocabulary
// is fixed to compiled identifiers and safe categories: never workspace IDs,
// paths, ports, filenames, command IDs, adapter errors, or summaries.
var emitRuntimeEvent = func(_ string, fields logger.Fields) {
	logger.Info("Runtime capability event", fields)
}

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
