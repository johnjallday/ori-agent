package personalhq

import (
	"strings"

	"github.com/johnjallday/ori-agent/internal/session"
)

// UpgradePlan is the pure, side-effect-free result of comparing a designated
// Personal HQ workspace against the current provisioning version (contract
// §5.1). It reports exactly what an apply would do, so the UI can show a diff
// and require explicit confirmation before anything changes (task 2.11), and so
// apply can be gated on a plan with no blockers.
type UpgradePlan struct {
	CurrentVersion int `json:"current_version"`
	TargetVersion  int `json:"target_version"`

	// UpToDate is true when no upgrade is needed (current >= target) and there
	// are no blockers. An up-to-date HQ still lists no additions/conflicts.
	UpToDate bool `json:"up_to_date"`

	// MissingRoles names the canonical specialist agents that are absent and
	// would be added by an apply (e.g. "Inbox", "Journal").
	MissingRoles []string `json:"missing_roles,omitempty"`

	// Additions is the human-readable list of changes an apply would make.
	Additions []string `json:"additions,omitempty"`

	// Conflicts names existing agents whose name matches a role but whose
	// identity is incompatible, so apply must generate a collision-safe
	// instance rather than reuse them (task 2.5). Advisory: apply resolves
	// these, it is not blocked by them.
	Conflicts []string `json:"conflicts,omitempty"`

	// PreservedCustomizations lists user-owned state an apply must leave
	// untouched, shown so the user can trust the upgrade is non-destructive
	// (task 2.6).
	PreservedCustomizations []string `json:"preserved_customizations,omitempty"`

	// Blockers are conditions that prevent an apply entirely (e.g. the target
	// is not an eligible HQ). A non-empty Blockers list means apply must refuse.
	Blockers []string `json:"blockers,omitempty"`

	// RetryablePriorFailure is true when the last recorded upgrade ended
	// partial/failed, so the UI can frame this as "resume/retry".
	RetryablePriorFailure bool `json:"retryable_prior_failure"`
}

// HasChanges reports whether applying the plan would modify the workspace.
func (p UpgradePlan) HasChanges() bool {
	return len(p.MissingRoles) > 0
}

// Blocked reports whether the plan cannot be applied.
func (p UpgradePlan) Blocked() bool {
	return len(p.Blockers) > 0
}

// PlanUpgrade computes what upgrading ws to CurrentProvisioningVersion would do.
// It is pure: it never mutates ws and never performs I/O. userID is the user the
// HQ is being planned for, used to detect an ineligible/wrong-owner target.
//
// Design notes:
//   - Version gate: an HQ already at or beyond the current version needs no
//     roster additions, but the plan is still computed so the UI can confirm
//     "up to date" and still surface a retryable prior failure.
//   - Arbitrary designated HQs: a workspace the user designated that was never
//     created from personal-ops reads as version 0 with all specialist roles
//     missing, so the same plan/apply path converges it onto the assistant
//     roster (task 2.1, 2.9) without assuming the template.
func PlanUpgrade(ws *session.Workspace, userID string) UpgradePlan {
	state := ReadProvisionState(ws)
	plan := UpgradePlan{
		CurrentVersion:        state.Version,
		TargetVersion:         CurrentProvisioningVersion,
		RetryablePriorFailure: state.LastUpgradeOutcome == UpgradeOutcomePartial || state.LastUpgradeOutcome == UpgradeOutcomeFailed,
	}

	// Eligibility blockers mirror designation eligibility so an upgrade can
	// never target a group/trashed/wrong-owner workspace.
	if ws == nil {
		plan.Blockers = append(plan.Blockers, "the workspace could not be loaded")
		return plan
	}
	switch ineligibleReason(ws, normalizeUserID(userID)) {
	case InvalidReasonGroup:
		plan.Blockers = append(plan.Blockers, "group workspaces cannot be a Personal HQ")
	case InvalidReasonTrashed:
		plan.Blockers = append(plan.Blockers, "the workspace is in the trash")
	case InvalidReasonMissing:
		plan.Blockers = append(plan.Blockers, "the workspace is missing")
	case InvalidReasonWrongOwner:
		plan.Blockers = append(plan.Blockers, "the workspace belongs to a different user")
	}
	if plan.Blocked() {
		return plan
	}

	// Compute missing/conflicting specialist roles regardless of version, so a
	// version-current HQ whose roster was hand-edited (a role deleted) is still
	// reported as needing that role back.
	for _, role := range V1Roster {
		inst, ok := FindRoleInstance(ws, role)
		if !ok {
			plan.MissingRoles = append(plan.MissingRoles, role.AgentName)
			plan.Additions = append(plan.Additions, "Add the "+role.AgentName+" specialist")
			continue
		}
		if role.Entry && !inst.EntryPoint {
			// The entry role exists but is not the entry agent — advisory only;
			// we never forcibly reassign a user's chosen entry point.
			plan.Conflicts = append(plan.Conflicts, role.AgentName+" exists but is not the entry agent")
		}
	}

	// Everything the user owns is preserved by apply (task 2.6).
	plan.PreservedCustomizations = preservedCustomizations(ws)

	if state.Version >= CurrentProvisioningVersion && !plan.HasChanges() {
		plan.UpToDate = true
	}
	return plan
}

// preservedCustomizations enumerates the user-owned state an apply guarantees to
// leave untouched, for display in the upgrade diff. It is descriptive, not
// exhaustive machine state.
func preservedCustomizations(ws *session.Workspace) []string {
	var out []string
	if edited := editedAgentNames(ws); len(edited) > 0 {
		out = append(out, "Edited agent prompts/instructions: "+strings.Join(edited, ", "))
	}
	if ws.OpenTaskCount > 0 {
		out = append(out, "Existing tasks")
	}
	// Non-roster agents the user added themselves.
	if extra := nonRosterAgentNames(ws); len(extra) > 0 {
		out = append(out, "Your other agents: "+strings.Join(extra, ", "))
	}
	return out
}

// editedAgentNames returns roster-role agents the user has customized (a
// per-instance CustomInstructions override), so the diff can promise those are
// preserved.
func editedAgentNames(ws *session.Workspace) []string {
	var out []string
	for i := range ws.AgentInstances {
		if strings.TrimSpace(ws.AgentInstances[i].CustomInstructions) != "" {
			out = append(out, ws.AgentInstances[i].Name)
		}
	}
	return out
}

// nonRosterAgentNames returns agents present in the workspace that are not part
// of the v1 specialist roster — user-added agents an upgrade must never remove.
func nonRosterAgentNames(ws *session.Workspace) []string {
	var out []string
	for i := range ws.AgentInstances {
		name := strings.TrimSpace(ws.AgentInstances[i].Name)
		if name == "" || isRosterAgentName(name) {
			continue
		}
		out = append(out, name)
	}
	return out
}

// isRosterAgentName reports whether name matches a v1 specialist role.
func isRosterAgentName(name string) bool {
	for _, role := range V1Roster {
		if strings.EqualFold(name, role.AgentName) {
			return true
		}
	}
	return false
}
