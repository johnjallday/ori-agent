package main

//go:generate ../../bin/ori-plugin-gen -yaml=plugin.yaml -output=math_generated.go

import (
	"context"
	_ "embed"
	"errors"
	"fmt"

	"github.com/johnjallday/ori-agent/pluginapi"
)

//go:embed plugin.yaml
var configYAML string

// mathTool implements pluginapi.Tool for basic arithmetic operations.
// Note: Compile-time interface check is in math_generated.go
type mathTool struct {
	pluginapi.BasePlugin // Embed BasePlugin to get version/metadata methods for free
}

// OperationHandler is a function that handles a specific operation
type OperationHandler func(m *mathTool, params *MathParams) (string, error)

// operationRegistry maps operation names to their handler functions
var operationRegistry = map[string]OperationHandler{
	"add":      handleAdd,
	"subtract": handleSubtract,
	"multiply": handleMultiply,
	"divide":   handleDivide,
}

// Note: Definition() is inherited from BasePlugin, which automatically reads from plugin.yaml
// Note: Call() is auto-generated in math_generated.go from plugin.yaml

// Execute contains the business logic - called by the generated Call() method
func (m *mathTool) Execute(ctx context.Context, params *MathParams) (string, error) {
	// Look up handler in registry
	handler, ok := operationRegistry[params.Operation]
	if !ok {
		return "", fmt.Errorf("unknown operation: %s. Valid operations: add, subtract, multiply, divide", params.Operation)
	}

	// Execute the handler
	return handler(m, params)
}

// Operation handlers

func handleAdd(m *mathTool, params *MathParams) (string, error) {
	result := params.A + params.B
	return fmt.Sprintf("%g", result), nil
}

func handleSubtract(m *mathTool, params *MathParams) (string, error) {
	result := params.A - params.B
	return fmt.Sprintf("%g", result), nil
}

func handleMultiply(m *mathTool, params *MathParams) (string, error) {
	result := params.A * params.B
	return fmt.Sprintf("%g", result), nil
}

func handleDivide(m *mathTool, params *MathParams) (string, error) {
	if params.B == 0 {
		return "", errors.New("division by zero")
	}
	result := params.A / params.B
	return fmt.Sprintf("%g", result), nil
}

func main() {
	pluginapi.ServePlugin(&mathTool{}, configYAML)
}
