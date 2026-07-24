package server

import (
	"context"
	"strings"

	"github.com/johnjallday/ori-agent/internal/connections"
	"github.com/johnjallday/ori-agent/internal/vault"
)

// gmailCredentialSink implements connections.CredentialSink by creating a vault
// EmailAccount for an enabled Gmail grant and returning its id as the opaque
// credential reference the connection stores (FR 39). The OAuth tokens live only
// in the vault behind that reference — the connection record never holds them.
// This is the seam that lets the native Gmail mailbox/broker reuse the same
// EmailAccount the connection grant points at, rather than a second token store.
type gmailCredentialSink struct {
	store *vault.Store
}

func newGmailCredentialSink(store *vault.Store) *gmailCredentialSink {
	return &gmailCredentialSink{store: store}
}

func (s *gmailCredentialSink) SaveGmailCredential(ctx context.Context, cred connections.GmailCredential) (string, error) {
	// Re-auth / scope upgrade: update the existing account in place so a scope
	// change never orphans the prior record. Only overwrite the refresh token
	// when Google returned a new one (it often omits it on re-auth).
	if ref := strings.TrimSpace(cred.ExistingRef); ref != "" {
		update := vault.EmailAccountUpdate{}
		if cred.AccessToken != "" {
			at := cred.AccessToken
			update.AccessToken = &at
		}
		if cred.RefreshToken != "" {
			rt := cred.RefreshToken
			update.RefreshToken = &rt
		}
		if _, err := s.store.UpdateEmailAccount(ctx, ref, update); err != nil {
			return "", err
		}
		return ref, nil
	}

	account, err := s.store.CreateEmailAccount(ctx, vault.EmailAccountInput{
		VaultID:      cred.VaultID,
		Label:        cred.Email,
		Source:       "google-connection",
		Provider:     vault.EmailProviderGmail,
		EmailAddress: cred.Email,
		DisplayName:  cred.DisplayName,
		AuthType:     vault.EmailAuthTypeOAuth2,
		Credentials: vault.EmailAccountCredentials{
			AccessToken:   cred.AccessToken,
			RefreshToken:  cred.RefreshToken,
			ClientID:      cred.ClientID,
			ClientSecret:  cred.ClientSecret,
			TokenEndpoint: cred.TokenEndpoint,
		},
	})
	if err != nil {
		return "", err
	}
	return account.ID, nil
}

// LinkGmailToWorkspace reuses the global Gmail grant's identity to give a
// workspace its own email account WITHOUT re-authorizing with Google (FR 47, 54):
// it reveals the grant's OAuth credential and creates a workspace-scoped
// EmailAccount carrying the same tokens. Multiple workspaces thus share one
// Google identity, each with its own account record the mailbox can resolve.
func (s *gmailCredentialSink) LinkGmailToWorkspace(ctx context.Context, credentialRef, vaultID, workspaceID string) (string, error) {
	creds, err := s.store.RevealEmailOAuthCredentials(ctx, credentialRef, vault.AccessContext{})
	if err != nil {
		return "", err
	}
	account, err := s.store.CreateEmailAccount(ctx, vault.EmailAccountInput{
		VaultID:      vaultID,
		WorkspaceID:  workspaceID,
		Label:        creds.EmailAddress,
		Source:       "google-connection",
		Provider:     vault.EmailProviderGmail,
		EmailAddress: creds.EmailAddress,
		AuthType:     vault.EmailAuthTypeOAuth2,
		Credentials: vault.EmailAccountCredentials{
			AccessToken:   creds.AccessToken,
			RefreshToken:  creds.RefreshToken,
			ClientID:      creds.ClientID,
			ClientSecret:  creds.ClientSecret,
			TokenEndpoint: creds.TokenEndpoint,
		},
	})
	if err != nil {
		return "", err
	}
	return account.ID, nil
}
