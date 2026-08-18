package workspace

import (
	"path/filepath"
	"testing"
	"time"

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

func TestAgentSnapshotStore_SavePreservesWorkspaceEditedSnapshot(t *testing.T) {
	primary := NewInMemoryStore()
	global := &agent.Agent{Type: agent.TypeToolCalling}
	global.Settings.Model = "global-model"
	global.Settings.Provider = "ollama"
	agents := &resolverAgentStoreStub{agents: map[string]*agent.Agent{"Manager": global}}
	store := NewAgentSnapshotStore(primary, agents)

	ws := &Workspace{
		ID:             "ws-local-edit",
		Name:           "Local edit",
		Status:         StatusActive,
		AgentInstances: AgentInstancesFromNames("Manager"),
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("initial Save: %v", err)
	}

	local := &agent.Agent{Type: agent.TypeToolCalling}
	local.Settings.Model = "gpt-5.4"
	local.Settings.Provider = "codex"
	if err := store.SaveWorkspaceAgent(ws.ID, "Manager", local); err != nil {
		t.Fatalf("save workspace edit: %v", err)
	}

	if err := store.Update(ws.ID, func(current *Workspace) error {
		current.Description = "Routine workspace mutation"
		return nil
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, ok, err := store.GetWorkspaceAgent(ws.ID, "Manager")
	if err != nil || !ok || got == nil {
		t.Fatalf("read workspace snapshot: ok=%v err=%v agent=%+v", ok, err, got)
	}
	if got.Settings.Provider != "codex" || got.Settings.Model != "gpt-5.4" {
		t.Fatalf("workspace edit was overwritten by global snapshot: %+v", got.Settings)
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

// TestAgentSnapshotStore_GetFolderPathForwardsToWrappedStore covers a real
// production bug found while verifying the workspace-backlog feature: the
// server wires orchestrationhttp's workspace store as
// NewAgentSnapshotStore(syncStore, agents) (see builder_workflow.go), and
// AgentSnapshotStore embeds the Store INTERFACE, not SyncStore/FileStore
// concretely. GetFolderPath is not part of the Store interface, so before
// this fix it was never promoted — any handler holding only *AgentSnapshotStore
// silently failed workspaceFolderForTaskMarkdown's type assertion (ok=false,
// err=nil), which made BOTH tasks.md and BACKLOG.md synchronization silently
// no-op on every write past the very first one. This test would have caught it.
func TestAgentSnapshotStore_GetFolderPathForwardsToWrappedStore(t *testing.T) {
	dir := t.TempDir()
	fileStore, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	primary := NewInMemoryStore()
	sync := NewSyncStore(primary, fileStore)
	agents := &resolverAgentStoreStub{agents: map[string]*agent.Agent{}}
	wrapped := NewAgentSnapshotStore(sync, agents)

	ws := &Workspace{ID: "ws-folder", Name: "Folder Test", Status: StatusActive}
	if err := wrapped.Save(ws); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// The interface-level assertions task_markdown_sync.go/
	// backlog_markdown_sync.go actually use.
	withFolder, ok := Store(wrapped).(interface {
		GetFolderPath(string) (string, error)
	})
	if !ok {
		t.Fatalf("AgentSnapshotStore does not implement GetFolderPath")
	}
	path, err := withFolder.GetFolderPath(ws.ID)
	if err != nil {
		t.Fatalf("GetFolderPath() error = %v", err)
	}
	wantPath, err := fileStore.GetFolderPath(ws.ID)
	if err != nil {
		t.Fatalf("fileStore.GetFolderPath() error = %v", err)
	}
	if path != wantPath {
		t.Fatalf("GetFolderPath() = %q, want %q (the underlying FileStore's path)", path, wantPath)
	}

	withFileSync, ok := Store(wrapped).(interface{ FileStore() *FileStore })
	if !ok {
		t.Fatalf("AgentSnapshotStore does not implement FileStore()")
	}
	if withFileSync.FileStore() != fileStore {
		t.Fatalf("FileStore() did not return the wrapped FileStore instance")
	}
}

// TestAgentSnapshotStore_GetFolderPathErrorsWithoutFolderCapableStore covers
// the case where the wrapped store has no folder concept at all (e.g. a bare
// InMemoryStore): the passthrough must fail loudly, not silently no-op.
func TestAgentSnapshotStore_GetFolderPathErrorsWithoutFolderCapableStore(t *testing.T) {
	primary := NewInMemoryStore()
	agents := &resolverAgentStoreStub{agents: map[string]*agent.Agent{}}
	wrapped := NewAgentSnapshotStore(primary, agents)

	if _, err := wrapped.GetFolderPath("any-id"); err == nil {
		t.Fatalf("expected an error when the wrapped store has no folder capability")
	}
	if wrapped.FileStore() != nil {
		t.Fatalf("expected nil FileStore() when the wrapped store has none")
	}
}

// TestAgentSnapshotStore_GetFolderWorkspaceForwardsThroughWrapping guards a
// real bug: production wraps the workspace store as
// AgentSnapshotStore{Store: SyncStore{...}}, and AgentSnapshotStore embeds
// the Store *interface* (not SyncStore's concrete type), so
// GetFolderWorkspace was never promoted -- any caller needing a
// folder-store-only field (TemplateProvenance, ProjectPath, Designation)
// through the fully-wrapped production store got a method-not-found error
// (or, before this was caught, a nil handler from a failed interface
// assertion at wiring time). This must keep working through the same
// wrapping chain builder.go's initializeWorkspaceStore actually builds.
func TestAgentSnapshotStore_GetFolderWorkspaceForwardsThroughWrapping(t *testing.T) {
	dir := t.TempDir()
	fileStore, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	primary := NewInMemoryStore()
	sync := NewSyncStore(primary, fileStore)
	store := NewAgentSnapshotStore(sync, &resolverAgentStoreStub{agents: map[string]*agent.Agent{}})

	ws := &Workspace{ID: "ws-provenance", Name: "Provenance", Status: StatusActive}
	if err := store.Save(ws); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := store.Update(ws.ID, func(w *Workspace) error {
		w.SetTemplateProvenance(&TemplateProvenance{TemplateID: "calendar-ops", Builtin: true})
		return nil
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// The interface this repo's HTTP handlers actually assert against
	// (calendarhttp.FolderStore mirrors this shape) -- confirms the fully
	// wrapped production store satisfies it, not just that the method exists.
	var reader interface {
		GetFolderWorkspace(id string) (*Workspace, error)
	} = store

	got, err := reader.GetFolderWorkspace(ws.ID)
	if err != nil {
		t.Fatalf("GetFolderWorkspace: %v", err)
	}
	if got == nil || got.TemplateProvenance == nil || got.TemplateProvenance.TemplateID != "calendar-ops" {
		t.Fatalf("expected forwarded read to see template provenance, got: %+v", got)
	}
}

// TestAgentSnapshotStore_AgentWorkSurvivesSetupProgress exercises the fully
// wrapped production store chain (AgentSnapshotStore over SyncStore over
// SQLite-shaped primary + FileStore) against the failure this feature cannot
// tolerate: a routine agent/task update saving a stale copy and erasing setup
// the user already completed.
func TestAgentSnapshotStore_AgentWorkSurvivesSetupProgress(t *testing.T) {
	dir := t.TempDir()
	fileStore, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	primary := NewInMemoryStore()
	store := NewAgentSnapshotStore(NewSyncStore(primary, fileStore), &resolverAgentStoreStub{agents: map[string]*agent.Agent{}})

	ws := &Workspace{ID: "ws-setup", Name: "Setup", Status: StatusActive}
	if err := store.Save(ws); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := store.Update(ws.ID, func(w *Workspace) error {
		w.SetTemplateProvenance(&TemplateProvenance{
			TemplateID: "downloads-janitor",
			Builtin:    true,
			SetupWizard: &SetupWizard{Version: 1, Title: "Set up", Steps: []SetupWizardStep{
				{ID: "folder", Kind: SetupStepKindDirectory, RequirementKey: "downloads-root", Required: true},
			}},
		})
		w.SetSetupWizardProgress(&SetupWizardProgress{
			WizardVersion: 1,
			State:         SetupWizardStateReady,
			Steps:         []SetupStepProgress{{StepID: "folder", Status: SetupStepStatusComplete}},
		})
		return nil
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// An ordinary task write, from a copy fetched through the primary — the
	// shape of every unrelated update in the app.
	if err := store.Update(ws.ID, func(w *Workspace) error {
		w.Tasks = append(w.Tasks, Task{ID: "unrelated", Status: TaskStatusPending})
		return nil
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := store.GetFolderWorkspace(ws.ID)
	if err != nil {
		t.Fatalf("GetFolderWorkspace: %v", err)
	}
	if !got.HasSetupWizard() {
		t.Fatalf("wizard snapshot lost through the wrapped store: %+v", got.GetTemplateProvenance())
	}
	if got.SetupWizardState() != SetupWizardStateReady {
		t.Fatalf("setup state = %q, want ready — an unrelated update reset completed setup", got.SetupWizardState())
	}
	if got.GetSetupWizardProgress().StepStatus("folder") != SetupStepStatusComplete {
		t.Fatalf("per-step progress lost: %+v", got.GetSetupWizardProgress())
	}
	if len(got.Tasks) != 1 {
		t.Fatalf("the unrelated task update was not written through: %+v", got.Tasks)
	}
}

func TestAgentSnapshotStore_UpdateHydratesRuntimeStateBeforeRevoke(t *testing.T) {
	fileStore, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = fileStore.Close() }()

	primary := NewInMemoryStore()
	store := NewAgentSnapshotStore(NewSyncStore(primary, fileStore), &resolverAgentStoreStub{agents: map[string]*agent.Agent{}})
	ws := &Workspace{ID: "ws-runtime-revoke", Name: "Runtime Revoke", Status: StatusActive}
	if err := store.Save(ws); err != nil {
		t.Fatalf("Save: %v", err)
	}

	canonical, err := fileStore.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	canonical.SetRuntimeState(&WorkspaceRuntimeState{
		SelectedModeID: "ori_assisted",
		Grants: []RuntimeCapabilityGrant{{
			CapabilityKey:   "reaper_live_control",
			AgentInstanceID: "producer-1",
			GrantedAt:       time.Date(2026, 8, 18, 13, 0, 0, 0, time.UTC),
		}},
	})
	if err := fileStore.Save(canonical); err != nil {
		t.Fatal(err)
	}

	revokedAt := time.Date(2026, 8, 18, 14, 0, 0, 0, time.UTC)
	if err := store.Update(ws.ID, func(current *Workspace) error {
		changed, revokeErr := current.RevokeRuntimeCapability("reaper_live_control", "producer-1", revokedAt)
		if !changed && revokeErr == nil {
			t.Fatal("active canonical grant was invisible to wrapped Update")
		}
		return revokeErr
	}); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetFolderWorkspace(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	state := got.GetRuntimeState()
	if state == nil {
		t.Fatal("runtime state was lost")
	}
	grant, found := state.RuntimeGrant("reaper_live_control", "producer-1")
	if state.SelectedModeID != "ori_assisted" || !found || grant.Active() || grant.RevokedAt == nil || !grant.RevokedAt.Equal(revokedAt) {
		t.Fatalf("runtime mode or revocation was lost: %+v", state)
	}
}

// TestAgentSnapshotStore_InstalledCapabilitySurvivesAgentWork exercises the
// fully wrapped production chain (AgentSnapshotStore over SyncStore over a
// SQLite-shaped primary + FileStore) against the FR-144 failure mode: an
// ordinary agent/task update saving a stale copy and silently uninstalling a
// capability. It also covers FR-11/FR-12 — the install stands whether or not the
// workspace came from a template and whether or not any agent exists.
func TestAgentSnapshotStore_InstalledCapabilitySurvivesAgentWork(t *testing.T) {
	dir := t.TempDir()
	fileStore, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	primary := NewInMemoryStore()
	store := NewAgentSnapshotStore(NewSyncStore(primary, fileStore), &resolverAgentStoreStub{agents: map[string]*agent.Agent{}})

	ws := &Workspace{ID: "ws-capability", Name: "Capability", Status: StatusActive}
	if err := store.Save(ws); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := store.Update(ws.ID, func(w *Workspace) error {
		_, addErr := w.AddInstalledCapability(InstalledCapability{
			ID:          CapabilityFileJanitor,
			Version:     1,
			InstalledAt: time.Now(),
			Source:      InstallSourceInPlace,
		})
		return addErr
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// An ordinary task write from a primary-fetched copy — the shape of every
	// unrelated update in the app (orchestrationhttp alone has ~40 of these).
	if err := store.Update(ws.ID, func(w *Workspace) error {
		w.Tasks = append(w.Tasks, Task{ID: "unrelated", Status: TaskStatusPending})
		return nil
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := store.GetFolderWorkspace(ws.ID)
	if err != nil {
		t.Fatalf("GetFolderWorkspace: %v", err)
	}
	if !got.HasInstalledCapability(CapabilityFileJanitor) {
		t.Fatalf("capability install lost through the wrapped store: %+v", got.GetInstalledCapabilities())
	}
	if got.GetTemplateProvenance() != nil {
		t.Fatal("precondition: this workspace was never created from a template (FR-11)")
	}
	if len(got.AgentInstances) != 0 {
		t.Fatal("precondition: this workspace has no agents (FR-12)")
	}
	if len(got.Tasks) != 1 {
		t.Fatalf("the unrelated task update was not written through: %+v", got.Tasks)
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

func TestBackfillLocalWorkspacesIntoAllowlist(t *testing.T) {
	local := NewInMemoryStore()
	live := &Workspace{ID: "ws-live", Name: "Live", AgentInstances: AgentInstancesFromNames("Research Lead")}
	trashed := &Workspace{ID: "ws-trashed", Name: "Trashed", Status: StatusTrashed}
	if err := local.Save(live); err != nil {
		t.Fatalf("save live: %v", err)
	}
	if err := local.Save(trashed); err != nil {
		t.Fatalf("save trashed: %v", err)
	}

	allowlist := NewAllowlist(filepath.Join(t.TempDir(), "workspace_allowlist.json"))

	BackfillLocalWorkspacesIntoAllowlist(local, allowlist)

	if !allowlist.Contains("ws-live") {
		t.Errorf("expected live workspace to be allowlisted")
	}
	if allowlist.Contains("ws-trashed") {
		t.Errorf("trashed workspace must not be allowlisted")
	}

	// Idempotent: a second run adds nothing and does not error.
	BackfillLocalWorkspacesIntoAllowlist(local, allowlist)
	if ids := allowlist.IDs(); len(ids) != 1 {
		t.Errorf("expected exactly 1 allowlisted id after re-run, got %v", ids)
	}
}
