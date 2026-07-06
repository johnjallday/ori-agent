package workspace

import (
	"encoding/json"
	"testing"
)

func TestDeriveTaskResponseSchema_FromOutputSchema(t *testing.T) {
	task := Task{
		OutputSchema: &TaskOutputSchema{
			Fields: []TaskOutputField{
				{Name: "title", Type: "string", Required: true},
				{Name: "count", Type: "integer"},
				{Name: "done", Type: "boolean", Required: true},
			},
		},
	}
	schema := deriveTaskResponseSchema(task)
	if schema == nil {
		t.Fatal("expected a schema")
	}
	if schema["type"] != "object" {
		t.Fatalf("type = %v, want object", schema["type"])
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok || len(props) != 3 {
		t.Fatalf("properties = %v", schema["properties"])
	}
	if props["count"].(map[string]any)["type"] != "integer" {
		t.Fatalf("count type not mapped: %v", props["count"])
	}
	if props["done"].(map[string]any)["type"] != "boolean" {
		t.Fatalf("bool should map to boolean: %v", props["done"])
	}
	required, _ := schema["required"].([]string)
	if len(required) != 2 {
		t.Fatalf("required = %v, want title+done", required)
	}
	// Must marshal cleanly (it is handed to providers as JSON).
	if _, err := json.Marshal(schema); err != nil {
		t.Fatalf("schema not marshalable: %v", err)
	}
}

func TestDeriveTaskResponseSchema_FromContract(t *testing.T) {
	task := Task{
		OutputContract: &TaskOutputContract{
			Columns: []TaskOutputContractColumn{
				{Name: "name", Type: "string", Required: true},
				{Name: "price", Type: "number"},
				{Name: "when", Type: "date"},
			},
		},
	}
	schema := deriveTaskResponseSchema(task)
	if schema == nil {
		t.Fatal("expected a schema from contract")
	}
	props := schema["properties"].(map[string]any)
	if props["price"].(map[string]any)["type"] != "number" {
		t.Fatalf("price type not mapped: %v", props["price"])
	}
	// date maps to string.
	if props["when"].(map[string]any)["type"] != "string" {
		t.Fatalf("date should map to string: %v", props["when"])
	}
}

func TestDeriveTaskResponseSchema_None(t *testing.T) {
	if got := deriveTaskResponseSchema(Task{}); got != nil {
		t.Fatalf("expected nil schema for spec-less task, got %v", got)
	}
	// Empty schema (no fields) yields nil, not an empty object.
	if got := deriveTaskResponseSchema(Task{OutputSchema: &TaskOutputSchema{}}); got != nil {
		t.Fatalf("expected nil for empty schema, got %v", got)
	}
}
