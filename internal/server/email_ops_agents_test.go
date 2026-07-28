package server

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/mailbox"
	"github.com/johnjallday/ori-agent/internal/vault"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// The Email Ops agent boundary: Postmaster coordinates, Inbox reads. This is the
// safety property that makes the workspace's roster meaningful — if Postmaster
// could read mail directly, the delegation the blueprint describes would be a
// suggestion rather than an enforced boundary (FR 40-42).

func emailOpsWorkspace() *workspace.Workspace {
	return &workspace.Workspace{
		ID:   "ws-email-ops",
		Name: "Email Ops",
		AgentInstances: []workspace.AgentInstance{
			{ID: "postmaster-id", Name: "Postmaster", EntryPoint: true},
			{ID: "inbox-id", Name: "Inbox"},
		},
		MCPBindings: []workspace.MCPBinding{{
			ID: "b-mail", ServerName: "gmail", Enabled: true,
			RuntimeKind: workspace.RuntimeKindNativeEmail,
			Config: map[string]any{
				"account_id":      "acct-1",
				"allowed_actions": []any{"read", "search"},
			},
		}},
	}
}

// FR 40, 41: Postmaster is the entry/orchestrator agent and must coordinate mail
// work through Inbox — it never receives the native mail tools itself.
func TestEmailOps_PostmasterHasNoMailAccess(t *testing.T) {
	access := newAccessFixture(t, emailOpsWorkspace())

	if access.CanAccess("ws-email-ops", "Postmaster") {
		t.Fatal("Postmaster must not be exposed the native mail tools")
	}
	if _, err := access.AuthorizedAccount(context.Background(), "ws-email-ops", "Postmaster"); err == nil {
		t.Fatal("Postmaster must be denied at call time as well as at exposure time")
	}

	// The entry agent being the orchestrator is exactly why exposure-time denial
	// is not enough: it is the agent the user talks to.
	ws := emailOpsWorkspace()
	if !ws.AgentInstances[0].EntryPoint || ws.AgentInstances[0].Name != "Postmaster" {
		t.Fatal("Postmaster is expected to be the entry agent")
	}
}

// FR 42: Inbox gets mail access only when the workspace has a healthy
// read/search binding and per-agent access permits that binding.
func TestEmailOps_InboxAccessRequiresHealthyBinding(t *testing.T) {
	ctx := context.Background()

	t.Run("healthy binding authorizes Inbox", func(t *testing.T) {
		access := newAccessFixture(t, emailOpsWorkspace())
		if !access.CanAccess("ws-email-ops", "Inbox") {
			t.Fatal("Inbox must be authorized with a healthy read/search binding")
		}
		acc, err := access.AuthorizedAccount(ctx, "ws-email-ops", "Inbox")
		if err != nil {
			t.Fatalf("AuthorizedAccount: %v", err)
		}
		if acc.ID != "acct-1" {
			t.Fatalf("account = %q, want acct-1", acc.ID)
		}
	})

	t.Run("disabled binding denies Inbox", func(t *testing.T) {
		ws := emailOpsWorkspace()
		ws.MCPBindings[0].Enabled = false
		access := newAccessFixture(t, ws)
		if access.CanAccess("ws-email-ops", "Inbox") {
			t.Fatal("a disabled binding must not authorize mail access")
		}
	})

	t.Run("write-only binding denies Inbox", func(t *testing.T) {
		ws := emailOpsWorkspace()
		ws.MCPBindings[0].Config["allowed_actions"] = []any{"send"}
		access := newAccessFixture(t, ws)
		if access.CanAccess("ws-email-ops", "Inbox") {
			t.Fatal("a binding without read/search must not authorize reading")
		}
	})

	t.Run("per-agent access can exclude Inbox", func(t *testing.T) {
		ws := emailOpsWorkspace()
		ws.AgentMCPAccess = []workspace.AgentMCPAccess{
			{AgentInstanceID: "inbox-id", EnabledBindingIDs: []string{"some-other-binding"}},
		}
		access := newAccessFixture(t, ws)
		if access.CanAccess("ws-email-ops", "Inbox") {
			t.Fatal("per-agent access must be able to withhold the mail binding")
		}
	})

	t.Run("account with no credential reads as expired, not authorized", func(t *testing.T) {
		store := workspace.NewInMemoryStore()
		if err := store.Save(emailOpsWorkspace()); err != nil {
			t.Fatalf("save: %v", err)
		}
		access := newMailboxAccess(store, fakeAccounts{acc: &vault.EmailAccount{
			ID: "acct-1", EmailAddress: "me@example.com",
			CredentialsStatus: vault.EmailAccountSecretState{},
		}}, stubMailProvider{})

		_, err := access.AuthorizedAccount(ctx, "ws-email-ops", "Inbox")
		if err == nil {
			t.Fatal("an account with no usable credential must not authorize reads")
		}
		if err != mailbox.ErrExpired {
			t.Fatalf("error = %v, want ErrExpired so the UI offers reconnect", err)
		}
	})
}

// FR 44: no Email Ops agent gains a direct send tool from this work. Sending
// stays behind the confirm-gated broker, which is a user action, not a tool.
func TestEmailOps_NoAgentReceivesASendTool(t *testing.T) {
	access := newAccessFixture(t, emailOpsWorkspace())

	// The mailbox provider the tools are built on is read-only by type: it
	// exposes search and read, and nothing else. A send tool would require a
	// capability this interface does not have.
	provider := access.Provider()
	if provider == nil {
		t.Fatal("expected a mailbox provider")
	}
	var _ interface {
		SearchThreads(context.Context, mailbox.Account, mailbox.Query) (mailbox.ThreadPage, error)
		GetThread(context.Context, mailbox.Account, string) (mailbox.Thread, error)
	} = provider

	if _, isSender := provider.(interface {
		Send(context.Context, mailbox.Account, any) error
	}); isSender {
		t.Fatal("the agent-facing mailbox provider must not be able to send")
	}
}
