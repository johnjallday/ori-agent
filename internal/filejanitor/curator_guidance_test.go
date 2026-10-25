package filejanitor

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestCuratorGuidanceStatusUsesCurrentAssociationWithoutSettingsOrFolderIO(t *testing.T) {
	ws := newFakeWorkspaceStore("one", "two")
	w := ws.workspaces["one"]
	if _, err := w.AddInstalledCapability(workspace.InstalledCapability{ID: workspace.CapabilityFileJanitor, Version: 1, Source: workspace.InstallSourceInPlace}); err != nil {
		t.Fatal(err)
	}
	w.AgentInstances = []workspace.AgentInstance{{ID: "curator", Name: "Renamed", NodeID: "helper"}}
	w.RecordCapabilityResource(workspace.CapabilityFileJanitor, workspace.CapabilityResource{Kind: workspace.ResourceCompanionAgent, ID: "curator", Shared: true})
	// No settings store or scanner is available. Guidance is metadata-only.
	s := NewService(nil, ws)
	got := s.CuratorGuidance("one")
	if !got.Bundled || !got.Available || got.AgentInstanceID != "curator" {
		t.Fatalf("status=%+v", got)
	}
	if other := s.CuratorGuidance("two"); other.Available {
		t.Fatal("unrelated workspace received guidance")
	}
	w.AgentInstances = nil
	if s.CuratorGuidance("one").Available {
		t.Fatal("stale association reported guidance available")
	}
	w.AgentInstances = []workspace.AgentInstance{{ID: "unrelated", Name: "File Curator"}}
	got = s.CuratorGuidance("one")
	if got.Available || len(got.Candidates) != 1 || got.Candidates[0].ID != "unrelated" {
		t.Fatalf("recovery=%+v", got)
	}
}
