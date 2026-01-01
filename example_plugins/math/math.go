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
// Most boilerplate is auto-generated in math_generated.go
type mathTool struct {
	pluginapi.BasePlugin
}

// --- Handler functions (the only code you need to write) ---
// The naming convention handle{PascalCase} is auto-wired by the generator

func handleAdd(ctx context.Context, m *mathTool, params *MathParams) (string, error) {
	result := params.A + params.B
	return fmt.Sprintf("%g", result), nil
}

func handleSubtract(ctx context.Context, m *mathTool, params *MathParams) (string, error) {
	result := params.A - params.B
	return fmt.Sprintf("%g", result), nil
}

func handleMultiply(ctx context.Context, m *mathTool, params *MathParams) (string, error) {
	result := params.A * params.B
	return fmt.Sprintf("%g", result), nil
}

func handleDivide(ctx context.Context, m *mathTool, params *MathParams) (string, error) {
	if params.B == 0 {
		return "", errors.New("division by zero")
	}
	result := params.A / params.B
	return fmt.Sprintf("%g", result), nil
}

func main() {
	pluginapi.ServePlugin(&mathTool{}, configYAML)
}
