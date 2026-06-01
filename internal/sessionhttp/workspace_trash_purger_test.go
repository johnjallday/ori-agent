package sessionhttp

import (
	"context"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/session"
)

func newPurgerTestStore(t *testing.T) (session.HybridStore, func()) {
	t.Helper()
	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{InMemory: true})
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	return session.NewHybridStoreWithDB(db, 100), func() { _ = db.Close() }
}

func trashTestWorkspace(t *testing.T, store session.HybridStore, ctx context.Context, id string) {
	t.Helper()
	ws := &session.Workspace{ID: id, Name: id, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.CreateWorkspace(ctx, ws); err != nil {
		t.Fatalf("failed to create workspace %s: %v", id, err)
	}
	if err := store.TrashWorkspace(ctx, id, true); err != nil {
		t.Fatalf("failed to trash workspace %s: %v", id, err)
	}
}

func TestTrashPurger_PurgesExpired(t *testing.T) {
	store, cleanup := newPurgerTestStore(t)
	defer cleanup()
	ctx := context.Background()

	trashTestWorkspace(t, store, ctx, "old")

	// Negative retention makes the cutoff in the future, so anything already in
	// Trash is treated as expired.
	purger := NewTrashPurger(store, nil, nil)
	purger.retention = -time.Hour
	purger.purgeExpired()

	trashed, err := store.ListTrashedWorkspaces(ctx)
	if err != nil {
		t.Fatalf("ListTrashedWorkspaces failed: %v", err)
	}
	if len(trashed) != 0 {
		t.Errorf("expected expired workspace to be purged, %d remain", len(trashed))
	}
	if _, err := store.GetWorkspace(ctx, "old"); err != session.ErrWorkspaceNotFound {
		t.Errorf("expected purged workspace to be gone, got err %v", err)
	}
}

func TestTrashPurger_KeepsWithinRetention(t *testing.T) {
	store, cleanup := newPurgerTestStore(t)
	defer cleanup()
	ctx := context.Background()

	trashTestWorkspace(t, store, ctx, "recent")

	// Default retention (30 days) — a just-trashed workspace must not be purged.
	purger := NewTrashPurger(store, nil, nil)
	purger.purgeExpired()

	trashed, err := store.ListTrashedWorkspaces(ctx)
	if err != nil {
		t.Fatalf("ListTrashedWorkspaces failed: %v", err)
	}
	if len(trashed) != 1 {
		t.Errorf("expected recently trashed workspace to be kept, got %d", len(trashed))
	}
}
