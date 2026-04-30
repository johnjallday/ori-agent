package workspace

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
)

func TestAgentSnapshotStore_SaveSnapshotsReferencedAgents(t *testing.T) {
	primary := NewInMemoryStore()
	agents := &resolverAgentStoreStub{agents: map[string]*agent.Agent{}}
	manager := &agent.Agent{Type: agent.TypeToolCalling}
	manager.Settings.Model = "gpt-5-nano"
	agents.agents["Manager"] = manager

	store := NewAgentSnapshotStore(primary, agents)

	ws := &Workspace{
		ID:     "ws-1",
		Name:   "Snapshot",
		Status: StatusActive,
		SharedData: map[string]interface{}{
			"entry_agent_name": "Manager",
		},
		Agents: []string{"Manager"},
		AgentInstances: []AgentInstance{
			{ID: "i-1", Name: "Manager", NodeID: "manager-node-1", EntryPoint: true},
		},
	}

	if err := store.Save(ws); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, ok, err := store.GetWorkspaceAgent(ws.ID, "Manager")
	if err != nil || !ok {
		t.Fatalf("expected snapshot after Save, got ok=%v err=%v", ok, err)
	}
	if got.Settings.Model != "gpt-5-nano" {
		t.Fatalf("snapshot model=%q", got.Settings.Model)
	}
}

func TestAgentSnapshotStore_NoGlobalAgentSkipsSnapshot(t *testing.T) {
	primary := NewInMemoryStore()
	agents := &resolverAgentStoreStub{agents: map[string]*agent.Agent{}}
	store := NewAgentSnapshotStore(primary, agents)

	ws := &Workspace{
		ID:     "ws-2",
		Name:   "Empty",
		Status: StatusActive,
		Agents: []string{"Ghost"},
	}

	if err := store.Save(ws); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if _, ok, _ := store.GetWorkspaceAgent(ws.ID, "Ghost"); ok {
		t.Fatal("expected no snapshot for agent absent from global store")
	}
}

func TestSnapshotAllWorkspaces_HealsExistingWorkspaces(t *testing.T) {
	primary := NewInMemoryStore()
	manager := &agent.Agent{Type: agent.TypeToolCalling}
	manager.Settings.Model = "local-model"
	agents := &resolverAgentStoreStub{agents: map[string]*agent.Agent{"Manager": manager}}

	ws := &Workspace{
		ID:     "ws-pre-existing",
		Name:   "Legacy",
		Status: StatusActive,
		SharedData: map[string]interface{}{
			"entry_agent_name": "Manager",
		},
		Agents: []string{"Manager"},
	}
	if err := primary.Save(ws); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, ok, _ := primary.GetWorkspaceAgent(ws.ID, "Manager"); ok {
		t.Fatal("precondition: workspace must start without a snapshot")
	}

	SnapshotAllWorkspaces(primary, agents)

	if _, ok, _ := primary.GetWorkspaceAgent(ws.ID, "Manager"); !ok {
		t.Fatal("expected migration to backfill snapshot")
	}
}

func TestAgentSnapshotStore_SyncStoreWritesSnapshotToDisk(t *testing.T) {
	dir := t.TempDir()
	fileStore, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	primary := NewInMemoryStore()
	sync := NewSyncStore(primary, fileStore)

	manager := &agent.Agent{Type: agent.TypeToolCalling}
	manager.Settings.Model = "gpt-5-nano"
	agents := &resolverAgentStoreStub{agents: map[string]*agent.Agent{"Manager": manager}}

	store := NewAgentSnapshotStore(sync, agents)

	ws := &Workspace{
		ID:     "ws-disk",
		Name:   "Disk",
		Status: StatusActive,
		Agents: []string{"Manager"},
		SharedData: map[string]interface{}{
			"entry_agent_name": "Manager",
		},
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, ok, err := fileStore.GetWorkspaceAgent(ws.ID, "Manager")
	if err != nil || !ok {
		t.Fatalf("expected snapshot on disk, ok=%v err=%v", ok, err)
	}
	if got.Settings.Model != "gpt-5-nano" {
		t.Fatalf("snapshot model=%q", got.Settings.Model)
	}
}

func TestRestoreWorkspaceAgents_RegistersMissingAgents(t *testing.T) {
	primary := NewInMemoryStore()
	manager := &agent.Agent{Type: agent.TypeToolCalling}
	manager.Settings.Model = "imported-model"
	if err := primary.SaveWorkspaceAgent("ws-imported", "Manager", manager); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}
	ws := &Workspace{
		ID:     "ws-imported",
		Name:   "Imported",
		Agents: []string{"Manager"},
		SharedData: map[string]interface{}{
			"entry_agent_name": "Manager",
		},
	}
	if err := primary.Save(ws); err != nil {
		t.Fatalf("save ws: %v", err)
	}
	agents := &resolverAgentStoreStub{agents: map[string]*agent.Agent{}}

	registered, err := RestoreWorkspaceAgents(primary, ws, agents)
	if err != nil {
		t.Fatalf("RestoreWorkspaceAgents: %v", err)
	}
	if len(registered) != 1 || registered[0] != "Manager" {
		t.Fatalf("expected [Manager] registered, got %v", registered)
	}
	if got, ok := agents.GetAgent("Manager"); !ok || got.Settings.Model != "imported-model" {
		t.Fatalf("expected snapshot pushed into global store, got ok=%v ag=%+v", ok, got)
	}
}

func TestRestoreWorkspaceAgents_DoesNotOverwriteExistingGlobal(t *testing.T) {
	primary := NewInMemoryStore()
	imported := &agent.Agent{Type: agent.TypeToolCalling}
	imported.Settings.Model = "imported-model"
	if err := primary.SaveWorkspaceAgent("ws-x", "Manager", imported); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}
	ws := &Workspace{
		ID:     "ws-x",
		Name:   "X",
		Agents: []string{"Manager"},
	}
	if err := primary.Save(ws); err != nil {
		t.Fatalf("save: %v", err)
	}

	existingGlobal := &agent.Agent{Type: agent.TypeGeneral}
	existingGlobal.Settings.Model = "global-model"
	agents := &resolverAgentStoreStub{agents: map[string]*agent.Agent{"Manager": existingGlobal}}

	registered, err := RestoreWorkspaceAgents(primary, ws, agents)
	if err != nil {
		t.Fatalf("RestoreWorkspaceAgents: %v", err)
	}
	if len(registered) != 0 {
		t.Fatalf("expected no registration when global already has agent, got %v", registered)
	}
	if got, _ := agents.GetAgent("Manager"); got.Settings.Model != "global-model" {
		t.Fatalf("expected existing global preserved, got %q", got.Settings.Model)
	}
}

func TestReferencedAgentNames_Dedupes(t *testing.T) {
	ws := &Workspace{
		Agents: []string{"Manager", "manager", "Helper"},
		AgentInstances: []AgentInstance{
			{Name: "Manager"},
			{Name: " Other "},
		},
		SharedData: map[string]interface{}{
			"entry_agent_name": "Manager",
		},
	}
	got := referencedAgentNames(ws)
	if len(got) != 3 {
		t.Fatalf("expected 3 unique names, got %v", got)
	}
}
