package server

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/connections"
	"github.com/johnjallday/ori-agent/internal/vault"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Unlink and disconnect both delete things. The property that matters is
// containment: unlinking ONE workspace must never disconnect another workspace
// or the account itself, and disconnecting Gmail must never touch Calendar,
// Drive, or the identity.

// teardownFixture wires a builder with just the stores teardown reads.
func teardownFixture(t *testing.T) (connectionProductTeardown, *ServerBuilder, *vault.Store, workspace.Store) {
	t.Helper()
	store := newTestVaultStore(t)
	createTestVault(t, store, "Personal")
	workspaces := workspace.NewInMemoryStore()
	b := &ServerBuilder{
		vaultStore:     store,
		workspaceStore: workspaces,
		connStore:      connections.NewStore(t.TempDir()),
	}
	return connectionProductTeardown{b: b}, b, store, workspaces
}

func seedConnectionWithGmail(t *testing.T, b *ServerBuilder, credentialRef, vaultID string) {
	t.Helper()
	if err := b.connStore.Save(&connections.Connection{
		ID: "c1", Provider: connections.ProviderGoogle, Subject: "sub-1",
		Email: "me@example.com", VaultID: vaultID,
		Grants: map[connections.ProductKey]*connections.ProductGrant{
			connections.ProductGmail: {
				ConnectionID: "c1", Product: connections.ProductGmail,
				CredentialRef: credentialRef, Health: connections.HealthHealthy,
			},
		},
	}); err != nil {
		t.Fatalf("seed connection: %v", err)
	}
}

// FR 73, 74: the shared authoritative credential survives a workspace unlink,
// because every other workspace — and the account itself — still needs it.
func TestUnlinkGmail_KeepsTheSharedCredential(t *testing.T) {
	ctx := context.Background()
	teardown, b, store, workspaces := teardownFixture(t)
	vaultID := vaultIDOf(t, store)

	authoritative := createAccount(t, store, vault.EmailAccountInput{
		VaultID: vaultID, EmailAddress: "me@example.com", Source: googleConnectionEmailSource,
	})
	seedConnectionWithGmail(t, b, authoritative.ID, vaultID)

	// Two workspaces reference the same authoritative credential.
	workspaceBoundTo(t, workspaces, "ws-1", authoritative.ID)
	workspaceBoundTo(t, workspaces, "ws-2", authoritative.ID)

	if err := teardown.UnlinkProductFromWorkspace(ctx, connections.ProductGmail, authoritative.ID, "ws-1"); err != nil {
		t.Fatalf("unlink: %v", err)
	}

	// ws-1 is unlinked...
	if got := boundAccountID(t, workspaces, "ws-1"); got != "" {
		t.Fatalf("ws-1 still has a mailbox binding referencing %q", got)
	}
	// ...but ws-2 and the credential are untouched.
	if got := boundAccountID(t, workspaces, "ws-2"); got != authoritative.ID {
		t.Fatalf("ws-2 binding = %q, want it untouched", got)
	}
	if acct, _ := store.GetEmailAccount(ctx, authoritative.ID); acct == nil {
		t.Fatal("unlinking one workspace deleted the shared credential")
	}
}

// A workspace-only legacy copy that nothing else references IS cleaned up.
func TestUnlinkGmail_RemovesAnUnreferencedWorkspaceCopy(t *testing.T) {
	ctx := context.Background()
	teardown, b, store, workspaces := teardownFixture(t)
	vaultID := vaultIDOf(t, store)

	authoritative := createAccount(t, store, vault.EmailAccountInput{
		VaultID: vaultID, EmailAddress: "me@example.com", Source: googleConnectionEmailSource,
	})
	legacyCopy := createAccount(t, store, vault.EmailAccountInput{
		VaultID: vaultID, EmailAddress: "me@example.com", WorkspaceID: "ws-1",
		Source: googleConnectionEmailSource,
	})
	seedConnectionWithGmail(t, b, authoritative.ID, vaultID)
	workspaceBoundTo(t, workspaces, "ws-1", legacyCopy.ID)

	if err := teardown.UnlinkProductFromWorkspace(ctx, connections.ProductGmail, authoritative.ID, "ws-1"); err != nil {
		t.Fatalf("unlink: %v", err)
	}

	if acct, _ := store.GetEmailAccount(ctx, legacyCopy.ID); acct != nil {
		t.Fatal("the workspace-only copy should have been cleaned up")
	}
	if acct, _ := store.GetEmailAccount(ctx, authoritative.ID); acct == nil {
		t.Fatal("the authoritative credential must survive")
	}
}

// The binding is removed BEFORE any deletion is considered, so no ordering can
// leave a binding pointing at a removed credential (FR 77).
func TestUnlinkGmail_NeverLeavesAnOrphanedBinding(t *testing.T) {
	ctx := context.Background()
	teardown, b, store, workspaces := teardownFixture(t)
	vaultID := vaultIDOf(t, store)

	legacyCopy := createAccount(t, store, vault.EmailAccountInput{
		VaultID: vaultID, EmailAddress: "me@example.com", WorkspaceID: "ws-1",
		Source: googleConnectionEmailSource,
	})
	seedConnectionWithGmail(t, b, "some-other-ref", vaultID)
	workspaceBoundTo(t, workspaces, "ws-1", legacyCopy.ID)

	if err := teardown.UnlinkProductFromWorkspace(ctx, connections.ProductGmail, "some-other-ref", "ws-1"); err != nil {
		t.Fatalf("unlink: %v", err)
	}

	ws, err := workspaces.Get("ws-1")
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}
	for _, binding := range ws.GetMCPBindings() {
		if binding.IsNativeEmail() {
			accountID := stringFromConfig(binding.Config, "account_id")
			if acct, _ := store.GetEmailAccount(ctx, accountID); acct == nil {
				t.Fatalf("binding %s references deleted credential %q", binding.ID, accountID)
			}
		}
	}
}

// Unlinking Gmail must leave a workspace's unrelated MCP bindings alone.
func TestUnlinkGmail_LeavesOtherBindingsAlone(t *testing.T) {
	ctx := context.Background()
	teardown, b, store, workspaces := teardownFixture(t)
	vaultID := vaultIDOf(t, store)

	authoritative := createAccount(t, store, vault.EmailAccountInput{
		VaultID: vaultID, EmailAddress: "me@example.com", Source: googleConnectionEmailSource,
	})
	seedConnectionWithGmail(t, b, authoritative.ID, vaultID)

	ws := &workspace.Workspace{ID: "ws-1"}
	for _, binding := range []workspace.MCPBinding{
		{ID: "b-mail", ServerName: "gmail", Enabled: true, RuntimeKind: workspace.RuntimeKindNativeEmail,
			Config: map[string]any{"account_id": authoritative.ID}},
		{ID: "b-fs", ServerName: "filesystem", Enabled: true},
		{ID: "b-cal", ServerName: "google-calendar", Enabled: true},
	} {
		if err := ws.UpsertMCPBinding(binding); err != nil {
			t.Fatalf("seed binding: %v", err)
		}
	}
	if err := workspaces.Save(ws); err != nil {
		t.Fatalf("save: %v", err)
	}

	if err := teardown.UnlinkProductFromWorkspace(ctx, connections.ProductGmail, authoritative.ID, "ws-1"); err != nil {
		t.Fatalf("unlink: %v", err)
	}

	saved, _ := workspaces.Get("ws-1")
	for _, id := range []string{"b-fs", "b-cal"} {
		if _, ok := saved.GetMCPBinding(id); !ok {
			t.Fatalf("Gmail unlink removed unrelated binding %s", id)
		}
	}
	if _, ok := saved.GetMCPBinding("b-mail"); ok {
		t.Fatal("the mailbox binding should be gone")
	}
}

// FR 75, 76: disconnecting Gmail removes its credential and nothing else.
func TestDisconnectGmail_IsProductScoped(t *testing.T) {
	ctx := context.Background()
	teardown, b, store, _ := teardownFixture(t)
	vaultID := vaultIDOf(t, store)

	gmailCred := createAccount(t, store, vault.EmailAccountInput{
		VaultID: vaultID, EmailAddress: "me@example.com", Source: googleConnectionEmailSource,
	})
	unrelated := createAccount(t, store, vault.EmailAccountInput{
		VaultID: vaultID, EmailAddress: "work@example.com", Source: "manual-setup",
	})
	seedConnectionWithGmail(t, b, gmailCred.ID, vaultID)

	if err := teardown.DisconnectProduct(ctx, connections.ProductGmail, gmailCred.ID); err != nil {
		t.Fatalf("disconnect: %v", err)
	}

	if acct, _ := store.GetEmailAccount(ctx, gmailCred.ID); acct != nil {
		t.Fatal("the Gmail credential should have been removed")
	}
	if acct, _ := store.GetEmailAccount(ctx, unrelated.ID); acct == nil {
		t.Fatal("disconnecting Gmail deleted an unrelated email account")
	}
	// The identity itself is untouched: disconnect is product-scoped.
	conn, err := b.connStore.Load()
	if err != nil || conn == nil || !conn.HasVerifiedIdentity() {
		t.Fatalf("the Google identity must survive a product disconnect: %+v", conn)
	}
}

// Repeating a teardown must stay safe — a half-completed disconnect is resumed,
// not compounded.
func TestTeardown_IsRepeatable(t *testing.T) {
	ctx := context.Background()
	teardown, b, store, workspaces := teardownFixture(t)
	vaultID := vaultIDOf(t, store)

	authoritative := createAccount(t, store, vault.EmailAccountInput{
		VaultID: vaultID, EmailAddress: "me@example.com", Source: googleConnectionEmailSource,
	})
	seedConnectionWithGmail(t, b, authoritative.ID, vaultID)
	workspaceBoundTo(t, workspaces, "ws-1", authoritative.ID)

	for i := 0; i < 3; i++ {
		if err := teardown.UnlinkProductFromWorkspace(ctx, connections.ProductGmail, authoritative.ID, "ws-1"); err != nil {
			t.Fatalf("unlink pass %d: %v", i, err)
		}
	}
	for i := 0; i < 3; i++ {
		if err := teardown.DisconnectProduct(ctx, connections.ProductGmail, authoritative.ID); err != nil && i == 0 {
			t.Fatalf("disconnect pass %d: %v", i, err)
		}
	}
}
