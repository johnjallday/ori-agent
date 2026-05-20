package workspace

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// NormalizeTaskOrchestrationMode clamps parent orchestration mode to supported values.
func NormalizeTaskOrchestrationMode(value string) TaskOrchestrationMode {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(TaskOrchestrationModeGraph):
		return TaskOrchestrationModeGraph
	default:
		return TaskOrchestrationModeSequential
	}
}

// NormalizeTaskResultCombinationMode clamps parent aggregation mode to supported values.
func NormalizeTaskResultCombinationMode(value string) TaskResultCombinationMode {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(TaskResultCombinationConcat):
		return TaskResultCombinationConcat
	case string(TaskResultCombinationJSONMap):
		return TaskResultCombinationJSONMap
	case string(TaskResultCombinationStructuredOutput):
		return TaskResultCombinationStructuredOutput
	default:
		return TaskResultCombinationLastResult
	}
}

// NormalizeTaskOutputSchema sanitizes and normalizes a structured output schema.
func NormalizeTaskOutputSchema(schema *TaskOutputSchema) *TaskOutputSchema {
	if schema == nil {
		return nil
	}

	normalized := &TaskOutputSchema{
		Name:        strings.TrimSpace(schema.Name),
		Description: strings.TrimSpace(schema.Description),
		Strict:      schema.Strict,
		Fields:      make([]TaskOutputField, 0, len(schema.Fields)),
	}

	seen := make(map[string]struct{}, len(schema.Fields))
	for _, field := range schema.Fields {
		name := strings.TrimSpace(field.Name)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}

		fieldType := normalizeTaskOutputFieldType(field.Type)
		normalized.Fields = append(normalized.Fields, TaskOutputField{
			Name:        name,
			Type:        fieldType,
			Description: strings.TrimSpace(field.Description),
			Required:    field.Required,
		})
	}

	if len(normalized.Fields) == 0 {
		return nil
	}
	return normalized
}

func normalizeTaskOutputFieldType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "string", "number", "integer", "boolean", "object", "array":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "string"
	}
}

// BuildTaskOutputSchemaPrompt renders concise instructions for a structured task result.
func BuildTaskOutputSchemaPrompt(schema *TaskOutputSchema) string {
	normalized := NormalizeTaskOutputSchema(schema)
	if normalized == nil {
		return ""
	}

	var prompt strings.Builder
	prompt.WriteString("Return ONLY a valid JSON object")
	if normalized.Name != "" {
		prompt.WriteString(fmt.Sprintf(" for `%s`", normalized.Name))
	}
	prompt.WriteString(". Do not wrap it in markdown fences.\n")
	if normalized.Description != "" {
		prompt.WriteString(normalized.Description)
		prompt.WriteString("\n")
	}
	prompt.WriteString("Fields:\n")
	for _, field := range normalized.Fields {
		requiredLabel := "optional"
		if field.Required {
			requiredLabel = "required"
		}
		prompt.WriteString(fmt.Sprintf("- %s (%s, %s)", field.Name, field.Type, requiredLabel))
		if field.Description != "" {
			prompt.WriteString(": ")
			prompt.WriteString(field.Description)
		}
		prompt.WriteString("\n")
	}
	if normalized.Strict {
		prompt.WriteString("Do not include keys outside this schema.\n")
	}
	return strings.TrimSpace(prompt.String())
}

// ValidateTaskStructuredOutput validates a JSON result against a task output schema.
func ValidateTaskStructuredOutput(schema *TaskOutputSchema, result string) (map[string]any, error) {
	normalized := NormalizeTaskOutputSchema(schema)
	if normalized == nil {
		return nil, nil
	}

	var payload map[string]any
	decoder := json.NewDecoder(strings.NewReader(strings.TrimSpace(result)))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("result must be valid JSON object: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("result must contain only one JSON object")
	}
	if payload == nil {
		return nil, fmt.Errorf("result must be a JSON object")
	}

	fieldMap := make(map[string]TaskOutputField, len(normalized.Fields))
	for _, field := range normalized.Fields {
		fieldMap[field.Name] = field
		if field.Required {
			value, ok := payload[field.Name]
			if !ok {
				return nil, fmt.Errorf("result is missing required field %q", field.Name)
			}
			if err := validateTaskOutputFieldType(field, value); err != nil {
				return nil, err
			}
			continue
		}
		if value, ok := payload[field.Name]; ok {
			if err := validateTaskOutputFieldType(field, value); err != nil {
				return nil, err
			}
		}
	}

	if normalized.Strict {
		for key := range payload {
			if _, ok := fieldMap[key]; !ok {
				return nil, fmt.Errorf("result includes unexpected field %q", key)
			}
		}
	}

	return payload, nil
}

func validateTaskOutputFieldType(field TaskOutputField, value any) error {
	switch field.Type {
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("field %q must be a string", field.Name)
		}
	case "number":
		if !isJSONNumberLike(value) {
			return fmt.Errorf("field %q must be a number", field.Name)
		}
	case "integer":
		number, ok := value.(json.Number)
		if !ok {
			return fmt.Errorf("field %q must be an integer", field.Name)
		}
		if _, err := number.Int64(); err != nil {
			return fmt.Errorf("field %q must be an integer", field.Name)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("field %q must be a boolean", field.Name)
		}
	case "object":
		if _, ok := value.(map[string]any); !ok {
			return fmt.Errorf("field %q must be an object", field.Name)
		}
	case "array":
		if _, ok := value.([]any); !ok {
			return fmt.Errorf("field %q must be an array", field.Name)
		}
	}
	return nil
}

func isJSONNumberLike(value any) bool {
	switch value.(type) {
	case json.Number:
		return true
	default:
		return false
	}
}
