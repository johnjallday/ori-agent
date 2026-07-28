package server

import (
	"context"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/connections"
	"github.com/johnjallday/ori-agent/internal/vault"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// One authoritative credential, many references. These tests pin the two
// properties that make that safe: a re-auth never loses the refresh token, and
// linking never creates a second copy of it.

func sinkFixture(t *testing.T) (*gmailCredentialSink, *vault.Store, string) {
	t.Helper()
	store := newTestVaultStore(t)
	created := createTestVault(t, store, "Personal")
	return newGmailCredentialSink(store), store, created.ID
}

// FR 72: Google omits the refresh token on most re-authorizations. Overwriting
// it with the empty string would silently destroy long-lived access, and the
// user would only discover it when the access token expired an hour later.
func TestSaveGmailCredential_PreservesRefreshTokenWhenGoogleOmitsIt(t *testing.T) {
	ctx := context.Background()
	sink, store, vaultID := sinkFixture(t)

	ref, err := sink.SaveGmailCredential(ctx, connections.GmailCredential{
		Email: "me@example.com", VaultID: vaultID,
		AccessToken: "at-1", RefreshToken: "rt-original",
	})
	if err != nil {
		t.Fatalf("initial save: %v", err)
	}

	// A re-auth returning only a new access token.
	again, err := sink.SaveGmailCredential(ctx, connections.GmailCredential{
		Email: "me@example.com", VaultID: vaultID,
		AccessToken: "at-2", RefreshToken: "",
		ExistingRef: ref,
	})
	if err != nil {
		t.Fatalf("re-auth save: %v", err)
	}
	if again != ref {
		t.Fatalf("re-auth created a new record %q; it must update %q in place", again, ref)
	}

	creds, err := store.RevealEmailOAuthCredentials(ctx, ref, vault.AccessContext{})
	if err != nil {
		t.Fatalf("reveal: %v", err)
	}
	if creds.RefreshToken != "rt-original" {
		t.Fatalf("refresh token = %q, want the original preserved", creds.RefreshToken)
	}
	if creds.AccessToken != "at-2" {
		t.Fatalf("access token = %q, want the refreshed one", creds.AccessToken)
	}
}

// A re-auth that DOES return a new refresh token must adopt it.
func TestSaveGmailCredential_AdoptsANewRefreshToken(t *testing.T) {
	ctx := context.Background()
	sink, store, vaultID := sinkFixture(t)

	ref, err := sink.SaveGmailCredential(ctx, connections.GmailCredential{
		Email: "me@example.com", VaultID: vaultID, AccessToken: "at-1", RefreshToken: "rt-1",
	})
	if err != nil {
		t.Fatalf("initial save: %v", err)
	}
	if _, err := sink.SaveGmailCredential(ctx, connections.GmailCredential{
		Email: "me@example.com", VaultID: vaultID,
		AccessToken: "at-2", RefreshToken: "rt-2", ExistingRef: ref,
	}); err != nil {
		t.Fatalf("re-auth: %v", err)
	}

	creds, err := store.RevealEmailOAuthCredentials(ctx, ref, vault.AccessContext{})
	if err != nil {
		t.Fatalf("reveal: %v", err)
	}
	if creds.RefreshToken != "rt-2" {
		t.Fatalf("refresh token = %q, want the replacement", creds.RefreshToken)
	}
}

// FR 68-70: linking references the authoritative credential; it copies nothing.
func TestLinkGmailToWorkspace_CreatesNoSecondCopy(t *testing.T) {
	ctx := context.Background()
	sink, store, vaultID := sinkFixture(t)

	ref, err := sink.SaveGmailCredential(ctx, connections.GmailCredential{
		Email: "me@example.com", VaultID: vaultID, AccessToken: "at", RefreshToken: "rt",
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	before, err := store.ListEmailAccounts(ctx, "", "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	// Two different workspaces both link.
	first, err := sink.LinkGmailToWorkspace(ctx, ref, vaultID, "ws-1")
	if err != nil {
		t.Fatalf("link ws-1: %v", err)
	}
	second, err := sink.LinkGmailToWorkspace(ctx, ref, vaultID, "ws-2")
	if err != nil {
		t.Fatalf("link ws-2: %v", err)
	}

	if first != ref || second != ref {
		t.Fatalf("links resolved to %q and %q, want the authoritative %q", first, second, ref)
	}
	after, err := store.ListEmailAccounts(ctx, "", "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("linking created %d extra credential record(s); it must create none", len(after)-len(before))
	}
}

// FR 69: relinking the same workspace is idempotent.
func TestLinkGmailToWorkspace_IsIdempotent(t *testing.T) {
	ctx := context.Background()
	sink, store, vaultID := sinkFixture(t)
	ref, err := sink.SaveGmailCredential(ctx, connections.GmailCredential{
		Email: "me@example.com", VaultID: vaultID, AccessToken: "at", RefreshToken: "rt",
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	for i := 0; i < 3; i++ {
		got, err := sink.LinkGmailToWorkspace(ctx, ref, vaultID, "ws-1")
		if err != nil {
			t.Fatalf("link %d: %v", i, err)
		}
		if got != ref {
			t.Fatalf("link %d returned %q, want %q", i, got, ref)
		}
	}
	accounts, _ := store.ListEmailAccounts(ctx, "", "")
	if len(accounts) != 1 {
		t.Fatalf("accounts = %d, want exactly one", len(accounts))
	}
}

// A broken reference must surface at link time, not at the first mailbox read.
func TestLinkGmailToWorkspace_RejectsAMissingCredential(t *testing.T) {
	ctx := context.Background()
	sink, _, vaultID := sinkFixture(t)

	if _, err := sink.LinkGmailToWorkspace(ctx, "", vaultID, "ws-1"); err == nil {
		t.Fatal("an empty credential reference must be rejected")
	}
	if _, err := sink.LinkGmailToWorkspace(ctx, "does-not-exist", vaultID, "ws-1"); err == nil {
		t.Fatal("a dangling credential reference must be rejected")
	}
}

// FR 88/89: migration folds a legacy record in only when it can prove it is a
// duplicate — and never leaves a binding pointing at a deleted id.
func TestMigrateAccount(t *testing.T) {
	ctx := context.Background()

	newFixture := func(t *testing.T) (*gmailCredentialSink, *vault.Store, workspace.Store, string) {
		t.Helper()
		store := newTestVaultStore(t)
		created := createTestVault(t, store, "Personal")
		workspaces := workspace.NewInMemoryStore()
		sink := newGmailCredentialSink(store)
		sink.lifecycle = newCredentialLifecycle(store, workspaces, nil)
		return sink, store, workspaces, created.ID
	}

	t.Run("a provable legacy copy is folded in and its binding repointed", func(t *testing.T) {
		sink, store, workspaces, vaultID := newFixture(t)
		authoritative := createAccount(t, store, vault.EmailAccountInput{
			VaultID: vaultID, EmailAddress: "me@example.com", Source: googleConnectionEmailSource,
		})
		legacy := createAccount(t, store, vault.EmailAccountInput{
			VaultID: vaultID, EmailAddress: "me@example.com", WorkspaceID: "ws-1",
			Source: googleConnectionEmailSource,
		})
		workspaceBoundTo(t, workspaces, "ws-1", legacy.ID)

		if err := sink.MigrateAccount(ctx, legacy.ID, "me@example.com", authoritative.ID, vaultID); err != nil {
			t.Fatalf("migrate: %v", err)
		}
		if got := boundAccountID(t, workspaces, "ws-1"); got != authoritative.ID {
			t.Fatalf("binding references %q after migration, want %q — a deleted id would orphan it",
				got, authoritative.ID)
		}
	})

	t.Run("a different Google account is rejected outright", func(t *testing.T) {
		sink, store, _, vaultID := newFixture(t)
		authoritative := createAccount(t, store, vault.EmailAccountInput{
			VaultID: vaultID, EmailAddress: "me@example.com", Source: googleConnectionEmailSource,
		})
		other := createAccount(t, store, vault.EmailAccountInput{
			VaultID: vaultID, EmailAddress: "someone@else.com", WorkspaceID: "ws-1",
			Source: googleConnectionEmailSource,
		})

		err := sink.MigrateAccount(ctx, other.ID, "me@example.com", authoritative.ID, vaultID)
		if err == nil || !strings.Contains(err.Error(), "different") {
			t.Fatalf("err = %v, want an account mismatch", err)
		}
		if acct, _ := store.GetEmailAccount(ctx, other.ID); acct == nil {
			t.Fatal("a different account must never be deleted")
		}
	})

	t.Run("an unprovable record is preserved and reported as skipped", func(t *testing.T) {
		sink, store, _, vaultID := newFixture(t)
		authoritative := createAccount(t, store, vault.EmailAccountInput{
			VaultID: vaultID, EmailAddress: "me@example.com", Source: googleConnectionEmailSource,
		})
		// Same address, but manually created: Ori did not make it, so Ori does
		// not delete it.
		manual := createAccount(t, store, vault.EmailAccountInput{
			VaultID: vaultID, EmailAddress: "me@example.com", WorkspaceID: "ws-1", Source: "manual-setup",
		})

		err := sink.MigrateAccount(ctx, manual.ID, "me@example.com", authoritative.ID, vaultID)
		if err == nil {
			t.Fatal("an unprovable migration must report that it was skipped")
		}
		if !strings.Contains(err.Error(), "left in place") {
			t.Fatalf("err = %v, want a skipped-not-failed outcome", err)
		}
		if acct, _ := store.GetEmailAccount(ctx, manual.ID); acct == nil {
			t.Fatal("the record must be preserved")
		}
	})
}
