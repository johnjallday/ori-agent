package workspace

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
)

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
