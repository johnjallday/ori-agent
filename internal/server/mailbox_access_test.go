package server

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/mailbox"
	"github.com/johnjallday/ori-agent/internal/vault"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type stubMailProvider struct{}

func (stubMailProvider) SearchThreads(ctx context.Context, a mailbox.Account, q mailbox.Query) (mailbox.ThreadPage, error) {
	return mailbox.ThreadPage{}, nil
}
func (stubMailProvider) GetThread(ctx context.Context, a mailbox.Account, id string) (mailbox.Thread, error) {
	return mailbox.Thread{}, nil
}

type fakeAccounts struct {
	acc *vault.EmailAccount
	err error
}

func (f fakeAccounts) GetEmailAccount(ctx context.Context, id string) (*vault.EmailAccount, error) {
	return f.acc, f.err
}

func hqWorkspace() *workspace.Workspace {
	return &workspace.Workspace{
		ID:   "hq-1",
		Name: "My HQ",
		AgentInstances: []workspace.AgentInstance{
			{ID: "chief-id", Name: "Personal Chief of Staff", EntryPoint: true},
			{ID: "inbox-id", Name: "Inbox"},
			{ID: "journal-id", Name: "Journal"},
		},
		MCPBindings: []workspace.MCPBinding{
			{ID: "b1", ServerName: "gmail", Enabled: true, Config: map[string]any{
				"account_id":      "acct-1",
				"allowed_actions": []any{"read", "search"},
			}},
		},
	}
}

func newAccessFixture(t *testing.T, ws *workspace.Workspace) *mailboxAccess {
	t.Helper()
	store := workspace.NewInMemoryStore()
	if err := store.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}
	accounts := fakeAccounts{acc: &vault.EmailAccount{
		ID: "acct-1", Provider: vault.EmailProviderGmail, EmailAddress: "me@example.com",
		// A usable credential: the gate requires a HEALTHY binding, not merely a
		// present one (FR 42), so an account with no tokens reads as expired.
		CredentialsStatus: vault.EmailAccountSecretState{HasRefreshToken: true},
	}}
	return newMailboxAccess(store, accounts, stubMailProvider{})
}

func TestMailboxAccessAuthorizesInboxAgent(t *testing.T) {
	a := newAccessFixture(t, hqWorkspace())

	if !a.CanAccess("hq-1", "Inbox") {
		t.Fatal("Inbox agent with an email binding should be granted exposure")
	}
	acc, err := a.AuthorizedAccount(context.Background(), "hq-1", "Inbox")
	if err != nil {
		t.Fatalf("AuthorizedAccount: %v", err)
	}
	if acc.ID != "acct-1" || acc.EmailAddress != "me@example.com" {
		t.Fatalf("unexpected account: %+v", acc)
	}
}

func TestMailboxAccessDeniesNonInboxAgents(t *testing.T) {
	a := newAccessFixture(t, hqWorkspace())
	for _, agent := range []string{"Journal", "Personal Chief of Staff", "Random"} {
		if a.CanAccess("hq-1", agent) {
			t.Errorf("%s must not be exposed the mail tools", agent)
		}
		if _, err := a.AuthorizedAccount(context.Background(), "hq-1", agent); !errors.Is(err, mailbox.ErrPermissionDenied) {
			t.Errorf("%s should be permission-denied, got %v", agent, err)
		}
	}
}

func TestMailboxAccessDisconnectedWithoutBinding(t *testing.T) {
	ws := hqWorkspace()
	ws.MCPBindings = nil
	a := newAccessFixture(t, ws)
	if a.CanAccess("hq-1", "Inbox") {
		t.Fatal("no email binding → no exposure")
	}
	if _, err := a.AuthorizedAccount(context.Background(), "hq-1", "Inbox"); !errors.Is(err, mailbox.ErrDisconnected) {
		t.Fatalf("expected ErrDisconnected without a binding, got %v", err)
	}
}

func TestMailboxAccessRespectsPerAgentBindingRestriction(t *testing.T) {
	ws := hqWorkspace()
	// Restrict the Inbox instance to a DIFFERENT binding — email must be denied.
	ws.AgentMCPAccess = []workspace.AgentMCPAccess{
		{AgentInstanceID: "inbox-id", EnabledBindingIDs: []string{"some-other-binding"}},
	}
	a := newAccessFixture(t, ws)
	if a.CanAccess("hq-1", "Inbox") {
		t.Fatal("an explicit access entry excluding the email binding must deny exposure")
	}
	if _, err := a.AuthorizedAccount(context.Background(), "hq-1", "Inbox"); !errors.Is(err, mailbox.ErrPermissionDenied) {
		t.Fatalf("expected permission denied under a restrictive access entry, got %v", err)
	}
}

func TestMailboxAccessBindingMustAllowRead(t *testing.T) {
	ws := hqWorkspace()
	ws.MCPBindings[0].Config["allowed_actions"] = []any{"send"} // no read/search
	a := newAccessFixture(t, ws)
	if a.CanAccess("hq-1", "Inbox") {
		t.Fatal("a send-only binding must not grant read exposure")
	}
}
