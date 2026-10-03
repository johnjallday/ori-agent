package emailsetup

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/johnjallday/ori-agent/internal/mailbox"
	"github.com/johnjallday/ori-agent/internal/vault"
)

// AccountSource marks accounts created by this flow, so they are never mistaken
// for accounts the Google sign-in or the Vault page made.
const AccountSource = "email-setup"

// SetupURL opens the "Set up email" card. Every surface that offers email setup
// links here.
const SetupURL = "/?setup=email"

// vaultName is the display name of the vault Ori keeps for mail logins.
const vaultName = "Email"

// LoginChecker signs in to an IMAP server. *mailbox.IMAPProvider satisfies it.
type LoginChecker interface {
	CheckLogin(ctx context.Context, creds mailbox.IMAPCredentials) error
}

// VaultKeyring returns the vault Ori can open by itself. *vault.Keyring
// satisfies it.
type VaultKeyring interface {
	EnsureVault(ctx context.Context, name string) (string, error)
}

// AccountStore is where the login is kept. *vault.Store satisfies it.
type AccountStore interface {
	ListEmailAccounts(ctx context.Context, vaultID, workspaceID string) ([]vault.EmailAccount, error)
	CreateEmailAccount(ctx context.Context, input vault.EmailAccountInput) (*vault.EmailAccount, error)
	UpdateEmailAccount(ctx context.Context, id string, update vault.EmailAccountUpdate) (*vault.EmailAccount, error)
}

// MailboxHome is the workspace the mailbox belongs to: the user's Email Ops.
type MailboxHome interface {
	// EnsureEmailOps returns the user's Email Ops workspace, creating it from
	// its blueprint when the user has none.
	EnsureEmailOps(ctx context.Context, userID string) (Workspace, error)
	// LinkMailbox links accountID to the workspace, read and search only.
	LinkMailbox(ctx context.Context, userID, workspaceID, accountID string) error
}

// Workspace is the Email Ops workspace as the card shows it.
type Workspace struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Route   string `json:"route,omitempty"`
	Created bool   `json:"created"`
}

// ConnectRequest is what the card sends. Password is the only secret, and it
// is never echoed back.
type ConnectRequest struct {
	Address  string `json:"address"`
	Password string `json:"password"`
	// Username, IMAPHost and IMAPPort are only sent when the card asked for them.
	Username string `json:"username,omitempty"`
	IMAPHost string `json:"imap_host,omitempty"`
	IMAPPort int    `json:"imap_port,omitempty"`
}

// ConnectResult is the receipt: what was connected and where it now lives.
type ConnectResult struct {
	Address   string    `json:"address"`
	Provider  Provider  `json:"provider"`
	Label     string    `json:"label"`
	AccountID string    `json:"account_id"`
	Workspace Workspace `json:"workspace"`
}

// Failure codes are stable, so the card can put the message next to the field
// it is about.
const (
	FailureInvalidAddress     = "invalid_address"
	FailureUnsupported        = "unsupported"
	FailureNeedsServer        = "needs_server"
	FailureNeedsUsername      = "needs_username"
	FailureMissingPassword    = "missing_password"
	FailureWrongPassword      = "wrong_password"
	FailureUnreachable        = "unreachable"
	FailureKeyringUnavailable = "keyring_unavailable"
	FailureInternal           = "internal"
)

// Failure is a setup step that needs the user. Message is safe to show: it
// never contains the password or server text.
type Failure struct {
	Code    string `json:"error"`
	Message string `json:"message"`
	// Field names the input the message is about: address, password, username,
	// or server.
	Field string `json:"field,omitempty"`
	// Profile is included when the card should redraw its questions.
	Profile *Profile `json:"profile,omitempty"`
	cause   error
}

func (f *Failure) Error() string {
	if f.cause != nil {
		return f.Code + ": " + f.cause.Error()
	}
	return f.Code + ": " + f.Message
}

func (f *Failure) Unwrap() error { return f.cause }

// Service connects a mailbox from an address and a password.
type Service struct {
	lookup   MXLookup
	logins   LoginChecker
	keyring  VaultKeyring
	accounts AccountStore
	home     MailboxHome

	// mu makes a double-clicked Connect run once after the other, so it updates
	// the account the first one made instead of making a second.
	mu sync.Mutex
}

// NewService builds the service. A nil lookup uses the system resolver.
func NewService(lookup MXLookup, logins LoginChecker, keyring VaultKeyring, accounts AccountStore, home MailboxHome) *Service {
	if lookup == nil {
		lookup = net.DefaultResolver.LookupMX
	}
	return &Service{lookup: lookup, logins: logins, keyring: keyring, accounts: accounts, home: home}
}

// Detect reports what the card should ask for an address.
func (s *Service) Detect(ctx context.Context, address string) (Profile, error) {
	profile, err := Detect(ctx, address, s.lookup)
	if err != nil {
		return Profile{}, &Failure{Code: FailureInvalidAddress, Field: "address", Message: "Enter one email address, like you@example.com.", cause: err}
	}
	return profile, nil
}

// Connect checks the login, keeps it, and links the mailbox to Email Ops.
// Nothing is saved until the server has accepted the login.
func (s *Service) Connect(ctx context.Context, userID string, req ConnectRequest) (ConnectResult, error) {
	if s == nil || s.logins == nil || s.keyring == nil || s.accounts == nil || s.home == nil {
		return ConnectResult{}, &Failure{Code: FailureInternal, Message: "Email setup isn't available in this build."}
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	profile, err := s.Detect(ctx, req.Address)
	if err != nil {
		return ConnectResult{}, err
	}
	if !profile.Supported {
		return ConnectResult{}, &Failure{Code: FailureUnsupported, Field: "address", Message: profile.Unsupported, Profile: &profile}
	}
	creds, err := loginTarget(profile, req)
	if err != nil {
		return ConnectResult{}, err
	}

	username, err := s.checkLogin(ctx, profile, req.Username, creds)
	if err != nil {
		return ConnectResult{}, err
	}
	creds.Username = username

	vaultID, err := s.keyring.EnsureVault(ctx, vaultName)
	if err != nil {
		if errors.Is(err, vault.ErrKeyringUnavailable) {
			return ConnectResult{}, &Failure{
				Code:    FailureKeyringUnavailable,
				Message: "Ori couldn't save your login in this computer's keychain, so it couldn't read your mail after a restart. Nothing was saved.",
				cause:   err,
			}
		}
		return ConnectResult{}, internalFailure("open the email vault", err)
	}
	account, err := s.saveAccount(ctx, vaultID, profile, creds)
	if err != nil {
		return ConnectResult{}, internalFailure("save the login", err)
	}

	ws, err := s.home.EnsureEmailOps(ctx, userID)
	if err != nil {
		return ConnectResult{}, internalFailure("create Email Ops", err)
	}
	if err := s.home.LinkMailbox(ctx, userID, ws.ID, account.ID); err != nil {
		return ConnectResult{}, internalFailure("link the mailbox", err)
	}
	return ConnectResult{
		Address: profile.Address, Provider: profile.Provider, Label: profile.Label,
		AccountID: account.ID, Workspace: ws,
	}, nil
}

// loginTarget fills in the server and checks the card supplied what this
// provider needs.
func loginTarget(profile Profile, req ConnectRequest) (mailbox.IMAPCredentials, error) {
	host := strings.TrimSpace(req.IMAPHost)
	port := req.IMAPPort
	if host == "" {
		if profile.NeedsServer {
			return mailbox.IMAPCredentials{}, &Failure{Code: FailureNeedsServer, Field: "server", Message: "Enter your provider's IMAP server.", Profile: &profile}
		}
		host, port = profile.IMAPHost, profile.IMAPPort
	}
	if !validHostname(host) {
		return mailbox.IMAPCredentials{}, &Failure{Code: FailureNeedsServer, Field: "server", Message: "That doesn't look like a mail server name, like imap.example.com.", Profile: &profile}
	}
	if port == 0 {
		port = 993
	}
	if port < 1 || port > 65535 {
		return mailbox.IMAPCredentials{}, &Failure{Code: FailureNeedsServer, Field: "server", Message: "The server port must be a number from 1 to 65535.", Profile: &profile}
	}
	if profile.NeedsUsername && strings.TrimSpace(req.Username) == "" {
		return mailbox.IMAPCredentials{}, &Failure{Code: FailureNeedsUsername, Field: "username", Message: profile.UsernameHint, Profile: &profile}
	}
	password := profile.NormalizePassword(req.Password)
	if password == "" {
		return mailbox.IMAPCredentials{}, &Failure{Code: FailureMissingPassword, Field: "password", Message: "Paste the app password you created.", Profile: &profile}
	}
	return mailbox.IMAPCredentials{Host: host, Port: port, Password: password}, nil
}

// checkLogin tries each username the provider may expect and returns the one
// the server accepted.
func (s *Service) checkLogin(ctx context.Context, profile Profile, username string, creds mailbox.IMAPCredentials) (string, error) {
	for _, name := range profile.LoginNames(username) {
		creds.Username = name
		err := s.logins.CheckLogin(ctx, creds)
		if err == nil {
			return name, nil
		}
		if !errors.Is(err, mailbox.ErrExpired) {
			return "", &Failure{
				Code: FailureUnreachable, Field: "server",
				Message: fmt.Sprintf("Ori couldn't reach %s's mail server. Check your connection and try again.", profile.Label),
				cause:   err,
			}
		}
	}
	return "", &Failure{Code: FailureWrongPassword, Field: "password", Message: wrongPasswordMessage(profile), Profile: &profile}
}

func wrongPasswordMessage(profile Profile) string {
	switch profile.Provider {
	case ProviderGmail, ProviderGoogleWorkspace:
		return "Google didn't accept that password. Use an app password, not your Google password; Google only offers one once 2-Step Verification is on."
	case ProviderICloud:
		if profile.NeedsUsername {
			return "iCloud didn't accept that login. Check that the username is your @icloud.com address and the password is an app-specific password."
		}
		return "iCloud didn't accept that password. Use an app-specific password, not your Apple Account password."
	case ProviderOther:
		return "The server didn't accept that login. Check the password, and the server name if you entered one."
	}
	return profile.Label + " didn't accept that password. Use an app password, not your account password."
}

// saveAccount keeps the login in the vault, replacing the one this flow saved
// for the same address before, so reconnecting never leaves a stale copy.
func (s *Service) saveAccount(ctx context.Context, vaultID string, profile Profile, creds mailbox.IMAPCredentials) (*vault.EmailAccount, error) {
	authType := vault.EmailAuthTypeAppPassword
	if profile.Provider == ProviderOther {
		// Some providers have no app passwords; the user may have typed the real one.
		authType = vault.EmailAuthTypePassword
	}
	existing, err := s.accounts.ListEmailAccounts(ctx, vaultID, "")
	if err != nil {
		return nil, err
	}
	for _, acc := range existing {
		if acc.Source != AccountSource || !strings.EqualFold(acc.EmailAddress, profile.Address) {
			continue
		}
		username, host, port, password := creds.Username, creds.Host, creds.Port, creds.Password
		smtpHost, smtpPort := profile.SMTPHost, profile.SMTPPort
		return s.accounts.UpdateEmailAccount(ctx, acc.ID, vault.EmailAccountUpdate{
			AuthType: &authType, Username: &username,
			IMAPHost: &host, IMAPPort: &port, SMTPHost: &smtpHost, SMTPPort: &smtpPort,
			Password: &password,
		})
	}
	smtpHost := profile.SMTPHost
	if smtpHost == "" {
		smtpHost = creds.Host
	}
	return s.accounts.CreateEmailAccount(ctx, vault.EmailAccountInput{
		VaultID:      vaultID,
		Label:        profile.Label,
		Source:       AccountSource,
		Provider:     vault.EmailProviderIMAPSMTP,
		EmailAddress: profile.Address,
		Username:     creds.Username,
		AuthType:     authType,
		IMAPHost:     creds.Host,
		IMAPPort:     creds.Port,
		SMTPHost:     smtpHost,
		SMTPPort:     profile.SMTPPort,
		Credentials:  vault.EmailAccountCredentials{Password: creds.Password},
	})
}

func internalFailure(step string, err error) *Failure {
	return &Failure{Code: FailureInternal, Message: "Ori couldn't " + step + ". Nothing you typed was lost; try again.", cause: err}
}

// validHostname accepts a plain DNS name, which is all a mail server setting
// needs. It keeps a URL, a path, or an address with a port out of the dial.
func validHostname(host string) bool {
	if host == "" || len(host) > 253 || !strings.Contains(host, ".") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !isHostnameRune(r) {
				return false
			}
		}
	}
	return true
}

func isHostnameRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
		return true
	}
	return false
}
