package session

import (
	"context"
	"testing"
	"time"
)

func mustCreateWorkspace(t *testing.T, store *SQLiteStore, ctx context.Context, id, name, parentID string) {
	t.Helper()
	ws := &Workspace{
		ID:        id,
		Name:      name,
		ParentID:  parentID,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := store.CreateWorkspace(ctx, ws); err != nil {
		t.Fatalf("Failed to create workspace %s: %v", id, err)
	}
}

func TestSQLiteStore_TrashWorkspace_SoftDelete(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	store := NewSQLiteStore(db)
	ctx := context.Background()

	mustCreateWorkspace(t, store, ctx, "ws1", "WS1", "")

	if err := store.TrashWorkspace(ctx, "ws1", true); err != nil {
		t.Fatalf("TrashWorkspace failed: %v", err)
	}

	// GetWorkspace still finds it, now trashed with a deletion time.
	got, err := store.GetWorkspace(ctx, "ws1")
	if err != nil {
		t.Fatalf("GetWorkspace failed: %v", err)
	}
	if got.Status != WorkspaceStatusTrashed {
		t.Errorf("expected status %q, got %q", WorkspaceStatusTrashed, got.Status)
	}
	if got.DeletedAt == nil {
		t.Errorf("expected DeletedAt to be set")
	}

	// Excluded from the active list.
	active, err := store.ListWorkspaces(ctx)
	if err != nil {
		t.Fatalf("ListWorkspaces failed: %v", err)
	}
	if len(active) != 0 {
		t.Errorf("expected 0 active workspaces, got %d", len(active))
	}

	// Present in the trash list with its deletion time.
	trashed, err := store.ListTrashedWorkspaces(ctx)
	if err != nil {
		t.Fatalf("ListTrashedWorkspaces failed: %v", err)
	}
	if len(trashed) != 1 || trashed[0].ID != "ws1" {
		t.Fatalf("expected 1 trashed workspace ws1, got %+v", trashed)
	}
	if trashed[0].DeletedAt == nil {
		t.Errorf("expected DeletedAt in trash listing")
	}
}

func TestSQLiteStore_TrashAndRestore_Subtree(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	store := NewSQLiteStore(db)
	ctx := context.Background()

	mustCreateWorkspace(t, store, ctx, "parent", "Parent", "")
	mustCreateWorkspace(t, store, ctx, "child", "Child", "parent")

	// Trash the whole subtree.
	if err := store.TrashWorkspace(ctx, "parent", true); err != nil {
		t.Fatalf("TrashWorkspace failed: %v", err)
	}

	active, _ := store.ListWorkspaces(ctx)
	if len(active) != 0 {
		t.Errorf("expected 0 active workspaces after subtree trash, got %d", len(active))
	}
	trashed, _ := store.ListTrashedWorkspaces(ctx)
	if len(trashed) != 2 {
		t.Errorf("expected 2 trashed workspaces, got %d", len(trashed))
	}

	// The parent link is preserved so the subtree can be rebuilt on restore.
	child, err := store.GetWorkspace(ctx, "child")
	if err != nil {
		t.Fatalf("GetWorkspace(child) failed: %v", err)
	}
	if child.ParentID != "parent" {
		t.Errorf("expected child parent_id preserved as 'parent', got %q", child.ParentID)
	}

	// Restore the parent — the trashed subtree comes back active.
	if err := store.RestoreWorkspace(ctx, "parent"); err != nil {
		t.Fatalf("RestoreWorkspace failed: %v", err)
	}

	active, _ = store.ListWorkspaces(ctx)
	if len(active) != 2 {
		t.Errorf("expected 2 active workspaces after restore, got %d", len(active))
	}
	for _, id := range []string{"parent", "child"} {
		ws, err := store.GetWorkspace(ctx, id)
		if err != nil {
			t.Fatalf("GetWorkspace(%s) failed: %v", id, err)
		}
		if ws.Status != WorkspaceStatusActive {
			t.Errorf("%s: expected status active after restore, got %q", id, ws.Status)
		}
		if ws.DeletedAt != nil {
			t.Errorf("%s: expected DeletedAt cleared after restore", id)
		}
	}
}

func TestSQLiteStore_TrashGroupOnly_ReparentsChildren(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	store := NewSQLiteStore(db)
	ctx := context.Background()

	mustCreateWorkspace(t, store, ctx, "group", "Group", "")
	mustCreateWorkspace(t, store, ctx, "child", "Child", "group")

	// "Trash group only": children move to root, then the group is trashed.
	if err := store.ReparentChildrenToRoot(ctx, "group"); err != nil {
		t.Fatalf("ReparentChildrenToRoot failed: %v", err)
	}
	if err := store.TrashWorkspace(ctx, "group", false); err != nil {
		t.Fatalf("TrashWorkspace failed: %v", err)
	}

	// The group is trashed.
	group, err := store.GetWorkspace(ctx, "group")
	if err != nil {
		t.Fatalf("GetWorkspace(group) failed: %v", err)
	}
	if group.Status != WorkspaceStatusTrashed {
		t.Errorf("expected group trashed, got status %q", group.Status)
	}

	// The child stays active and is now at root.
	child, err := store.GetWorkspace(ctx, "child")
	if err != nil {
		t.Fatalf("GetWorkspace(child) failed: %v", err)
	}
	if child.ParentID != "" {
		t.Errorf("expected child reparented to root, got parent_id %q", child.ParentID)
	}
	if child.Status == WorkspaceStatusTrashed {
		t.Errorf("expected child to stay active, got trashed")
	}

	active, _ := store.ListWorkspaces(ctx)
	if len(active) != 1 || active[0].ID != "child" {
		t.Errorf("expected only child active, got %+v", active)
	}
	trashed, _ := store.ListTrashedWorkspaces(ctx)
	if len(trashed) != 1 || trashed[0].ID != "group" {
		t.Errorf("expected only group trashed, got %+v", trashed)
	}
}

func TestSQLiteStore_RestoreWorkspace_NotTrashedReturnsNotFound(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	store := NewSQLiteStore(db)
	ctx := context.Background()

	mustCreateWorkspace(t, store, ctx, "ws1", "WS1", "")

	// Restoring an active (non-trashed) workspace affects no rows.
	if err := store.RestoreWorkspace(ctx, "ws1"); err != ErrWorkspaceNotFound {
		t.Errorf("expected ErrWorkspaceNotFound restoring an active workspace, got %v", err)
	}
}
