package workspace

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDedupKey_Deterministic(t *testing.T) {
	a := DedupKey("ws-1", "Brand voice drifted in 3 posts")
	b := DedupKey("ws-1", "Brand voice drifted in 3 posts")
	if a != b {
		t.Fatalf("expected DedupKey to be deterministic; got %q vs %q", a, b)
	}
}

func TestDedupKey_NormalizationCollapsesTrivialDifferences(t *testing.T) {
	// Title differences that should collapse: case, punctuation, spacing.
	canonical := DedupKey("ws-1", "Brand voice drift")
	variants := []string{
		"brand voice drift",
		"Brand-voice drift",
		"  Brand   voice drift  ",
		"BRAND, voice; drift!",
	}
	for _, v := range variants {
		if got := DedupKey("ws-1", v); got != canonical {
			t.Errorf("DedupKey(%q) = %q; want %q", v, got, canonical)
		}
	}
}

func TestDedupKey_DistinctTitlesProduceDistinctKeys(t *testing.T) {
	if DedupKey("ws-1", "Brand voice drift") == DedupKey("ws-1", "Missing alt text on hero image") {
		t.Fatal("distinct titles must produce distinct dedup keys")
	}
}

func TestDedupKey_DistinctWorkspacesProduceDistinctKeys(t *testing.T) {
	if DedupKey("ws-1", "Same title") == DedupKey("ws-2", "Same title") {
		t.Fatal("same title in different workspaces must produce distinct dedup keys")
	}
}

func newTestWorkspaceWithStore(t *testing.T) (*InMemoryStore, *Workspace) {
	t.Helper()
	store := NewInMemoryStore()
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Test"})
	if err := store.Save(ws); err != nil {
		t.Fatalf("Save: %v", err)
	}
	return store, ws
}

func TestOpportunityStore_InsertAndList(t *testing.T) {
	store, ws := newTestWorkspaceWithStore(t)
	opps := NewOpportunityStore(store)

	got, merged, err := opps.Upsert(Opportunity{
		WorkspaceID: ws.ID,
		Title:       "Brand voice drift",
		Summary:     "Three recent posts diverge in tone.",
		Priority:    "medium",
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if merged {
		t.Fatalf("expected new insert, got merge")
	}
	if got.ID == "" {
		t.Fatal("Upsert must assign an ID when none provided")
	}
	if got.Status != OpportunityNew {
		t.Errorf("default status = %q; want %q", got.Status, OpportunityNew)
	}

	list, err := opps.List(ws.ID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List returned %d items; want 1", len(list))
	}
}

func TestOpportunityStore_UpsertMergesOnDedupKey(t *testing.T) {
	store, ws := newTestWorkspaceWithStore(t)
	opps := NewOpportunityStore(store)

	first, _, err := opps.Upsert(Opportunity{
		WorkspaceID: ws.ID,
		Title:       "Brand voice drift",
		Evidence:    "Post A diverges.",
		Priority:    "low",
		Confidence:  "medium",
	})
	if err != nil {
		t.Fatalf("first Upsert: %v", err)
	}

	// Second run reports the "same" finding with different wording and a
	// higher priority. Should merge into the existing record, not create a new one.
	merged, wasMerged, err := opps.Upsert(Opportunity{
		WorkspaceID: ws.ID,
		Title:       "  Brand-voice drift!",
		Evidence:    "Post B also diverges.",
		Priority:    "high",
		Confidence:  "high",
	})
	if err != nil {
		t.Fatalf("second Upsert: %v", err)
	}
	if !wasMerged {
		t.Fatal("expected the second Upsert to merge into the first")
	}
	if merged.ID != first.ID {
		t.Errorf("merged ID = %q; want %q", merged.ID, first.ID)
	}
	if merged.Priority != "high" {
		t.Errorf("merged priority = %q; want %q (highest wins)", merged.Priority, "high")
	}
	if merged.Confidence != "high" {
		t.Errorf("merged confidence = %q; want %q (highest wins)", merged.Confidence, "high")
	}
	if !strings.Contains(merged.Evidence, "Post A diverges.") || !strings.Contains(merged.Evidence, "Post B also diverges.") {
		t.Errorf("merged evidence missing prior or new entry: %q", merged.Evidence)
	}

	list, _ := opps.List(ws.ID)
	if len(list) != 1 {
		t.Errorf("expected merge to keep 1 record; got %d", len(list))
	}
}

func TestOpportunityStore_UpsertMergePreservesUserStatus(t *testing.T) {
	store, ws := newTestWorkspaceWithStore(t)
	opps := NewOpportunityStore(store)

	first, _, err := opps.Upsert(Opportunity{
		WorkspaceID: ws.ID,
		Title:       "Brand voice drift",
		Status:      OpportunitySnoozed,
	})
	if err != nil {
		t.Fatalf("first Upsert: %v", err)
	}

	merged, wasMerged, err := opps.Upsert(Opportunity{
		WorkspaceID: ws.ID,
		Title:       "Brand voice drift",
		Evidence:    "still happening",
	})
	if err != nil {
		t.Fatalf("second Upsert: %v", err)
	}
	if !wasMerged {
		t.Fatal("expected merge")
	}
	if merged.Status != OpportunitySnoozed {
		t.Errorf("merge clobbered user status: got %q; want %q", merged.Status, OpportunitySnoozed)
	}
	if merged.ID != first.ID {
		t.Errorf("merged ID changed: %q vs %q", merged.ID, first.ID)
	}
}

func TestOpportunityStore_UpsertReopensResolvedOnRecurrence(t *testing.T) {
	store, ws := newTestWorkspaceWithStore(t)
	opps := NewOpportunityStore(store)

	first, _, err := opps.Upsert(Opportunity{WorkspaceID: ws.ID, Title: "Brand voice drift"})
	if err != nil {
		t.Fatalf("first Upsert: %v", err)
	}
	// User sees and resolves it.
	if err := opps.MarkSeen(ws.ID, first.ID); err != nil {
		t.Fatalf("MarkSeen: %v", err)
	}
	if err := opps.MarkResolved(ws.ID, first.ID); err != nil {
		t.Fatalf("MarkResolved: %v", err)
	}

	// A later run re-detects the same issue.
	merged, wasMerged, err := opps.Upsert(Opportunity{
		WorkspaceID: ws.ID,
		Title:       "Brand voice drift",
		Evidence:    "came back",
	})
	if err != nil {
		t.Fatalf("recurrence Upsert: %v", err)
	}
	if !wasMerged {
		t.Fatal("expected merge into the resolved record")
	}
	if merged.ID != first.ID {
		t.Errorf("merged ID changed: %q vs %q", merged.ID, first.ID)
	}
	if merged.Status != OpportunityNew {
		t.Errorf("recurring resolved item should re-open as new; got %q", merged.Status)
	}
	if merged.ResolvedAt != nil {
		t.Error("re-opened item should clear ResolvedAt")
	}
	if merged.SeenAt != nil {
		t.Error("re-opened item should read as unseen")
	}
	if !merged.IsOpen() {
		t.Error("re-opened item must be in the active backlog")
	}
}

func TestOpportunityStore_UpsertKeepsDismissedOnRecurrence(t *testing.T) {
	store, ws := newTestWorkspaceWithStore(t)
	opps := NewOpportunityStore(store)

	first, _, err := opps.Upsert(Opportunity{WorkspaceID: ws.ID, Title: "Off-brand emoji"})
	if err != nil {
		t.Fatalf("first Upsert: %v", err)
	}
	if err := opps.Dismiss(ws.ID, first.ID, DismissalNotUseful); err != nil {
		t.Fatalf("Dismiss: %v", err)
	}

	merged, wasMerged, err := opps.Upsert(Opportunity{
		WorkspaceID: ws.ID,
		Title:       "Off-brand emoji",
		Evidence:    "still here",
	})
	if err != nil {
		t.Fatalf("recurrence Upsert: %v", err)
	}
	if !wasMerged {
		t.Fatal("expected merge into the dismissed record")
	}
	if merged.Status != OpportunityDismissed {
		t.Errorf("an explicit dismissal must survive recurrence; got %q", merged.Status)
	}
}

func TestOpportunityStore_GetAndDelete(t *testing.T) {
	store, ws := newTestWorkspaceWithStore(t)
	opps := NewOpportunityStore(store)

	created, _, err := opps.Upsert(Opportunity{
		WorkspaceID: ws.ID,
		Title:       "Missing alt text",
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := opps.Get(ws.ID, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Title != created.Title {
		t.Errorf("Get returned wrong record: %q vs %q", got.Title, created.Title)
	}

	if err := opps.Delete(ws.ID, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := opps.Get(ws.ID, created.ID); err != ErrOpportunityNotFound {
		t.Errorf("after Delete, Get error = %v; want %v", err, ErrOpportunityNotFound)
	}
}

func TestOpportunity_IsOpen(t *testing.T) {
	cases := []struct {
		status OpportunityStatus
		open   bool
	}{
		{OpportunityNew, true},
		{OpportunitySnoozed, true},
		{OpportunityResolved, false},
		{OpportunityDismissed, false},
	}
	for _, c := range cases {
		got := Opportunity{Status: c.status}.IsOpen()
		if got != c.open {
			t.Errorf("status=%q IsOpen()=%v; want %v", c.status, got, c.open)
		}
	}
}

func TestWorkspace_MissionFields_RoundTrip(t *testing.T) {
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Brand"})
	ws.Mission = "Keep brand consistent."
	ws.MissionEnabled = true
	ws.AutonomyPolicy = AutonomyPropose
	ws.MissionExecutionCount = 3
	ws.NotificationPolicy = &NotificationPolicy{MinPriority: "medium", OnFindings: "if_any"}
	ws.Cadence = &ScheduleConfig{Type: ScheduleMonthly, DayOfMonth: 15, TimeOfDay: "09:00"}

	raw, err := json.Marshal(ws)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got Workspace
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Mission != ws.Mission {
		t.Errorf("Mission mismatch: %q vs %q", got.Mission, ws.Mission)
	}
	if !got.MissionEnabled {
		t.Error("MissionEnabled lost in round trip")
	}
	if got.AutonomyPolicy != AutonomyPropose {
		t.Errorf("AutonomyPolicy mismatch: %q vs %q", got.AutonomyPolicy, AutonomyPropose)
	}
	if got.Cadence == nil || got.Cadence.Type != ScheduleMonthly || got.Cadence.DayOfMonth != 15 {
		t.Errorf("Cadence not preserved: %+v", got.Cadence)
	}
	if got.NotificationPolicy == nil || got.NotificationPolicy.MinPriority != "medium" {
		t.Errorf("NotificationPolicy not preserved: %+v", got.NotificationPolicy)
	}
}

func TestWorkspace_LegacyJSON_LoadsWithZeroMissionFields(t *testing.T) {
	// JSON written before mission fields existed must still deserialize cleanly.
	legacy := `{"id":"ws-1","name":"Legacy","shared_data":{},"messages":[],"tasks":[],"status":"active","created_at":"2025-01-01T00:00:00Z","updated_at":"2025-01-01T00:00:00Z"}`
	var ws Workspace
	if err := json.Unmarshal([]byte(legacy), &ws); err != nil {
		t.Fatalf("Unmarshal legacy: %v", err)
	}
	if ws.MissionEnabled {
		t.Error("MissionEnabled must default to false for legacy records")
	}
	if ws.AutonomyPolicy != "" {
		t.Errorf("AutonomyPolicy = %q; want zero value", ws.AutonomyPolicy)
	}
	if ws.Cadence != nil {
		t.Errorf("Cadence = %+v; want nil", ws.Cadence)
	}
	if len(ws.Opportunities) != 0 {
		t.Errorf("Opportunities = %v; want empty", ws.Opportunities)
	}
}

func TestWorkspaceMCPBinding_SideEffectRoundTrip(t *testing.T) {
	b := MCPBinding{
		ID:                "b-1",
		ServerName:        "fs",
		Enabled:           true,
		DefaultSideEffect: SideEffectWrite,
		ToolOverrides: map[string]SideEffect{
			"read_file": SideEffectRead,
			"http_post": SideEffectExternal,
		},
	}
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got MCPBinding
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.DefaultSideEffect != SideEffectWrite {
		t.Errorf("DefaultSideEffect mismatch: %q vs %q", got.DefaultSideEffect, SideEffectWrite)
	}
	if got.ToolOverrides["read_file"] != SideEffectRead {
		t.Errorf("override for read_file lost: %q", got.ToolOverrides["read_file"])
	}
	if got.ToolOverrides["http_post"] != SideEffectExternal {
		t.Errorf("override for http_post lost: %q", got.ToolOverrides["http_post"])
	}
}
