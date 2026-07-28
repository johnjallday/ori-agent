package server

import (
	"context"
	"errors"

	"github.com/johnjallday/ori-agent/internal/connections"
	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/vault"
)

// connectionGrantHealth maps a Google product grant's underlying runtime state to
// a grant health without any browser interaction (FR 85). Calendar/Drive read
// their MCP server's status from the registry; Gmail reads whether its vault
// credential still exists. Transient states keep the stored health (ok=false).
// Like the other connection adapters it reads the builder's stores lazily.
type connectionGrantHealth struct{ b *ServerBuilder }

func (c connectionGrantHealth) LiveHealth(product connections.ProductKey, credentialRef string) (connections.GrantHealth, bool) {
	switch product {
	case connections.ProductCalendar, connections.ProductDrive:
		if credentialRef == "" || c.b.mcpRegistry == nil {
			return "", false
		}
		status, err := c.b.mcpRegistry.GetServerStatus(credentialRef)
		if err != nil {
			// The grant references a server that no longer exists — its credential
			// is gone, so a reconnect is required (FR 85).
			return connections.HealthReconnectRequired, true
		}
		switch status {
		case mcp.StatusRunning:
			return connections.HealthHealthy, true
		case mcp.StatusAuthRequired, mcp.StatusError, mcp.StatusStopped:
			return connections.HealthReconnectRequired, true
		default:
			// starting / restarting — transient; keep the stored health.
			return "", false
		}
	case connections.ProductGmail:
		return c.gmailHealth(credentialRef)
	}
	return "", false
}

// gmailHealth reports reconnect-required when the grant references a vault
// credential that no longer exists.
//
// Without this, a grant whose credential was deleted — by a vault being
// recreated, a data directory moving, or a partial teardown — keeps reporting
// "healthy" forever. The card then offers a Connect action that cannot possibly
// work, and the user gets an opaque 500. Calendar and Drive already reconcile
// exactly this way against their MCP server; Gmail simply never did.
//
// A LOCKED vault is deliberately NOT reconnect-required: the credential is
// intact and only temporarily unreadable, so claiming it needs reauthorization
// would send the user to redo work that is not broken. Only a definitively
// absent record downgrades the health.
func (c connectionGrantHealth) gmailHealth(credentialRef string) (connections.GrantHealth, bool) {
	if credentialRef == "" || c.b == nil || c.b.vaultStore == nil {
		return "", false
	}
	account, err := c.b.vaultStore.GetEmailAccount(context.Background(), credentialRef)
	if err != nil {
		if errors.Is(err, vault.ErrRecordNotFound) {
			return connections.HealthReconnectRequired, true
		}
		// Locked, unavailable, or any other read failure: not evidence the
		// credential is gone. Keep the stored health.
		return "", false
	}
	if account == nil {
		return connections.HealthReconnectRequired, true
	}
	if !account.CredentialsStatus.HasAccessToken && !account.CredentialsStatus.HasRefreshToken {
		// The row survives but its secrets are unreadable. The vault rejects
		// tokenless OAuth accounts on write, so this cannot be reached by normal
		// use — it guards a record whose secret-store entry was lost separately,
		// and matches the check the mailbox gate and readiness evaluator make.
		return connections.HealthReconnectRequired, true
	}
	return "", false
}
