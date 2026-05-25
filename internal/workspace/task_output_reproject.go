package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// knownReprojectValue returns a deterministic value for harness-known columns
// (task/run metadata and the raw result), so re-projecting a result into a
// destination CSV's columns doesn't need the LLM for fields the harness
// already owns. The bool is false when the column isn't a recognized harness
// field and must be extracted from the result instead.
func knownReprojectValue(task *Task, rawResult, column string) (string, bool) {
	meta := latestTaskOutputMetadata(task)
	switch strings.ToLower(strings.TrimSpace(column)) {
	case "task_id", "taskid", "id":
		if task != nil {
			return task.ID, true
		}
		return "", true
	case "description", "task", "prompt", "name", "title":
		if task != nil {
			return task.Description, true
		}
		return "", true
	case "timestamp", "executed_at", "date", "time", "created_at", "ran_at":
		if v := meta["executed_at"]; v != "" {
			return v, true
		}
		return time.Now().UTC().Format(time.RFC3339Nano), true
	case "agent", "assigned_agent", "assignee", "to":
		if task != nil {
			return task.To, true
		}
		return "", true
	case "run_id", "runid":
		return meta["run_id"], true
	case "status":
		return meta["status"], true
	case "duration_ms", "duration":
		return meta["duration_ms"], true
	case "result", "output", "response", "answer", "value", "content":
		return strings.TrimSpace(rawResult), true
	case "summary":
		return strings.TrimSpace(latestTaskExecutionSummary(task)), true
	}
	return "", false
}

func latestTaskExecutionSummary(task *Task) string {
	if task == nil || len(task.ExecutionHistory) == 0 {
		return ""
	}
	return task.ExecutionHistory[len(task.ExecutionHistory)-1].Summary
}

// ReprojectResultToColumns rebuilds a task's raw result into a single CSV row
// whose header exactly matches targetColumns — typically a destination file's
// existing header that a fresh run failed to match. Harness-known columns
// (task/run metadata, the raw result) are filled deterministically; remaining
// columns are extracted from the raw result by the LLM assistant when one is
// available, and left blank otherwise. Returns the CSV (header + one row) and
// whether the assistant was invoked.
func ReprojectResultToColumns(ctx context.Context, task *Task, rawResult string, targetColumns []string, assistant TaskOutputSpecAssistant) (string, bool, error) {
	cleaned := make([]string, 0, len(targetColumns))
	for _, column := range targetColumns {
		if trimmed := strings.TrimSpace(column); trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	if len(cleaned) == 0 {
		return "", false, fmt.Errorf("no target columns to project into")
	}

	row := make(map[string]string, len(cleaned))
	var unknown []string
	for _, column := range cleaned {
		if value, ok := knownReprojectValue(task, rawResult, column); ok {
			row[column] = value
		} else {
			unknown = append(unknown, column)
		}
	}

	usedAssistant := false
	if len(unknown) > 0 && assistant != nil && task != nil {
		extractTask := taskOutputSpecAssistantTask(*task, buildReprojectExtractionSpec(unknown))
		raw, err := assistant.NormalizeTaskOutputSpec(ctx, extractTask, rawResult)
		if err != nil {
			return "", true, fmt.Errorf("assistant could not extract columns %v: %w", unknown, err)
		}
		usedAssistant = true
		var object map[string]any
		if jsonErr := json.Unmarshal([]byte(strings.TrimSpace(raw)), &object); jsonErr == nil {
			lookup := make(map[string]any, len(object))
			for key, value := range object {
				lookup[strings.ToLower(strings.TrimSpace(key))] = value
			}
			for _, column := range unknown {
				if value, ok := lookup[strings.ToLower(column)]; ok {
					row[column] = csvCellValue(value)
				}
			}
		}
	}

	return writeCSV(cleaned, []map[string]string{row}), usedAssistant, nil
}

// buildReprojectExtractionSpec creates a minimal output spec whose schema and
// contract are exactly the given columns, so NormalizeTaskOutputSpec asks the
// assistant for one JSON object keyed by those column names.
func buildReprojectExtractionSpec(columns []string) *TaskOutputSpec {
	schemaFields := make([]TaskOutputField, 0, len(columns))
	contractColumns := make([]TaskOutputContractColumn, 0, len(columns))
	mappings := make([]TaskOutputMapping, 0, len(columns))
	for _, column := range columns {
		schemaFields = append(schemaFields, TaskOutputField{Name: column, Type: "string"})
		contractColumns = append(contractColumns, TaskOutputContractColumn{Name: column, Type: "string"})
		mappings = append(mappings, TaskOutputMapping{
			SchemaField: column,
			CSVColumn:   column,
			Transform:   TaskOutputMappingTransformIdentity,
		})
	}
	return &TaskOutputSpec{
		Source:   "csv_header",
		Schema:   &TaskOutputSchema{Name: "destination_columns", Fields: schemaFields},
		Contract: &TaskOutputContract{Columns: contractColumns},
		Mappings: mappings,
	}
}
