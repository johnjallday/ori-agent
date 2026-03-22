package workspace

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSyncStore_SaveSyncsToDisk(t *testing.T) {
	// Set up an in-memory primary store and a file-based sync store
	primary := NewInMemoryStore()
	dir := t.TempDir()
	fileSync, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer fileSync.Close()

	store := NewSyncStore(primary, fileSync)

	now := time.Now()
	ws := &Workspace{
		ID:         "ws-sync-test",
		Name:       "Sync Test",
		Status:     StatusActive,
		CreatedAt:  now,
		UpdatedAt:  now,
		SharedData: make(map[string]interface{}),
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
	defer fileSync.Close()

	store := NewSyncStore(primary, fileSync)

	now := time.Now()
	ws := &Workspace{
		ID:         "ws-update-test",
		Name:       "Before Update",
		Status:     StatusActive,
		CreatedAt:  now,
		UpdatedAt:  now,
		SharedData: make(map[string]interface{}),
		MCPBindings: []WorkspaceMCPBinding{
			{ID: "mcp-1", ServerName: "test-server", Enabled: true},
		},
		SkillBindings: []WorkspaceSkillBinding{
			{ID: "skill-1", SkillName: "test-skill", Enabled: true},
		},
	}

	// First save
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}

	// Update with new data
	ws.MCPBindings = append(ws.MCPBindings, WorkspaceMCPBinding{
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

func TestSyncStore_DeleteRemovesFromBoth(t *testing.T) {
	primary := NewInMemoryStore()
	dir := t.TempDir()
	fileSync, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer fileSync.Close()

	store := NewSyncStore(primary, fileSync)

	now := time.Now()
	ws := &Workspace{
		ID:         "ws-delete-test",
		Name:       "Delete Me",
		Status:     StatusActive,
		CreatedAt:  now,
		UpdatedAt:  now,
		SharedData: make(map[string]interface{}),
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
	defer fileSync.Close()

	store := NewSyncStore(primary, fileSync)

	now := time.Now()
	ws := &Workspace{
		ID:         "ws-get-test",
		Name:       "Get Test",
		Status:     StatusActive,
		CreatedAt:  now,
		UpdatedAt:  now,
		SharedData: make(map[string]interface{}),
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
	defer fileSync.Close()

	store := NewSyncStore(primary, fileSync)

	now := time.Now()
	ws := &Workspace{
		ID:         "ws-files-test",
		Name:       "Files Test",
		Status:     StatusActive,
		CreatedAt:  now,
		UpdatedAt:  now,
		SharedData: make(map[string]interface{}),
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
	defer fileSync.Close()

	store := NewSyncStore(primary, fileSync)

	if store.FileStore() != fileSync {
		t.Error("FileStore() should return the underlying FileStore")
	}
}
