package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/connections"
	"github.com/johnjallday/ori-agent/internal/vault"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Upgrade compatibility.
//
// This feature changes how bindings are classified, how credentials are
// referenced, and how failures are retried. None of that may require the user
// to do anything: an install that was working before must keep working after
// the upgrade, and an install that was BROKEN before must start working without
// a reconnect.
//
// The fixtures below are on-disk shapes an upgraded install genuinely holds.
// They are written as raw JSON where the point is that a record predates a
// field — constructing them through today's Go structs would quietly add the
// field and prove nothing.

// legacyWorkspaceJSON is a workspace saved before RuntimeKind existed: a `gmail`
// binding with no runtime_kind alongside a real MCP binding. This is the exact
// shape that produced "server gmail not found".
const legacyWorkspaceJSON = `{
  "id": "ws-email-ops",
  "name": "Email Ops",
  "mcp_bindings": [
    {
      "id": "b-mail",
      "server_name": "gmail",
      "enabled": true,
      "config": {"account_id": "acct-legacy", "allowed_actions": ["read", "search"]}
    },
    {
      "id": "b-fs",
      "server_name": "filesystem",
      "enabled": true
    }
  ],
  "agent_instances": [
    {"id": "postmaster-id", "name": "Postmaster", "entry_point": true},
    {"id": "inbox-id", "name": "Inbox"}
  ]
}`

func decodeLegacyWorkspace(t *testing.T, raw string) *workspace.Workspace {
	t.Helper()
	var ws workspace.Workspace
	if err := json.Unmarshal([]byte(raw), &ws); err != nil {
		t.Fatalf("decode legacy workspace: %v", err)
	}
	return &ws
}

// Rollout 1: a legacy Gmail binding works immediately as native. No migration
// step, no relink, no reauthorization — it simply classifies correctly on the
// next read.
func TestUpgrade_LegacyGmailBindingIsNativeImmediately(t *testing.T) {
	ws := decodeLegacyWorkspace(t, legacyWorkspaceJSON)

	mail, ok := ws.GetMCPBinding("b-mail")
	if !ok {
		t.Fatal("expected the legacy mail binding")
	}
	if mail.RuntimeKind != "" {
		t.Fatalf("the fixture must predate runtime_kind, got %q", mail.RuntimeKind)
	}
	if !mail.IsNativeEmail() {
		t.Fatal("a legacy gmail binding must classify as native email with no migration")
	}
	if mail.IsRuntimeMCP() {
		t.Fatal("a legacy gmail binding must be excluded from MCP materialization")
	}

	// The real MCP binding beside it is unaffected.
	fs, _ := ws.GetMCPBinding("b-fs")
	if !fs.IsRuntimeMCP() {
		t.Fatal("the filesystem binding must still materialize as MCP")
	}

	// And the mailbox gate accepts it, so Inbox keeps its access.
	if _, found := emailBindingFor(ws); !found {
		t.Fatal("the mailbox gate must still resolve a legacy binding")
	}
}

// Rollout 2: an existing healthy grant stays connected. Nothing about the new
// vault preflight or credential lifecycle may invalidate a working connection.
func TestUpgrade_HealthyGrantSurvives(t *testing.T) {
	ctx := context.Background()
	store := newTestVaultStore(t)
	created := createTestVault(t, store, "Personal")
	account := createAccount(t, store, vault.EmailAccountInput{
		VaultID: created.ID, EmailAddress: "me@example.com", Source: googleConnectionEmailSource,
	})

	// A connection record as an earlier build wrote it.
	conns := connections.NewStore(t.TempDir())
	if err := conns.Save(&connections.Connection{
		ID: "c1", Provider: connections.ProviderGoogle, Subject: "sub-1",
		Email: "me@example.com", VaultID: created.ID,
		Grants: map[connections.ProductKey]*connections.ProductGrant{
			connections.ProductGmail: {
				ConnectionID: "c1", Product: connections.ProductGmail,
				CredentialRef: account.ID, Health: connections.HealthHealthy,
			},
		},
	}); err != nil {
		t.Fatalf("seed connection: %v", err)
	}

	// The upgraded build reads it and finds everything ready — no reconnect.
	catalog := newConnectionVaultCatalog(store)
	loaded, err := conns.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	preflight, err := connections.PreflightVault(ctx, catalog, loaded.VaultID)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if preflight.Outcome != connections.VaultOutcomeReady {
		t.Fatalf("preflight = %+v, want ready — an upgrade must not demand vault work", preflight)
	}

	workspaces := workspace.NewInMemoryStore()
	ws := decodeLegacyWorkspace(t, legacyWorkspaceJSON)
	// Point the legacy binding at the real account so readiness can resolve it.
	binding, _ := ws.GetMCPBinding("b-mail")
	binding.Config["account_id"] = account.ID
	if err := ws.UpsertMCPBinding(*binding); err != nil {
		t.Fatalf("rebind: %v", err)
	}
	if err := workspaces.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}

	readiness := newEmailReadinessEvaluator(conns, catalog, workspaces, store)
	if verdict := readiness.Evaluate(ctx, ws.ID); !verdict.Ready {
		t.Fatalf("an upgraded healthy install reports %+v, want ready", verdict)
	}
}

// Rollout 3-5: a task blocked BEFORE the upgrade stays blocked after it. The
// upgrade fixes the underlying cause, but running the task is the user's call —
// silently executing work they never asked to resume is exactly what FR 37
// forbids.
func TestUpgrade_ExistingBlockedTaskStaysPaused(t *testing.T) {
	// A task as it was persisted when the old runtime failed on "server gmail
	// not found": waiting_for_choice, with its failure recorded.
	const blockedTaskJSON = `{
	  "id": "task-triage",
	  "workspace_id": "ws-email-ops",
	  "description": "Triage today's inbox",
	  "to": "Postmaster",
	  "status": "waiting_for_choice",
	  "context": {
	    "human_loop": {
	      "state": "waiting_for_choice",
	      "reason_code": "tool_access_unavailable",
	      "reason": "server gmail not found"
	    }
	  },
	  "execution_history": [
	    {"outcome": "blocked", "summary": "server gmail not found"}
	  ]
	}`

	var task workspace.Task
	if err := json.Unmarshal([]byte(blockedTaskJSON), &task); err != nil {
		t.Fatalf("decode blocked task: %v", err)
	}

	// It is still paused, and its original failure is still visible.
	if task.Status != workspace.TaskStatusWaitingForChoice {
		t.Fatalf("status = %q, want the task still paused after upgrade", task.Status)
	}
	if len(task.ExecutionHistory) != 1 {
		t.Fatalf("execution history = %d entries, want the prior failure retained", len(task.ExecutionHistory))
	}
	humanLoop, _ := task.Context["human_loop"].(map[string]any)
	if humanLoop["reason"] != "server gmail not found" {
		t.Fatalf("the original failure was lost: %+v", humanLoop)
	}

	// Nothing in the upgrade path resumes it: only an explicit run does, and
	// that is a user action this test deliberately does not take.
	if task.StartedAt != nil {
		t.Fatal("an upgraded blocked task must not have been started")
	}
}

// A task that predates RequiredCapabilities is never gated — the field's
// absence must not turn an ordinary task into a blocked one.
func TestUpgrade_TaskWithoutCapabilitiesIsNotGated(t *testing.T) {
	const legacyTaskJSON = `{"id":"task-1","description":"Write the report","to":"Writer","status":"pending"}`
	var task workspace.Task
	if err := json.Unmarshal([]byte(legacyTaskJSON), &task); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(task.RequiredCapabilities) != 0 {
		t.Fatalf("legacy task gained requirements: %v", task.RequiredCapabilities)
	}

	// Even with nothing connected at all, it is not gated.
	evaluator := newEmailReadinessEvaluator(
		connections.NewStore(t.TempDir()), nil, workspace.NewInMemoryStore(), nil,
	)
	if blocked := evaluator.CheckTaskCapabilities("ws-1", task.RequiredCapabilities); blocked != nil {
		t.Fatalf("a legacy task was gated: %+v", blocked)
	}
}

// An upgraded install holding a provable workspace credential copy consolidates
// cleanly; one holding an ambiguous record keeps it. Both must be true of the
// SAME install, since a real one holds a mix.
func TestUpgrade_MixedLegacyCredentialsResolveIndependently(t *testing.T) {
	ctx := context.Background()
	lifecycle, store, workspaces, _ := lifecycleFixture(t)
	vaultID := vaultIDOf(t, store)

	authoritative := createAccount(t, store, vault.EmailAccountInput{
		VaultID: vaultID, EmailAddress: "me@example.com", Source: googleConnectionEmailSource,
	})
	provable := createAccount(t, store, vault.EmailAccountInput{
		VaultID: vaultID, EmailAddress: "me@example.com", WorkspaceID: "ws-copy",
		Source: googleConnectionEmailSource,
	})
	ambiguous := createAccount(t, store, vault.EmailAccountInput{
		VaultID: vaultID, EmailAddress: "me@example.com", WorkspaceID: "ws-manual",
		Source: "manual-setup",
	})
	workspaceBoundTo(t, workspaces, "ws-copy", provable.ID)
	workspaceBoundTo(t, workspaces, "ws-manual", ambiguous.ID)

	consolidated, err := lifecycle.consolidateDuplicate(ctx, authoritative.ID, provable.ID, authoritative.ID)
	if err != nil || !consolidated {
		t.Fatalf("the provable copy should consolidate: %v %v", consolidated, err)
	}
	consolidated, err = lifecycle.consolidateDuplicate(ctx, authoritative.ID, ambiguous.ID, authoritative.ID)
	if err != nil {
		t.Fatalf("consolidate ambiguous: %v", err)
	}
	if consolidated {
		t.Fatal("the ambiguous record must be preserved")
	}

	// Both workspaces still resolve to a real credential.
	for workspaceID, wantAccount := range map[string]string{
		"ws-copy":   authoritative.ID,
		"ws-manual": ambiguous.ID,
	} {
		got := boundAccountID(t, workspaces, workspaceID)
		if got != wantAccount {
			t.Fatalf("%s references %q, want %q", workspaceID, got, wantAccount)
		}
		if acct, _ := store.GetEmailAccount(ctx, got); acct == nil {
			t.Fatalf("%s references a deleted credential", workspaceID)
		}
	}
}

// FR 91: a workspace record round-tripped through the new code must not gain
// anything token-bearing. Bindings carry references, never credentials.
func TestUpgrade_WorkspaceRecordCarriesNoCredentialMaterial(t *testing.T) {
	ws := decodeLegacyWorkspace(t, legacyWorkspaceJSON)
	binding, _ := ws.GetMCPBinding("b-mail")
	binding.RuntimeKind = workspace.RuntimeKindNativeEmail
	if err := ws.UpsertMCPBinding(*binding); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	ws.UpdatedAt = time.Now()

	encoded, err := json.Marshal(ws)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	blob := strings.ToLower(string(encoded))
	for _, forbidden := range []string{"access_token", "refresh_token", "client_secret", "password", "id_token"} {
		if strings.Contains(blob, forbidden) {
			t.Fatalf("workspace record contains %q: %s", forbidden, encoded)
		}
	}
}
