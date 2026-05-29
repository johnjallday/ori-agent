package workspace

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// JSONL (newline-delimited JSON) is the canonical append format for recurring
// task output: each run appends one JSON object per data record. Unlike the
// CSV path it needs no header, no column contract, and no header-mismatch
// reconciliation — every record carries its own keys. CSV is produced on
// demand from the JSONL via ExportCSVFromJSONL.

// AppendJSONLFileName returns the file an append run writes to within the
// default/derived output folder: the user's custom FileName (normalized to
// .jsonl) when set, otherwise a slug derived from the task description.
func AppendJSONLFileName(task *Task, storage *ResultStorageConfig) string {
	if storage != nil {
		if custom := sanitizeAppendFileName(storage.FileName, "jsonl"); custom != "" {
			return custom
		}
	}
	return appendFileSlug(taskAppendSlugSource(task)) + ".jsonl"
}

// sanitizeAppendFileName strips any directory component and forces the given
// extension, keeping the write inside the chosen folder. Returns "" when the
// name has no usable characters.
func sanitizeAppendFileName(name, ext string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	name = filepath.Base(filepath.Clean(name))
	lower := strings.ToLower(name)
	for _, known := range []string{".jsonl", ".csv", ".json"} {
		if strings.HasSuffix(lower, known) {
			name = name[:len(name)-len(known)]
			break
		}
	}
	slug := appendFileSlug(name)
	if slug == "" {
		return ""
	}
	return slug + "." + ext
}

func taskAppendSlugSource(task *Task) string {
	if task == nil {
		return ""
	}
	name := task.Description
	if len(name) > 30 {
		name = name[:30]
	}
	return name
}

// appendFileSlug reduces a name to a filename-safe slug (letters, digits, '_',
// '-'; spaces become underscores). Falls back to "task" when empty.
func appendFileSlug(name string) string {
	var slug strings.Builder
	for _, r := range name {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-':
			slug.WriteRune(r)
		case r == ' ':
			slug.WriteByte('_')
		}
	}
	if slug.Len() == 0 {
		return "task"
	}
	return slug.String()
}

// TaskResultToJSONLRecords extracts the run's data records from the task's
// structured output (or its decoded JSON result) and merges the supplied run
// metadata into each. An array result yields one record per element; an object
// yields a single record. When there is no structured output it falls back to a
// single record wrapping the raw result, so a run is never dropped. Existing
// data keys are never overwritten by metadata.
func TaskResultToJSONLRecords(task *Task, result string, metadata map[string]any) []map[string]any {
	records := taskStructuredRecords(task, result)
	if len(records) == 0 {
		record := map[string]any{"result": strings.TrimSpace(result)}
		if task != nil {
			record["task_id"] = task.ID
			record["description"] = task.Description
		}
		records = []map[string]any{record}
	}

	if len(metadata) > 0 {
		for _, record := range records {
			for key, value := range metadata {
				if _, exists := record[key]; exists {
					continue
				}
				record[key] = value
			}
		}
	}
	return records
}

// taskStructuredRecords pulls structured records from Context["structured_output"]
// first (set by output normalization), then falls back to decoding the result
// string as JSON. Mirrors taskStructuredRowsForCSV but keeps values as-is.
func taskStructuredRecords(task *Task, result string) []map[string]any {
	if task != nil && task.Context != nil {
		if output, ok := task.Context["structured_output"]; ok {
			if records := structuredRecordsFromValue(output); len(records) > 0 {
				return records
			}
		}
	}

	var decoded any
	decoder := json.NewDecoder(strings.NewReader(strings.TrimSpace(result)))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return nil
	}
	return structuredRecordsFromValue(decoded)
}

// structuredRecordsFromValue normalizes a decoded JSON value into a slice of
// record objects. Arrays of objects map element-for-element; a wrapper object
// is unwrapped via a "data"/"rows"/"items" key when present; a plain object is
// a single record. Anything else yields nil.
func structuredRecordsFromValue(value any) []map[string]any {
	switch typed := value.(type) {
	case []any:
		records := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			object, ok := item.(map[string]any)
			if !ok {
				return nil
			}
			records = append(records, object)
		}
		return records
	case map[string]any:
		for _, key := range []string{"data", "rows", "items"} {
			if nested, ok := typed[key]; ok {
				if records := structuredRecordsFromValue(nested); len(records) > 0 {
					return records
				}
			}
		}
		return []map[string]any{typed}
	default:
		return nil
	}
}

// MarshalJSONLRecords renders records as newline-delimited JSON: one compact
// object per line, with a trailing newline after each.
func MarshalJSONLRecords(records []map[string]any) (string, error) {
	var buffer bytes.Buffer
	for _, record := range records {
		data, err := json.Marshal(record)
		if err != nil {
			return "", err
		}
		buffer.Write(data)
		buffer.WriteByte('\n')
	}
	return buffer.String(), nil
}

// AppendJSONLToFile appends one or more JSONL records to filePath, creating the
// file and parent directory as needed. No header is written — each line is a
// self-describing record — which is what makes the append path simpler than CSV.
func AppendJSONLToFile(filePath, content string) error {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return err
	}

	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	_, err = file.WriteString(trimmed + "\n")
	return err
}

// ExportCSVFromJSONL derives a CSV from JSONL content on demand. preferred lists
// the columns that should lead (e.g. the task's output-schema fields, in order);
// any remaining keys across the records are appended after them, sorted. Missing
// cells are written empty. This is how the spreadsheet-friendly CSV view is
// produced without maintaining a separate CSV-append contract.
func ExportCSVFromJSONL(jsonlContent string, preferred []string) (string, error) {
	rows := []map[string]string{}

	scanner := bufio.NewScanner(strings.NewReader(jsonlContent))
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var object map[string]any
		decoder := json.NewDecoder(strings.NewReader(line))
		decoder.UseNumber()
		if err := decoder.Decode(&object); err != nil {
			return "", fmt.Errorf("invalid JSONL on line %d: %w", lineNumber, err)
		}
		rows = append(rows, csvRowFromMap(object))
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", nil
	}

	return writeCSV(exportColumns(preferred, rows), rows), nil
}

// exportColumns orders CSV export columns: the preferred (schema) columns first,
// then any other keys present in the data, sorted, so data leads and run
// bookkeeping trails (the same data-first ordering the on-page preview uses).
func exportColumns(preferred []string, rows []map[string]string) []string {
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

	for _, column := range preferred {
		add(column)
	}

	remaining := []string{}
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
