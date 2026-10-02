package server

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/mailbox"
	"github.com/johnjallday/ori-agent/internal/vault"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// A mailbox connected with a password or app password is read over IMAP and
// never touches Google. These tests pin the places that used to assume every
// usable account holds a Google token.

func passwordAccount(hasPassword bool) fakeAccounts {
	return fakeAccounts{acc: &vault.EmailAccount{
		ID: "acct-1", Provider: vault.EmailProviderIMAPSMTP, AuthType: vault.EmailAuthTypeAppPassword,
		EmailAddress:      "me@fastmail.example",
		CredentialsStatus: vault.EmailAccountSecretState{HasPassword: hasPassword},
	}}
}

func TestEmailReadiness_PasswordAccountNeedsNoGoogleConnection(t *testing.T) {
	// No Google connection at all, and no vault catalog entry for one.
	e := newEmailReadinessEvaluator(connectionStore(t, nil), healthyVaults(), linkedWorkspace(t), passwordAccount(true))

	got := e.Evaluate(context.Background(), "ws-1")
	if !got.Ready {
		t.Fatalf("readiness = %+v, want ready: a password account has nothing to do with Google", got)
	}
	if got.EmailAddress != "me@fastmail.example" {
		t.Fatalf("email = %q", got.EmailAddress)
	}
}

func TestEmailReadiness_PasswordAccountWithoutPasswordIsNotAGoogleRepair(t *testing.T) {
	e := newEmailReadinessEvaluator(connectionStore(t, nil), healthyVaults(), linkedWorkspace(t), passwordAccount(false))

	got := e.Evaluate(context.Background(), "ws-1")
	if got.Ready || got.Reason != workspace.BlockedReasonReconnectRequired {
		t.Fatalf("readiness = %+v, want reconnect required", got)
	}
	if got.Action == emailActionReconnect || got.ActionURL == googleAccountCard {
		t.Fatalf("readiness = %+v, must not send a password account to the Google Account card", got)
	}
}

func TestEmailReadiness_UnlinkedWorkspaceKeepsTheGoogleFirstOrder(t *testing.T) {
	// Nothing is linked, so there is no password account to exempt: the existing
	// first step is unchanged.
	e := newEmailReadinessEvaluator(connectionStore(t, nil), healthyVaults(), unlinkedWorkspace(t), passwordAccount(true))

	if got := e.Evaluate(context.Background(), "ws-1"); got.Action != emailActionConnectGoogle {
		t.Fatalf("readiness = %+v, want the Google connection step", got)
	}
}

func TestMailboxAccess_AuthorizesPasswordAccount(t *testing.T) {
	store := workspace.NewInMemoryStore()
	if err := store.Save(hqWorkspace()); err != nil {
		t.Fatalf("save workspace: %v", err)
	}

	ready := newMailboxAccess(store, passwordAccount(true), stubMailProvider{})
	acc, err := ready.AuthorizedAccount(context.Background(), "hq-1", "Inbox")
	if err != nil || acc.EmailAddress != "me@fastmail.example" {
		t.Fatalf("AuthorizedAccount = %+v, %v; want the password account", acc, err)
	}

	missing := newMailboxAccess(store, passwordAccount(false), stubMailProvider{})
	if _, err := missing.AuthorizedAccount(context.Background(), "hq-1", "Inbox"); !errors.Is(err, mailbox.ErrExpired) {
		t.Fatalf("err = %v, want ErrExpired for an account with no saved password", err)
	}
}

func TestAccountHealth_PasswordAccount(t *testing.T) {
	if got := accountHealth(passwordAccount(true).acc); got != "healthy" {
		t.Fatalf("health = %q, want healthy", got)
	}
	if got := accountHealth(passwordAccount(false).acc); got != "disconnected" {
		t.Fatalf("health = %q, want disconnected", got)
	}
	// An OAuth account is still judged by its tokens, never by a password field.
	oauthWithPasswordOnly := &vault.EmailAccount{AuthType: vault.EmailAuthTypeOAuth2, CredentialsStatus: vault.EmailAccountSecretState{HasPassword: true}}
	if got := accountHealth(oauthWithPasswordOnly); got != "disconnected" {
		t.Fatalf("health = %q, want disconnected", got)
	}
}
