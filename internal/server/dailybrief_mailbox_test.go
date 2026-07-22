package server

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/vault"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// briefMailboxHarness wires a dailyBriefMailboxSource with a designated HQ and a
// folder-store workspace store the resolver and binding reads share.
func briefMailboxHarness(t *testing.T, wstore workspace.Store) *dailyBriefMailboxSource {
	t.Helper()
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
	if err := sessionStore.CreateWorkspace(ctx, &session.Workspace{
		ID: "hq-1", Name: "My HQ", Kind: session.WorkspaceKindWorkspace,
		OwnerUserID: userID, Status: session.WorkspaceStatusActive,
	}); err != nil {
		t.Fatalf("create session workspace: %v", err)
	}
	if _, err := hq.Designate(ctx, userID, "hq-1"); err != nil {
		t.Fatalf("designate: %v", err)
	}

	accounts := fakeAccounts{acc: &vault.EmailAccount{
		ID: "acct-1", Provider: vault.EmailProviderGmail, EmailAddress: "me@example.com",
	}}
	src := func() workspace.EmailOpsWorkspaceSource { return wstore }
	return newDailyBriefMailboxSource(hq, wstore, accounts, stubMailProvider{}, src)
}

func emailBindingWS(id, name string) *workspace.Workspace {
	return &workspace.Workspace{
		ID: id, Name: name,
		MCPBindings: []workspace.MCPBinding{{
			ID: "b-" + id, ServerName: "gmail", Enabled: true,
			Config: map[string]any{"account_id": "acct-1", "allowed_actions": []any{"read", "search"}},
		}},
	}
}

// TestBriefEmailSourcePrefersEmailOps: with an Email Ops workspace connected and
// the HQ having no binding, the brief resolves via Email Ops (FR18).
func TestBriefEmailSourcePrefersEmailOps(t *testing.T) {
	wstore := workspace.NewInMemoryStore()
	// HQ workspace with NO email binding.
	_ = wstore.Save(&workspace.Workspace{ID: "hq-1", Name: "My HQ", OwnerUserID: userprofile.LocalUserID, Status: workspace.StatusActive})
	// Email Ops workspace with a binding + provenance.
	eo := emailBindingWS("eo-1", "Email Ops")
	eo.OwnerUserID = userprofile.LocalUserID
	eo.Status = workspace.StatusActive
	eo.SetTemplateProvenance(&workspace.TemplateProvenance{TemplateID: workspace.EmailOpsTemplateID})
	_ = wstore.Save(eo)

	s := briefMailboxHarness(t, wstore)
	if _, err := s.BriefEmailThreads(context.Background(), userprofile.LocalUserID); err != nil {
		t.Fatalf("expected email to resolve via Email Ops, got %v", err)
	}
}

// TestBriefEmailSourceFallsBackToHQ: with no Email Ops workspace but the HQ
// carrying a legacy in-place binding, the brief still resolves via the HQ.
func TestBriefEmailSourceFallsBackToHQ(t *testing.T) {
	wstore := workspace.NewInMemoryStore()
	hq := emailBindingWS("hq-1", "My HQ")
	hq.OwnerUserID = userprofile.LocalUserID
	hq.Status = workspace.StatusActive
	_ = wstore.Save(hq) // no email-ops provenance anywhere

	s := briefMailboxHarness(t, wstore)
	if _, err := s.BriefEmailThreads(context.Background(), userprofile.LocalUserID); err != nil {
		t.Fatalf("expected legacy HQ binding fallback to resolve, got %v", err)
	}
}

// TestBriefEmailSourceNotConfigured: no Email Ops workspace and no HQ binding →
// ErrEmailNotConfigured (a clean "not set up", not a gap).
func TestBriefEmailSourceNotConfigured(t *testing.T) {
	wstore := workspace.NewInMemoryStore()
	_ = wstore.Save(&workspace.Workspace{ID: "hq-1", Name: "My HQ", OwnerUserID: userprofile.LocalUserID, Status: workspace.StatusActive})

	s := briefMailboxHarness(t, wstore)
	_, err := s.BriefEmailThreads(context.Background(), userprofile.LocalUserID)
	if err != dailybrief.ErrEmailNotConfigured {
		t.Fatalf("expected ErrEmailNotConfigured, got %v", err)
	}
}
