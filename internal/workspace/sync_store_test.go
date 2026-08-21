package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
)

type failingWorkspaceSaveStore struct {
	Store
	err error
}

func (s *failingWorkspaceSaveStore) Save(*Workspace) error { return s.err }

func TestSyncStore_SaveRollsBackNewFolderWhenPrimaryFails(t *testing.T) {
	primary := NewInMemoryStore()
	fileStore, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = fileStore.Close() }()

	failure := errors.New("primary unavailable")
	store := NewSyncStore(&failingWorkspaceSaveStore{Store: primary, err: failure}, fileStore)
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Rollback Me"})
	if err := store.Save(ws); !errors.Is(err, failure) {
		t.Fatalf("Save error = %v, want primary failure", err)
	}
	if _, err := fileStore.Get(ws.ID); err == nil {
		t.Fatal("failed primary save left a folder registration behind")
	}
	if _, err := os.Stat(filepath.Join(fileStore.BasePath(), "rollback-me")); !os.IsNotExist(err) {
		t.Fatalf("failed primary save left folder on disk: %v", err)
	}
}

func TestSyncStore_FileSlugConflictDoesNotPersistPrimary(t *testing.T) {
	primary := NewInMemoryStore()
	fileStore, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = fileStore.Close() }()

	existing := NewWorkspace(CreateWorkspaceParams{Name: "Reports"})
	if err := fileStore.Save(existing); err != nil {
		t.Fatalf("seed FileStore: %v", err)
	}
	candidate := NewWorkspace(CreateWorkspaceParams{Name: "Reports"})
	err = NewSyncStore(primary, fileStore).Save(candidate)
	var conflict *FolderSlugConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("Save error = %v, want FolderSlugConflictError", err)
	}
	if _, err := primary.Get(candidate.ID); err == nil {
		t.Fatal("FileStore slug conflict still persisted the primary record")
	}
}

func TestSyncStore_SaveSyncsToDisk(t *testing.T) {
	// Set up an in-memory primary store and a file-based sync store
	primary := NewInMemoryStore()
	dir := t.TempDir()
	fileSync, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fileSync.Close() }()

	store := NewSyncStore(primary, fileSync)

	now := time.Now()
	ws := &Workspace{
		ID:         "ws-sync-test",
		Name:       "Sync Test",
		Status:     StatusActive,
		CreatedAt:  now,
		UpdatedAt:  now,
		SharedData: make(map[string]any),
	}

	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}

	// Verify primary store has the workspace
	got, err := primary.Get("ws-sync-test")
	if err != nil {
		t.Fatalf("Primary store should have workspace: %v", err)
	}
	if got.Name != "Sync Test" {
		t.Errorf("Primary store name = %q, want %q", got.Name, "Sync Test")
	}

	// Verify FileStore has the workspace (workspace.json on disk)
	folderPath, err := fileSync.GetFolderPath("ws-sync-test")
	if err != nil {
		t.Fatalf("FileStore should have workspace: %v", err)
	}
	configPath := filepath.Join(folderPath, WorkspaceConfigFile)
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("workspace.json should exist at %s: %v", configPath, err)
	}

	// Verify files/ and notes/ directories were created
	if _, err := os.Stat(filepath.Join(folderPath, FilesDir)); err != nil {
		t.Error("files/ directory should exist")
	}
	if _, err := os.Stat(filepath.Join(folderPath, NotesDir)); err != nil {
		t.Error("notes/ directory should exist")
	}
}

func TestSyncStore_SaveUpdatesWorkspaceJSON(t *testing.T) {
	primary := NewInMemoryStore()
	dir := t.TempDir()
	fileSync, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fileSync.Close() }()

	store := NewSyncStore(primary, fileSync)

	now := time.Now()
	ws := &Workspace{
		ID:         "ws-update-test",
		Name:       "Before Update",
		Status:     StatusActive,
		CreatedAt:  now,
		UpdatedAt:  now,
		SharedData: make(map[string]any),
		MCPBindings: []MCPBinding{
			{ID: "mcp-1", ServerName: "test-server", Enabled: true},
		},
		SkillBindings: []SkillBinding{
			{ID: "skill-1", SkillName: "test-skill", Enabled: true},
		},
	}

	// First save
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}

	// Update with new data
	ws.MCPBindings = append(ws.MCPBindings, MCPBinding{
		ID: "mcp-2", ServerName: "another-server", Enabled: true,
	})
	ws.UpdatedAt = time.Now()

	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}

	// Reload from disk to verify the update was persisted
	diskWS, err := fileSync.Get("ws-update-test")
	if err != nil {
		t.Fatalf("FileStore should have updated workspace: %v", err)
	}
	if len(diskWS.MCPBindings) != 2 {
		t.Errorf("Disk workspace should have 2 MCP bindings, got %d", len(diskWS.MCPBindings))
	}
	if len(diskWS.SkillBindings) != 1 {
		t.Errorf("Disk workspace should have 1 skill binding, got %d", len(diskWS.SkillBindings))
	}
}

func TestSyncStore_SavePreservesCanonicalProjectPathFromStalePrimaryWorkspace(t *testing.T) {
	primary := NewInMemoryStore()
	fileSync, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fileSync.Close() }()

	store := NewSyncStore(primary, fileSync)
	ws := newTestWorkspace("ws-project-path-sync", "Project Path Sync")
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}

	// Reproduce Create Workspace: orchestration can retain a SQLite-backed
	// workspace fetched before the template handler writes project_path directly
	// to canonical workspace.json.
	staleWorkspace, err := primary.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	canonicalWorkspace, err := fileSync.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	canonicalWorkspace.ProjectPath = "song"
	if err := SetProjectEntryPath(canonicalWorkspace.SharedData, "song.rpp"); err != nil {
		t.Fatal(err)
	}
	if err := fileSync.Save(canonicalWorkspace); err != nil {
		t.Fatal(err)
	}

	staleWorkspace.SharedData[ProjectEntryPathKey] = "song.rpp"
	staleWorkspace.Tasks = append(staleWorkspace.Tasks, Task{ID: "setup-task", Status: TaskStatusCompleted})
	if err := store.Save(staleWorkspace); err != nil {
		t.Fatal(err)
	}

	diskWorkspace, err := fileSync.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if diskWorkspace.ProjectPath != "song" {
		t.Fatalf("canonical project_path = %q, want song", diskWorkspace.ProjectPath)
	}
	if diskWorkspace.SharedData[ProjectEntryPathKey] != "song.rpp" {
		t.Fatalf("canonical project entry = %v, want song.rpp", diskWorkspace.SharedData[ProjectEntryPathKey])
	}
	if len(diskWorkspace.Tasks) != 1 || diskWorkspace.Tasks[0].ID != "setup-task" {
		t.Fatalf("task update was not written through: %+v", diskWorkspace.Tasks)
	}
}

func TestSyncStore_SavePreservesCanonicalDesignationFromStalePrimaryWorkspace(t *testing.T) {
	primary := NewInMemoryStore()
	fileSync, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fileSync.Close() }()

	store := NewSyncStore(primary, fileSync)
	ws := newTestWorkspace("ws-designation-sync", "Designation Sync")
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}

	// Personal HQ designation is projected directly to workspace.json. The
	// primary store intentionally has no column for it, so a later task save
	// must retain the canonical designation rather than erase it.
	canonicalWorkspace, err := fileSync.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	canonicalWorkspace.Designation = "personal_hq"
	if err := fileSync.Save(canonicalWorkspace); err != nil {
		t.Fatal(err)
	}

	staleWorkspace, err := primary.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	staleWorkspace.Tasks = append(staleWorkspace.Tasks, Task{ID: "after-designation", Status: TaskStatusPending})
	if err := store.Save(staleWorkspace); err != nil {
		t.Fatal(err)
	}

	diskWorkspace, err := fileSync.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if diskWorkspace.Designation != "personal_hq" {
		t.Fatalf("canonical designation = %q, want personal_hq", diskWorkspace.Designation)
	}
	if len(diskWorkspace.Tasks) != 1 || diskWorkspace.Tasks[0].ID != "after-designation" {
		t.Fatalf("task update was not written through: %+v", diskWorkspace.Tasks)
	}
}

func TestSyncStore_SavePreservesCanonicalTemplateProvenanceFromStalePrimaryWorkspace(t *testing.T) {
	primary := NewInMemoryStore()
	fileSync, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fileSync.Close() }()

	store := NewSyncStore(primary, fileSync)
	ws := newTestWorkspace("ws-provenance-sync", "Provenance Sync")
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}

	// Template provenance is projected directly to workspace.json; the primary
	// store has no column for it. A later save of a SQLite-fetched (stale)
	// workspace must retain the canonical provenance rather than erase it —
	// otherwise a template-origin-based feature (REAPER readiness, Calendar
	// Ops setup detection, the Email Ops resolver, ...) loses the workspace
	// over time.
	canonicalWorkspace, err := fileSync.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	canonicalWorkspace.SetTemplateProvenance(&TemplateProvenance{TemplateID: EmailOpsTemplateID, Builtin: true})
	if err := fileSync.Save(canonicalWorkspace); err != nil {
		t.Fatal(err)
	}

	staleWorkspace, err := primary.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if staleWorkspace.GetTemplateProvenance() != nil {
		t.Fatal("precondition: the in-memory stale copy should have no provenance")
	}
	staleWorkspace.Tasks = append(staleWorkspace.Tasks, Task{ID: "after-provenance", Status: TaskStatusPending})
	if err := store.Save(staleWorkspace); err != nil {
		t.Fatal(err)
	}

	diskWorkspace, err := fileSync.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !diskWorkspace.IsFromTemplate(EmailOpsTemplateID) {
		t.Fatalf("canonical template provenance was clobbered: %+v", diskWorkspace.GetTemplateProvenance())
	}
	if len(diskWorkspace.Tasks) != 1 || diskWorkspace.Tasks[0].ID != "after-provenance" {
		t.Fatalf("task update was not written through: %+v", diskWorkspace.Tasks)
	}
}

func TestSyncStore_SavePreservesSetupWizardProgressFromStalePrimaryWorkspace(t *testing.T) {
	primary := NewInMemoryStore()
	fileSync, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fileSync.Close() }()

	store := NewSyncStore(primary, fileSync)
	ws := newTestWorkspace("ws-setup-progress-sync", "Setup Progress Sync")
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}

	// Setup progress is a workspace.json-only field, like provenance. The user
	// approving a folder writes it to disk; any unrelated task or agent update
	// that saves a SQLite-fetched (stale) copy afterwards must not erase it, or
	// the wizard would forget approvals the user has already given and ask for
	// them again.
	canonicalWorkspace, err := fileSync.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	canonicalWorkspace.SetSetupWizardProgress(&SetupWizardProgress{
		WizardVersion: 1,
		State:         SetupWizardStateInProgress,
		CurrentStepID: "automation",
		Steps:         []SetupStepProgress{{StepID: "folder", Status: SetupStepStatusComplete}},
	})
	if err := fileSync.Save(canonicalWorkspace); err != nil {
		t.Fatal(err)
	}

	staleWorkspace, err := primary.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if staleWorkspace.GetSetupWizardProgress() != nil {
		t.Fatal("precondition: the in-memory stale copy should have no setup progress")
	}
	staleWorkspace.Tasks = append(staleWorkspace.Tasks, Task{ID: "after-setup", Status: TaskStatusPending})
	if err := store.Save(staleWorkspace); err != nil {
		t.Fatal(err)
	}

	diskWorkspace, err := fileSync.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	progress := diskWorkspace.GetSetupWizardProgress()
	if progress == nil {
		t.Fatal("setup progress was clobbered by an unrelated task update")
	}
	if progress.CurrentStepID != "automation" || progress.StepStatus("folder") != SetupStepStatusComplete {
		t.Fatalf("setup progress was not preserved intact: %+v", progress)
	}
	if len(diskWorkspace.Tasks) != 1 || diskWorkspace.Tasks[0].ID != "after-setup" {
		t.Fatalf("task update was not written through: %+v", diskWorkspace.Tasks)
	}
}

func TestSyncStore_SavePreservesRuntimeStateFromStalePrimaryWorkspace(t *testing.T) {
	primary := NewInMemoryStore()
	fileSync, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fileSync.Close() }()

	store := NewSyncStore(primary, fileSync)
	ws := newTestWorkspace("ws-runtime-state-sync", "Runtime State Sync")
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}

	verified := time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)
	canonicalWorkspace, err := fileSync.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	canonicalWorkspace.SetRuntimeState(&WorkspaceRuntimeState{
		SelectedModeID: "assisted",
		RequirementStates: []RuntimeRequirementState{{
			RequirementKey:     "reaper_live_control",
			ConfigurationState: RuntimeConfigurationConfigured,
			FirstVerifiedAt:    &verified,
		}},
	})
	if err := fileSync.Save(canonicalWorkspace); err != nil {
		t.Fatal(err)
	}

	staleWorkspace, err := primary.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if staleWorkspace.GetRuntimeState() != nil {
		t.Fatal("precondition: stale primary copy should have no runtime state")
	}
	staleWorkspace.Tasks = append(staleWorkspace.Tasks, Task{ID: "after-runtime", Status: TaskStatusPending})
	if err := store.Save(staleWorkspace); err != nil {
		t.Fatal(err)
	}

	diskWorkspace, err := fileSync.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	state := diskWorkspace.GetRuntimeState()
	if state == nil || state.SelectedModeID != "assisted" || len(state.RequirementStates) != 1 || state.RequirementStates[0].ConfigurationState != RuntimeConfigurationConfigured {
		t.Fatalf("canonical runtime state was clobbered: %+v", state)
	}
	if state.RequirementStates[0].FirstVerifiedAt == nil || !state.RequirementStates[0].FirstVerifiedAt.Equal(verified) {
		t.Fatalf("verification history was not preserved: %+v", state)
	}
	if len(diskWorkspace.Tasks) != 1 || diskWorkspace.Tasks[0].ID != "after-runtime" {
		t.Fatalf("task update was not written through: %+v", diskWorkspace.Tasks)
	}
}

func TestSyncStore_UpdateHydratesRuntimeStateBeforeMutation(t *testing.T) {
	primary := NewInMemoryStore()
	fileSync, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fileSync.Close() }()

	store := NewSyncStore(primary, fileSync)
	ws := newTestWorkspace("ws-runtime-update-sync", "Runtime Update Sync")
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}

	canonicalWorkspace, err := fileSync.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	canonicalWorkspace.SetRuntimeState(&WorkspaceRuntimeState{SelectedModeID: "ori_assisted"})
	if err := fileSync.Save(canonicalWorkspace); err != nil {
		t.Fatal(err)
	}

	grantedAt := time.Date(2026, 8, 18, 13, 0, 0, 0, time.UTC)
	if err := store.Update(ws.ID, func(current *Workspace) error {
		if state := current.GetRuntimeState(); state == nil || state.SelectedModeID != "ori_assisted" {
			t.Fatalf("Update callback received stale runtime state: %+v", state)
		}
		_, grantErr := current.GrantRuntimeCapability("reaper_live_control", "producer-1", grantedAt)
		return grantErr
	}); err != nil {
		t.Fatal(err)
	}

	diskWorkspace, err := fileSync.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	state := diskWorkspace.GetRuntimeState()
	if state == nil || state.SelectedModeID != "ori_assisted" || !state.HasActiveRuntimeGrant("reaper_live_control", "producer-1") {
		t.Fatalf("runtime mode or grant was lost: %+v", state)
	}
}

// TestSyncStore_SavePreservesInstalledCapabilitiesFromStalePrimaryWorkspace is
// the FR-144 guard for capability installs: "a stale workspace snapshot must not
// silently erase a capability install or its directory reference". Unlike
// provenance, this field IS mirrored into SQLite — but a record written before
// the column existed, or built by a caller that never loaded it, still arrives
// carrying nothing, and must not be treated as an uninstall.
func TestSyncStore_SavePreservesInstalledCapabilitiesFromStalePrimaryWorkspace(t *testing.T) {
	primary := NewInMemoryStore()
	fileSync, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fileSync.Close() }()

	store := NewSyncStore(primary, fileSync)
	ws := newTestWorkspace("ws-capability-sync", "Capability Sync")
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}

	canonicalWorkspace, err := fileSync.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := canonicalWorkspace.AddInstalledCapability(InstalledCapability{
		ID:          CapabilityFileJanitor,
		Version:     1,
		InstalledAt: time.Now(),
		Source:      InstallSourceInPlace,
	}); err != nil {
		t.Fatal(err)
	}
	if err := fileSync.Save(canonicalWorkspace); err != nil {
		t.Fatal(err)
	}

	staleWorkspace, err := primary.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if staleWorkspace.HasInstalledCapability(CapabilityFileJanitor) {
		t.Fatal("precondition: the stale copy should carry no capability install")
	}
	staleWorkspace.Tasks = append(staleWorkspace.Tasks, Task{ID: "after-install", Status: TaskStatusPending})
	if err := store.Save(staleWorkspace); err != nil {
		t.Fatal(err)
	}

	diskWorkspace, err := fileSync.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	installed, ok := diskWorkspace.GetInstalledCapability(CapabilityFileJanitor)
	if !ok {
		t.Fatalf("capability install was clobbered by an unrelated task update: %+v", diskWorkspace.GetInstalledCapabilities())
	}
	if installed.Source != InstallSourceInPlace || installed.Version != 1 {
		t.Fatalf("install record was not preserved intact: %+v", installed)
	}
	if len(diskWorkspace.Tasks) != 1 || diskWorkspace.Tasks[0].ID != "after-install" {
		t.Fatalf("task update was not written through: %+v", diskWorkspace.Tasks)
	}
}

// TestSyncStore_SaveWritesThroughNewInstalledCapability is the other half of the
// guard above: preserve-if-empty must not become preserve-always. A save that
// genuinely carries an install has to reach disk.
func TestSyncStore_SaveWritesThroughNewInstalledCapability(t *testing.T) {
	primary := NewInMemoryStore()
	fileSync, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fileSync.Close() }()

	store := NewSyncStore(primary, fileSync)
	ws := newTestWorkspace("ws-capability-install", "Capability Install")
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}

	if err := store.Update(ws.ID, func(w *Workspace) error {
		_, addErr := w.AddInstalledCapability(InstalledCapability{
			ID:          CapabilityFileJanitor,
			Version:     1,
			InstalledAt: time.Now(),
			Source:      InstallSourceBlueprint,
		})
		return addErr
	}); err != nil {
		t.Fatal(err)
	}

	diskWorkspace, err := fileSync.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	installed, ok := diskWorkspace.GetInstalledCapability(CapabilityFileJanitor)
	if !ok {
		t.Fatalf("install never reached workspace.json: %+v", diskWorkspace.GetInstalledCapabilities())
	}
	if installed.Source != InstallSourceBlueprint {
		t.Fatalf("install source not written through: %+v", installed)
	}

	// And it must be readable back through the primary too, so a subsequent
	// Get -> mutate -> Save cycle does not depend on the preservation shim.
	primaryWorkspace, err := primary.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !primaryWorkspace.HasInstalledCapability(CapabilityFileJanitor) {
		t.Fatalf("install did not reach the primary store: %+v", primaryWorkspace.GetInstalledCapabilities())
	}
}

// TestSyncStore_SaveWritesThroughDeliberateUninstall is the counterpart to the
// stale-write guard above, and the reason Workspace tracks edit intent at all.
//
// Both an uninstall and a never-loaded record leave the collection empty. If the
// guard used emptiness alone it would restore the install from workspace.json
// and the uninstall would silently fail — so removal would have to bypass
// SyncStore entirely. Tracking intent keeps removal on the ordinary Update path.
func TestSyncStore_SaveWritesThroughDeliberateUninstall(t *testing.T) {
	primary := NewInMemoryStore()
	fileSync, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fileSync.Close() }()

	store := NewSyncStore(primary, fileSync)
	ws := newTestWorkspace("ws-capability-uninstall", "Capability Uninstall")
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
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
		t.Fatal(err)
	}

	// Precondition: it really is on disk, so a failed removal would be visible.
	if diskWorkspace, err := fileSync.Get(ws.ID); err != nil {
		t.Fatal(err)
	} else if !diskWorkspace.HasInstalledCapability(CapabilityFileJanitor) {
		t.Fatal("precondition: install should be on disk before removal")
	}

	if err := store.Update(ws.ID, func(w *Workspace) error {
		if !w.RemoveInstalledCapability(CapabilityFileJanitor) {
			t.Fatal("RemoveInstalledCapability reported no change")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	diskWorkspace, err := fileSync.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if diskWorkspace.HasInstalledCapability(CapabilityFileJanitor) {
		t.Fatalf("uninstall was undone by the stale-write guard: %+v", diskWorkspace.GetInstalledCapabilities())
	}

	primaryWorkspace, err := primary.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if primaryWorkspace.HasInstalledCapability(CapabilityFileJanitor) {
		t.Fatalf("uninstall did not reach the primary store: %+v", primaryWorkspace.GetInstalledCapabilities())
	}
}

// TestWorkspace_CapabilityEditIntentDoesNotSurviveReload proves the intent flag
// is scoped to one mutate-then-save cycle. A workspace decoded from disk must
// carry no pending intent, or the very next unrelated save from a partial record
// would be treated as an authorized erasure.
func TestWorkspace_CapabilityEditIntentDoesNotSurviveReload(t *testing.T) {
	ws := newTestWorkspace("ws-intent", "Intent")
	if _, err := ws.AddInstalledCapability(InstalledCapability{
		ID:          CapabilityFileJanitor,
		Version:     1,
		InstalledAt: time.Now(),
		Source:      InstallSourceInPlace,
	}); err != nil {
		t.Fatal(err)
	}
	if !ws.InstalledCapabilitiesExplicit() {
		t.Fatal("editing the collection should mark intent")
	}

	data, err := ws.ToJSON()
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := FromJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.InstalledCapabilitiesExplicit() {
		t.Fatal("edit intent leaked through a JSON round trip; a reloaded record must carry none")
	}
}

func TestSyncStore_SaveSkipsDiskForTrashedWorkspace(t *testing.T) {
	primary := NewInMemoryStore()
	dir := t.TempDir()
	fileSync, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fileSync.Close() }()

	store := NewSyncStore(primary, fileSync)

	now := time.Now()
	ws := &Workspace{
		ID:         "ws-trashed-test",
		Name:       "Trashed Test",
		Status:     StatusTrashed,
		CreatedAt:  now,
		UpdatedAt:  now,
		SharedData: map[string]any{"_trash": map[string]any{"original_path": filepath.Join(dir, "trashed-test")}},
	}

	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}

	got, err := primary.Get(ws.ID)
	if err != nil {
		t.Fatalf("Primary store should have trashed workspace: %v", err)
	}
	if got.Status != StatusTrashed {
		t.Fatalf("Primary status = %q, want %q", got.Status, StatusTrashed)
	}
	if _, err := fileSync.GetFolderPath(ws.ID); err == nil {
		t.Fatal("FileStore should not register a trashed workspace during sync")
	}
	if _, err := os.Stat(filepath.Join(dir, "trashed-test")); !os.IsNotExist(err) {
		t.Fatalf("trashed workspace folder should not be recreated, stat err = %v", err)
	}
}

// TestSyncStore_SaveSkipsDiskForMissingWorkspace guards against resurrection:
// a workspace whose folder was deleted externally (status missing) must not
// have its folder silently recreated by a write-through save.
func TestSyncStore_SaveSkipsDiskForMissingWorkspace(t *testing.T) {
	primary := NewInMemoryStore()
	dir := t.TempDir()
	fileSync, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fileSync.Close() }()

	store := NewSyncStore(primary, fileSync)

	now := time.Now()
	ws := &Workspace{
		ID:         "ws-missing-test",
		Name:       "Missing Test",
		FolderSlug: "missing-test",
		Status:     StatusMissing,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}

	got, err := primary.Get(ws.ID)
	if err != nil {
		t.Fatalf("Primary store should have missing workspace: %v", err)
	}
	if got.Status != StatusMissing {
		t.Fatalf("Primary status = %q, want %q", got.Status, StatusMissing)
	}
	if _, err := os.Stat(filepath.Join(dir, "missing-test")); !os.IsNotExist(err) {
		t.Fatalf("missing workspace folder should not be recreated, stat err = %v", err)
	}
}

func TestSyncStore_SaveWorkspaceAgentSkipsTrashedWorkspace(t *testing.T) {
	primary := NewInMemoryStore()
	dir := t.TempDir()
	fileSync, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fileSync.Close() }()

	store := NewSyncStore(primary, fileSync)

	now := time.Now()
	ws := &Workspace{
		ID:        "ws-trashed-agent",
		Name:      "Trashed Agent",
		Status:    StatusTrashed,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := primary.Save(ws); err != nil {
		t.Fatal(err)
	}

	if err := store.SaveWorkspaceAgent(ws.ID, "Manager", &agent.Agent{Type: agent.TypeToolCalling}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := primary.GetWorkspaceAgent(ws.ID, "Manager"); err != nil || ok {
		t.Fatalf("primary agent snapshot should not be written for trashed workspace, ok=%v err=%v", ok, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "trashed-agent")); !os.IsNotExist(err) {
		t.Fatalf("trashed workspace folder should not be recreated by agent snapshot, stat err = %v", err)
	}
}

func TestSyncStore_SaveWorkspaceAgentSkipsMissingWorkspace(t *testing.T) {
	primary := NewInMemoryStore()
	dir := t.TempDir()
	fileSync, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fileSync.Close() }()

	store := NewSyncStore(primary, fileSync)

	now := time.Now()
	ws := &Workspace{
		ID:         "ws-missing-agent",
		Name:       "Missing Agent",
		FolderSlug: "missing-agent",
		Status:     StatusMissing,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := primary.Save(ws); err != nil {
		t.Fatal(err)
	}

	if err := store.SaveWorkspaceAgent(ws.ID, "Manager", &agent.Agent{Type: agent.TypeToolCalling}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := primary.GetWorkspaceAgent(ws.ID, "Manager"); err != nil || ok {
		t.Fatalf("primary agent snapshot should not be written for missing workspace, ok=%v err=%v", ok, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "missing-agent")); !os.IsNotExist(err) {
		t.Fatalf("missing workspace folder should not be recreated by agent snapshot, stat err = %v", err)
	}
}

func TestSyncStore_DeleteRemovesFromBoth(t *testing.T) {
	primary := NewInMemoryStore()
	dir := t.TempDir()
	fileSync, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fileSync.Close() }()

	store := NewSyncStore(primary, fileSync)

	now := time.Now()
	ws := &Workspace{
		ID:         "ws-delete-test",
		Name:       "Delete Me",
		Status:     StatusActive,
		CreatedAt:  now,
		UpdatedAt:  now,
		SharedData: make(map[string]any),
	}

	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}

	// Verify folder exists
	folderPath, err := fileSync.GetFolderPath("ws-delete-test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(folderPath); err != nil {
		t.Fatal("Workspace folder should exist before delete")
	}

	// Delete
	if err := store.Delete("ws-delete-test"); err != nil {
		t.Fatal(err)
	}

	// Verify removed from primary
	if _, err := primary.Get("ws-delete-test"); err == nil {
		t.Error("Primary store should not have workspace after delete")
	}

	// Verify folder removed from disk
	if _, err := os.Stat(folderPath); !os.IsNotExist(err) {
		t.Error("Workspace folder should be removed after delete")
	}
}

func TestSyncStore_GetDelegatesToPrimary(t *testing.T) {
	primary := NewInMemoryStore()
	dir := t.TempDir()
	fileSync, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fileSync.Close() }()

	store := NewSyncStore(primary, fileSync)

	now := time.Now()
	ws := &Workspace{
		ID:         "ws-get-test",
		Name:       "Get Test",
		Status:     StatusActive,
		CreatedAt:  now,
		UpdatedAt:  now,
		SharedData: make(map[string]any),
	}

	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}

	got, err := store.Get("ws-get-test")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Get Test" {
		t.Errorf("Get name = %q, want %q", got.Name, "Get Test")
	}
}

func TestSyncStore_GetFilesPathUsesFileStore(t *testing.T) {
	primary := NewInMemoryStore()
	dir := t.TempDir()
	fileSync, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fileSync.Close() }()

	store := NewSyncStore(primary, fileSync)

	now := time.Now()
	ws := &Workspace{
		ID:         "ws-files-test",
		Name:       "Files Test",
		Status:     StatusActive,
		CreatedAt:  now,
		UpdatedAt:  now,
		SharedData: make(map[string]any),
	}
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}

	filesPath := store.GetFilesPath("ws-files-test")
	// Should use the FileStore's slug-based path, not the primary's ID-based path
	expected := filepath.Join(dir, "files-test", FilesDir)
	if filesPath != expected {
		t.Errorf("GetFilesPath = %q, want %q", filesPath, expected)
	}
}

func TestSyncStore_FileStoreAccessor(t *testing.T) {
	primary := NewInMemoryStore()
	dir := t.TempDir()
	fileSync, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fileSync.Close() }()

	store := NewSyncStore(primary, fileSync)

	if store.FileStore() != fileSync {
		t.Error("FileStore() should return the underlying FileStore")
	}
}
