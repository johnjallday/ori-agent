package server

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/connections"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Reproduces the exact reported state: a Google connection whose Gmail grant
// says "healthy" while the vault holds zero email accounts. Before the fix the
// card offered Connect email and the link 500'd.
func TestRepro_HealthyGrantWithNoCredential(t *testing.T) {
	ctx := context.Background()
	store := newTestVaultStore(t)
	created := createTestVault(t, store, "fase")
	// Deliberately create NO email account — the user's vault had zero.

	conns := connections.NewStore(t.TempDir())
	if err := conns.Save(&connections.Connection{
		ID: "c1", Provider: connections.ProviderGoogle, Subject: "sub-1",
		Email: "user@example.com", VaultID: created.ID,
		Grants: map[connections.ProductKey]*connections.ProductGrant{
			connections.ProductGmail: {
				ConnectionID: "c1", Product: connections.ProductGmail,
				CredentialRef: "acct-long-gone",
				Health:        connections.HealthHealthy, // stale
			},
		},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// 1. Health reconciliation now tells the truth.
	health := connectionGrantHealth{b: &ServerBuilder{vaultStore: store}}
	got, ok := health.LiveHealth(connections.ProductGmail, "acct-long-gone")
	if !ok || got != connections.HealthReconnectRequired {
		t.Fatalf("health = %q ok=%v, want reconnect_required — the card must stop offering Connect", got, ok)
	}

	// 2. The link path fails typed, so the endpoint can offer a reconnect.
	sink := newGmailCredentialSink(store)
	if _, err := sink.LinkGmailToWorkspace(ctx, "acct-long-gone", created.ID, "ws-1"); !isCredentialMissing(err) {
		t.Fatalf("link err = %v, want ErrCredentialMissing", err)
	}

	// 3. Readiness explains it to the workspace surface too.
	workspaces := workspace.NewInMemoryStore()
	ws := &workspace.Workspace{ID: "ws-1", Name: "Email Ops"}
	if err := ws.UpsertMCPBinding(workspace.MCPBinding{
		ID: "b-mail", ServerName: "gmail", Enabled: true,
		RuntimeKind: workspace.RuntimeKindNativeEmail,
		Config:      map[string]any{"account_id": "acct-long-gone"},
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if err := workspaces.Save(ws); err != nil {
		t.Fatalf("save: %v", err)
	}
	readiness := newEmailReadinessEvaluator(conns, newConnectionVaultCatalog(store), workspaces, store)
	verdict := readiness.Evaluate(ctx, "ws-1")
	if verdict.Ready {
		t.Fatal("readiness must not report ready with a missing credential")
	}
	// The vault itself is fine — it exists and is unlocked, exactly as in the
	// reported case. What is missing is the credential the binding points at.
	if verdict.Reason != workspace.BlockedReasonAccountUnavailable {
		t.Fatalf("reason = %q, want account_unavailable", verdict.Reason)
	}
	if verdict.ActionLabel == "" {
		t.Fatal("the user needs a concrete next step, not just a reason")
	}
}
