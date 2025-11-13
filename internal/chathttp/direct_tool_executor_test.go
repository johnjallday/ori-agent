package chathttp

import (
	"context"
	"fmt"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/pluginapi"
)

// mockTool is a mock implementation of pluginapi.Tool for testing
type mockTool struct {
	name        string
	description string
	callFunc    func(ctx context.Context, args string) (string, error)
}

func (m *mockTool) Definition() pluginapi.Tool {
	return pluginapi.Tool{
		Name:        m.name,
		Description: m.description,
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	}
}

func (m *mockTool) Call(ctx context.Context, args string) (string, error) {
	if m.callFunc != nil {
		return m.callFunc(ctx, args)
	}
	return "mock result", nil
}

// TestParseDirectToolCommand tests the parsing of direct tool commands
func TestParseDirectToolCommand(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantToolName string
		wantArgs     string
		wantErr      bool
	}{
		{
			name:         "valid command with simple args",
			input:        `/tool math {"operation": "add", "a": 5, "b": 3}`,
			wantToolName: "math",
			wantArgs:     `{"operation": "add", "a": 5, "b": 3}`,
			wantErr:      false,
		},
		{
			name:         "valid command with complex args",
			input:        `/tool weather {"city": "San Francisco", "units": "metric", "lang": "en"}`,
			wantToolName: "weather",
			wantArgs:     `{"city": "San Francisco", "units": "metric", "lang": "en"}`,
			wantErr:      false,
		},
		{
			name:         "valid command with nested JSON",
			input:        `/tool complex {"data": {"nested": true, "value": 42}}`,
			wantToolName: "complex",
			wantArgs:     `{"data": {"nested": true, "value": 42}}`,
			wantErr:      false,
		},
		{
			name:    "missing /tool prefix",
			input:   `math {"operation": "add"}`,
			wantErr: true,
		},
		{
			name:    "missing tool name",
			input:   `/tool {"operation": "add"}`,
			wantErr: true,
		},
		{
			name:    "missing JSON args",
			input:   `/tool math`,
			wantErr: true,
		},
		{
			name:    "invalid JSON args",
			input:   `/tool math {invalid json}`,
			wantErr: true,
		},
		{
			name:    "empty command",
			input:   `/tool `,
			wantErr: true,
		},
		{
			name:         "valid command with extra whitespace",
			input:        `/tool   math   {"operation": "add"}`,
			wantToolName: "math",
			wantArgs:     `{"operation": "add"}`,
			wantErr:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, err := parseDirectToolCommand(tt.input)

			if tt.wantErr {
				if err == nil {
					t.Errorf("parseDirectToolCommand() expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("parseDirectToolCommand() unexpected error: %v", err)
				return
			}

			if cmd.ToolName != tt.wantToolName {
				t.Errorf("parseDirectToolCommand() toolName = %v, want %v", cmd.ToolName, tt.wantToolName)
			}

			if cmd.Args != tt.wantArgs {
				t.Errorf("parseDirectToolCommand() args = %v, want %v", cmd.Args, tt.wantArgs)
			}
		})
	}
}

// TestExecuteDirectTool tests the execution of direct tool commands
func TestExecuteDirectTool(t *testing.T) {
	tests := []struct {
		name               string
		toolName           string
		args               string
		mockResult         string
		mockErr            error
		wantSuccess        bool
		wantResultContains string
	}{
		{
			name:               "successful tool execution",
			toolName:           "math",
			args:               `{"operation": "add", "a": 5, "b": 3}`,
			mockResult:         "8",
			mockErr:            nil,
			wantSuccess:        true,
			wantResultContains: "8",
		},
		{
			name:               "tool execution with error",
			toolName:           "math",
			args:               `{"operation": "divide", "a": 5, "b": 0}`,
			mockResult:         "",
			mockErr:            fmt.Errorf("division by zero"),
			wantSuccess:        false,
			wantResultContains: "division by zero",
		},
		{
			name:               "tool not found",
			toolName:           "nonexistent",
			args:               `{"param": "value"}`,
			wantSuccess:        false,
			wantResultContains: "not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock tool
			mock := &mockTool{
				name:        "math",
				description: "Math operations",
				callFunc: func(ctx context.Context, args string) (string, error) {
					return tt.mockResult, tt.mockErr
				},
			}

			// Create test agent with mock tool
			ag := &agent.Agent{
				Plugins: map[string]types.LoadedPlugin{
					"math": {
						Tool: mock,
						Definition: pluginapi.Tool{
							Name:        "math",
							Description: "Math operations",
							Parameters: map[string]interface{}{
								"type":       "object",
								"properties": map[string]interface{}{},
							},
						},
					},
				},
			}

			// Create handler with nil dependencies (not needed for this test)
			h := &Handler{}

			// Create command
			cmd := &DirectToolCommand{
				ToolName: tt.toolName,
				Args:     tt.args,
			}

			// Execute
			result := h.executeDirectTool(context.Background(), ag, cmd)

			// Verify success/failure
			if result.Success != tt.wantSuccess {
				t.Errorf("executeDirectTool() success = %v, want %v", result.Success, tt.wantSuccess)
			}

			// Verify result contains expected string
			if tt.wantResultContains != "" {
				if result.Result == "" || !containsSubstring(result.Result, tt.wantResultContains) {
					t.Errorf("executeDirectTool() result = %v, want to contain %v", result.Result, tt.wantResultContains)
				}
			}

			// Verify execution time is recorded
			if result.ExecutionTimeMs < 0 {
				t.Errorf("executeDirectTool() execution time should be non-negative, got %v", result.ExecutionTimeMs)
			}
		})
	}
}

// TestGetAvailableToolNames tests getting available tool names
func TestGetAvailableToolNames(t *testing.T) {
	// Create test agent with multiple tools
	ag := &agent.Agent{
		Plugins: map[string]types.LoadedPlugin{
			"math": {
				Tool: &mockTool{name: "math", description: "Math operations"},
				Definition: pluginapi.Tool{
					Name:        "math",
					Description: "Math operations",
					Parameters: map[string]interface{}{
						"type":       "object",
						"properties": map[string]interface{}{},
					},
				},
			},
			"weather": {
				Tool: &mockTool{name: "weather", description: "Weather info"},
				Definition: pluginapi.Tool{
					Name:        "weather",
					Description: "Weather info",
					Parameters: map[string]interface{}{
						"type":       "object",
						"properties": map[string]interface{}{},
					},
				},
			},
		},
	}

	h := &Handler{}
	toolNames := h.getAvailableToolNames(ag)

	// Verify we got the expected number of tools
	if len(toolNames) != 2 {
		t.Errorf("getAvailableToolNames() returned %d tools, want 2", len(toolNames))
	}

	// Verify tool names are present
	foundMath := false
	foundWeather := false
	for _, name := range toolNames {
		if name == "math" {
			foundMath = true
		}
		if name == "weather" {
			foundWeather = true
		}
	}

	if !foundMath {
		t.Errorf("getAvailableToolNames() missing 'math' tool")
	}
	if !foundWeather {
		t.Errorf("getAvailableToolNames() missing 'weather' tool")
	}
}

// TestFormatDirectToolResponse tests formatting of direct tool results
func TestFormatDirectToolResponse(t *testing.T) {
	tests := []struct {
		name       string
		result     *DirectToolResult
		wantFields []string
	}{
		{
			name: "successful result",
			result: &DirectToolResult{
				Success:         true,
				ToolName:        "math",
				ToolArgs:        `{"operation": "add"}`,
				Result:          "42",
				ExecutionTimeMs: 10,
			},
			wantFields: []string{"response", "direct_tool_call", "tool_name", "execution_time_ms", "success"},
		},
		{
			name: "failed result with error",
			result: &DirectToolResult{
				Success:         false,
				ToolName:        "math",
				ToolArgs:        `{"operation": "divide"}`,
				Result:          "error",
				Error:           "division by zero",
				ExecutionTimeMs: 5,
			},
			wantFields: []string{"response", "direct_tool_call", "tool_name", "execution_time_ms", "success", "error"},
		},
		{
			name: "structured result",
			result: &DirectToolResult{
				Success:         true,
				ToolName:        "table_tool",
				ToolArgs:        `{}`,
				Result:          "table data",
				ExecutionTimeMs: 20,
				Structured:      true,
				DisplayType:     "table",
				Title:           "Test Table",
			},
			wantFields: []string{"response", "direct_tool_call", "tool_name", "execution_time_ms", "success", "structured", "displayType", "title"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := formatDirectToolResponse(tt.result)

			// Check that all expected fields are present
			for _, field := range tt.wantFields {
				if _, exists := response[field]; !exists {
					t.Errorf("formatDirectToolResponse() missing field %q", field)
				}
			}

			// Verify direct_tool_call is always true
			if dtc, ok := response["direct_tool_call"].(bool); !ok || !dtc {
				t.Errorf("formatDirectToolResponse() direct_tool_call should be true")
			}

			// Verify response contains the result
			if respStr, ok := response["response"].(string); !ok || respStr != tt.result.Result {
				t.Errorf("formatDirectToolResponse() response = %v, want %v", respStr, tt.result.Result)
			}
		})
	}
}

// Helper function to check if a string contains a substring
func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > len(substr) && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
