package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/hashicorp/go-plugin"
	"github.com/johnjallday/ori-agent/pluginapi"
)

//go:embed plugin.yaml
var configYAML string

// ensure mathTool implements pluginapi.PluginTool and optional interfaces at compile time
var (
	_ pluginapi.PluginTool          = (*mathTool)(nil)
	_ pluginapi.VersionedTool       = (*mathTool)(nil)
	_ pluginapi.PluginCompatibility = (*mathTool)(nil)
	_ pluginapi.MetadataProvider    = (*mathTool)(nil)
)

// mathTool implements pluginapi.Tool for basic arithmetic operations.
type mathTool struct {
	pluginapi.BasePlugin // Embed BasePlugin to get version/metadata methods for free
}

// Definition returns the generic function definition for the math operation.
func (m *mathTool) Definition() pluginapi.Tool {
	return pluginapi.Tool{
		Name:        "math",
		Description: "Perform basic math operations: add, subtract, multiply, divide",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"operation": map[string]interface{}{
					"type":        "string",
					"description": "Operation to perform: add, subtract, multiply, divide",
					"enum":        []string{"add", "subtract", "multiply", "divide"},
				},
				"a": map[string]interface{}{
					"type":        "number",
					"description": "First operand",
				},
				"b": map[string]interface{}{
					"type":        "number",
					"description": "Second operand",
				},
			},
			"required": []string{"operation", "a", "b"},
		},
	}
}

// Call is invoked with the function arguments and returns the computed result.
func (m *mathTool) Call(ctx context.Context, args string) (string, error) {
	var p struct {
		Operation string  `json:"operation"`
		A         float64 `json:"a"`
		B         float64 `json:"b"`
	}
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return "", err
	}
	var result float64
	switch p.Operation {
	case "add":
		result = p.A + p.B
	case "subtract":
		result = p.A - p.B
	case "multiply":
		result = p.A * p.B
	case "divide":
		if p.B == 0 {
			return "", errors.New("division by zero")
		}
		result = p.A / p.B
	default:
		return "", fmt.Errorf("unknown operation %q", p.Operation)
	}
	return fmt.Sprintf("%g", result), nil
}

// No need for getter methods - BasePlugin provides them all!

func main() {
	// Parse plugin config from embedded YAML
	config := pluginapi.ReadPluginConfig(configYAML)

	// Create math tool with base plugin
	tool := &mathTool{
		BasePlugin: pluginapi.NewBasePlugin(
			"math",                            // Plugin name
			config.Version,                    // Version from config
			config.Requirements.MinOriVersion, // Min agent version
			"",                                // Max agent version (no limit)
			"v1",                              // API version
		),
	}

	// Set metadata from config
	if metadata, err := config.ToMetadata(); err == nil {
		tool.SetMetadata(metadata)
	}

	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: pluginapi.Handshake,
		Plugins: map[string]plugin.Plugin{
			"tool": &pluginapi.ToolRPCPlugin{Impl: tool},
		},
		GRPCServer: plugin.DefaultGRPCServer,
	})
}
