package workspacecapability

import (
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// migrationStore adds List to the shared test store.
type migrationStore struct {
	*memStore
	listErr    error
	updateErrs map[string]error
}

func newMigrationStore(workspaces ...*workspace.Workspace) *migrationStore {
	return &migrationStore{memStore: newMemStore(workspaces...), updateErrs: map[string]error{}}
}

func (s *migrationStore) List() ([]string, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.workspaces))
	for id := range s.workspaces {
		ids = append(ids, id)
	}
	return ids, nil
}

func (s *migrationStore) Update(id string, fn func(*workspace.Workspace) error) error {
	if err := s.updateErrs[id]; err != nil {
		return err
	}
	return s.memStore.Update(id, fn)
}

// configuredProbe reports the given workspaces as having completed Janitor
// setup on disk.
type configuredProbe map[string]bool

func (p configuredProbe) HasConfiguredJanitorState(workspaceID string) bool { return p[workspaceID] }

func plainWorkspace(id, name string) *workspace.Workspace {
	now := time.Date(2026, 7, 30, 9, 0, 0, 0, time.UTC)
	return &workspace.Workspace{
		ID:          id,
		Name:        name,
		OwnerUserID: "local",
		Status:      workspace.StatusActive,
		SharedData:  map[string]any{},
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

func fromDownloadsTemplate(id, name string) *workspace.Workspace {
	ws := plainWorkspace(id, name)
	ws.SetTemplateProvenance(&workspace.TemplateProvenance{
		TemplateID: LegacyDownloadsTemplateID,
		Builtin:    true,
	})
	return ws
}

func pendingDownloadsSetup(id, name, requirementKey string) *workspace.Workspace {
	ws := plainWorkspace(id, name)
	ws.SetTemplateProvenance(&workspace.TemplateProvenance{
		TemplateID: "some-other-template",
		Builtin:    true,
		DirectoryRequirements: []workspace.DirectoryRequirement{
			{Key: requirementKey, Label: "Folder to tidy"},
		},
	})
	return ws
}

func newTestMigrator(t *testing.T, store *migrationStore, probe LegacyStateProbe) *Migrator {
	t.Helper()
	return NewMigrator(mustBuiltinRegistry(t), store, probe)
}

// --- Authoritative signals (FR-126) -----------------------------------------

func TestMigration_BackfillsOnDownloadsTemplateProvenance(t *testing.T) {
	store := newMigrationStore(fromDownloadsTemplate("ws-1", "Tidy"))
	result := newTestMigrator(t, store, nil).Run()

	if result.Migrated != 1 {
		t.Fatalf("migrated = %d, want 1 (%+v)", result.Migrated, result)
	}
	ws, _ := store.Get("ws-1")
	record, ok := ws.GetInstalledCapability(workspace.CapabilityFileJanitor)
	if !ok {
		t.Fatalf("no install record written: %+v", ws.GetInstalledCapabilities())
	}
	if record.Source != workspace.InstallSourceLegacyMigration {
		t.Fatalf("source = %q, want the migration source", record.Source)
	}
	if record.Version != FileJanitorDefinitionVersion {
		t.Fatalf("version = %d", record.Version)
	}
	if record.InstalledAt.IsZero() {
		t.Fatal("installed_at was not stamped")
	}
}

func TestMigration_BackfillsOnConfiguredJanitorState(t *testing.T) {
	// No Downloads provenance at all — this workspace had the capability set up
	// in place, which is exactly the case template provenance cannot see.
	store := newMigrationStore(plainWorkspace("ws-1", "Research Notes"))
	result := newTestMigrator(t, store, configuredProbe{"ws-1": true}).Run()

	if result.Migrated != 1 {
		t.Fatalf("migrated = %d, want 1 (%+v)", result.Migrated, result)
	}
	ws, _ := store.Get("ws-1")
	if !ws.HasInstalledCapability(workspace.CapabilityFileJanitor) {
		t.Fatal("a configured workspace was not migrated")
	}
}

func TestMigration_BackfillsOnPendingSetupRequirement(t *testing.T) {
	for _, key := range []string{"downloads-root", "file-janitor-root"} {
		t.Run(key, func(t *testing.T) {
			store := newMigrationStore(pendingDownloadsSetup("ws-1", "Inbox", key))
			result := newTestMigrator(t, store, nil).Run()

			if result.Migrated != 1 {
				t.Fatalf("migrated = %d, want 1 (%+v)", result.Migrated, result)
			}
		})
	}
}

// --- Rejected signals (FR-136) ----------------------------------------------

// TestMigration_RejectsFalsePositiveSignals is the FR-136 guarantee. Each of
// these workspaces looks like a Janitor workspace from some angle, and none of
// them was ever running it. Migrating any of them would put a station on an
// unrelated workspace and claim access the user never granted.
func TestMigration_RejectsFalsePositiveSignals(t *testing.T) {
	filedDir := plainWorkspace("ws-filed", "Archive")
	filedDir.Folders = []workspace.Folder{{ID: "f1", Path: "Filed"}}

	directoryRef := plainWorkspace("ws-dir", "Research")
	directoryRef.DirectoryReferences = []workspace.DirectoryReference{
		{ID: "d1", WorkspaceID: "ws-dir", Name: "Downloads", Path: "/Users/someone/Downloads"},
	}

	curatorAgent := plainWorkspace("ws-agent", "Media")
	curatorAgent.AgentInstances = []workspace.AgentInstance{
		{ID: "a1", Name: "Downloads Curator", NodeID: "n1"},
	}

	otherTemplate := plainWorkspace("ws-template", "Songs")
	otherTemplate.SetTemplateProvenance(&workspace.TemplateProvenance{
		TemplateID: "reaper-song",
		Builtin:    true,
	})

	cases := []*workspace.Workspace{
		plainWorkspace("ws-name-1", "Downloads Janitor"),
		plainWorkspace("ws-name-2", "My Downloads"),
		plainWorkspace("ws-name-3", "The Janitor"),
		filedDir,
		directoryRef,
		curatorAgent,
		otherTemplate,
		plainWorkspace("ws-plain", "Ordinary workspace"),
	}

	store := newMigrationStore(cases...)
	result := newTestMigrator(t, store, configuredProbe{}).Run()

	if result.Migrated != 0 {
		t.Fatalf("migrated %d workspaces on non-authoritative evidence (%+v)", result.Migrated, result)
	}
	if result.Skipped != len(cases) {
		t.Fatalf("skipped = %d, want %d", result.Skipped, len(cases))
	}
	for _, ws := range cases {
		stored, _ := store.Get(ws.ID)
		if stored.HasInstalledCapability(workspace.CapabilityFileJanitor) {
			t.Fatalf("workspace %q (%s) was migrated on a false-positive signal", ws.ID, ws.Name)
		}
	}
}

// TestMigration_NameAloneIsNeverEvidence states the name rule directly. The
// helper exists precisely so this boundary is visible in code rather than being
// an absence of code.
func TestMigration_NameAloneIsNeverEvidence(t *testing.T) {
	name := "Downloads Janitor"
	if !LooksLikeJanitorByName(name) {
		t.Fatal("precondition: this name should look like the Janitor's")
	}

	store := newMigrationStore(plainWorkspace("ws-1", name))
	if migrated := newTestMigrator(t, store, nil).Run().Migrated; migrated != 0 {
		t.Fatalf("a name-only match migrated %d workspaces", migrated)
	}
}

// --- Idempotency and preservation (FR-127, FR-129) ---------------------------

func TestMigration_IsIdempotentAcrossRepeatedStartups(t *testing.T) {
	store := newMigrationStore(fromDownloadsTemplate("ws-1", "Tidy"))
	migrator := newTestMigrator(t, store, nil)

	first := migrator.Run()
	if first.Migrated != 1 {
		t.Fatalf("first pass migrated %d", first.Migrated)
	}
	firstRecord, _ := mustWorkspace(t, store, "ws-1").GetInstalledCapability(workspace.CapabilityFileJanitor)

	for i := range 4 {
		again := migrator.Run()
		if again.Migrated != 0 {
			t.Fatalf("pass %d migrated %d workspaces again", i+2, again.Migrated)
		}
		if again.AlreadyInstalled != 1 {
			t.Fatalf("pass %d reported already_installed = %d", i+2, again.AlreadyInstalled)
		}
	}

	ws := mustWorkspace(t, store, "ws-1")
	records := ws.GetInstalledCapabilities()
	if len(records) != 1 {
		t.Fatalf("repeated startups produced %d install records", len(records))
	}
	if !records[0].InstalledAt.Equal(firstRecord.InstalledAt) || records[0].Source != firstRecord.Source {
		t.Fatalf("a later pass rewrote the original record: %+v -> %+v", firstRecord, records[0])
	}
}

// TestMigration_PreservesEverythingElse covers FR-129/FR-130: the backfill adds
// an install record and touches nothing else. Name, ID, parent, tasks, agents,
// directory references, MCP bindings, and template provenance all survive
// unchanged, and no new folder access appears.
func TestMigration_PreservesEverythingElse(t *testing.T) {
	ws := fromDownloadsTemplate("ws-1", "Downloads Janitor")
	ws.ParentID = "parent-1"
	ws.Tasks = []workspace.Task{{ID: "t1", Description: "Existing", Status: workspace.TaskStatusPending}}
	ws.AgentInstances = []workspace.AgentInstance{{ID: "a1", Name: "Downloads Curator", NodeID: "n1"}}
	ws.DirectoryReferences = []workspace.DirectoryReference{
		{ID: "d1", WorkspaceID: "ws-1", Name: "Downloads", Path: "/Users/someone/Downloads"},
	}
	ws.MCPBindings = []workspace.MCPBinding{{ID: "b1", ServerName: "filesystem", Alias: "downloads_janitor_root", Enabled: true}}

	store := newMigrationStore(ws)
	before := maskedWorkspaceJSON(t, mustWorkspace(t, store, "ws-1"))

	if migrated := newTestMigrator(t, store, nil).Run().Migrated; migrated != 1 {
		t.Fatalf("migrated = %d", migrated)
	}

	after := mustWorkspace(t, store, "ws-1")
	if got := maskedWorkspaceJSON(t, after); got != before {
		t.Fatalf("migration changed more than the capability collection:\n before %s\n after  %s", before, got)
	}
	if !after.HasInstalledCapability(workspace.CapabilityFileJanitor) {
		t.Fatal("migration did not record the capability")
	}
	// Explicitly: the pre-existing grant is neither widened nor duplicated.
	if len(after.DirectoryReferences) != 1 || after.DirectoryReferences[0].Path != "/Users/someone/Downloads" {
		t.Fatalf("directory references changed: %+v", after.DirectoryReferences)
	}
	if len(after.MCPBindings) != 1 {
		t.Fatalf("MCP bindings changed: %+v", after.MCPBindings)
	}
	if !after.IsFromTemplate(LegacyDownloadsTemplateID) {
		t.Fatal("template provenance was lost")
	}
}

// --- Failure isolation (FR-139) ---------------------------------------------

// TestMigration_IsolatesOneWorkspaceFailure proves a workspace that cannot be
// migrated does not stop the pass, does not lose its legacy state, and can be
// retried later.
func TestMigration_IsolatesOneWorkspaceFailure(t *testing.T) {
	store := newMigrationStore(
		fromDownloadsTemplate("ws-broken", "Broken"),
		fromDownloadsTemplate("ws-ok-1", "Fine"),
		fromDownloadsTemplate("ws-ok-2", "Also fine"),
	)
	store.updateErrs["ws-broken"] = errors.New("state store unavailable")

	result := newTestMigrator(t, store, nil).Run()

	if result.Failed != 1 {
		t.Fatalf("failed = %d, want 1 (%+v)", result.Failed, result)
	}
	if result.Migrated != 2 {
		t.Fatalf("migrated = %d, want the other two to continue (%+v)", result.Migrated, result)
	}
	for _, id := range []string{"ws-ok-1", "ws-ok-2"} {
		if !mustWorkspace(t, store, id).HasInstalledCapability(workspace.CapabilityFileJanitor) {
			t.Fatalf("%s was not migrated despite an unrelated failure", id)
		}
	}

	broken := mustWorkspace(t, store, "ws-broken")
	if broken.HasInstalledCapability(workspace.CapabilityFileJanitor) {
		t.Fatal("a failed migration left a partial install record")
	}
	if !broken.IsFromTemplate(LegacyDownloadsTemplateID) {
		t.Fatal("the failed workspace's legacy provenance was disturbed")
	}

	// A later pass retries it once the store recovers.
	delete(store.updateErrs, "ws-broken")
	retry := newTestMigrator(t, store, nil).Run()
	if retry.Migrated != 1 || retry.Failed != 0 {
		t.Fatalf("retry did not recover: %+v", retry)
	}
}

func TestMigration_ListFailureIsReportedNotPanicked(t *testing.T) {
	store := newMigrationStore(fromDownloadsTemplate("ws-1", "Tidy"))
	store.listErr = errors.New("store unavailable")

	result := newTestMigrator(t, store, nil).Run()
	if result.Scanned != 0 || result.Migrated != 0 {
		t.Fatalf("expected an empty pass, got %+v", result)
	}
}

// TestMigration_DoesNothingWhenTheCapabilityIsNotCompiledIn guards against
// writing install records that resolve to nothing (FR-14): a record naming a
// capability this build cannot run is a station the user could never use.
func TestMigration_DoesNothingWhenTheCapabilityIsNotCompiledIn(t *testing.T) {
	emptyRegistry, err := NewRegistry()
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	store := newMigrationStore(fromDownloadsTemplate("ws-1", "Tidy"))

	result := NewMigrator(emptyRegistry, store, nil).Run()
	if result.Migrated != 0 {
		t.Fatalf("migrated %d workspaces without a compiled definition", result.Migrated)
	}
	if mustWorkspace(t, store, "ws-1").HasInstalledCapability(workspace.CapabilityFileJanitor) {
		t.Fatal("an install record was written for an uncompiled capability")
	}
}

func TestMigration_NilReceiverAndStoreAreSafe(t *testing.T) {
	var nilMigrator *Migrator
	if result := nilMigrator.Run(); result.Scanned != 0 {
		t.Fatalf("nil migrator did work: %+v", result)
	}
	if result := NewMigrator(nil, nil, nil).Run(); result.Scanned != 0 {
		t.Fatalf("unwired migrator did work: %+v", result)
	}
}

func mustWorkspace(t *testing.T, store *migrationStore, id string) *workspace.Workspace {
	t.Helper()
	ws, err := store.Get(id)
	if err != nil {
		t.Fatalf("Get(%s): %v", id, err)
	}
	return ws
}
