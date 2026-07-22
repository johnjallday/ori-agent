package server

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/vault"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// newWorkspaceMailboxTestLinker builds a linker with no Personal HQ designated,
// exercising the workspace-scoped path in isolation from HQ resolution.
func newWorkspaceMailboxTestLinker(t *testing.T, wstore workspace.Store, accounts emailAccountResolver) *mailboxLinkerService {
	t.Helper()
	// hq service is present but nothing is designated; the workspace-scoped
	// methods must not consult it.
	return newMailboxLinkerService(&personalhq.Service{}, wstore, accounts, nil)
}

func TestWorkspaceMailboxLinkStatusUnlink_NoHQRequired(t *testing.T) {
	ctx := context.Background()
	userID := userprofile.LocalUserID

	wstore := workspace.NewInMemoryStore()
	if err := wstore.Save(&workspace.Workspace{ID: "eo-1", Name: "Email Ops", OwnerUserID: userID}); err != nil {
		t.Fatalf("save workspace: %v", err)
	}
	accounts := fakeAccounts{acc: &vault.EmailAccount{
		ID: "acct-1", Provider: vault.EmailProviderGmail, EmailAddress: "me@example.com",
		CredentialsStatus: vault.EmailAccountSecretState{HasRefreshToken: true},
	}}
	linker := newWorkspaceMailboxTestLinker(t, wstore, accounts)

	// Initially disconnected.
	st, err := linker.WorkspaceMailboxStatus(ctx, userID, "eo-1")
	if err != nil || st.Connected {
		t.Fatalf("expected disconnected, got %+v err=%v", st, err)
	}

	// Link, with no Personal HQ designated anywhere.
	st, err = linker.LinkWorkspaceMailbox(ctx, userID, "eo-1", "acct-1")
	if err != nil {
		t.Fatalf("LinkWorkspaceMailbox: %v", err)
	}
	if !st.Connected || st.EmailAddress != "me@example.com" {
		t.Fatalf("unexpected linked status: %+v", st)
	}
	ws, _ := wstore.Get("eo-1")
	if _, ok := emailBindingFor(ws); !ok {
		t.Fatal("expected an email binding on the Email Ops workspace after linking")
	}

	// Unlink removes it.
	st, err = linker.UnlinkWorkspaceMailbox(ctx, userID, "eo-1")
	if err != nil || st.Connected {
		t.Fatalf("expected disconnected after unlink, got %+v err=%v", st, err)
	}
	ws, _ = wstore.Get("eo-1")
	if _, ok := emailBindingFor(ws); ok {
		t.Fatal("email binding should be gone after unlink")
	}
}

func TestWorkspaceMailboxLinkRejectsNonOwner(t *testing.T) {
	ctx := context.Background()
	wstore := workspace.NewInMemoryStore()
	// Workspace owned by a different user.
	if err := wstore.Save(&workspace.Workspace{ID: "eo-2", Name: "Someone else's", OwnerUserID: "other-user"}); err != nil {
		t.Fatalf("save workspace: %v", err)
	}
	accounts := fakeAccounts{acc: &vault.EmailAccount{ID: "acct-1", Provider: vault.EmailProviderGmail}}
	linker := newWorkspaceMailboxTestLinker(t, wstore, accounts)

	if _, err := linker.LinkWorkspaceMailbox(ctx, userprofile.LocalUserID, "eo-2", "acct-1"); err == nil {
		t.Fatal("expected linking a workspace owned by another user to be rejected")
	}
	if _, err := linker.WorkspaceMailboxStatus(ctx, userprofile.LocalUserID, "eo-2"); err == nil {
		t.Fatal("expected status on a workspace owned by another user to be rejected")
	}
}

// TestWorkspaceMailboxLinkSecondWorkspaceNoReauth links the same already-
// authorized account (empty WorkspaceID = global) into two owned workspaces.
// GetEmailAccount is the only account touchpoint — no OAuth is performed — so a
// second link succeeding proves no re-authorization is required (FR9).
func TestWorkspaceMailboxLinkSecondWorkspaceNoReauth(t *testing.T) {
	ctx := context.Background()
	userID := userprofile.LocalUserID
	wstore := workspace.NewInMemoryStore()
	_ = wstore.Save(&workspace.Workspace{ID: "eo-a", Name: "Email Ops A", OwnerUserID: userID})
	_ = wstore.Save(&workspace.Workspace{ID: "eo-b", Name: "Email Ops B", OwnerUserID: userID})

	// Global account (empty WorkspaceID): shareable across the user's workspaces.
	accounts := countingAccounts{acc: &vault.EmailAccount{
		ID: "acct-1", Provider: vault.EmailProviderGmail, EmailAddress: "me@example.com",
		CredentialsStatus: vault.EmailAccountSecretState{HasRefreshToken: true},
	}}
	linker := newWorkspaceMailboxTestLinker(t, wstore, &accounts)

	if _, err := linker.LinkWorkspaceMailbox(ctx, userID, "eo-a", "acct-1"); err != nil {
		t.Fatalf("link workspace A: %v", err)
	}
	if _, err := linker.LinkWorkspaceMailbox(ctx, userID, "eo-b", "acct-1"); err != nil {
		t.Fatalf("link workspace B (same account, should not re-auth): %v", err)
	}
	// Both workspaces are connected to the same account.
	for _, id := range []string{"eo-a", "eo-b"} {
		ws, _ := wstore.Get(id)
		if _, ok := emailBindingFor(ws); !ok {
			t.Fatalf("workspace %s should be connected to the shared account", id)
		}
	}
	// Only account lookups happened — never any OAuth/create.
	if accounts.oauthCalls != 0 {
		t.Fatalf("expected no re-authorization, got %d oauth calls", accounts.oauthCalls)
	}
}

// countingAccounts is a resolver that records whether anything beyond a plain
// account lookup was attempted; the linker only ever calls GetEmailAccount.
type countingAccounts struct {
	acc        *vault.EmailAccount
	oauthCalls int
}

func (c *countingAccounts) GetEmailAccount(_ context.Context, id string) (*vault.EmailAccount, error) {
	if c.acc != nil && c.acc.ID == id {
		return c.acc, nil
	}
	return nil, nil
}
