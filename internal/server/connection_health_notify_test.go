package server

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/connections"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestConnectionHealthNotifier_RaisesCoalescesAndClears(t *testing.T) {
	ws := workspace.NewInMemoryStore()
	w := &workspace.Workspace{ID: "ws1", Name: "Scheduling", Status: workspace.StatusActive}
	if err := w.UpsertMCPBinding(workspace.MCPBinding{ID: "b1", ServerName: "google-calendar", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := ws.Save(w); err != nil {
		t.Fatal(err)
	}

	b := &ServerBuilder{}
	b.workspaceStore = ws
	n := connectionHealthNotifier{b: b}
	opps := workspace.NewOpportunityStore(ws)
	ctx := context.Background()

	// Unhealthy → a finding is raised in the affected workspace (FR 86).
	n.OnGrantHealthChanged(ctx, connections.ProductCalendar, "google-calendar", connections.HealthReconnectRequired)
	if list, _ := opps.List("ws1"); len(list) != 1 {
		t.Fatalf("expected 1 opportunity after unhealthy, got %d", len(list))
	}

	// Repeated unhealthy reconcile coalesces (dedup by title) — still one.
	n.OnGrantHealthChanged(ctx, connections.ProductCalendar, "google-calendar", connections.HealthReconnectRequired)
	if list, _ := opps.List("ws1"); len(list) != 1 {
		t.Errorf("expected coalesced to 1, got %d", len(list))
	}

	// Recovery → the finding is cleared.
	n.OnGrantHealthChanged(ctx, connections.ProductCalendar, "google-calendar", connections.HealthHealthy)
	if list, _ := opps.List("ws1"); len(list) != 0 {
		t.Errorf("expected finding cleared on recovery, got %d", len(list))
	}
}

func TestConnectionHealthNotifier_NoAffectedWorkspacesNoOp(t *testing.T) {
	ws := workspace.NewInMemoryStore()
	w := &workspace.Workspace{ID: "ws1", Name: "Other", Status: workspace.StatusActive} // no google-calendar binding
	if err := ws.Save(w); err != nil {
		t.Fatal(err)
	}
	b := &ServerBuilder{}
	b.workspaceStore = ws
	n := connectionHealthNotifier{b: b}

	n.OnGrantHealthChanged(context.Background(), connections.ProductCalendar, "google-calendar", connections.HealthReconnectRequired)
	if list, _ := workspace.NewOpportunityStore(ws).List("ws1"); len(list) != 0 {
		t.Errorf("workspace not using the product should get no finding, got %d", len(list))
	}
}
