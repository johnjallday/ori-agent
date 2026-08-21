package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspaceplan"
	"github.com/johnjallday/ori-agent/internal/workspacepolicy"
	"github.com/johnjallday/ori-agent/internal/workspacesettings"
)

// Guidance and enforcement must not be able to impersonate each other
// (FR-124, FR-129).
//
// They travel through two separate resolvers into two separate destinations:
// guidance reaches the generator's prompt and nothing else, an enforced policy
// snapshot reaches the immutable version record and never the model. These
// tests hold the seam by checking the SHAPES — a guidance field cannot be
// expressed as an enforced control, and vice versa, because the types have no
// overlapping field a value could cross through.

// A guidance value is prose. Nothing in the enforced snapshot can hold it, so a
// style or tone can never arrive somewhere that reads as enforcement.
func TestGuidanceCannotBecomeAnEnforcedControl(t *testing.T) {
	guidance := workspaceplan.GuidanceInput{
		Style:              "investigation",
		ClarificationDepth: "deep",
		PreferredArtifacts: []string{"prd", "task_list"},
		DetailLevel:        "thorough",
	}

	// The enforced snapshot's control map is bool-valued. There is no way to
	// carry "investigation" into it, and this test exists to fail loudly if
	// somebody widens it to accept strings.
	snapshot := workspaceplan.PolicySnapshot{Enforced: map[string]bool{}}
	for _, artifact := range guidance.PreferredArtifacts {
		// A preferred artifact is a REQUEST. If it appeared as an enforced key
		// the screen would read "artifacts are written" when nothing checks it.
		if _, exists := snapshot.Enforced[artifact]; exists {
			t.Errorf("guidance value %q leaked into the enforced control map", artifact)
		}
	}
	if guidance.Style == "" || guidance.DetailLevel == "" {
		t.Fatal("fixture is empty; this test would pass vacuously")
	}
}

// Every enforced key recorded in a snapshot must be a control the policy layer
// actually knows how to compute. A snapshot naming something else would claim
// an enforcement that never ran.
func TestEnforcedSnapshotKeysAreRealControls(t *testing.T) {
	policy := workspacesettings.BuildEffectivePolicy(
		workspacesettings.PresetDefaultsForProfile("software_project", "planner"),
		workspacesettings.WorkspaceCapabilities{HasFolder: true, IsRepository: true, CurrentBranch: "feature/x"},
	)

	known := map[string]bool{}
	for _, control := range policy.Enforced {
		known[control.Key] = true
	}
	if len(known) == 0 {
		t.Fatal("policy produced no controls")
	}

	for key := range policy.ActiveControls() {
		if !known[key] {
			t.Errorf("snapshot key %q is not a control the policy layer defines", key)
		}
	}
	for key := range policy.UnavailableControls() {
		if !known[key] {
			t.Errorf("unavailable key %q is not a control the policy layer defines", key)
		}
	}
}

// A control that cannot be enforced here must never be recorded as enforced,
// however the user set it. This is the audit property: reading a snapshot back
// later must explain what actually happened (FR-127, FR-144).
func TestAnUnenforceableControlIsNeverRecordedAsEnforced(t *testing.T) {
	// Planner enables both repository controls; the workspace is not a repo.
	policy := workspacesettings.BuildEffectivePolicy(
		workspacesettings.PresetDefaultsForProfile("software_project", "planner"),
		workspacesettings.WorkspaceCapabilities{HasFolder: true},
	)

	active := policy.ActiveControls()
	unavailable := policy.UnavailableControls()

	for _, key := range []string{
		workspacesettings.ControlRepoScan,
		workspacesettings.ControlSafeBranch,
	} {
		if active[key] {
			t.Errorf("%s recorded as enforced in a workspace that cannot enforce it", key)
		}
		if unavailable[key] == "" {
			t.Errorf("%s gave no machine-readable reason for being unavailable", key)
		}
	}
}

// The guidance resolver must not read the enforced controls, and the policy
// resolver must not read guidance. With no workspace store wired both degrade
// to empty rather than to each other's data.
func TestResolversDegradeToEmptyRatherThanToEachOther(t *testing.T) {
	builder := &ServerBuilder{}
	ctx := context.Background()

	guidance := builder.resolvePlanGuidance(ctx, "ws-1")
	if guidance.Style != "" || len(guidance.PreferredArtifacts) != 0 {
		t.Errorf("guidance invented content with nothing wired: %+v", guidance)
	}

	snapshot := builder.resolvePlanPolicy(ctx, "ws-1")
	if len(snapshot.Enforced) != 0 {
		t.Errorf("policy claimed enforcement with nothing wired: %+v", snapshot.Enforced)
	}
	if snapshot.Profile != "" {
		t.Errorf("policy invented a profile: %q", snapshot.Profile)
	}
	if artifacts := builder.resolvePlanArtifacts(ctx, "ws-1"); artifacts.Apply {
		t.Errorf("artifact resolver invented compiled output policy: %+v", artifacts)
	}
}

func TestApprovedOriArtifactsOfferWtHandoffOnlyFromDevWorktree(t *testing.T) {
	store, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("new workspace store: %v", err)
	}
	ws := &workspace.Workspace{ID: "ws-1", Name: "Ori dev", ProjectPath: "project"}
	if err := store.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}
	project := filepath.Join(filepath.Dir(store.GetFilesPath(ws.ID)), "project")
	if err := os.MkdirAll(filepath.Join(project, ".git"), 0o750); err != nil {
		t.Fatalf("create fake repository: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(project, "scripts"), 0o750); err != nil {
		t.Fatalf("create scripts directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, ".git", "HEAD"), []byte("ref: refs/heads/dev\n"), 0o600); err != nil {
		t.Fatalf("write HEAD: %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, "scripts", "wt.sh"), []byte("# wt\n"), 0o600); err != nil {
		t.Fatalf("write wt script: %v", err)
	}

	builder := &ServerBuilder{workspaceStore: store, workspacePlanPolicy: workspacepolicy.NewResolver(store)}
	if got := builder.workspacePlanArtifactRoot(ws.ID, "tasks/tasks-approved.md"); got != project {
		t.Fatalf("repository task root = %q, want project %q", got, project)
	}
	if got := builder.workspacePlanArtifactRoot(ws.ID, "older-dir/prd-approved.md"); got != project {
		t.Fatalf("approved PRD root changed with current settings: %q, want %q", got, project)
	}
	if got := builder.workspacePlanArtifactRoot(ws.ID, "notes/context.md"); got != store.GetFilesPath(ws.ID) {
		t.Fatalf("note root = %q, want workspace files root %q", got, store.GetFilesPath(ws.ID))
	}

	plan := &workspaceplan.Plan{ID: "plan_12345678", WorkspaceID: ws.ID, OriginalRequest: "Approved bridge"}
	feature := workspaceplan.PlanFeatureSlug(plan)
	paths := []string{"tasks/tasks-" + feature + ".md"}

	handoff := builder.resolveWorkspacePlanHandoff(ws.ID, plan, paths)
	if handoff == nil || handoff.Kind != "wt" || handoff.Feature != feature {
		t.Fatalf("handoff = %#v", handoff)
	}
	if !strings.Contains(handoff.Command, "wt start "+feature) {
		t.Fatalf("handoff command = %q", handoff.Command)
	}

	if err := os.WriteFile(filepath.Join(project, ".git", "HEAD"), []byte("ref: refs/heads/feature/work\n"), 0o600); err != nil {
		t.Fatalf("change HEAD: %v", err)
	}
	if got := builder.resolveWorkspacePlanHandoff(ws.ID, plan, paths); got != nil {
		t.Fatalf("non-dev worktree offered wt start: %#v", got)
	}
}

// Autonomous selects automatic execution but the invariant gates survive: the
// snapshot still records approval and materialization as enforced (FR-140).
func TestAutonomousSnapshotKeepsApprovalEnforced(t *testing.T) {
	policy := workspacesettings.BuildEffectivePolicy(
		workspacesettings.PresetDefaultsForProfile("software_project", "autonomous"),
		workspacesettings.WorkspaceCapabilities{HasFolder: true, IsRepository: true, CurrentBranch: "feature/x"},
	)
	active := policy.ActiveControls()

	for _, key := range []string{
		workspacesettings.ControlPlanApproval,
		workspacesettings.ControlTaskMaterialization,
	} {
		if !active[key] {
			t.Errorf("Autonomous dropped the invariant control %s", key)
		}
	}
}

// The enforced controls describe compiled behavior. None may use the language
// FR-129 reserves for things code actually checks — in the guidance half.
func TestGuidanceCarriesNoGuaranteeLanguage(t *testing.T) {
	policy := workspacesettings.BuildEffectivePolicy(
		workspacesettings.PresetDefaultsForProfile("research", "planner"),
		workspacesettings.WorkspaceCapabilities{HasFolder: true},
	)

	guidance := []string{
		policy.Guidance.Style,
		policy.Guidance.ClarificationDepth,
		policy.Guidance.DetailLevel,
		policy.Guidance.Tone,
	}
	guidance = append(guidance, policy.Guidance.PreferredArtifacts...)

	for _, value := range guidance {
		lower := strings.ToLower(value)
		for _, forbidden := range []string{"required", "guaranteed", "will be", "must"} {
			if strings.Contains(lower, forbidden) {
				t.Errorf("guidance value %q uses enforcement language %q", value, forbidden)
			}
		}
	}
}
