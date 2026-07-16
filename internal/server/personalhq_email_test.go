package server

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/vault"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestPersonalHQMailboxLinkStatusUnlink(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatalf("database.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	profiles := userprofile.NewSQLiteStore(db)
	sessionStore := session.NewSQLiteStore(db)
	hq := personalhq.NewService(profiles, sessionStore)

	userID := userprofile.LocalUserID
	if err := profiles.Upsert(ctx, &userprofile.UserProfile{ID: userID}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	sws := &session.Workspace{ID: "hq-1", Name: "My HQ", Kind: session.WorkspaceKindWorkspace, OwnerUserID: userID, Status: session.WorkspaceStatusActive}
	if err := sessionStore.CreateWorkspace(ctx, sws); err != nil {
		t.Fatalf("create session workspace: %v", err)
	}
	if _, err := hq.Designate(ctx, userID, "hq-1"); err != nil {
		t.Fatalf("designate: %v", err)
	}

	// The folder-store workspace.Workspace the linker mutates (same ID).
	wstore := workspace.NewInMemoryStore()
	if err := wstore.Save(&workspace.Workspace{ID: "hq-1", Name: "My HQ"}); err != nil {
		t.Fatalf("save workspace: %v", err)
	}

	accounts := fakeAccounts{acc: &vault.EmailAccount{
		ID: "acct-1", Provider: vault.EmailProviderGmail, EmailAddress: "me@example.com",
		CredentialsStatus: vault.EmailAccountSecretState{HasRefreshToken: true},
	}}

	linker := newPersonalHQMailboxLinker(hq, wstore, accounts, nil)

	// Initially not connected.
	st, err := linker.MailboxStatus(ctx, userID)
	if err != nil || st.Connected {
		t.Fatalf("expected disconnected, got %+v err=%v", st, err)
	}

	// Link the account.
	st, err = linker.LinkMailbox(ctx, userID, "acct-1")
	if err != nil {
		t.Fatalf("LinkMailbox: %v", err)
	}
	if !st.Connected || st.EmailAddress != "me@example.com" || st.Health != "healthy" {
		t.Fatalf("unexpected linked status: %+v", st)
	}
	ws, _ := wstore.Get("hq-1")
	if _, ok := emailBindingFor(ws); !ok {
		t.Fatal("expected an email binding after linking")
	}

	// Re-link is idempotent (updates in place, no second binding).
	if _, err := linker.LinkMailbox(ctx, userID, "acct-1"); err != nil {
		t.Fatalf("re-link: %v", err)
	}
	ws, _ = wstore.Get("hq-1")
	emailBindings := 0
	for _, b := range ws.MCPBindings {
		if isEmailServerName(b.ServerName) {
			emailBindings++
		}
	}
	if emailBindings != 1 {
		t.Fatalf("expected exactly one email binding after re-link, got %d", emailBindings)
	}

	// Unlink removes it.
	st, err = linker.UnlinkMailbox(ctx, userID)
	if err != nil || st.Connected {
		t.Fatalf("expected disconnected after unlink, got %+v err=%v", st, err)
	}
	ws, _ = wstore.Get("hq-1")
	if _, ok := emailBindingFor(ws); ok {
		t.Fatal("email binding should be gone after unlink")
	}
}

func TestPersonalHQMailboxLinkRejectsUnknownAccount(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatalf("database.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	profiles := userprofile.NewSQLiteStore(db)
	sessionStore := session.NewSQLiteStore(db)
	hq := personalhq.NewService(profiles, sessionStore)
	userID := userprofile.LocalUserID
	_ = profiles.Upsert(ctx, &userprofile.UserProfile{ID: userID})
	_ = sessionStore.CreateWorkspace(ctx, &session.Workspace{ID: "hq-1", Name: "HQ", Kind: session.WorkspaceKindWorkspace, OwnerUserID: userID, Status: session.WorkspaceStatusActive})
	_, _ = hq.Designate(ctx, userID, "hq-1")
	wstore := workspace.NewInMemoryStore()
	_ = wstore.Save(&workspace.Workspace{ID: "hq-1"})

	linker := newPersonalHQMailboxLinker(hq, wstore, fakeAccounts{acc: nil}, nil)
	if _, err := linker.LinkMailbox(ctx, userID, "nope"); err == nil {
		t.Fatal("expected an error linking an unknown account")
	}
}
