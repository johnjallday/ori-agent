package workspacecapability

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// memStore is a minimal workspace store with the one property that matters
// here: Update serializes read-modify-write per workspace, like the real store.
type memStore struct {
	mu         sync.Mutex
	workspaces map[string]*workspace.Workspace
	saveErr    error
	saves      int
}

func newMemStore(ws ...*workspace.Workspace) *memStore {
	s := &memStore{workspaces: make(map[string]*workspace.Workspace, len(ws))}
	for _, w := range ws {
		s.workspaces[w.ID] = w
	}
	return s
}

func (s *memStore) Get(id string) (*workspace.Workspace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ws, ok := s.workspaces[id]
	if !ok {
		return nil, errors.New("workspace not found")
	}
	return cloneWorkspace(ws), nil
}

func (s *memStore) Update(id string, fn func(*workspace.Workspace) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ws, ok := s.workspaces[id]
	if !ok {
		return errors.New("workspace not found")
	}
	working := cloneWorkspace(ws)
	if err := fn(working); err != nil {
		return err
	}
	if s.saveErr != nil {
		return s.saveErr
	}
	s.saves++
	s.workspaces[id] = working
	return nil
}

func cloneWorkspace(ws *workspace.Workspace) *workspace.Workspace {
	data, err := ws.ToJSON()
	if err != nil {
		panic(err)
	}
	clone, err := workspace.FromJSON(data)
	if err != nil {
		panic(err)
	}
	return clone
}

func testWorkspace() *workspace.Workspace {
	now := time.Date(2026, 7, 30, 9, 0, 0, 0, time.UTC)
	return &workspace.Workspace{
		ID:          "ws-1",
		Name:        "Research Notes",
		Kind:        "workspace",
		Description: "Unrelated existing workspace",
		ParentID:    "parent-1",
		OwnerUserID: "local",
		Status:      workspace.StatusActive,
		SharedData:  map[string]any{"existing": "value"},
		Tasks: []workspace.Task{
			{ID: "task-1", Description: "Existing task", Status: workspace.TaskStatusPending},
		},
		AgentInstances: []workspace.AgentInstance{
			{ID: "agent-1", Name: "Workspace Manager", NodeID: "manager-1", EntryPoint: true},
		},
		TemplateProvenance: &workspace.TemplateProvenance{TemplateID: "research", Builtin: true},
		CreatedAt:          now,
		UpdatedAt:          now,
	}
}

func newTestService(t *testing.T, store WorkspaceStore) *Service {
	t.Helper()
	return NewService(mustBuiltinRegistry(t), store)
}

type noOpPluginComponents struct{}

func (noOpPluginComponents) AttachCapability(string, Definition) error { return nil }
func (noOpPluginComponents) DetachCapability(string, Definition) error { return nil }

func TestService_PluginInstallPersistsOnlyInertOwnerProvenance(t *testing.T) {
	store := newMemStore(testWorkspace())
	registry := mustBuiltinRegistry(t)
	owner := pluginCapabilityOwner("weather-tools", "1.2.0")
	if err := registry.RegisterPluginDefinitions(owner, []Definition{pluginCapabilityDefinition(owner, "forecast")}); err != nil {
		t.Fatal(err)
	}
	svc := NewService(registry, store)
	svc.SetPluginComponentReconciler(noOpPluginComponents{})
	result, err := svc.Install(InstallRequest{WorkspaceID: "ws-1", CapabilityID: "forecast"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Record.Owner == nil || !result.Record.Owner.MatchesPlugin("weather-tools") || result.Record.Owner.PluginVersion != "1.2.0" {
		t.Fatalf("installed owner = %+v", result.Record.Owner)
	}
	stored, err := store.Get("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	record, ok := stored.GetInstalledCapability("forecast")
	if !ok || record.Owner == nil || !record.Owner.MatchesPlugin("weather-tools") {
		t.Fatalf("stored plugin capability = %+v, %v", record, ok)
	}
}

func TestService_CatalogListsFileJanitorAsAvailableAndNotInstalled(t *testing.T) {
	svc := newTestService(t, newMemStore(testWorkspace()))

	items, err := svc.Catalog("ws-1")
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	if len(items) != len(BuiltinDefinitions()) {
		t.Fatalf("expected every compiled catalog item, got %d", len(items))
	}
	item := items[0]
	if item.Definition.ID != workspace.CapabilityFileJanitor {
		t.Fatalf("unexpected capability: %q", item.Definition.ID)
	}
	if item.Installed {
		t.Fatal("File Janitor reported installed before any install")
	}
	if !item.Available {
		t.Fatal("a compiled definition must be available")
	}
	if item.Record != nil || item.Status != nil {
		t.Fatalf("an uninstalled capability must carry no record or status: %+v", item)
	}
}

func TestService_CatalogReportsInstalledStateAndDerivedStatus(t *testing.T) {
	store := newMemStore(testWorkspace())
	svc := newTestService(t, store)
	if err := svc.Registry().BindRuntime(workspace.CapabilityFileJanitor, &stubRuntime{
		status: Status{State: StatusSetupNeeded, Detail: "Choose a folder"},
	}); err != nil {
		t.Fatalf("BindRuntime: %v", err)
	}

	if _, err := svc.Install(InstallRequest{WorkspaceID: "ws-1", CapabilityID: workspace.CapabilityFileJanitor}); err != nil {
		t.Fatalf("Install: %v", err)
	}

	items, err := svc.Catalog("ws-1")
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	item := items[0]
	if !item.Installed || item.Record == nil {
		t.Fatalf("expected an installed record: %+v", item)
	}
	if item.Record.Source != workspace.InstallSourceInPlace {
		t.Fatalf("install source = %q", item.Record.Source)
	}
	if item.Status == nil || item.Status.State != StatusSetupNeeded {
		t.Fatalf("status must be derived from the runtime, got %+v", item.Status)
	}
}

// TestService_CatalogKeepsUnknownInstalledRecordVisible covers FR-14/FR-145: a
// workspace carrying an install this build cannot resolve must still list it,
// marked unavailable, without the catalog failing.
func TestService_CatalogKeepsUnknownInstalledRecordVisible(t *testing.T) {
	ws := testWorkspace()
	if _, err := ws.AddInstalledCapability(workspace.InstalledCapability{
		ID:          "capability-from-the-future",
		Version:     4,
		InstalledAt: time.Now(),
		Source:      workspace.InstallSourceBlueprint,
	}); err != nil {
		t.Fatalf("seed install: %v", err)
	}
	svc := newTestService(t, newMemStore(ws))

	items, err := svc.Catalog("ws-1")
	if err != nil {
		t.Fatalf("Catalog must not fail on an unknown install: %v", err)
	}
	if len(items) != len(BuiltinDefinitions())+1 {
		t.Fatalf("expected compiled definitions plus the unknown record, got %d", len(items))
	}

	unknown := items[len(items)-1]
	if unknown.Available {
		t.Fatal("an unknown capability must not be reported available")
	}
	if !unknown.Installed || unknown.Record == nil {
		t.Fatalf("the unknown record must stay visible: %+v", unknown)
	}
	if unknown.Status == nil || unknown.Status.State != StatusUnavailable {
		t.Fatalf("unknown capability status = %+v, want unavailable", unknown.Status)
	}
	if unknown.Definition.Console.PanelID != "" || unknown.Definition.API.Prefix != "" {
		t.Fatalf("the placeholder definition exposes surfaces: %+v", unknown.Definition)
	}
}

func TestService_InstallPersistsRecord(t *testing.T) {
	store := newMemStore(testWorkspace())
	svc := newTestService(t, store)

	result, err := svc.Install(InstallRequest{WorkspaceID: "ws-1", CapabilityID: workspace.CapabilityFileJanitor})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if result.AlreadyInstalled {
		t.Fatal("a first install must not report already-installed")
	}
	if result.Record.ID != workspace.CapabilityFileJanitor || result.Record.Version != FileJanitorDefinitionVersion {
		t.Fatalf("unexpected record: %+v", result.Record)
	}
	if result.Record.InstalledAt.IsZero() || result.Record.Source == "" {
		t.Fatalf("record is missing required provenance: %+v", result.Record)
	}

	stored, err := store.Get("ws-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !stored.HasInstalledCapability(workspace.CapabilityFileJanitor) {
		t.Fatal("install was not persisted")
	}
}

// TestService_InstallChangesNothingButTheCapabilityCollection is the structural
// proof for FR-10 and FR-19-FR-23. Rather than asserting a list of things that
// did not happen, it diffs the whole workspace before and after with only
// installed_capabilities and updated_at masked out: any other mutation — a
// rename, a reparent, a cleared task list, a replaced agent team, a new
// directory reference, dropped template provenance — fails this test.
func TestService_InstallChangesNothingButTheCapabilityCollection(t *testing.T) {
	store := newMemStore(testWorkspace())
	svc := newTestService(t, store)

	before, err := store.Get("ws-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	beforeJSON := maskedWorkspaceJSON(t, before)

	if _, err := svc.Install(InstallRequest{WorkspaceID: "ws-1", CapabilityID: workspace.CapabilityFileJanitor}); err != nil {
		t.Fatalf("Install: %v", err)
	}

	after, err := store.Get("ws-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	afterJSON := maskedWorkspaceJSON(t, after)

	if beforeJSON != afterJSON {
		t.Fatalf("install touched the workspace beyond its capability collection:\nbefore: %s\nafter:  %s", beforeJSON, afterJSON)
	}

	// And the one thing it was allowed to change, did change.
	if !after.HasInstalledCapability(workspace.CapabilityFileJanitor) {
		t.Fatal("install did not record the capability")
	}
}

// maskedWorkspaceJSON serializes a workspace with the fields install is allowed
// to touch removed, so the remainder can be compared byte-for-byte.
func maskedWorkspaceJSON(t *testing.T, ws *workspace.Workspace) string {
	t.Helper()
	data, err := ws.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	delete(envelope, "installed_capabilities")
	delete(envelope, "updated_at")
	delete(envelope, "version")
	masked, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(masked)
}

// TestService_InstallIsIdempotent covers FR-9: repeating an install must produce
// exactly one record and must not rewrite the original's provenance.
func TestService_InstallIsIdempotent(t *testing.T) {
	store := newMemStore(testWorkspace())
	svc := newTestService(t, store)

	first, err := svc.Install(InstallRequest{WorkspaceID: "ws-1", CapabilityID: workspace.CapabilityFileJanitor, Source: workspace.InstallSourceBlueprint})
	if err != nil {
		t.Fatalf("first install: %v", err)
	}

	for i := 0; i < 3; i++ {
		repeat, err := svc.Install(InstallRequest{WorkspaceID: "ws-1", CapabilityID: workspace.CapabilityFileJanitor, Source: workspace.InstallSourceLegacyMigration})
		if err != nil {
			t.Fatalf("repeat install %d: %v", i, err)
		}
		if !repeat.AlreadyInstalled {
			t.Fatalf("repeat install %d did not report already-installed", i)
		}
		if repeat.Record.Source != first.Record.Source {
			t.Fatalf("repeat install rewrote provenance: %q -> %q", first.Record.Source, repeat.Record.Source)
		}
		if !repeat.Record.InstalledAt.Equal(first.Record.InstalledAt) {
			t.Fatalf("repeat install rewrote the install timestamp")
		}
	}

	stored, err := store.Get("ws-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := len(stored.GetInstalledCapabilities()); got != 1 {
		t.Fatalf("expected exactly one install record, got %d", got)
	}
}

// TestService_ConcurrentInstallsProduceOneRecord exercises the same guarantee
// through the lock: the fast path can be raced past, so the check inside Update
// is what has to hold.
func TestService_ConcurrentInstallsProduceOneRecord(t *testing.T) {
	store := newMemStore(testWorkspace())
	svc := newTestService(t, store)

	const attempts = 8
	var wg sync.WaitGroup
	errs := make([]error, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = svc.Install(InstallRequest{WorkspaceID: "ws-1", CapabilityID: workspace.CapabilityFileJanitor})
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent install %d failed: %v", i, err)
		}
	}

	stored, err := store.Get("ws-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := len(stored.GetInstalledCapabilities()); got != 1 {
		t.Fatalf("expected exactly one install record after %d concurrent installs, got %d", attempts, got)
	}
}

func TestService_InstallRejectsUnknownCapability(t *testing.T) {
	store := newMemStore(testWorkspace())
	svc := newTestService(t, store)

	_, err := svc.Install(InstallRequest{WorkspaceID: "ws-1", CapabilityID: "made-up"})
	var lifecycleErr *Error
	if !errors.As(err, &lifecycleErr) {
		t.Fatalf("expected a lifecycle error, got %v", err)
	}
	if lifecycleErr.Code != CodeCapabilityUnavailable {
		t.Fatalf("code = %q, want %q", lifecycleErr.Code, CodeCapabilityUnavailable)
	}

	stored, _ := store.Get("ws-1")
	if len(stored.GetInstalledCapabilities()) != 0 {
		t.Fatalf("a rejected install still wrote a record: %+v", stored.GetInstalledCapabilities())
	}
}

func TestService_InstallRejectsUnknownWorkspace(t *testing.T) {
	svc := newTestService(t, newMemStore(testWorkspace()))

	_, err := svc.Install(InstallRequest{WorkspaceID: "does-not-exist", CapabilityID: workspace.CapabilityFileJanitor})
	var lifecycleErr *Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Code != CodeWorkspaceMissing {
		t.Fatalf("expected workspace_missing, got %v", err)
	}

	if _, err := svc.Catalog(""); err == nil {
		t.Fatal("an empty workspace id must not resolve")
	}
}

func TestService_InstallReportsPersistenceFailureWithoutWriting(t *testing.T) {
	store := newMemStore(testWorkspace())
	store.saveErr = errors.New("disk full")
	svc := newTestService(t, store)

	_, err := svc.Install(InstallRequest{WorkspaceID: "ws-1", CapabilityID: workspace.CapabilityFileJanitor})
	var lifecycleErr *Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Code != CodeInstallFailed {
		t.Fatalf("expected install_failed, got %v", err)
	}
	if !strings.Contains(lifecycleErr.Message, "Nothing was changed") {
		t.Fatalf("the user-facing message should say nothing changed: %q", lifecycleErr.Message)
	}

	stored, _ := store.Get("ws-1")
	if len(stored.GetInstalledCapabilities()) != 0 {
		t.Fatalf("a failed install left a record behind: %+v", stored.GetInstalledCapabilities())
	}
}

// failingInstaller is a runtime whose capability-specific install step fails,
// exercising the rollback path (FR-15).
type failingInstaller struct {
	stubRuntime
	installErr      error
	automationStops int
	stopErr         error
}

func (f *failingInstaller) OnCapabilityInstall(string) error { return f.installErr }

func (f *failingInstaller) StopCapabilityAutomation(string) error {
	f.automationStops++
	return f.stopErr
}

// TestService_InstallRollsBackAfterFailedInstallHook covers FR-15: a partial
// install must leave no record — so no station, catalog entry, or status can
// claim the capability is active — and must stop any automation the failed step
// started.
func TestService_InstallRollsBackAfterFailedInstallHook(t *testing.T) {
	store := newMemStore(testWorkspace())
	svc := newTestService(t, store)
	runtime := &failingInstaller{installErr: errors.New("could not reach the folder")}
	if err := svc.Registry().BindRuntime(workspace.CapabilityFileJanitor, runtime); err != nil {
		t.Fatalf("BindRuntime: %v", err)
	}

	_, err := svc.Install(InstallRequest{WorkspaceID: "ws-1", CapabilityID: workspace.CapabilityFileJanitor})
	var lifecycleErr *Error
	if !errors.As(err, &lifecycleErr) {
		t.Fatalf("expected a lifecycle error, got %v", err)
	}
	if lifecycleErr.Code != CodeInstallFailed {
		t.Fatalf("code = %q, want %q", lifecycleErr.Code, CodeInstallFailed)
	}
	if runtime.automationStops != 1 {
		t.Fatalf("rollback stopped automation %d times, want 1", runtime.automationStops)
	}

	stored, _ := store.Get("ws-1")
	if len(stored.GetInstalledCapabilities()) != 0 {
		t.Fatalf("rollback left an install record behind: %+v", stored.GetInstalledCapabilities())
	}

	// The catalog must agree: nothing installed, so no misleading station.
	items, err := svc.Catalog("ws-1")
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	if items[0].Installed {
		t.Fatal("the catalog still shows the rolled-back capability as installed")
	}
}

// TestService_InstallReportsIncompleteWhenRollbackAlsoFails proves the one case
// where a record may survive a failure: the user gets a distinct, repairable
// code rather than a silent half-install.
func TestService_InstallReportsIncompleteWhenRollbackAlsoFails(t *testing.T) {
	installErr := errors.New("install step failed")

	// The install record writes, the capability's install step fails, and the
	// rollback write then fails too — the only path that may leave a record.
	store := &breakAfterFirstSaveStore{memStore: newMemStore(testWorkspace())}
	svc := newTestService(t, store)
	if err := svc.Registry().BindRuntime(workspace.CapabilityFileJanitor, &failingInstaller{installErr: installErr}); err != nil {
		t.Fatalf("BindRuntime: %v", err)
	}

	_, err := svc.Install(InstallRequest{WorkspaceID: "ws-1", CapabilityID: workspace.CapabilityFileJanitor})
	var lifecycleErr *Error
	if !errors.As(err, &lifecycleErr) {
		t.Fatalf("expected a lifecycle error, got %v", err)
	}
	if lifecycleErr.Code != CodeInstallIncomplete {
		t.Fatalf("code = %q, want %q", lifecycleErr.Code, CodeInstallIncomplete)
	}
	if lifecycleErr.Repair == "" {
		t.Fatal("an incomplete install must name a repair action")
	}
	if !errors.Is(lifecycleErr, installErr) {
		t.Fatalf("the original cause should be preserved for logs: %v", lifecycleErr)
	}
}

// breakAfterFirstSaveStore lets exactly one Update succeed, then fails.
type breakAfterFirstSaveStore struct {
	*memStore
	updates int
}

func (s *breakAfterFirstSaveStore) Update(id string, fn func(*workspace.Workspace) error) error {
	s.updates++
	if s.updates > 1 {
		return errors.New("store unavailable")
	}
	return s.memStore.Update(id, fn)
}

func TestService_InstallDefaultsSourceToInPlace(t *testing.T) {
	store := newMemStore(testWorkspace())
	svc := newTestService(t, store)

	result, err := svc.Install(InstallRequest{WorkspaceID: "ws-1", CapabilityID: "  FILE-JANITOR  ", Source: "   "})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if result.Record.Source != workspace.InstallSourceInPlace {
		t.Fatalf("source = %q, want %q", result.Record.Source, workspace.InstallSourceInPlace)
	}
	if result.Record.ID != workspace.CapabilityFileJanitor {
		t.Fatalf("capability id was not normalized: %q", result.Record.ID)
	}
}
