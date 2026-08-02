package workspacerun

import (
	"context"
	"testing"
)

// Durable snapshot/Wrap-up persistence (task 5.16; PRD FR-107, FR-114).
//
// The memory store proves the shape; this proves the SQLite columns actually
// carry it — the layer where the earlier workspace fields were silently lost.

func TestSQLiteStore_SnapshotAndWrapUpRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t, ctx)
	createTestWorkspace(t, ctx, db, "ws-1")
	store := NewSQLiteStore(db)

	run := &Run{ID: "run-1", WorkspaceID: "ws-1", ProfileID: "p", ProfileVersion: "1", Prompt: "do the thing"}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}

	snapshot := testSnapshot()
	if err := store.SetToolboxSnapshot(ctx, "ws-1", "run-1", *snapshot); err != nil {
		t.Fatalf("SetToolboxSnapshot() error = %v", err)
	}
	wrapUp := BuildToolboxWrapUp("run-1", snapshot, []TraceEvent{toolCall(1, "read_note")}, nil, 1500)
	if err := store.SetToolboxWrapUp(ctx, "ws-1", "run-1", *wrapUp); err != nil {
		t.Fatalf("SetToolboxWrapUp() error = %v", err)
	}

	stored, err := store.GetRun(ctx, "ws-1", "run-1")
	if err != nil {
		t.Fatalf("GetRun() error = %v", err)
	}
	if stored.ToolboxSnapshot == nil {
		t.Fatalf("expected the snapshot to survive SQLite")
	}
	if stored.ToolboxSnapshot.Hash != snapshot.Hash {
		t.Fatalf("snapshot hash = %q, want %q", stored.ToolboxSnapshot.Hash, snapshot.Hash)
	}
	// The exact operations are what FR-112 enforces against, so they have to
	// come back exactly.
	if !stored.ToolboxSnapshot.AllowsTool("ws:ws-1:mcp:notes:mb-notes", "write_note") {
		t.Fatalf("expected the exact allowlist to survive, got %+v", stored.ToolboxSnapshot.MCPBindings)
	}
	if stored.ToolboxSnapshot.ToolboxVersion != 3 || stored.ToolboxSnapshot.WorkspaceVersion != 12 {
		t.Fatalf("expected the pinned versions to survive, got %+v", stored.ToolboxSnapshot)
	}
	if len(stored.ToolboxSnapshot.Skills) != 2 {
		t.Fatalf("expected the effective skills to survive, got %+v", stored.ToolboxSnapshot.Skills)
	}

	if stored.ToolboxWrapUp == nil || stored.ToolboxWrapUp.TotalToolCalls != 1 {
		t.Fatalf("expected the wrap-up to survive, got %+v", stored.ToolboxWrapUp)
	}
	if len(stored.ToolboxWrapUp.SkillObservations) != 2 {
		t.Fatalf("expected the skill observations to survive, got %+v", stored.ToolboxWrapUp.SkillObservations)
	}
	if len(stored.ToolboxWrapUp.UnusedOperations) != 2 {
		t.Fatalf("expected the unused operations to survive, got %v", stored.ToolboxWrapUp.UnusedOperations)
	}
}

// A run created before this feature reads back with neither field set —
// migration 037 is additive, and NULL means "predates snapshots" rather than
// "had no capabilities".
func TestSQLiteStore_RunWithoutSnapshotReadsBackNil(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t, ctx)
	createTestWorkspace(t, ctx, db, "ws-1")
	store := NewSQLiteStore(db)

	if err := store.CreateRun(ctx, &Run{
		ID: "run-old", WorkspaceID: "ws-1", ProfileID: "p", ProfileVersion: "1", Prompt: "legacy",
	}); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}

	stored, err := store.GetRun(ctx, "ws-1", "run-old")
	if err != nil {
		t.Fatalf("GetRun() error = %v", err)
	}
	if stored.ToolboxSnapshot != nil || stored.ToolboxWrapUp != nil {
		t.Fatalf("expected a historical run to carry neither, got %+v / %+v",
			stored.ToolboxSnapshot, stored.ToolboxWrapUp)
	}
}

// The snapshot may be written at creation time as well as via the setter, so
// both paths must carry it.
func TestSQLiteStore_SnapshotSurvivesCreateRun(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t, ctx)
	createTestWorkspace(t, ctx, db, "ws-1")
	store := NewSQLiteStore(db)

	snapshot := testSnapshot()
	if err := store.CreateRun(ctx, &Run{
		ID: "run-2", WorkspaceID: "ws-1", ProfileID: "p", ProfileVersion: "1", Prompt: "x",
		ToolboxSnapshot: snapshot,
	}); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}

	stored, err := store.GetRun(ctx, "ws-1", "run-2")
	if err != nil {
		t.Fatalf("GetRun() error = %v", err)
	}
	if stored.ToolboxSnapshot == nil || stored.ToolboxSnapshot.Hash != snapshot.Hash {
		t.Fatalf("expected a snapshot supplied at creation to survive, got %+v", stored.ToolboxSnapshot)
	}
}
