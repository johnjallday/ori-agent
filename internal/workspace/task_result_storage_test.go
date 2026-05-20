package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTaskResultToCSV_UsesStructuredOutput(t *testing.T) {
	task := &Task{
		ID:          "task-1",
		Description: "Check pollen",
		Context: map[string]interface{}{
			"structured_output": map[string]interface{}{
				"location": "NYC",
				"value":    8,
				"unit":     "index",
			},
		},
	}

	got := TaskResultToCSV(task, "plain result", "20260519-120000", "")
	if !strings.Contains(got, "location,value,unit") {
		t.Fatalf("expected structured columns, got %q", got)
	}
	if !strings.Contains(got, "NYC,8,index") {
		t.Fatalf("expected structured row, got %q", got)
	}
}

func TestTaskResultToCSV_ParsesJSONArray(t *testing.T) {
	got := TaskResultToCSV(nil, `[{"date":"2026-05-18","level":"Moderate"},{"date":"2026-05-19","level":"High"}]`, "20260519-120000", "")
	if !strings.Contains(got, "date,level") {
		t.Fatalf("expected date and level header, got %q", got)
	}
	if !strings.Contains(got, "2026-05-19,High") {
		t.Fatalf("expected high pollen row, got %q", got)
	}
}

func TestTaskResultToCSV_FallsBackToSingleResultRow(t *testing.T) {
	task := &Task{ID: "task-1", Description: "Check pollen"}
	got := TaskResultToCSV(task, "Pollen is high.", "20260519-120000", "agent-1")
	if !strings.HasPrefix(got, "task_id,description,timestamp,agent,result") {
		t.Fatalf("expected fallback header, got %q", got)
	}
	if !strings.Contains(got, "task-1,Check pollen,20260519-120000,agent-1,Pollen is high.") {
		t.Fatalf("expected fallback row, got %q", got)
	}
}

func TestAppendCSVToFile_WritesHeaderOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pollen.csv")
	first := "date,level\n2026-05-18,Moderate"
	second := "date,level\n2026-05-19,High"

	if err := AppendCSVToFile(path, first); err != nil {
		t.Fatalf("append first csv: %v", err)
	}
	if err := AppendCSVToFile(path, second); err != nil {
		t.Fatalf("append second csv: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	want := "date,level\n2026-05-18,Moderate\n2026-05-19,High"
	if string(data) != want {
		t.Fatalf("csv data = %q, want %q", string(data), want)
	}
}

func TestBootstrapOutputContractFromCSVHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pollen.csv")
	if err := os.WriteFile(path, []byte("date,location,pollen_count\n2026-05-20,NYC,8"), 0644); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	task := &Task{
		ID: "task-1",
		ResultStorage: &ResultStorageConfig{
			Enabled:   true,
			FilePath:  path,
			Format:    "csv",
			WriteMode: "append",
		},
	}

	contract := BootstrapOutputContractFromCSVHeader(nil, task)
	if contract == nil {
		t.Fatal("expected header-derived contract")
	}
	if contract.Source != "csv_header" {
		t.Fatalf("source = %q, want csv_header", contract.Source)
	}
	if len(contract.Columns) != 3 {
		t.Fatalf("expected 3 columns, got %d", len(contract.Columns))
	}
	if contract.Columns[2].Name != "pollen_count" || contract.Columns[2].Type != "string" {
		t.Fatalf("unexpected third column: %+v", contract.Columns[2])
	}
}
