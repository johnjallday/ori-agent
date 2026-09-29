package session

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func newVersionedAdapter(t *testing.T) (*WorkspaceStoreAdapter, HybridStore) {
	t.Helper()
	db, err := database.Open(context.Background(), &database.Config{InMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := NewHybridStoreWithDB(db, 10)
	return NewWorkspaceStoreAdapter(store), store
}

// The seeded local user is the only owner the workspaces table's foreign key
// accepts in a fresh database.
func fencedHomeRecord(id string) *workspace.Workspace {
	ws := &workspace.Workspace{ID: id, Name: "Music Home", OwnerUserID: "local", Status: workspace.StatusActive, Version: 1}
	ws.SetAssistantProgramState(&workspace.AssistantProgramState{
		SchemaVersion: workspace.AssistantProgramStateSchemaVersion,
		Key:           workspace.AssistantProgramKey{OwnerUserID: "local", ProgramID: "music"},
	})
	return ws
}

func TestWorkspaceStoreAdapter_SaveExpectingIsOneConditionalStatement(t *testing.T) {
	adapter, _ := newVersionedAdapter(t)
	home := fencedHomeRecord("home-cas")
	if err := adapter.Save(home); err != nil {
		t.Fatal(err)
	}
	next, err := adapter.Get(home.ID)
	if err != nil {
		t.Fatal(err)
	}
	next.Version, next.Description = 2, "first writer"
	if err := adapter.SaveExpecting(next, 1); err != nil {
		t.Fatalf("expected version 1 refused: %v", err)
	}
	stale, err := adapter.Get(home.ID)
	if err != nil {
		t.Fatal(err)
	}
	stale.Version, stale.Description = 2, "stale writer"
	err = adapter.SaveExpecting(stale, 1)
	if !errors.Is(err, workspace.ErrStaleWorkspaceVersion) || !errors.Is(err, ErrWorkspaceVersionConflict) {
		t.Fatalf("stale expectation was not refused as a version conflict: %v", err)
	}
	got, err := adapter.Get(home.ID)
	if err != nil || got.Description != "first writer" || got.Version != 2 {
		t.Fatalf("conflicting write changed the row: %+v %v", got, err)
	}
}

func TestSQLiteStore_GenericUpdateRefusesStaleProtectedRowOnly(t *testing.T) {
	adapter, store := newVersionedAdapter(t)
	ctx := context.Background()
	home := fencedHomeRecord("home-generic")
	if err := adapter.Save(home); err != nil {
		t.Fatal(err)
	}
	plain := &workspace.Workspace{ID: "plain-generic", Name: "Plain", OwnerUserID: "local", Status: workspace.StatusActive, Version: 1}
	if err := adapter.Save(plain); err != nil {
		t.Fatal(err)
	}
	// A fenced writer moves both records to version 2.
	for _, id := range []string{home.ID, plain.ID} {
		current, err := adapter.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		current.Version = 2
		if err := adapter.SaveExpecting(current, 1); err != nil {
			t.Fatal(err)
		}
	}
	// A generic session-level writer still holding version 1 of the Home is
	// refused; the same stale write to an ordinary row keeps the old behavior.
	staleHome := adapter.toSessionWorkspace(home)
	staleHome.Description = "stale generic Home write"
	if err := store.UpdateWorkspace(ctx, staleHome); !errors.Is(err, ErrWorkspaceVersionConflict) {
		t.Fatalf("stale generic Home update was not refused: %v", err)
	}
	stalePlain := adapter.toSessionWorkspace(plain)
	stalePlain.Description = "stale generic plain write"
	if err := store.UpdateWorkspace(ctx, stalePlain); err != nil {
		t.Fatalf("ordinary row lost its unconditional update: %v", err)
	}
	gotHome, err := adapter.Get(home.ID)
	if err != nil || gotHome.Description != "" || gotHome.Version != 2 {
		t.Fatalf("protected row changed by a stale generic write: %+v %v", gotHome, err)
	}
	gotPlain, err := adapter.Get(plain.ID)
	if err != nil || gotPlain.Description != "stale generic plain write" {
		t.Fatalf("ordinary row did not accept its update: %+v %v", gotPlain, err)
	}
	if _, err := store.GetWorkspace(ctx, "missing-row"); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("missing row lookup changed: %v", err)
	}
}
