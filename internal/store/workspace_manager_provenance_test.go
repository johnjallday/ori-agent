package store

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
)

// A workspace manager is bound to its workspace by provenance written with the
// definition, not by its display name. These tests pin that the binding is
// persisted, survives a reload and a rename, and is never written onto a
// profile that was not created as a manager.

func TestWorkspaceManagerProvenance_PersistsThroughReloadAndRename(t *testing.T) {
	f := newCompositeFixture(t)
	c := f.open(t)

	selected, err := c.CreateStaffingProfile(context.Background(), "House Manager", &CreateAgentConfig{
		WorkspaceManager: &agent.WorkspaceManagerProvenance{WorkspaceID: "ws-house"},
	})
	if err != nil || selected.Name != "House Manager" || selected.Witness == "" {
		t.Fatalf("CreateStaffingProfile = %#v, %v", selected, err)
	}

	reopened := f.open(t)
	ag, ok := reopened.GetAgent("House Manager")
	if !ok || !ag.WorkspaceManager.ManagesWorkspace("ws-house") {
		t.Fatalf("reloaded manager provenance = %#v", ag)
	}

	if err := reopened.RenameAgent("House Manager", "Home Lead"); err != nil {
		t.Fatalf("RenameAgent: %v", err)
	}
	renamed, ok := f.open(t).GetAgent("Home Lead")
	if !ok || !renamed.WorkspaceManager.ManagesWorkspace("ws-house") {
		t.Fatalf("a renamed manager lost its workspace binding: %#v", renamed)
	}
	if renamed.WorkspaceManager.ManagesWorkspace("ws-other") {
		t.Fatal("a manager must not report managing another workspace")
	}
}

func TestWorkspaceManagerProvenance_OrdinaryProfilesCarryNone(t *testing.T) {
	f := newCompositeFixture(t)
	c := f.open(t)
	if err := c.CreateAgent("House Manager", &CreateAgentConfig{}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	ag, ok := f.open(t).GetAgent("House Manager")
	if !ok || ag.WorkspaceManager != nil {
		t.Fatalf("a profile with a manager-like name but no create provenance got one: %#v", ag)
	}
	var nilProvenance *agent.WorkspaceManagerProvenance
	if nilProvenance.ManagesWorkspace("ws-house") || (&agent.WorkspaceManagerProvenance{}).ManagesWorkspace("") {
		t.Fatal("missing or empty provenance must manage nothing")
	}
}
