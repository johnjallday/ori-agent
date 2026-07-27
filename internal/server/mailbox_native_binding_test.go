package server

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/vault"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// The mailbox gate and the runtime resolver must agree about which bindings are
// native mail: the gate authorizes exactly the bindings the resolver excludes
// from MCP materialization (FR 26, 31). These tests pin that agreement, plus the
// link/unlink behavior that keeps unrelated MCP bindings intact (FR 29, 30).

func linkerWithWorkspace(t *testing.T, ws *workspace.Workspace, acc *vault.EmailAccount) (*mailboxLinkerService, workspace.Store) {
	t.Helper()
	store := workspace.NewInMemoryStore()
	if err := store.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}
	return newMailboxLinkerService(nil, store, fakeAccounts{acc: acc}, nil), store
}

func gmailAccount() *vault.EmailAccount {
	return &vault.EmailAccount{
		ID: "acct-1", Provider: vault.EmailProviderGmail, EmailAddress: "me@example.com",
		CredentialsStatus: vault.EmailAccountSecretState{HasRefreshToken: true},
	}
}

// FR 29: linking marks the binding native, so the very next task resolution
// does not try to launch a gmail MCP server.
func TestLinkMailbox_WritesNativeRuntimeKind(t *testing.T) {
	ctx := context.Background()
	ws := &workspace.Workspace{ID: "ws-1", Name: "Email Ops"}
	linker, store := linkerWithWorkspace(t, ws, gmailAccount())

	if _, err := linker.LinkWorkspaceMailbox(ctx, "", "ws-1", "acct-1"); err != nil {
		t.Fatalf("link: %v", err)
	}

	saved, _ := store.Get("ws-1")
	binding, ok := emailBindingFor(saved)
	if !ok {
		t.Fatal("expected a native email binding after linking")
	}
	if binding.RuntimeKind != workspace.RuntimeKindNativeEmail {
		t.Fatalf("runtime kind = %q, want native_email", binding.RuntimeKind)
	}
	if binding.IsRuntimeMCP() {
		t.Fatal("a linked mailbox must never be treated as an MCP binding")
	}
}

// FR 29 (upgrade path): relinking a workspace whose binding predates the field
// rewrites it in place as native, rather than adding a second binding.
func TestLinkMailbox_UpgradesLegacyBindingInPlace(t *testing.T) {
	ctx := context.Background()
	ws := &workspace.Workspace{ID: "ws-1", Name: "Email Ops"}
	if err := ws.UpsertMCPBinding(workspace.MCPBinding{
		ID: "legacy-mail", ServerName: "gmail", Enabled: true,
		Config: map[string]any{"account_id": "acct-1", "allowed_actions": []any{"read", "search"}},
	}); err != nil {
		t.Fatalf("seed legacy binding: %v", err)
	}
	linker, store := linkerWithWorkspace(t, ws, gmailAccount())

	if _, err := linker.LinkWorkspaceMailbox(ctx, "", "ws-1", "acct-1"); err != nil {
		t.Fatalf("relink: %v", err)
	}

	saved, _ := store.Get("ws-1")
	if len(saved.MCPBindings) != 1 {
		t.Fatalf("bindings = %d, want the legacy one updated in place", len(saved.MCPBindings))
	}
	binding := saved.MCPBindings[0]
	if binding.ID != "legacy-mail" {
		t.Fatalf("binding id = %q, want the existing legacy id reused", binding.ID)
	}
	if binding.RuntimeKind != workspace.RuntimeKindNativeEmail {
		t.Fatalf("runtime kind = %q, want the legacy binding upgraded", binding.RuntimeKind)
	}
}

// FR 30: unlinking touches only the mailbox binding.
func TestUnlinkMailbox_LeavesOtherBindingsIntact(t *testing.T) {
	ctx := context.Background()
	ws := &workspace.Workspace{ID: "ws-1", Name: "Email Ops"}
	for _, b := range []workspace.MCPBinding{
		{ID: "b-fs", ServerName: "filesystem", Enabled: true},
		{ID: "b-cal", ServerName: "google-calendar", Enabled: true},
		{ID: "b-drive", ServerName: "google-drive", Enabled: true},
	} {
		if err := ws.UpsertMCPBinding(b); err != nil {
			t.Fatalf("seed %s: %v", b.ID, err)
		}
	}
	linker, store := linkerWithWorkspace(t, ws, gmailAccount())

	if _, err := linker.LinkWorkspaceMailbox(ctx, "", "ws-1", "acct-1"); err != nil {
		t.Fatalf("link: %v", err)
	}
	saved, _ := store.Get("ws-1")
	if len(saved.MCPBindings) != 4 {
		t.Fatalf("bindings after link = %d, want 4", len(saved.MCPBindings))
	}

	if _, err := linker.UnlinkWorkspaceMailbox(ctx, "", "ws-1"); err != nil {
		t.Fatalf("unlink: %v", err)
	}
	saved, _ = store.Get("ws-1")
	if len(saved.MCPBindings) != 3 {
		t.Fatalf("bindings after unlink = %d, want the 3 MCP bindings preserved", len(saved.MCPBindings))
	}
	for _, id := range []string{"b-fs", "b-cal", "b-drive"} {
		if _, ok := saved.GetMCPBinding(id); !ok {
			t.Fatalf("unlink removed unrelated binding %s", id)
		}
	}
	if _, ok := emailBindingFor(saved); ok {
		t.Fatal("the native email binding should be gone")
	}
}

// FR 26, 31: the gate accepts exactly the bindings the resolver classifies as
// native, including every legacy alias, and rejects real MCP bindings.
func TestEmailBindingFor_UsesSharedClassifier(t *testing.T) {
	cases := []struct {
		name    string
		binding workspace.MCPBinding
		want    bool
	}{
		{"explicit native", workspace.MCPBinding{ID: "b", ServerName: "gmail", Enabled: true, RuntimeKind: workspace.RuntimeKindNativeEmail, Config: readableAccountConfig()}, true},
		{"legacy gmail", workspace.MCPBinding{ID: "b", ServerName: "gmail", Enabled: true, Config: readableAccountConfig()}, true},
		{"legacy imap", workspace.MCPBinding{ID: "b", ServerName: "imap-smtp", Enabled: true, Config: readableAccountConfig()}, true},
		{"legacy outlook", workspace.MCPBinding{ID: "b", ServerName: "outlook-mail", Enabled: true, Config: readableAccountConfig()}, true},
		{"filesystem", workspace.MCPBinding{ID: "b", ServerName: "filesystem", Enabled: true, Config: readableAccountConfig()}, false},
		{"calendar", workspace.MCPBinding{ID: "b", ServerName: "google-calendar", Enabled: true, Config: readableAccountConfig()}, false},
		{"explicit mcp on an email name", workspace.MCPBinding{ID: "b", ServerName: "email", Enabled: true, RuntimeKind: workspace.RuntimeKindMCP, Config: readableAccountConfig()}, false},
		{"unknown kind fails closed", workspace.MCPBinding{ID: "b", ServerName: "gmail", Enabled: true, RuntimeKind: workspace.BindingRuntimeKind("native_calendar"), Config: readableAccountConfig()}, false},
		{"disabled native binding", workspace.MCPBinding{ID: "b", ServerName: "gmail", Enabled: false, RuntimeKind: workspace.RuntimeKindNativeEmail, Config: readableAccountConfig()}, false},
		{"native binding with no account", workspace.MCPBinding{ID: "b", ServerName: "gmail", Enabled: true, RuntimeKind: workspace.RuntimeKindNativeEmail}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ws := &workspace.Workspace{ID: "ws-1", MCPBindings: []workspace.MCPBinding{tc.binding}}
			if _, ok := emailBindingFor(ws); ok != tc.want {
				t.Fatalf("emailBindingFor = %v, want %v", ok, tc.want)
			}
			// Whatever the gate accepts, the resolver must exclude — and vice versa.
			if tc.want && tc.binding.IsRuntimeMCP() {
				t.Fatal("a binding the gate authorizes must not also be materialized as MCP")
			}
		})
	}
}

func readableAccountConfig() map[string]any {
	return map[string]any{"account_id": "acct-1", "allowed_actions": []any{"read", "search"}}
}
