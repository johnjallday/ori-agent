package workspace

import (
	"path/filepath"
	"testing"
)

// TestAgentWorkspaceMemberships covers shared definitions across multiple
// workspaces, entry-point flagging, within-workspace dedup, and exclusion of
// trashed/missing workspaces (PRD FR1/FR2).
func TestAgentWorkspaceMemberships(t *testing.T) {
	store, err := NewFileStore(filepath.Join(t.TempDir(), "workspaces"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	// "Shared" is the entry agent of two workspaces (and also listed as an
	// instance in Alpha to exercise within-workspace dedup). "Solo" is a
	// specialist in Alpha only.
	alpha := &Workspace{
		ID:   "ws-alpha",
		Name: "Alpha",
		AgentInstances: []AgentInstance{
			{ID: "a1", Name: "Shared", EntryPoint: true},
			{ID: "a2", Name: "Solo"},
		},
		SharedData: map[string]any{"entry_agent_name": "Shared"},
	}
	beta := &Workspace{
		ID:   "ws-beta",
		Name: "Beta",
		AgentInstances: []AgentInstance{
			{ID: "b1", Name: "Shared", EntryPoint: true},
		},
		SharedData: map[string]any{"entry_agent_name": "Shared"},
	}
	trashed := &Workspace{
		ID:             "ws-trashed",
		Name:           "Trashed",
		Status:         StatusTrashed,
		AgentInstances: []AgentInstance{{ID: "t1", Name: "Ghost"}},
	}
	missing := &Workspace{
		ID:             "ws-missing",
		Name:           "Missing",
		Status:         StatusMissing,
		AgentInstances: []AgentInstance{{ID: "m1", Name: "Ghost"}},
	}
	for _, ws := range []*Workspace{alpha, beta, trashed, missing} {
		if err := store.Save(ws); err != nil {
			t.Fatalf("Save %s: %v", ws.ID, err)
		}
	}

	m := AgentWorkspaceMemberships(store)

	shared, ok := m["shared"]
	if !ok {
		t.Fatalf("expected 'shared' membership, got %+v", m)
	}
	if shared.Count != 2 || len(shared.Workspaces) != 2 {
		t.Fatalf("expected Shared in 2 workspaces, got count=%d refs=%+v", shared.Count, shared.Workspaces)
	}
	// Name-sorted: Alpha before Beta, both entry points.
	if shared.Workspaces[0].ID != "ws-alpha" || shared.Workspaces[1].ID != "ws-beta" {
		t.Errorf("expected name-sorted [ws-alpha, ws-beta], got %+v", shared.Workspaces)
	}
	for _, ref := range shared.Workspaces {
		if !ref.EntryPoint {
			t.Errorf("expected Shared to be entry_point in %s", ref.ID)
		}
	}

	solo, ok := m["solo"]
	if !ok {
		t.Fatalf("expected 'solo' membership")
	}
	if solo.Count != 1 || solo.Workspaces[0].EntryPoint {
		t.Errorf("expected Solo attached to 1 workspace as non-entry, got %+v", solo)
	}

	if _, ok := m["ghost"]; ok {
		t.Errorf("expected 'ghost' to be excluded (trashed + missing workspaces), got %+v", m["ghost"])
	}
}

// TestAgentWorkspaceMemberships_NilStore verifies a nil store yields an empty,
// non-nil map.
func TestAgentWorkspaceMemberships_NilStore(t *testing.T) {
	if m := AgentWorkspaceMemberships(nil); m == nil || len(m) != 0 {
		t.Fatalf("expected empty non-nil map, got %+v", m)
	}
}
