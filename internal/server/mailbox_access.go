package server

import (
	"context"
	"strings"

	"github.com/johnjallday/ori-agent/internal/mailbox"
	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/vault"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// emailAccountResolver is the narrow Vault contract needed to resolve a bound
// email account to its token-free identity. *vault.Store satisfies it.
type emailAccountResolver interface {
	GetEmailAccount(ctx context.Context, id string) (*vault.EmailAccount, error)
}

// mailboxAccess implements chathttp.MailboxAccess, enforcing the most-restrictive
// access policy for the Personal HQ mail tools (contract §3.2, task 3.8). Layers,
// all of which must permit:
//   - workspace binding: an enabled email MCP binding naming a read/search account,
//   - per-agent: the executing agent is the Inbox specialist AND is allowed to use
//     that binding (AgentMCPAccess), so email is never authorized workspace-wide,
//   - scope/grant: enforced downstream by the credential resolver + GmailProvider,
//     which surface expired/disconnected as typed errors.
type mailboxAccess struct {
	workspaces workspace.Store
	accounts   emailAccountResolver
	provider   mailbox.MailboxProvider
}

func newMailboxAccess(workspaces workspace.Store, accounts emailAccountResolver, provider mailbox.MailboxProvider) *mailboxAccess {
	return &mailboxAccess{workspaces: workspaces, accounts: accounts, provider: provider}
}

func (a *mailboxAccess) Provider() mailbox.MailboxProvider {
	if a == nil {
		return nil
	}
	return a.provider
}

// CanAccess is the cheap, synchronous exposure gate.
func (a *mailboxAccess) CanAccess(workspaceID, agentName string) bool {
	if a == nil || a.provider == nil || a.workspaces == nil {
		return false
	}
	if !isInboxAgent(agentName) {
		return false
	}
	ws, err := a.workspaces.Get(workspaceID)
	if err != nil || ws == nil {
		return false
	}
	binding, ok := emailBindingFor(ws)
	return ok && agentAllowedForBinding(ws, agentName, binding.ID)
}

// AuthorizedAccount applies the full policy and resolves the readable account.
func (a *mailboxAccess) AuthorizedAccount(ctx context.Context, workspaceID, agentName string) (mailbox.Account, error) {
	if a == nil || a.workspaces == nil || a.accounts == nil {
		return mailbox.Account{}, mailbox.ErrDisconnected
	}
	ws, err := a.workspaces.Get(workspaceID)
	if err != nil || ws == nil {
		return mailbox.Account{}, mailbox.ErrDisconnected
	}
	binding, ok := emailBindingFor(ws)
	if !ok {
		return mailbox.Account{}, mailbox.ErrDisconnected
	}
	if !isInboxAgent(agentName) || !agentAllowedForBinding(ws, agentName, binding.ID) {
		return mailbox.Account{}, mailbox.ErrPermissionDenied
	}
	accountID := stringFromConfig(binding.Config, "account_id")
	if accountID == "" {
		return mailbox.Account{}, mailbox.ErrDisconnected
	}
	acc, err := a.accounts.GetEmailAccount(ctx, accountID)
	if err != nil || acc == nil {
		return mailbox.Account{}, mailbox.ErrDisconnected
	}
	// FR 42: the binding must be HEALTHY, not merely present. An account whose
	// tokens are gone would otherwise fail deep inside the provider with an error
	// the agent reads as a transient mail problem.
	if !acc.CredentialsStatus.HasAccessToken && !acc.CredentialsStatus.HasRefreshToken {
		return mailbox.Account{}, mailbox.ErrExpired
	}
	return mailbox.Account{
		ID:           acc.ID,
		Provider:     string(acc.Provider),
		EmailAddress: acc.EmailAddress,
		Health:       mailbox.HealthHealthy,
	}, nil
}

// isInboxAgent reports whether agentName is the canonical Personal HQ Inbox
// specialist — the only role authorized to read mail in v1.
func isInboxAgent(name string) bool {
	name = strings.TrimSpace(name)
	for _, r := range personalhq.V1Roster {
		if r.Slug == "inbox" {
			return strings.EqualFold(name, r.AgentName)
		}
	}
	return strings.EqualFold(name, "Inbox")
}

// emailBindingFor returns the workspace's first enabled native email binding
// that names an account and permits read/search, or false.
//
// Classification comes from the shared workspace classifier — the same one the
// runtime resolver uses to EXCLUDE these bindings from MCP materialization — so
// the two can never disagree about which bindings are native mail (FR 26, 31).
func emailBindingFor(ws *workspace.Workspace) (workspace.MCPBinding, bool) {
	for _, b := range ws.MCPBindings {
		if !b.Enabled || !b.IsNativeEmail() {
			continue
		}
		if stringFromConfig(b.Config, "account_id") == "" {
			continue
		}
		if bindingAllowsRead(b) {
			return b, true
		}
	}
	return workspace.MCPBinding{}, false
}

// bindingAllowsRead reports whether the binding's allowed_actions include a read
// or search action (defaulting to allowed when unset, matching the email handler
// default of {"read","search"}).
func bindingAllowsRead(b workspace.MCPBinding) bool {
	raw, ok := b.Config["allowed_actions"]
	if !ok {
		return true
	}
	list, ok := raw.([]any)
	if !ok {
		return true
	}
	for _, v := range list {
		s, _ := v.(string)
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "read", "search":
			return true
		}
	}
	return false
}

// agentAllowedForBinding applies the AgentMCPAccess layer: an agent with no
// access entry is allowed all enabled bindings (default-allow); an agent with an
// entry is allowed only the listed binding IDs.
func agentAllowedForBinding(ws *workspace.Workspace, agentName, bindingID string) bool {
	inst, ok := agentInstanceByName(ws, agentName)
	if !ok {
		return false
	}
	entry, ok := ws.GetAgentMCPAccess(inst.ID)
	if !ok {
		return true // no restriction entry → default-allow
	}
	for _, id := range entry.EnabledBindingIDs {
		if strings.EqualFold(strings.TrimSpace(id), strings.TrimSpace(bindingID)) {
			return true
		}
	}
	return false
}

func agentInstanceByName(ws *workspace.Workspace, name string) (workspace.AgentInstance, bool) {
	name = strings.TrimSpace(name)
	for _, inst := range ws.AgentInstances {
		if strings.EqualFold(strings.TrimSpace(inst.Name), name) {
			return inst, true
		}
	}
	return workspace.AgentInstance{}, false
}

func stringFromConfig(cfg map[string]any, key string) string {
	if cfg == nil {
		return ""
	}
	s, _ := cfg[key].(string)
	return strings.TrimSpace(s)
}
