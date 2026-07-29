package downloadsjanitor

import (
	"context"
	"fmt"

	"github.com/johnjallday/ori-agent/internal/setupwizard"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// SetupAdapterID is the registry key a blueprint manifest may name for this
// domain. It matches projecttemplates.ValidSetupWizardAdapters.
const SetupAdapterID = "downloads_janitor"

// SetupAdapter answers the Setup Wizard's questions about a Downloads Janitor
// workspace, and performs the one action the wizard itself commits.
//
// It owns no state and duplicates no logic: every answer is derived from
// Service.Status, which is the same readiness the Janitor's own surfaces show.
// That is the point of the adapter boundary — a workspace cannot be "ready
// according to the wizard" and "needs attention according to the Janitor" at
// the same time, because there is only one evaluation.
//
// Two things it deliberately does not do:
//
//   - It never accepts a filesystem path. Choosing a folder is a real grant, so
//     it goes through the Janitor's own owner-scoped setup endpoint with the
//     path the native picker returned; the wizard then re-reads status. A path
//     arriving through a generic wizard action would be a second, weaker door
//     into the same capability.
//   - It never switches automation on as a side effect. The folder step grants
//     access; the watcher and the daily scan start only when the user confirms
//     the step that describes them.
type SetupAdapter struct {
	service *Service
	// automation brings the watcher in line with the settings after the user
	// approves it, so approval takes effect immediately rather than at the next
	// restart. Optional: without it the readiness step reports the watcher as
	// not running, which is visible and repairable.
	automation WatcherSync
}

// WatcherSync is the slice of the automation service the adapter needs.
type WatcherSync interface {
	EnsureWatcher(workspaceID string) error
}

// NewSetupAdapter builds the adapter over the Janitor service.
func NewSetupAdapter(service *Service) *SetupAdapter {
	return &SetupAdapter{service: service}
}

// SetAutomation wires the watcher lifecycle.
func (a *SetupAdapter) SetAutomation(automation WatcherSync) {
	if a != nil {
		a.automation = automation
	}
}

// ID implements setupwizard.Adapter.
func (a *SetupAdapter) ID() string { return SetupAdapterID }

// Evaluate reports where each step stands. It is strictly read-only: no folder
// is chosen, no watcher registered, and no setting changed by looking.
func (a *SetupAdapter) Evaluate(_ context.Context, req setupwizard.StepRequest) (setupwizard.StepReadiness, error) {
	if a == nil || a.service == nil {
		return setupwizard.StepReadiness{
			Blocked:       true,
			Summary:       "Downloads Janitor is unavailable in this build.",
			ErrorCategory: setupwizard.ErrorCategoryUnavailable,
		}, nil
	}
	status, err := a.service.Status(req.WorkspaceID)
	if err != nil {
		return setupwizard.StepReadiness{
			Blocked:       true,
			Summary:       "Ori could not read this workspace's Downloads settings.",
			ErrorCategory: setupwizard.ErrorCategoryDomainError,
		}, nil
	}
	switch req.Step.Kind {
	case workspace.SetupStepKindDirectory:
		return directoryReadiness(status), nil
	case workspace.SetupStepKindAutomationReview:
		return automationReadiness(status), nil
	default:
		// readiness and summary both ask the same question — is the Janitor
		// actually working? — so they get the same answer.
		return overallReadiness(status), nil
	}
}

// Confirm performs a step's committing action after explicit approval.
//
// Only the automation step has one. The folder step's commit happens at the
// Janitor's own endpoint (it carries a path); confirming it here re-reads
// status so the wizard reflects what that endpoint did, and refuses to advance
// while no folder has been chosen.
func (a *SetupAdapter) Confirm(ctx context.Context, req setupwizard.StepRequest, _ setupwizard.StepAction) (setupwizard.StepReadiness, error) {
	if a == nil || a.service == nil {
		return setupwizard.StepReadiness{}, fmt.Errorf("downloads janitor is unavailable")
	}
	if req.Step.Kind != workspace.SetupStepKindAutomationReview {
		return a.Evaluate(ctx, req)
	}

	status, err := a.service.Status(req.WorkspaceID)
	if err != nil {
		return setupwizard.StepReadiness{}, err
	}
	if status.Settings.RootPath == "" {
		// Nothing to automate yet. Returning the pending readiness (rather than
		// an error) keeps the wizard on the folder step with a reason.
		return automationReadiness(status), nil
	}

	// This is the activation the user just approved: record the approval and
	// resume unattended work for a workspace the wizard deliberately configured
	// paused. It is idempotent, so a retry after a timeout resumes the same
	// workspace rather than registering a second watcher.
	if _, err := a.service.ApproveAutomation(req.WorkspaceID); err != nil {
		return setupwizard.StepReadiness{}, err
	}
	if a.automation != nil {
		if err := a.automation.EnsureWatcher(req.WorkspaceID); err != nil {
			// The setting is saved; the watcher is what failed. Report it as a
			// blocked step the user can retry rather than losing the approval.
			return setupwizard.StepReadiness{
				Blocked:       true,
				Summary:       "Ori saved your approval but could not start folder watching. Try again.",
				ErrorCategory: setupwizard.ErrorCategoryDomainError,
			}, nil
		}
	}
	refreshed, err := a.service.Status(req.WorkspaceID)
	if err != nil {
		return setupwizard.StepReadiness{}, err
	}
	return automationReadiness(refreshed), nil
}

// directoryReadiness reports whether a folder has been chosen and is usable.
// Its summaries never contain the path: the folder step's own content shows it,
// read from the Janitor's status, so the wizard's payload stays free of local
// paths.
func directoryReadiness(status Status) setupwizard.StepReadiness {
	if status.Settings.RootPath == "" {
		return setupwizard.StepReadiness{
			Summary:       "No folder chosen yet.",
			ErrorCategory: setupwizard.ErrorCategoryNotConfigured,
		}
	}
	for _, component := range []ReadinessComponent{ComponentDirectoryAccess, ComponentDestination} {
		if check, ok := findCheck(status.Readiness, component); ok && check.Status == ComponentFailed {
			return setupwizard.StepReadiness{
				Blocked:       true,
				Summary:       check.Message,
				ErrorCategory: categoryFor(check.Code),
			}
		}
	}
	return setupwizard.StepReadiness{Ready: true, Summary: "Folder chosen, and Ori can read it."}
}

// automationReadiness reports whether unattended work is actually running.
//
// A paused workspace is pending, not blocked: before the user approves the
// automation step the wizard configures it that way on purpose, and "not
// started yet" is a different statement from "something is broken".
func automationReadiness(status Status) setupwizard.StepReadiness {
	if status.Settings.RootPath == "" {
		return setupwizard.StepReadiness{
			Summary:       "Choose a folder first.",
			ErrorCategory: setupwizard.ErrorCategoryNotConfigured,
		}
	}
	// A watcher or scheduler that failed to register is broken, whether or not
	// the user has approved automation — that is a repair, not a decision.
	for _, component := range []ReadinessComponent{ComponentWatcher, ComponentScheduler} {
		if check, ok := findCheck(status.Readiness, component); ok && check.Status == ComponentFailed {
			return setupwizard.StepReadiness{
				Blocked:       true,
				Summary:       check.Message,
				ErrorCategory: categoryFor(check.Code),
			}
		}
	}
	if automationApproved(status) {
		if status.Settings.Paused {
			// Approved, then paused. Pausing is an operational choice the user
			// makes about something they already agreed to; treating it as
			// unfinished setup would nag them for having used a feature.
			return setupwizard.StepReadiness{
				Ready:   true,
				Summary: "Approved, and currently paused by you.",
			}
		}
		return setupwizard.StepReadiness{
			Ready:   true,
			Summary: "Watching this folder, with a daily catch-up scan.",
		}
	}
	return setupwizard.StepReadiness{
		Summary:       "Not running yet. Approving this step starts the watcher and the daily scan.",
		ErrorCategory: setupwizard.ErrorCategoryNotConfigured,
	}
}

// automationApproved reports whether the user has agreed to unattended work.
//
// The recorded approval is the answer. The fallback covers workspaces
// configured before that timestamp existed: one that is running unattended
// today was set up by someone who saw the same disclosure, so re-asking would
// be a migration artifact rather than a real question.
func automationApproved(status Status) bool {
	if !status.Settings.AutomationApprovedAt.IsZero() {
		return true
	}
	if status.Settings.Paused {
		return false
	}
	for _, component := range []ReadinessComponent{ComponentWatcher, ComponentScheduler} {
		if check, ok := findCheck(status.Readiness, component); !ok || check.Status != ComponentOK {
			return false
		}
	}
	return true
}

// overallReadiness maps the Janitor's own workspace-level state onto the step.
// The wizard adds no judgment of its own here — if the Janitor says it needs
// attention, so does setup.
func overallReadiness(status Status) setupwizard.StepReadiness {
	switch status.Readiness.State {
	case ReadinessReady:
		if !automationApproved(status) {
			// The Janitor reports a paused workspace as ready-but-paused, which
			// is right for its own surface. Here it means the user has not
			// finished setup, so say that instead of claiming completion.
			return setupwizard.StepReadiness{
				Summary:       "Waiting for you to approve the watcher and the daily scan.",
				ErrorCategory: setupwizard.ErrorCategoryNotConfigured,
			}
		}
		return setupwizard.StepReadiness{
			Ready:   true,
			Summary: "Everything Downloads Janitor needs is working.",
		}
	case ReadinessNeedsAttention:
		if failing := status.Readiness.Failing(); len(failing) > 0 {
			return setupwizard.StepReadiness{
				Blocked:       true,
				Summary:       failing[0].Message,
				ErrorCategory: categoryFor(failing[0].Code),
			}
		}
		return setupwizard.StepReadiness{
			Blocked:       true,
			Summary:       "Something Downloads Janitor needs stopped working.",
			ErrorCategory: setupwizard.ErrorCategoryDomainError,
		}
	default:
		return setupwizard.StepReadiness{
			Summary:       "Setup is not finished yet.",
			ErrorCategory: setupwizard.ErrorCategoryNotConfigured,
		}
	}
}

func findCheck(readiness Readiness, component ReadinessComponent) (ComponentCheck, bool) {
	for _, check := range readiness.Checks {
		if check.Component == component {
			return check, true
		}
	}
	return ComponentCheck{}, false
}

// categoryFor maps a Janitor failure code onto the wizard's stable, safe
// categories. Anything unrecognized degrades to domain_error rather than
// leaking a code the wizard's vocabulary does not define.
func categoryFor(code string) string {
	switch code {
	case CodePermissionDenied:
		return setupwizard.ErrorCategoryPermissionRequired
	case CodePending:
		return setupwizard.ErrorCategoryNotConfigured
	case "":
		return ""
	default:
		return setupwizard.ErrorCategoryDomainError
	}
}
