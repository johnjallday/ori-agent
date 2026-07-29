package server

import (
	"context"
	"fmt"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/setupwizard"
)

// emailSetupAdapterID is the registry key a blueprint manifest may name for
// Email Ops. It matches projecttemplates.ValidSetupWizardAdapters.
const emailSetupAdapterID = "email_ops"

// emailSetupAdapter answers the Setup Wizard's questions about an Email Ops
// workspace.
//
// Mail readiness is already layered, and the layers are the point: a connected
// account, a mail permission on it, a credential still in the vault, and a
// mailbox linked to *this* workspace are four different things that all look
// like "email is set up" from the outside. The existing evaluator decides which
// one is unmet and names the exact repair; this adapter translates that verdict
// into a step, and adds no judgment of its own.
//
// It commits nothing. Connecting an account, granting mail access, and linking
// a mailbox happen at endpoints that already own those boundaries — the wizard
// says what is missing and re-reads afterwards.
type emailSetupAdapter struct {
	readiness *emailReadinessEvaluator
}

func newEmailSetupAdapter(readiness *emailReadinessEvaluator) *emailSetupAdapter {
	return &emailSetupAdapter{readiness: readiness}
}

// ID implements setupwizard.Adapter.
func (a *emailSetupAdapter) ID() string { return emailSetupAdapterID }

// Evaluate reports where each step stands. Read-only: no account is connected,
// no permission requested, and no mailbox linked by looking.
func (a *emailSetupAdapter) Evaluate(ctx context.Context, req setupwizard.StepRequest) (setupwizard.StepReadiness, error) {
	if a == nil || a.readiness == nil {
		return setupwizard.StepReadiness{
			Blocked:       true,
			Summary:       "Email setup is unavailable in this build.",
			ErrorCategory: setupwizard.ErrorCategoryUnavailable,
		}, nil
	}
	return emailStepReadiness(a.readiness.Evaluate(ctx, req.WorkspaceID)), nil
}

// Confirm re-reads readiness. Every mail mutation belongs to an endpoint that
// enforces its own boundary; a step passes when the domain says it is satisfied.
func (a *emailSetupAdapter) Confirm(ctx context.Context, req setupwizard.StepRequest, _ setupwizard.StepAction) (setupwizard.StepReadiness, error) {
	if a == nil || a.readiness == nil {
		return setupwizard.StepReadiness{}, fmt.Errorf("email setup is unavailable")
	}
	return a.Evaluate(ctx, req)
}

// emailStepReadiness translates the mail verdict into the wizard's vocabulary.
//
// The distinction that matters is between "you have not done this yet" and
// "something that was working has broken". Connecting an account for the first
// time is unfinished setup; a credential that has vanished from the vault, or a
// linked account that no longer exists, is a repair. Reporting the second as
// the first would send a user back through a setup they already completed.
func emailStepReadiness(readiness EmailReadiness) setupwizard.StepReadiness {
	if readiness.Ready {
		return setupwizard.StepReadiness{
			Ready:   true,
			Summary: "A mailbox is linked to this workspace and Ori can read it.",
		}
	}

	out := setupwizard.StepReadiness{Summary: readiness.Message}
	if out.Summary == "" {
		out.Summary = "Email setup is not finished yet."
	}
	switch readiness.Action {
	case emailActionConnectGoogle, emailActionEnableGmail:
		// Neither has been granted yet: unfinished, and the grant is the user's
		// to give in Settings.
		out.ErrorCategory = setupwizard.ErrorCategoryPermissionRequired
	case emailActionLinkAccount:
		// The account side is healthy; this workspace simply has no mailbox
		// linked yet. That is the one thing the wizard's own step does.
		out.ErrorCategory = setupwizard.ErrorCategoryNotConfigured
	case emailActionReconnect, emailActionRepairVault:
		// Previously working, now broken.
		out.Blocked = true
		out.ErrorCategory = setupwizard.ErrorCategoryPermissionRequired
	default:
		out.ErrorCategory = setupwizard.ErrorCategoryNotConfigured
	}
	return out
}

// wireEmailSetupAdapter registers the Email Ops adapter once the readiness
// evaluator exists. Kept beside the adapter so the one thing a reader needs to
// check — that the registry key matches what a manifest may name — is visible
// in a single file.
func (b *ServerBuilder) wireEmailSetupAdapter(registry *setupwizard.Registry) {
	if b == nil || registry == nil || b.emailReadiness == nil {
		return
	}
	if err := registry.Register(newEmailSetupAdapter(b.emailReadiness)); err != nil {
		logger.Warn("Email Ops setup adapter not registered", logger.Fields{"error": err})
	}
}
