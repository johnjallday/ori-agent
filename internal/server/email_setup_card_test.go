package server

import (
	"context"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/emailsetup"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/progression"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/vault"
)

// newEmailSetupTestBuilder builds a real server whose secret store is a file
// in the test's temp directory. A HOME override does not isolate the macOS
// Keychain, and the setup card writes to it.
func newEmailSetupTestBuilder(t *testing.T) *ServerBuilder {
	t.Helper()
	t.Setenv("ORI_DISABLE_NATIVE_SECRET_STORE", "1")
	t.Setenv("ORI_VAULT_PASSPHRASE", "email-setup-test-passphrase")
	return newBuiltTestBuilder(t)
}

// The card's whole server path on a built server: Email Ops is created from its
// blueprint, a password account is linked, and every surface that asks "is email
// working?" agrees it is.
func TestEmailSetupCardCreatesEmailOpsAndLinksAPasswordAccount(t *testing.T) {
	b := newEmailSetupTestBuilder(t)
	ctx := context.Background()
	if b.emailSetupHandler == nil || b.vaultKeyring == nil {
		t.Fatal("the setup card was not wired")
	}

	vaultID, err := b.vaultKeyring.EnsureVault(ctx, "Email")
	if err != nil {
		t.Fatalf("EnsureVault: %v", err)
	}
	account, err := b.vaultStore.CreateEmailAccount(ctx, vault.EmailAccountInput{
		VaultID: vaultID, Source: emailsetup.AccountSource,
		Provider: vault.EmailProviderIMAPSMTP, AuthType: vault.EmailAuthTypeAppPassword,
		EmailAddress: "me@fastmail.example", IMAPHost: "imap.fastmail.com", SMTPHost: "smtp.fastmail.com",
		Credentials: vault.EmailAccountCredentials{Password: "app-password"},
	})
	if err != nil {
		t.Fatalf("CreateEmailAccount: %v", err)
	}

	home := emailSetupHome{b: b}
	ws, err := home.EnsureEmailOps(ctx, userprofile.LocalUserID)
	if err != nil {
		t.Fatalf("EnsureEmailOps: %v", err)
	}
	if !ws.Created || ws.Name != "Email Ops" || !strings.HasPrefix(ws.Route, "/workspaces/") {
		t.Fatalf("workspace = %+v", ws)
	}
	again, err := home.EnsureEmailOps(ctx, userprofile.LocalUserID)
	if err != nil || again.ID != ws.ID || again.Created {
		t.Fatalf("second EnsureEmailOps = %+v, %v; want the same workspace, not a new one", again, err)
	}

	created, err := b.workspaceFileStore.Get(ws.ID)
	if err != nil {
		t.Fatalf("load Email Ops: %v", err)
	}
	var names []string
	for _, inst := range created.AgentInstances {
		names = append(names, inst.Name)
	}
	if !strings.Contains(strings.Join(names, ","), "Inbox") {
		t.Fatalf("agents = %v; mail access is granted to the agent named Inbox", names)
	}

	if err := home.LinkMailbox(ctx, userprofile.LocalUserID, ws.ID, account.ID); err != nil {
		t.Fatalf("LinkMailbox: %v", err)
	}
	if readiness := b.emailReadiness.Evaluate(ctx, ws.ID); !readiness.Ready || readiness.EmailAddress != "me@fastmail.example" {
		t.Fatalf("readiness = %+v, want ready with no Google connection at all", readiness)
	}
	if !b.mailboxAccess.CanAccess(ws.ID, "Inbox") {
		t.Fatal("the Inbox agent cannot read the linked mailbox")
	}

	// #441: the Home card judges the Email Ops workspace, not the HQ it is given.
	card := personalAssistantEmailCapability{readiness: b.emailReadiness, emailOps: b.workspaceFileStore}
	if got := card.EmailCapability(ctx, "some-hq-workspace"); got.Status != personalassistant.CapabilityAvailable || got.Route != ws.Route {
		t.Fatalf("Home email card = %+v, want available, linking to Email Ops", got)
	}

	if b.progressionEngine != nil && !b.progressionEngine.HasCompleted(progression.ConnectSourceQuestID) {
		t.Fatal("connecting email did not complete the connect-a-source mission")
	}
}

// After a restart every vault is locked; the one the card made must open again
// by itself, or the morning brief could not read mail.
func TestEmailSetupVaultUnlocksAfterRestart(t *testing.T) {
	ctx := context.Background()
	b := newEmailSetupTestBuilder(t)
	vaultID, err := b.vaultKeyring.EnsureVault(ctx, "Email")
	if err != nil {
		t.Fatalf("EnsureVault: %v", err)
	}
	if err := b.vaultStore.Lock(ctx, vaultID); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	// What startup does.
	b.wireEmailSetup(b.vaultStore)
	status, err := b.vaultStore.Status(ctx, vaultID)
	if err != nil {
		t.Fatalf("VaultStatus: %v", err)
	}
	if status.Locked {
		t.Fatal("the email vault is still locked after startup")
	}
}
