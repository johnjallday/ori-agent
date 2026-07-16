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

// personalHQMailboxLinker implements personalhqhttp.MailboxLinker: it attaches or
// detaches an OAuth-connected email account to the user's designated Personal HQ
// by managing an email MCP binding on the HQ workspace (task 3.10). Because the
// mail tools are gated to the Inbox specialist by mailboxAccess, linking does not
// need to rewrite every agent's access — the binding presence is enough, and the
// role gate keeps email off non-Inbox agents.
type personalHQMailboxLinker struct {
	hq          *personalhq.Service
	workspaces  workspace.Store
	accounts    emailAccountResolver
	invalidator accountInvalidator
}

func newPersonalHQMailboxLinker(hq *personalhq.Service, workspaces workspace.Store, accounts emailAccountResolver, invalidator accountInvalidator) *personalHQMailboxLinker {
	return &personalHQMailboxLinker{hq: hq, workspaces: workspaces, accounts: accounts, invalidator: invalidator}
}

// hqWorkspace resolves the user's designated, valid Personal HQ workspace.
func (l *personalHQMailboxLinker) hqWorkspace(ctx context.Context, userID string) (*workspace.Workspace, error) {
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

func (l *personalHQMailboxLinker) MailboxStatus(ctx context.Context, userID string) (personalhqhttp.MailboxStatus, error) {
	ws, err := l.hqWorkspace(ctx, userID)
	if err != nil {
		return personalhqhttp.MailboxStatus{}, err
	}
	binding, ok := emailBindingFor(ws)
	if !ok {
		return personalhqhttp.MailboxStatus{Connected: false}, nil
	}
	accountID := stringFromConfig(binding.Config, "account_id")
	acc, err := l.accounts.GetEmailAccount(ctx, accountID)
	if err != nil || acc == nil {
		// Binding exists but the account is gone → surface as needing repair.
		return personalhqhttp.MailboxStatus{Connected: false, AccountID: accountID, Health: "disconnected"}, nil
	}
	return personalhqhttp.MailboxStatus{
		Connected:    true,
		AccountID:    acc.ID,
		EmailAddress: acc.EmailAddress,
		Health:       accountHealth(acc.CredentialsStatus),
	}, nil
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

func (l *personalHQMailboxLinker) LinkMailbox(ctx context.Context, userID, accountID string) (personalhqhttp.MailboxStatus, error) {
	ws, err := l.hqWorkspace(ctx, userID)
	if err != nil {
		return personalhqhttp.MailboxStatus{}, err
	}
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
		Enabled:    true,
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
	return l.MailboxStatus(ctx, userID)
}

func (l *personalHQMailboxLinker) UnlinkMailbox(ctx context.Context, userID string) (personalhqhttp.MailboxStatus, error) {
	ws, err := l.hqWorkspace(ctx, userID)
	if err != nil {
		return personalhqhttp.MailboxStatus{}, err
	}
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
