package server

import (
	"context"
	"strings"

	"github.com/johnjallday/ori-agent/internal/connections"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Deterministic readiness for a workspace's email capability.
//
// Email Ops setup state used to be inferred from whatever the setup agent said
// it had done. That is not a fact — the model can report success it did not
// achieve, or fail on an unrelated error and leave a genuinely-connected
// workspace looking broken. Readiness is now computed from state the server can
// check itself, in the order the user must repair it (FR 32, 33, 35):
//
//	Google connected → Gmail enabled → Gmail healthy → vault usable →
//	linked to THIS workspace → the linked account still has a credential
//
// The first unmet condition is the answer, because repairing a later one while
// an earlier one is broken achieves nothing.

// EmailReadiness is the evaluated state of one workspace's mailbox capability.
// It is safe to return to the browser: no tokens, vault names, or message
// content, and the email address only when the workspace is actually linked.
type EmailReadiness struct {
	// Ready is true only when mail work can actually run right now.
	Ready bool `json:"ready"`
	// Reason is the stable machine code for the first unmet condition
	// (workspace.BlockedReason*). Empty when Ready.
	Reason string `json:"reason,omitempty"`
	// Message explains the state in one sentence.
	Message string `json:"message,omitempty"`
	// Action is the stable code for the exact repair (see emailRepairActions).
	Action string `json:"action,omitempty"`
	// ActionLabel is the button text for that repair.
	ActionLabel string `json:"action_label,omitempty"`
	// ActionURL is where the repair happens, when it lives on another page.
	ActionURL string `json:"action_url,omitempty"`
	// AccountID/EmailAddress describe the linked account, when there is one.
	AccountID    string `json:"account_id,omitempty"`
	EmailAddress string `json:"email_address,omitempty"`
}

// Repair action codes. Each names a concrete thing the user does, never a
// vague "check your settings".
const (
	emailActionConnectGoogle = "connect_google"
	emailActionEnableGmail   = "enable_gmail"
	emailActionReconnect     = "reconnect_gmail"
	emailActionRepairVault   = "repair_vault"
	emailActionLinkAccount   = "link_account"
)

// googleAccountCard is where every connection-level repair happens.
const googleAccountCard = "/settings#google-account"

// emailReadinessEvaluator computes readiness from the account connection, the
// credential vault, and the workspace's own binding. Every collaborator is
// optional: a build without one degrades to "cannot verify" rather than
// claiming readiness it has not checked.
type emailReadinessEvaluator struct {
	connections *connections.Store
	vaults      connections.VaultCatalog
	workspaces  workspace.Store
	accounts    emailAccountResolver
}

func newEmailReadinessEvaluator(
	conns *connections.Store,
	vaults connections.VaultCatalog,
	workspaces workspace.Store,
	accounts emailAccountResolver,
) *emailReadinessEvaluator {
	return &emailReadinessEvaluator{connections: conns, vaults: vaults, workspaces: workspaces, accounts: accounts}
}

// Evaluate reports whether workspaceID can do mail work right now.
func (e *emailReadinessEvaluator) Evaluate(ctx context.Context, workspaceID string) EmailReadiness {
	if e == nil || e.workspaces == nil {
		return EmailReadiness{
			Reason:  workspace.BlockedReasonConnectionRequired,
			Message: "Email isn't available in this build.",
		}
	}

	// 1-4: the connection-level conditions, shared by every workspace.
	if blocked := e.evaluateConnection(ctx); blocked != nil {
		return *blocked
	}

	// 5: does THIS workspace have a mailbox linked?
	ws, err := e.workspaces.Get(workspaceID)
	if err != nil || ws == nil {
		return EmailReadiness{
			Reason:  workspace.BlockedReasonNotLinkedToWorkspace,
			Message: "This workspace could not be loaded, so its email link can't be verified.",
			Action:  emailActionLinkAccount, ActionLabel: "Connect email",
		}
	}
	binding, ok := emailBindingFor(ws)
	if !ok {
		return EmailReadiness{
			Reason:      workspace.BlockedReasonNotLinkedToWorkspace,
			Message:     "Connect your email account to this workspace to start triaging the inbox.",
			Action:      emailActionLinkAccount,
			ActionLabel: "Connect email",
		}
	}

	// 6: the linked account must still exist and hold a usable credential.
	accountID := stringFromConfig(binding.Config, "account_id")
	if e.accounts == nil {
		return EmailReadiness{
			Reason:      workspace.BlockedReasonAccountUnavailable,
			Message:     "The linked email account can't be verified in this build.",
			Action:      emailActionLinkAccount,
			ActionLabel: "Reconnect email",
			AccountID:   accountID,
		}
	}
	acc, err := e.accounts.GetEmailAccount(ctx, accountID)
	if err != nil || acc == nil {
		return EmailReadiness{
			Reason:      workspace.BlockedReasonAccountUnavailable,
			Message:     "The email account linked to this workspace is no longer available. Reconnect it to continue.",
			Action:      emailActionLinkAccount,
			ActionLabel: "Reconnect email",
			AccountID:   accountID,
		}
	}
	if !acc.CredentialsStatus.HasAccessToken && !acc.CredentialsStatus.HasRefreshToken {
		return EmailReadiness{
			Reason:       workspace.BlockedReasonReconnectRequired,
			Message:      "The linked email account needs to be reconnected before Ori can read it.",
			Action:       emailActionReconnect,
			ActionLabel:  "Reconnect Gmail",
			ActionURL:    googleAccountCard,
			AccountID:    acc.ID,
			EmailAddress: acc.EmailAddress,
		}
	}

	return EmailReadiness{Ready: true, AccountID: acc.ID, EmailAddress: acc.EmailAddress}
}

// evaluateConnection checks the workspace-independent conditions, returning the
// first unmet one or nil when the account side is healthy.
func (e *emailReadinessEvaluator) evaluateConnection(ctx context.Context) *EmailReadiness {
	if e.connections == nil {
		return nil // no connection store wired: fall through to the binding checks
	}
	conn, err := e.connections.Load()
	if err != nil {
		return &EmailReadiness{
			Reason:      workspace.BlockedReasonConnectionRequired,
			Message:     "Ori couldn't read your Google connection. Open the Google Account card and try again.",
			Action:      emailActionConnectGoogle,
			ActionLabel: "Open Google Account",
			ActionURL:   googleAccountCard,
		}
	}
	if !conn.HasVerifiedIdentity() {
		return &EmailReadiness{
			Reason:      workspace.BlockedReasonConnectionRequired,
			Message:     "Connect your Google account before this workspace can read email.",
			Action:      emailActionConnectGoogle,
			ActionLabel: "Connect Google",
			ActionURL:   googleAccountCard,
		}
	}

	grant, ok := conn.Grant(connections.ProductGmail)
	if !ok || grant == nil || grant.Health == connections.HealthNotEnabled {
		return &EmailReadiness{
			Reason:      workspace.BlockedReasonCapabilityNotEnabled,
			Message:     "Enable Gmail on your Google account so this workspace can read your inbox.",
			Action:      emailActionEnableGmail,
			ActionLabel: "Enable Gmail",
			ActionURL:   googleAccountCard,
		}
	}
	if grant.Health != connections.HealthHealthy {
		return &EmailReadiness{
			Reason:      workspace.BlockedReasonReconnectRequired,
			Message:     "Your Gmail access needs attention before this workspace can read email.",
			Action:      emailActionReconnect,
			ActionLabel: "Reconnect Gmail",
			ActionURL:   googleAccountCard,
		}
	}

	// The credential is useless if its vault can't be opened, so check the vault
	// before claiming the connection is healthy.
	if e.vaults != nil {
		preflight, err := connections.PreflightVault(ctx, e.vaults, conn.VaultID)
		if err != nil {
			return &EmailReadiness{
				Reason:      workspace.BlockedReasonVaultRepairRequired,
				Message:     "Ori couldn't reach the vault holding your email credentials.",
				Action:      emailActionRepairVault,
				ActionLabel: "Open Google Account",
				ActionURL:   googleAccountCard,
			}
		}
		if preflight.Outcome != connections.VaultOutcomeReady {
			return &EmailReadiness{
				Reason:      workspace.BlockedReasonVaultRepairRequired,
				Message:     vaultRepairMessage(preflight.Outcome),
				Action:      emailActionRepairVault,
				ActionLabel: vaultRepairLabel(preflight.Outcome),
				ActionURL:   googleAccountCard + "?gc_action=" + string(preflight.Outcome),
			}
		}
	}
	return nil
}

func vaultRepairMessage(outcome connections.VaultOutcome) string {
	switch outcome {
	case connections.VaultOutcomeUnlock:
		return "Unlock the vault holding your email credentials so Ori can read your inbox."
	case connections.VaultOutcomeChoose:
		return "Choose which vault holds your email credentials before Ori can read your inbox."
	case connections.VaultOutcomeCreate:
		return "Create a vault to hold your email credentials before Ori can read your inbox."
	default:
		return "The vault holding your email credentials is unavailable. Repair it to continue."
	}
}

func vaultRepairLabel(outcome connections.VaultOutcome) string {
	switch outcome {
	case connections.VaultOutcomeUnlock:
		return "Unlock vault"
	case connections.VaultOutcomeChoose:
		return "Choose vault"
	case connections.VaultOutcomeCreate:
		return "Create vault"
	default:
		return "Repair vault"
	}
}

// EvaluateTaskCapability claims only the existing abstract email key. Ordinary
// toolbox/planning keys remain unclaimed, while the composite gate can register
// other runtime evaluators without adding domain conditionals here.
func (e *emailReadinessEvaluator) EvaluateTaskCapability(workspaceID, capability string) (bool, *workspace.TaskBlockedError) {
	if e == nil || strings.ToLower(strings.TrimSpace(capability)) != workspace.CapabilityEmail {
		return false, nil
	}
	readiness := e.Evaluate(context.Background(), workspaceID)
	if readiness.Ready {
		return true, nil
	}
	return true, &workspace.TaskBlockedError{
		ReasonCode:       readiness.Reason,
		Reason:           readiness.Message,
		Question:         readiness.Message,
		SuggestedActions: emailRepairActions(readiness),
		Repair: &workspace.TaskRepairAction{
			Code:  readiness.Action,
			Label: readiness.ActionLabel,
			URL:   readiness.ActionURL,
		},
	}
}

// CheckTaskCapabilities retains the legacy direct-gate surface for callers and
// tests outside server wiring. Production registers this evaluator behind the
// composite; behavior and reason/action codes are identical.
func (e *emailReadinessEvaluator) CheckTaskCapabilities(workspaceID string, capabilities []string) *workspace.TaskBlockedError {
	for _, key := range workspace.NormalizeCapabilityKeys(capabilities) {
		claimed, blocked := e.EvaluateTaskCapability(workspaceID, key)
		if claimed && blocked != nil {
			return blocked
		}
	}
	return nil
}

// emailRepairActions renders the single concrete repair. It deliberately offers
// ONE action: a blocked mailbox has exactly one next step, and listing
// alternatives that cannot fix it (switch agent, let Ori decide) is what made
// these blocks unactionable before.
func emailRepairActions(readiness EmailReadiness) []string {
	label := strings.TrimSpace(readiness.ActionLabel)
	if label == "" {
		return nil
	}
	if url := strings.TrimSpace(readiness.ActionURL); url != "" {
		return []string{label + " (" + url + ")"}
	}
	return []string{label}
}
