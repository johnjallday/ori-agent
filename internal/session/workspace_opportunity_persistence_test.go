package session

import (
	"context"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// TestWorkspaceOpportunityPersistence is a regression test for the Action Center
// opportunity-persistence gap: the session adapter previously never serialized
// Workspace.Opportunities, so mission findings were dropped on every read and
// lost on restart. They must now round-trip through Save/Get, survive an Upsert
// (the mission/Action Center write path), and persist to the DB.
func TestWorkspaceOpportunityPersistence(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{InMemory: true})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	store := NewHybridStoreWithDB(db, 50)
	defer func() { _ = store.Close() }()
	adapter := NewWorkspaceStoreAdapter(store)

	now := time.Now().UTC().Truncate(time.Second)
	wsID := "ws-opp-test"
	ws := &workspace.Workspace{
		ID:         wsID,
		Name:       "Opp Test",
		Status:     workspace.StatusActive,
		SharedData: map[string]any{},
		CreatedAt:  now,
		UpdatedAt:  now,
		Opportunities: []workspace.Opportunity{
			{ID: "opp-1", WorkspaceID: wsID, Title: "Blog post still in draft", Priority: "high", Confidence: "high", Status: workspace.OpportunityNew, CreatedAt: now, UpdatedAt: now},
			{ID: "opp-2", WorkspaceID: wsID, Title: "Pricing mismatch", Priority: "medium", Confidence: "high", Status: workspace.OpportunityNew, CreatedAt: now, UpdatedAt: now},
		},
	}

	if err := adapter.Save(ws); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Save -> Get must preserve opportunities (this is the core of the bug).
	got, err := adapter.Get(wsID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Opportunities) != 2 {
		t.Fatalf("after Save/Get: expected 2 opportunities, got %d", len(got.Opportunities))
	}
	if got.Opportunities[0].Title != "Blog post still in draft" || got.Opportunities[0].Priority != "high" {
		t.Fatalf("opportunity fields not preserved: %+v", got.Opportunities[0])
	}

	// Upsert via the OpportunityStore (the mission / Action Center write path,
	// which goes Get -> mutate -> Save through the adapter). It must add the new
	// finding without clobbering the existing two.
	oppStore := workspace.NewOpportunityStore(adapter)
	if _, _, err := oppStore.Upsert(workspace.Opportunity{
		WorkspaceID: wsID, Title: "No owner for launch support", Priority: "medium", Confidence: "medium",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	opps, err := oppStore.List(wsID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(opps) != 3 {
		t.Fatalf("after Upsert: expected 3 opportunities, got %d", len(opps))
	}

	// Simulate a restart: a fresh store over the same DB has an empty cache, so
	// this Get is served from SQLite. Proves the findings persisted to disk.
	restarted := NewWorkspaceStoreAdapter(NewHybridStoreWithDB(db, 50))
	reloaded, err := restarted.Get(wsID)
	if err != nil {
		t.Fatalf("reload get: %v", err)
	}
	if len(reloaded.Opportunities) != 3 {
		t.Fatalf("after restart: expected 3 persisted opportunities, got %d", len(reloaded.Opportunities))
	}
}
