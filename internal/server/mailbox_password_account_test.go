package server

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/connections"
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
	if got.Action != emailActionSetUpEmail || got.ActionURL != emailSetupURL {
		t.Fatalf("readiness = %+v, want the setup card, never the Google Account card", got)
	}
}

func TestEmailReadiness_UnlinkedWorkspaceWithoutGoogleOffersTheSetupCard(t *testing.T) {
	// Nothing is linked and Google sign-in is not set up: the card connects a
	// mailbox without it, so it is the first step rather than Google.
	e := newEmailReadinessEvaluator(connectionStore(t, nil), healthyVaults(), unlinkedWorkspace(t), passwordAccount(true))

	got := e.Evaluate(context.Background(), "ws-1")
	if got.Action != emailActionSetUpEmail || got.ActionURL != emailSetupURL || got.Reason != workspace.BlockedReasonNotLinkedToWorkspace {
		t.Fatalf("readiness = %+v, want the setup card", got)
	}
}

func TestEmailReadiness_UnlinkedWorkspaceWithGoogleReadyKeepsTheGoogleLink(t *testing.T) {
	// A healthy Google connection still links in one click, as before.
	e := newEmailReadinessEvaluator(connectionStore(t, connectedWithGmail(connections.HealthHealthy)), healthyVaults(), unlinkedWorkspace(t), healthyAccount())

	if got := e.Evaluate(context.Background(), "ws-1"); got.Action != emailActionLinkAccount {
		t.Fatalf("readiness = %+v, want the existing link step", got)
	}
}

func TestEmailReadiness_LockedVaultIsNamedFirst(t *testing.T) {
	// The linked account's vault is locked: the account cannot be read, so its
	// kind is unknown, and "connect Google" would be the wrong repair.
	e := newEmailReadinessEvaluator(connectionStore(t, nil), healthyVaults(), linkedWorkspace(t), fakeAccounts{err: vault.ErrVaultLocked})

	got := e.Evaluate(context.Background(), "ws-1")
	if got.Action != emailActionRepairVault || got.Reason != workspace.BlockedReasonVaultRepairRequired || got.ActionURL != vaultsPage {
		t.Fatalf("readiness = %+v, want the unlock-vault repair", got)
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
