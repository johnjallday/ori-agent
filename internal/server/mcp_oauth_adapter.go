package server

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/vault"
)

// vaultMCPCredentialStore adapts vault.Store to mcp.RemoteCredentialStore so
// the mcp package stays storage-agnostic. Wired in once the vault store
// exists (see initializeHandlers), later than the MCP registry itself.
type vaultMCPCredentialStore struct {
	store   *vault.Store
	vaultID string
}

func newVaultMCPCredentialStore(store *vault.Store) *vaultMCPCredentialStore {
	return &vaultMCPCredentialStore{store: store, vaultID: vault.DefaultVaultID}
}

func (a *vaultMCPCredentialStore) LoadCredential(ctx context.Context, authRef string) (mcp.RemoteCredential, bool, error) {
	cred, ok, err := a.store.RevealMCPOAuthCredential(ctx, a.vaultID, authRef)
	if err != nil || !ok {
		return mcp.RemoteCredential{}, ok, err
	}
	return mcp.RemoteCredential{
		AuthRef:       cred.AuthRef,
		ServerName:    cred.ServerName,
		ClientID:      cred.ClientID,
		ClientSecret:  cred.ClientSecret,
		AccessToken:   cred.AccessToken,
		RefreshToken:  cred.RefreshToken,
		TokenType:     cred.TokenType,
		TokenEndpoint: cred.TokenEndpoint,
		Expiry:        cred.Expiry,
		Scopes:        cred.Scopes,
	}, true, nil
}

func (a *vaultMCPCredentialStore) SaveCredential(ctx context.Context, cred mcp.RemoteCredential) error {
	_, err := a.store.UpsertMCPOAuthCredential(ctx, a.vaultID, vault.MCPOAuthCredential{
		AuthRef:       cred.AuthRef,
		ServerName:    cred.ServerName,
		ClientID:      cred.ClientID,
		ClientSecret:  cred.ClientSecret,
		AccessToken:   cred.AccessToken,
		RefreshToken:  cred.RefreshToken,
		TokenType:     cred.TokenType,
		TokenEndpoint: cred.TokenEndpoint,
		Expiry:        cred.Expiry,
		Scopes:        cred.Scopes,
	})
	return err
}

func (a *vaultMCPCredentialStore) DeleteCredential(ctx context.Context, authRef string) error {
	return a.store.DeleteMCPOAuthCredential(ctx, a.vaultID, authRef)
}

// mcpOAuthUserID resolves the initiating user for a remote MCP OAuth flow.
// Ori is currently single-user local, so this always resolves to the local
// user id; kept as its own function (rather than inlining userprofile.LocalUserID)
// so a future multi-user server can swap in a real resolver without touching
// the mcp package.
func mcpOAuthUserID(context.Context) (string, error) {
	return userprofile.LocalUserID, nil
}
