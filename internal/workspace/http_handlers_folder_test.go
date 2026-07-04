package workspace

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHTTPHandlerCreateWorkspaceFolderAndFileTree(t *testing.T) {
	store, ws, handler := newFolderHandlerTest(t, "ws-folder-create", "Folder Create")

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+ws.ID+"/folders", bytes.NewBufferString(`{"path":"research"}`))
	rr := httptest.NewRecorder()
	handler.CreateWorkspaceFolder(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", rr.Code, rr.Body.String())
	}

	stored, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("Get workspace: %v", err)
	}
	if len(stored.Folders) != 1 {
		t.Fatalf("expected 1 folder, got %d", len(stored.Folders))
	}
	if stored.Folders[0].Path != "research" {
		t.Fatalf("expected folder path research, got %q", stored.Folders[0].Path)
	}
	if _, err := os.Stat(filepath.Join(store.GetFilesPath(ws.ID), "research")); err != nil {
		t.Fatalf("expected folder on disk: %v", err)
	}

	if err := os.WriteFile(filepath.Join(store.GetFilesPath(ws.ID), "loose.txt"), []byte("loose"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	treeReq := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+ws.ID+"/files/tree", nil)
	treeRR := httptest.NewRecorder()
	handler.GetWorkspaceFilesTree(treeRR, treeReq)
	if treeRR.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", treeRR.Code, treeRR.Body.String())
	}
	files := decodeFileTreeResponse(t, treeRR.Body.Bytes())
	assertFileInfo(t, files, "research", true)
	assertFileInfo(t, files, "loose.txt", false)
}

func TestGetWorkspaceFilesTreeIndexesRPPAndOmitsHiddenFiles(t *testing.T) {
	store, ws, handler := newFolderHandlerTest(t, "ws-tree-rpp-hidden", "Tree RPP Hidden")
	filesPath := store.GetFilesPath(ws.ID)
	if err := os.WriteFile(filepath.Join(filesPath, ".DS_Store"), []byte("hidden"), 0644); err != nil {
		t.Fatalf("WriteFile .DS_Store: %v", err)
	}
	hiddenChild := filepath.Join(filesPath, ".cache", "data.json")
	if err := os.MkdirAll(filepath.Dir(hiddenChild), 0755); err != nil {
		t.Fatalf("MkdirAll hidden child: %v", err)
	}
	if err := os.WriteFile(hiddenChild, []byte("{}"), 0644); err != nil {
		t.Fatalf("WriteFile hidden child: %v", err)
	}
	rppPath := filepath.Join(filesPath, "House Test.RPP")
	if err := os.WriteFile(rppPath, []byte("<REAPER_PROJECT 0.1 \"7.0\" 1690000000\n>"), 0644); err != nil {
		t.Fatalf("WriteFile RPP: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+ws.ID+"/files/tree", nil)
	rr := httptest.NewRecorder()
	handler.GetWorkspaceFilesTree(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	files := decodeFileTreeResponse(t, rr.Body.Bytes())
	rpp := findFileInfo(files, "House Test.RPP")
	if rpp == nil {
		t.Fatalf("expected RPP file in tree, got %#v", files)
	}
	if rpp.AttachmentID == "" {
		t.Fatalf("expected RPP file to be indexed as an attachment, got %#v", rpp)
	}
	if findFileInfo(files, ".DS_Store") != nil {
		t.Fatalf("expected .DS_Store to be omitted from tree")
	}
	if findFileInfo(files, ".cache") != nil || findFileInfo(files, filepath.Join(".cache", "data.json")) != nil {
		t.Fatalf("expected hidden directory entries to be omitted from tree, got %#v", files)
	}

	stored, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("Get workspace: %v", err)
	}
	if len(stored.Attachments) != 1 {
		t.Fatalf("expected exactly one indexed attachment, got %#v", stored.Attachments)
	}
	if stored.Attachments[0].File == nil || stored.Attachments[0].File.RelativePath != "House Test.RPP" {
		t.Fatalf("expected indexed RPP attachment, got %#v", stored.Attachments[0].File)
	}
}

func TestHTTPHandlerCreateWorkspaceFolderRejectsTraversalAndFileCollision(t *testing.T) {
	store, ws, handler := newFolderHandlerTest(t, "ws-folder-invalid", "Folder Invalid")

	traversalReq := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+ws.ID+"/folders", bytes.NewBufferString(`{"path":"../outside"}`))
	traversalRR := httptest.NewRecorder()
	handler.CreateWorkspaceFolder(traversalRR, traversalReq)
	if traversalRR.Code != http.StatusBadRequest {
		t.Fatalf("expected traversal status 400, got %d: %s", traversalRR.Code, traversalRR.Body.String())
	}

	if err := os.WriteFile(filepath.Join(store.GetFilesPath(ws.ID), "collision"), []byte("file"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	collisionReq := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+ws.ID+"/folders", bytes.NewBufferString(`{"path":"collision"}`))
	collisionRR := httptest.NewRecorder()
	handler.CreateWorkspaceFolder(collisionRR, collisionReq)
	if collisionRR.Code != http.StatusConflict {
		t.Fatalf("expected collision status 409, got %d: %s", collisionRR.Code, collisionRR.Body.String())
	}
}

func TestHTTPHandlerRenameWorkspaceFolderUpdatesNestedMetadataAndFiles(t *testing.T) {
	store, ws, handler := newFolderHandlerTest(t, "ws-folder-rename", "Folder Rename")
	filesPath := store.GetFilesPath(ws.ID)
	if err := os.MkdirAll(filepath.Join(filesPath, "research"), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(filesPath, "research", "spec.md"), []byte("spec"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	now := time.Now()
	ws.Folders = []Folder{{ID: "folder-1", Path: "research", CreatedAt: now, UpdatedAt: now}}
	ws.StoreNodes = []StoreNode{
		{
			ID:            "store-1",
			CanvasNodeID:  "store-node-1",
			WorkspaceID:   ws.ID,
			Name:          "Reports",
			BaseDir:       filepath.Join("research", "exports"),
			StorageTarget: StorageTargetWorkspaceFolder,
			Folder:        filepath.Join("research", "exports"),
			Format:        "csv",
			WriteMode:     "append",
			CreatedAt:     now,
			UpdatedAt:     now,
		},
	}
	ws.Tasks = []Task{
		{
			ID:          "task-1",
			WorkspaceID: ws.ID,
			Description: "Store result",
			Status:      TaskStatusPending,
			ResultStorage: &ResultStorageConfig{
				Enabled:       true,
				StorageTarget: StorageTargetWorkspaceFolder,
				Folder:        filepath.Join("research", "results"),
				FileName:      "runs.csv",
				Format:        "csv",
				WriteMode:     "append",
			},
			CreatedAt: now,
		},
	}
	if err := ws.AddAttachment(Attachment{
		ID:          "att-1",
		WorkspaceID: ws.ID,
		Title:       "Spec",
		Type:        AttachmentTypeDoc,
		File: &AttachmentFileMeta{
			Name:         "spec.md",
			RelativePath: filepath.Join("research", "spec.md"),
			URL:          workspaceFileURL(ws.ID, filepath.Join("research", "spec.md")),
		},
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("AddAttachment: %v", err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("Save workspace: %v", err)
	}

	req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/"+ws.ID+"/folders/folder-1", bytes.NewBufferString(`{"path":"archive"}`))
	rr := httptest.NewRecorder()
	handler.UpdateWorkspaceFolder(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(filesPath, "archive", "spec.md")); err != nil {
		t.Fatalf("expected renamed file on disk: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filesPath, "research")); !os.IsNotExist(err) {
		t.Fatalf("expected old folder to be gone, got err=%v", err)
	}

	stored, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("Get workspace: %v", err)
	}
	if stored.Folders[0].Path != "archive" {
		t.Fatalf("expected folder path archive, got %q", stored.Folders[0].Path)
	}
	attachment, err := stored.GetAttachment("att-1")
	if err != nil {
		t.Fatalf("GetAttachment: %v", err)
	}
	wantRelativePath := filepath.Join("archive", "spec.md")
	if attachment.File.RelativePath != wantRelativePath {
		t.Fatalf("expected attachment relative path %q, got %q", wantRelativePath, attachment.File.RelativePath)
	}
	if attachment.File.URL != workspaceFileURL(ws.ID, wantRelativePath) {
		t.Fatalf("expected attachment URL to be updated, got %q", attachment.File.URL)
	}
	if stored.StoreNodes[0].Folder != filepath.Join("archive", "exports") {
		t.Fatalf("expected store node workspace folder archive/exports, got %q", stored.StoreNodes[0].Folder)
	}
	if stored.StoreNodes[0].BaseDir != filepath.Join("archive", "exports") {
		t.Fatalf("expected store node base dir archive/exports, got %q", stored.StoreNodes[0].BaseDir)
	}
	if stored.Tasks[0].ResultStorage.Folder != filepath.Join("archive", "results") {
		t.Fatalf("expected task result storage folder archive/results, got %q", stored.Tasks[0].ResultStorage.Folder)
	}
}

func TestHTTPHandlerDeleteWorkspaceFolderRejectsNonEmptyAndDeletesEmpty(t *testing.T) {
	store, ws, handler := newFolderHandlerTest(t, "ws-folder-delete", "Folder Delete")
	filesPath := store.GetFilesPath(ws.ID)
	if err := os.MkdirAll(filepath.Join(filesPath, "research"), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(filesPath, "research", "spec.md"), []byte("spec"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	now := time.Now()
	ws.Folders = []Folder{{ID: "folder-1", Path: "research", CreatedAt: now, UpdatedAt: now}}
	if err := store.Save(ws); err != nil {
		t.Fatalf("Save workspace: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/workspaces/"+ws.ID+"/folders/folder-1", nil)
	rr := httptest.NewRecorder()
	handler.DeleteWorkspaceFolder(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected non-empty delete status 409, got %d: %s", rr.Code, rr.Body.String())
	}

	if err := os.Remove(filepath.Join(filesPath, "research", "spec.md")); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	req = httptest.NewRequest(http.MethodDelete, "/api/workspaces/"+ws.ID+"/folders/folder-1", nil)
	rr = httptest.NewRecorder()
	handler.DeleteWorkspaceFolder(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected empty delete status 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(filesPath, "research")); !os.IsNotExist(err) {
		t.Fatalf("expected folder to be deleted from disk, got err=%v", err)
	}
	stored, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("Get workspace: %v", err)
	}
	if len(stored.Folders) != 0 {
		t.Fatalf("expected folder metadata to be removed, got %d folders", len(stored.Folders))
	}
}

func TestHTTPHandlerDeleteWorkspaceFolderRejectsStorageReferences(t *testing.T) {
	store, ws, handler := newFolderHandlerTest(t, "ws-folder-delete-storage-ref", "Folder Delete Storage Ref")
	filesPath := store.GetFilesPath(ws.ID)
	if err := os.MkdirAll(filepath.Join(filesPath, "reports"), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	now := time.Now()
	ws.Folders = []Folder{{ID: "folder-1", Path: "reports", CreatedAt: now, UpdatedAt: now}}
	ws.StoreNodes = []StoreNode{
		{
			ID:            "store-1",
			CanvasNodeID:  "store-node-1",
			WorkspaceID:   ws.ID,
			Name:          "Reports",
			BaseDir:       "reports",
			StorageTarget: StorageTargetWorkspaceFolder,
			Folder:        "reports",
			Format:        "csv",
			WriteMode:     "append",
			CreatedAt:     now,
			UpdatedAt:     now,
		},
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("Save workspace: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/workspaces/"+ws.ID+"/folders/folder-1", nil)
	rr := httptest.NewRecorder()
	handler.DeleteWorkspaceFolder(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected delete with storage reference status 409, got %d: %s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(filesPath, "reports")); err != nil {
		t.Fatalf("expected referenced folder to remain on disk: %v", err)
	}
}

func TestHTTPHandlerMoveAttachmentFileMovesFileAndUpdatesMetadata(t *testing.T) {
	store, ws, handler := newFolderHandlerTest(t, "ws-file-move", "File Move")
	filesPath := store.GetFilesPath(ws.ID)
	if err := os.WriteFile(filepath.Join(filesPath, "abc12345_spec.md"), []byte("spec"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	now := time.Now()
	if err := ws.AddAttachment(Attachment{
		ID:          "att-1",
		WorkspaceID: ws.ID,
		Title:       "Spec",
		Type:        AttachmentTypeDoc,
		File: &AttachmentFileMeta{
			Name:         "spec.md",
			RelativePath: "abc12345_spec.md",
			URL:          workspaceFileURL(ws.ID, "abc12345_spec.md"),
		},
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("AddAttachment: %v", err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("Save workspace: %v", err)
	}

	req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/"+ws.ID+"/attachments/att-1/move", bytes.NewBufferString(`{"target_folder":"research"}`))
	rr := httptest.NewRecorder()
	handler.MoveAttachmentFile(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	wantRelativePath := filepath.Join("research", "abc12345_spec.md")
	if _, err := os.Stat(filepath.Join(filesPath, wantRelativePath)); err != nil {
		t.Fatalf("expected moved file on disk: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filesPath, "abc12345_spec.md")); !os.IsNotExist(err) {
		t.Fatalf("expected old file to be gone, got err=%v", err)
	}
	stored, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("Get workspace: %v", err)
	}
	attachment, err := stored.GetAttachment("att-1")
	if err != nil {
		t.Fatalf("GetAttachment: %v", err)
	}
	if attachment.File.RelativePath != wantRelativePath {
		t.Fatalf("expected relative path %q, got %q", wantRelativePath, attachment.File.RelativePath)
	}
	if attachment.File.URL != workspaceFileURL(ws.ID, wantRelativePath) {
		t.Fatalf("expected updated URL, got %q", attachment.File.URL)
	}
}

func TestBuildWorkspaceFileTreeExcludesTrashedAttachments(t *testing.T) {
	store, ws, _ := newFolderHandlerTest(t, "ws-tree-trash", "Tree Trash")
	filesPath := store.GetFilesPath(ws.ID)
	if err := os.WriteFile(filepath.Join(filesPath, "active.txt"), []byte("active"), 0644); err != nil {
		t.Fatalf("WriteFile active: %v", err)
	}
	if err := os.WriteFile(filepath.Join(filesPath, "trashed.txt"), []byte("trashed"), 0644); err != nil {
		t.Fatalf("WriteFile trashed: %v", err)
	}
	now := time.Now()
	deletedAt := now
	ws.Attachments = []Attachment{
		{
			ID:          "active",
			WorkspaceID: ws.ID,
			Title:       "Active",
			Type:        AttachmentTypeDoc,
			File:        &AttachmentFileMeta{Name: "active.txt", RelativePath: "active.txt", URL: workspaceFileURL(ws.ID, "active.txt")},
			CreatedAt:   now,
			UpdatedAt:   now,
		},
		{
			ID:          "trashed",
			WorkspaceID: ws.ID,
			Title:       "Trashed",
			Type:        AttachmentTypeDoc,
			File:        &AttachmentFileMeta{Name: "trashed.txt", RelativePath: "trashed.txt", URL: workspaceFileURL(ws.ID, "trashed.txt")},
			DeletedAt:   &deletedAt,
			CreatedAt:   now,
			UpdatedAt:   now,
		},
	}

	files, err := buildWorkspaceFileTree(ws, filesPath)
	if err != nil {
		t.Fatalf("buildWorkspaceFileTree: %v", err)
	}
	assertFileInfo(t, files, "active.txt", false)
	if findFileInfo(files, "trashed.txt") != nil {
		t.Fatalf("expected trashed attachment path to be omitted from tree")
	}
}

func newFolderHandlerTest(t *testing.T, id, name string) (*FileStore, *Workspace, *HTTPHandler) {
	t.Helper()
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ws := newTestWorkspace(id, name)
	if err := store.Save(ws); err != nil {
		t.Fatalf("Save workspace: %v", err)
	}
	return store, ws, NewHTTPHandler(store, nil, nil)
}

func decodeFileTreeResponse(t *testing.T, data []byte) []FileInfo {
	t.Helper()
	var response struct {
		Files []FileInfo `json:"files"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatalf("decode file tree response: %v", err)
	}
	return response.Files
}

func assertFileInfo(t *testing.T, files []FileInfo, relativePath string, isDir bool) {
	t.Helper()
	item := findFileInfo(files, relativePath)
	if item == nil {
		t.Fatalf("expected tree item %q, got %#v", relativePath, files)
	}
	if item.IsDir != isDir {
		t.Fatalf("expected %q is_dir=%v, got %v", relativePath, isDir, item.IsDir)
	}
}

func findFileInfo(files []FileInfo, relativePath string) *FileInfo {
	for i := range files {
		if files[i].RelativePath == relativePath {
			return &files[i]
		}
	}
	return nil
}
