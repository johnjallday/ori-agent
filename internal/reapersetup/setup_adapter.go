package reapersetup

import (
	"context"
	"fmt"

	"github.com/johnjallday/ori-agent/internal/setupwizard"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// SetupAdapterID is the registry key a blueprint manifest may name for Reaper
// Song. It matches projecttemplates.ValidSetupWizardAdapters.
const SetupAdapterID = "reaper_song"

// The two supported outcomes of REAPER setup. They are the whole reason this
// blueprint's setup is a choice rather than a checklist: one of them is a
// complete, finished answer that installs nothing.
const (
	// ModeFileOnly: Ori works with the one scaffolded project file and nothing
	// else. No plugin, no CLI agent, no native access.
	ModeFileOnly = "file_only"
	// ModeOriAssisted: Ori is configured to attempt live REAPER control, which
	// needs the plugin, a compatible CLI agent, and two explicit permissions.
	ModeOriAssisted = "ori_assisted"
)

// SetupAdapter answers the Setup Wizard's questions about a Reaper Song
// workspace.
//
// REAPER is the blueprint where "ready" is easiest to overclaim, so this
// adapter is careful about two things.
//
// First, file-only is a real answer. A user who wants Ori to edit one project
// file has finished setup — they have not skipped it, and they must not be
// nagged. That is why the mode is recorded rather than inferred: without it,
// "chose the simpler path" and "never finished" look identical from the
// outside, which is exactly what the old pending-setup-task heuristic could not
// tell apart.
//
// Second, ori_ready means Ori's own prerequisites are in place — plugin
// attached, a compatible agent, both permissions granted. It does not mean
// REAPER is running, the project is open, or Web Remote is reachable. Nothing
// here checks those, so nothing here says them; live verification is a separate
// operational check that reports not_checked until something actually asks
// REAPER.
type SetupAdapter struct {
	resolver *Resolver
}

// NewSetupAdapter builds the adapter over the readiness resolver.
func NewSetupAdapter(resolver *Resolver) *SetupAdapter {
	return &SetupAdapter{resolver: resolver}
}

// ID implements setupwizard.Adapter.
func (a *SetupAdapter) ID() string { return SetupAdapterID }

// Evaluate reports where each step stands. Read-only: no plugin is installed,
// enabled, or attached, no agent assigned, no permission granted, and no .rpp
// touched by looking.
func (a *SetupAdapter) Evaluate(_ context.Context, req setupwizard.StepRequest) (setupwizard.StepReadiness, error) {
	if a == nil || a.resolver == nil {
		return setupwizard.StepReadiness{
			Blocked:       true,
			Summary:       "REAPER setup is unavailable in this build.",
			ErrorCategory: setupwizard.ErrorCategoryUnavailable,
		}, nil
	}
	readiness, err := a.resolver.Resolve(req.WorkspaceID)
	if err != nil {
		return setupwizard.StepReadiness{
			Blocked:       true,
			Summary:       "Ori could not check this workspace's REAPER setup.",
			ErrorCategory: setupwizard.ErrorCategoryDomainError,
		}, nil
	}
	switch req.Step.Kind {
	case workspace.SetupStepKindPluginReadiness:
		return modeReadiness(readiness, effectiveMode(readiness, req)), nil
	default:
		return overallReadiness(readiness, effectiveMode(readiness, req)), nil
	}
}

// chosenMode is the workspace's recorded answer, from whichever step asked it.
// The later steps check the prerequisites for a decision made on an earlier
// step, so reading only their own SelectedOption would leave them permanently
// unanswered. The mode's step ID is the manifest author's to name, so this
// matches on the recorded value instead of a hard-coded step ID.
func chosenMode(req setupwizard.StepRequest) string {
	if isMode(req.SelectedOption) {
		return req.SelectedOption
	}
	for _, selected := range req.Selections {
		if isMode(selected) {
			return selected
		}
	}
	return ""
}

// effectiveMode is chosenMode plus the one inference that is not a guess.
//
// A workspace that predates this wizard recorded no answer, and asking someone
// who wired up the plugin, the agent, and both permissions months ago to please
// choose a mode is a migration artifact, not a question. Full Ori-assisted
// readiness is only reachable by having taken that path deliberately, so it is
// read as the answer. Anything short of it stays unanswered: a workspace with
// no plugin is equally consistent with "chose file-only" and "never finished",
// and those must not be conflated.
func effectiveMode(readiness Readiness, req setupwizard.StepRequest) string {
	if mode := chosenMode(req); mode != "" {
		return mode
	}
	if readiness.Status == StatusOriReady {
		return ModeOriAssisted
	}
	return ""
}

func isMode(value string) bool {
	return value == ModeFileOnly || value == ModeOriAssisted
}

// Confirm records the chosen mode. It performs no installation, enablement,
// attachment, agent assignment, or permission change: each of those is its own
// explicit action at the existing REAPER endpoints, initiated by the user, so
// that choosing a mode can never be the click that granted something.
func (a *SetupAdapter) Confirm(ctx context.Context, req setupwizard.StepRequest, action setupwizard.StepAction) (setupwizard.StepReadiness, error) {
	if a == nil || a.resolver == nil {
		return setupwizard.StepReadiness{}, fmt.Errorf("reaper setup is unavailable")
	}
	if req.Step.Kind == workspace.SetupStepKindPluginReadiness {
		if chosen := action.Option; chosen != "" && chosen != ModeFileOnly && chosen != ModeOriAssisted {
			return setupwizard.StepReadiness{}, fmt.Errorf("%q is not a supported REAPER mode", chosen)
		}
	}
	return a.Evaluate(ctx, req)
}

// modeOptions is the choice itself, with each option saying what it does and —
// as importantly — what it does not.
func modeOptions(selected string) []setupwizard.StepOption {
	return []setupwizard.StepOption{
		{
			ID:    ModeFileOnly,
			Label: "File only",
			Description: "Ori works with the one project file this workspace created. " +
				"No plugin, no permissions, and no control of a running REAPER.",
			Selected: selected == ModeFileOnly,
		},
		{
			ID:    ModeOriAssisted,
			Label: "Ori-assisted REAPER",
			Description: "Ori is set up to try to drive REAPER: the plugin, a compatible CLI agent, " +
				"and two permissions you grant one at a time.",
			Selected: selected == ModeOriAssisted,
		},
	}
}

// modeReadiness reports the mode step, which asks a question and is therefore
// answered by answering it. The prerequisites of the answer belong to the step
// that checks them: leaving this one blocked on a missing plugin would strand
// the user on a step whose only control is the choice they already made, one
// step short of the controls that could fix it.
func modeReadiness(readiness Readiness, selected string) setupwizard.StepReadiness {
	out := setupwizard.StepReadiness{Options: modeOptions(selected)}
	switch selected {
	case ModeFileOnly:
		out.Ready = true
		out.Summary = "Ori will work with this workspace's project file only."
		return out
	case ModeOriAssisted:
		out.Ready = true
		if readiness.Status == StatusOriReady {
			out.Summary = "Ori-assisted REAPER. Its prerequisites are in place."
			return out
		}
		out.Summary = "Ori-assisted REAPER. Its prerequisites are checked in the next step."
		return out
	default:
		out.Summary = "Choose how Ori should work with REAPER."
		out.ErrorCategory = setupwizard.ErrorCategoryNotConfigured
		return out
	}
}

// overallReadiness is the readiness and summary steps' verdict: whatever the
// chosen mode requires, and nothing more.
func overallReadiness(readiness Readiness, selected string) setupwizard.StepReadiness {
	switch selected {
	case ModeFileOnly:
		return setupwizard.StepReadiness{
			Ready: true,
			// Said plainly, because this is the step where a user decides whether
			// they are done: Ori has not looked at REAPER at all.
			Summary: "Set up for file-only work. Ori has not checked whether REAPER is running.",
		}
	case ModeOriAssisted:
		if readiness.Status == StatusOriReady {
			return setupwizard.StepReadiness{
				Ready: true,
				// The ceiling, in the sentence that reports success.
				Summary: "Ori's prerequisites are in place. Whether REAPER is actually running is checked separately, when something asks it.",
			}
		}
		return setupwizard.StepReadiness{
			Summary:       blockerSummary(readiness),
			ErrorCategory: blockerCategory(readiness.Status),
		}
	default:
		return setupwizard.StepReadiness{
			Summary:       "Choose how Ori should work with REAPER first.",
			ErrorCategory: setupwizard.ErrorCategoryNotConfigured,
		}
	}
}

// blockerSummary names the one outstanding prerequisite.
//
// It deliberately does not reuse the resolver's explanation. That sentence is
// written from the resolver's own inference of the project's mode — it calls a
// workspace with no plugin a "file-only project", which is precisely the wrong
// thing to tell someone who just chose the assisted path, and it names the
// project file, which the wizard's payload keeps out of logs.
func blockerSummary(readiness Readiness) string {
	switch readiness.Status {
	case StatusPluginMissing:
		return "The REAPER plugin is not installed yet."
	case StatusPluginDisabled:
		return "The REAPER plugin is installed but not enabled."
	case StatusPluginDetached:
		return "The REAPER plugin is not attached to this workspace yet."
	case StatusCLIAgentRequired:
		return "This workspace needs an agent that can run REAPER's tools."
	case StatusNativeCLIAccessRequired:
		return "Ori still needs your permission to let the agent use REAPER's tools directly."
	default:
		return "Ori-assisted REAPER is not set up yet."
	}
}

// blockerCategory maps an outstanding prerequisite onto a stable safe category.
// A missing permission is deliberately its own category: it is the user's to
// grant, and nothing else can resolve it.
func blockerCategory(status Status) string {
	switch status {
	case StatusNativeCLIAccessRequired:
		return setupwizard.ErrorCategoryPermissionRequired
	default:
		return setupwizard.ErrorCategoryNotConfigured
	}
}
