package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
)

func TestFileStore_SaveAndGetWorkspaceAgent(t *testing.T) {
	dir := t.TempDir()
	st, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	ws := newTestWorkspace("ws-snap-1", "Snap")
	if err := st.Save(ws); err != nil {
		t.Fatalf("Save workspace: %v", err)
	}

	if _, ok, err := st.GetWorkspaceAgent(ws.ID, "Manager"); err != nil || ok {
		t.Fatalf("expected no snapshot yet, got ok=%v err=%v", ok, err)
	}

	ag := &agent.Agent{Type: agent.TypeToolCalling}
	ag.Settings.Model = "gpt-5-nano"
	if err := st.SaveWorkspaceAgent(ws.ID, "Manager", ag); err != nil {
		t.Fatalf("SaveWorkspaceAgent: %v", err)
	}

	folder, err := st.GetFolderPath(ws.ID)
	if err != nil {
		t.Fatalf("GetFolderPath: %v", err)
	}
	snapshotPath := filepath.Join(folder, WorkspaceAgentsDir, "manager", WorkspaceAgentConfigFile)
	if _, err := os.Stat(snapshotPath); err != nil {
		t.Fatalf("expected snapshot at %s, got %v", snapshotPath, err)
	}

	got, ok, err := st.GetWorkspaceAgent(ws.ID, "Manager")
	if err != nil || !ok {
		t.Fatalf("read back: ok=%v err=%v", ok, err)
	}
	if got.Settings.Model != "gpt-5-nano" {
		t.Fatalf("expected model gpt-5-nano, got %q", got.Settings.Model)
	}
}

func newTestWorkspace(id, name string) *Workspace {
	return &Workspace{
		ID:         id,
		Name:       name,
		Status:     StatusActive,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
		SharedData: map[string]interface{}{},
	}
}

func TestFileStore_SaveAndGet(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	ws := newTestWorkspace("ws-1", "My Project")
	if err := store.Save(ws); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify folder structure on disk
	configPath := filepath.Join(dir, "my-project", WorkspaceConfigFile)
	if _, err := os.Stat(configPath); err != nil {
		t.Errorf("workspace.json not found at %s", configPath)
	}
	filesDir := filepath.Join(dir, "my-project", FilesDir)
	if _, err := os.Stat(filesDir); err != nil {
		t.Errorf("files/ dir not found at %s", filesDir)
	}

	// Retrieve by ID
	got, err := store.Get("ws-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "My Project" {
		t.Errorf("got Name=%q, want %q", got.Name, "My Project")
	}
	if got.FolderSlug != "my-project" {
		t.Errorf("got FolderSlug=%q, want %q", got.FolderSlug, "my-project")
	}
}

func TestFileStore_List(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	// Save two workspaces
	_ = store.Save(newTestWorkspace("ws-1", "Alpha"))
	_ = store.Save(newTestWorkspace("ws-2", "Beta"))

	ids, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(ids) != 2 {
		t.Errorf("List returned %d IDs, want 2", len(ids))
	}
}

func TestFileStore_Delete(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	ws := newTestWorkspace("ws-1", "To Delete")
	_ = store.Save(ws)

	if err := store.Delete("ws-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Folder should be gone
	folderPath := filepath.Join(dir, "to-delete")
	if _, err := os.Stat(folderPath); !os.IsNotExist(err) {
		t.Errorf("workspace folder still exists after delete")
	}

	// Get should fail
	if _, err := store.Get("ws-1"); err == nil {
		t.Error("Get after Delete should fail")
	}
}

func TestFileStore_DeleteNotFound(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	if err := store.Delete("nonexistent"); err == nil {
		t.Error("Delete of nonexistent workspace should fail")
	}
}

func TestFileStore_GetFilesPath(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	ws := newTestWorkspace("ws-1", "My Project")
	_ = store.Save(ws)

	got := store.GetFilesPath("ws-1")
	want := filepath.Join(dir, "my-project", FilesDir)
	if got != want {
		t.Errorf("GetFilesPath=%q, want %q", got, want)
	}
}

func TestFileStore_Rename(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	ws := newTestWorkspace("ws-1", "Old Name")
	_ = store.Save(ws)

	if err := store.Rename("ws-1", "New Name"); err != nil {
		t.Fatalf("Rename: %v", err)
	}

	// Old folder should not exist
	if _, err := os.Stat(filepath.Join(dir, "old-name")); !os.IsNotExist(err) {
		t.Error("old folder still exists after rename")
	}

	// New folder should exist
	if _, err := os.Stat(filepath.Join(dir, "new-name", WorkspaceConfigFile)); err != nil {
		t.Error("new folder workspace.json not found after rename")
	}

	// Get by same ID should work
	got, err := store.Get("ws-1")
	if err != nil {
		t.Fatalf("Get after rename: %v", err)
	}
	if got.Name != "New Name" {
		t.Errorf("Name=%q after rename, want %q", got.Name, "New Name")
	}
	if got.FolderSlug != "new-name" {
		t.Errorf("FolderSlug=%q after rename, want %q", got.FolderSlug, "new-name")
	}
}

func TestFileStore_RenameConflict(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	_ = store.Save(newTestWorkspace("ws-1", "Alpha"))
	_ = store.Save(newTestWorkspace("ws-2", "Beta"))

	// Rename Alpha to Beta should fail
	err = store.Rename("ws-1", "Beta")
	if err == nil {
		t.Fatal("Rename to existing name should fail")
	}

	var conflict *FolderSlugConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected FolderSlugConflictError, got %T", err)
	}
	if conflict.Slug != "beta" {
		t.Fatalf("expected conflicting slug %q, got %q", "beta", conflict.Slug)
	}
	if conflict.SuggestedSlug == "" || conflict.SuggestedSlug == "beta" {
		t.Fatalf("expected suggested slug for conflict, got %q", conflict.SuggestedSlug)
	}
}

func TestFileStore_LoadCacheOnStartup(t *testing.T) {
	dir := t.TempDir()

	// Create a workspace with one store instance
	store1, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	ws := newTestWorkspace("ws-1", "Persistent")
	_ = store1.Save(ws)

	// Create a new store instance — it should discover the workspace on disk
	store2, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore (reload): %v", err)
	}

	got, err := store2.Get("ws-1")
	if err != nil {
		t.Fatalf("Get from reloaded store: %v", err)
	}
	if got.Name != "Persistent" {
		t.Errorf("Name=%q, want %q", got.Name, "Persistent")
	}
}

func TestFileStore_SaveConflict(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	_ = store.Save(newTestWorkspace("ws-1", "My Project"))

	// Saving a different workspace with the same name should fail
	ws2 := newTestWorkspace("ws-2", "My Project")
	if err := store.Save(ws2); err == nil {
		t.Error("Save with conflicting folder name should fail")
	}
}

func TestFileStore_SaveUpdate(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	ws := newTestWorkspace("ws-1", "My Project")
	_ = store.Save(ws)

	// Saving the same workspace again (update) should succeed
	ws.Description = "updated"
	if err := store.Save(ws); err != nil {
		t.Fatalf("Save update: %v", err)
	}

	got, _ := store.Get("ws-1")
	if got.Description != "updated" {
		t.Errorf("Description=%q after update, want %q", got.Description, "updated")
	}
}

func TestFileStore_SaveUpdatePreservesCustomLocation(t *testing.T) {
	baseDir := t.TempDir()
	customRoot := t.TempDir()

	store, err := NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	ws := newTestWorkspace("ws-custom", "Custom Root")
	if err := store.SaveAt(ws, customRoot); err != nil {
		t.Fatalf("SaveAt: %v", err)
	}

	ws.Description = "updated"
	if err := store.Save(ws); err != nil {
		t.Fatalf("Save update: %v", err)
	}

	customConfig := filepath.Join(customRoot, "custom-root", WorkspaceConfigFile)
	if _, err := os.Stat(customConfig); err != nil {
		t.Fatalf("expected workspace.json at custom location: %v", err)
	}

	defaultConfig := filepath.Join(baseDir, "custom-root", WorkspaceConfigFile)
	if _, err := os.Stat(defaultConfig); !os.IsNotExist(err) {
		t.Fatalf("did not expect duplicate workspace under base path, stat err=%v", err)
	}

	gotPath, err := store.GetFolderPath("ws-custom")
	if err != nil {
		t.Fatalf("GetFolderPath: %v", err)
	}

	wantPath := filepath.Join(customRoot, "custom-root")
	if gotPath != wantPath {
		t.Fatalf("GetFolderPath=%q, want %q", gotPath, wantPath)
	}
}

func TestFileStore_RebindExistingFolderPreservesUnmirroredFields(t *testing.T) {
	baseDir := t.TempDir()
	store, err := NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	reboundFolder := filepath.Join(t.TempDir(), "rebound-workspace")
	if err := os.MkdirAll(filepath.Join(reboundFolder, FilesDir), 0755); err != nil {
		t.Fatalf("mkdir files dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(reboundFolder, NotesDir), 0755); err != nil {
		t.Fatalf("mkdir notes dir: %v", err)
	}

	diskWorkspace := newTestWorkspace("ws-rebind", "Disk Workspace")
	diskWorkspace.FolderSlug = "disk-workspace"
	diskWorkspace.AgentInstances = []AgentInstance{
		{
			ID:             "agent-1",
			Name:           "Planner",
			InstanceNumber: 1,
			NodeID:         "planner-node-1",
			CreatedAt:      time.Now(),
		},
	}
	diskWorkspace.SkillBindings = []WorkspaceSkillBinding{
		{
			ID:        "binding-1",
			SkillName: "workspace-planning",
			Enabled:   true,
			Trusted:   true,
		},
	}
	diskWorkspace.AgentSkillAccess = []WorkspaceAgentSkillAccess{
		{
			AgentInstanceID:   "agent-1",
			EnabledBindingIDs: []string{"binding-1"},
		},
	}

	data, err := diskWorkspace.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}
	if err := os.WriteFile(filepath.Join(reboundFolder, WorkspaceConfigFile), data, 0644); err != nil {
		t.Fatalf("write workspace.json: %v", err)
	}

	dbWorkspace := newTestWorkspace("ws-rebind", "Database Workspace")
	dbWorkspace.Description = "fresh database copy"
	dbWorkspace.FolderSlug = "database-workspace"

	if err := store.RebindExistingFolder(dbWorkspace, reboundFolder); err != nil {
		t.Fatalf("RebindExistingFolder: %v", err)
	}

	got, err := store.Get("ws-rebind")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "Database Workspace" {
		t.Fatalf("Name=%q, want %q", got.Name, "Database Workspace")
	}
	if got.Description != "fresh database copy" {
		t.Fatalf("Description=%q, want %q", got.Description, "fresh database copy")
	}
	if len(got.SkillBindings) != 1 || got.SkillBindings[0].SkillName != "workspace-planning" {
		t.Fatalf("expected preserved skill bindings, got %#v", got.SkillBindings)
	}
	if len(got.AgentSkillAccess) != 1 || got.AgentSkillAccess[0].AgentInstanceID != "agent-1" {
		t.Fatalf("expected preserved agent skill access, got %#v", got.AgentSkillAccess)
	}

	gotPath, err := store.GetFolderPath("ws-rebind")
	if err != nil {
		t.Fatalf("GetFolderPath: %v", err)
	}
	if gotPath != reboundFolder {
		t.Fatalf("GetFolderPath=%q, want %q", gotPath, reboundFolder)
	}

	rebuiltData, err := os.ReadFile(filepath.Join(reboundFolder, WorkspaceConfigFile))
	if err != nil {
		t.Fatalf("read rebound workspace.json: %v", err)
	}
	rebuilt, err := FromJSON(rebuiltData)
	if err != nil {
		t.Fatalf("FromJSON: %v", err)
	}
	if rebuilt.Description != "fresh database copy" {
		t.Fatalf("rebuilt Description=%q, want %q", rebuilt.Description, "fresh database copy")
	}
	if len(rebuilt.SkillBindings) != 1 || rebuilt.SkillBindings[0].SkillName != "workspace-planning" {
		t.Fatalf("expected preserved skill bindings in workspace.json, got %#v", rebuilt.SkillBindings)
	}
}

func TestFileStore_SubWorkspace(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	// Create parent
	parent := newTestWorkspace("ws-parent", "Parent Project")
	if err := store.Save(parent); err != nil {
		t.Fatalf("Save parent: %v", err)
	}

	// Create child
	child := newTestWorkspace("ws-child", "Child Module")
	child.ParentID = "ws-parent"
	if err := store.Save(child); err != nil {
		t.Fatalf("Save child: %v", err)
	}

	// Verify folder structure
	childConfig := filepath.Join(dir, "parent-project", SubWorkspacesDir, "child-module", WorkspaceConfigFile)
	if _, err := os.Stat(childConfig); err != nil {
		t.Errorf("child workspace.json not found at %s", childConfig)
	}

	// Get child by ID
	got, err := store.Get("ws-child")
	if err != nil {
		t.Fatalf("Get child: %v", err)
	}
	if got.Name != "Child Module" {
		t.Errorf("child Name=%q, want %q", got.Name, "Child Module")
	}
	if got.ParentID != "ws-parent" {
		t.Errorf("child ParentID=%q, want %q", got.ParentID, "ws-parent")
	}

	// List should include both
	ids, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(ids) != 2 {
		t.Errorf("List returned %d IDs, want 2", len(ids))
	}

	// Delete parent should cascade
	if err := store.Delete("ws-parent"); err != nil {
		t.Fatalf("Delete parent: %v", err)
	}
	if _, err := store.Get("ws-child"); err == nil {
		t.Error("Get child after parent delete should fail")
	}
}

func TestFileStore_NestingDepthLimit(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	// Create a chain of workspaces up to MaxNestingDepth
	parentID := ""
	for i := 0; i < MaxNestingDepth; i++ {
		ws := newTestWorkspace(fmt.Sprintf("ws-%d", i), fmt.Sprintf("Level %d", i))
		ws.ParentID = parentID
		if err := store.Save(ws); err != nil {
			t.Fatalf("Save level %d: %v", i, err)
		}
		parentID = ws.ID
	}

	// One more should fail
	tooDeep := newTestWorkspace("ws-too-deep", "Too Deep")
	tooDeep.ParentID = parentID
	if err := store.Save(tooDeep); err == nil {
		t.Error("Save beyond max nesting depth should fail")
	}
}

func TestFileStore_Import(t *testing.T) {
	storeDir := t.TempDir()
	store, err := NewFileStore(storeDir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	// Create a workspace folder externally (simulating an exported workspace)
	extDir := t.TempDir()
	wsDir := filepath.Join(extDir, "imported-project")
	_ = os.MkdirAll(filepath.Join(wsDir, FilesDir), 0755)

	ws := newTestWorkspace("ws-imported", "Imported Project")
	ws.FolderSlug = "imported-project"
	data, _ := ws.ToJSON()
	_ = os.WriteFile(filepath.Join(wsDir, WorkspaceConfigFile), data, 0644)

	// Import it
	imported, warning, err := store.Import(wsDir)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if warning != "" {
		t.Logf("Warning: %s", warning)
	}
	if imported.ID != "ws-imported" {
		t.Errorf("imported ID=%q, want %q", imported.ID, "ws-imported")
	}

	// Should be accessible by ID
	got, err := store.Get("ws-imported")
	if err != nil {
		t.Fatalf("Get after import: %v", err)
	}
	if got.Name != "Imported Project" {
		t.Errorf("Name=%q, want %q", got.Name, "Imported Project")
	}

	// The folder should exist under the store's basePath
	importedConfig := filepath.Join(storeDir, "imported-project", WorkspaceConfigFile)
	if _, err := os.Stat(importedConfig); err != nil {
		t.Errorf("imported workspace.json not found at %s", importedConfig)
	}
}

func TestFileStore_ImportConflict(t *testing.T) {
	storeDir := t.TempDir()
	store, err := NewFileStore(storeDir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	// Create an existing workspace
	_ = store.Save(newTestWorkspace("ws-existing", "My Project"))

	// Create an external workspace with the same slug
	extDir := t.TempDir()
	wsDir := filepath.Join(extDir, "my-project")
	_ = os.MkdirAll(wsDir, 0755)
	ws := newTestWorkspace("ws-other", "My Project")
	ws.FolderSlug = "my-project"
	data, _ := ws.ToJSON()
	_ = os.WriteFile(filepath.Join(wsDir, WorkspaceConfigFile), data, 0644)

	// Import should fail due to conflict
	_, _, err = store.Import(wsDir)
	if err == nil {
		t.Error("Import with conflicting folder name should fail")
	}
}

func TestFileStore_ImportInvalid(t *testing.T) {
	storeDir := t.TempDir()
	store, err := NewFileStore(storeDir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	// Import a folder without workspace.json
	emptyDir := t.TempDir()
	_, _, err = store.Import(emptyDir)
	if err == nil {
		t.Error("Import of folder without workspace.json should fail")
	}
}

func TestFileStore_ImportProjectPathWarning(t *testing.T) {
	storeDir := t.TempDir()
	store, err := NewFileStore(storeDir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	// Create a workspace with an unresolvable project_path
	extDir := t.TempDir()
	wsDir := filepath.Join(extDir, "with-path")
	_ = os.MkdirAll(wsDir, 0755)
	ws := newTestWorkspace("ws-path", "With Path")
	ws.FolderSlug = "with-path"
	ws.ProjectPath = "../../nonexistent-project"
	data, _ := ws.ToJSON()
	_ = os.WriteFile(filepath.Join(wsDir, WorkspaceConfigFile), data, 0644)

	_, warning, err := store.Import(wsDir)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if warning == "" {
		t.Error("Expected warning about unresolvable project_path")
	}
}

func TestFileStore_GetFolderPath(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	ws := newTestWorkspace("ws-1", "Test Project")
	_ = store.Save(ws)

	got, err := store.GetFolderPath("ws-1")
	if err != nil {
		t.Fatalf("GetFolderPath: %v", err)
	}
	want := filepath.Join(dir, "test-project")
	if got != want {
		t.Errorf("GetFolderPath=%q, want %q", got, want)
	}
}
