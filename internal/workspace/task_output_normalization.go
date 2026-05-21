package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
)

const (
	TaskOutputRepairNotAttempted = "not_attempted"
	TaskOutputRepairSucceeded    = "succeeded"
	TaskOutputRepairFailed       = "failed"

	taskOutputRawResultRefLatest      = "execution_history.latest.result"
	taskOutputNormalizedRowRefLatest  = "execution_history.latest.normalized_row"
	taskOutputNormalizationFailedCode = "normalization_failed"
)

// TaskOutputSpecAssistant optionally lets the execution harness ask the assigned
// agent to normalize prose/raw output into the approved row shape, then repair a
// row once if projection validation fails.
type TaskOutputSpecAssistant interface {
	NormalizeTaskOutputSpec(ctx context.Context, task Task, rawResult string) (string, error)
	RepairTaskOutputSpec(ctx context.Context, task Task, rawResult string, invalidRow map[string]any, validationErrors []TaskValidationError) (string, error)
}

// ValidateTaskOutputSpecResult normalizes a raw result under the task's active
// structured output spec, validates the normalized row, and returns CSV ready
// for storage. It uses only deterministic parsing; callers that can use an
// assistant should call ValidateTaskOutputSpecResultWithAssistant.
func ValidateTaskOutputSpecResult(task *Task, result string) (*TaskValidationResult, string) {
	return ValidateTaskOutputSpecResultWithAssistant(context.Background(), task, result, nil)
}

// ValidateTaskOutputSpecResultWithAssistant normalizes and validates a task
// result. It first accepts raw results that already match the approved schema.
// When an assistant is supplied, it can perform one normalization attempt for
// prose/raw output and one bounded repair attempt for validation failures.
func ValidateTaskOutputSpecResultWithAssistant(ctx context.Context, task *Task, result string, assistant TaskOutputSpecAssistant) (*TaskValidationResult, string) {
	if task == nil {
		return notApplicableValidationResult(), ""
	}
	spec := SnapshotTaskOutputSpec(ActiveTaskOutputSpec(task))
	normalizedSpec, errs := NormalizeTaskOutputSpec(spec)
	if normalizedSpec == nil {
		return notApplicableValidationResult(), ""
	}
	now := time.Now().UTC()
	validation := &TaskValidationResult{
		ValidationStatus: TaskValidationPassed,
		StorageStatus:    TaskStorageNotAttempted,
		ContractVersion:  strings.TrimSpace(normalizedSpec.Version),
		RawOutputRef:     taskOutputRawResultRefLatest,
		OutputSpec:       SnapshotTaskOutputSpec(normalizedSpec),
		RepairStatus:     TaskOutputRepairNotAttempted,
		ValidatedAt:      &now,
	}
	if validation.ContractVersion == "" && normalizedSpec.Contract != nil {
		validation.ContractVersion = normalizedSpec.Contract.Version
	}
	logTaskOutputSpecTelemetry("normalization_attempted", task, validation, nil)
	if len(errs) > 0 {
		validation.ValidationStatus = TaskValidationNeedsReview
		validation.StorageStatus = TaskStorageSkippedInvalid
		for _, msg := range errs {
			validation.Errors = append(validation.Errors, TaskValidationError{
				Code:    "invalid_output_spec",
				Message: msg,
			})
		}
		logTaskOutputSpecTelemetry("normalization_failed", task, validation, logger.Fields{"reason": "invalid_output_spec"})
		return validation, ""
	}

	row, err := normalizeTaskOutputSpecRow(normalizedSpec, result)
	normalizationSource := "deterministic"
	if err != nil {
		if assistant == nil {
			recordTaskOutputSpecFailure(validation, taskOutputNormalizationFailedCode, err.Error())
			logTaskOutputSpecTelemetry("normalization_failed", task, validation, logger.Fields{"reason": taskOutputNormalizationFailedCode})
			return validation, ""
		}
		assistantResult, assistantErr := assistant.NormalizeTaskOutputSpec(ctx, taskOutputSpecAssistantTask(*task, normalizedSpec), result)
		if assistantErr != nil {
			recordTaskOutputSpecFailure(validation, "normalization_provider_error", assistantErr.Error())
			logTaskOutputSpecTelemetry("normalization_failed", task, validation, logger.Fields{"reason": "normalization_provider_error"})
			return validation, ""
		}
		row, err = normalizeTaskOutputSpecRow(normalizedSpec, assistantResult)
		if err != nil {
			recordTaskOutputSpecFailure(validation, taskOutputNormalizationFailedCode, fmt.Sprintf("Assistant normalized result is invalid: %v", err))
			logTaskOutputSpecTelemetry("normalization_failed", task, validation, logger.Fields{"reason": taskOutputNormalizationFailedCode})
			return validation, ""
		}
		normalizationSource = "assistant"
	}
	logTaskOutputSpecTelemetry("normalization_succeeded", task, validation, logger.Fields{"source": normalizationSource})

	csvData, projectErrs := validateAndRecordTaskOutputSpecRow(task, normalizedSpec, validation, row)
	if len(projectErrs) > 0 {
		if assistant == nil {
			recordTaskOutputSpecErrors(validation, projectErrs)
			return validation, ""
		}
		logTaskOutputSpecTelemetry("repair_attempted", task, validation, logger.Fields{"error_count": len(projectErrs)})
		repaired, repairErr := assistant.RepairTaskOutputSpec(ctx, taskOutputSpecAssistantTask(*task, normalizedSpec), result, row, projectErrs)
		if repairErr != nil {
			validation.RepairStatus = TaskOutputRepairFailed
			recordTaskOutputSpecFailure(validation, "repair_provider_error", repairErr.Error())
			logTaskOutputSpecTelemetry("repair_failed", task, validation, logger.Fields{"reason": "repair_provider_error"})
			return validation, ""
		}
		repairedRow, err := normalizeTaskOutputSpecRow(normalizedSpec, repaired)
		if err != nil {
			validation.RepairStatus = TaskOutputRepairFailed
			recordTaskOutputSpecFailure(validation, "repair_failed", fmt.Sprintf("Assistant repaired result is invalid: %v", err))
			logTaskOutputSpecTelemetry("repair_failed", task, validation, logger.Fields{"reason": "invalid_repaired_result"})
			return validation, ""
		}
		csvData, projectErrs = validateAndRecordTaskOutputSpecRow(task, normalizedSpec, validation, repairedRow)
		if len(projectErrs) > 0 {
			validation.RepairStatus = TaskOutputRepairFailed
			recordTaskOutputSpecErrors(validation, projectErrs)
			logTaskOutputSpecTelemetry("repair_failed", task, validation, logger.Fields{"reason": "repaired_row_invalid", "error_count": len(projectErrs)})
			return validation, ""
		}
		validation.RepairStatus = TaskOutputRepairSucceeded
		validation.Errors = nil
		logTaskOutputSpecTelemetry("repair_succeeded", task, validation, nil)
	}
	return validation, csvData
}

func taskOutputSpecAssistantTask(task Task, spec *TaskOutputSpec) Task {
	task.OutputSpec = SnapshotTaskOutputSpec(spec)
	if task.OutputSpec != nil {
		task.OutputSchema = task.OutputSpec.Schema
		task.OutputContract = task.OutputSpec.Contract
	}
	return task
}

func validateAndRecordTaskOutputSpecRow(task *Task, spec *TaskOutputSpec, validation *TaskValidationResult, row map[string]any) (string, []TaskValidationError) {
	if task == nil || validation == nil {
		return "", nil
	}
	if task.Context == nil {
		task.Context = map[string]any{}
	}
	task.Context["normalized_output"] = row
	validation.NormalizedRowRef = taskOutputNormalizedRowRefLatest
	validation.NormalizedRow = cloneTaskOutputNormalizedRow(row)

	columns, projectedRow, projectErrs := projectTaskOutputSpecRow(spec, row, latestTaskOutputMetadata(task))
	if len(projectErrs) > 0 {
		return "", projectErrs
	}
	return writeCSV(columns, []map[string]string{projectedRow}), nil
}

func recordTaskOutputSpecFailure(validation *TaskValidationResult, code, message string) {
	recordTaskOutputSpecErrors(validation, []TaskValidationError{{
		Code:    code,
		Message: message,
	}})
}

func recordTaskOutputSpecErrors(validation *TaskValidationResult, errs []TaskValidationError) {
	if validation == nil {
		return
	}
	validation.ValidationStatus = TaskValidationNeedsReview
	validation.StorageStatus = TaskStorageSkippedInvalid
	validation.Errors = append(validation.Errors, errs...)
}

func cloneTaskOutputNormalizedRow(row map[string]any) map[string]any {
	if row == nil {
		return nil
	}
	data, err := json.Marshal(row)
	if err != nil {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}
	return out
}

func normalizeTaskOutputSpecRow(spec *TaskOutputSpec, result string) (map[string]any, error) {
	if spec == nil {
		return nil, fmt.Errorf("output spec is required")
	}
	if spec.Schema != nil {
		row, err := ValidateTaskStructuredOutput(spec.Schema, result)
		if err != nil {
			return nil, err
		}
		if row == nil {
			return nil, fmt.Errorf("result did not produce a structured row")
		}
		return row, nil
	}
	var row map[string]any
	decoder := json.NewDecoder(strings.NewReader(strings.TrimSpace(result)))
	decoder.UseNumber()
	if err := decoder.Decode(&row); err != nil {
		return nil, fmt.Errorf("result must be valid JSON object: %w", err)
	}
	if row == nil {
		return nil, fmt.Errorf("result must be a JSON object")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("result must contain only one JSON object")
	}
	return row, nil
}

func projectTaskOutputSpecRow(spec *TaskOutputSpec, row map[string]any, metadata map[string]string) ([]string, map[string]string, []TaskValidationError) {
	if spec == nil || spec.Contract == nil {
		return nil, nil, []TaskValidationError{{Code: "missing_output_contract", Message: "Output spec must include a CSV output contract."}}
	}
	projected := map[string]string{}
	columns := []string{}
	seen := map[string]struct{}{}
	addColumn := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		columns = append(columns, name)
	}

	for _, field := range spec.MetadataPolicy.Fields {
		if !field.Include {
			continue
		}
		name := strings.TrimSpace(field.Name)
		if name == "" {
			continue
		}
		addColumn(name)
		projected[name] = metadata[name]
	}

	mappingsByColumn := make(map[string]TaskOutputMapping, len(spec.Mappings))
	for _, mapping := range spec.Mappings {
		mappingsByColumn[strings.ToLower(mapping.CSVColumn)] = mapping
	}

	var errs []TaskValidationError
	for _, column := range spec.Contract.Columns {
		addColumn(column.Name)
		mapping, ok := mappingsByColumn[strings.ToLower(column.Name)]
		value := ""
		if ok {
			raw, exists := row[mapping.SchemaField]
			if (!exists || isEmptyTaskOutputValue(raw)) && strings.TrimSpace(mapping.DefaultValue) != "" {
				value = strings.TrimSpace(mapping.DefaultValue)
			} else if exists {
				value = taskOutputMappedValue(raw, mapping.Transform)
			}
		}
		value = strings.TrimSpace(value)
		if column.Required && value == "" {
			errs = append(errs, TaskValidationError{
				Code:    "missing_required_column",
				Column:  column.Name,
				Message: fmt.Sprintf("Missing required column `%s`.", column.Name),
			})
			continue
		}
		if value != "" {
			if err := validateOutputContractValue(column, value); err != nil {
				errs = append(errs, TaskValidationError{
					Code:    "type_mismatch",
					Column:  column.Name,
					Message: err.Error(),
				})
				continue
			}
		}
		projected[column.Name] = value
	}
	return columns, projected, errs
}

func taskOutputMappedValue(value any, transform string) string {
	switch strings.ToLower(strings.TrimSpace(transform)) {
	case TaskOutputMappingTransformJSONString:
		data, err := json.Marshal(value)
		if err == nil {
			return string(data)
		}
	}
	return csvCellValue(value)
}

func isEmptyTaskOutputValue(value any) bool {
	if value == nil {
		return true
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text) == ""
	}
	return false
}

func latestTaskOutputMetadata(task *Task) map[string]string {
	metadata := map[string]string{
		"run_id":      "",
		"executed_at": "",
		"status":      "",
		"duration_ms": "",
	}
	if task == nil || len(task.ExecutionHistory) == 0 {
		return metadata
	}
	entry := task.ExecutionHistory[len(task.ExecutionHistory)-1]
	metadata["run_id"] = strings.TrimSpace(entry.RunID)
	if !entry.ExecutedAt.IsZero() {
		metadata["executed_at"] = entry.ExecutedAt.Format(time.RFC3339Nano)
	}
	metadata["status"] = strings.TrimSpace(entry.Status)
	if entry.Duration > 0 {
		metadata["duration_ms"] = fmt.Sprintf("%d", entry.Duration)
	}
	return metadata
}

func logTaskOutputSpecTelemetry(action string, task *Task, validation *TaskValidationResult, fields logger.Fields) {
	if fields == nil {
		fields = logger.Fields{}
	}
	if task != nil {
		fields["workspace_id"] = task.WorkspaceID
		fields["task_id"] = task.ID
		fields["run_id"] = latestTaskOutputMetadata(task)["run_id"]
	}
	if validation != nil {
		fields["contract_version"] = validation.ContractVersion
		fields["validation_status"] = validation.ValidationStatus
		fields["storage_status"] = validation.StorageStatus
		fields["error_count"] = len(validation.Errors)
	}
	fields["action"] = action
	logger.Info("Task output normalization telemetry", fields)
}
