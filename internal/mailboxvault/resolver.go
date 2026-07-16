// Package mailboxvault bridges the provider-neutral mailbox runtime
// (internal/mailbox) to Vault-stored credentials. It implements
// mailbox.CredentialResolver so internal/mailbox never imports internal/vault —
// the coupling lives here, at the wiring seam.
package mailboxvault

import (
	"context"
	"strings"
	"time"

	"golang.org/x/oauth2"
	googleoauth "golang.org/x/oauth2/google"

	"github.com/johnjallday/ori-agent/internal/mailbox"
	"github.com/johnjallday/ori-agent/internal/vault"
)

// CredentialStore is the narrow reveal contract the resolver needs. *vault.Store
// satisfies it.
type CredentialStore interface {
	RevealEmailOAuthCredentials(ctx context.Context, id string, access vault.AccessContext) (*vault.EmailOAuthCredentials, error)
}

// Resolver resolves a mailbox account ID to a refreshing OAuth2 token source
// backed by Vault. Credentials are read on demand and never cached in memory
// beyond the returned token source, and never returned to callers.
type Resolver struct {
	store CredentialStore
}

// NewResolver constructs a Vault-backed credential resolver.
func NewResolver(store CredentialStore) *Resolver { return &Resolver{store: store} }

var _ mailbox.CredentialResolver = (*Resolver)(nil)

// TokenSource returns a refreshing token source for accountID, mapping missing
// or non-OAuth accounts to the typed mailbox errors the runtime expects. It
// never returns raw tokens.
func (r *Resolver) TokenSource(ctx context.Context, accountID string) (oauth2.TokenSource, error) {
	if r == nil || r.store == nil {
		return nil, mailbox.ErrDisconnected
	}
	creds, err := r.store.RevealEmailOAuthCredentials(ctx, accountID, vault.AccessContext{})
	if err != nil {
		// A missing/inaccessible account reads as disconnected rather than
		// leaking the vault error class.
		return nil, mailbox.ErrDisconnected
	}
	if creds == nil || creds.AuthType != vault.EmailAuthTypeOAuth2 {
		return nil, mailbox.ErrDisconnected
	}
	if strings.TrimSpace(creds.AccessToken) == "" && strings.TrimSpace(creds.RefreshToken) == "" {
		return nil, mailbox.ErrDisconnected
	}

	tokenURL := strings.TrimSpace(creds.TokenEndpoint)
	if tokenURL == "" {
		// Default to Google's token endpoint for Gmail accounts that did not
		// persist one.
		tokenURL = googleoauth.Endpoint.TokenURL
	}
	cfg := &oauth2.Config{
		ClientID:     creds.ClientID,
		ClientSecret: creds.ClientSecret,
		Endpoint:     oauth2.Endpoint{TokenURL: tokenURL},
	}

	tok := &oauth2.Token{
		AccessToken:  creds.AccessToken,
		RefreshToken: creds.RefreshToken,
	}
	// Expiry is not persisted with the account. When a refresh token is
	// available, force the source to refresh on first use by backdating the
	// expiry, so a stale access token is transparently renewed rather than
	// failing. (The renewed token is not written back to Vault in v1 — Google
	// refresh tokens are long-lived, so re-refreshing per source is safe if
	// wasteful; persistence is a future optimization.)
	if strings.TrimSpace(creds.RefreshToken) != "" {
		tok.Expiry = time.Now().Add(-time.Minute)
	}
	return cfg.TokenSource(ctx, tok), nil
}
