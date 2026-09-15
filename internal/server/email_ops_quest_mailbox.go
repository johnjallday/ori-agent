package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/connections"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalhqhttp"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
	"github.com/johnjallday/ori-agent/internal/setupwizard"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Steps 2 and 3 of the Email Ops host quest. Both readers are views over the one
// mailbox readiness authority (emailReadinessEvaluator), so finishing setup on
// the workspace's own wizard or CTA finishes it here, and the reverse. The only
// consequence the quest performs is the reviewed mailbox link, through the same
// sink and linker the workspace's "Connect email" action uses.

const (
	emailOpsWizardMailboxStepID = "mailbox"
	googleAccountSettingsLabel  = "Open Google Account"
	// emailOpsLinkDisclosure is the exact consent copy bound into the review.
	emailOpsLinkDisclosure = "Email Ops will read and search this mailbox and prepare drafts. " +
		"It never sends a message without your confirmation of that specific message. " +
		"No new sign-in happens."
	emailOpsLinkInputDigestSource = "account_link:link_mailbox:v1"
)

// oauthClientConfigured reports whether this server can start Google sign-in.
// It is a seam so tests do not depend on the process environment.
type oauthClientConfigured func() bool

func defaultOAuthClientConfigured() bool {
	_, _, source, verdict := connections.ResolveOAuthClientChecked()
	return source.Configured() && verdict.Problem == ""
}

// emailOpsAccountConnectReader reports the workspace-independent half of mailbox
// readiness: Google identity, Gmail grant, grant health, and vault.
type emailOpsAccountConnectReader struct {
	readiness        *emailReadinessEvaluator
	clientConfigured oauthClientConfigured
}

func (r emailOpsAccountConnectReader) Read(ctx context.Context, _ setupjourney.ReadScope) (setupjourney.CanonicalStepRead, error) {
	if r.readiness == nil || r.readiness.connections == nil {
		return setupjourney.CanonicalStepRead{}, errEmailOpsQuestUnavailable
	}
	configured := r.clientConfigured != nil && r.clientConfigured()
	actions := []setupjourney.ActionID{setupjourney.ActionOpenAccountSettings, setupjourney.ActionRecheckConnection}
	blocked := r.readiness.evaluateConnection(ctx)
	if blocked == nil {
		conn, err := r.readiness.connections.Load()
		if err != nil || conn == nil || !conn.HasVerifiedIdentity() {
			return setupjourney.CanonicalStepRead{}, errEmailOpsQuestUnavailable
		}
		// A working connection is usable whatever the current client setting.
		return setupjourney.CanonicalStepRead{
			Complete: true,
			AccountConnect: &setupjourney.AccountConnectProjection{
				Configured: true, IdentityEmail: questAccountEmail(conn.Email),
				GmailHealth: setupjourney.AccountHealthHealthy,
			},
		}, nil
	}
	projection := &setupjourney.AccountConnectProjection{
		Configured:  configured,
		ActionLabel: questActionLabel(blocked.ActionLabel),
		ActionURL:   questSettingsRoute(blocked.ActionURL),
	}
	if conn, err := r.readiness.connections.Load(); err == nil && conn != nil && conn.HasVerifiedIdentity() {
		projection.IdentityEmail = questAccountEmail(conn.Email)
	}
	read := setupjourney.CanonicalStepRead{AvailableActions: actions, AccountConnect: projection}
	switch blocked.Reason {
	case workspace.BlockedReasonConnectionRequired:
		if !configured {
			// FR 29: the server, not the user, is missing Google sign-in.
			projection.GmailHealth = setupjourney.AccountHealthUnconfigured
			projection.ActionLabel = googleAccountSettingsLabel
			projection.ActionURL = googleAccountCard
			read.BlockedReason = setupjourney.ReasonAccountConnectionNotConfigured
			return read, nil
		}
		// Unfinished first-time setup: the step is active, not blocked.
		projection.GmailHealth = setupjourney.AccountHealthNotConnected
	case workspace.BlockedReasonCapabilityNotEnabled:
		projection.GmailHealth = setupjourney.AccountHealthNotEnabled
		if !configured {
			projection.Configured = true // an identity exists, so sign-in worked before
		}
	case workspace.BlockedReasonReconnectRequired:
		projection.GmailHealth = setupjourney.AccountHealthUnhealthy
		projection.Configured = true
		read.BlockedReason = setupjourney.ReasonAccountReconnectRequired
	case workspace.BlockedReasonVaultRepairRequired:
		projection.GmailHealth = setupjourney.AccountHealthVaultUnavailable
		projection.Configured = true
		read.BlockedReason = setupjourney.ReasonAccountVaultRepairRequired
	default:
		return setupjourney.CanonicalStepRead{}, errEmailOpsQuestUnavailable
	}
	return read, nil
}

// emailOpsAccountLinkReader reports whether the Email Ops workspace step 1
// recorded can do mail work right now. Complete means exactly what
// GET /api/workspaces/{id}/email/status reports as setup.ready.
type emailOpsAccountLinkReader struct {
	readiness  *emailReadinessEvaluator
	workspaces workspace.EmailOpsWorkspaceSource
}

func (r emailOpsAccountLinkReader) Read(ctx context.Context, scope setupjourney.ReadScope) (setupjourney.CanonicalStepRead, error) {
	if r.readiness == nil || r.workspaces == nil {
		return setupjourney.CanonicalStepRead{}, errEmailOpsQuestUnavailable
	}
	workspaceID := strings.TrimSpace(scope.ProjectWorkspaceID)
	if workspaceID == "" {
		// No workspace yet: the framework keeps this step out of reach.
		return setupjourney.CanonicalStepRead{}, nil
	}
	ws, err := r.workspaces.Get(workspaceID)
	if err != nil || ws == nil {
		return setupjourney.CanonicalStepRead{}, nil
	}
	status := r.readiness.Evaluate(ctx, workspaceID)
	_, linked := emailBindingFor(ws)
	projection := &setupjourney.AccountLinkProjection{WorkspaceLabel: questWorkspaceLabel(ws.Name), Linked: linked}
	if status.Ready {
		projection.AccountEmail = questAccountEmail(status.EmailAddress)
		projection.Ready = projection.AccountEmail != ""
		if !projection.Ready {
			return setupjourney.CanonicalStepRead{}, errEmailOpsQuestUnavailable
		}
		return setupjourney.CanonicalStepRead{Complete: true, AccountLink: projection}, nil
	}
	if linked {
		projection.AccountEmail = questAccountEmail(status.EmailAddress)
	} else if r.readiness.connections != nil {
		if conn, loadErr := r.readiness.connections.Load(); loadErr == nil && conn != nil && conn.HasVerifiedIdentity() {
			projection.AccountEmail = questAccountEmail(conn.Email)
		}
	}
	read := setupjourney.CanonicalStepRead{AccountLink: projection}
	switch status.Reason {
	case workspace.BlockedReasonNotLinkedToWorkspace:
		// The one gap this quest closes itself, after review.
		read.AvailableActions = []setupjourney.ActionID{setupjourney.ActionReviewMailboxLink}
	case workspace.BlockedReasonAccountUnavailable:
		read.BlockedReason = setupjourney.ReasonMailboxAccountUnavailable
		read.AvailableActions = []setupjourney.ActionID{setupjourney.ActionOpenAccountSettings}
	case workspace.BlockedReasonReconnectRequired:
		read.BlockedReason = setupjourney.ReasonAccountReconnectRequired
		read.AvailableActions = []setupjourney.ActionID{setupjourney.ActionOpenAccountSettings}
	case workspace.BlockedReasonVaultRepairRequired:
		read.BlockedReason = setupjourney.ReasonAccountVaultRepairRequired
		read.AvailableActions = []setupjourney.ActionID{setupjourney.ActionOpenAccountSettings}
	default:
		// The connection itself regressed; step 2 is current and owns the repair.
	}
	return read, nil
}

type questGmailSink interface {
	LinkGmailToWorkspace(ctx context.Context, credentialRef, vaultID, workspaceID string) (string, error)
}

type questMailboxLinker interface {
	LinkWorkspaceMailbox(ctx context.Context, userID, workspaceID, accountID string) (personalhqhttp.MailboxStatus, error)
}

type questWizardConfirmer interface {
	Confirm(ctx context.Context, workspaceID, stepID string, action setupwizard.StepAction) (setupwizard.Status, error)
}

// emailOpsMailboxLinkAdapter is the one review/commit owner for step 3. Every
// input is server-derived: the workspace from the run's receipt re-checked
// against the Email Ops resolver, the credential from the shared connection.
type emailOpsMailboxLinkAdapter struct {
	readiness  *emailReadinessEvaluator
	resolver   workspace.EmailOpsWorkspaceSource
	workspaces workspace.EmailOpsWorkspaceSource
	sink       questGmailSink
	linker     questMailboxLinker
	wizard     questWizardConfirmer
}

type emailOpsLinkTarget struct {
	workspace *workspace.Workspace
	conn      *connections.Connection
	grant     *connections.ProductGrant
}

func (a *emailOpsMailboxLinkAdapter) InputDigest(action setupjourney.ActionID, raw json.RawMessage) (string, error) {
	if action != setupjourney.ActionReviewMailboxLink && action != setupjourney.ActionLinkMailbox {
		return "", errEmailOpsQuestUnavailable
	}
	if err := decodeEmptyQuestInput(raw); err != nil {
		return "", setupjourney.ErrInvalid
	}
	return setupjourney.Digest([]byte(emailOpsLinkInputDigestSource)), nil
}

func (a *emailOpsMailboxLinkAdapter) Review(ctx context.Context, scope setupjourney.ReadScope, action setupjourney.ActionID, raw json.RawMessage) (setupjourney.ActionReviewMaterial, error) {
	if action != setupjourney.ActionReviewMailboxLink {
		return setupjourney.ActionReviewMaterial{}, errEmailOpsQuestUnavailable
	}
	return a.material(ctx, scope, raw)
}

func (a *emailOpsMailboxLinkAdapter) PrepareCommit(ctx context.Context, scope setupjourney.ReadScope, action setupjourney.ActionID, raw json.RawMessage) (setupjourney.ActionReviewMaterial, error) {
	if action != setupjourney.ActionLinkMailbox {
		return setupjourney.ActionReviewMaterial{}, errEmailOpsQuestUnavailable
	}
	return a.material(ctx, scope, raw)
}

// Commit links the connection's authoritative Gmail credential to the
// workspace (no new sign-in, no new scope), attaches it with read/search only,
// and records the workspace wizard's mailbox step so both surfaces agree.
func (a *emailOpsMailboxLinkAdapter) Commit(ctx context.Context, scope setupjourney.ReadScope, action setupjourney.ActionID, _ json.RawMessage, _ setupjourney.ActionReviewMaterial) (setupjourney.CanonicalResult, error) {
	if action != setupjourney.ActionLinkMailbox || a.sink == nil || a.linker == nil {
		return setupjourney.CanonicalResult{}, errEmailOpsQuestUnavailable
	}
	target, err := a.target(scope)
	if err != nil {
		return setupjourney.CanonicalResult{}, err
	}
	accountID, err := a.sink.LinkGmailToWorkspace(ctx, target.grant.CredentialRef, target.conn.VaultID, target.workspace.ID)
	if err != nil {
		return setupjourney.CanonicalResult{}, fmt.Errorf("resolve the connection's gmail credential: %w", err)
	}
	if _, err := a.linker.LinkWorkspaceMailbox(ctx, scope.OwnerUserID, target.workspace.ID, accountID); err != nil {
		return setupjourney.CanonicalResult{}, fmt.Errorf("link the workspace mailbox: %w", err)
	}
	if a.wizard != nil {
		// Bookkeeping for the workspace's own wizard. The binding is the
		// consequence; the wizard re-derives this step from the same evaluator.
		if _, err := a.wizard.Confirm(ctx, target.workspace.ID, emailOpsWizardMailboxStepID, setupwizard.StepAction{Type: setupwizard.ActionConfirm}); err != nil {
			logger.Warn("Email Ops quest could not record the wizard mailbox step", logger.Fields{"error": err.Error()})
		}
	}
	return setupjourney.CanonicalResult{}, nil
}

// ConsequenceObserved treats a Ready mailbox as the link's consequence: the
// link is idempotent, and readiness is exactly what the user asked for.
func (a *emailOpsMailboxLinkAdapter) ConsequenceObserved(action setupjourney.ActionID, read setupjourney.CanonicalStepRead) bool {
	return action == setupjourney.ActionLinkMailbox && read.Complete && read.AccountLink != nil && read.AccountLink.Ready
}

func (a *emailOpsMailboxLinkAdapter) material(ctx context.Context, scope setupjourney.ReadScope, raw json.RawMessage) (setupjourney.ActionReviewMaterial, error) {
	inputDigest, err := a.InputDigest(setupjourney.ActionLinkMailbox, raw)
	if err != nil {
		return setupjourney.ActionReviewMaterial{}, err
	}
	target, err := a.target(scope)
	if err != nil {
		return setupjourney.ActionReviewMaterial{}, err
	}
	if a.readiness != nil {
		// Only the not-linked verdict is a reviewable link. Anything else is
		// either already done or a repair the review cannot perform.
		if status := a.readiness.Evaluate(ctx, target.workspace.ID); status.Ready || status.Reason != workspace.BlockedReasonNotLinkedToWorkspace {
			return setupjourney.ActionReviewMaterial{}, setupjourney.ErrConflict
		}
	}
	binding, linked := emailBindingFor(target.workspace)
	bindingRevision := "none"
	if linked {
		bindingRevision = setupjourney.Digest([]byte(strings.Join([]string{
			binding.ID, stringFromConfig(binding.Config, "account_id"), fmt.Sprint(binding.Config["allowed_actions"]), fmt.Sprint(binding.Enabled),
		}, "|")))
	}
	label := questWorkspaceLabel(target.workspace.Name)
	email := questAccountEmail(target.conn.Email)
	owner := setupjourney.Digest([]byte(strings.Join([]string{
		"account_link_owner:v1", target.conn.Subject, string(target.grant.Health), target.grant.CredentialRef,
		target.workspace.ID, bindingRevision,
	}, "|")))
	disclosure := setupjourney.Digest([]byte(strings.Join([]string{
		"account_link_disclosure:v1", label, email, emailOpsLinkDisclosure,
	}, "|")))
	return setupjourney.ActionReviewMaterial{
		CommitAction: setupjourney.ActionLinkMailbox, InputDigest: inputDigest,
		OwnerRevisionDigest: owner, DisclosureDigest: disclosure,
		AccountLink: &setupjourney.AccountLinkProjection{WorkspaceLabel: label, AccountEmail: email, Linked: linked},
	}, nil
}

// target re-derives everything the link needs from server state. A receipt that
// no longer names the user's Email Ops workspace, or a connection that changed,
// is a stale review rather than something to link anyway.
func (a *emailOpsMailboxLinkAdapter) target(scope setupjourney.ReadScope) (emailOpsLinkTarget, error) {
	if a.readiness == nil || a.readiness.connections == nil || a.resolver == nil || a.workspaces == nil {
		return emailOpsLinkTarget{}, errEmailOpsQuestUnavailable
	}
	workspaceID := strings.TrimSpace(scope.ProjectWorkspaceID)
	current, err := workspace.ResolveEmailOpsWorkspace(a.resolver, scope.OwnerUserID)
	if err != nil {
		return emailOpsLinkTarget{}, errEmailOpsQuestUnavailable
	}
	if workspaceID == "" || current != workspaceID {
		return emailOpsLinkTarget{}, setupjourney.ErrConflict
	}
	ws, err := a.workspaces.Get(workspaceID)
	if err != nil || ws == nil {
		return emailOpsLinkTarget{}, setupjourney.ErrConflict
	}
	conn, err := a.readiness.connections.Load()
	if err != nil || conn == nil || !conn.HasVerifiedIdentity() {
		return emailOpsLinkTarget{}, setupjourney.ErrConflict
	}
	grant, ok := conn.Grant(connections.ProductGmail)
	if !ok || grant == nil || grant.Health != connections.HealthHealthy || strings.TrimSpace(grant.CredentialRef) == "" {
		return emailOpsLinkTarget{}, setupjourney.ErrConflict
	}
	return emailOpsLinkTarget{workspace: ws, conn: conn, grant: grant}, nil
}

func decodeEmptyQuestInput(raw json.RawMessage) error {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "{}" {
		return nil
	}
	var input map[string]json.RawMessage
	if err := json.Unmarshal(raw, &input); err != nil {
		return err
	}
	if len(input) != 0 {
		return errors.New("this action takes no input")
	}
	return nil
}

func questAccountEmail(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 254 || strings.Count(value, "@") != 1 || strings.ContainsAny(value, " \t\r\n<>\"'\\/") {
		return ""
	}
	return value
}

func questActionLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 60 {
		return googleAccountSettingsLabel
	}
	return value
}

// questSettingsRoute keeps the evaluator's own repair route when it is a plain
// Settings route, and otherwise falls back to the Google Account card.
func questSettingsRoute(value string) string {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "/settings") || strings.Contains(value, "//") || strings.ContainsAny(value, " \t\r\n\\:<>\"'") {
		return googleAccountCard
	}
	return value
}
