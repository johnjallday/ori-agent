package workspacepolicy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspaceplan"
	"github.com/johnjallday/ori-agent/internal/workspacesettings"
)

// repoDir builds a folder that looks like a git checkout on the given branch.
func repoDir(t *testing.T, branch string) string {
	t.Helper()
	root := t.TempDir()
	gitDir := filepath.Join(root, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("create .git: %v", err)
	}
	head := "ref: refs/heads/" + branch + "\n"
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte(head), 0o600); err != nil {
		t.Fatalf("write HEAD: %v", err)
	}
	return root
}

func TestInspectReportsAPlainFolder(t *testing.T) {
	caps := Inspect(t.TempDir())
	if !caps.HasFolder {
		t.Error("a real folder was reported as absent")
	}
	if caps.IsRepository {
		t.Error("a plain folder was reported as a repository")
	}
}

func TestInspectReportsARepositoryAndItsBranch(t *testing.T) {
	caps := Inspect(repoDir(t, "feature/planning"))
	if !caps.IsRepository {
		t.Fatal("a git checkout was not recognized")
	}
	if caps.CurrentBranch != "feature/planning" {
		t.Errorf("branch = %q, want feature/planning", caps.CurrentBranch)
	}
}

// A git worktree's .git is a FILE pointing at the real git dir. Treating only
// the directory case as a repository would report every worktree as
// unversioned — and a worktree is exactly where a branch precondition matters.
func TestInspectRecognizesAWorktree(t *testing.T) {
	realRepo := repoDir(t, "main")
	worktree := t.TempDir()
	pointer := "gitdir: " + filepath.Join(realRepo, ".git") + "\n"
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte(pointer), 0o600); err != nil {
		t.Fatalf("write .git file: %v", err)
	}

	caps := Inspect(worktree)
	if !caps.IsRepository {
		t.Fatal("a git worktree was not recognized as a repository")
	}
	if caps.CurrentBranch != "main" {
		t.Errorf("branch = %q, want main", caps.CurrentBranch)
	}
}

// A detached HEAD has no branch, and saying so beats inventing one the user
// cannot switch away from.
func TestInspectReportsNoBranchWhenDetached(t *testing.T) {
	root := repoDir(t, "main")
	head := filepath.Join(root, ".git", "HEAD")
	if err := os.WriteFile(head, []byte("9fceb02aa8c1f2b5c9d0e1f3a4b5c6d7e8f90123\n"), 0o600); err != nil {
		t.Fatalf("write detached HEAD: %v", err)
	}

	caps := Inspect(root)
	if !caps.IsRepository {
		t.Fatal("a detached checkout is still a repository")
	}
	if caps.CurrentBranch != "" {
		t.Errorf("branch = %q, want empty for a detached HEAD", caps.CurrentBranch)
	}
}

// A configured folder that has since moved reports as absent rather than
// erroring: the policy answer is the same, and a settings page should not fail
// to render because a directory was renamed.
func TestInspectTreatsAMissingFolderAsAbsent(t *testing.T) {
	caps := Inspect(filepath.Join(t.TempDir(), "gone"))
	if caps.HasFolder || caps.IsRepository {
		t.Errorf("a missing folder reported capabilities: %+v", caps)
	}
}

// --- Preflight (FR-136, FR-137) --------------------------------------------

func policyWith(t *testing.T, caps workspacesettings.WorkspaceCapabilities) PolicyResolver {
	t.Helper()
	settings := workspacesettings.PresetDefaultsForProfile("software_project", "planner")
	policy := workspacesettings.BuildEffectivePolicy(settings, caps)
	return func(context.Context, string) (workspacesettings.EffectivePolicy, workspacesettings.WorkspaceCapabilities) {
		return policy, caps
	}
}

// A protected branch blocks, and the block names the branch and the fix.
func TestBranchPreflightBlocksOnAProtectedBranch(t *testing.T) {
	caps := workspacesettings.WorkspaceCapabilities{HasFolder: true, IsRepository: true, CurrentBranch: "main"}
	preflight := NewPreflight(policyWith(t, caps), nil)

	gate, err := preflight.CheckPrecondition(context.Background(), "ws-1", "plan-1",
		workspacesettings.ControlSafeBranch)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if gate == nil {
		t.Fatal("running on main was allowed")
	}
	if gate.Kind != workspaceplan.GateBranch {
		t.Errorf("kind = %q, want branch", gate.Kind)
	}
	if !strings.Contains(gate.Reason, "main") {
		t.Errorf("the block does not name the current branch: %q", gate.Reason)
	}
	if !strings.Contains(gate.Resolution, "git switch") {
		t.Errorf("the block does not say what to do: %q", gate.Resolution)
	}
}

func TestBranchPreflightAllowsAWorkingBranch(t *testing.T) {
	caps := workspacesettings.WorkspaceCapabilities{HasFolder: true, IsRepository: true, CurrentBranch: "feature/x"}
	preflight := NewPreflight(policyWith(t, caps), nil)

	gate, err := preflight.CheckPrecondition(context.Background(), "ws-1", "plan-1",
		workspacesettings.ControlSafeBranch)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if gate != nil {
		t.Errorf("a working branch was blocked: %q", gate.Reason)
	}
}

// Outside a repository the control is unavailable, and an unavailable control
// must NOT block: the condition can never become true, so blocking would
// strand the plan forever.
func TestBranchPreflightDoesNotBlockWhereItCannotEnforce(t *testing.T) {
	caps := workspacesettings.WorkspaceCapabilities{HasFolder: true}
	preflight := NewPreflight(policyWith(t, caps), nil)

	gate, err := preflight.CheckPrecondition(context.Background(), "ws-1", "plan-1",
		workspacesettings.ControlSafeBranch)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if gate != nil {
		t.Errorf("an unenforceable control blocked execution: %q", gate.Reason)
	}
}

// stubState answers the repository-inspection question.
type stubState struct{ inspected bool }

func (s stubState) RepositoryInspected(context.Context, string) (bool, error) {
	return s.inspected, nil
}

func TestRepositoryInspectionBlocksUntilItHasRun(t *testing.T) {
	caps := workspacesettings.WorkspaceCapabilities{HasFolder: true, IsRepository: true, CurrentBranch: "feature/x"}
	resolver := policyWith(t, caps)

	blocked := NewPreflight(resolver, stubState{inspected: false})
	gate, err := blocked.CheckPrecondition(context.Background(), "ws-1", "plan-1",
		workspacesettings.ControlRepoScan)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if gate == nil {
		t.Fatal("code work started before the repository was inspected")
	}

	allowed := NewPreflight(resolver, stubState{inspected: true})
	gate, err = allowed.CheckPrecondition(context.Background(), "ws-1", "plan-1",
		workspacesettings.ControlRepoScan)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if gate != nil {
		t.Errorf("an inspected repository was still blocked: %q", gate.Reason)
	}
}

// Enforcement on with nothing able to record the step fails closed. The
// approval was given on the promise of an inspection.
func TestRepositoryInspectionFailsClosedWithNoRecorder(t *testing.T) {
	caps := workspacesettings.WorkspaceCapabilities{HasFolder: true, IsRepository: true, CurrentBranch: "feature/x"}
	preflight := NewPreflight(policyWith(t, caps), nil)

	gate, err := preflight.CheckPrecondition(context.Background(), "ws-1", "plan-1",
		workspacesettings.ControlRepoScan)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if gate == nil {
		t.Fatal("an unverifiable inspection requirement allowed the work")
	}
}

// A precondition this build does not implement is NOT satisfied. Running anyway
// would silently drop an enforcement the approval was given on.
func TestAnUnknownPreconditionBlocks(t *testing.T) {
	caps := workspacesettings.WorkspaceCapabilities{HasFolder: true, IsRepository: true, CurrentBranch: "feature/x"}
	preflight := NewPreflight(policyWith(t, caps), nil)

	gate, err := preflight.CheckPrecondition(context.Background(), "ws-1", "plan-1", "invented_control")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if gate == nil {
		t.Fatal("an unimplemented precondition was treated as satisfied")
	}
	if !strings.Contains(gate.Reason, "does not know how to check") {
		t.Errorf("the reason does not say the control is unimplemented: %q", gate.Reason)
	}
}

// A control the user switched off does not block.
func TestADisabledControlDoesNotBlock(t *testing.T) {
	caps := workspacesettings.WorkspaceCapabilities{HasFolder: true, IsRepository: true, CurrentBranch: "main"}
	settings := workspacesettings.PresetDefaultsForProfile("software_project", "planner")
	settings.Planning.RequireBranch = false
	policy := workspacesettings.BuildEffectivePolicy(settings, caps)

	preflight := NewPreflight(func(context.Context, string) (workspacesettings.EffectivePolicy, workspacesettings.WorkspaceCapabilities) {
		return policy, caps
	}, nil)

	gate, err := preflight.CheckPrecondition(context.Background(), "ws-1", "plan-1",
		workspacesettings.ControlSafeBranch)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if gate != nil {
		t.Errorf("a disabled branch requirement still blocked: %q", gate.Reason)
	}
}
