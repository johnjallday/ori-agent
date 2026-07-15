package mailboxvault

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/mailbox"
	"github.com/johnjallday/ori-agent/internal/vault"
)

type fakeStore struct {
	creds *vault.EmailOAuthCredentials
	err   error
}

func (f fakeStore) RevealEmailOAuthCredentials(ctx context.Context, id string, access vault.AccessContext) (*vault.EmailOAuthCredentials, error) {
	return f.creds, f.err
}

func TestResolverReturnsRefreshingSourceForOAuthAccount(t *testing.T) {
	r := NewResolver(fakeStore{creds: &vault.EmailOAuthCredentials{
		AuthType:     vault.EmailAuthTypeOAuth2,
		AccessToken:  "at",
		RefreshToken: "rt",
		ClientID:     "cid",
		ClientSecret: "secret",
	}})
	ts, err := r.TokenSource(context.Background(), "acct-1")
	if err != nil {
		t.Fatalf("TokenSource: %v", err)
	}
	if ts == nil {
		t.Fatal("expected a non-nil token source")
	}
}

func TestResolverMissingAccountIsDisconnected(t *testing.T) {
	r := NewResolver(fakeStore{err: errors.New("not found")})
	if _, err := r.TokenSource(context.Background(), "missing"); !errors.Is(err, mailbox.ErrDisconnected) {
		t.Fatalf("expected ErrDisconnected, got %v", err)
	}
}

func TestResolverNonOAuthAccountIsDisconnected(t *testing.T) {
	r := NewResolver(fakeStore{creds: &vault.EmailOAuthCredentials{
		AuthType:    vault.EmailAuthTypePassword,
		AccessToken: "x",
	}})
	if _, err := r.TokenSource(context.Background(), "imap-1"); !errors.Is(err, mailbox.ErrDisconnected) {
		t.Fatalf("password account must not resolve to a token source, got %v", err)
	}
}

func TestResolverNoTokensIsDisconnected(t *testing.T) {
	r := NewResolver(fakeStore{creds: &vault.EmailOAuthCredentials{AuthType: vault.EmailAuthTypeOAuth2}})
	if _, err := r.TokenSource(context.Background(), "empty"); !errors.Is(err, mailbox.ErrDisconnected) {
		t.Fatalf("an account with no tokens must be disconnected, got %v", err)
	}
}
