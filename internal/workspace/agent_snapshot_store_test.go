package workspace

import (
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/types"
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
		SharedData: map[string]any{
			"entry_agent_name": "Manager",
		},
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
		ID:             "ws-2",
		Name:           "Empty",
		Status:         StatusActive,
		AgentInstances: AgentInstancesFromNames("Ghost"),
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
		SharedData: map[string]any{
			"entry_agent_name": "Manager",
		},
		AgentInstances: AgentInstancesFromNames("Manager"),
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
		ID:             "ws-disk",
		Name:           "Disk",
		Status:         StatusActive,
		AgentInstances: AgentInstancesFromNames("Manager"),
		SharedData: map[string]any{
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
		ID:             "ws-imported",
		Name:           "Imported",
		AgentInstances: AgentInstancesFromNames("Manager"),
		SharedData: map[string]any{
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

func TestRestoreAllWorkspaceAgents_RestoresSnapshotsFromLoadedFileStore(t *testing.T) {
	dir := t.TempDir()
	fileStore, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	ws := &Workspace{
		ID:         "ws-pollen",
		Name:       "Pollen",
		FolderSlug: "pollen",
		SharedData: map[string]any{
			"entry_agent_name": "Pollen Manager",
		},
		AgentInstances: []AgentInstance{
			{ID: "pollen-manager-1", Name: "Pollen Manager", NodeID: "pollen-manager-node-1", EntryPoint: true},
		},
	}
	if err := fileStore.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}
	manager := &agent.Agent{Type: agent.TypeToolCalling}
	manager.Settings.Model = "imported-pollen-model"
	if err := fileStore.SaveWorkspaceAgent(ws.ID, "Pollen Manager", manager); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}
	if err := fileStore.Close(); err != nil {
		t.Fatalf("close file store: %v", err)
	}

	loadedStore, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("reload file store: %v", err)
	}
	defer func() { _ = loadedStore.Close() }()
	agents := &resolverAgentStoreStub{agents: map[string]*agent.Agent{}}

	RestoreAllWorkspaceAgents(loadedStore, agents)

	got, ok := agents.GetAgent("Pollen Manager")
	if !ok || got == nil {
		t.Fatalf("expected Pollen Manager restored into global store, ok=%v", ok)
	}
	if got.Settings.Model != "imported-pollen-model" {
		t.Fatalf("expected restored model imported-pollen-model, got %q", got.Settings.Model)
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
		ID:             "ws-x",
		Name:           "X",
		AgentInstances: AgentInstancesFromNames("Manager"),
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

func TestRestoreAllowlistedWorkspaceAgents_OnlyRestoresAllowlisted(t *testing.T) {
	primary := NewInMemoryStore()

	allowedAg := &agent.Agent{Type: agent.TypeToolCalling}
	allowedAg.Settings.Model = "allowed-model"
	if err := primary.SaveWorkspaceAgent("ws-allow", "AllowedManager", allowedAg); err != nil {
		t.Fatalf("seed allow snapshot: %v", err)
	}
	deniedAg := &agent.Agent{Type: agent.TypeToolCalling}
	deniedAg.Settings.Model = "denied-model"
	if err := primary.SaveWorkspaceAgent("ws-deny", "DeniedManager", deniedAg); err != nil {
		t.Fatalf("seed deny snapshot: %v", err)
	}
	if err := primary.Save(&Workspace{
		ID:             "ws-allow",
		Name:           "Allow",
		AgentInstances: AgentInstancesFromNames("AllowedManager"),
	}); err != nil {
		t.Fatalf("save allow ws: %v", err)
	}
	if err := primary.Save(&Workspace{
		ID:             "ws-deny",
		Name:           "Deny",
		AgentInstances: AgentInstancesFromNames("DeniedManager"),
	}); err != nil {
		t.Fatalf("save deny ws: %v", err)
	}

	agents := &resolverAgentStoreStub{agents: map[string]*agent.Agent{}}
	allowlist := NewAllowlist(filepath.Join(t.TempDir(), "wl.json"))
	if err := allowlist.Add("ws-allow"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	RestoreAllowlistedWorkspaceAgents(primary, agents, allowlist)

	if _, ok := agents.GetAgent("AllowedManager"); !ok {
		t.Fatal("expected AllowedManager restored")
	}
	if _, ok := agents.GetAgent("DeniedManager"); ok {
		t.Fatal("DeniedManager must not be restored (workspace not allowlisted)")
	}
}

func TestRestoreAllowlistedWorkspaceAgents_NilAllowlistRestoresNothing(t *testing.T) {
	primary := NewInMemoryStore()
	managerAg := &agent.Agent{Type: agent.TypeToolCalling}
	if err := primary.SaveWorkspaceAgent("ws-x", "Manager", managerAg); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := primary.Save(&Workspace{ID: "ws-x", AgentInstances: AgentInstancesFromNames("Manager")}); err != nil {
		t.Fatalf("save ws: %v", err)
	}
	agents := &resolverAgentStoreStub{agents: map[string]*agent.Agent{}}

	RestoreAllowlistedWorkspaceAgents(primary, agents, nil)

	if _, ok := agents.GetAgent("Manager"); ok {
		t.Fatal("nil allowlist must restore nothing")
	}
}

func TestWipeNonAllowlistedAgentSnapshots_RemovesUnallowedKeepsAllowedAndSystem(t *testing.T) {
	primary := NewInMemoryStore()

	// Snapshots mirror the global definition at snapshot time (as SaveWorkspaceAgent
	// does in production). One allowlisted workspace, two not: one holding a pure
	// mirror, one holding a *stale* snapshot of a since-edited global.
	allowedDef := &agent.Agent{Type: agent.TypeGeneral}
	deniedDef := &agent.Agent{Type: agent.TypeGeneral}
	editedSnapshot := &agent.Agent{Type: agent.TypeGeneral, Settings: types.Settings{SystemPrompt: "OLD"}}
	if err := primary.SaveWorkspaceAgent("ws-allow", "AllowedManager", allowedDef); err != nil {
		t.Fatalf("seed allow snapshot: %v", err)
	}
	if err := primary.SaveWorkspaceAgent("ws-deny", "DeniedManager", deniedDef); err != nil {
		t.Fatalf("seed deny snapshot: %v", err)
	}
	if err := primary.SaveWorkspaceAgent("ws-deny-edited", "EditedManager", editedSnapshot); err != nil {
		t.Fatalf("seed edited snapshot: %v", err)
	}
	if err := primary.Save(&Workspace{ID: "ws-allow", AgentInstances: AgentInstancesFromNames("AllowedManager")}); err != nil {
		t.Fatalf("save allow ws: %v", err)
	}
	if err := primary.Save(&Workspace{ID: "ws-deny", AgentInstances: AgentInstancesFromNames("DeniedManager")}); err != nil {
		t.Fatalf("save deny ws: %v", err)
	}
	if err := primary.Save(&Workspace{ID: "ws-deny-edited", AgentInstances: AgentInstancesFromNames("EditedManager")}); err != nil {
		t.Fatalf("save edited ws: %v", err)
	}

	// Global agent store has: the system agent (Ori), the two mirror-managed
	// agents, a user-owned agent no workspace knows about, and EditedManager
	// whose global system prompt has since diverged from its stale snapshot.
	agents := &resolverAgentStoreStub{agents: map[string]*agent.Agent{
		"Ori":             {Type: agent.TypeToolCalling},
		"AllowedManager":  {Type: agent.TypeGeneral},
		"DeniedManager":   {Type: agent.TypeGeneral},
		"EditedManager":   {Type: agent.TypeGeneral, Settings: types.Settings{SystemPrompt: "EDITED"}},
		"UserOwnedHelper": {Type: agent.TypeGeneral},
	}}
	allowlist := NewAllowlist(filepath.Join(t.TempDir(), "wl.json"))
	if err := allowlist.Add("ws-allow"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	WipeNonAllowlistedAgentSnapshots(primary, agents, allowlist)

	if _, ok := agents.GetAgent("DeniedManager"); ok {
		t.Fatal("DeniedManager should be wiped (mirror of a non-allowlisted workspace snapshot)")
	}
	if _, ok := agents.GetAgent("AllowedManager"); !ok {
		t.Fatal("AllowedManager should be preserved (workspace allowlisted)")
	}
	if _, ok := agents.GetAgent("EditedManager"); !ok {
		t.Fatal("EditedManager must be preserved: user edited the global, so it is not a pure snapshot mirror (PRD FR11)")
	}
	if _, ok := agents.GetAgent("Ori"); !ok {
		t.Fatal("system agent Ori must never be wiped")
	}
	if _, ok := agents.GetAgent("UserOwnedHelper"); !ok {
		t.Fatal("agent with no workspace snapshot must not be wiped")
	}
}

func TestWipeNonAllowlistedAgentSnapshots_KeepsAgentReferencedByOneAllowlistedWorkspace(t *testing.T) {
	primary := NewInMemoryStore()

	// Two workspaces both reference "Manager", but only one is allowlisted.
	// The agent should still be kept because at least one allowlisted
	// workspace claims it.
	if err := primary.SaveWorkspaceAgent("ws-allow", "Manager", &agent.Agent{}); err != nil {
		t.Fatalf("seed allow: %v", err)
	}
	if err := primary.SaveWorkspaceAgent("ws-deny", "Manager", &agent.Agent{}); err != nil {
		t.Fatalf("seed deny: %v", err)
	}
	if err := primary.Save(&Workspace{ID: "ws-allow", AgentInstances: AgentInstancesFromNames("Manager")}); err != nil {
		t.Fatalf("save allow: %v", err)
	}
	if err := primary.Save(&Workspace{ID: "ws-deny", AgentInstances: AgentInstancesFromNames("Manager")}); err != nil {
		t.Fatalf("save deny: %v", err)
	}

	agents := &resolverAgentStoreStub{agents: map[string]*agent.Agent{
		"Manager": {Type: agent.TypeToolCalling},
	}}
	allowlist := NewAllowlist(filepath.Join(t.TempDir(), "wl.json"))
	if err := allowlist.Add("ws-allow"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	WipeNonAllowlistedAgentSnapshots(primary, agents, allowlist)

	if _, ok := agents.GetAgent("Manager"); !ok {
		t.Fatal("Manager must be preserved because ws-allow references it")
	}
}

func TestReferencedAgentNames_Dedupes(t *testing.T) {
	ws := &Workspace{
		AgentInstances: []AgentInstance{
			{Name: "Manager"},
			{Name: "manager"},
			{Name: "Helper"},
			{Name: " Other "},
		},
		SharedData: map[string]any{
			"entry_agent_name": "Manager",
		},
	}
	got := referencedAgentNames(ws)
	if len(got) != 3 {
		t.Fatalf("expected 3 unique names, got %v", got)
	}
}
