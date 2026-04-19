package sessionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/session"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

func TestHandleWorkspaceSyncStatusAndLocateMissingWorkspaceFolder(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	baseDir := t.TempDir()
	fileStore, err := agentworkspace.NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = fileStore.Close() }()
	handler.SetWorkspaceStore(fileStore)

	workspaceID := createTestWorkspace(t, handler, "Sync Recover")

	originalPath, err := fileStore.GetFolderPath(workspaceID)
	if err != nil {
		t.Fatalf("GetFolderPath: %v", err)
	}

	relocatedRoot := t.TempDir()
	relocatedPath := filepath.Join(relocatedRoot, filepath.Base(originalPath))
	if err := os.Rename(originalPath, relocatedPath); err != nil {
		t.Fatalf("Rename workspace folder: %v", err)
	}
	expectedRelocatedPath, err := normalizeImportPath(relocatedPath)
	if err != nil {
		t.Fatalf("normalize relocated path: %v", err)
	}

	statusReq := httptest.NewRequest(http.MethodGet, "/api/workspaces/sync-status", nil)
	statusW := httptest.NewRecorder()
	handler.HandleWorkspaces(statusW, statusReq)
	if statusW.Code != http.StatusOK {
		t.Fatalf("expected 200 for sync status, got %d: %s", statusW.Code, statusW.Body.String())
	}

	var status agentworkspace.SyncStatus
	if err := json.Unmarshal(statusW.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode sync status: %v", err)
	}
	if status.InSync {
		t.Fatalf("expected sync mismatch after moving workspace folder")
	}
	if len(status.Orphaned) != 1 {
		t.Fatalf("expected 1 missing workspace, got %#v", status.Orphaned)
	}
	if status.Orphaned[0].ID != workspaceID {
		t.Fatalf("expected missing workspace %q, got %#v", workspaceID, status.Orphaned[0])
	}
	if status.Orphaned[0].Path != originalPath {
		t.Fatalf("expected last known path %q, got %q", originalPath, status.Orphaned[0].Path)
	}

	payload, err := json.Marshal(map[string]interface{}{
		"locate": []map[string]string{
			{
				"id":   workspaceID,
				"path": relocatedPath,
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal sync request: %v", err)
	}

	syncReq := httptest.NewRequest(http.MethodPost, "/api/workspaces/sync", bytes.NewBuffer(payload))
	syncReq.Header.Set("Content-Type", "application/json")
	syncW := httptest.NewRecorder()
	handler.HandleWorkspaces(syncW, syncReq)
	if syncW.Code != http.StatusOK {
		t.Fatalf("expected 200 for sync apply, got %d: %s", syncW.Code, syncW.Body.String())
	}

	var syncResp map[string]interface{}
	if err := json.Unmarshal(syncW.Body.Bytes(), &syncResp); err != nil {
		t.Fatalf("decode sync response: %v", err)
	}
	if got := int(syncResp["located"].(float64)); got != 1 {
		t.Fatalf("expected located=1, got %d", got)
	}

	updatedPath, err := fileStore.GetFolderPath(workspaceID)
	if err != nil {
		t.Fatalf("GetFolderPath after locate: %v", err)
	}
	normalizedUpdatedPath, err := normalizeImportPath(updatedPath)
	if err != nil {
		t.Fatalf("normalize updated path: %v", err)
	}
	if normalizedUpdatedPath != expectedRelocatedPath {
		t.Fatalf("expected rebound path %q, got %q", expectedRelocatedPath, updatedPath)
	}

	workspace, err := handler.store.GetWorkspace(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}

	refs, err := decodeDirectoryReferences(workspace.DirectoryReferencesJSON)
	if err != nil {
		t.Fatalf("decode directory references: %v", err)
	}
	if len(refs) == 0 {
		t.Fatalf("expected directory references after locate")
	}
	foundRef := false
	for _, ref := range refs {
		if cleanWorkspaceSyncPath(ref.Path) == expectedRelocatedPath {
			foundRef = true
			break
		}
	}
	if !foundRef {
		t.Fatalf("expected a directory reference path %q, got %#v", expectedRelocatedPath, refs)
	}

	bindings, err := decodeWorkspaceMCPBindings(workspace.MCPBindingsJSON)
	if err != nil {
		t.Fatalf("decode mcp bindings: %v", err)
	}
	if len(bindings) == 0 {
		t.Fatalf("expected workspace-files binding after locate")
	}
	foundBinding := false
	for _, binding := range bindings {
		if workspaceBindingHasRoot(binding.Config, expectedRelocatedPath) {
			foundBinding = true
			break
		}
	}
	if !foundBinding {
		t.Fatalf("expected workspace-files binding roots to include %q, got %#v", expectedRelocatedPath, bindings)
	}
}

func TestHandleWorkspaceSyncStatusSkipsImportedWorkspaceFolders(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	baseDir := t.TempDir()
	fileStore, err := agentworkspace.NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = fileStore.Close() }()
	handler.SetWorkspaceStore(fileStore)

	importDir := filepath.Join(t.TempDir(), "import-target")
	if err := os.MkdirAll(importDir, 0755); err != nil {
		t.Fatalf("create import dir: %v", err)
	}

	payload, err := json.Marshal(map[string]interface{}{
		"path": importDir,
	})
	if err != nil {
		t.Fatalf("marshal import payload: %v", err)
	}

	importReq := httptest.NewRequest(http.MethodPost, "/api/workspaces/import", bytes.NewBuffer(payload))
	importReq.Header.Set("Content-Type", "application/json")
	importW := httptest.NewRecorder()
	handler.HandleWorkspaces(importW, importReq)
	if importW.Code != http.StatusCreated {
		t.Fatalf("expected 201 for workspace import, got %d: %s", importW.Code, importW.Body.String())
	}

	statusReq := httptest.NewRequest(http.MethodGet, "/api/workspaces/sync-status", nil)
	statusW := httptest.NewRecorder()
	handler.HandleWorkspaces(statusW, statusReq)
	if statusW.Code != http.StatusOK {
		t.Fatalf("expected 200 for sync status, got %d: %s", statusW.Code, statusW.Body.String())
	}

	var status agentworkspace.SyncStatus
	if err := json.Unmarshal(statusW.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode sync status: %v", err)
	}
	if len(status.Orphaned) != 0 {
		t.Fatalf("expected imported workspace to be skipped, got %#v", status.Orphaned)
	}
}

func TestHandleWorkspaceSyncImportPreservesDiskWorkspaceMetadata(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	baseDir := t.TempDir()
	fileStore, err := agentworkspace.NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = fileStore.Close() }()
	handler.SetWorkspaceStore(fileStore)

	now := time.Now().UTC().Round(time.Second)
	folderPath := filepath.Join(baseDir, "disk-workspace")
	diskWorkspace := &agentworkspace.Workspace{
		ID:          "disk-ws-1",
		Name:        "Disk Workspace",
		Kind:        "workspace",
		Description: "Loaded from workspace.json",
		FolderSlug:  "disk-workspace",
		SharedData: map[string]interface{}{
			"source": "disk",
		},
		DirectoryReferences: []agentworkspace.DirectoryReference{
			{
				ID:          "dir-1",
				WorkspaceID: "disk-ws-1",
				Name:        "disk-workspace",
				Path:        folderPath,
				CreatedAt:   now,
				UpdatedAt:   now,
			},
		},
		MCPBindings: []agentworkspace.WorkspaceMCPBinding{
			{
				ID:         "binding-1",
				ServerName: "filesystem",
				Alias:      "workspace-files",
				Enabled:    true,
				Config: map[string]interface{}{
					"roots": []string{folderPath},
				},
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
		Status:    agentworkspace.StatusActive,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := fileStore.Save(diskWorkspace); err != nil {
		t.Fatalf("Save disk workspace: %v", err)
	}

	payload, err := json.Marshal(map[string]interface{}{
		"import": []string{"disk-ws-1"},
	})
	if err != nil {
		t.Fatalf("marshal sync request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/sync", bytes.NewBuffer(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for sync import, got %d: %s", w.Code, w.Body.String())
	}

	workspace, err := handler.store.GetWorkspace(context.Background(), "disk-ws-1")
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}

	if workspace.SharedData == nil || workspace.SharedData["source"] != "disk" {
		t.Fatalf("expected shared_data from disk workspace, got %#v", workspace.SharedData)
	}

	refs, err := decodeDirectoryReferences(workspace.DirectoryReferencesJSON)
	if err != nil {
		t.Fatalf("decode directory references: %v", err)
	}
	if len(refs) != 1 || cleanWorkspaceSyncPath(refs[0].Path) != cleanWorkspaceSyncPath(folderPath) {
		t.Fatalf("expected imported directory reference %q, got %#v", folderPath, refs)
	}

	bindings, err := decodeWorkspaceMCPBindings(workspace.MCPBindingsJSON)
	if err != nil {
		t.Fatalf("decode mcp bindings: %v", err)
	}
	if len(bindings) != 1 || !workspaceBindingHasRoot(bindings[0].Config, folderPath) {
		t.Fatalf("expected imported workspace-files binding for %q, got %#v", folderPath, bindings)
	}
}

func TestListWorkspacesHydratesDirectoryReferencesFromDiskWorkspace(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	baseDir := t.TempDir()
	fileStore, err := agentworkspace.NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = fileStore.Close() }()
	handler.SetWorkspaceStore(fileStore)

	now := time.Now().UTC().Round(time.Second)
	folderPath := filepath.Join(baseDir, "hydrated-workspace")
	diskWorkspace := &agentworkspace.Workspace{
		ID:          "hydrated-ws-1",
		Name:        "Hydrated Workspace",
		Kind:        "workspace",
		Description: "Hydrate list response",
		FolderSlug:  "hydrated-workspace",
		DirectoryReferences: []agentworkspace.DirectoryReference{
			{
				ID:          "dir-1",
				WorkspaceID: "hydrated-ws-1",
				Name:        "hydrated-workspace",
				Path:        folderPath,
				CreatedAt:   now,
				UpdatedAt:   now,
			},
		},
		Status:    agentworkspace.StatusActive,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := fileStore.Save(diskWorkspace); err != nil {
		t.Fatalf("Save disk workspace: %v", err)
	}

	if err := handler.store.CreateWorkspace(context.Background(), &session.Workspace{
		ID:          "hydrated-ws-1",
		Name:        "Hydrated Workspace",
		FolderSlug:  "hydrated-workspace",
		Description: "Hydrate list response",
	}); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces?tree=true", nil)
	w := httptest.NewRecorder()
	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for workspace tree, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Workspaces []struct {
			ID                  string `json:"id"`
			FolderSlug          string `json:"folder_slug"`
			DirectoryReferences []struct {
				Path string `json:"path"`
			} `json:"directory_references"`
		} `json:"workspaces"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode workspace tree: %v", err)
	}

	if len(resp.Workspaces) != 1 {
		t.Fatalf("expected 1 workspace, got %#v", resp.Workspaces)
	}
	if resp.Workspaces[0].FolderSlug != "hydrated-workspace" {
		t.Fatalf("expected hydrated folder_slug, got %#v", resp.Workspaces[0].FolderSlug)
	}
	if len(resp.Workspaces[0].DirectoryReferences) != 1 {
		t.Fatalf("expected hydrated directory references, got %#v", resp.Workspaces[0].DirectoryReferences)
	}
	if cleanWorkspaceSyncPath(resp.Workspaces[0].DirectoryReferences[0].Path) != cleanWorkspaceSyncPath(folderPath) {
		t.Fatalf("expected hydrated directory path %q, got %#v", folderPath, resp.Workspaces[0].DirectoryReferences)
	}
}

func TestHandleWorkspaceSyncRecreateMissingWorkspaceFolder(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	baseDir := t.TempDir()
	fileStore, err := agentworkspace.NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = fileStore.Close() }()
	handler.SetWorkspaceStore(fileStore)

	workspaceID := createTestWorkspace(t, handler, "Recreate Recover")

	note := &session.WorkspaceNote{
		ID:          "note-recreate-1",
		WorkspaceID: workspaceID,
		Name:        "Recovery Plan",
		Content:     "restore from database",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := handler.store.CreateNote(context.Background(), note); err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	originalPath, err := fileStore.GetFolderPath(workspaceID)
	if err != nil {
		t.Fatalf("GetFolderPath: %v", err)
	}
	if err := os.RemoveAll(originalPath); err != nil {
		t.Fatalf("RemoveAll workspace folder: %v", err)
	}

	payload, err := json.Marshal(map[string]interface{}{
		"recreate": []string{workspaceID},
	})
	if err != nil {
		t.Fatalf("marshal recreate payload: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/sync", bytes.NewBuffer(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for sync recreate, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode recreate response: %v", err)
	}
	if got := int(resp["recreated"].(float64)); got != 1 {
		t.Fatalf("expected recreated=1, got %d", got)
	}

	if _, err := os.Stat(filepath.Join(originalPath, agentworkspace.WorkspaceConfigFile)); err != nil {
		t.Fatalf("expected recreated workspace.json, got error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(originalPath, agentworkspace.FilesDir)); err != nil {
		t.Fatalf("expected recreated files directory, got error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(originalPath, agentworkspace.NotesDir)); err != nil {
		t.Fatalf("expected recreated notes directory, got error: %v", err)
	}

	notePath := filepath.Join(originalPath, agentworkspace.NotesDir, agentworkspace.NoteFilename(note.Name, note.ID))
	noteBytes, err := os.ReadFile(notePath)
	if err != nil {
		t.Fatalf("expected recreated note file, got error: %v", err)
	}
	if !bytes.Contains(noteBytes, []byte(note.Content)) {
		t.Fatalf("expected recreated note file to contain note content, got %q", string(noteBytes))
	}
}
