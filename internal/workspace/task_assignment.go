package workspace

import "strings"

// TaskAssignmentMode records how a task's assignee was chosen. It is set by the
// shared coordinator assignment path (and the manual create/update path) so the
// UI and telemetry can distinguish coordinator decisions from user overrides.
type TaskAssignmentMode string

const (
	// TaskAssignmentModeStaticPlan — assigned by the coordinator during static planning.
	TaskAssignmentModeStaticPlan TaskAssignmentMode = "static_plan"
	// TaskAssignmentModeManual — assigned or overridden directly by the user.
	TaskAssignmentModeManual TaskAssignmentMode = "manual"
	// TaskAssignmentModeDynamicDelegation — assigned by the coordinator's runtime delegation loop.
	TaskAssignmentModeDynamicDelegation TaskAssignmentMode = "dynamic_delegation"
	// TaskAssignmentModeLegacyUnknown — provenance could not be determined; backfilled
	// onto tasks that predate assignment provenance during migration.
	TaskAssignmentModeLegacyUnknown TaskAssignmentMode = "legacy_unknown"
)

// TaskAssignedByManual is the sentinel AssignedBy value for user-driven assignments.
const TaskAssignedByManual = "manual"

// IsValidTaskAssignmentMode reports whether mode is one of the known values.
func IsValidTaskAssignmentMode(mode TaskAssignmentMode) bool {
	switch mode {
	case TaskAssignmentModeStaticPlan,
		TaskAssignmentModeManual,
		TaskAssignmentModeDynamicDelegation,
		TaskAssignmentModeLegacyUnknown:
		return true
	}
	return false
}

// backfillTaskAssignmentProvenance stamps legacy_unknown provenance on any task
// that predates assignment provenance. It never changes Task.To / AssignedNodeID —
// existing assignments are preserved exactly. Returns true if any task changed,
// so the caller knows whether the workspace needs to be persisted.
//
// This is idempotent: once a task has a non-empty AssignmentMode it is left alone.
func backfillTaskAssignmentProvenance(ws *Workspace) bool {
	if ws == nil {
		return false
	}
	changed := false
	for i := range ws.Tasks {
		if strings.TrimSpace(string(ws.Tasks[i].AssignmentMode)) == "" {
			ws.Tasks[i].AssignmentMode = TaskAssignmentModeLegacyUnknown
			changed = true
		}
	}
	return changed
}
