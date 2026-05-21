package workspace

import (
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"
)

const outputContractRawOutputRefLatest = "execution_history.latest.result"

// NormalizeTaskOutputContract sanitizes and normalizes a CSV-oriented task output contract.
func NormalizeTaskOutputContract(contract *TaskOutputContract) *TaskOutputContract {
	if contract == nil {
		return nil
	}

	normalized := &TaskOutputContract{
		Source:  normalizeTaskOutputContractSource(contract.Source),
		Columns: make([]TaskOutputContractColumn, 0, len(contract.Columns)),
	}
	seen := make(map[string]struct{}, len(contract.Columns))
	for _, column := range contract.Columns {
		name := strings.TrimSpace(column.Name)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		normalized.Columns = append(normalized.Columns, TaskOutputContractColumn{
			Name:        name,
			Type:        normalizeTaskOutputContractColumnType(column.Type),
			Required:    column.Required,
			Description: strings.TrimSpace(column.Description),
		})
	}

	if len(normalized.Columns) == 0 {
		return nil
	}
	normalized.Version = outputContractVersion(normalized)
	return normalized
}

func normalizeTaskOutputContractSource(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "ai_suggested", "manual", "csv_header":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func normalizeTaskOutputContractColumnType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "string", "number", "boolean", "date":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "string"
	}
}

func outputContractVersion(contract *TaskOutputContract) string {
	type versionColumn struct {
		Name        string `json:"name"`
		Type        string `json:"type"`
		Required    bool   `json:"required"`
		Description string `json:"description,omitempty"`
	}
	canonical := struct {
		Source  string          `json:"source,omitempty"`
		Columns []versionColumn `json:"columns"`
	}{
		Source:  contract.Source,
		Columns: make([]versionColumn, 0, len(contract.Columns)),
	}
	for _, column := range contract.Columns {
		canonical.Columns = append(canonical.Columns, versionColumn(column))
	}
	data, _ := json.Marshal(canonical)
	sum := sha256.Sum256(data)
	return "ocv_" + hex.EncodeToString(sum[:])[:12]
}

// ValidateTaskOutputContractResult validates result against task's output contract and returns contract-ordered CSV.
func ValidateTaskOutputContractResult(task *Task, result string) (*TaskValidationResult, string) {
	if task == nil {
		return notApplicableValidationResult(), ""
	}
	contract := NormalizeTaskOutputContract(task.OutputContract)
	if contract == nil {
		return notApplicableValidationResult(), ""
	}
	return validateOutputContractResult(contract, task, result)
}

func notApplicableValidationResult() *TaskValidationResult {
	now := time.Now().UTC()
	return &TaskValidationResult{
		ValidationStatus: TaskValidationNotApplicable,
		StorageStatus:    TaskStorageNotAttempted,
		ValidatedAt:      &now,
	}
}

func validateOutputContractResult(contract *TaskOutputContract, task *Task, result string) (*TaskValidationResult, string) {
	now := time.Now().UTC()
	validation := &TaskValidationResult{
		ValidationStatus: TaskValidationPassed,
		StorageStatus:    TaskStorageNotAttempted,
		ContractVersion:  contract.Version,
		ValidatedAt:      &now,
	}

	rows := outputContractRowsFromTaskResult(task, result)
	if len(rows) == 0 {
		validation.ValidationStatus = TaskValidationNeedsReview
		validation.StorageStatus = TaskStorageSkippedInvalid
		validation.RawOutputRef = outputContractRawOutputRefLatest
		validation.Errors = []TaskValidationError{{
			Code:    "unparseable_result",
			Message: "Result must be structured JSON or CSV with a header row.",
		}}
		return validation, ""
	}

	contractColumns := make([]string, 0, len(contract.Columns))
	contractRows := make([]map[string]string, 0, len(rows))
	for rowIndex, row := range rows {
		indexedRow := indexOutputContractRow(row)
		contractRow := make(map[string]string, len(contract.Columns))
		for _, column := range contract.Columns {
			contractColumns = appendContractColumn(contractColumns, column.Name)
			value, ok := indexedRow[strings.ToLower(column.Name)]
			value = strings.TrimSpace(value)
			if column.Required && (!ok || value == "") {
				validation.Errors = append(validation.Errors, TaskValidationError{
					Code:    "missing_required_column",
					Column:  column.Name,
					Message: fmt.Sprintf("Missing required column `%s`.", column.Name),
				})
				continue
			}
			if value != "" {
				if err := validateOutputContractValue(column, value); err != nil {
					validation.Errors = append(validation.Errors, TaskValidationError{
						Code:    "type_mismatch",
						Column:  column.Name,
						Message: err.Error(),
					})
					continue
				}
			}
			contractRow[column.Name] = value
		}
		if len(validation.Errors) == 0 || len(contractRows) == rowIndex {
			contractRows = append(contractRows, contractRow)
		}
	}

	if len(validation.Errors) > 0 {
		validation.ValidationStatus = TaskValidationNeedsReview
		validation.StorageStatus = TaskStorageSkippedInvalid
		validation.RawOutputRef = outputContractRawOutputRefLatest
		return validation, ""
	}

	return validation, writeCSV(contractColumns, contractRows)
}

func appendContractColumn(columns []string, name string) []string {
	if slices.Contains(columns, name) {
		return columns
	}
	return append(columns, name)
}

func outputContractRowsFromTaskResult(task *Task, result string) []map[string]string {
	if task != nil && task.Context != nil {
		if output, ok := task.Context["structured_output"]; ok {
			if _, rows := csvRowsFromValue(output); len(rows) > 0 {
				return rows
			}
		}
	}

	trimmed := strings.TrimSpace(result)
	if trimmed == "" {
		return nil
	}

	var decoded any
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err == nil {
		var trailing any
		if err := decoder.Decode(&trailing); err == io.EOF {
			if _, rows := csvRowsFromValue(decoded); len(rows) > 0 {
				return rows
			}
		}
	}

	return outputContractRowsFromCSV(trimmed)
}

func outputContractRowsFromCSV(value string) []map[string]string {
	reader := csv.NewReader(strings.NewReader(value))
	reader.TrimLeadingSpace = true
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil || len(records) < 2 {
		return nil
	}
	header := records[0]
	if len(header) == 0 {
		return nil
	}
	rows := make([]map[string]string, 0, len(records)-1)
	for _, record := range records[1:] {
		if len(record) == 0 {
			continue
		}
		row := make(map[string]string, len(header))
		for i, name := range header {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if i < len(record) {
				row[name] = strings.TrimSpace(record[i])
			} else {
				row[name] = ""
			}
		}
		rows = append(rows, row)
	}
	return rows
}

func indexOutputContractRow(row map[string]string) map[string]string {
	indexed := make(map[string]string, len(row))
	for key, value := range row {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if normalized == "" {
			continue
		}
		indexed[normalized] = value
	}
	return indexed
}

func validateOutputContractValue(column TaskOutputContractColumn, value string) error {
	switch column.Type {
	case "number":
		if _, err := strconv.ParseFloat(value, 64); err != nil {
			return fmt.Errorf("column `%s` must be a number", column.Name)
		}
	case "boolean":
		if _, err := strconv.ParseBool(strings.ToLower(value)); err != nil {
			return fmt.Errorf("column `%s` must be true or false", column.Name)
		}
	case "date":
		if !isOutputContractDate(value) {
			return fmt.Errorf("column `%s` must be a date", column.Name)
		}
	}
	return nil
}

func isOutputContractDate(value string) bool {
	for _, layout := range []string{time.DateOnly, time.RFC3339, "2006-01-02 15:04:05", "2006/01/02"} {
		if _, err := time.Parse(layout, value); err == nil {
			return true
		}
	}
	return false
}

// BuildTaskOutputContractPrompt renders final-answer instructions for a task output contract.
func BuildTaskOutputContractPrompt(contract *TaskOutputContract) string {
	normalized := NormalizeTaskOutputContract(contract)
	if normalized == nil {
		return ""
	}

	var prompt strings.Builder
	prompt.WriteString("Return ONLY a valid JSON object matching this output contract. Do not wrap it in markdown fences.\n")
	prompt.WriteString("Fields:\n")
	for _, column := range normalized.Columns {
		requiredLabel := "optional"
		if column.Required {
			requiredLabel = "required"
		}
		fmt.Fprintf(&prompt, "- %s (%s, %s)", column.Name, column.Type, requiredLabel)
		if column.Description != "" {
			prompt.WriteString(": ")
			prompt.WriteString(column.Description)
		}
		prompt.WriteString("\n")
	}
	prompt.WriteString("Use ISO dates like 2026-05-20 for date fields. Do not include commentary outside the JSON object.")
	return strings.TrimSpace(prompt.String())
}

// ApplyTaskValidationResultToLatestExecution annotates the latest matching run history entry.
func ApplyTaskValidationResultToLatestExecution(task *Task, validation *TaskValidationResult) {
	if task == nil || validation == nil || len(task.ExecutionHistory) == 0 {
		return
	}
	task.ExecutionHistory[len(task.ExecutionHistory)-1].Validation = validation
}
