package server

import (
	"context"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/connections"
	"github.com/johnjallday/ori-agent/internal/vault"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Email Ops setup state must be a FACT the server checks, not a claim an agent
// made. These tests walk the readiness ladder condition by condition and assert
// that the first unmet one wins — repairing a later condition while an earlier
// one is broken achieves nothing, so naming the later one would waste the
// user's time.

type stubReadinessVaults struct {
	vaults []connections.VaultRef
}

func (s stubReadinessVaults) ListVaults(context.Context) ([]connections.VaultRef, error) {
	return s.vaults, nil
}

func (s stubReadinessVaults) VaultAvailability(_ context.Context, id string) (connections.VaultAvailability, error) {
	for _, v := range s.vaults {
		if v.ID == id {
			return v.Availability, nil
		}
	}
	return connections.VaultMissing, nil
}

func healthyVaults() stubReadinessVaults {
	return stubReadinessVaults{vaults: []connections.VaultRef{{ID: "v-1", Name: "Personal", Availability: connections.VaultAvailable}}}
}

// connectionStore builds a connections.Store seeded with conn (nil = none).
func connectionStore(t *testing.T, conn *connections.Connection) *connections.Store {
	t.Helper()
	store := connections.NewStore(t.TempDir())
	if conn != nil {
		if err := store.Save(conn); err != nil {
			t.Fatalf("seed connection: %v", err)
		}
	}
	return store
}

func connectedWithGmail(health connections.GrantHealth) *connections.Connection {
	return &connections.Connection{
		ID: "c1", Provider: connections.ProviderGoogle, Subject: "sub-1",
		Email: "me@example.com", VaultID: "v-1",
		Grants: map[connections.ProductKey]*connections.ProductGrant{
			connections.ProductGmail: {
				ConnectionID: "c1", Product: connections.ProductGmail,
				CredentialRef: "vault://email/acct-1", Health: health,
			},
		},
	}
}

func linkedWorkspace(t *testing.T) workspace.Store {
	t.Helper()
	ws := &workspace.Workspace{ID: "ws-1", Name: "Email Ops"}
	if err := ws.UpsertMCPBinding(workspace.MCPBinding{
		ID: "b-mail", ServerName: "gmail", Enabled: true,
		RuntimeKind: workspace.RuntimeKindNativeEmail,
		Config:      map[string]any{"account_id": "acct-1", "allowed_actions": []any{"read", "search"}},
	}); err != nil {
		t.Fatalf("seed binding: %v", err)
	}
	store := workspace.NewInMemoryStore()
	if err := store.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}
	return store
}

func unlinkedWorkspace(t *testing.T) workspace.Store {
	t.Helper()
	store := workspace.NewInMemoryStore()
	if err := store.Save(&workspace.Workspace{ID: "ws-1", Name: "Email Ops"}); err != nil {
		t.Fatalf("save workspace: %v", err)
	}
	return store
}

func healthyAccount() fakeAccounts {
	return fakeAccounts{acc: &vault.EmailAccount{
		ID: "acct-1", Provider: vault.EmailProviderGmail, EmailAddress: "me@example.com",
		CredentialsStatus: vault.EmailAccountSecretState{HasRefreshToken: true},
	}}
}

func TestEmailReadiness_Ladder(t *testing.T) {
	cases := []struct {
		name       string
		conn       *connections.Connection
		vaults     connections.VaultCatalog
		workspaces func(*testing.T) workspace.Store
		accounts   emailAccountResolver
		wantReady  bool
		wantReason string
		wantAction string
	}{
		{
			name:       "no google connection",
			conn:       nil,
			vaults:     healthyVaults(),
			workspaces: linkedWorkspace,
			accounts:   healthyAccount(),
			wantReason: workspace.BlockedReasonConnectionRequired,
			wantAction: emailActionConnectGoogle,
		},
		{
			name: "connected but gmail never enabled",
			conn: &connections.Connection{
				ID: "c1", Provider: connections.ProviderGoogle, Subject: "sub-1", VaultID: "v-1",
			},
			vaults:     healthyVaults(),
			workspaces: linkedWorkspace,
			accounts:   healthyAccount(),
			wantReason: workspace.BlockedReasonCapabilityNotEnabled,
			wantAction: emailActionEnableGmail,
		},
		{
			name:       "gmail grant needs reconnect",
			conn:       connectedWithGmail(connections.HealthReconnectRequired),
			vaults:     healthyVaults(),
			workspaces: linkedWorkspace,
			accounts:   healthyAccount(),
			wantReason: workspace.BlockedReasonReconnectRequired,
			wantAction: emailActionReconnect,
		},
		{
			name:       "vault locked",
			conn:       connectedWithGmail(connections.HealthHealthy),
			vaults:     stubReadinessVaults{vaults: []connections.VaultRef{{ID: "v-1", Name: "Personal", Availability: connections.VaultLocked}}},
			workspaces: linkedWorkspace,
			accounts:   healthyAccount(),
			wantReason: workspace.BlockedReasonVaultRepairRequired,
			wantAction: emailActionRepairVault,
		},
		{
			name:       "healthy globally but this workspace is not linked",
			conn:       connectedWithGmail(connections.HealthHealthy),
			vaults:     healthyVaults(),
			workspaces: unlinkedWorkspace,
			accounts:   healthyAccount(),
			wantReason: workspace.BlockedReasonNotLinkedToWorkspace,
			wantAction: emailActionLinkAccount,
		},
		{
			name:       "linked account no longer exists",
			conn:       connectedWithGmail(connections.HealthHealthy),
			vaults:     healthyVaults(),
			workspaces: linkedWorkspace,
			accounts:   fakeAccounts{},
			wantReason: workspace.BlockedReasonAccountUnavailable,
			wantAction: emailActionLinkAccount,
		},
		{
			name:       "linked account lost its credential",
			conn:       connectedWithGmail(connections.HealthHealthy),
			vaults:     healthyVaults(),
			workspaces: linkedWorkspace,
			accounts: fakeAccounts{acc: &vault.EmailAccount{
				ID: "acct-1", EmailAddress: "me@example.com",
				CredentialsStatus: vault.EmailAccountSecretState{},
			}},
			wantReason: workspace.BlockedReasonReconnectRequired,
			wantAction: emailActionReconnect,
		},
		{
			name:       "fully connected",
			conn:       connectedWithGmail(connections.HealthHealthy),
			vaults:     healthyVaults(),
			workspaces: linkedWorkspace,
			accounts:   healthyAccount(),
			wantReady:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			evaluator := newEmailReadinessEvaluator(
				connectionStore(t, tc.conn), tc.vaults, tc.workspaces(t), tc.accounts,
			)
			got := evaluator.Evaluate(context.Background(), "ws-1")

			if got.Ready != tc.wantReady {
				t.Fatalf("ready = %v, want %v (reason %q)", got.Ready, tc.wantReady, got.Reason)
			}
			if tc.wantReady {
				if got.EmailAddress != "me@example.com" {
					t.Fatalf("email address = %q, want the linked account's", got.EmailAddress)
				}
				return
			}
			if got.Reason != tc.wantReason {
				t.Fatalf("reason = %q, want %q", got.Reason, tc.wantReason)
			}
			if got.Action != tc.wantAction {
				t.Fatalf("action = %q, want %q", got.Action, tc.wantAction)
			}
			if strings.TrimSpace(got.Message) == "" || strings.TrimSpace(got.ActionLabel) == "" {
				t.Fatalf("every blocked state needs a message and an action label: %+v", got)
			}
		})
	}
}

// The verdict is browser-facing: it must never carry credentials or vault
// internals.
func TestEmailReadiness_CarriesNoSecrets(t *testing.T) {
	evaluator := newEmailReadinessEvaluator(
		connectionStore(t, connectedWithGmail(connections.HealthHealthy)),
		stubReadinessVaults{vaults: []connections.VaultRef{{ID: "v-1", Name: "My Secret Vault", Availability: connections.VaultLocked}}},
		linkedWorkspace(t), healthyAccount(),
	)
	got := evaluator.Evaluate(context.Background(), "ws-1")

	blob := strings.ToLower(got.Message + " " + got.ActionLabel + " " + got.ActionURL)
	for _, forbidden := range []string{"my secret vault", "vault://email/acct-1", "token"} {
		if strings.Contains(blob, forbidden) {
			t.Fatalf("readiness leaked %q: %+v", forbidden, got)
		}
	}
}

// --- Task gating (FR 34, 35) ------------------------------------------------

func TestCheckTaskCapabilities_BlocksWithTheExactRepair(t *testing.T) {
	evaluator := newEmailReadinessEvaluator(
		connectionStore(t, nil), healthyVaults(), linkedWorkspace(t), healthyAccount(),
	)

	blocked := evaluator.CheckTaskCapabilities("ws-1", []string{workspace.CapabilityEmail})
	if blocked == nil {
		t.Fatal("a mail-dependent task must not run with no Google connection")
	}
	if blocked.ReasonCode != workspace.BlockedReasonConnectionRequired {
		t.Fatalf("reason code = %q, want %q", blocked.ReasonCode, workspace.BlockedReasonConnectionRequired)
	}
	if blocked.Repair == nil || blocked.Repair.Code != emailActionConnectGoogle {
		t.Fatalf("repair = %+v, want connect_google", blocked.Repair)
	}
	if strings.TrimSpace(blocked.Repair.Label) == "" || strings.TrimSpace(blocked.Repair.URL) == "" {
		t.Fatalf("the repair must be actionable: %+v", blocked.Repair)
	}
	// Exactly one action: listing alternatives that cannot fix it is what made
	// these blocks unactionable before (FR 56).
	if len(blocked.SuggestedActions) != 1 {
		t.Fatalf("suggested actions = %v, want exactly one concrete repair", blocked.SuggestedActions)
	}
}

func TestCheckTaskCapabilities_AllowsWhenHealthy(t *testing.T) {
	evaluator := newEmailReadinessEvaluator(
		connectionStore(t, connectedWithGmail(connections.HealthHealthy)),
		healthyVaults(), linkedWorkspace(t), healthyAccount(),
	)
	if blocked := evaluator.CheckTaskCapabilities("ws-1", []string{workspace.CapabilityEmail}); blocked != nil {
		t.Fatalf("healthy workspace was blocked: %+v", blocked)
	}
}

// A task that declares nothing is never gated — that is every task created
// before this feature existed.
func TestCheckTaskCapabilities_IgnoresTasksWithNoRequirements(t *testing.T) {
	evaluator := newEmailReadinessEvaluator(
		connectionStore(t, nil), healthyVaults(), unlinkedWorkspace(t), fakeAccounts{},
	)
	if blocked := evaluator.CheckTaskCapabilities("ws-1", nil); blocked != nil {
		t.Fatalf("a task with no requirements must never be gated: %+v", blocked)
	}
	if blocked := evaluator.CheckTaskCapabilities("ws-1", []string{"  "}); blocked != nil {
		t.Fatalf("blank requirements must be ignored: %+v", blocked)
	}
	// An unknown key belongs to a future gate, not this one.
	if blocked := evaluator.CheckTaskCapabilities("ws-1", []string{"calendar"}); blocked != nil {
		t.Fatalf("an unrelated capability must not be gated here: %+v", blocked)
	}
}

// --- Linking is a plain application operation (FR 33) -----------------------

// Linking must not depend on a model call succeeding: the linker touches only
// the workspace store and the vault, and the resulting status carries a
// freshly-computed setup verdict (FR 33, 36).
func TestLinkMailbox_IsDeterministicAndRefreshesSetupState(t *testing.T) {
	ctx := context.Background()
	ws := &workspace.Workspace{ID: "ws-1", Name: "Email Ops"}
	store := workspace.NewInMemoryStore()
	if err := store.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}
	evaluator := newEmailReadinessEvaluator(
		connectionStore(t, connectedWithGmail(connections.HealthHealthy)),
		healthyVaults(), store, healthyAccount(),
	)
	linker := newMailboxLinkerService(nil, store, healthyAccount(), nil)
	linker.readiness = evaluator

	// Before linking: healthy globally, but this workspace is not connected.
	before, err := linker.WorkspaceMailboxStatus(ctx, "", "ws-1")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if before.Setup == nil || before.Setup.Ready {
		t.Fatalf("setup = %+v, want not-ready before linking", before.Setup)
	}
	if before.Setup.Reason != workspace.BlockedReasonNotLinkedToWorkspace {
		t.Fatalf("reason = %q, want not_linked_to_workspace", before.Setup.Reason)
	}

	// Linking is a plain store operation — no LLM anywhere in this path.
	after, err := linker.LinkWorkspaceMailbox(ctx, "", "ws-1", "acct-1")
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	if !after.Connected {
		t.Fatalf("status after link = %+v, want connected", after)
	}
	// FR 36: the setup state is current the moment the link completes.
	if after.Setup == nil || !after.Setup.Ready {
		t.Fatalf("setup = %+v, want ready immediately after linking", after.Setup)
	}

	// FR 37: linking must not have started anything.
	saved, _ := store.Get("ws-1")
	for _, task := range saved.Tasks {
		if task.Status == workspace.TaskStatusInProgress {
			t.Fatalf("linking started task %q; repair must never auto-execute", task.Description)
		}
	}
}
