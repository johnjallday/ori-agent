package emailsetup

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/mailbox"
	"github.com/johnjallday/ori-agent/internal/vault"
)

const testPassword = "abcd-efgh-ijkl-mnop"

// fakeLogins accepts one username and password, and records every attempt.
type fakeLogins struct {
	acceptUser string
	acceptPass string
	err        error // returned for every attempt when set
	attempts   []mailbox.IMAPCredentials
}

func (f *fakeLogins) CheckLogin(_ context.Context, creds mailbox.IMAPCredentials) error {
	f.attempts = append(f.attempts, creds)
	if f.err != nil {
		return f.err
	}
	if creds.Username == f.acceptUser && creds.Password == f.acceptPass {
		return nil
	}
	return mailbox.ErrExpired
}

type fakeKeyring struct {
	err   error
	calls int
}

func (f *fakeKeyring) EnsureVault(context.Context, string) (string, error) {
	f.calls++
	if f.err != nil {
		return "", f.err
	}
	return "v-email", nil
}

type fakeAccountStore struct {
	accounts []vault.EmailAccount
	created  []vault.EmailAccountInput
	updated  map[string]vault.EmailAccountUpdate
}

func (f *fakeAccountStore) ListEmailAccounts(context.Context, string, string) ([]vault.EmailAccount, error) {
	return f.accounts, nil
}

func (f *fakeAccountStore) CreateEmailAccount(_ context.Context, input vault.EmailAccountInput) (*vault.EmailAccount, error) {
	f.created = append(f.created, input)
	acc := vault.EmailAccount{ID: "acct-new", VaultID: input.VaultID, EmailAddress: input.EmailAddress, Source: input.Source}
	f.accounts = append(f.accounts, acc)
	return &acc, nil
}

func (f *fakeAccountStore) UpdateEmailAccount(_ context.Context, id string, update vault.EmailAccountUpdate) (*vault.EmailAccount, error) {
	if f.updated == nil {
		f.updated = map[string]vault.EmailAccountUpdate{}
	}
	f.updated[id] = update
	for _, acc := range f.accounts {
		if acc.ID == id {
			return &acc, nil
		}
	}
	return nil, vault.ErrRecordNotFound
}

type fakeHome struct {
	ensured int
	linked  []string // "workspace/account"
	err     error
}

func (f *fakeHome) EnsureEmailOps(context.Context, string) (Workspace, error) {
	f.ensured++
	if f.err != nil {
		return Workspace{}, f.err
	}
	return Workspace{ID: "ws-email", Name: "Email Ops", Route: "/workspaces/email-ops", Created: true}, nil
}

func (f *fakeHome) LinkMailbox(_ context.Context, _, workspaceID, accountID string) error {
	f.linked = append(f.linked, workspaceID+"/"+accountID)
	return nil
}

type serviceFixture struct {
	logins   *fakeLogins
	keyring  *fakeKeyring
	accounts *fakeAccountStore
	home     *fakeHome
	service  *Service
}

func newServiceFixture(acceptUser, acceptPass string) *serviceFixture {
	f := &serviceFixture{
		logins:   &fakeLogins{acceptUser: acceptUser, acceptPass: acceptPass},
		keyring:  &fakeKeyring{},
		accounts: &fakeAccountStore{},
		home:     &fakeHome{},
	}
	lookup := func(_ context.Context, domain string) ([]*net.MX, error) {
		return nil, &net.DNSError{Err: "no such host", Name: domain, IsNotFound: true}
	}
	f.service = NewService(lookup, f.logins, f.keyring, f.accounts, f.home)
	return f
}

// nothingSaved asserts a failed connect left no trace.
func (f *serviceFixture) nothingSaved(t *testing.T) {
	t.Helper()
	if f.keyring.calls != 0 || len(f.accounts.created) != 0 || len(f.accounts.updated) != 0 || f.home.ensured != 0 || len(f.home.linked) != 0 {
		t.Fatalf("a failed connect saved something: keyring=%d created=%d updated=%d ensured=%d linked=%v",
			f.keyring.calls, len(f.accounts.created), len(f.accounts.updated), f.home.ensured, f.home.linked)
	}
}

func failureCode(err error) string {
	var failure *Failure
	if errors.As(err, &failure) {
		return failure.Code
	}
	return ""
}

func TestConnectGmailEndToEnd(t *testing.T) {
	f := newServiceFixture("me@gmail.com", "abcdefghijklmnop")
	got, err := f.service.Connect(context.Background(), "local", ConnectRequest{Address: "Me@Gmail.com", Password: "abcd efgh ijkl mnop"})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if got.Provider != ProviderGmail || got.AccountID != "acct-new" || got.Workspace.ID != "ws-email" {
		t.Fatalf("result = %+v", got)
	}
	if len(f.logins.attempts) != 1 || f.logins.attempts[0].Host != "imap.gmail.com" || f.logins.attempts[0].Port != 993 {
		t.Fatalf("login attempts = %+v", f.logins.attempts)
	}
	created := f.accounts.created[0]
	if created.VaultID != "v-email" || created.Source != AccountSource || created.AuthType != vault.EmailAuthTypeAppPassword ||
		created.Provider != vault.EmailProviderIMAPSMTP || created.Credentials.Password != "abcdefghijklmnop" {
		t.Fatalf("saved account = %+v", created)
	}
	if strings.Join(f.home.linked, ",") != "ws-email/acct-new" {
		t.Fatalf("linked = %v", f.home.linked)
	}
}

func TestConnectWrongPasswordSavesNothing(t *testing.T) {
	f := newServiceFixture("me@gmail.com", "right")
	_, err := f.service.Connect(context.Background(), "local", ConnectRequest{Address: "me@gmail.com", Password: "wrong-password-here"})
	if failureCode(err) != FailureWrongPassword {
		t.Fatalf("err = %v, want wrong_password", err)
	}
	if strings.Contains(err.Error(), "wrong-password-here") {
		t.Fatalf("the failure repeats the password: %q", err)
	}
	f.nothingSaved(t)
}

func TestConnectICloudTriesTheNameThenTheAddress(t *testing.T) {
	f := newServiceFixture("jane@icloud.com", testPassword)
	if _, err := f.service.Connect(context.Background(), "local", ConnectRequest{Address: "jane@icloud.com", Password: testPassword}); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if len(f.logins.attempts) != 2 || f.logins.attempts[0].Username != "jane" || f.logins.attempts[1].Username != "jane@icloud.com" {
		t.Fatalf("attempts = %+v", f.logins.attempts)
	}
	if got := f.accounts.created[0]; got.Username != "jane@icloud.com" || got.Credentials.Password != testPassword {
		t.Fatalf("saved username/password = %q/%q, want the accepted login with Apple's dashes kept", got.Username, got.Credentials.Password)
	}
}

func TestConnectUnreachableServerSavesNothing(t *testing.T) {
	f := newServiceFixture("", "")
	f.logins.err = mailbox.ErrTimeout
	_, err := f.service.Connect(context.Background(), "local", ConnectRequest{Address: "jane@icloud.com", Password: testPassword})
	if failureCode(err) != FailureUnreachable || len(f.logins.attempts) != 1 {
		t.Fatalf("err = %v after %d attempts, want unreachable after one", err, len(f.logins.attempts))
	}
	f.nothingSaved(t)
}

func TestConnectRefusesWhatCannotWork(t *testing.T) {
	cases := []struct {
		name string
		req  ConnectRequest
		code string
	}{
		{"not an address", ConnectRequest{Address: "jane", Password: "x"}, FailureInvalidAddress},
		{"microsoft", ConnectRequest{Address: "jane@hotmail.com", Password: "x"}, FailureUnsupported},
		{"no password", ConnectRequest{Address: "jane@gmail.com", Password: "   "}, FailureMissingPassword},
		{"unknown host needs a server", ConnectRequest{Address: "jane@own.example", Password: "x"}, FailureNeedsServer},
		{"server is not a hostname", ConnectRequest{Address: "jane@own.example", Password: "x", IMAPHost: "imap.own.example/../x"}, FailureNeedsServer},
		{"server with a port in it", ConnectRequest{Address: "jane@own.example", Password: "x", IMAPHost: "imap.own.example:993"}, FailureNeedsServer},
		{"bad port", ConnectRequest{Address: "jane@own.example", Password: "x", IMAPHost: "imap.own.example", IMAPPort: 70000}, FailureNeedsServer},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newServiceFixture("", "")
			_, err := f.service.Connect(context.Background(), "local", tc.req)
			if failureCode(err) != tc.code {
				t.Fatalf("err = %v, want %s", err, tc.code)
			}
			if len(f.logins.attempts) != 0 {
				t.Fatalf("a login was attempted for a request that could not work: %+v", f.logins.attempts)
			}
			f.nothingSaved(t)
		})
	}
}

func TestConnectCustomServerUsesItAndAPlainPassword(t *testing.T) {
	f := newServiceFixture("jane@own.example", "secret")
	_, err := f.service.Connect(context.Background(), "local", ConnectRequest{Address: "jane@own.example", Password: "secret", IMAPHost: "mail.own.example", IMAPPort: 143})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if got := f.logins.attempts[0]; got.Host != "mail.own.example" || got.Port != 143 {
		t.Fatalf("login target = %+v", got)
	}
	if got := f.accounts.created[0]; got.AuthType != vault.EmailAuthTypePassword || got.IMAPHost != "mail.own.example" {
		t.Fatalf("saved = %+v", got)
	}
}

func TestConnectAgainUpdatesTheSameAccount(t *testing.T) {
	f := newServiceFixture("me@gmail.com", "new-password")
	f.accounts.accounts = []vault.EmailAccount{
		{ID: "acct-other", EmailAddress: "me@gmail.com", Source: "google-connection"},
		{ID: "acct-mine", EmailAddress: "ME@gmail.com", Source: AccountSource},
	}
	got, err := f.service.Connect(context.Background(), "local", ConnectRequest{Address: "me@gmail.com", Password: "new-password"})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if got.AccountID != "acct-mine" || len(f.accounts.created) != 0 {
		t.Fatalf("result %+v, created %d; want the earlier setup's account updated", got, len(f.accounts.created))
	}
	update, ok := f.accounts.updated["acct-mine"]
	if !ok || update.Password == nil || *update.Password != "new-password" {
		t.Fatalf("update = %+v", update)
	}
	if _, touched := f.accounts.updated["acct-other"]; touched {
		t.Fatal("an account another flow made was overwritten")
	}
}

func TestConnectWithoutAKeychainSavesNothing(t *testing.T) {
	f := newServiceFixture("me@gmail.com", "pw")
	f.keyring.err = vault.ErrKeyringUnavailable
	_, err := f.service.Connect(context.Background(), "local", ConnectRequest{Address: "me@gmail.com", Password: "pw"})
	if failureCode(err) != FailureKeyringUnavailable {
		t.Fatalf("err = %v, want keyring_unavailable", err)
	}
	if len(f.accounts.created) != 0 || f.home.ensured != 0 {
		t.Fatal("the login was saved somewhere Ori could not read after a restart")
	}
}

func TestConnectNeedsTheICloudAddressForACustomDomain(t *testing.T) {
	f := newServiceFixture("jane.doe@icloud.com", testPassword)
	f.service.lookup = func(_ context.Context, domain string) ([]*net.MX, error) {
		return []*net.MX{{Host: "mx01.mail.icloud.com."}}, nil
	}
	_, err := f.service.Connect(context.Background(), "local", ConnectRequest{Address: "jane@family.example", Password: testPassword})
	if failureCode(err) != FailureNeedsUsername {
		t.Fatalf("err = %v, want needs_username", err)
	}
	if _, err := f.service.Connect(context.Background(), "local", ConnectRequest{Address: "jane@family.example", Password: testPassword, Username: "jane.doe@icloud.com"}); err != nil {
		t.Fatalf("Connect with the iCloud username: %v", err)
	}
	if got := f.accounts.created[0]; got.EmailAddress != "jane@family.example" || got.Username != "jane.doe@icloud.com" {
		t.Fatalf("saved = %+v", got)
	}
}
