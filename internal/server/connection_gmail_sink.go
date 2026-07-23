package server

import (
	"context"

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
