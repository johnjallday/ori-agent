package workspace

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHTTPHandler_ExportResultCSV(t *testing.T) {
	store := NewInMemoryStore()
	handler := NewHTTPHandler(store, nil, nil)

	dir := t.TempDir()
	jsonlPath := filepath.Join(dir, "runs.jsonl")
	content := `{"date":"2026-05-09","value":"7","run_id":"r1"}` + "\n" +
		`{"date":"2026-05-10","value":"5","run_id":"r2"}` + "\n"
	if err := os.WriteFile(jsonlPath, []byte(content), 0644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}

	ws := &Workspace{
		ID:     "ws-1",
		Name:   "Demo",
		Status: StatusActive,
		Tasks: []Task{{
			ID:          "task-1",
			Description: "Pollen",
			ResultStorage: &ResultStorageConfig{
				Enabled:   true,
				WriteMode: "append",
				FilePath:  jsonlPath,
			},
			OutputContract: &TaskOutputContract{
				Columns: []TaskOutputContractColumn{{Name: "date"}, {Name: "value"}},
			},
		}},
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet,
		"/api/workspaces/ws-1/tasks/task-1/results/export-csv", nil)
	rec := httptest.NewRecorder()

	handler.ExportResultCSV(rec, withTaskPath(req))

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("content-type = %q, want text/csv", ct)
	}
	lines := strings.Split(strings.TrimSpace(rec.Body.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected header + 2 rows, got %d: %q", len(lines), rec.Body.String())
	}
	if lines[0] != "date,value,run_id" {
		t.Errorf("header = %q, want declared columns first then run metadata", lines[0])
	}
	if lines[1] != "2026-05-09,7,r1" {
		t.Errorf("first row = %q", lines[1])
	}
}

func TestHTTPHandler_ExportResultCSV_NoDatasetYet(t *testing.T) {
	store := NewInMemoryStore()
	handler := NewHTTPHandler(store, nil, nil)

	dir := t.TempDir()
	ws := &Workspace{
		ID:     "ws-1",
		Name:   "Demo",
		Status: StatusActive,
		Tasks: []Task{{
			ID:          "task-1",
			Description: "Pollen",
			ResultStorage: &ResultStorageConfig{
				Enabled:   true,
				WriteMode: "append",
				FilePath:  filepath.Join(dir, "missing.jsonl"),
			},
		}},
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet,
		"/api/workspaces/ws-1/tasks/task-1/results/export-csv", nil)
	rec := httptest.NewRecorder()

	handler.ExportResultCSV(rec, withTaskPath(req))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404 for missing dataset", rec.Code)
	}
}

func TestHTTPHandler_ExportResultCSV_RejectsNonAppendTask(t *testing.T) {
	store := NewInMemoryStore()
	handler := NewHTTPHandler(store, nil, nil)

	ws := &Workspace{
		ID:     "ws-1",
		Name:   "Demo",
		Status: StatusActive,
		Tasks:  []Task{{ID: "task-1", Description: "One-off"}},
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet,
		"/api/workspaces/ws-1/tasks/task-1/results/export-csv", nil)
	rec := httptest.NewRecorder()

	handler.ExportResultCSV(rec, withTaskPath(req))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400 when there is no append dataset", rec.Code)
	}
}
