package pluginapi

import (
	"testing"
)

func TestYAMLToolDefinition_ToToolDefinition(t *testing.T) {
	tests := []struct {
		name        string
		yaml        *YAMLToolDefinition
		expectError bool
		validate    func(t *testing.T, tool Tool)
	}{
		{
			name: "simple string parameter",
			yaml: &YAMLToolDefinition{
				Name:        "test-tool",
				Description: "A test tool",
				Parameters: map[string]YAMLToolParameter{
					"message": {
						Type:        "string",
						Description: "The message to send",
						Required:    true,
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, tool Tool) {
				if tool.Name != "test-tool" {
					t.Errorf("expected name 'test-tool', got '%s'", tool.Name)
				}
				if tool.Description != "A test tool" {
					t.Errorf("expected description 'A test tool', got '%s'", tool.Description)
				}

				params := tool.Parameters

				props, ok := params["properties"].(map[string]interface{})
				if !ok {
					t.Fatal("properties is not a map")
				}

				message, ok := props["message"].(map[string]interface{})
				if !ok {
					t.Fatal("message property not found")
				}

				if message["type"] != "string" {
					t.Errorf("expected type 'string', got '%v'", message["type"])
				}
			},
		},
		{
			name: "integer with min/max",
			yaml: &YAMLToolDefinition{
				Name:        "test-tool",
				Description: "A test tool",
				Parameters: map[string]YAMLToolParameter{
					"count": {
						Type:        "integer",
						Description: "Number of items",
						Required:    true,
						Min:         floatPtr(1),
						Max:         floatPtr(100),
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, tool Tool) {
				params := tool.Parameters
				props := params["properties"].(map[string]interface{})
				count := props["count"].(map[string]interface{})

				if count["type"] != "integer" {
					t.Errorf("expected type 'integer', got '%v'", count["type"])
				}
				if count["minimum"] != 1 {
					t.Errorf("expected minimum 1, got %v", count["minimum"])
				}
				if count["maximum"] != 100 {
					t.Errorf("expected maximum 100, got %v", count["maximum"])
				}
			},
		},
		{
			name: "enum parameter",
			yaml: &YAMLToolDefinition{
				Name:        "test-tool",
				Description: "A test tool",
				Parameters: map[string]YAMLToolParameter{
					"status": {
						Type:        "enum",
						Description: "Status value",
						Required:    true,
						Enum:        []string{"pending", "active", "done"},
						Default:     "pending",
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, tool Tool) {
				params := tool.Parameters
				props := params["properties"].(map[string]interface{})
				status := props["status"].(map[string]interface{})

				if status["type"] != "string" {
					t.Errorf("expected type 'string' for enum, got '%v'", status["type"])
				}

				enum, ok := status["enum"].([]string)
				if !ok {
					t.Fatal("enum is not a string slice")
				}

				if len(enum) != 3 {
					t.Errorf("expected 3 enum values, got %d", len(enum))
				}

				if status["default"] != "pending" {
					t.Errorf("expected default 'pending', got %v", status["default"])
				}
			},
		},
		{
			name: "array parameter",
			yaml: &YAMLToolDefinition{
				Name:        "test-tool",
				Description: "A test tool",
				Parameters: map[string]YAMLToolParameter{
					"tags": {
						Type:        "array",
						Description: "List of tags",
						Required:    false,
						Items: &struct {
							Type string `yaml:"type"`
						}{
							Type: "string",
						},
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, tool Tool) {
				params := tool.Parameters
				props := params["properties"].(map[string]interface{})
				tags := props["tags"].(map[string]interface{})

				if tags["type"] != "array" {
					t.Errorf("expected type 'array', got '%v'", tags["type"])
				}

				items, ok := tags["items"].(map[string]interface{})
				if !ok {
					t.Fatal("items is not a map")
				}

				if items["type"] != "string" {
					t.Errorf("expected items type 'string', got '%v'", items["type"])
				}
			},
		},
		{
			name: "nested object parameter",
			yaml: &YAMLToolDefinition{
				Name:        "test-tool",
				Description: "A test tool",
				Parameters: map[string]YAMLToolParameter{
					"config": {
						Type:        "object",
						Description: "Configuration object",
						Required:    true,
						Properties: map[string]YAMLToolParameter{
							"host": {
								Type:        "string",
								Description: "Server host",
								Required:    true,
							},
							"port": {
								Type:        "integer",
								Description: "Server port",
								Required:    true,
							},
						},
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, tool Tool) {
				params := tool.Parameters
				props := params["properties"].(map[string]interface{})
				config := props["config"].(map[string]interface{})

				if config["type"] != "object" {
					t.Errorf("expected type 'object', got '%v'", config["type"])
				}

				configProps, ok := config["properties"].(map[string]interface{})
				if !ok {
					t.Fatal("nested properties not found")
				}

				host, ok := configProps["host"].(map[string]interface{})
				if !ok {
					t.Fatal("host property not found")
				}

				if host["type"] != "string" {
					t.Errorf("expected host type 'string', got '%v'", host["type"])
				}
			},
		},
		{
			name:        "nil definition",
			yaml:        nil,
			expectError: true,
		},
		{
			name: "missing name",
			yaml: &YAMLToolDefinition{
				Description: "A test tool",
				Parameters: map[string]YAMLToolParameter{
					"param1": {Type: "string", Description: "Test"},
				},
			},
			expectError: true,
		},
		{
			name: "missing description",
			yaml: &YAMLToolDefinition{
				Name: "test-tool",
				Parameters: map[string]YAMLToolParameter{
					"param1": {Type: "string", Description: "Test"},
				},
			},
			expectError: true,
		},
		{
			name: "unsupported parameter type",
			yaml: &YAMLToolDefinition{
				Name:        "test-tool",
				Description: "A test tool",
				Parameters: map[string]YAMLToolParameter{
					"param1": {
						Type:        "unsupported",
						Description: "Test",
					},
				},
			},
			expectError: true,
		},
		{
			name: "enum without values",
			yaml: &YAMLToolDefinition{
				Name:        "test-tool",
				Description: "A test tool",
				Parameters: map[string]YAMLToolParameter{
					"status": {
						Type:        "enum",
						Description: "Status",
						Enum:        []string{},
					},
				},
			},
			expectError: true,
		},
		{
			name: "array without items",
			yaml: &YAMLToolDefinition{
				Name:        "test-tool",
				Description: "A test tool",
				Parameters: map[string]YAMLToolParameter{
					"tags": {
						Type:        "array",
						Description: "Tags",
					},
				},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool, err := tt.yaml.ToToolDefinition()

			if tt.expectError {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if tt.validate != nil {
				tt.validate(t, tool)
			}
		})
	}
}

func TestValidateYAMLToolDefinition(t *testing.T) {
	tests := []struct {
		name        string
		yaml        *YAMLToolDefinition
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid definition",
			yaml: &YAMLToolDefinition{
				Name:        "test-tool",
				Description: "A test tool",
				Parameters: map[string]YAMLToolParameter{
					"param1": {
						Type:        "string",
						Description: "Test parameter",
					},
				},
			},
			expectError: false,
		},
		{
			name:        "nil definition",
			yaml:        nil,
			expectError: true,
			errorMsg:    "cannot be nil",
		},
		{
			name: "missing name",
			yaml: &YAMLToolDefinition{
				Description: "A test tool",
				Parameters: map[string]YAMLToolParameter{
					"param1": {Type: "string", Description: "Test"},
				},
			},
			expectError: true,
			errorMsg:    "name is required",
		},
		{
			name: "name too long",
			yaml: &YAMLToolDefinition{
				Name:        "this-is-a-very-long-tool-name-that-exceeds-the-maximum-length-limit-of-sixty-four-characters",
				Description: "A test tool",
				Parameters: map[string]YAMLToolParameter{
					"param1": {Type: "string", Description: "Test"},
				},
			},
			expectError: true,
			errorMsg:    "64 characters or less",
		},
		{
			name: "missing description",
			yaml: &YAMLToolDefinition{
				Name: "test-tool",
				Parameters: map[string]YAMLToolParameter{
					"param1": {Type: "string", Description: "Test"},
				},
			},
			expectError: true,
			errorMsg:    "description is required",
		},
		{
			name: "no parameters",
			yaml: &YAMLToolDefinition{
				Name:        "test-tool",
				Description: "A test tool",
				Parameters:  map[string]YAMLToolParameter{},
			},
			expectError: true,
			errorMsg:    "at least one parameter",
		},
		{
			name: "parameter missing description",
			yaml: &YAMLToolDefinition{
				Name:        "test-tool",
				Description: "A test tool",
				Parameters: map[string]YAMLToolParameter{
					"param1": {
						Type: "string",
					},
				},
			},
			expectError: true,
			errorMsg:    "description is required",
		},
		{
			name: "invalid parameter type",
			yaml: &YAMLToolDefinition{
				Name:        "test-tool",
				Description: "A test tool",
				Parameters: map[string]YAMLToolParameter{
					"param1": {
						Type:        "invalid",
						Description: "Test",
					},
				},
			},
			expectError: true,
			errorMsg:    "invalid type",
		},
		{
			name: "enum without values",
			yaml: &YAMLToolDefinition{
				Name:        "test-tool",
				Description: "A test tool",
				Parameters: map[string]YAMLToolParameter{
					"status": {
						Type:        "enum",
						Description: "Status",
						Enum:        []string{},
					},
				},
			},
			expectError: true,
			errorMsg:    "enum type requires",
		},
		{
			name: "array without items",
			yaml: &YAMLToolDefinition{
				Name:        "test-tool",
				Description: "A test tool",
				Parameters: map[string]YAMLToolParameter{
					"tags": {
						Type:        "array",
						Description: "Tags",
					},
				},
			},
			expectError: true,
			errorMsg:    "array type requires",
		},
		{
			name: "min greater than max",
			yaml: &YAMLToolDefinition{
				Name:        "test-tool",
				Description: "A test tool",
				Parameters: map[string]YAMLToolParameter{
					"count": {
						Type:        "integer",
						Description: "Count",
						Min:         floatPtr(100),
						Max:         floatPtr(10),
					},
				},
			},
			expectError: true,
			errorMsg:    "min",
		},
		{
			name: "enum default not in values",
			yaml: &YAMLToolDefinition{
				Name:        "test-tool",
				Description: "A test tool",
				Parameters: map[string]YAMLToolParameter{
					"status": {
						Type:        "enum",
						Description: "Status",
						Enum:        []string{"active", "done"},
						Default:     "pending", // Not in enum
					},
				},
			},
			expectError: true,
			errorMsg:    "not in enum values",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateYAMLToolDefinition(tt.yaml)

			if tt.expectError {
				if err == nil {
					t.Error("expected error, got nil")
					return
				}
				if tt.errorMsg != "" {
					if !contains(err.Error(), tt.errorMsg) {
						t.Errorf("expected error containing %q, got %q", tt.errorMsg, err.Error())
					}
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestBasePlugin_GetToolDefinition(t *testing.T) {
	t.Run("no plugin config", func(t *testing.T) {
		bp := NewBasePlugin("test", "1.0.0", "", "", "v1")
		_, err := bp.GetToolDefinition()
		if err == nil {
			t.Error("expected error when plugin config not set")
		}
	})

	t.Run("no tool definition in config", func(t *testing.T) {
		bp := NewBasePlugin("test", "1.0.0", "", "", "v1")
		bp.SetPluginConfig(&PluginConfig{
			Name:        "test-plugin",
			Version:     "1.0.0",
			Description: "Test",
			License:     "MIT",
			Repository:  "https://example.com",
			Tool:        nil, // No tool definition
		})

		_, err := bp.GetToolDefinition()
		if err == nil {
			t.Error("expected error when no tool definition in config")
		}
	})

	t.Run("valid tool definition", func(t *testing.T) {
		bp := NewBasePlugin("test", "1.0.0", "", "", "v1")
		bp.SetPluginConfig(&PluginConfig{
			Name:        "test-plugin",
			Version:     "1.0.0",
			Description: "Test",
			License:     "MIT",
			Repository:  "https://example.com",
			Tool: &YAMLToolDefinition{
				Name:        "test-tool",
				Description: "A test tool",
				Parameters: map[string]YAMLToolParameter{
					"message": {
						Type:        "string",
						Description: "The message",
						Required:    true,
					},
				},
			},
		})

		tool, err := bp.GetToolDefinition()
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		if tool.Name != "test-tool" {
			t.Errorf("expected name 'test-tool', got '%s'", tool.Name)
		}
	})
}

// Helper functions

func floatPtr(f float64) *float64 {
	return &f
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && stringContains(s, substr))
}

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
