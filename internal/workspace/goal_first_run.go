package workspace

import (
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspacesettings"
)

const (
	GoalFirstRunStarting = "starting"
	GoalFirstRunBlocked  = "blocked"
	GoalFirstRunAccepted = "accepted"
	GoalFirstRunFailed   = "failed"
	GoalFirstRunCleared  = "cleared"
)

const GoalReadConsentReason = "Choose Run now to read authorized project context and get goal suggestions. Start first look runs only the separate assessment; saving or opening this goal starts neither."

// GoalRequiresReadConsent uses semantic, persisted declarations, never names or
// project-file inspection. The provenance profile survives ordinary template
// copies and workspace preset overrides. Legacy records retain their existing
// profile/binding/host-task markers as conservative fallbacks.
func GoalRequiresReadConsent(ws *Workspace) bool {
	if ws == nil {
		return false
	}
	if p := ws.GetTemplateProvenance(); p != nil &&
		(strings.EqualFold(p.BehaviorProfile, "software_project") || strings.EqualFold(p.TemplateID, "code-project")) {
		return true
	}
	if workspacesettings.Extract(ws.SharedData).Profile == "software_project" {
		return true
	}
	if _, present := ws.SharedData[SoftwareProjectKey]; present {
		return true // a damaged binding must not weaken consent
	}
	for i := range ws.Tasks {
		task := &ws.Tasks[i]
		if FirstTaskRecipe(task) != "" || (task.Context["template_id"] == "folder-digest" && task.Context["template_starter_task"] == true) {
			return true
		}
	}
	return false
}

func goalFirstRunAccepted(ws *Workspace) bool {
	return ws.GoalFirstRunState == GoalFirstRunAccepted || ws.MissionExecutionCount > 0 || len(ws.MissionRuns) > 0
}

// ArmFirstGoalRun is for a first save, not a template migration. Once a first
// attempt has been claimed, edits and reloads cannot arm another attempt.
func ArmFirstGoalRun(ws *Workspace) bool {
	if ws.GoalFirstRunState != "" || goalFirstRunAccepted(ws) {
		return false
	}
	ws.GoalFirstRunPending = true
	return true
}

func cancelDeferredGoalRun(ws *Workspace) {
	ws.GoalFirstRunPending = false
	if !goalFirstRunAccepted(ws) {
		ws.GoalFirstRunState = GoalFirstRunCleared
	}
	ClearGoalNeedsAttention(ws)
}

// goalRunAttention includes a conservative recovery message for a process that
// stopped after claiming admission. It never turns that record into permission
// to repeat execution. Actual execution failures remain in MissionRuns.
func goalRunAttention(ws *Workspace, inFlight bool) string {
	if ws.GoalToolboxPolicy != nil && ws.GoalToolboxPolicy.NeedsAttention {
		return ws.GoalToolboxPolicy.NeedsAttentionReason
	}
	if !inFlight && ws.GoalFirstRunState == GoalFirstRunStarting {
		return "The previous goal start was interrupted. Review run history, then choose Run now to try again."
	}
	return ""
}
