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

// OperationParams holds all possible parameters for operations
type OperationParams struct {
	Operation string  `json:"operation"`
	A         float64 `json:"a"`
	B         float64 `json:"b"`
}

// OperationHandler is a function that handles a specific operation
type OperationHandler func(m *mathTool, params *OperationParams) (string, error)

// operationRegistry maps operation names to their handler functions
var operationRegistry = map[string]OperationHandler{
	"add":      handleAdd,
	"subtract": handleSubtract,
	"multiply": handleMultiply,
	"divide":   handleDivide,
}

// Note: Definition() is inherited from BasePlugin, which automatically reads from plugin.yaml

// Call is invoked with the function arguments and returns the computed result.
func (m *mathTool) Call(ctx context.Context, args string) (string, error) {
	var params OperationParams
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", err
	}

	// Look up handler in registry
	handler, ok := operationRegistry[params.Operation]
	if !ok {
		return "", fmt.Errorf("unknown operation: %s. Valid operations: add, subtract, multiply, divide", params.Operation)
	}

	// Execute the handler
	return handler(m, &params)
}

// Operation handlers

func handleAdd(m *mathTool, params *OperationParams) (string, error) {
	result := params.A + params.B
	return fmt.Sprintf("%g", result), nil
}

func handleSubtract(m *mathTool, params *OperationParams) (string, error) {
	result := params.A - params.B
	return fmt.Sprintf("%g", result), nil
}

func handleMultiply(m *mathTool, params *OperationParams) (string, error) {
	result := params.A * params.B
	return fmt.Sprintf("%g", result), nil
}

func handleDivide(m *mathTool, params *OperationParams) (string, error) {
	if params.B == 0 {
		return "", errors.New("division by zero")
	}
	result := params.A / params.B
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

	// Set plugin config for YAML-based features
	tool.SetPluginConfig(&config)

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
