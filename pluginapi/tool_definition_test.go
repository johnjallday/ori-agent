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
	if _, ok := params["oneOf"]; !ok {
		t.Fatalf("expected conditional schema oneOf")
	}

	err = ValidateToolParameters(params, map[string]interface{}{"operation": "echo"})
	if err == nil {
		t.Fatalf("expected error for missing message")
	}

	err = ValidateToolParameters(params, map[string]interface{}{"operation": "echo", "message": "hi"})
	if err != nil {
		t.Fatalf("unexpected error for echo: %v", err)
	}

	err = ValidateToolParameters(params, map[string]interface{}{"operation": "status"})
	if err != nil {
		t.Fatalf("unexpected error for status: %v", err)
	}

	err = ValidateToolParameters(params, map[string]interface{}{"operation": "unknown"})
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
