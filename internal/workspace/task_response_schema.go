package workspace

import "strings"

// deriveTaskResponseSchema builds a JSON Schema object (as a decoded map) for a
// task's active output spec, for runtime-constrained decoding on providers that
// support it (WS3). Returns nil when the task has no usable schema, so callers
// leave the request unconstrained.
func deriveTaskResponseSchema(task Task) map[string]any {
	spec := ActiveTaskOutputSpec(&task)
	if spec == nil {
		return nil
	}
	if schema := schemaFromOutputSchema(spec.Schema); schema != nil {
		return schema
	}
	return schemaFromContract(spec.Contract)
}

// schemaFromOutputSchema builds an object schema from the task's field-based
// output schema.
func schemaFromOutputSchema(s *TaskOutputSchema) map[string]any {
	if s == nil || len(s.Fields) == 0 {
		return nil
	}
	properties := make(map[string]any, len(s.Fields))
	var required []string
	for _, f := range s.Fields {
		name := strings.TrimSpace(f.Name)
		if name == "" {
			continue
		}
		prop := map[string]any{"type": jsonSchemaType(f.Type)}
		if desc := strings.TrimSpace(f.Description); desc != "" {
			prop["description"] = desc
		}
		properties[name] = prop
		if f.Required {
			required = append(required, name)
		}
	}
	return objectSchema(properties, required)
}

// schemaFromContract builds an object schema from the task's CSV-oriented output
// contract columns.
func schemaFromContract(c *TaskOutputContract) map[string]any {
	if c == nil || len(c.Columns) == 0 {
		return nil
	}
	properties := make(map[string]any, len(c.Columns))
	var required []string
	for _, col := range c.Columns {
		name := strings.TrimSpace(col.Name)
		if name == "" {
			continue
		}
		prop := map[string]any{"type": jsonSchemaType(col.Type)}
		if desc := strings.TrimSpace(col.Description); desc != "" {
			prop["description"] = desc
		}
		properties[name] = prop
		if col.Required {
			required = append(required, name)
		}
	}
	return objectSchema(properties, required)
}

func objectSchema(properties map[string]any, required []string) map[string]any {
	if len(properties) == 0 {
		return nil
	}
	obj := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		obj["required"] = required
	}
	return obj
}

// jsonSchemaType maps an output-spec field/column type to a JSON Schema type,
// defaulting to "string" for unknown or date types (dates serialize as strings).
func jsonSchemaType(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "number", "float", "double":
		return "number"
	case "integer", "int":
		return "integer"
	case "boolean", "bool":
		return "boolean"
	case "object":
		return "object"
	case "array", "list":
		return "array"
	default:
		return "string"
	}
}
