package server

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/connections"
	"github.com/johnjallday/ori-agent/internal/connectionshttp"
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
			return "", classifyVaultWriteError(err)
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
		return "", classifyVaultWriteError(err)
	}
	return account.ID, nil
}

// classifyVaultWriteError translates a vault write failure into the connection
// domain's sentinels, so a vault that locked (or vanished) between preflight and
// the callback still reaches the user as unlock-and-retry or choose-a-vault
// rather than a generic "we couldn't save it" (FR 13-15).
func classifyVaultWriteError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, vault.ErrVaultLocked),
		errors.Is(err, vault.ErrVaultKeyUnavailable),
		errors.Is(err, vault.ErrVaultPasswordRequired):
		return fmt.Errorf("%w: %w", connections.ErrVaultLockedWrite, err)
	case errors.Is(err, vault.ErrVaultNotFound),
		errors.Is(err, vault.ErrVaultFileMissing),
		errors.Is(err, vault.ErrVaultRequired):
		return fmt.Errorf("%w: %w", connections.ErrVaultMissingWrite, err)
	default:
		return err
	}
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

// ListLegacyGmailAccounts returns Gmail vault accounts NOT sourced from the
// unified connection — accounts set up the old per-workspace way — as migration
// candidates (FR 88). Content-free: id, email, workspace only.
func (s *gmailCredentialSink) ListLegacyGmailAccounts(ctx context.Context) ([]connectionshttp.LegacyAccount, error) {
	accts, err := s.store.ListEmailAccounts(ctx, "", "")
	if err != nil {
		return nil, err
	}
	out := make([]connectionshttp.LegacyAccount, 0)
	for _, a := range accts {
		if a.Provider == vault.EmailProviderGmail && a.Source != googleConnectionEmailSource {
			out = append(out, connectionshttp.LegacyAccount{ID: a.ID, Email: a.EmailAddress, WorkspaceID: a.WorkspaceID})
		}
	}
	return out, nil
}

// MigrateAccount folds a legacy Gmail account into the unified connection
// (FR 88/89): it verifies the legacy account is the SAME Google account (email
// match, else ErrAccountMismatch), re-links its workspace to the connection with
// no re-auth (reusing the connection's Gmail credential), and removes the legacy
// record. A workspace-less legacy account is simply removed — the connection's
// global Gmail already covers it.
func (s *gmailCredentialSink) MigrateAccount(ctx context.Context, accountID, connectedEmail, credentialRef, vaultID string) error {
	acct, err := s.store.GetEmailAccount(ctx, accountID)
	if err != nil {
		return err
	}
	if acct == nil {
		return errors.New("connections: legacy account not found")
	}
	if !strings.EqualFold(strings.TrimSpace(acct.EmailAddress), strings.TrimSpace(connectedEmail)) {
		return connections.ErrAccountMismatch
	}
	if strings.TrimSpace(acct.WorkspaceID) != "" {
		if _, err := s.LinkGmailToWorkspace(ctx, credentialRef, vaultID, acct.WorkspaceID); err != nil {
			return err
		}
	}
	return s.store.DeleteEmailAccount(ctx, accountID)
}
