package workspacesurface

import (
	"encoding/json"
	"testing"
)

func TestOperationSchemaValidationRejectsUnknownNestedInputAndOutput(t *testing.T) {
	operation := Operation{
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{"items":{"type":"array","items":{"type":"string","maxLength":8},"maxItems":2}},
			"required":["items"],
			"additionalProperties":false
		}`),
		OutputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{"ok":{"type":"boolean"}},
			"required":["ok"],
			"additionalProperties":false
		}`),
	}
	if err := ValidateOperationInput(operation, json.RawMessage(`{"items":["one","two"]}`)); err != nil {
		t.Fatalf("valid input error = %v", err)
	}
	for _, input := range []string{
		`{"items":["one","two","three"]}`,
		`{"items":["one"],"workspace_id":"other"}`,
		`{"items":["value-that-is-too-long"]}`,
	} {
		if err := ValidateOperationInput(operation, json.RawMessage(input)); err == nil {
			t.Fatalf("invalid input accepted: %s", input)
		}
	}
	if err := ValidateOperationOutput(operation, json.RawMessage(`{"ok":true}`)); err != nil {
		t.Fatalf("valid output error = %v", err)
	}
	if err := ValidateOperationOutput(operation, json.RawMessage(`{"ok":true,"path":"/private/plugin"}`)); err == nil {
		t.Fatal("undeclared service output field was accepted")
	}
}
