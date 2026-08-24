package workspacesurface

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"strings"
	"unicode/utf8"
)

const MaxOperationInputBytes = 64 << 10

var ErrInputInvalid = errors.New("workspace surface operation input is invalid")

type prototypeSchema struct {
	Schema               string                     `json:"$schema,omitempty"`
	Type                 json.RawMessage            `json:"type"`
	Properties           map[string]prototypeSchema `json:"properties,omitempty"`
	Required             []string                   `json:"required,omitempty"`
	AdditionalProperties *bool                      `json:"additionalProperties,omitempty"`
	Items                *prototypeSchema           `json:"items,omitempty"`
	Enum                 []any                      `json:"enum,omitempty"`
	Const                any                        `json:"const,omitempty"`
	MinLength            *int                       `json:"minLength,omitempty"`
	MaxLength            *int                       `json:"maxLength,omitempty"`
	Minimum              *float64                   `json:"minimum,omitempty"`
	Maximum              *float64                   `json:"maximum,omitempty"`
	MinItems             *int                       `json:"minItems,omitempty"`
	MaxItems             *int                       `json:"maxItems,omitempty"`
	Description          string                     `json:"description,omitempty"`
}

// ValidateOperationInput applies the frozen closed JSON schema after the byte
// gate and before a Runtime receives browser data.
func ValidateOperationInput(operation Operation, input json.RawMessage) error {
	if len(input) == 0 || len(input) > MaxOperationInputBytes || !utf8.Valid(input) {
		return ErrInputInvalid
	}
	return validateOperationValue(operation.InputSchema, input, ErrInputInvalid)
}

// ValidateOperationOutput treats native service output as untrusted too. The
// caller applies the operation's byte limit before this schema check.
func ValidateOperationOutput(operation Operation, output json.RawMessage) error {
	if len(output) == 0 || !utf8.Valid(output) {
		return fmt.Errorf("workspace surface operation output is invalid")
	}
	return validateOperationValue(operation.OutputSchema, output, fmt.Errorf("workspace surface operation output is invalid"))
}

func validateOperationValue(schemaJSON, valueJSON json.RawMessage, invalid error) error {
	var schema prototypeSchema
	decoder := json.NewDecoder(bytes.NewReader(schemaJSON))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(&schema); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return invalid
	}
	decoder = json.NewDecoder(bytes.NewReader(valueJSON))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return invalid
	}
	if err := validateSchemaValue(schema, value, 0); err != nil {
		return invalid
	}
	return nil
}

func validateSchemaValue(schema prototypeSchema, value any, depth int) error {
	if depth > 16 {
		return ErrInputInvalid
	}
	types, err := valueSchemaTypes(schema.Type)
	if err != nil {
		return err
	}
	if value == nil {
		if containsType(types, "null") {
			return validateEnumConst(schema, value)
		}
		return ErrInputInvalid
	}
	primary := ""
	for _, candidate := range types {
		if candidate != "null" {
			primary = candidate
		}
	}
	switch primary {
	case "object":
		object, ok := value.(map[string]any)
		if !ok || schema.AdditionalProperties == nil || *schema.AdditionalProperties || len(object) > 256 {
			return ErrInputInvalid
		}
		for key, child := range object {
			property, declared := schema.Properties[key]
			if !declared || validateSchemaValue(property, child, depth+1) != nil {
				return ErrInputInvalid
			}
		}
		for _, required := range schema.Required {
			if _, present := object[required]; !present {
				return ErrInputInvalid
			}
		}
	case "array":
		array, ok := value.([]any)
		if !ok || schema.Items == nil || schema.MaxItems == nil || len(array) > *schema.MaxItems || len(array) > 256 {
			return ErrInputInvalid
		}
		if schema.MinItems != nil && len(array) < *schema.MinItems {
			return ErrInputInvalid
		}
		for _, child := range array {
			if validateSchemaValue(*schema.Items, child, depth+1) != nil {
				return ErrInputInvalid
			}
		}
	case "string":
		text, ok := value.(string)
		if !ok || !utf8.ValidString(text) || schema.MaxLength == nil {
			return ErrInputInvalid
		}
		length := utf8.RuneCountInString(text)
		if length > *schema.MaxLength || (schema.MinLength != nil && length < *schema.MinLength) {
			return ErrInputInvalid
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return ErrInputInvalid
		}
	case "integer", "number":
		number, ok := value.(json.Number)
		if !ok {
			return ErrInputInvalid
		}
		parsed, err := number.Float64()
		if err != nil || math.IsInf(parsed, 0) || math.IsNaN(parsed) {
			return ErrInputInvalid
		}
		if primary == "integer" && strings.ContainsAny(number.String(), ".eE") {
			return ErrInputInvalid
		}
		if schema.Minimum != nil && parsed < *schema.Minimum {
			return ErrInputInvalid
		}
		if schema.Maximum != nil && parsed > *schema.Maximum {
			return ErrInputInvalid
		}
	default:
		return ErrInputInvalid
	}
	return validateEnumConst(schema, value)
}

func validateEnumConst(schema prototypeSchema, value any) error {
	if len(schema.Enum) > 0 {
		found := false
		for _, candidate := range schema.Enum {
			if reflect.DeepEqual(candidate, value) {
				found = true
				break
			}
		}
		if !found {
			return ErrInputInvalid
		}
	}
	if schema.Const != nil && !reflect.DeepEqual(schema.Const, value) {
		return ErrInputInvalid
	}
	return nil
}

func valueSchemaTypes(raw json.RawMessage) ([]string, error) {
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return []string{single}, nil
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err != nil || len(list) != 2 {
		return nil, ErrInputInvalid
	}
	return list, nil
}

func containsType(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
