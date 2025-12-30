package pluginapi

import "testing"

func TestConditionalToolSchemaValidation(t *testing.T) {
	toolDef := &YAMLToolDefinition{
		Name:        "multi-op",
		Description: "test",
		Parameters: []YAMLToolParameter{
			{
				Name:        "operation",
				Type:        "string",
				Description: "operation",
				Required:    true,
				Enum:        []string{"echo", "status"},
			},
		},
		Operations: map[string]YAMLOperationDefinition{
			"echo": {
				Parameters: []YAMLToolParameter{
					{
						Name:        "message",
						Type:        "string",
						Description: "message",
						Required:    true,
					},
				},
			},
			"status": {
				Parameters: []YAMLToolParameter{},
			},
		},
	}

	tool, err := toolDef.ToToolDefinition()
	if err != nil {
		t.Fatalf("ToToolDefinition failed: %v", err)
	}

	params := tool.Parameters
	if params == nil {
		t.Fatalf("expected parameters schema")
	}

	// Schema should NOT have oneOf (for OpenAI compatibility)
	if _, ok := params["oneOf"]; ok {
		t.Fatalf("schema should not have oneOf for OpenAI compatibility")
	}

	// Schema should have all properties as a flat union
	props, ok := params["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected properties in schema")
	}
	if _, ok := props["operation"]; !ok {
		t.Fatalf("expected operation property")
	}
	if _, ok := props["message"]; !ok {
		t.Fatalf("expected message property (from echo operation)")
	}

	// Use ValidateToolParametersWithOperations for operation-specific validation
	err = ValidateToolParametersWithOperations(toolDef, map[string]interface{}{"operation": "echo"})
	if err == nil {
		t.Fatalf("expected error for missing message")
	}

	err = ValidateToolParametersWithOperations(toolDef, map[string]interface{}{"operation": "echo", "message": "hi"})
	if err != nil {
		t.Fatalf("unexpected error for echo: %v", err)
	}

	err = ValidateToolParametersWithOperations(toolDef, map[string]interface{}{"operation": "status"})
	if err != nil {
		t.Fatalf("unexpected error for status: %v", err)
	}

	err = ValidateToolParametersWithOperations(toolDef, map[string]interface{}{"operation": "unknown"})
	if err == nil {
		t.Fatalf("expected error for unknown operation")
	}
}

func TestConditionalToolDefinitionValidation(t *testing.T) {
	toolDef := &YAMLToolDefinition{
		Name:        "invalid",
		Description: "test",
		Parameters: []YAMLToolParameter{
			{
				Name:        "operation",
				Type:        "string",
				Description: "operation",
				Required:    true,
				Enum:        []string{"echo"},
			},
		},
		Operations: map[string]YAMLOperationDefinition{
			"echo":   {Parameters: []YAMLToolParameter{}},
			"status": {Parameters: []YAMLToolParameter{}},
		},
	}

	if err := ValidateYAMLToolDefinition(toolDef); err == nil {
		t.Fatalf("expected validation error for missing enum value")
	}
}

func TestAutoDerivesEnumFromOperations(t *testing.T) {
	// Test that enum is auto-derived from operations keys when not explicitly provided
	toolDef := &YAMLToolDefinition{
		Name:        "auto-enum",
		Description: "test auto-derived enum",
		Parameters: []YAMLToolParameter{
			{
				Name:        "operation",
				Type:        "string",
				Description: "operation to perform",
				Required:    true,
				// No enum specified - should be auto-derived from operations keys
			},
		},
		Operations: map[string]YAMLOperationDefinition{
			"create": {
				Parameters: []YAMLToolParameter{
					{Name: "name", Type: "string", Description: "name", Required: true},
				},
			},
			"list": {
				Parameters: []YAMLToolParameter{},
			},
			"delete": {
				Parameters: []YAMLToolParameter{
					{Name: "id", Type: "string", Description: "id", Required: true},
				},
			},
		},
	}

	// Validation should pass without explicit enum
	if err := ValidateYAMLToolDefinition(toolDef); err != nil {
		t.Fatalf("validation should pass without explicit enum: %v", err)
	}

	// ToToolDefinition should derive enum from operations keys
	tool, err := toolDef.ToToolDefinition()
	if err != nil {
		t.Fatalf("ToToolDefinition failed: %v", err)
	}

	// Check that properties contains operation with enum
	props, ok := tool.Parameters["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected properties in schema")
	}
	opProp, ok := props["operation"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected operation property in schema")
	}
	enumValues, ok := opProp["enum"].([]string)
	if !ok {
		t.Fatalf("expected enum in operation property, got %T", opProp["enum"])
	}

	// Should have all 3 operations (sorted alphabetically)
	if len(enumValues) != 3 {
		t.Fatalf("expected 3 enum values, got %d: %v", len(enumValues), enumValues)
	}

	// Check that all operations are present
	expected := map[string]bool{"create": true, "list": true, "delete": true}
	for _, v := range enumValues {
		if !expected[v] {
			t.Fatalf("unexpected enum value: %s", v)
		}
		delete(expected, v)
	}
	if len(expected) > 0 {
		t.Fatalf("missing enum values: %v", expected)
	}

	// Use ValidateToolParametersWithOperations for operation-specific validation
	err = ValidateToolParametersWithOperations(toolDef, map[string]interface{}{"operation": "create", "name": "test"})
	if err != nil {
		t.Fatalf("unexpected error for create: %v", err)
	}

	err = ValidateToolParametersWithOperations(toolDef, map[string]interface{}{"operation": "list"})
	if err != nil {
		t.Fatalf("unexpected error for list: %v", err)
	}

	err = ValidateToolParametersWithOperations(toolDef, map[string]interface{}{"operation": "unknown"})
	if err == nil {
		t.Fatalf("expected error for unknown operation")
	}
}
