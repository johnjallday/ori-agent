package server

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/connections"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// connectionProductTeardown removes a Google product's local footprint on
// disconnect/unlink (FR 78, 79, 80). Like the impact enumerator it holds the
// builder and reads b.workspaceStore / b.vaultStore / b.mcp* lazily at request
// time (they are wired in a later phase than the connections handler).
//
// Calendar/Drive teardown revokes the MCP server's OAuth and stops it but keeps
// the server definition + workspace bindings, so the product is reconnectable
// and workspaces show "Connection required" (FR 80). Gmail teardown deletes the
// vault email account.
type connectionProductTeardown struct{ b *ServerBuilder }

func (t connectionProductTeardown) DisconnectProduct(ctx context.Context, product connections.ProductKey, credentialRef string) error {
	switch product {
	case connections.ProductCalendar, connections.ProductDrive:
		return t.disconnectMCPServer(ctx, credentialRef)
	case connections.ProductGmail:
		if t.b.vaultStore == nil || credentialRef == "" {
			return nil
		}
		return t.b.vaultStore.DeleteEmailAccount(ctx, credentialRef)
	}
	return nil
}

func (t connectionProductTeardown) disconnectMCPServer(ctx context.Context, serverName string) error {
	if serverName == "" || t.b.mcpConfigManager == nil {
		return nil
	}
	cfg, err := t.b.mcpConfigManager.GetServer(serverName)
	if err != nil {
		return nil // server already gone — nothing to tear down
	}
	if t.b.mcpRegistry != nil {
		if stopErr := t.b.mcpRegistry.StopServer(serverName); stopErr != nil {
			logger.Warn("connection teardown: stop server failed", logger.Fields{"server": serverName, "error": stopErr})
		}
	}
	return mcp.DisconnectOAuth(ctx, *cfg)
}

func (t connectionProductTeardown) UnlinkProductFromWorkspace(ctx context.Context, product connections.ProductKey, credentialRef, workspaceID string) error {
	switch product {
	case connections.ProductCalendar, connections.ProductDrive:
		return t.unlinkMCPBinding(credentialRef, workspaceID)
	case connections.ProductGmail:
		return t.unlinkGmailAccount(ctx, workspaceID)
	}
	return nil
}

func (t connectionProductTeardown) unlinkMCPBinding(serverName, workspaceID string) error {
	if serverName == "" || t.b.workspaceStore == nil {
		return nil
	}
	ws, err := t.b.workspaceStore.Get(workspaceID)
	if err != nil || ws == nil {
		return err
	}
	removed := false
	for _, b := range ws.GetMCPBindings() {
		if b.ServerName == serverName {
			if delErr := ws.DeleteMCPBinding(b.ID); delErr == nil {
				removed = true
			}
		}
	}
	if !removed {
		return nil
	}
	return t.b.workspaceStore.Save(ws)
}

// unlinkGmailAccount removes one workspace's use of Gmail (FR 73, 74).
//
// Two rules keep this from disconnecting anything else. First, it removes the
// workspace's own native email binding — the reference — BEFORE considering any
// deletion, so nothing is ever left pointing at a removed credential. Second, a
// credential is deleted only when it is a workspace-only legacy copy AND a full
// reverse scan proves nothing else references it. The connection's
// authoritative credential is never deleted by a workspace unlink: other
// workspaces, and the account itself, still need it.
func (t connectionProductTeardown) unlinkGmailAccount(ctx context.Context, workspaceID string) error {
	if t.b.vaultStore == nil {
		return nil
	}

	// Step 1: drop the binding and remember what it referenced.
	referenced := ""
	if t.b.workspaceStore != nil {
		if err := workspace.CanonicalUpdate(t.b.workspaceStore, workspaceID, func(ws *workspace.Workspace) error {
			for _, binding := range ws.GetMCPBindings() {
				if !binding.IsNativeEmail() {
					continue
				}
				if accountID := stringFromConfig(binding.Config, "account_id"); accountID != "" {
					referenced = accountID
				}
				if err := ws.DeleteMCPBinding(binding.ID); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}

	// Step 2: delete the credential only if it was this workspace's own copy and
	// nothing else points at it.
	lifecycle := t.b.credentialLifecycle()
	if lifecycle == nil || referenced == "" {
		return nil
	}
	grantRef := t.b.activeGmailCredentialRef()
	if _, err := lifecycle.deleteWorkspaceCredentialIfUnreferenced(ctx, referenced, grantRef); err != nil {
		return err
	}
	return nil
}

// credentialLifecycle builds the reference-scanning consolidator on demand. It
// reads b.vaultStore / b.workspaceStore lazily because, like the rest of this
// adapter, it is constructed before those phases run.
func (b *ServerBuilder) credentialLifecycle() *credentialLifecycle {
	if b == nil || b.vaultStore == nil || b.workspaceStore == nil {
		return nil
	}
	return newCredentialLifecycle(b.vaultStore, b.workspaceStore, b.mailboxInvalidator)
}

// activeGmailCredentialRef returns the connection's authoritative Gmail
// credential reference, or empty when Gmail is not enabled. Every deletion
// decision consults it so the authoritative record is never a candidate.
func (b *ServerBuilder) activeGmailCredentialRef() string {
	if b == nil || b.connStore == nil {
		return ""
	}
	conn, err := b.connStore.Load()
	if err != nil || conn == nil {
		return ""
	}
	grant, ok := conn.Grant(connections.ProductGmail)
	if !ok || grant == nil {
		return ""
	}
	return grant.CredentialRef
}
