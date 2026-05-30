package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTaskResultToJSONLRecordsArrayMergesMetadata(t *testing.T) {
	task := &Task{
		ID:          "task-1",
		Description: "pollen",
		Context: map[string]any{
			"structured_output": []any{
				map[string]any{"date": "2026-05-09", "pollen_index": "7"},
				map[string]any{"date": "2026-05-10", "pollen_index": "5"},
			},
		},
	}
	metadata := map[string]any{"run_id": "run-9", "status": "success"}

	records := TaskResultToJSONLRecords(task, "", metadata)
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	for i, record := range records {
		if record["run_id"] != "run-9" {
			t.Errorf("record %d missing merged run_id: %#v", i, record)
		}
		if record["status"] != "success" {
			t.Errorf("record %d missing merged status: %#v", i, record)
		}
		if record["date"] == nil {
			t.Errorf("record %d lost its data field: %#v", i, record)
		}
	}
}

func TestTaskResultToJSONLRecordsDoesNotClobberData(t *testing.T) {
	task := &Task{
		Context: map[string]any{
			"structured_output": map[string]any{"status": "high", "value": "9"},
		},
	}
	records := TaskResultToJSONLRecords(task, "", map[string]any{"status": "success"})
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0]["status"] != "high" {
		t.Errorf("metadata clobbered a data field; got status=%v", records[0]["status"])
	}
}

func TestTaskResultToJSONLRecordsDecodesResultJSON(t *testing.T) {
	task := &Task{ID: "t"}
	records := TaskResultToJSONLRecords(task, `{"date":"2026-05-12","level":"low"}`, nil)
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0]["date"] != "2026-05-12" {
		t.Errorf("expected decoded date, got %#v", records[0])
	}
}

func TestTaskResultToJSONLRecordsFallbackWrapsRawResult(t *testing.T) {
	task := &Task{ID: "task-7", Description: "summary"}
	records := TaskResultToJSONLRecords(task, "just some prose, not JSON", map[string]any{"run_id": "r1"})
	if len(records) != 1 {
		t.Fatalf("expected 1 fallback record, got %d", len(records))
	}
	rec := records[0]
	if rec["result"] != "just some prose, not JSON" {
		t.Errorf("fallback should carry raw result, got %#v", rec)
	}
	if rec["task_id"] != "task-7" || rec["run_id"] != "r1" {
		t.Errorf("fallback missing task_id/metadata: %#v", rec)
	}
}

func TestAppendJSONLToFileAppendsLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.jsonl")

	first, err := MarshalJSONLRecords([]map[string]any{{"a": 1}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := AppendJSONLToFile(path, first); err != nil {
		t.Fatalf("append 1: %v", err)
	}
	second, err := MarshalJSONLRecords([]map[string]any{{"a": 2}, {"a": 3}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := AppendJSONLToFile(path, second); err != nil {
		t.Fatalf("append 2: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 JSONL lines, got %d: %q", len(lines), string(data))
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "{") || !strings.HasSuffix(line, "}") {
			t.Errorf("line is not a JSON object: %q", line)
		}
	}
}

func TestExportCSVFromJSONLLeadsWithPreferredColumns(t *testing.T) {
	jsonl := strings.Join([]string{
		`{"date":"2026-05-09","value":"7","run_id":"r1"}`,
		`{"date":"2026-05-10","value":"5","run_id":"r2"}`,
	}, "\n")

	csv, err := ExportCSVFromJSONL(jsonl, []string{"date", "value"})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(csv), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected header + 2 rows, got %d: %q", len(lines), csv)
	}
	if lines[0] != "date,value,run_id" {
		t.Errorf("expected data-first header, got %q", lines[0])
	}
	if lines[1] != "2026-05-09,7,r1" {
		t.Errorf("unexpected first row: %q", lines[1])
	}
}

func TestExportCSVFromJSONLEmptyIsEmpty(t *testing.T) {
	csv, err := ExportCSVFromJSONL("   \n  ", nil)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if strings.TrimSpace(csv) != "" {
		t.Errorf("expected empty CSV for empty JSONL, got %q", csv)
	}
}

func TestExportCSVFromJSONLRejectsBadLine(t *testing.T) {
	_, err := ExportCSVFromJSONL("{\"ok\":1}\nnot json", nil)
	if err == nil {
		t.Fatal("expected an error for a malformed JSONL line")
	}
}

func TestBuildAppendJSONLUsesValidatedRow(t *testing.T) {
	validation := &TaskValidationResult{
		ValidationStatus: TaskValidationPassed,
		NormalizedRow:    map[string]any{"date": "2026-05-09", "pollen_index": "7"},
	}
	out, err := BuildAppendJSONL(&Task{ID: "t"}, "ignored raw result", validation)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	line := strings.TrimSpace(out)
	if strings.Count(line, "\n") != 0 {
		t.Fatalf("expected a single JSONL line, got %q", out)
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(line), &record); err != nil {
		t.Fatalf("invalid JSONL: %v", err)
	}
	if record["date"] != "2026-05-09" || record["pollen_index"] != "7" {
		t.Errorf("validated row not carried through: %#v", record)
	}
	if record["validation_status"] != string(TaskValidationPassed) {
		t.Errorf("validation_status not merged: %#v", record)
	}
}

func TestBuildAppendJSONLFallsBackToResult(t *testing.T) {
	out, err := BuildAppendJSONL(&Task{ID: "t"}, `{"date":"2026-05-12","level":"low"}`, nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &record); err != nil {
		t.Fatalf("invalid JSONL: %v", err)
	}
	if record["date"] != "2026-05-12" || record["level"] != "low" {
		t.Errorf("fallback did not extract result fields: %#v", record)
	}
}

func TestCSVToJSONL(t *testing.T) {
	out, err := CSVToJSONL("date,value\n2026-05-09,7\n2026-05-10,5")
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 records, got %d: %q", len(lines), out)
	}
	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if first["date"] != "2026-05-09" || first["value"] != "7" {
		t.Errorf("record = %#v", first)
	}

	empty, err := CSVToJSONL("date,value")
	if err != nil {
		t.Fatalf("header-only: %v", err)
	}
	if strings.TrimSpace(empty) != "" {
		t.Errorf("header-only CSV should yield no records, got %q", empty)
	}
}

func TestAppendJSONLFileName(t *testing.T) {
	custom := AppendJSONLFileName(nil, &ResultStorageConfig{FileName: "My Runs.csv"})
	if custom != "My_Runs.jsonl" {
		t.Errorf("custom name should normalize to .jsonl, got %q", custom)
	}

	derived := AppendJSONLFileName(&Task{Description: "Daily pollen report"}, nil)
	if derived != "Daily_pollen_report.jsonl" {
		t.Errorf("derived name unexpected: %q", derived)
	}

	fallback := AppendJSONLFileName(&Task{Description: "***"}, nil)
	if fallback != "task.jsonl" {
		t.Errorf("expected fallback task.jsonl, got %q", fallback)
	}
}
