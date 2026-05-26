package workspace

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestHTTPHandlerCreateStoreNodeSupportsWorkspaceFolderTarget(t *testing.T) {
	store, ws, handler := newFolderHandlerTest(t, "ws-store-node-create", "Store Node Create")

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+ws.ID+"/store-nodes", bytes.NewBufferString(`{
		"name":"Workspace Reports",
		"storage_target":"workspace_folder",
		"workspace_folder":"reports/daily",
		"format":"csv",
		"write_mode":"append",
		"auto_create_dir":true
	}`))
	rr := httptest.NewRecorder()

	handler.CreateStoreNode(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", rr.Code, rr.Body.String())
	}

	var node StoreNode
	if err := json.Unmarshal(rr.Body.Bytes(), &node); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if node.StorageTarget != StorageTargetWorkspaceFolder {
		t.Fatalf("expected storage target %q, got %q", StorageTargetWorkspaceFolder, node.StorageTarget)
	}
	if node.WorkspaceFolder != filepath.Join("reports", "daily") {
		t.Fatalf("expected workspace folder reports/daily, got %q", node.WorkspaceFolder)
	}
	if node.BaseDir != filepath.Join("reports", "daily") {
		t.Fatalf("expected display base dir reports/daily, got %q", node.BaseDir)
	}
	if _, err := os.Stat(filepath.Join(store.GetFilesPath(ws.ID), "reports", "daily")); err != nil {
		t.Fatalf("expected workspace folder to be created: %v", err)
	}

	stored, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("Get workspace: %v", err)
	}
	if len(stored.StoreNodes) != 1 {
		t.Fatalf("expected 1 store node, got %d", len(stored.StoreNodes))
	}
	if stored.StoreNodes[0].WorkspaceFolder != filepath.Join("reports", "daily") {
		t.Fatalf("expected stored workspace folder reports/daily, got %q", stored.StoreNodes[0].WorkspaceFolder)
	}
}

func TestHTTPHandlerCreateStoreNodeRejectsWorkspaceFolderTraversal(t *testing.T) {
	_, ws, handler := newFolderHandlerTest(t, "ws-store-node-invalid", "Store Node Invalid")

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+ws.ID+"/store-nodes", bytes.NewBufferString(`{
		"name":"Bad Reports",
		"storage_target":"workspace_folder",
		"workspace_folder":"../outside",
		"format":"csv",
		"write_mode":"append"
	}`))
	rr := httptest.NewRecorder()

	handler.CreateStoreNode(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHTTPHandlerUpdateStoreNodeSwitchesToWorkspaceFolderTarget(t *testing.T) {
	store, ws, handler := newFolderHandlerTest(t, "ws-store-node-update", "Store Node Update")
	ws.StoreNodes = []StoreNode{{
		ID:            "store-1",
		CanvasNodeID:  "store-node-1",
		WorkspaceID:   ws.ID,
		Name:          "External Reports",
		BaseDir:       t.TempDir(),
		Format:        "text",
		WriteMode:     "overwrite",
		AutoCreateDir: true,
	}}
	if err := store.Save(ws); err != nil {
		t.Fatalf("Save workspace: %v", err)
	}

	req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/"+ws.ID+"/store-nodes/store-1", bytes.NewBufferString(`{
		"storage_target":"workspace_folder",
		"workspace_folder":"exports",
		"format":"csv",
		"write_mode":"append"
	}`))
	rr := httptest.NewRecorder()

	handler.UpdateStoreNode(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var node StoreNode
	if err := json.Unmarshal(rr.Body.Bytes(), &node); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if node.StorageTarget != StorageTargetWorkspaceFolder || node.WorkspaceFolder != "exports" || node.BaseDir != "exports" {
		t.Fatalf("unexpected workspace folder node: %+v", node)
	}
	if _, err := os.Stat(filepath.Join(store.GetFilesPath(ws.ID), "exports")); err != nil {
		t.Fatalf("expected exports folder to be created: %v", err)
	}

	stored, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("Get workspace: %v", err)
	}
	if stored.StoreNodes[0].StorageTarget != StorageTargetWorkspaceFolder {
		t.Fatalf("expected stored node to use workspace folder target, got %q", stored.StoreNodes[0].StorageTarget)
	}
}
