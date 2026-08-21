package workspacesettings

import (
	"strings"
	"testing"
)

// inRepo is a workspace whose folder is a version-controlled repository.
var inRepo = WorkspaceCapabilities{HasFolder: true, IsRepository: true, CurrentBranch: "feature/x"}

// plainFolder is a workspace with a folder that is not a repository.
var plainFolder = WorkspaceCapabilities{HasFolder: true}

func policyFor(t *testing.T, profile, preset string, caps WorkspaceCapabilities) EffectivePolicy {
	t.Helper()
	return BuildEffectivePolicy(PresetDefaultsForProfile(profile, preset), caps)
}

func control(t *testing.T, policy EffectivePolicy, key string) EnforcedControl {
	t.Helper()
	found, ok := policy.Control(key)
	if !ok {
		t.Fatalf("policy has no %q control", key)
	}
	return found
}

// --- Profile defaults (FR-130 through FR-135) ------------------------------

// A General workspace supports Plans but keeps structured planning off until
// the user asks for it.
func TestGeneralGuidedKeepsPlanningDisabled(t *testing.T) {
	policy := policyFor(t, "general", "", plainFolder)

	if policy.PlanningEnabled {
		t.Error("a General workspace enabled structured planning by default")
	}
	if policy.Preset != "guided" {
		t.Errorf("preset = %q, want guided", policy.Preset)
	}
}

// Enabled in a General workspace, planning assumes no repository, no PRD, no
// task list, and steps through.
func TestGeneralEnabledPlanningAssumesNoRepository(t *testing.T) {
	settings := PresetDefaultsForProfile("general", "")
	settings.Planning.Enabled = true
	policy := BuildEffectivePolicy(settings, plainFolder)

	if policy.Guidance.Style != "feature" {
		t.Errorf("style = %q, want the general-purpose default", policy.Guidance.Style)
	}
	// Neither repository control can be enforced without a repository, whatever
	// the setting says.
	for _, key := range []string{ControlRepoScan, ControlSafeBranch} {
		found := control(t, policy, key)
		if found.Available {
			t.Errorf("%s is available in a workspace with no repository", key)
		}
		if found.Active() {
			t.Errorf("%s is active in a workspace with no repository", key)
		}
	}
}

// Research keeps planning off by default and, when enabled, questions deeply,
// requires no branch, and writes no software documents.
func TestResearchDefaultsAreInvestigative(t *testing.T) {
	settings := PresetDefaultsForProfile("research", "")
	if settings.Planning.Enabled {
		t.Error("a Research workspace enabled structured planning by default")
	}

	settings.Planning.Enabled = true
	policy := BuildEffectivePolicy(settings, inRepo)

	if policy.Guidance.Style != "investigation" {
		t.Errorf("style = %q, want investigation", policy.Guidance.Style)
	}
	if policy.Guidance.ClarificationDepth != "deep" {
		t.Errorf("clarification depth = %q, want deep", policy.Guidance.ClarificationDepth)
	}
	// No branch requirement, even though this workspace IS a repository — the
	// setting is off, not merely unenforceable.
	branch := control(t, policy, ControlSafeBranch)
	if branch.Enabled {
		t.Error("Research required a branch")
	}
	for _, artifact := range policy.Guidance.PreferredArtifacts {
		if artifact == "prd" || artifact == "task_list" {
			t.Errorf("Research prefers software artifacts: %v", policy.Guidance.PreferredArtifacts)
		}
	}
	if policy.Artifacts.WritePRD || policy.Artifacts.WriteTaskList {
		t.Errorf("Research enabled software artifact writes: %+v", policy.Artifacts)
	}
}

// Software Project keeps Planner as its default: planning on, repository
// inspection on, compiled PRD/task-list exports, and step-through execution.
func TestSoftwareProjectDefaultsToPlanner(t *testing.T) {
	policy := policyFor(t, "software_project", "", inRepo)

	if policy.Preset != "planner" {
		t.Errorf("preset = %q, want planner", policy.Preset)
	}
	if !policy.PlanningEnabled {
		t.Error("Software Project did not enable structured planning")
	}
	if !control(t, policy, ControlRepoScan).Active() {
		t.Error("repository inspection is not active in a Software Project repo")
	}

	artifacts := strings.Join(policy.Guidance.PreferredArtifacts, ",")
	if strings.Contains(artifacts, "prd") || strings.Contains(artifacts, "task_list") {
		t.Errorf("model guidance still asks for app-owned planning files: %v", policy.Guidance.PreferredArtifacts)
	}
	if policy.Artifacts.Directory != "tasks" || !policy.Artifacts.WritePRD || !policy.Artifacts.WriteTaskList {
		t.Errorf("compiled artifact output = %+v, want canonical tasks directory with both files", policy.Artifacts)
	}

	mode := control(t, policy, ControlExecutionMode)
	if !strings.Contains(mode.Description, "waits for you") {
		t.Errorf("execution description = %q, want step-through", mode.Description)
	}
}

// The branch precondition is enforced only in a recognized repository (FR-135).
func TestBranchEnforcementNeedsARepository(t *testing.T) {
	enforced := policyFor(t, "software_project", "planner", inRepo)
	if !control(t, enforced, ControlSafeBranch).Active() {
		t.Error("branch enforcement is inactive inside a repository")
	}

	unenforceable := policyFor(t, "software_project", "planner", plainFolder)
	branch := control(t, unenforceable, ControlSafeBranch)
	if branch.Available {
		t.Error("branch enforcement claims to be available outside a repository")
	}
	if branch.Reason != ReasonNotARepository {
		t.Errorf("reason = %q, want not_a_repository", branch.Reason)
	}
	if branch.Detail == "" {
		t.Error("an unavailable control gave no explanation")
	}
	// The setting stays on. Availability and enablement are different facts,
	// and collapsing them would silently rewrite the user's choice.
	if !branch.Enabled {
		t.Error("an unavailable control switched the user's setting off")
	}
}

// A workspace with no folder at all reports the missing folder, not a missing
// repository — they are different problems with different fixes.
func TestNoFolderReportsTheMissingFolder(t *testing.T) {
	policy := policyFor(t, "software_project", "planner", WorkspaceCapabilities{})
	branch := control(t, policy, ControlSafeBranch)
	if branch.Reason != ReasonNoFolder {
		t.Errorf("reason = %q, want no_workspace_folder", branch.Reason)
	}
}

// --- Preset semantics (FR-138 through FR-141) ------------------------------

func TestMinimalDisablesStructuredPlanning(t *testing.T) {
	policy := policyFor(t, "general", "minimal", plainFolder)
	if policy.PlanningEnabled {
		t.Error("Minimal enabled structured planning")
	}
}

func TestPlannerEnablesPlanningAndKeepsApproval(t *testing.T) {
	policy := policyFor(t, "software_project", "planner", inRepo)
	if !policy.PlanningEnabled {
		t.Error("Planner did not enable structured planning")
	}
	if !control(t, policy, ControlPlanApproval).Active() {
		t.Error("Planner did not require approval")
	}
}

// Autonomous may choose automatic execution, but approval and the invariant
// safety gates survive it (FR-140).
func TestAutonomousKeepsEveryInvariantGate(t *testing.T) {
	policy := policyFor(t, "software_project", "autonomous", inRepo)

	mode := control(t, policy, ControlExecutionMode)
	if !strings.Contains(mode.Description, "starts automatically") {
		t.Errorf("Autonomous did not select automatic execution: %q", mode.Description)
	}
	// The two that no preset may switch off.
	for _, key := range []string{ControlPlanApproval, ControlTaskMaterialization} {
		if !control(t, policy, key).Active() {
			t.Errorf("Autonomous turned off %s", key)
		}
	}
	// And the description still says approval is required, so the screen does
	// not read as "this workspace runs whatever it likes".
	if !strings.Contains(mode.Description, "Approval is still required") {
		t.Errorf("the automatic mode description omits approval: %q", mode.Description)
	}
}

// Autonomous may relax destructive-action confirmation. That is a real
// difference and the policy reports it rather than hiding it.
func TestAutonomousRelaxesOnlyDestructiveConfirmation(t *testing.T) {
	planner := policyFor(t, "software_project", "planner", inRepo)
	autonomous := policyFor(t, "software_project", "autonomous", inRepo)

	if !control(t, planner, ControlDestructiveConfirm).Active() {
		t.Error("Planner did not confirm destructive actions")
	}
	if control(t, autonomous, ControlDestructiveConfirm).Active() {
		t.Error("Autonomous still confirmed destructive actions; the preset means nothing")
	}
}

// --- Truthfulness of the split (FR-124, FR-129) ----------------------------

// Guidance and enforcement must not overlap. A key appearing in both would let
// the UI show the same idea twice with two different strengths of promise.
func TestGuidanceAndEnforcementDoNotOverlap(t *testing.T) {
	policy := policyFor(t, "software_project", "planner", inRepo)

	guidanceValues := []string{
		policy.Guidance.Style,
		policy.Guidance.ClarificationDepth,
		policy.Guidance.DetailLevel,
		policy.Guidance.Tone,
	}
	for _, enforced := range policy.Enforced {
		for _, value := range guidanceValues {
			if value != "" && value == enforced.Key {
				t.Errorf("%q appears as both guidance and an enforced control", value)
			}
		}
	}
}

// Every enforced control describes what compiled code does. None of them may
// describe a preference, because a preference cannot be enforced.
func TestEveryEnforcedControlHasADescription(t *testing.T) {
	policy := policyFor(t, "software_project", "planner", inRepo)
	if len(policy.Enforced) == 0 {
		t.Fatal("policy has no enforced controls")
	}
	for _, enforced := range policy.Enforced {
		if enforced.Key == "" || enforced.Label == "" || enforced.Description == "" {
			t.Errorf("incomplete control: %+v", enforced)
		}
		if !enforced.Available && enforced.Reason == "" {
			t.Errorf("%s is unavailable with no machine-readable reason", enforced.Key)
		}
		if enforced.Available && enforced.Reason != "" {
			t.Errorf("%s is available but carries reason %q", enforced.Key, enforced.Reason)
		}
	}
}

// The active/unavailable maps are what the plan policy snapshot records, so
// they must agree with the controls they summarize.
func TestActiveAndUnavailableMapsAgreeWithTheControls(t *testing.T) {
	policy := policyFor(t, "software_project", "planner", plainFolder)

	active := policy.ActiveControls()
	unavailable := policy.UnavailableControls()
	for _, enforced := range policy.Enforced {
		if active[enforced.Key] != enforced.Active() {
			t.Errorf("%s active = %t, control says %t", enforced.Key, active[enforced.Key], enforced.Active())
		}
		_, listed := unavailable[enforced.Key]
		if listed == enforced.Available {
			t.Errorf("%s available = %t but listed as unavailable = %t",
				enforced.Key, enforced.Available, listed)
		}
	}
}

// The policy is a pure function, so a preset preview can show the real answer
// before anything is saved (FR-142).
func TestBuildingTheSamePolicyTwiceAgrees(t *testing.T) {
	settings := PresetDefaultsForProfile("software_project", "autonomous")
	first := BuildEffectivePolicy(settings, inRepo)
	second := BuildEffectivePolicy(settings, inRepo)

	if len(first.Enforced) != len(second.Enforced) {
		t.Fatalf("control counts differ: %d vs %d", len(first.Enforced), len(second.Enforced))
	}
	for i := range first.Enforced {
		if first.Enforced[i] != second.Enforced[i] {
			t.Errorf("control %d differs: %+v vs %+v", i, first.Enforced[i], second.Enforced[i])
		}
	}
}
