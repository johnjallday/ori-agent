package mailboxvault

import (
	"context"
	"strings"

	"github.com/johnjallday/ori-agent/internal/mailbox"
	"github.com/johnjallday/ori-agent/internal/vault"
)

// LoginStore is the narrow reveal contract for password-connected mailboxes.
// *vault.Store satisfies it.
type LoginStore interface {
	RevealEmailLoginCredentials(ctx context.Context, id string, access vault.AccessContext) (*vault.EmailLoginCredentials, error)
}

// LoginResolver resolves password-connected accounts for the IMAP reader, and
// tells the mailbox router which reader serves each account. Like Resolver it
// reads the vault on demand and never returns a secret to anyone but the
// reader that uses it.
type LoginResolver struct {
	store LoginStore
}

// NewLoginResolver constructs a vault-backed login resolver.
func NewLoginResolver(store LoginStore) *LoginResolver { return &LoginResolver{store: store} }

var (
	_ mailbox.IMAPCredentialResolver = (*LoginResolver)(nil)
	_ mailbox.TransportResolver      = (*LoginResolver)(nil)
)

// Transport follows how the account was connected. OAuth is read through the
// Gmail API, which is the only OAuth reader Ori has: a Microsoft OAuth account
// has no reader and reads as disconnected rather than being sent to Gmail. A
// password or app password is read over IMAP whoever hosts the mailbox.
func (r *LoginResolver) Transport(ctx context.Context, accountID string) (mailbox.Transport, error) {
	login, err := r.reveal(ctx, accountID)
	if err != nil {
		return "", err
	}
	switch login.AuthType {
	case vault.EmailAuthTypeOAuth2:
		if login.Provider == vault.EmailProviderGmail {
			return mailbox.TransportGmailAPI, nil
		}
	case vault.EmailAuthTypePassword, vault.EmailAuthTypeAppPassword:
		return mailbox.TransportIMAP, nil
	}
	return "", mailbox.ErrDisconnected
}

// IMAPCredentials returns the account's IMAP login. An account with no saved
// password, or one connected with OAuth, has no IMAP login.
func (r *LoginResolver) IMAPCredentials(ctx context.Context, accountID string) (mailbox.IMAPCredentials, error) {
	login, err := r.reveal(ctx, accountID)
	if err != nil {
		return mailbox.IMAPCredentials{}, err
	}
	if login.AuthType != vault.EmailAuthTypePassword && login.AuthType != vault.EmailAuthTypeAppPassword {
		return mailbox.IMAPCredentials{}, mailbox.ErrDisconnected
	}
	username := strings.TrimSpace(login.Username)
	if username == "" {
		username = strings.TrimSpace(login.EmailAddress)
	}
	creds := mailbox.IMAPCredentials{
		Host:     strings.TrimSpace(login.IMAPHost),
		Port:     login.IMAPPort,
		Username: username,
		Password: login.Password,
	}
	if creds.Host == "" || creds.Username == "" || creds.Password == "" {
		return mailbox.IMAPCredentials{}, mailbox.ErrDisconnected
	}
	return creds, nil
}

// reveal reads the account's login. A missing or unreadable account (a locked
// vault included) reads as disconnected, never as the vault's own error.
func (r *LoginResolver) reveal(ctx context.Context, accountID string) (*vault.EmailLoginCredentials, error) {
	if r == nil || r.store == nil {
		return nil, mailbox.ErrDisconnected
	}
	login, err := r.store.RevealEmailLoginCredentials(ctx, accountID, vault.AccessContext{})
	if err != nil || login == nil {
		return nil, mailbox.ErrDisconnected
	}
	return login, nil
}
