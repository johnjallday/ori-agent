package server

import (
	"context"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/vault"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Consolidation deletes a credential, which is irreversible. These tests exist
// to pin the asymmetry: a preserved orphan wastes bytes, while a wrongly-deleted
// credential costs the user their mailbox access. Every ambiguous case must
// resolve to "keep it".

type recordingInvalidator struct{ invalidated []string }

func (r *recordingInvalidator) InvalidateAccount(id string) {
	r.invalidated = append(r.invalidated, id)
}

// lifecycleFixture builds a real vault store plus a workspace store.
func lifecycleFixture(t *testing.T) (*credentialLifecycle, *vault.Store, workspace.Store, *recordingInvalidator) {
	t.Helper()
	store := newTestVaultStore(t)
	createTestVault(t, store, "Personal")
	workspaces := workspace.NewInMemoryStore()
	inv := &recordingInvalidator{}
	return newCredentialLifecycle(store, workspaces, inv), store, workspaces, inv
}

func vaultIDOf(t *testing.T, store *vault.Store) string {
	t.Helper()
	vaults, err := store.ListVaults(context.Background())
	if err != nil || len(vaults) == 0 {
		t.Fatalf("list vaults: %v", err)
	}
	return vaults[0].ID
}

func createAccount(t *testing.T, store *vault.Store, in vault.EmailAccountInput) *vault.EmailAccount {
	t.Helper()
	if in.VaultID == "" {
		in.VaultID = vaultIDOf(t, store)
	}
	if in.Provider == "" {
		in.Provider = vault.EmailProviderGmail
	}
	if in.AuthType == "" {
		in.AuthType = vault.EmailAuthTypeOAuth2
	}
	if in.Label == "" {
		in.Label = in.EmailAddress
	}
	// The vault rejects an OAuth account with no token, so give every fixture a
	// placeholder. Consolidation never reads it — the proof is metadata-only.
	if in.Credentials.AccessToken == "" && in.Credentials.RefreshToken == "" {
		in.Credentials.RefreshToken = "fixture-refresh-token"
	}
	account, err := store.CreateEmailAccount(context.Background(), in)
	if err != nil {
		t.Fatalf("create email account: %v", err)
	}
	return account
}

func workspaceBoundTo(t *testing.T, store workspace.Store, workspaceID, accountID string) {
	t.Helper()
	ws := &workspace.Workspace{ID: workspaceID, Name: workspaceID}
	if err := ws.UpsertMCPBinding(workspace.MCPBinding{
		ID: "b-mail", ServerName: "gmail", Enabled: true,
		RuntimeKind: workspace.RuntimeKindNativeEmail,
		Config:      map[string]any{"account_id": accountID, "allowed_actions": []any{"read", "search"}},
	}); err != nil {
		t.Fatalf("bind workspace: %v", err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}
}

func boundAccountID(t *testing.T, store workspace.Store, workspaceID string) string {
	t.Helper()
	ws, err := store.Get(workspaceID)
	if err != nil || ws == nil {
		t.Fatalf("get workspace %s: %v", workspaceID, err)
	}
	binding, ok := ws.GetMCPBinding("b-mail")
	if !ok {
		return ""
	}
	return stringFromConfig(binding.Config, "account_id")
}

// --- The proof itself --------------------------------------------------------

func TestProveDuplicate(t *testing.T) {
	authoritative := &vault.EmailAccount{
		ID: "auth-1", Provider: vault.EmailProviderGmail, EmailAddress: "me@example.com",
		VaultID: "v-1", Source: googleConnectionEmailSource,
	}
	duplicateOf := func(mutate func(*vault.EmailAccount)) *vault.EmailAccount {
		d := &vault.EmailAccount{
			ID: "dup-1", Provider: vault.EmailProviderGmail, EmailAddress: "me@example.com",
			VaultID: "v-1", WorkspaceID: "ws-1", Source: googleConnectionEmailSource,
		}
		if mutate != nil {
			mutate(d)
		}
		return d
	}

	cases := []struct {
		name        string
		candidate   *vault.EmailAccount
		wantProven  bool
		wantFailure consolidationFailure
	}{
		{"a genuine workspace copy", duplicateOf(nil), true, consolidationOK},
		{"the authoritative record itself", authoritative, false, failureSameRecord},
		{
			"a different Google account",
			duplicateOf(func(d *vault.EmailAccount) { d.EmailAddress = "other@example.com" }),
			false, failureIdentityMismatch,
		},
		{
			// Two records for the same address in different vaults are NOT
			// provably the same credential; the user drew that boundary.
			"the same address in a different vault",
			duplicateOf(func(d *vault.EmailAccount) { d.VaultID = "v-2" }),
			false, failureVaultMismatch,
		},
		{
			"a record with no identity to compare",
			duplicateOf(func(d *vault.EmailAccount) { d.EmailAddress = "" }),
			false, failureIdentityUnknown,
		},
		{
			"a global record, not a workspace copy",
			duplicateOf(func(d *vault.EmailAccount) { d.WorkspaceID = "" }),
			false, failureNotWorkspaceOwned,
		},
		{
			// Ori did not create it, so Ori does not get to delete it.
			"a record Ori did not create",
			duplicateOf(func(d *vault.EmailAccount) { d.Source = "manual-setup" }),
			false, failureForeignSource,
		},
		{
			"a non-Gmail record",
			duplicateOf(func(d *vault.EmailAccount) { d.Provider = vault.EmailProviderMicrosoft }),
			false, failureProviderMismatch,
		},
		{"a missing record", nil, false, failureIdentityUnknown},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			verdict := proveDuplicate(authoritative, tc.candidate)
			if verdict.Proven != tc.wantProven {
				t.Fatalf("proven = %v, want %v (failure %q)", verdict.Proven, tc.wantProven, verdict.Failure)
			}
			if verdict.Failure != tc.wantFailure {
				t.Fatalf("failure = %q, want %q", verdict.Failure, tc.wantFailure)
			}
			if !verdict.Proven && strings.TrimSpace(verdict.Detail) == "" {
				t.Fatal("a refusal must explain which condition failed")
			}
			// Diagnostics must never carry the account address.
			if strings.Contains(verdict.Detail, "@") {
				t.Fatalf("diagnostic leaked an address: %s", verdict.Detail)
			}
		})
	}
}

// Case differences in an email address are not a different account.
func TestProveDuplicate_IdentityComparisonIsCaseInsensitive(t *testing.T) {
	authoritative := &vault.EmailAccount{
		ID: "auth-1", Provider: vault.EmailProviderGmail, EmailAddress: "Me@Example.com",
		VaultID: "v-1", Source: googleConnectionEmailSource,
	}
	candidate := &vault.EmailAccount{
		ID: "dup-1", Provider: vault.EmailProviderGmail, EmailAddress: "me@example.com",
		VaultID: "v-1", WorkspaceID: "ws-1", Source: googleConnectionEmailSource,
	}
	if verdict := proveDuplicate(authoritative, candidate); !verdict.Proven {
		t.Fatalf("verdict = %+v, want proven", verdict)
	}
}

// --- The reverse-reference scan ---------------------------------------------

func TestReferencesTo_FindsEveryUse(t *testing.T) {
	lifecycle, store, workspaces, _ := lifecycleFixture(t)
	acct := createAccount(t, store, vault.EmailAccountInput{EmailAddress: "me@example.com", Source: googleConnectionEmailSource})

	workspaceBoundTo(t, workspaces, "ws-1", acct.ID)
	workspaceBoundTo(t, workspaces, "ws-2", acct.ID)
	workspaceBoundTo(t, workspaces, "ws-3", "some-other-account")

	refs, err := lifecycle.referencesTo(acct.ID, "")
	if err != nil {
		t.Fatalf("referencesTo: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("found %d references, want 2: %+v", len(refs), refs)
	}

	// The connection grant counts as a reference too.
	refs, err = lifecycle.referencesTo(acct.ID, acct.ID)
	if err != nil {
		t.Fatalf("referencesTo: %v", err)
	}
	if len(refs) != 3 {
		t.Fatalf("found %d references, want 3 including the grant", len(refs))
	}
	grantRefs := 0
	for _, ref := range refs {
		if ref.IsGrant {
			grantRefs++
		}
	}
	if grantRefs != 1 {
		t.Fatalf("grant references = %d, want 1", grantRefs)
	}
}

// A binding that is not native email is not a mailbox reference.
func TestReferencesTo_IgnoresNonEmailBindings(t *testing.T) {
	lifecycle, store, workspaces, _ := lifecycleFixture(t)
	acct := createAccount(t, store, vault.EmailAccountInput{EmailAddress: "me@example.com", Source: googleConnectionEmailSource})

	ws := &workspace.Workspace{ID: "ws-1"}
	if err := ws.UpsertMCPBinding(workspace.MCPBinding{
		ID: "b-fs", ServerName: "filesystem", Enabled: true,
		Config: map[string]any{"account_id": acct.ID},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := workspaces.Save(ws); err != nil {
		t.Fatalf("save: %v", err)
	}

	refs, err := lifecycle.referencesTo(acct.ID, "")
	if err != nil {
		t.Fatalf("referencesTo: %v", err)
	}
	if len(refs) != 0 {
		t.Fatalf("found %+v, want none", refs)
	}
}

// --- Proven consolidation ----------------------------------------------------

func TestConsolidateDuplicate_RepointsThenDeletes(t *testing.T) {
	ctx := context.Background()
	lifecycle, store, workspaces, inv := lifecycleFixture(t)

	authoritative := createAccount(t, store, vault.EmailAccountInput{
		EmailAddress: "me@example.com", Source: googleConnectionEmailSource,
	})
	duplicate := createAccount(t, store, vault.EmailAccountInput{
		EmailAddress: "me@example.com", WorkspaceID: "ws-1", Source: googleConnectionEmailSource,
	})
	workspaceBoundTo(t, workspaces, "ws-1", duplicate.ID)

	consolidated, err := lifecycle.consolidateDuplicate(ctx, authoritative.ID, duplicate.ID, authoritative.ID)
	if err != nil {
		t.Fatalf("consolidate: %v", err)
	}
	if !consolidated {
		t.Fatal("a provable duplicate should have been consolidated")
	}

	// The binding now points at the authoritative record — never at a deleted id.
	if got := boundAccountID(t, workspaces, "ws-1"); got != authoritative.ID {
		t.Fatalf("binding references %q, want the authoritative %q", got, authoritative.ID)
	}
	// The duplicate is gone; the authoritative record survives.
	if acct, _ := store.GetEmailAccount(ctx, duplicate.ID); acct != nil {
		t.Fatal("the duplicate should have been deleted")
	}
	if acct, _ := store.GetEmailAccount(ctx, authoritative.ID); acct == nil {
		t.Fatal("the authoritative record must survive")
	}
	// Cached reads for the removed record are dropped.
	if len(inv.invalidated) != 1 || inv.invalidated[0] != duplicate.ID {
		t.Fatalf("invalidated = %v, want the duplicate", inv.invalidated)
	}
}

// Running it twice must be safe — a partially-completed consolidation is
// resumable, and a completed one is a no-op.
func TestConsolidateDuplicate_IsIdempotent(t *testing.T) {
	ctx := context.Background()
	lifecycle, store, workspaces, _ := lifecycleFixture(t)

	authoritative := createAccount(t, store, vault.EmailAccountInput{EmailAddress: "me@example.com", Source: googleConnectionEmailSource})
	duplicate := createAccount(t, store, vault.EmailAccountInput{EmailAddress: "me@example.com", WorkspaceID: "ws-1", Source: googleConnectionEmailSource})
	workspaceBoundTo(t, workspaces, "ws-1", duplicate.ID)

	if _, err := lifecycle.consolidateDuplicate(ctx, authoritative.ID, duplicate.ID, authoritative.ID); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	consolidated, err := lifecycle.consolidateDuplicate(ctx, authoritative.ID, duplicate.ID, authoritative.ID)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if consolidated {
		t.Fatal("the second pass should find nothing to do")
	}
	if got := boundAccountID(t, workspaces, "ws-1"); got != authoritative.ID {
		t.Fatalf("binding drifted to %q", got)
	}
}

// Every unprovable case preserves the record. This is the test that matters
// most: each row is a credential a user would otherwise have lost.
func TestConsolidateDuplicate_PreservesAmbiguousRecords(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name      string
		candidate vault.EmailAccountInput
	}{
		{"a different account", vault.EmailAccountInput{EmailAddress: "other@example.com", WorkspaceID: "ws-1", Source: googleConnectionEmailSource}},
		{"a record Ori did not create", vault.EmailAccountInput{EmailAddress: "me@example.com", WorkspaceID: "ws-1", Source: "manual-setup"}},
		{"a global record", vault.EmailAccountInput{EmailAddress: "me@example.com", Source: googleConnectionEmailSource}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lifecycle, store, workspaces, _ := lifecycleFixture(t)
			authoritative := createAccount(t, store, vault.EmailAccountInput{EmailAddress: "me@example.com", Source: googleConnectionEmailSource})
			candidate := createAccount(t, store, tc.candidate)
			workspaceBoundTo(t, workspaces, "ws-1", candidate.ID)

			consolidated, err := lifecycle.consolidateDuplicate(ctx, authoritative.ID, candidate.ID, authoritative.ID)
			if err != nil {
				t.Fatalf("consolidate: %v", err)
			}
			if consolidated {
				t.Fatal("an unprovable record must not be consolidated")
			}
			if acct, _ := store.GetEmailAccount(ctx, candidate.ID); acct == nil {
				t.Fatal("an unprovable record must be PRESERVED, not deleted")
			}
			// Its binding is untouched, so the workspace keeps working.
			if got := boundAccountID(t, workspaces, "ws-1"); got != candidate.ID {
				t.Fatalf("binding was repointed to %q despite no proof", got)
			}
		})
	}
}

// If the connection's own grant points at the candidate, it IS the live
// credential. Deleting it would disconnect the account entirely.
func TestConsolidateDuplicate_RefusesWhenTheGrantReferencesIt(t *testing.T) {
	ctx := context.Background()
	lifecycle, store, workspaces, _ := lifecycleFixture(t)

	authoritative := createAccount(t, store, vault.EmailAccountInput{EmailAddress: "me@example.com", Source: googleConnectionEmailSource})
	live := createAccount(t, store, vault.EmailAccountInput{EmailAddress: "me@example.com", WorkspaceID: "ws-1", Source: googleConnectionEmailSource})
	workspaceBoundTo(t, workspaces, "ws-1", live.ID)

	// The grant points at the candidate.
	consolidated, err := lifecycle.consolidateDuplicate(ctx, authoritative.ID, live.ID, live.ID)
	if err != nil {
		t.Fatalf("consolidate: %v", err)
	}
	if consolidated {
		t.Fatal("the live credential must never be consolidated away")
	}
	if acct, _ := store.GetEmailAccount(ctx, live.ID); acct == nil {
		t.Fatal("the live credential was deleted")
	}
}

// A workspace we cannot read might hold a reference, so "zero references" must
// not be concluded from an incomplete scan.
func TestConsolidateDuplicate_RefusesOnAnIncompleteScan(t *testing.T) {
	ctx := context.Background()
	store := newTestVaultStore(t)
	createTestVault(t, store, "Personal")
	lifecycle := newCredentialLifecycle(store, unreadableWorkspaceStore{}, nil)

	authoritative := createAccount(t, store, vault.EmailAccountInput{EmailAddress: "me@example.com", Source: googleConnectionEmailSource})
	duplicate := createAccount(t, store, vault.EmailAccountInput{EmailAddress: "me@example.com", WorkspaceID: "ws-1", Source: googleConnectionEmailSource})

	if _, err := lifecycle.consolidateDuplicate(ctx, authoritative.ID, duplicate.ID, authoritative.ID); err == nil {
		t.Fatal("an unreadable workspace must abort the scan, not be skipped")
	}
	if acct, _ := store.GetEmailAccount(ctx, duplicate.ID); acct == nil {
		t.Fatal("nothing may be deleted when the scan is incomplete")
	}
}

// --- Workspace-only deletion (unlink) ---------------------------------------

func TestDeleteWorkspaceCredentialIfUnreferenced(t *testing.T) {
	ctx := context.Background()

	t.Run("deletes an unreferenced workspace copy", func(t *testing.T) {
		lifecycle, store, _, inv := lifecycleFixture(t)
		acct := createAccount(t, store, vault.EmailAccountInput{
			EmailAddress: "me@example.com", WorkspaceID: "ws-1", Source: googleConnectionEmailSource,
		})
		deleted, err := lifecycle.deleteWorkspaceCredentialIfUnreferenced(ctx, acct.ID, "grant-ref")
		if err != nil {
			t.Fatalf("delete: %v", err)
		}
		if !deleted {
			t.Fatal("an unreferenced workspace copy should be removed")
		}
		if len(inv.invalidated) != 1 {
			t.Fatalf("invalidated = %v, want the removed account", inv.invalidated)
		}
	})

	t.Run("never deletes the authoritative credential", func(t *testing.T) {
		lifecycle, store, _, _ := lifecycleFixture(t)
		acct := createAccount(t, store, vault.EmailAccountInput{
			EmailAddress: "me@example.com", WorkspaceID: "ws-1", Source: googleConnectionEmailSource,
		})
		deleted, err := lifecycle.deleteWorkspaceCredentialIfUnreferenced(ctx, acct.ID, acct.ID)
		if err != nil {
			t.Fatalf("delete: %v", err)
		}
		if deleted {
			t.Fatal("unlinking a workspace must never delete the account's own credential")
		}
		if got, _ := store.GetEmailAccount(ctx, acct.ID); got == nil {
			t.Fatal("the authoritative credential was deleted")
		}
	})

	t.Run("never deletes a global record", func(t *testing.T) {
		lifecycle, store, _, _ := lifecycleFixture(t)
		acct := createAccount(t, store, vault.EmailAccountInput{
			EmailAddress: "me@example.com", Source: googleConnectionEmailSource,
		})
		deleted, _ := lifecycle.deleteWorkspaceCredentialIfUnreferenced(ctx, acct.ID, "grant-ref")
		if deleted {
			t.Fatal("a global record is not a workspace's to delete")
		}
	})

	t.Run("never deletes a record Ori did not create", func(t *testing.T) {
		lifecycle, store, _, _ := lifecycleFixture(t)
		acct := createAccount(t, store, vault.EmailAccountInput{
			EmailAddress: "me@example.com", WorkspaceID: "ws-1", Source: "manual-setup",
		})
		deleted, _ := lifecycle.deleteWorkspaceCredentialIfUnreferenced(ctx, acct.ID, "grant-ref")
		if deleted {
			t.Fatal("a manually-created record must be preserved")
		}
	})

	t.Run("never deletes while another workspace still references it", func(t *testing.T) {
		lifecycle, store, workspaces, _ := lifecycleFixture(t)
		acct := createAccount(t, store, vault.EmailAccountInput{
			EmailAddress: "me@example.com", WorkspaceID: "ws-1", Source: googleConnectionEmailSource,
		})
		workspaceBoundTo(t, workspaces, "ws-other", acct.ID)

		deleted, err := lifecycle.deleteWorkspaceCredentialIfUnreferenced(ctx, acct.ID, "grant-ref")
		if err != nil {
			t.Fatalf("delete: %v", err)
		}
		if deleted {
			t.Fatal("a credential another workspace still uses must survive")
		}
		if got, _ := store.GetEmailAccount(ctx, acct.ID); got == nil {
			t.Fatal("deleting it would have orphaned the other workspace's binding")
		}
	})
}

// unreadableWorkspaceStore fails every read, standing in for a corrupt or
// locked workspace during a scan.
type unreadableWorkspaceStore struct{ workspace.Store }

func (unreadableWorkspaceStore) List() ([]string, error) { return []string{"ws-1"}, nil }
func (unreadableWorkspaceStore) Get(string) (*workspace.Workspace, error) {
	return nil, context.DeadlineExceeded
}
