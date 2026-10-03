// Package emailsetup connects a mailbox from an email address and an app
// password: it works out who hosts the address, checks the login, keeps the
// credential where Ori can read it unattended, and links the mailbox to the
// user's Email Ops workspace.
package emailsetup

import (
	"context"
	"errors"
	"net"
	"net/mail"
	"strings"
	"time"
	"unicode"
)

// Provider names who hosts a mailbox, as far as setup needs to know.
type Provider string

const (
	ProviderGmail           Provider = "gmail"
	ProviderGoogleWorkspace Provider = "google_workspace"
	ProviderICloud          Provider = "icloud"
	ProviderYahoo           Provider = "yahoo"
	ProviderAOL             Provider = "aol"
	ProviderFastmail        Provider = "fastmail"
	ProviderMicrosoft       Provider = "microsoft"
	ProviderOther           Provider = "other"
)

// Profile is what the setup card needs to ask for one address: the servers, where
// the user makes an app password, and anything that would stop a password login
// from working. It carries no secret.
type Profile struct {
	Address  string   `json:"address"`
	Provider Provider `json:"provider"`
	Label    string   `json:"label"`
	// Supported is false when this provider does not accept passwords for mail
	// apps at all; Unsupported says why.
	Supported   bool   `json:"supported"`
	Unsupported string `json:"unsupported,omitempty"`

	IMAPHost string `json:"imap_host,omitempty"`
	IMAPPort int    `json:"imap_port,omitempty"`
	SMTPHost string `json:"smtp_host,omitempty"`
	SMTPPort int    `json:"smtp_port,omitempty"`
	// NeedsServer means Ori could not tell who hosts the address, so the card
	// asks for the mail server; IMAPHost is only a guess.
	NeedsServer bool `json:"needs_server,omitempty"`
	// NeedsUsername means this provider signs in with a different name than the
	// address typed; UsernameHint says which.
	NeedsUsername bool   `json:"needs_username,omitempty"`
	UsernameHint  string `json:"username_hint,omitempty"`

	AppPasswordURL   string `json:"app_password_url,omitempty"`
	AppPasswordSteps string `json:"app_password_steps,omitempty"`
	// Warning is a likely problem worth saying before the user goes looking,
	// e.g. a school account whose admin blocks app passwords.
	Warning string `json:"warning,omitempty"`
}

// ErrInvalidAddress means the input is not a single email address.
var ErrInvalidAddress = errors.New("emailsetup: not an email address")

// MXLookup resolves a domain's mail exchangers. net.DefaultResolver.LookupMX
// satisfies it.
type MXLookup func(ctx context.Context, domain string) ([]*net.MX, error)

const mxLookupTimeout = 3 * time.Second

const (
	googleAppPasswordURL = "https://myaccount.google.com/apppasswords"
	appleAccountURL      = "https://account.apple.com"
	yahooSecurityURL     = "https://login.yahoo.com/account/security"
	aolSecurityURL       = "https://login.aol.com/account/security"
	fastmailSettingsURL  = "https://app.fastmail.com/settings/"
)

func gmailProfile() Profile {
	return Profile{
		Provider: ProviderGmail, Label: "Gmail", Supported: true,
		IMAPHost: "imap.gmail.com", IMAPPort: 993, SMTPHost: "smtp.gmail.com", SMTPPort: 465,
		AppPasswordURL:   googleAppPasswordURL,
		AppPasswordSteps: "Turn on 2-Step Verification first; until it is on, Google says the app password setting isn't available. Then create an app password named Ori and paste it here.",
	}
}

func googleWorkspaceProfile() Profile {
	p := gmailProfile()
	p.Provider, p.Label = ProviderGoogleWorkspace, "Google Workspace"
	p.Warning = "Work and school Google accounts are often set up so app passwords aren't allowed. If Google says the setting isn't available for your account, this address can't be connected with a password."
	return p
}

func iCloudProfile() Profile {
	return Profile{
		Provider: ProviderICloud, Label: "iCloud Mail", Supported: true,
		IMAPHost: "imap.mail.me.com", IMAPPort: 993, SMTPHost: "smtp.mail.me.com", SMTPPort: 587,
		AppPasswordURL:   appleAccountURL,
		AppPasswordSteps: "Sign in, open Sign-In and Security, then App-Specific Passwords, and create one named Ori. Paste it exactly as Apple shows it.",
	}
}

func yahooProfile() Profile {
	return Profile{
		Provider: ProviderYahoo, Label: "Yahoo Mail", Supported: true,
		IMAPHost: "imap.mail.yahoo.com", IMAPPort: 993, SMTPHost: "smtp.mail.yahoo.com", SMTPPort: 465,
		AppPasswordURL:   yahooSecurityURL,
		AppPasswordSteps: "Choose Generate app password, name it Ori, and paste it here.",
	}
}

func aolProfile() Profile {
	return Profile{
		Provider: ProviderAOL, Label: "AOL Mail", Supported: true,
		IMAPHost: "imap.aol.com", IMAPPort: 993, SMTPHost: "smtp.aol.com", SMTPPort: 465,
		AppPasswordURL:   aolSecurityURL,
		AppPasswordSteps: "Choose Generate app password, name it Ori, and paste it here.",
	}
}

func fastmailProfile() Profile {
	return Profile{
		Provider: ProviderFastmail, Label: "Fastmail", Supported: true,
		IMAPHost: "imap.fastmail.com", IMAPPort: 993, SMTPHost: "smtp.fastmail.com", SMTPPort: 465,
		AppPasswordURL:   fastmailSettingsURL,
		AppPasswordSteps: "Open Privacy & Security, then App passwords, and create one for mail. Fastmail's Basic plan has no mail-app access.",
	}
}

func microsoftProfile() Profile {
	return Profile{
		Provider: ProviderMicrosoft, Label: "Outlook", Supported: false,
		Unsupported: "Microsoft no longer lets mail apps sign in with a password, so Outlook, Hotmail, and Microsoft 365 addresses can't be connected this way yet.",
	}
}

// knownDomains are the consumer domains whose host is certain from the name.
var knownDomains = map[string]func() Profile{
	"gmail.com": gmailProfile, "googlemail.com": gmailProfile,
	"icloud.com": iCloudProfile, "me.com": iCloudProfile, "mac.com": iCloudProfile,
	"yahoo.com": yahooProfile, "ymail.com": yahooProfile, "rocketmail.com": yahooProfile,
	"aol.com": aolProfile, "aim.com": aolProfile,
	"fastmail.com": fastmailProfile, "fastmail.fm": fastmailProfile,
	"outlook.com": microsoftProfile, "hotmail.com": microsoftProfile, "live.com": microsoftProfile, "msn.com": microsoftProfile,
}

// iCloudDomains are the addresses iCloud signs in with directly.
var iCloudDomains = map[string]bool{"icloud.com": true, "me.com": true, "mac.com": true}

// Detect works out who hosts address. A known consumer domain answers from its
// name; any other domain is identified from its mail exchangers, which is how a
// custom domain on Google Workspace, Microsoft 365, iCloud, or Fastmail is
// recognised. When nothing matches, the card asks for the server.
func Detect(ctx context.Context, address string, lookup MXLookup) (Profile, error) {
	address, domain, err := splitAddress(address)
	if err != nil {
		return Profile{}, err
	}
	profile, ok := profileForDomain(domain)
	if !ok {
		profile = profileFromMX(ctx, domain, lookup)
	}
	profile.Address = address
	if profile.Provider == ProviderICloud && !iCloudDomains[domain] {
		// A custom domain on iCloud signs in with the account's own iCloud
		// address, which the typed address does not reveal.
		profile.NeedsUsername = true
		profile.UsernameHint = "Your @icloud.com address. iCloud signs in with it, not with your custom domain."
	}
	return profile, nil
}

func profileForDomain(domain string) (Profile, bool) {
	if build, ok := knownDomains[domain]; ok {
		return build(), true
	}
	// Yahoo's country domains: yahoo.co.uk, yahoo.fr, ...
	if strings.HasPrefix(domain, "yahoo.") {
		return yahooProfile(), true
	}
	return Profile{}, false
}

func profileFromMX(ctx context.Context, domain string, lookup MXLookup) Profile {
	if lookup != nil {
		lookupCtx, cancel := context.WithTimeout(ctx, mxLookupTimeout)
		defer cancel()
		if records, err := lookup(lookupCtx, domain); err == nil {
			for _, record := range records {
				if record == nil {
					continue
				}
				if profile, ok := profileForMailExchanger(strings.ToLower(strings.TrimSuffix(record.Host, "."))); ok {
					return profile
				}
			}
		}
	}
	// Unknown host: most providers serve IMAP at imap.<domain>, so it is offered
	// as the starting guess, and the card asks to confirm it.
	return Profile{
		Provider: ProviderOther, Label: domain, Supported: true, NeedsServer: true,
		IMAPHost: "imap." + domain, IMAPPort: 993, SMTPHost: "smtp." + domain, SMTPPort: 465,
		AppPasswordSteps: "Use an app password if your provider offers one, or your mail password. Your provider's help pages list the IMAP server.",
	}
}

func profileForMailExchanger(host string) (Profile, bool) {
	switch {
	case hasDomainSuffix(host, "google.com"), hasDomainSuffix(host, "googlemail.com"):
		return googleWorkspaceProfile(), true
	case hasDomainSuffix(host, "mail.protection.outlook.com"), hasDomainSuffix(host, "outlook.com"):
		return microsoftProfile(), true
	case hasDomainSuffix(host, "mail.icloud.com"):
		return iCloudProfile(), true
	case hasDomainSuffix(host, "messagingengine.com"):
		return fastmailProfile(), true
	case hasDomainSuffix(host, "yahoodns.net"):
		return yahooProfile(), true
	}
	return Profile{}, false
}

func hasDomainSuffix(host, suffix string) bool {
	return host == suffix || strings.HasSuffix(host, "."+suffix)
}

// splitAddress accepts exactly one bare address and returns it lowercased, with
// its domain.
func splitAddress(raw string) (string, string, error) {
	raw = strings.TrimSpace(raw)
	parsed, err := mail.ParseAddress(raw)
	if err != nil || parsed.Address != raw {
		// A display name or angle brackets is not what the card asks for.
		return "", "", ErrInvalidAddress
	}
	at := strings.LastIndex(parsed.Address, "@")
	if at <= 0 || at == len(parsed.Address)-1 {
		return "", "", ErrInvalidAddress
	}
	address := strings.ToLower(parsed.Address)
	domain := address[at+1:]
	if !strings.Contains(domain, ".") || strings.ContainsAny(domain, "[]") {
		return "", "", ErrInvalidAddress
	}
	return address, domain, nil
}

// LoginNames are the usernames to try, in order. An explicit username is the
// only one tried. iCloud takes the name before the @ first and the full address
// second, as Apple's own instructions say.
func (p Profile) LoginNames(username string) []string {
	if username = strings.TrimSpace(username); username != "" {
		return []string{username}
	}
	if p.Provider == ProviderICloud && !p.NeedsUsername {
		if at := strings.Index(p.Address, "@"); at > 0 {
			return []string{p.Address[:at], p.Address}
		}
	}
	return []string{p.Address}
}

// NormalizePassword cleans what the user pasted. Google shows an app password
// in four groups separated by spaces that are not part of it; Apple's dashes
// are part of the password and stay.
func (p Profile) NormalizePassword(password string) string {
	switch p.Provider {
	case ProviderGmail, ProviderGoogleWorkspace:
		return strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, password)
	}
	return strings.TrimSpace(password)
}
