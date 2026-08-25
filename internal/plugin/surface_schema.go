package plugin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

type operationSchemaNode struct {
	Schema               string                     `json:"$schema,omitempty"`
	Type                 json.RawMessage            `json:"type"`
	Properties           map[string]json.RawMessage `json:"properties,omitempty"`
	Required             []string                   `json:"required,omitempty"`
	AdditionalProperties *bool                      `json:"additionalProperties,omitempty"`
	Items                json.RawMessage            `json:"items,omitempty"`
	Enum                 []json.RawMessage          `json:"enum,omitempty"`
	Const                json.RawMessage            `json:"const,omitempty"`
	MinLength            *int                       `json:"minLength,omitempty"`
	MaxLength            *int                       `json:"maxLength,omitempty"`
	Minimum              *float64                   `json:"minimum,omitempty"`
	Maximum              *float64                   `json:"maximum,omitempty"`
	MinItems             *int                       `json:"minItems,omitempty"`
	MaxItems             *int                       `json:"maxItems,omitempty"`
	Description          string                     `json:"description,omitempty"`
}

func validateOperationSchema(raw json.RawMessage) error {
	return validateOperationSchemaAt(raw, 0)
}

func validateOperationSchemaAt(raw json.RawMessage, depth int) error {
	if depth > 16 || len(raw) == 0 || len(raw) > 64<<10 {
		return fmt.Errorf("schema size or depth exceeds v1 limits")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var node operationSchemaNode
	if err := decoder.Decode(&node); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return fmt.Errorf("schema contains an unknown field or invalid JSON")
	}
	types, err := schemaTypes(node.Type)
	if err != nil {
		return err
	}
	primary := ""
	for _, candidate := range types {
		if candidate != "null" {
			primary = candidate
		}
	}
	if primary == "" {
		primary = "null"
	}

	switch primary {
	case "object":
		if node.AdditionalProperties == nil || *node.AdditionalProperties {
			return fmt.Errorf("object schema must be closed")
		}
		if len(node.Properties) > 256 {
			return fmt.Errorf("object schema has too many properties")
		}
		for name, child := range node.Properties {
			if name == "" || len(name) > 64 {
				return fmt.Errorf("object property name is invalid")
			}
			if err := validateOperationSchemaAt(child, depth+1); err != nil {
				return fmt.Errorf("property %q: %w", name, err)
			}
		}
		seen := make(map[string]struct{}, len(node.Required))
		for _, required := range node.Required {
			if _, exists := node.Properties[required]; !exists {
				return fmt.Errorf("required property %q is not declared", required)
			}
			if _, duplicate := seen[required]; duplicate {
				return fmt.Errorf("required property %q is duplicated", required)
			}
			seen[required] = struct{}{}
		}
	case "array":
		if len(node.Items) == 0 || node.MaxItems == nil || *node.MaxItems < 0 || *node.MaxItems > 256 {
			return fmt.Errorf("array schema must have one bounded items schema")
		}
		if node.MinItems != nil && (*node.MinItems < 0 || *node.MinItems > *node.MaxItems) {
			return fmt.Errorf("array item bounds are invalid")
		}
		if err := validateOperationSchemaAt(node.Items, depth+1); err != nil {
			return fmt.Errorf("array items: %w", err)
		}
	case "string":
		if node.MaxLength == nil || *node.MaxLength < 0 || *node.MaxLength > 32768 {
			return fmt.Errorf("string schema must declare a bounded maxLength")
		}
		if node.MinLength != nil && (*node.MinLength < 0 || *node.MinLength > *node.MaxLength) {
			return fmt.Errorf("string bounds are invalid")
		}
	case "integer", "number":
		if node.Minimum != nil && node.Maximum != nil && *node.Minimum > *node.Maximum {
			return fmt.Errorf("numeric bounds are invalid")
		}
	case "boolean", "null":
	default:
		return fmt.Errorf("schema type %q is unsupported", primary)
	}
	if len(node.Enum) > 256 {
		return fmt.Errorf("schema enum is too large")
	}
	for _, value := range node.Enum {
		if !json.Valid(value) {
			return fmt.Errorf("schema enum contains invalid JSON")
		}
	}
	if len(node.Const) > 0 && !json.Valid(node.Const) {
		return fmt.Errorf("schema const contains invalid JSON")
	}
	if len(node.Description) > 500 {
		return fmt.Errorf("schema description is too long")
	}
	return nil
}

func schemaTypes(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("schema type is required")
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		if !knownSchemaType(single) {
			return nil, fmt.Errorf("schema type %q is unsupported", single)
		}
		return []string{single}, nil
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err != nil || len(list) != 2 {
		return nil, fmt.Errorf("schema type must be one type or one nullable type")
	}
	if list[0] == list[1] || (list[0] != "null" && list[1] != "null") || !knownSchemaType(list[0]) || !knownSchemaType(list[1]) {
		return nil, fmt.Errorf("schema nullable type is invalid")
	}
	return list, nil
}

func knownSchemaType(value string) bool {
	switch value {
	case "object", "array", "string", "integer", "number", "boolean", "null":
		return true
	default:
		return false
	}
}
