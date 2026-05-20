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

	handler.AppendResultToCSV(rec, req)

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

	contents, err := os.ReadFile(storedPath)
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	got := strings.TrimSpace(string(contents))
	want := "timestamp,value\n2026-05-20,high"
	if got != want {
		t.Fatalf("csv contents=%q, want %q", got, want)
	}

	// Second append should not duplicate the header.
	req2 := httptest.NewRequest(http.MethodPost,
		"/api/workspaces/ws-1/tasks/task-1/results/append-csv",
		strings.NewReader(`{"csv":"timestamp,value\n2026-05-21,low","use_storage":true}`))
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	handler.AppendResultToCSV(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("status2=%d body=%s", rec2.Code, rec2.Body.String())
	}

	contents2, err := os.ReadFile(storedPath)
	if err != nil {
		t.Fatalf("re-read csv: %v", err)
	}
	got2 := strings.TrimSpace(string(contents2))
	want2 := "timestamp,value\n2026-05-20,high\n2026-05-21,low"
	if got2 != want2 {
		t.Fatalf("csv contents after second append=%q, want %q", got2, want2)
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
	handler.AppendResultToCSV(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	contents, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	if strings.TrimSpace(string(contents)) != "name,score\nalpha,9" {
		t.Fatalf("unexpected csv contents: %q", string(contents))
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
	handler.AppendResultToCSV(rec, req)

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
	handler.UpdateTask(rec, req)

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
	handler.AppendResultToCSV(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s, want 400", rec.Code, rec.Body.String())
	}
}
