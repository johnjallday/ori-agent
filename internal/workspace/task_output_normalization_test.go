package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateTaskOutputSpecResult_ProjectsNormalizedRowWithMetadata(t *testing.T) {
	task := structuredPollenTaskForNormalization()
	task.CurrentRunID = "run-1"
	started := time.Date(2026, 5, 21, 8, 30, 0, 0, time.UTC)
	RecordTaskExecution(task, "success", `{"forecast_date":"2026-05-21","pollen_count":9.7,"top_allergens":["Oak","Birch"]}`, started, 1500*time.Millisecond)

	validation, csvData := ValidateTaskOutputSpecResult(task, `{"forecast_date":"2026-05-21","pollen_count":9.7,"top_allergens":["Oak","Birch"]}`)
	if validation.ValidationStatus != TaskValidationPassed {
		t.Fatalf("validation=%+v, want passed", validation)
	}
	if validation.NormalizedRowRef == "" || validation.RawOutputRef == "" {
		t.Fatalf("expected stable refs, got %+v", validation)
	}
	wantHeader := "run_id,executed_at,status,duration_ms,forecast_date,pollen_count,top_allergens"
	if !strings.HasPrefix(csvData, wantHeader+"\n") {
		t.Fatalf("csv header = %q, want prefix %q", csvData, wantHeader)
	}
	if !strings.Contains(csvData, `run-1,2026-05-21T08:30:00Z,success,1500,2026-05-21,9.7,"[""Oak"",""Birch""]"`) {
		t.Fatalf("csv row missing normalized data: %q", csvData)
	}
	if task.Context["normalized_output"] == nil {
		t.Fatalf("expected normalized output in task context")
	}
}

func TestValidateTaskOutputSpecResult_ProseNeedsReview(t *testing.T) {
	task := structuredPollenTaskForNormalization()
	RecordTaskExecution(task, "success", "Pollen is high.", time.Now(), time.Second)

	validation, csvData := ValidateTaskOutputSpecResult(task, "Pollen is high.")
	if csvData != "" {
		t.Fatalf("csvData=%q, want empty", csvData)
	}
	if validation.ValidationStatus != TaskValidationNeedsReview || validation.StorageStatus != TaskStorageSkippedInvalid {
		t.Fatalf("validation=%+v, want needs_review/skipped_invalid", validation)
	}
	if len(validation.Errors) == 0 || validation.Errors[0].Code != taskOutputNormalizationFailedCode {
		t.Fatalf("expected normalization failure, got %+v", validation.Errors)
	}
}

func TestValidateTaskOutputSpecResultWithAssistant_NormalizesProse(t *testing.T) {
	task := structuredPollenTaskForNormalization()
	RecordTaskExecution(task, "success", "Pollen is high.", time.Now(), time.Second)
	assistant := &fakeTaskOutputSpecAssistant{
		normalizeResult: `{"forecast_date":"2026-05-21","pollen_count":9.7,"top_allergens":["Oak"]}`,
	}

	validation, csvData := ValidateTaskOutputSpecResultWithAssistant(context.Background(), task, "Pollen is high.", assistant)
	if validation.ValidationStatus != TaskValidationPassed {
		t.Fatalf("validation=%+v, want passed", validation)
	}
	if assistant.normalizeCalls != 1 {
		t.Fatalf("normalizeCalls=%d, want 1", assistant.normalizeCalls)
	}
	if !strings.Contains(csvData, "2026-05-21,9.7") {
		t.Fatalf("csv missing assistant-normalized row: %q", csvData)
	}
}

func TestValidateTaskOutputSpecResultWithAssistant_RepairsProjectedRow(t *testing.T) {
	task := structuredPollenTaskForNormalization()
	RecordTaskExecution(task, "success", `{"forecast_date":"not-a-date","pollen_count":9.7,"top_allergens":["Oak"]}`, time.Now(), time.Second)
	assistant := &fakeTaskOutputSpecAssistant{
		repairResult: `{"forecast_date":"2026-05-21","pollen_count":9.7,"top_allergens":["Oak"]}`,
	}

	validation, csvData := ValidateTaskOutputSpecResultWithAssistant(context.Background(), task, `{"forecast_date":"not-a-date","pollen_count":9.7,"top_allergens":["Oak"]}`, assistant)
	if validation.ValidationStatus != TaskValidationPassed {
		t.Fatalf("validation=%+v, want passed", validation)
	}
	if validation.RepairStatus != TaskOutputRepairSucceeded {
		t.Fatalf("repair_status=%q, want succeeded", validation.RepairStatus)
	}
	if assistant.repairCalls != 1 {
		t.Fatalf("repairCalls=%d, want 1", assistant.repairCalls)
	}
	if !strings.Contains(csvData, "2026-05-21,9.7") {
		t.Fatalf("csv missing repaired row: %q", csvData)
	}
}

func TestValidateTaskOutputSpecResultWithAssistant_ProviderFailureNeedsReview(t *testing.T) {
	task := structuredPollenTaskForNormalization()
	RecordTaskExecution(task, "success", "Pollen is high.", time.Now(), time.Second)
	assistant := &fakeTaskOutputSpecAssistant{normalizeErr: fmt.Errorf("provider unavailable")}

	validation, csvData := ValidateTaskOutputSpecResultWithAssistant(context.Background(), task, "Pollen is high.", assistant)
	if csvData != "" {
		t.Fatalf("csvData=%q, want empty", csvData)
	}
	if validation.ValidationStatus != TaskValidationNeedsReview || validation.StorageStatus != TaskStorageSkippedInvalid {
		t.Fatalf("validation=%+v, want needs_review/skipped_invalid", validation)
	}
	if len(validation.Errors) == 0 || validation.Errors[0].Code != "normalization_provider_error" {
		t.Fatalf("expected normalization_provider_error, got %+v", validation.Errors)
	}
}

func TestBuildTaskOutputSpecPrompt_ComposesSchemaAndContract(t *testing.T) {
	prompt := BuildTaskOutputSpecPrompt(structuredPollenTaskForNormalization().OutputSpec)
	if !strings.Contains(prompt, "Schema fields:") || !strings.Contains(prompt, "CSV storage projection:") || !strings.Contains(prompt, "Field-to-column mappings:") {
		t.Fatalf("prompt missing composed sections:\n%s", prompt)
	}
	if !strings.Contains(prompt, "The system will add run metadata") {
		t.Fatalf("prompt missing metadata note:\n%s", prompt)
	}
}

type fakeTaskOutputSpecAssistant struct {
	normalizeResult string
	normalizeErr    error
	repairResult    string
	repairErr       error
	normalizeCalls  int
	repairCalls     int
}

func (f *fakeTaskOutputSpecAssistant) NormalizeTaskOutputSpec(_ context.Context, _ Task, _ string) (string, error) {
	f.normalizeCalls++
	return f.normalizeResult, f.normalizeErr
}

func (f *fakeTaskOutputSpecAssistant) RepairTaskOutputSpec(_ context.Context, _ Task, _ string, _ map[string]any, _ []TaskValidationError) (string, error) {
	f.repairCalls++
	return f.repairResult, f.repairErr
}

// JSONL append has no header to reconcile, so a passing run is appended as a
// new record regardless of what the destination file already contains — the
// CSV header-mismatch "needs review" path no longer applies to appends.
func TestAutoStoreResult_AppendsJSONLRecordOnPass(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "pollen.jsonl")
	// A pre-existing record (even with different keys) must not block the append.
	if err := os.WriteFile(filePath, []byte(`{"other":"row"}`+"\n"), 0644); err != nil {
		t.Fatalf("write existing jsonl: %v", err)
	}
	task := structuredPollenTaskForNormalization()
	task.ResultStorage = &ResultStorageConfig{
		Enabled:   true,
		WriteMode: "append",
		FilePath:  filePath,
	}
	task.CurrentRunID = "run-1"
	RecordTaskExecution(task, "success", `{"forecast_date":"2026-05-21","pollen_count":9.7,"top_allergens":["Oak"]}`, time.Now(), time.Second)
	ws := &Workspace{ID: "ws-1", Name: "Workspace", Status: StatusActive}
	if err := ws.AddTask(*task); err != nil {
		t.Fatalf("add task: %v", err)
	}
	store := newTestWorkspaceStore(t, ws)

	AutoStoreResult(ws, task, `{"forecast_date":"2026-05-21","pollen_count":9.7,"top_allergens":["Oak"]}`, store)

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read jsonl: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected existing + appended line, got %d: %q", len(lines), string(data))
	}
	var appended map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &appended); err != nil {
		t.Fatalf("appended line is not JSON: %v", err)
	}
	if appended["forecast_date"] != "2026-05-21" {
		t.Errorf("appended record missing data field: %#v", appended)
	}
	if appended["run_id"] != "run-1" || appended["status"] != "success" {
		t.Errorf("appended record missing run metadata: %#v", appended)
	}

	updated, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("reload workspace: %v", err)
	}
	updatedTask, err := updated.GetTask(task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	validation := updatedTask.ExecutionHistory[len(updatedTask.ExecutionHistory)-1].Validation
	if validation == nil || validation.ValidationStatus != TaskValidationPassed || validation.StorageStatus != TaskStorageAppended {
		t.Fatalf("validation=%+v, want passed/appended", validation)
	}
}

func structuredPollenTaskForNormalization() *Task {
	spec, errs := NormalizeTaskOutputSpec(&TaskOutputSpec{
		Source: "manual",
		Schema: &TaskOutputSchema{
			Name:   "pollen",
			Strict: true,
			Fields: []TaskOutputField{
				{Name: "forecast_date", Type: "string", Required: true},
				{Name: "pollen_count", Type: "number", Required: true},
				{Name: "top_allergens", Type: "array"},
			},
		},
		Contract: &TaskOutputContract{
			Source: "manual",
			Columns: []TaskOutputContractColumn{
				{Name: "forecast_date", Type: "date", Required: true},
				{Name: "pollen_count", Type: "number", Required: true},
				{Name: "top_allergens", Type: "string"},
			},
		},
		Mappings: []TaskOutputMapping{
			{SchemaField: "forecast_date", CSVColumn: "forecast_date", Transform: TaskOutputMappingTransformIdentity},
			{SchemaField: "pollen_count", CSVColumn: "pollen_count", Transform: TaskOutputMappingTransformIdentity},
			{SchemaField: "top_allergens", CSVColumn: "top_allergens", Transform: TaskOutputMappingTransformJSONString},
		},
	})
	if len(errs) > 0 {
		panic(strings.Join(errs, "; "))
	}
	spec = AssignTaskOutputSpecVersion(spec)
	return &Task{
		ID:          "task-1",
		WorkspaceID: "ws-1",
		Description: "Daily pollen",
		OutputSpec:  spec,
		Context:     map[string]any{},
	}
}
