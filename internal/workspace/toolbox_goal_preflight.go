package workspace

import (
	"fmt"
	"strings"
	"time"
)

// Goal preflight: stopping a scheduled run BEFORE the model is invoked when its
// pinned Toolbox has become unusable (PRD FR-105).
//
// The timing is the whole point. A pinned goal runs unattended on a cadence, so
// nobody is watching when its toolbox is archived or its connection is removed.
// Without this check the run would start, the agent would silently be handed
// fewer capabilities than the goal was designed around, and the only evidence
// would be a disappointing result days later. Stopping first — and saying why —
// turns a silent degradation into a visible `Needs attention`.

// GoalPreflightResult is the answer to "may this goal start?".
type GoalPreflightResult struct {
	// OK is true when the goal may proceed.
	OK bool `json:"ok"`
	// Reason explains a refusal in plain language, including what to do.
	Reason string `json:"reason,omitempty"`
	// ToolboxID / ToolboxVersion identify what would be used.
	ToolboxID      string `json:"toolbox_id,omitempty"`
	ToolboxVersion int64  `json:"toolbox_version,omitempty"`
	// Readiness is the resolved state, for the surface that reports it.
	Readiness string `json:"readiness,omitempty"`
	// UsedCurrentAtStart records that the goal resolved the instance's current
	// assignment rather than a pin (FR-103).
	UsedCurrentAtStart bool `json:"used_current_at_start,omitempty"`
}

// PreflightGoalToolbox resolves and validates the Toolbox a Goal would run
// with, without invoking anything.
//
// A goal with no policy passes: goals predate Toolboxes, and one that never
// chose a policy should keep working exactly as it did (the entry agent's
// current assignment applies, which is also what UseCurrentAtStart means).
func PreflightGoalToolbox(
	ws *Workspace,
	learned []ResolvedSkill,
	capacity int,
	expertMode bool,
	thresholds FocusThresholds,
) GoalPreflightResult {
	policy := ws.GoalToolboxPolicy
	if policy == nil {
		return GoalPreflightResult{OK: true, UsedCurrentAtStart: true}
	}

	instanceID := strings.TrimSpace(policy.EntryAgentInstanceID)
	if instanceID == "" {
		return GoalPreflightResult{OK: true, UsedCurrentAtStart: true}
	}
	instance := findAgentInstanceByID(ws, instanceID)
	if instance == nil {
		return GoalPreflightResult{
			OK:     false,
			Reason: "This goal's agent is no longer attached to the workspace. Choose another agent for it.",
		}
	}

	// --- Current-at-start: the deliberate alternative to pinning (FR-103). ---
	if !policy.Pinned() {
		definition, recipe, assigned, err := ws.ResolveAssignedToolbox(instanceID)
		if err != nil {
			return GoalPreflightResult{
				OK:     false,
				Reason: fmt.Sprintf("This goal's agent has an unreadable toolbox assignment: %v", err),
			}
		}
		if !assigned {
			// No explicit assignment means the legacy path, which still works.
			return GoalPreflightResult{OK: true, UsedCurrentAtStart: true}
		}
		preview := PreviewToolbox(ws, instance, definition, recipe, learned, capacity, expertMode, thresholds)
		return goalPreflightFromPreview(preview, definition.ID, recipe.Version, true)
	}

	// --- Pinned: the exact version, or a refusal (FR-104, FR-105). ---
	definition, exists := ws.GetToolbox(policy.ToolboxID)
	if !exists {
		return GoalPreflightResult{
			OK:             false,
			ToolboxID:      policy.ToolboxID,
			ToolboxVersion: policy.ToolboxVersion,
			Readiness:      ReadinessMissingCapability,
			Reason:         "The toolbox this goal is pinned to no longer exists. Pick another toolbox for it.",
		}
	}
	if definition.Archived() {
		return GoalPreflightResult{
			OK:             false,
			ToolboxID:      definition.ID,
			ToolboxVersion: policy.ToolboxVersion,
			Readiness:      ReadinessArchived,
			Reason: fmt.Sprintf("%q is archived, so this goal cannot start. Restore it, or pin the goal to another toolbox.",
				definition.Name),
		}
	}
	recipe, err := definition.ResolveVersion(policy.ToolboxVersion)
	if err != nil {
		return GoalPreflightResult{
			OK:             false,
			ToolboxID:      definition.ID,
			ToolboxVersion: policy.ToolboxVersion,
			Readiness:      ReadinessNeedsRepair,
			Reason: fmt.Sprintf("Version %d of %q is no longer available. Pin this goal to a current version.",
				policy.ToolboxVersion, definition.Name),
		}
	}

	preview := PreviewToolbox(ws, instance, *definition, recipe, learned, capacity, expertMode, thresholds)
	return goalPreflightFromPreview(preview, definition.ID, recipe.Version, false)
}

func goalPreflightFromPreview(preview ToolboxPreview, toolboxID string, version int64, usedCurrent bool) GoalPreflightResult {
	result := GoalPreflightResult{
		ToolboxID:          toolboxID,
		ToolboxVersion:     version,
		Readiness:          preview.Readiness,
		UsedCurrentAtStart: usedCurrent,
	}
	if preview.Readiness == ReadinessReady {
		result.OK = true
		return result
	}
	result.Reason = fmt.Sprintf("This goal's toolbox is %q: %s",
		strings.ToLower(preview.Readiness), firstBlockingMessage(preview.Issues))
	return result
}

// MarkGoalNeedsAttention records a failed preflight on the workspace so the
// Goal surface can explain the stop without re-running the check (FR-105).
//
// Must be called with the workspace loaded for update.
func MarkGoalNeedsAttention(ws *Workspace, reason string) {
	if ws == nil {
		return
	}
	if ws.GoalToolboxPolicy == nil {
		ws.GoalToolboxPolicy = &GoalToolboxPolicy{}
	}
	ws.GoalToolboxPolicy.NeedsAttention = true
	ws.GoalToolboxPolicy.NeedsAttentionReason = strings.TrimSpace(reason)
	ws.GoalToolboxPolicy.UpdatedAt = time.Now()
	ws.UpdatedAt = ws.GoalToolboxPolicy.UpdatedAt
}

// ClearGoalNeedsAttention clears the flag once the goal can run again.
func ClearGoalNeedsAttention(ws *Workspace) {
	if ws == nil || ws.GoalToolboxPolicy == nil {
		return
	}
	if !ws.GoalToolboxPolicy.NeedsAttention {
		return
	}
	ws.GoalToolboxPolicy.NeedsAttention = false
	ws.GoalToolboxPolicy.NeedsAttentionReason = ""
	ws.GoalToolboxPolicy.UpdatedAt = time.Now()
	ws.UpdatedAt = ws.GoalToolboxPolicy.UpdatedAt
}
