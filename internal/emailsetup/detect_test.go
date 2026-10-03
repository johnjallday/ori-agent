package emailsetup

import (
	"context"
	"errors"
	"net"
	"testing"
)

func mxFor(records map[string][]string) MXLookup {
	return func(_ context.Context, domain string) ([]*net.MX, error) {
		hosts, ok := records[domain]
		if !ok {
			return nil, &net.DNSError{Err: "no such host", Name: domain, IsNotFound: true}
		}
		out := make([]*net.MX, 0, len(hosts))
		for _, h := range hosts {
			out = append(out, &net.MX{Host: h})
		}
		return out, nil
	}
}

func TestDetectKnownConsumerDomains(t *testing.T) {
	noLookup := func(context.Context, string) ([]*net.MX, error) {
		t.Fatal("a known domain must not need a DNS lookup")
		return nil, nil
	}
	for address, want := range map[string]Provider{
		"Me@Gmail.com": ProviderGmail, "me@icloud.com": ProviderICloud, "me@me.com": ProviderICloud,
		"me@yahoo.co.uk": ProviderYahoo, "me@aol.com": ProviderAOL, "me@fastmail.com": ProviderFastmail,
		"me@hotmail.com": ProviderMicrosoft,
	} {
		got, err := Detect(context.Background(), address, noLookup)
		if err != nil || got.Provider != want {
			t.Errorf("Detect(%s) = %s, %v; want %s", address, got.Provider, err, want)
		}
	}
}

func TestDetectCustomDomainsFromMailExchangers(t *testing.T) {
	lookup := mxFor(map[string][]string{
		"school.example": {"aspmx.l.google.com.", "alt1.aspmx.l.google.com."},
		"corp.example":   {"corp-example.mail.protection.outlook.com."},
		"family.example": {"mx01.mail.icloud.com."},
		"fm.example":     {"in1-smtp.messagingengine.com."},
		"own.example":    {"mail.own.example."},
	})
	cases := []struct {
		address  string
		provider Provider
		check    func(Profile) bool
	}{
		{"me@school.example", ProviderGoogleWorkspace, func(p Profile) bool { return p.Supported && p.Warning != "" && p.IMAPHost == "imap.gmail.com" }},
		{"me@corp.example", ProviderMicrosoft, func(p Profile) bool { return !p.Supported && p.Unsupported != "" }},
		{"me@family.example", ProviderICloud, func(p Profile) bool { return p.NeedsUsername && p.UsernameHint != "" }},
		{"me@fm.example", ProviderFastmail, func(p Profile) bool { return p.IMAPHost == "imap.fastmail.com" }},
		{"me@own.example", ProviderOther, func(p Profile) bool { return p.NeedsServer && p.IMAPHost == "imap.own.example" }},
		{"me@nodns.example", ProviderOther, func(p Profile) bool { return p.NeedsServer }},
	}
	for _, tc := range cases {
		got, err := Detect(context.Background(), tc.address, lookup)
		if err != nil || got.Provider != tc.provider || !tc.check(got) {
			t.Errorf("Detect(%s) = %+v, %v; want %s", tc.address, got, err, tc.provider)
		}
		if got.Address != tc.address {
			t.Errorf("Detect(%s).Address = %q", tc.address, got.Address)
		}
	}
}

// A domain that merely ends in a provider's name must not be mistaken for it.
func TestDetectMatchesWholeDomainLabels(t *testing.T) {
	lookup := mxFor(map[string][]string{"lookalike.example": {"mx.notgoogle.com."}})
	got, err := Detect(context.Background(), "me@lookalike.example", lookup)
	if err != nil || got.Provider != ProviderOther {
		t.Fatalf("Detect = %s, %v; want other", got.Provider, err)
	}
}

func TestDetectRejectsWhatIsNotOneAddress(t *testing.T) {
	for _, input := range []string{"", "me", "me@", "@gmail.com", "Me <me@gmail.com>", "a@b.com, c@d.com", "me@localhost", "me@[127.0.0.1]"} {
		if _, err := Detect(context.Background(), input, mxFor(nil)); !errors.Is(err, ErrInvalidAddress) {
			t.Errorf("Detect(%q) err = %v, want ErrInvalidAddress", input, err)
		}
	}
}

func TestLoginNames(t *testing.T) {
	icloud, _ := Detect(context.Background(), "jane@icloud.com", mxFor(nil))
	if got := icloud.LoginNames(""); len(got) != 2 || got[0] != "jane" || got[1] != "jane@icloud.com" {
		t.Fatalf("iCloud login names = %q, want the name first, then the address", got)
	}
	if got := icloud.LoginNames(" jane.doe "); len(got) != 1 || got[0] != "jane.doe" {
		t.Fatalf("explicit username = %q", got)
	}
	gmail, _ := Detect(context.Background(), "jane@gmail.com", mxFor(nil))
	if got := gmail.LoginNames(""); len(got) != 1 || got[0] != "jane@gmail.com" {
		t.Fatalf("Gmail login names = %q", got)
	}
}

func TestNormalizePassword(t *testing.T) {
	gmail, _ := Detect(context.Background(), "jane@gmail.com", mxFor(nil))
	if got := gmail.NormalizePassword(" abcd efgh ijkl mnop "); got != "abcdefghijklmnop" {
		t.Fatalf("Google app password = %q, want the spaces removed", got)
	}
	icloud, _ := Detect(context.Background(), "jane@icloud.com", mxFor(nil))
	if got := icloud.NormalizePassword(" abcd-efgh-ijkl-mnop\n"); got != "abcd-efgh-ijkl-mnop" {
		t.Fatalf("Apple app password = %q, want the dashes kept", got)
	}
}
