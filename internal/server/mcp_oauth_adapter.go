package server

import (
	"context"
	"errors"
	"fmt"

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
	// vault.DefaultVaultID ("default") is only a display-name/legacy-id
	// comparison sentinel (see vault/store.go's IsDefault assignments) --
	// real vaults created through the API get a generated UUID, so it is
	// never an actual, resolvable vault id. The correct way to reference
	// "the user's one vault" is Store.resolveVaultID's own empty-string
	// convention, which auto-resolves when exactly one vault exists (and
	// errors ErrVaultRequired otherwise, same as every other vault-backed
	// feature in this app).
	return &vaultMCPCredentialStore{store: store, vaultID: ""}
}

func (a *vaultMCPCredentialStore) LoadCredential(ctx context.Context, authRef string) (mcp.RemoteCredential, bool, error) {
	cred, ok, err := a.store.RevealMCPOAuthCredential(ctx, a.vaultID, authRef)
	if err != nil {
		// No vault has been created yet (or the selection is ambiguous) is
		// not a failure to report -- it is simply the state of having no
		// stored credential, since a credential can only live in a vault.
		// Translating it here keeps every caller from having to special-case
		// a vault error: a remote server with no credential reports
		// "needs connecting" rather than "something went wrong", which is
		// both truthful and actionable on a fresh install.
		if errors.Is(err, vault.ErrVaultRequired) {
			return mcp.RemoteCredential{}, false, nil
		}
		// A locked vault is the opposite case: the credential is there,
		// it just cannot be read until the user unlocks it. Map it onto the
		// storage-agnostic sentinel so callers can tell "unlock this" apart
		// from "connect something" without importing the vault package.
		if errors.Is(err, vault.ErrVaultLocked) {
			return mcp.RemoteCredential{}, false, fmt.Errorf("%w: %w", mcp.ErrCredentialStoreLocked, err)
		}
		return mcp.RemoteCredential{}, false, err
	}
	if !ok {
		return mcp.RemoteCredential{}, false, nil
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
