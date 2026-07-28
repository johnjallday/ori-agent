package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/personalhqhttp"
	"github.com/johnjallday/ori-agent/internal/vault"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// accountInvalidator lets the linker drop cached reads for an account on
// disconnect (contract §6). *mailbox.CachingProvider satisfies it.
type accountInvalidator interface {
	InvalidateAccount(accountID string)
}

// mailboxLinkerService attaches or detaches an OAuth-connected email account to a
// workspace by managing an email MCP binding on it. Because the mail tools are
// gated to the Inbox specialist by mailboxAccess, linking does not need to
// rewrite every agent's access — the binding presence is enough, and the role
// gate keeps email off non-Inbox agents (so the Email Ops Postmaster delegates
// to Inbox rather than reading mail directly).
//
// It satisfies two interfaces over a single shared core (the *ForWorkspace
// methods): personalhqhttp.MailboxLinker, whose user-keyed methods target the
// user's designated Personal HQ, and personalhqhttp.WorkspaceMailboxLinker,
// whose workspace-keyed methods target any workspace the user owns (e.g. an
// Email Ops workspace, which needs no Personal HQ to exist).
type mailboxLinkerService struct {
	hq          *personalhq.Service
	workspaces  workspace.Store
	accounts    emailAccountResolver
	invalidator accountInvalidator
	// readiness computes the deterministic setup state reported alongside every
	// status, so the UI never has to infer setup from an agent's narration
	// (FR 32). Nil omits the setup block.
	readiness *emailReadinessEvaluator
}

func newMailboxLinkerService(hq *personalhq.Service, workspaces workspace.Store, accounts emailAccountResolver, invalidator accountInvalidator) *mailboxLinkerService {
	return &mailboxLinkerService{hq: hq, workspaces: workspaces, accounts: accounts, invalidator: invalidator}
}

// hqWorkspace resolves the user's designated, valid Personal HQ workspace.
func (l *mailboxLinkerService) hqWorkspace(ctx context.Context, userID string) (*workspace.Workspace, error) {
	if l == nil || l.hq == nil || l.workspaces == nil {
		return nil, fmt.Errorf("personal hq email is not configured")
	}
	status, err := l.hq.Status(ctx, userID)
	if err != nil {
		return nil, err
	}
	if status == nil || !status.Valid {
		return nil, fmt.Errorf("no valid Personal HQ is designated")
	}
	ws, err := l.workspaces.Get(status.WorkspaceID)
	if err != nil || ws == nil {
		return nil, fmt.Errorf("the Personal HQ workspace could not be loaded")
	}
	return ws, nil
}

// ownedWorkspace resolves a workspace the user owns, by explicit ID. It requires
// no Personal HQ. A workspace with a set owner that is not this user is denied;
// an empty owner (legacy/local single-user records) is permitted.
func (l *mailboxLinkerService) ownedWorkspace(_ context.Context, userID, workspaceID string) (*workspace.Workspace, error) {
	if l == nil || l.workspaces == nil {
		return nil, fmt.Errorf("email linking is not configured")
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return nil, fmt.Errorf("workspace_id is required")
	}
	ws, err := l.workspaces.Get(workspaceID)
	if err != nil || ws == nil {
		return nil, fmt.Errorf("the workspace could not be loaded")
	}
	owner := strings.TrimSpace(ws.OwnerUserID)
	if owner != "" && !strings.EqualFold(owner, strings.TrimSpace(userID)) {
		return nil, fmt.Errorf("that workspace belongs to a different user")
	}
	return ws, nil
}

// ---- Shared core: operate on an explicit workspace ------------------------

// mailboxStatusForWorkspace reports the email connection state of ws.
func (l *mailboxLinkerService) mailboxStatusForWorkspace(ctx context.Context, ws *workspace.Workspace) (personalhqhttp.MailboxStatus, error) {
	binding, ok := emailBindingFor(ws)
	if !ok {
		return l.withSetup(ctx, ws, personalhqhttp.MailboxStatus{Connected: false}), nil
	}
	accountID := stringFromConfig(binding.Config, "account_id")
	acc, err := l.accounts.GetEmailAccount(ctx, accountID)
	if err != nil || acc == nil {
		// Binding exists but the account is gone → surface as needing repair.
		return l.withSetup(ctx, ws, personalhqhttp.MailboxStatus{Connected: false, AccountID: accountID, Health: "disconnected"}), nil
	}
	return l.withSetup(ctx, ws, personalhqhttp.MailboxStatus{
		Connected:    true,
		AccountID:    acc.ID,
		EmailAddress: acc.EmailAddress,
		Health:       accountHealth(acc.CredentialsStatus),
	}), nil
}

// withSetup attaches the deterministic readiness verdict to a status. Because
// every link/unlink call returns a freshly-computed status, the setup state the
// UI shows is always current the moment an operation completes (FR 36).
func (l *mailboxLinkerService) withSetup(ctx context.Context, ws *workspace.Workspace, status personalhqhttp.MailboxStatus) personalhqhttp.MailboxStatus {
	if l == nil || l.readiness == nil || ws == nil {
		return status
	}
	readiness := l.readiness.Evaluate(ctx, ws.ID)
	status.Setup = &personalhqhttp.MailboxSetupState{
		Ready:       readiness.Ready,
		Reason:      readiness.Reason,
		Message:     readiness.Message,
		Action:      readiness.Action,
		ActionLabel: readiness.ActionLabel,
		ActionURL:   readiness.ActionURL,
	}
	return status
}

// linkMailboxToWorkspace attaches an already OAuth-connected account to ws by
// upserting the email MCP binding. OAuth credentials live in the global vault
// keyed by account, so attaching an existing account never re-authorizes.
func (l *mailboxLinkerService) linkMailboxToWorkspace(ctx context.Context, ws *workspace.Workspace, accountID string) (personalhqhttp.MailboxStatus, error) {
	accountID = strings.TrimSpace(accountID)
	acc, err := l.accounts.GetEmailAccount(ctx, accountID)
	if err != nil || acc == nil {
		return personalhqhttp.MailboxStatus{}, fmt.Errorf("email account %q was not found", accountID)
	}
	if acc.WorkspaceID != "" && !strings.EqualFold(strings.TrimSpace(acc.WorkspaceID), strings.TrimSpace(ws.ID)) {
		return personalhqhttp.MailboxStatus{}, fmt.Errorf("that email account is scoped to a different workspace")
	}

	// Reuse the existing email binding's ID if present, so re-linking updates in
	// place rather than accumulating bindings.
	bindingID := uuid.NewString()
	if existing, ok := emailBindingFor(ws); ok {
		bindingID = existing.ID
	}
	if err := ws.UpsertMCPBinding(workspace.MCPBinding{
		ID:         bindingID,
		ServerName: "gmail",
		// Mark the binding native so the runtime never looks for an MCP template
		// named "gmail" (FR 29). Relinking rewrites it, which is also how a legacy
		// binding written before this field existed gets upgraded in place.
		RuntimeKind: workspace.RuntimeKindNativeEmail,
		Enabled:     true,
		Config: map[string]any{
			"account_id":      acc.ID,
			"allowed_actions": []any{"read", "search"},
		},
	}); err != nil {
		return personalhqhttp.MailboxStatus{}, err
	}
	if err := l.workspaces.Save(ws); err != nil {
		return personalhqhttp.MailboxStatus{}, err
	}
	return l.mailboxStatusForWorkspace(ctx, ws)
}

// unlinkMailboxFromWorkspace removes ws's email binding and invalidates the
// account's cached reads.
func (l *mailboxLinkerService) unlinkMailboxFromWorkspace(ctx context.Context, ws *workspace.Workspace) (personalhqhttp.MailboxStatus, error) {
	binding, ok := emailBindingFor(ws)
	if !ok {
		return personalhqhttp.MailboxStatus{Connected: false}, nil
	}
	accountID := stringFromConfig(binding.Config, "account_id")
	if err := ws.DeleteMCPBinding(binding.ID); err != nil {
		return personalhqhttp.MailboxStatus{}, err
	}
	if err := l.workspaces.Save(ws); err != nil {
		return personalhqhttp.MailboxStatus{}, err
	}
	if l.invalidator != nil && accountID != "" {
		l.invalidator.InvalidateAccount(accountID)
	}
	return personalhqhttp.MailboxStatus{Connected: false}, nil
}

// accountHealth maps a Vault email secret state to a mailbox health label for the
// UI: a connected account with a usable token is healthy; one whose tokens are
// gone reads as disconnected (needs reconnect).
func accountHealth(state vault.EmailAccountSecretState) string {
	if state.HasAccessToken || state.HasRefreshToken {
		return "healthy"
	}
	return "disconnected"
}

// ---- HQ-scoped front end (personalhqhttp.MailboxLinker) -------------------

func (l *mailboxLinkerService) MailboxStatus(ctx context.Context, userID string) (personalhqhttp.MailboxStatus, error) {
	ws, err := l.hqWorkspace(ctx, userID)
	if err != nil {
		return personalhqhttp.MailboxStatus{}, err
	}
	return l.mailboxStatusForWorkspace(ctx, ws)
}

func (l *mailboxLinkerService) LinkMailbox(ctx context.Context, userID, accountID string) (personalhqhttp.MailboxStatus, error) {
	ws, err := l.hqWorkspace(ctx, userID)
	if err != nil {
		return personalhqhttp.MailboxStatus{}, err
	}
	return l.linkMailboxToWorkspace(ctx, ws, accountID)
}

func (l *mailboxLinkerService) UnlinkMailbox(ctx context.Context, userID string) (personalhqhttp.MailboxStatus, error) {
	ws, err := l.hqWorkspace(ctx, userID)
	if err != nil {
		return personalhqhttp.MailboxStatus{}, err
	}
	return l.unlinkMailboxFromWorkspace(ctx, ws)
}

// ---- Workspace-scoped front end (personalhqhttp.WorkspaceMailboxLinker) ----

func (l *mailboxLinkerService) WorkspaceMailboxStatus(ctx context.Context, userID, workspaceID string) (personalhqhttp.MailboxStatus, error) {
	ws, err := l.ownedWorkspace(ctx, userID, workspaceID)
	if err != nil {
		return personalhqhttp.MailboxStatus{}, err
	}
	return l.mailboxStatusForWorkspace(ctx, ws)
}

func (l *mailboxLinkerService) LinkWorkspaceMailbox(ctx context.Context, userID, workspaceID, accountID string) (personalhqhttp.MailboxStatus, error) {
	ws, err := l.ownedWorkspace(ctx, userID, workspaceID)
	if err != nil {
		return personalhqhttp.MailboxStatus{}, err
	}
	return l.linkMailboxToWorkspace(ctx, ws, accountID)
}

func (l *mailboxLinkerService) UnlinkWorkspaceMailbox(ctx context.Context, userID, workspaceID string) (personalhqhttp.MailboxStatus, error) {
	ws, err := l.ownedWorkspace(ctx, userID, workspaceID)
	if err != nil {
		return personalhqhttp.MailboxStatus{}, err
	}
	return l.unlinkMailboxFromWorkspace(ctx, ws)
}
