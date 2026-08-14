package workspacesettings

import (
	"sort"
	"strings"
)

// Effective planning policy: the app-owned answer to what a workspace's
// planning actually does (FR-123, FR-124).
//
// The split between guidance and enforcement is the point of this file, and it
// is a truth claim rather than a presentation choice. Guidance is what the
// model is ASKED for — style, depth, preferred artifacts. Nothing checks that
// it was honored, so nothing may promise it was. Enforcement is what compiled
// code CHECKS, and every enforced control names a real adapter that runs.
//
// A setting that reads like a guarantee but is only ever passed to a prompt is
// the specific failure this separation exists to prevent: it teaches a user
// that the workspace will refuse something it will happily do.

// PolicyVersion is the schema version of the effective-policy payload. It is
// carried on the wire so a client can tell a policy it understands from one it
// does not, rather than inferring from which fields happen to be present.
const PolicyVersion = 1

// EffectivePolicy is the whole answer for one workspace.
type EffectivePolicy struct {
	Version int    `json:"version"`
	Profile string `json:"profile"`
	Preset  string `json:"preset"`
	// PlanningEnabled gates structured planning entirely. A workspace with it
	// off still has its existing Plans and can read them; it just does not
	// recommend or create new ones (FR-130, FR-132, FR-138).
	PlanningEnabled bool              `json:"planning_enabled"`
	Guidance        GuidancePolicy    `json:"guidance"`
	Enforced        []EnforcedControl `json:"enforced"`
}

// GuidancePolicy is advisory. Every field here is a request to the model, and
// the API says so by keeping them in their own group rather than mixed in with
// controls that are actually checked (FR-124, FR-125, FR-129).
type GuidancePolicy struct {
	// Style is the planning style, for example "feature" or "investigation".
	Style string `json:"style"`
	// ClarificationDepth is "minimal", "standard", or "deep".
	ClarificationDepth string `json:"clarification_depth"`
	// PreferredArtifacts names the artifact kinds the workspace would like.
	// Whether they are actually written is an ENFORCED question, answered by
	// the artifact_write control — this field only says what to propose.
	PreferredArtifacts []string `json:"preferred_artifacts"`
	// DetailLevel and Tone shape prose, and nothing verifies either.
	DetailLevel string `json:"detail_level"`
	Tone        string `json:"tone"`
}

// EnforcedControl is one compiled control: what it is, whether it is on, and
// whether this workspace can enforce it at all (FR-126, FR-127).
type EnforcedControl struct {
	// Key is the adapter key. It is the stable identifier the planning engine
	// and the policy snapshot both use; the label is for people.
	Key   string `json:"key"`
	Label string `json:"label"`
	// Description says what the compiled check actually does, in the words a
	// user reads. It describes behavior, never intent.
	Description string `json:"description"`
	// Enabled is the user's setting. Available is whether this workspace can
	// honor it. Both matter: an enabled control in a workspace that cannot
	// enforce it is not enforcement, and reporting it as one would be the lie
	// FR-127 exists to prevent.
	Enabled   bool `json:"enabled"`
	Available bool `json:"available"`
	// Reason is machine-readable and set only when Available is false, so a
	// client can branch on it rather than matching prose (FR-127, FR-128).
	Reason UnavailableReason `json:"reason,omitempty"`
	// Detail explains the reason in a sentence the UI can show as-is.
	Detail string `json:"detail,omitempty"`
}

// Active reports whether this control will actually run. A control the user
// enabled in a workspace that cannot support it is not active, and this is the
// only predicate anything should gate on.
func (c EnforcedControl) Active() bool { return c.Enabled && c.Available }

// UnavailableReason is why a control cannot be enforced here.
type UnavailableReason string

const (
	// ReasonNotARepository is a version-control control in a workspace that is
	// not a recognized repository (FR-135).
	ReasonNotARepository UnavailableReason = "not_a_repository"
	// ReasonNoFolder is a filesystem control in a workspace with no folder.
	ReasonNoFolder UnavailableReason = "no_workspace_folder"
	// ReasonProfileNotApplicable is a control that does not apply to this
	// workspace's kind of work — software artifacts in a research workspace.
	ReasonProfileNotApplicable UnavailableReason = "not_applicable_to_profile"
)

// Enforced adapter keys (FR-126). These are the names the policy snapshot
// records and the planning engine resolves, so they are declared once here and
// referenced everywhere rather than spelled out at each use.
const (
	ControlPlanApproval        = "plan_approval"
	ControlTaskMaterialization = "task_materialization"
	ControlExecutionMode       = "execution_mode"
	ControlRepoScan            = "repo_scan"
	ControlSafeBranch          = "safe_branch"
	ControlHandoffConfirmation = "handoff_confirmation"
	ControlArtifactWrite       = "artifact_write"
	ControlNoteCreation        = "note_creation"
	ControlDestructiveConfirm  = "destructive_confirmation"
)

// WorkspaceCapabilities is what the application knows about a workspace that
// its settings cannot say: whether it has a folder, and whether that folder is
// a repository.
//
// It is passed in rather than looked up here so this package stays free of
// filesystem and version-control knowledge, and so the same policy can be
// computed for a hypothetical workspace when previewing a preset.
type WorkspaceCapabilities struct {
	// HasFolder is whether the workspace has a folder on disk.
	HasFolder bool
	// IsRepository is whether that folder is a recognized version-controlled
	// repository. A branch precondition is enforceable only here (FR-135).
	IsRepository bool
	// CurrentBranch is the branch that folder is on, when known. It is carried
	// so the UI can say what would be blocked before anything is blocked.
	CurrentBranch string
}

// BuildEffectivePolicy computes what a workspace's planning actually does.
//
// It is a pure function of settings and capabilities, which is what lets the
// preset preview show the real answer before saving: the same code produces the
// policy for settings that are only being considered (FR-142).
func BuildEffectivePolicy(settings Settings, caps WorkspaceCapabilities) EffectivePolicy {
	settings = Normalize(settings)

	policy := EffectivePolicy{
		Version:         PolicyVersion,
		Profile:         settings.Profile,
		Preset:          settings.Preset,
		PlanningEnabled: settings.Planning.Enabled,
		Guidance: GuidancePolicy{
			Style:              settings.Planning.Mode,
			ClarificationDepth: settings.Planning.ClarificationMode,
			PreferredArtifacts: preferredArtifacts(settings),
			DetailLevel:        detailLevelFor(settings),
			Tone:               toneFor(settings),
		},
	}
	policy.Enforced = enforcedControls(settings, caps)
	return policy
}

// preferredArtifacts lists the artifact kinds the workspace would like a plan
// to propose. It is guidance: whether they are written is the artifact_write
// control's business.
func preferredArtifacts(settings Settings) []string {
	artifacts := []string{}
	if settings.Planning.WritePRD {
		artifacts = append(artifacts, "prd")
	}
	if settings.Planning.WriteTaskList {
		artifacts = append(artifacts, "task_list")
	}
	if settings.Workflow.SaveOutputsAsNotes {
		artifacts = append(artifacts, "note")
	}
	return artifacts
}

// detailLevelFor derives how much prose a plan should carry from the
// clarification depth, because a workspace that wants deep questioning wants
// the reasoning written down too.
func detailLevelFor(settings Settings) string {
	switch settings.Planning.ClarificationMode {
	case "deep":
		return "thorough"
	case "minimal":
		return "concise"
	default:
		return "standard"
	}
}

// toneFor picks a register from the workspace's kind of work.
func toneFor(settings Settings) string {
	if settings.Profile == "research" {
		return "investigative"
	}
	return "practical"
}

// enforcedControls builds the compiled half, in a stable order.
//
// Every control here corresponds to code that runs. A control with no adapter
// behind it does not belong in this list at any availability — reporting it as
// "enabled but unavailable" would still imply Ori knows how to enforce it and
// merely cannot here (FR-141).
func enforcedControls(settings Settings, caps WorkspaceCapabilities) []EnforcedControl {
	controls := []EnforcedControl{
		{
			Key:   ControlPlanApproval,
			Label: "Explicit plan approval",
			Description: "No tasks, files, or runs are created until you approve " +
				"one exact plan version.",
			// This one is not a setting. It is an invariant: no preset turns it
			// off, including Autonomous (FR-140, FR-168).
			Enabled:   true,
			Available: true,
		},
		{
			Key:   ControlTaskMaterialization,
			Label: "Approval creates the tasks",
			Description: "Approved plan items become workspace tasks, with the " +
				"approval recorded on each one.",
			Enabled:   true,
			Available: true,
		},
		{
			Key:   ControlExecutionMode,
			Label: "Execution mode",
			Description: executionModeDescription(settings.Planning.DefaultExecutionMode) +
				" Approval is still required either way.",
			Enabled:   true,
			Available: true,
		},
		{
			Key:   ControlRepoScan,
			Label: "Repository inspection before code work",
			Description: "Code-oriented execution does not begin until the " +
				"repository inspection step has completed.",
			Enabled:   settings.Workflow.RequireRepoScan,
			Available: caps.IsRepository,
		},
		{
			Key:   ControlSafeBranch,
			Label: "Branch precondition",
			Description: "Code execution is blocked on a disallowed branch, and " +
				"reports the current branch and what to do about it.",
			Enabled:   settings.Planning.RequireBranch,
			Available: caps.IsRepository,
		},
		{
			Key:   ControlHandoffConfirmation,
			Label: "Confirm specialist handoffs",
			Description: "Handing work to a specialist agent stops for your " +
				"confirmation first.",
			Enabled:   settings.Workflow.AskBeforeSpecialistHandoff,
			Available: true,
		},
		{
			Key:   ControlArtifactWrite,
			Label: "Write planning documents",
			Description: "Approved plans render their documents into the " +
				"workspace, checked to stay inside it.",
			Enabled:   settings.Planning.WritePRD || settings.Planning.WriteTaskList,
			Available: caps.HasFolder,
		},
		{
			Key:         ControlNoteCreation,
			Label:       "Save outputs as notes",
			Description: "Useful results are written to workspace notes.",
			Enabled:     settings.Workflow.SaveOutputsAsNotes,
			Available:   true,
		},
		{
			Key:   ControlDestructiveConfirm,
			Label: "Confirm destructive actions",
			Description: "An action that deletes or overwrites stops for your " +
				"confirmation first.",
			// "none" is the only value that turns this off, and it is reachable
			// only under Autonomous. Even there it governs destructive ACTIONS,
			// never the plan approval above it.
			Enabled:   settings.Workflow.ConfirmationMode != "none",
			Available: true,
		},
	}

	for i := range controls {
		if controls[i].Available {
			continue
		}
		controls[i].Reason, controls[i].Detail = unavailability(controls[i].Key, caps)
	}

	sort.SliceStable(controls, func(i, j int) bool { return controls[i].Key < controls[j].Key })
	return controls
}

// executionModeDescription says what the mode does, without implying approval
// is skipped in either one.
func executionModeDescription(mode string) string {
	if strings.TrimSpace(mode) == "auto" {
		return "Approved work starts automatically, one plan at a time per workspace."
	}
	return "Approved work waits for you to start each step."
}

// unavailability explains why a control cannot be enforced, in both machine and
// human form.
func unavailability(key string, caps WorkspaceCapabilities) (UnavailableReason, string) {
	switch key {
	case ControlRepoScan, ControlSafeBranch:
		if !caps.HasFolder {
			return ReasonNoFolder,
				"This workspace has no folder, so there is no repository to inspect."
		}
		return ReasonNotARepository,
			"This workspace's folder is not a version-controlled repository, " +
				"so there is no branch to check."
	case ControlArtifactWrite:
		return ReasonNoFolder,
			"This workspace has no folder to write documents into."
	default:
		return ReasonProfileNotApplicable,
			"This control does not apply to this workspace."
	}
}

// Control returns one control by key, and whether it exists.
func (p EffectivePolicy) Control(key string) (EnforcedControl, bool) {
	for _, control := range p.Enforced {
		if control.Key == key {
			return control, true
		}
	}
	return EnforcedControl{}, false
}

// ActiveControls returns the adapter keys that will actually run, as the map
// the plan policy snapshot records.
func (p EffectivePolicy) ActiveControls() map[string]bool {
	active := make(map[string]bool, len(p.Enforced))
	for _, control := range p.Enforced {
		active[control.Key] = control.Active()
	}
	return active
}

// UnavailableControls returns the machine-readable reasons, keyed by adapter,
// for every control this workspace cannot enforce (FR-127).
func (p EffectivePolicy) UnavailableControls() map[string]string {
	unavailable := map[string]string{}
	for _, control := range p.Enforced {
		if !control.Available {
			unavailable[control.Key] = string(control.Reason)
		}
	}
	return unavailable
}
