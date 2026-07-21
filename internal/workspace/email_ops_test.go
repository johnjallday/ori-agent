package workspace

import (
	"testing"
	"time"
)

func emailOpsWS(id, owner string, updated time.Time) *Workspace {
	ws := &Workspace{ID: id, Name: id, OwnerUserID: owner, Status: StatusActive, UpdatedAt: updated}
	ws.SetTemplateProvenance(&TemplateProvenance{TemplateID: EmailOpsTemplateID, Builtin: true})
	return ws
}

func TestResolveEmailOpsWorkspace_NewestOwnedActiveWins(t *testing.T) {
	store := NewInMemoryStore()
	base := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)

	// Two email-ops workspaces owned by the user; newest should win.
	_ = store.Save(emailOpsWS("eo-old", "local", base))
	_ = store.Save(emailOpsWS("eo-new", "local", base.Add(time.Hour)))

	// Decoys that must be ignored:
	//  - a non-email-ops workspace
	other := &Workspace{ID: "other", Name: "other", OwnerUserID: "local", Status: StatusActive, UpdatedAt: base.Add(2 * time.Hour)}
	_ = store.Save(other)
	//  - an email-ops workspace owned by another user
	_ = store.Save(emailOpsWS("eo-elsewhere", "someone-else", base.Add(3*time.Hour)))
	//  - an inactive (trashed) email-ops workspace, even though newest
	inactive := emailOpsWS("eo-trashed", "local", base.Add(4*time.Hour))
	inactive.Status = StatusTrashed
	_ = store.Save(inactive)

	got, err := ResolveEmailOpsWorkspace(store, "local")
	if err != nil {
		t.Fatalf("ResolveEmailOpsWorkspace: %v", err)
	}
	if got != "eo-new" {
		t.Fatalf("expected newest owned active email-ops workspace eo-new, got %q", got)
	}
}

func TestResolveEmailOpsWorkspace_NoneReturnsEmpty(t *testing.T) {
	store := NewInMemoryStore()
	_ = store.Save(&Workspace{ID: "plain", Name: "plain", OwnerUserID: "local", Status: StatusActive})

	got, err := ResolveEmailOpsWorkspace(store, "local")
	if err != nil {
		t.Fatalf("ResolveEmailOpsWorkspace: %v", err)
	}
	if got != "" {
		t.Fatalf("expected empty when no email-ops workspace exists, got %q", got)
	}
}

func TestResolveEmailOpsWorkspace_EmptyOwnerTreatedAsLocal(t *testing.T) {
	store := NewInMemoryStore()
	// Legacy record with no owner set: treated as the local single-user's.
	ws := emailOpsWS("eo-legacy", "", time.Now())
	_ = store.Save(ws)

	got, err := ResolveEmailOpsWorkspace(store, "local")
	if err != nil {
		t.Fatalf("ResolveEmailOpsWorkspace: %v", err)
	}
	if got != "eo-legacy" {
		t.Fatalf("expected empty-owner email-ops workspace to resolve, got %q", got)
	}
}
