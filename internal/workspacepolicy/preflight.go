package workspacepolicy

import (
	"context"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspaceplan"
	"github.com/johnjallday/ori-agent/internal/workspacesettings"
)

// Preflight is the compiled enforcement behind a Plan's execution
// preconditions (FR-136, FR-137).
//
// It answers one question per named adapter, at the moment a Task would be
// dispatched: may this run right now? A gate returned here stops the dispatch
// and carries what would fix it, because a block with no stated remedy just
// moves the user's confusion later.
type Preflight struct {
	// policy resolves the workspace's effective policy at check time. It is a
	// function rather than a snapshot because the branch changes while a plan
	// runs, and a preflight that trusted a value read at approval would let
	// work land on a branch nobody approved.
	policy PolicyResolver
	// state reports whether workflow steps have completed, which is what the
	// repository-inspection control checks (FR-137).
	state StateReader
}

// PolicyResolver returns a workspace's effective policy and capabilities as
// they are right now.
type PolicyResolver func(ctx context.Context, workspaceID string) (workspacesettings.EffectivePolicy, workspacesettings.WorkspaceCapabilities)

// StateReader reports compiled workflow state for a workspace.
type StateReader interface {
	// RepositoryInspected reports whether the repository inspection step has
	// completed for this workspace.
	RepositoryInspected(ctx context.Context, workspaceID string) (bool, error)
}

// AllowedBranches is the set of branches code execution may run on when the
// branch precondition is enforced.
//
// The rule is stated as a denylist of trunks rather than an allowlist of
// feature branches, because the feature branch names are unbounded and the
// trunks are not. A workspace on `main` is the case worth blocking; a workspace
// on `feature/anything` is the ordinary case and must not need registration.
var protectedBranches = map[string]bool{
	"main":    true,
	"master":  true,
	"trunk":   true,
	"release": true,
}

// NewPreflight builds the preflight checker.
func NewPreflight(policy PolicyResolver, state StateReader) *Preflight {
	return &Preflight{policy: policy, state: state}
}

// CheckPrecondition implements workspaceplan.PreconditionChecker.
//
// A nil gate means satisfied. An unknown adapter name is NOT satisfied: the
// plan named a precondition this build does not implement, and running the work
// anyway would silently drop an enforcement the approval was given on.
func (p *Preflight) CheckPrecondition(ctx context.Context, workspaceID, planID, name string) (*workspaceplan.Gate, error) {
	if p == nil || p.policy == nil {
		return nil, fmt.Errorf("preflight is not configured")
	}

	policy, caps := p.policy(ctx, workspaceID)
	key := strings.ToLower(strings.TrimSpace(name))

	control, known := policy.Control(key)
	switch {
	case !known:
		return &workspaceplan.Gate{
			Kind:       workspaceplan.GatePrecondition,
			Class:      workspaceplan.GateAutomation,
			Title:      name,
			Reason:     fmt.Sprintf("this plan requires %q, which this version of Ori does not know how to check", name),
			Resolution: "remove the precondition from the plan, or start this step yourself",
		}, nil
	case !control.Available:
		// A control this workspace cannot enforce does not block: the plan
		// asked for something that does not apply here, and the settings screen
		// already says so. Blocking would strand the plan on a condition that
		// can never become true.
		return nil, nil
	case !control.Enabled:
		return nil, nil
	}

	switch key {
	case workspacesettings.ControlSafeBranch:
		return p.checkBranch(caps), nil
	case workspacesettings.ControlRepoScan:
		return p.checkRepositoryInspection(ctx, workspaceID)
	default:
		// Every other enforced control is checked at its own point in the
		// lifecycle — approval, materialization, artifact writes — rather than
		// before a dispatch. Reaching here means it is on and has nothing to
		// say about starting this Task.
		return nil, nil
	}
}

// checkBranch blocks execution on a protected branch and names the branch plus
// the corrective action (FR-136).
func (p *Preflight) checkBranch(caps workspacesettings.WorkspaceCapabilities) *workspaceplan.Gate {
	branch := strings.TrimSpace(caps.CurrentBranch)
	if branch == "" {
		return &workspaceplan.Gate{
			Kind:  workspaceplan.GateBranch,
			Class: workspaceplan.GateAutomation,
			Title: "Branch precondition",
			Reason: "this repository is not on a branch (detached HEAD), " +
				"so there is nothing to commit work onto",
			Resolution: "check out a working branch, then start this step",
		}
	}
	if !protectedBranches[strings.ToLower(branch)] {
		return nil
	}
	return &workspaceplan.Gate{
		Kind:   workspaceplan.GateBranch,
		Class:  workspaceplan.GateAutomation,
		Title:  "Branch precondition",
		Reason: fmt.Sprintf("this repository is on %q, which this workspace protects", branch),
		Resolution: fmt.Sprintf(
			"create a working branch (git switch -c feature/…) and switch off %q, then start this step", branch),
	}
}

// checkRepositoryInspection blocks code-oriented execution until the inspection
// step has been recorded as complete (FR-137).
func (p *Preflight) checkRepositoryInspection(ctx context.Context, workspaceID string) (*workspaceplan.Gate, error) {
	if p.state == nil {
		// Enforcement is on and nothing can confirm the step ran. Failing
		// closed is the only honest answer: the approval was given on the
		// promise of an inspection.
		return &workspaceplan.Gate{
			Kind:       workspaceplan.GatePrecondition,
			Class:      workspaceplan.GateAutomation,
			Title:      "Repository inspection",
			Reason:     "this workspace requires a repository inspection, and nothing is configured to record one",
			Resolution: "turn off the repository-inspection requirement, or start this step yourself",
		}, nil
	}

	inspected, err := p.state.RepositoryInspected(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("check repository inspection: %w", err)
	}
	if inspected {
		return nil, nil
	}
	return &workspaceplan.Gate{
		Kind:       workspaceplan.GatePrecondition,
		Class:      workspaceplan.GateAutomation,
		Title:      "Repository inspection",
		Reason:     "the repository has not been inspected yet, and this workspace requires it before code work",
		Resolution: "run the repository inspection step, then start this step",
	}, nil
}
