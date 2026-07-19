package workspace

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHTTPHandler_AppendResultToCSV_UsesTaskStorage(t *testing.T) {
	store := NewInMemoryStore()
	handler := NewHTTPHandler(store, nil, nil)

	dir := t.TempDir()
	storedPath := filepath.Join(dir, "runs.csv")

	ws := &Workspace{
		ID:     "ws-1",
		Name:   "Demo",
		Status: StatusActive,
		Tasks: []Task{{
			ID:          "task-1",
			Description: "Allergy Report",
			ResultStorage: &ResultStorageConfig{
				Enabled:   true,
				Format:    "csv",
				WriteMode: "append",
				FilePath:  storedPath,
			},
		}},
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}

	body := `{"csv":"timestamp,value\n2026-05-20,high","use_storage":true}`
	req := httptest.NewRequest(http.MethodPost,
		"/api/workspaces/ws-1/tasks/task-1/results/append-csv",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.AppendResultToCSV(rec, withTaskPath(req))

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		AppendedRows int    `json:"appended_rows"`
		FilePath     string `json:"file_path"`
		Label        string `json:"label"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.AppendedRows != 1 {
		t.Fatalf("appended_rows=%d, want 1", resp.AppendedRows)
	}
	if resp.FilePath != storedPath {
		t.Fatalf("file_path=%q, want %q", resp.FilePath, storedPath)
	}
	if resp.Label != "runs.csv" {
		t.Fatalf("label=%q, want runs.csv", resp.Label)
	}

	records := readAppendedJSONL(t, storedPath)
	if len(records) != 1 {
		t.Fatalf("expected 1 appended record, got %d", len(records))
	}
	if records[0]["timestamp"] != "2026-05-20" || records[0]["value"] != "high" {
		t.Fatalf("record = %#v", records[0])
	}

	// Second append should not duplicate the header.
	req2 := httptest.NewRequest(http.MethodPost,
		"/api/workspaces/ws-1/tasks/task-1/results/append-csv",
		strings.NewReader(`{"csv":"timestamp,value\n2026-05-21,low","use_storage":true}`))
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	handler.AppendResultToCSV(rec2, withTaskPath(req2))
	if rec2.Code != http.StatusOK {
		t.Fatalf("status2=%d body=%s", rec2.Code, rec2.Body.String())
	}

	records2 := readAppendedJSONL(t, storedPath)
	if len(records2) != 2 {
		t.Fatalf("expected 2 records after second append, got %d", len(records2))
	}
	if records2[1]["timestamp"] != "2026-05-21" || records2[1]["value"] != "low" {
		t.Fatalf("second record = %#v", records2[1])
	}
}

// readAppendedJSONL reads a .jsonl dataset file and parses each line into a
// record. The manual-append / approve paths now write JSONL (converted from
// the CSV they receive), so these tests assert records rather than CSV text.
func readAppendedJSONL(t *testing.T, path string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read jsonl: %v", err)
	}
	var records []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("line %q is not JSON: %v", line, err)
		}
		records = append(records, record)
	}
	return records
}

func TestHTTPHandler_AppendResultToCSV_UsesWorkspaceFolderStorage(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = store.Close() }()
	handler := NewHTTPHandler(store, nil, nil)

	ws := &Workspace{
		ID:     "ws-folder-storage",
		Name:   "Folder Storage",
		Status: StatusActive,
		Tasks: []Task{{
			ID:          "task-folder",
			Description: "Folder CSV",
			ResultStorage: &ResultStorageConfig{
				Enabled:       true,
				Format:        "csv",
				WriteMode:     "append",
				StorageTarget: StorageTargetWorkspaceFolder,
				Folder:        "reports",
				FilePath:      "runs.csv",
			},
		}},
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost,
		"/api/workspaces/ws-folder-storage/tasks/task-folder/results/append-csv",
		strings.NewReader(`{"csv":"timestamp,value\n2026-05-26,high","use_storage":true}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.AppendResultToCSV(rec, withTaskPath(req))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	wantPath := filepath.Join(store.GetFilesPath(ws.ID), "reports", "runs.csv")
	var resp struct {
		FilePath string `json:"file_path"`
		Label    string `json:"label"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.FilePath != wantPath {
		t.Fatalf("file_path=%q, want %q", resp.FilePath, wantPath)
	}
	if resp.Label != "runs.csv" {
		t.Fatalf("label=%q, want runs.csv", resp.Label)
	}
	records := readAppendedJSONL(t, wantPath)
	if len(records) != 1 || records[0]["timestamp"] != "2026-05-26" || records[0]["value"] != "high" {
		t.Fatalf("unexpected appended records: %#v", records)
	}

	treeReq := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+ws.ID+"/files/tree", nil)
	treeRR := httptest.NewRecorder()
	handler.GetWorkspaceFilesTree(treeRR, withFilesPath(treeReq))
	if treeRR.Code != http.StatusOK {
		t.Fatalf("tree status=%d body=%s", treeRR.Code, treeRR.Body.String())
	}
	files := decodeFileTreeResponse(t, treeRR.Body.Bytes())
	assertFileInfo(t, files, filepath.Join("reports", "runs.csv"), false)
	if item := findFileInfo(files, filepath.Join("reports", "runs.csv")); item == nil || item.AttachmentID != "" {
		t.Fatalf("expected disk-backed tree entry without attachment id, got %#v", item)
	}
}

func TestHTTPHandler_AppendResultToCSV_UsesWorkspaceFolderStoreNode(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = store.Close() }()
	handler := NewHTTPHandler(store, nil, nil)

	ws := &Workspace{
		ID:     "ws-folder-store-node",
		Name:   "Folder Store Node",
		Status: StatusActive,
		StoreNodes: []StoreNode{{
			ID:            "store-1",
			CanvasNodeID:  "store-node-1",
			WorkspaceID:   "ws-folder-store-node",
			Name:          "CSV Store",
			StorageTarget: StorageTargetWorkspaceFolder,
			Folder:        "exports",
			Format:        "csv",
			WriteMode:     "append",
			AutoCreateDir: true,
		}},
		Tasks: []Task{{
			ID:          "task-store-node",
			Description: "Store Node CSV",
			ResultStorage: &ResultStorageConfig{
				Enabled:     true,
				Format:      "csv",
				WriteMode:   "append",
				StoreNodeID: "store-1",
				FilePath:    "runs.csv",
			},
		}},
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost,
		"/api/workspaces/ws-folder-store-node/tasks/task-store-node/results/append-csv",
		strings.NewReader(`{"csv":"timestamp,value\n2026-05-26,stored","use_storage":true}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.AppendResultToCSV(rec, withTaskPath(req))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	wantPath := filepath.Join(store.GetFilesPath(ws.ID), "exports", "runs.csv")
	records := readAppendedJSONL(t, wantPath)
	if len(records) != 1 || records[0]["timestamp"] != "2026-05-26" || records[0]["value"] != "stored" {
		t.Fatalf("unexpected appended records: %#v", records)
	}

	updated, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("reload workspace: %v", err)
	}
	if updated.StoreNodes[0].WriteCount != 1 {
		t.Fatalf("expected store node write count 1, got %d", updated.StoreNodes[0].WriteCount)
	}
	if updated.StoreNodes[0].LastFilePath != "runs.csv" {
		t.Fatalf("expected last file path runs.csv, got %q", updated.StoreNodes[0].LastFilePath)
	}
}

func TestHTTPHandler_AppendResultToCSV_OneShotCustomPath(t *testing.T) {
	store := NewInMemoryStore()
	handler := NewHTTPHandler(store, nil, nil)

	dir := t.TempDir()
	targetPath := filepath.Join(dir, "manual.csv")

	ws := &Workspace{
		ID:     "ws-2",
		Name:   "Demo",
		Status: StatusActive,
		Tasks: []Task{{
			ID:          "task-2",
			Description: "Ad-hoc Task",
			// No ResultStorage configured.
		}},
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}

	body := map[string]any{
		"csv":         "name,score\nalpha,9",
		"use_storage": false,
		"file_path":   targetPath,
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost,
		"/api/workspaces/ws-2/tasks/task-2/results/append-csv",
		strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.AppendResultToCSV(rec, withTaskPath(req))

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	records := readAppendedJSONL(t, targetPath)
	if len(records) != 1 || records[0]["name"] != "alpha" || records[0]["score"] != "9" {
		t.Fatalf("unexpected appended records: %#v", records)
	}
}

func TestHTTPHandler_AppendResultToCSV_UseStorageNotConfigured(t *testing.T) {
	store := NewInMemoryStore()
	handler := NewHTTPHandler(store, nil, nil)

	ws := &Workspace{
		ID:     "ws-3",
		Name:   "Demo",
		Status: StatusActive,
		Tasks:  []Task{{ID: "task-3", Description: "No storage"}},
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost,
		"/api/workspaces/ws-3/tasks/task-3/results/append-csv",
		strings.NewReader(`{"csv":"a,b\n1,2","use_storage":true}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.AppendResultToCSV(rec, withTaskPath(req))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s, want 400", rec.Code, rec.Body.String())
	}
}

func TestHTTPHandler_UpdateTask_PersistsResultStorage(t *testing.T) {
	store := NewInMemoryStore()
	handler := NewHTTPHandler(store, nil, nil)

	ws := &Workspace{
		ID:     "ws-storage-patch",
		Name:   "Demo",
		Status: StatusActive,
		Tasks:  []Task{{ID: "task-1", Description: "Allergy"}},
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("save: %v", err)
	}

	body := `{
		"output_contract": {"source": "manual", "columns": [{"name": "date", "type": "date", "required": true}]},
		"result_storage": {"enabled": true, "format": "csv", "write_mode": "append", "file_path": "/tmp/runs.csv"}
	}`
	req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/ws-storage-patch/tasks/task-1",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.UpdateTask(rec, withTaskPath(req))

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	updated, err := store.Get("ws-storage-patch")
	if err != nil {
		t.Fatalf("reload workspace: %v", err)
	}
	if len(updated.Tasks) != 1 {
		t.Fatalf("task count=%d, want 1", len(updated.Tasks))
	}
	storage := updated.Tasks[0].ResultStorage
	if storage == nil {
		t.Fatalf("result_storage was not persisted")
	}
	if !storage.Enabled || storage.WriteMode != "append" || storage.Format != "csv" {
		t.Fatalf("unexpected storage config: %+v", storage)
	}
	if storage.FilePath != "/tmp/runs.csv" {
		t.Fatalf("file_path=%q, want /tmp/runs.csv", storage.FilePath)
	}
	contract := updated.Tasks[0].OutputContract
	if contract == nil || len(contract.Columns) != 1 || contract.Columns[0].Name != "date" {
		t.Fatalf("output_contract not persisted: %+v", contract)
	}
}

func TestHTTPHandler_AppendResultToCSV_MissingCSV(t *testing.T) {
	store := NewInMemoryStore()
	handler := NewHTTPHandler(store, nil, nil)

	ws := &Workspace{
		ID:     "ws-4",
		Name:   "Demo",
		Status: StatusActive,
		Tasks:  []Task{{ID: "task-4", Description: "Empty CSV"}},
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost,
		"/api/workspaces/ws-4/tasks/task-4/results/append-csv",
		strings.NewReader(`{"csv":"   "}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.AppendResultToCSV(rec, withTaskPath(req))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s, want 400", rec.Code, rec.Body.String())
	}
}
