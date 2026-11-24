package settingshttp

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/settingsreset"
)

// This is result-projection coverage, not a claim that the pending pre-start
// applier has produced a real mixed outcome. Keep that integration delivery gate.
func TestResetOutcomeMixedResultDoesNotHideFailure(t *testing.T) {
	op := settingsreset.Operation{SchemaVersion: settingsreset.SchemaVersion, Intent: settingsreset.IntentSelectedData,
		State: settingsreset.StatePartialFailure, Restart: settingsreset.RestartInfo{Mode: settingsreset.RestartProcessRelaunch},
		Results: []settingsreset.CategoryResult{
			{ID: settingsreset.CategoryAgents, Outcome: settingsreset.OutcomeCompleted, Checks: []settingsreset.CheckResult{
				{Name: "old_profiles_absent", Outcome: settingsreset.OutcomeCompleted}, {Name: "agent_rehydration_disabled", Outcome: settingsreset.OutcomeCompleted}}},
			{ID: settingsreset.CategorySettings, Outcome: settingsreset.OutcomeFailed, Retryable: true},
		}}
	result := operationResponse(op)
	if result.Success || !result.RequiresRestart || len(result.ResetItems) != 1 || result.ResetItems[0] != "agents" {
		t.Fatal("mixed result lost the failure or verified category:", result)
	}
	op.State = settingsreset.StateCompleted // A contradictory state is insufficient.
	if operationResponse(op).Success {
		t.Fatal("overall state hid a failed category")
	}
}

// A canceled receipt applied nothing and awaits nothing: it is neither a
// success, nor a reset still pending a restart, nor does it list any category.
func TestResetOutcomeCanceledIsNotSuccessNorPending(t *testing.T) {
	op := settingsreset.Operation{SchemaVersion: settingsreset.SchemaVersion, Intent: settingsreset.IntentStartFresh,
		State: settingsreset.StateCanceled, Revision: 4, Restart: settingsreset.RestartInfo{Mode: settingsreset.RestartProcessRelaunch},
		Cancellation: &settingsreset.Cancellation{FromRevision: 3, PriorBlocker: "recovery_scope_changed"},
		Results: []settingsreset.CategoryResult{
			{ID: settingsreset.CategorySettings, Outcome: settingsreset.OutcomePending},
			{ID: settingsreset.CategoryAgents, Outcome: settingsreset.OutcomePending},
		}}
	result := operationResponse(op)
	if result.Success || result.RequiresRestart || len(result.ResetItems) != 0 {
		t.Fatalf("a canceled receipt read as success or pending: %+v", result)
	}
}
