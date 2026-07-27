package server

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/connections"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/mcp"
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

func (t connectionProductTeardown) unlinkGmailAccount(ctx context.Context, workspaceID string) error {
	if t.b.vaultStore == nil {
		return nil
	}
	accounts, err := t.b.vaultStore.ListEmailAccounts(ctx, "", workspaceID)
	if err != nil {
		return err
	}
	for _, a := range accounts {
		if a.Source == googleConnectionEmailSource {
			if delErr := t.b.vaultStore.DeleteEmailAccount(ctx, a.ID); delErr != nil {
				return delErr
			}
		}
	}
	return nil
}
