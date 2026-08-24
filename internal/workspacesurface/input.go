package workspacesurface

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"unicode/utf8"
)

const MaxOperationInputBytes = 64 << 10

var ErrInputInvalid = errors.New("workspace surface operation input is invalid")

type prototypeSchema struct {
	Type                 string                     `json:"type"`
	Properties           map[string]prototypeSchema `json:"properties"`
	Required             []string                   `json:"required"`
	AdditionalProperties *bool                      `json:"additionalProperties"`
	MinLength            *int                       `json:"minLength"`
	MaxLength            *int                       `json:"maxLength"`
	Minimum              *float64                   `json:"minimum"`
	Maximum              *float64                   `json:"maximum"`
}

// ValidateOperationInput is the prototype's closed-object value gate. Group 2
// promotes the full documented v1 schema subset; this slice already guarantees
// that unknown browser fields (including workspace/path/confirmed overrides),
// missing required fields, primitive type mismatches, and declared scalar
// bounds never reach a Runtime.
func ValidateOperationInput(operation Operation, input json.RawMessage) error {
	if len(input) == 0 || len(input) > MaxOperationInputBytes || !utf8.Valid(input) {
		return ErrInputInvalid
	}
	var schema prototypeSchema
	decoder := json.NewDecoder(bytes.NewReader(operation.InputSchema))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&schema); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return fmt.Errorf("%w: installed input schema is invalid", ErrInputInvalid)
	}
	if schema.Type != "object" || schema.AdditionalProperties == nil || *schema.AdditionalProperties {
		return fmt.Errorf("%w: installed input schema is not closed", ErrInputInvalid)
	}

	decoder = json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil || decoder.Decode(&struct{}{}) != io.EOF || value == nil {
		return ErrInputInvalid
	}
	for key := range value {
		property, declared := schema.Properties[key]
		if !declared || validatePrototypeValue(property, value[key]) != nil {
			return ErrInputInvalid
		}
	}
	for _, required := range schema.Required {
		if _, present := value[required]; !present {
			return ErrInputInvalid
		}
	}
	return nil
}

func validatePrototypeValue(schema prototypeSchema, value any) error {
	switch schema.Type {
	case "string":
		text, ok := value.(string)
		if !ok || !utf8.ValidString(text) {
			return ErrInputInvalid
		}
		length := utf8.RuneCountInString(text)
		if schema.MinLength != nil && length < *schema.MinLength {
			return ErrInputInvalid
		}
		if schema.MaxLength == nil || length > *schema.MaxLength {
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
		if schema.Type == "integer" && strings.ContainsAny(number.String(), ".eE") {
			return ErrInputInvalid
		}
		if schema.Minimum != nil && parsed < *schema.Minimum {
			return ErrInputInvalid
		}
		if schema.Maximum != nil && parsed > *schema.Maximum {
			return ErrInputInvalid
		}
	case "null":
		if value != nil {
			return ErrInputInvalid
		}
	default:
		return ErrInputInvalid
	}
	return nil
}
