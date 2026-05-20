package workspace

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// TaskResultToCSV converts a task result into importable CSV for result storage.
func TaskResultToCSV(task *Task, result, timestamp, agent string) string {
	if looksLikeCSV(result) {
		return strings.TrimSpace(result)
	}

	if columns, rows := taskStructuredRowsForCSV(task, result); len(columns) > 0 && len(rows) > 0 {
		return writeCSV(columns, rows)
	}

	row := map[string]string{
		"task_id":     "",
		"description": "",
		"timestamp":   timestamp,
		"agent":       agent,
		"result":      strings.TrimSpace(result),
	}
	if task != nil {
		row["task_id"] = task.ID
		row["description"] = task.Description
	}
	return writeCSV([]string{"task_id", "description", "timestamp", "agent", "result"}, []map[string]string{row})
}

func looksLikeCSV(value string) bool {
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(value, "\r\n", "\n")), "\n")
	if len(lines) < 2 {
		return false
	}
	return strings.Count(lines[0], ",") > 0 && strings.Count(lines[1], ",") > 0
}

func taskStructuredRowsForCSV(task *Task, result string) ([]string, []map[string]string) {
	if task != nil && task.Context != nil {
		if output, ok := task.Context["structured_output"]; ok {
			if columns, rows := csvRowsFromValue(output); len(columns) > 0 && len(rows) > 0 {
				return columns, rows
			}
		}
	}

	var decoded interface{}
	decoder := json.NewDecoder(strings.NewReader(strings.TrimSpace(result)))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return nil, nil
	}
	return csvRowsFromValue(decoded)
}

func csvRowsFromValue(value interface{}) ([]string, []map[string]string) {
	switch typed := value.(type) {
	case []interface{}:
		rows := make([]map[string]string, 0, len(typed))
		for _, item := range typed {
			object, ok := item.(map[string]interface{})
			if !ok {
				return nil, nil
			}
			rows = append(rows, csvRowFromMap(object))
		}
		return csvColumns(rows, nil), rows
	case map[string]interface{}:
		for _, key := range []string{"data", "rows", "items"} {
			if nested, ok := typed[key]; ok {
				if columns, rows := csvRowsFromValue(nested); len(columns) > 0 && len(rows) > 0 {
					return columns, rows
				}
			}
		}
		return csvColumns(nil, typed), []map[string]string{csvRowFromMap(typed)}
	default:
		return nil, nil
	}
}

func csvRowFromMap(values map[string]interface{}) map[string]string {
	row := make(map[string]string, len(values))
	for key, value := range values {
		row[strings.TrimSpace(key)] = csvCellValue(value)
	}
	return row
}

func csvCellValue(value interface{}) string {
	if value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	case float64, bool:
		data, err := json.Marshal(typed)
		if err == nil {
			return string(data)
		}
	}
	data, err := json.Marshal(value)
	if err != nil {
		return strings.TrimSpace(fmt.Sprint(value))
	}
	return string(data)
}

func csvColumns(rows []map[string]string, object map[string]interface{}) []string {
	seen := make(map[string]struct{})
	columns := []string{}
	add := func(column string) {
		column = strings.TrimSpace(column)
		if column == "" {
			return
		}
		key := strings.ToLower(column)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		columns = append(columns, column)
	}

	for _, preferred := range []string{"timestamp", "date", "location", "metric", "value", "unit", "source", "summary"} {
		if object != nil {
			if _, ok := object[preferred]; ok {
				add(preferred)
			}
		}
		for _, row := range rows {
			if _, ok := row[preferred]; ok {
				add(preferred)
			}
		}
	}

	remaining := []string{}
	if object != nil {
		for key := range object {
			remaining = append(remaining, key)
		}
	}
	for _, row := range rows {
		for key := range row {
			remaining = append(remaining, key)
		}
	}
	sort.Strings(remaining)
	for _, key := range remaining {
		add(key)
	}
	return columns
}

func writeCSV(columns []string, rows []map[string]string) string {
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	_ = writer.Write(columns)
	for _, row := range rows {
		record := make([]string, len(columns))
		for index, column := range columns {
			record[index] = row[column]
		}
		_ = writer.Write(record)
	}
	writer.Flush()
	return strings.TrimRight(buffer.String(), "\n")
}

func csvWithoutHeader(csvData string) string {
	normalized := strings.TrimSpace(strings.ReplaceAll(csvData, "\r\n", "\n"))
	lines := strings.Split(normalized, "\n")
	if len(lines) <= 1 {
		return ""
	}
	return strings.TrimSpace(strings.Join(lines[1:], "\n"))
}

// AppendCSVToFile appends CSV rows to filePath, writing the header only once.
func AppendCSVToFile(filePath, csvData string) error {
	trimmed := strings.TrimSpace(csvData)
	if trimmed == "" {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return err
	}

	writeHeader := true
	if info, err := os.Stat(filePath); err == nil && info.Size() > 0 {
		writeHeader = false
		trimmed = csvWithoutHeader(trimmed)
	}
	if strings.TrimSpace(trimmed) == "" {
		return nil
	}

	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	if !writeHeader {
		if _, err := file.WriteString("\n"); err != nil {
			return err
		}
	}
	_, err = file.WriteString(trimmed)
	return err
}

func csvWithoutHeaderForExistingStore(node *StoreNode, filePath, csvData string) string {
	if node == nil {
		return csvData
	}
	finalPath, err := BuildFinalPath(node.BaseDir, filePath)
	if err != nil {
		return csvData
	}
	if info, err := os.Stat(finalPath); err == nil && info.Size() > 0 {
		return csvWithoutHeader(csvData)
	}
	return csvData
}

// BootstrapOutputContractFromCSVHeader derives a string-typed output contract from an existing CSV header.
func BootstrapOutputContractFromCSVHeader(ws *Workspace, task *Task) *TaskOutputContract {
	if task == nil || NormalizeTaskOutputContract(task.OutputContract) != nil {
		return NormalizeTaskOutputContract(task.OutputContract)
	}
	storage := task.ResultStorage
	if storage == nil || !storage.Enabled || strings.ToLower(strings.TrimSpace(storage.WriteMode)) != "append" {
		return nil
	}

	filePath := strings.TrimSpace(storage.FilePath)
	if filePath == "" {
		return nil
	}
	if strings.TrimSpace(storage.StoreNodeID) != "" {
		if ws == nil {
			return nil
		}
		var node *StoreNode
		for i := range ws.StoreNodes {
			if ws.StoreNodes[i].ID == storage.StoreNodeID || ws.StoreNodes[i].CanvasNodeID == storage.StoreNodeID {
				node = &ws.StoreNodes[i]
				break
			}
		}
		if node == nil {
			return nil
		}
		finalPath, err := BuildFinalPath(node.BaseDir, filePath)
		if err != nil {
			return nil
		}
		filePath = finalPath
	}

	file, err := os.Open(filePath)
	if err != nil {
		return nil
	}
	defer func() { _ = file.Close() }()

	reader := csv.NewReader(file)
	reader.FieldsPerRecord = -1
	header, err := reader.Read()
	if err != nil || len(header) == 0 {
		return nil
	}

	columns := make([]TaskOutputContractColumn, 0, len(header))
	seen := map[string]struct{}{}
	for _, raw := range header {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		columns = append(columns, TaskOutputContractColumn{
			Name:     name,
			Type:     "string",
			Required: true,
		})
	}
	return NormalizeTaskOutputContract(&TaskOutputContract{
		Source:  "csv_header",
		Columns: columns,
	})
}

// ResolveTaskResultStorageOwner returns the task that owns result storage for
// a single task or workflow parent. V1 workflow ownership defaults to the final
// subtask ordered by SubtaskIndex, with CreatedAt/ID as deterministic ties.
func ResolveTaskResultStorageOwner(ws *Workspace, task *Task) *Task {
	if task == nil {
		return nil
	}
	if ws == nil || strings.TrimSpace(task.ID) == "" {
		clone := *task
		return &clone
	}

	subtasks := ws.GetSubtasks(task.ID)
	if len(subtasks) == 0 {
		clone := *task
		return &clone
	}
	sort.SliceStable(subtasks, func(i, j int) bool {
		left := subtasks[i]
		right := subtasks[j]
		if left.SubtaskIndex != right.SubtaskIndex {
			return left.SubtaskIndex < right.SubtaskIndex
		}
		if !left.CreatedAt.Equal(right.CreatedAt) {
			return left.CreatedAt.Before(right.CreatedAt)
		}
		return left.ID < right.ID
	})
	owner := subtasks[len(subtasks)-1]
	return &owner
}

// ResolveTaskResultStorageOwnerID returns the ID of ResolveTaskResultStorageOwner.
func ResolveTaskResultStorageOwnerID(ws *Workspace, task *Task) string {
	owner := ResolveTaskResultStorageOwner(ws, task)
	if owner == nil {
		return ""
	}
	return owner.ID
}
