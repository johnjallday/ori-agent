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
	// lifecycle performs proven consolidation of legacy duplicate credentials.
	// Nil disables migration rather than falling back to an unproven delete.
	lifecycle *credentialLifecycle
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

// LinkGmailToWorkspace points a workspace at the connection's AUTHORITATIVE
// Gmail credential (FR 68-70). It returns that credential's reference; it
// creates nothing.
//
// This used to reveal the grant's tokens and copy them into a workspace-scoped
// EmailAccount, so every linked workspace held its own copy of the same refresh
// token. That made reauthorization ambiguous (which copy is current?), disconnect
// incomplete (copies survived), and every workspace an extra place a token could
// leak from. One account, many references, is both safer and simpler: a re-auth
// updates one record and every workspace sees it immediately.
//
// Linking is therefore idempotent by construction — the same workspace linking
// twice resolves to the same reference (FR 69).
func (s *gmailCredentialSink) LinkGmailToWorkspace(ctx context.Context, credentialRef, vaultID, workspaceID string) (string, error) {
	ref := strings.TrimSpace(credentialRef)
	if ref == "" {
		return "", errors.New("connections: the Google connection has no Gmail credential to link")
	}
	// Verify the credential actually resolves before a workspace binds to it, so
	// a broken reference surfaces here rather than at first mailbox read.
	account, err := s.store.GetEmailAccount(ctx, ref)
	if err != nil {
		return "", err
	}
	if account == nil {
		return "", errors.New("connections: the Gmail credential referenced by this connection no longer exists")
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
// (FR 88/89).
//
// It first rejects a different Google account outright (email mismatch). Then
// it hands the record to proven consolidation, which repoints every workspace
// binding onto the connection's authoritative credential, verifies the repoint,
// re-scans for references, and only then deletes.
//
// The previous implementation deleted the legacy record while leaving workspace
// bindings pointing at the deleted id — the exact orphan FR 77 forbids. It also
// deleted on an email match alone, which cannot distinguish two records for the
// same address held in different vaults.
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
	if s.lifecycle == nil {
		return errors.New("connections: credential consolidation is unavailable in this build")
	}
	migrated, err := s.lifecycle.consolidateDuplicate(ctx, credentialRef, accountID, credentialRef)
	if err != nil {
		return err
	}
	if !migrated {
		// Preserved on purpose — the reason is in the server log, token-free.
		return ErrMigrationNotProven
	}
	return nil
}

// ErrMigrationNotProven means a legacy record could not be proven to be a
// redundant copy, so it was preserved. This is a normal outcome, not a fault:
// the alternative is deleting a credential the user may still need.
var ErrMigrationNotProven = fmt.Errorf("%w: this account could not be proven to be a duplicate, so it was left in place", connectionshttp.ErrNotProven)
