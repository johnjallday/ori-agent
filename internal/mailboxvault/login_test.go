package mailboxvault

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/mailbox"
	"github.com/johnjallday/ori-agent/internal/vault"
)

type fakeLoginStore struct {
	login *vault.EmailLoginCredentials
	err   error
}

func (f fakeLoginStore) RevealEmailLoginCredentials(context.Context, string, vault.AccessContext) (*vault.EmailLoginCredentials, error) {
	return f.login, f.err
}

func TestLoginResolverTransportFollowsHowTheAccountWasConnected(t *testing.T) {
	cases := []struct {
		name  string
		login *vault.EmailLoginCredentials
		want  mailbox.Transport
	}{
		{"gmail oauth", &vault.EmailLoginCredentials{Provider: vault.EmailProviderGmail, AuthType: vault.EmailAuthTypeOAuth2}, mailbox.TransportGmailAPI},
		{"gmail app password", &vault.EmailLoginCredentials{Provider: vault.EmailProviderGmail, AuthType: vault.EmailAuthTypeAppPassword}, mailbox.TransportIMAP},
		{"custom imap password", &vault.EmailLoginCredentials{Provider: vault.EmailProviderIMAPSMTP, AuthType: vault.EmailAuthTypePassword}, mailbox.TransportIMAP},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NewLoginResolver(fakeLoginStore{login: tc.login}).Transport(context.Background(), "a")
			if err != nil || got != tc.want {
				t.Fatalf("Transport = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestLoginResolverUnsupportedOrMissingIsDisconnected(t *testing.T) {
	for name, store := range map[string]LoginStore{
		"microsoft oauth has no reader": fakeLoginStore{login: &vault.EmailLoginCredentials{Provider: vault.EmailProviderMicrosoft, AuthType: vault.EmailAuthTypeOAuth2}},
		"missing account":               fakeLoginStore{err: errors.New("record not found: vault is locked")},
		"nil login":                     fakeLoginStore{},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := NewLoginResolver(store).Transport(context.Background(), "a")
			if !errors.Is(err, mailbox.ErrDisconnected) {
				t.Fatalf("err = %v, want ErrDisconnected", err)
			}
			if err != nil && err.Error() != mailbox.ErrDisconnected.Error() {
				t.Fatalf("the vault's own error leaked: %q", err)
			}
		})
	}
}

func TestLoginResolverIMAPCredentials(t *testing.T) {
	r := NewLoginResolver(fakeLoginStore{login: &vault.EmailLoginCredentials{
		AuthType: vault.EmailAuthTypeAppPassword, EmailAddress: "me@icloud.example",
		Password: "abcd-efgh", IMAPHost: " imap.mail.example ", IMAPPort: 993,
	}})
	creds, err := r.IMAPCredentials(context.Background(), "a")
	if err != nil {
		t.Fatalf("IMAPCredentials: %v", err)
	}
	if creds.Host != "imap.mail.example" || creds.Port != 993 || creds.Username != "me@icloud.example" || creds.Password != "abcd-efgh" {
		t.Fatalf("creds = %+v, want the address as the username when none is saved", creds)
	}
}

func TestLoginResolverIMAPCredentialsRefusesIncompleteLogins(t *testing.T) {
	for name, login := range map[string]*vault.EmailLoginCredentials{
		"oauth account":  {AuthType: vault.EmailAuthTypeOAuth2, IMAPHost: "imap.gmail.com", EmailAddress: "a@b", Password: "x"},
		"no password":    {AuthType: vault.EmailAuthTypeAppPassword, IMAPHost: "imap.example", EmailAddress: "a@b"},
		"no imap server": {AuthType: vault.EmailAuthTypePassword, EmailAddress: "a@b", Password: "x"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewLoginResolver(fakeLoginStore{login: login}).IMAPCredentials(context.Background(), "a"); !errors.Is(err, mailbox.ErrDisconnected) {
				t.Fatalf("err = %v, want ErrDisconnected", err)
			}
		})
	}
}
