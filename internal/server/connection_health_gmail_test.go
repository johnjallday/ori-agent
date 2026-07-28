package server

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/connections"
	"github.com/johnjallday/ori-agent/internal/connectionshttp"
	"github.com/johnjallday/ori-agent/internal/vault"
)

// A Gmail grant can outlive its credential: a vault gets recreated, a data
// directory moves, a teardown half-completes. The grant then keeps reporting
// "healthy" — the connection card offers Connect email, the link fails, and the
// user gets an opaque 500 with a dead button.
//
// Health must be reconciled against what the vault actually holds. The
// distinction these tests protect is that a MISSING credential is a reconnect,
// while a LOCKED vault is not: the credential is intact and only temporarily
// unreadable, so telling the user to reauthorize would send them to redo work
// that is not broken.

func healthFixture(t *testing.T) (connectionGrantHealth, *vault.Store, string) {
	t.Helper()
	store := newTestVaultStore(t)
	created := createTestVault(t, store, "Personal")
	return connectionGrantHealth{b: &ServerBuilder{vaultStore: store}}, store, created.ID
}

// The reported bug: the grant references a credential the vault no longer has.
func TestGmailGrantHealth_MissingCredentialNeedsReconnect(t *testing.T) {
	health, _, _ := healthFixture(t)

	got, ok := health.LiveHealth(connections.ProductGmail, "acct-that-never-existed")
	if !ok {
		t.Fatal("a grant referencing a missing credential must be reconciled, not left as stored")
	}
	if got != connections.HealthReconnectRequired {
		t.Fatalf("health = %q, want reconnect_required", got)
	}
}

// A healthy credential keeps the stored health — reconciliation must not
// downgrade a working connection.
func TestGmailGrantHealth_PresentCredentialKeepsStoredHealth(t *testing.T) {
	health, store, vaultID := healthFixture(t)
	account := createAccount(t, store, vault.EmailAccountInput{
		VaultID: vaultID, EmailAddress: "me@example.com", Source: googleConnectionEmailSource,
	})

	if _, ok := health.LiveHealth(connections.ProductGmail, account.ID); ok {
		t.Fatal("a resolvable credential must not be downgraded")
	}
}

// The distinction that matters: a locked vault must NOT read as reconnect.
// The credential is fine; only the vault is closed. Reporting reconnect here
// would send the user through a Google reauthorization they do not need.
func TestGmailGrantHealth_LockedVaultIsNotAReconnect(t *testing.T) {
	ctx := context.Background()
	health, store, vaultID := healthFixture(t)
	account := createAccount(t, store, vault.EmailAccountInput{
		VaultID: vaultID, EmailAddress: "me@example.com", Source: googleConnectionEmailSource,
	})
	if err := store.Lock(ctx, vaultID); err != nil {
		t.Fatalf("lock vault: %v", err)
	}

	got, ok := health.LiveHealth(connections.ProductGmail, account.ID)
	if ok && got == connections.HealthReconnectRequired {
		t.Fatal("a locked vault must not be reported as needing reauthorization")
	}
}

// With nothing wired, reconciliation abstains rather than guessing.
func TestGmailGrantHealth_AbstainsWithoutAVaultStore(t *testing.T) {
	health := connectionGrantHealth{b: &ServerBuilder{}}
	if _, ok := health.LiveHealth(connections.ProductGmail, "acct-1"); ok {
		t.Fatal("with no vault store, health must be left as stored")
	}
	if _, ok := health.LiveHealth(connections.ProductGmail, ""); ok {
		t.Fatal("with no credential reference, health must be left as stored")
	}
}

// The link path is the symptom the user hits. A dangling credential must be a
// typed, actionable conflict — not an opaque failure.
func TestLinkGmailToWorkspace_DanglingCredentialIsTyped(t *testing.T) {
	ctx := context.Background()
	sink, _, vaultID := sinkFixture(t)

	_, err := sink.LinkGmailToWorkspace(ctx, "acct-that-never-existed", vaultID, "ws-1")
	if err == nil {
		t.Fatal("linking to a missing credential must fail")
	}
	if !isCredentialMissing(err) {
		t.Fatalf("err = %v, want it to match ErrCredentialMissing so the endpoint can offer a reconnect", err)
	}
}

func isCredentialMissing(err error) bool {
	return errors.Is(err, connectionshttp.ErrCredentialMissing)
}
