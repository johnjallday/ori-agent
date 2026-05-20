package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNormalizeTaskOutputContract_DeduplicatesAndVersions(t *testing.T) {
	contract := NormalizeTaskOutputContract(&TaskOutputContract{
		Source: "manual",
		Columns: []TaskOutputContractColumn{
			{Name: " date ", Type: "date", Required: true},
			{Name: "DATE", Type: "number"},
			{Name: "pollen_count", Type: "number", Required: true, Description: " Daily index "},
			{Name: "", Type: "boolean"},
			{Name: "source", Type: "unsupported"},
		},
	})

	if contract == nil {
		t.Fatal("expected normalized contract")
	}
	if len(contract.Columns) != 3 {
		t.Fatalf("expected 3 columns after normalization, got %d", len(contract.Columns))
	}
	if contract.Columns[0].Name != "date" || contract.Columns[0].Type != "date" {
		t.Fatalf("unexpected first column: %+v", contract.Columns[0])
	}
	if contract.Columns[2].Type != "string" {
		t.Fatalf("invalid type should default to string, got %q", contract.Columns[2].Type)
	}
	if !strings.HasPrefix(contract.Version, "ocv_") {
		t.Fatalf("expected generated version, got %q", contract.Version)
	}
}

func TestValidateTaskOutputContractResult_JSONPassesAndEmitsContractCSV(t *testing.T) {
	task := contractedPollenTask(t.TempDir())
	validation, csvData := ValidateTaskOutputContractResult(task, `{"date":"2026-05-20","location":"NYC","pollen_count":8,"high":true}`)

	if validation.ValidationStatus != TaskValidationPassed {
		t.Fatalf("validation status = %q, want passed: %+v", validation.ValidationStatus, validation.Errors)
	}
	if validation.ContractVersion == "" {
		t.Fatal("expected contract version on validation result")
	}
	want := "date,location,pollen_count,high\n2026-05-20,NYC,8,true"
	if csvData != want {
		t.Fatalf("csv = %q, want %q", csvData, want)
	}
}

func TestValidateTaskOutputContractResult_CSVPassesCaseInsensitiveColumns(t *testing.T) {
	task := contractedPollenTask(t.TempDir())
	validation, csvData := ValidateTaskOutputContractResult(task, "Date,LOCATION,POLLEN_COUNT,high\n2026-05-20,NYC,8,false")

	if validation.ValidationStatus != TaskValidationPassed {
		t.Fatalf("validation status = %q, want passed: %+v", validation.ValidationStatus, validation.Errors)
	}
	if csvData != "date,location,pollen_count,high\n2026-05-20,NYC,8,false" {
		t.Fatalf("unexpected contract csv: %q", csvData)
	}
}

func TestValidateTaskOutputContractResult_NeedsReviewForMissingAndTypeErrors(t *testing.T) {
	task := contractedPollenTask(t.TempDir())
	validation, csvData := ValidateTaskOutputContractResult(task, `{"date":"not-a-date","location":"NYC","high":"maybe"}`)

	if validation.ValidationStatus != TaskValidationNeedsReview {
		t.Fatalf("validation status = %q, want needs_review", validation.ValidationStatus)
	}
	if validation.StorageStatus != TaskStorageSkippedInvalid {
		t.Fatalf("storage status = %q, want skipped_invalid", validation.StorageStatus)
	}
	if csvData != "" {
		t.Fatalf("expected no csv for invalid result, got %q", csvData)
	}
	if len(validation.Errors) != 3 {
		t.Fatalf("expected 3 validation errors, got %d: %+v", len(validation.Errors), validation.Errors)
	}
	if validation.RawOutputRef == "" {
		t.Fatal("expected raw output reference for review")
	}
}

func TestBuildTaskOutputContractPrompt(t *testing.T) {
	task := contractedPollenTask(t.TempDir())
	prompt := BuildTaskOutputContractPrompt(task.OutputContract)

	if !strings.Contains(prompt, "Return ONLY a valid JSON object") {
		t.Fatalf("expected JSON-only instruction, got %q", prompt)
	}
	if !strings.Contains(prompt, "pollen_count (number, required)") {
		t.Fatalf("expected column instruction, got %q", prompt)
	}
	if !strings.Contains(prompt, "Use ISO dates") {
		t.Fatalf("expected date guidance, got %q", prompt)
	}
}

func TestValidateTaskOutputContractResult_RecurringTaskScenarios(t *testing.T) {
	tests := []struct {
		name     string
		columns  []TaskOutputContractColumn
		result   string
		wantCSV  string
		wantFail bool
	}{
		{
			name: "weather",
			columns: []TaskOutputContractColumn{
				{Name: "date", Type: "date", Required: true},
				{Name: "location", Type: "string", Required: true},
				{Name: "temperature", Type: "number"},
				{Name: "condition", Type: "string"},
			},
			result:  `{"date":"2026-05-20","location":"NYC","temperature":71.5,"condition":"Clear"}`,
			wantCSV: "date,location,temperature,condition\n2026-05-20,NYC,71.5,Clear",
		},
		{
			name: "price",
			columns: []TaskOutputContractColumn{
				{Name: "date", Type: "date", Required: true},
				{Name: "symbol", Type: "string", Required: true},
				{Name: "price", Type: "number", Required: true},
				{Name: "currency", Type: "string"},
			},
			result:  `{"date":"2026-05-20","symbol":"BTC","price":"104200.12","currency":"USD"}`,
			wantCSV: "date,symbol,price,currency\n2026-05-20,BTC,104200.12,USD",
		},
		{
			name: "website check",
			columns: []TaskOutputContractColumn{
				{Name: "date", Type: "date", Required: true},
				{Name: "url", Type: "string", Required: true},
				{Name: "available", Type: "boolean", Required: true},
				{Name: "status_code", Type: "number"},
			},
			result:  "date,url,available,status_code\n2026-05-20,https://example.com,true,200",
			wantCSV: "date,url,available,status_code\n2026-05-20,https://example.com,true,200",
		},
		{
			name: "pollen invalid number",
			columns: []TaskOutputContractColumn{
				{Name: "date", Type: "date", Required: true},
				{Name: "location", Type: "string", Required: true},
				{Name: "pollen_count", Type: "number", Required: true},
			},
			result:   `{"date":"2026-05-20","location":"NYC","pollen_count":"high"}`,
			wantFail: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := &Task{
				ID:          "task-" + tt.name,
				WorkspaceID: "ws-1",
				OutputContract: NormalizeTaskOutputContract(&TaskOutputContract{
					Source:  "manual",
					Columns: tt.columns,
				}),
			}
			validation, csvData := ValidateTaskOutputContractResult(task, tt.result)
			if tt.wantFail {
				if validation.ValidationStatus != TaskValidationNeedsReview {
					t.Fatalf("validation status = %q, want needs_review", validation.ValidationStatus)
				}
				if csvData != "" {
					t.Fatalf("expected invalid result to skip csv, got %q", csvData)
				}
				return
			}
			if validation.ValidationStatus != TaskValidationPassed {
				t.Fatalf("validation status = %q, want passed: %+v", validation.ValidationStatus, validation.Errors)
			}
			if csvData != tt.wantCSV {
				t.Fatalf("csv = %q, want %q", csvData, tt.wantCSV)
			}
		})
	}
}

func TestAutoStoreResult_AppendCSVUsesContractAndRecordsValidation(t *testing.T) {
	tempDir := t.TempDir()
	task := contractedPollenTask(tempDir)
	RecordTaskExecution(task, "success", `{"date":"2026-05-20","location":"NYC","pollen_count":8,"high":true}`, time.Now(), time.Second)

	ws := &Workspace{ID: "ws-1", Name: "Workspace", Status: StatusActive}
	if err := ws.AddTask(*task); err != nil {
		t.Fatalf("add task: %v", err)
	}
	store := newTestWorkspaceStore(t, ws)

	AutoStoreResult(ws, task, `{"date":"2026-05-20","location":"NYC","pollen_count":8,"high":true}`, store)

	data, err := os.ReadFile(task.ResultStorage.FilePath)
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	want := "date,location,pollen_count,high\n2026-05-20,NYC,8,true"
	if string(data) != want {
		t.Fatalf("csv = %q, want %q", string(data), want)
	}

	updated, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}
	updatedTask, err := updated.GetTask(task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	validation := updatedTask.ExecutionHistory[len(updatedTask.ExecutionHistory)-1].Validation
	if validation == nil {
		t.Fatal("expected validation result")
	}
	if validation.ValidationStatus != TaskValidationPassed || validation.StorageStatus != TaskStorageAppended {
		t.Fatalf("validation = %+v, want passed/appended", validation)
	}
}

func TestAutoStoreResult_InvalidContractOutputSkipsAppend(t *testing.T) {
	tempDir := t.TempDir()
	task := contractedPollenTask(tempDir)
	RecordTaskExecution(task, "success", `{"date":"2026-05-20","location":"NYC"}`, time.Now(), time.Second)

	ws := &Workspace{ID: "ws-1", Name: "Workspace", Status: StatusActive}
	if err := ws.AddTask(*task); err != nil {
		t.Fatalf("add task: %v", err)
	}
	store := newTestWorkspaceStore(t, ws)

	AutoStoreResult(ws, task, `{"date":"2026-05-20","location":"NYC"}`, store)

	if _, err := os.Stat(task.ResultStorage.FilePath); !os.IsNotExist(err) {
		t.Fatalf("expected csv file not to be created, stat err = %v", err)
	}

	updated, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}
	updatedTask, err := updated.GetTask(task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	validation := updatedTask.ExecutionHistory[len(updatedTask.ExecutionHistory)-1].Validation
	if validation == nil {
		t.Fatal("expected validation result")
	}
	if validation.ValidationStatus != TaskValidationNeedsReview || validation.StorageStatus != TaskStorageSkippedInvalid {
		t.Fatalf("validation = %+v, want needs_review/skipped_invalid", validation)
	}
}

func TestAutoStoreResult_NoContractKeepsExistingAppendBehavior(t *testing.T) {
	tempDir := t.TempDir()
	task := &Task{
		ID:          "task-1",
		WorkspaceID: "ws-1",
		Description: "Daily note",
		ResultStorage: &ResultStorageConfig{
			Enabled:   true,
			FilePath:  filepath.Join(tempDir, "notes.csv"),
			Format:    "csv",
			WriteMode: "append",
		},
	}
	RecordTaskExecution(task, "success", "Pollen is high.", time.Now(), time.Second)

	ws := &Workspace{ID: "ws-1", Name: "Workspace", Status: StatusActive}
	if err := ws.AddTask(*task); err != nil {
		t.Fatalf("add task: %v", err)
	}
	store := newTestWorkspaceStore(t, ws)

	AutoStoreResult(ws, task, "Pollen is high.", store)

	data, err := os.ReadFile(task.ResultStorage.FilePath)
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	if !strings.Contains(string(data), "task_id,description,timestamp,agent,result") {
		t.Fatalf("expected legacy fallback csv, got %q", string(data))
	}

	updated, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}
	updatedTask, err := updated.GetTask(task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	validation := updatedTask.ExecutionHistory[len(updatedTask.ExecutionHistory)-1].Validation
	if validation == nil {
		t.Fatal("expected validation metadata")
	}
	if validation.ValidationStatus != TaskValidationNotApplicable || validation.StorageStatus != TaskStorageAppended {
		t.Fatalf("validation = %+v, want not_applicable/appended", validation)
	}
}

func contractedPollenTask(tempDir string) *Task {
	return &Task{
		ID:          "task-1",
		WorkspaceID: "ws-1",
		Description: "Daily pollen",
		OutputContract: NormalizeTaskOutputContract(&TaskOutputContract{
			Source: "manual",
			Columns: []TaskOutputContractColumn{
				{Name: "date", Type: "date", Required: true},
				{Name: "location", Type: "string", Required: true},
				{Name: "pollen_count", Type: "number", Required: true},
				{Name: "high", Type: "boolean"},
			},
		}),
		ResultStorage: &ResultStorageConfig{
			Enabled:   true,
			FilePath:  filepath.Join(tempDir, "pollen.csv"),
			Format:    "csv",
			WriteMode: "append",
		},
	}
}
