# Plugin Development Guide

This guide walks you through building plugins for Ori Agent using the modern code generation pattern.

## Table of Contents

- [Overview](#overview)
- [Quick Start](#quick-start)
- [Plugin Structure](#plugin-structure)
- [Step-by-Step Tutorial](#step-by-step-tutorial)
- [Code Generation with `ori-plugin-gen`](#code-generation-with-ori-plugin-gen)
- [Optional Plugin Interfaces](#optional-plugin-interfaces)
- [Building and Testing](#building-and-testing)
- [Best Practices](#best-practices)
- [Troubleshooting](#troubleshooting)

## Overview

### What is a Plugin?

A plugin is a standalone executable that extends Ori Agent with custom tools. Plugins:

- Run as separate processes (not shared libraries)
- Communicate via gRPC/Protocol Buffers
- Are language-agnostic (though this guide focuses on Go)
- Can be distributed as binaries

### Modern Plugin Pattern

The modern approach uses **code generation** to eliminate boilerplate:

1. Define parameters in `plugin.yaml`
2. Run `go generate` to create type-safe structs and validation
3. Implement business logic in an `Execute()` method
4. Let `pluginapi.ServePlugin()` handle the rest

**Result**: 70-90% less code compared to manual implementation.

## Quick Start

### Prerequisites

- Go 1.25+
- Ori Agent installed and built
- The `ori-plugin-gen` tool (built automatically with Ori Agent)

### Create a Simple Plugin (5 minutes)

```bash
# 1. Create plugin directory
mkdir my-plugin
cd my-plugin

# 2. Initialize Go module
go mod init github.com/yourusername/my-plugin
go get github.com/johnjallday/ori-agent/pluginapi

# 3. Create plugin.yaml (see below)
# 4. Create main.go (see below)
# 5. Generate code
go generate

# 6. Build
go build -o my-plugin .
```

## Plugin Structure

### Minimal Plugin Directory

```
my-plugin/
├── plugin.yaml          # Plugin metadata and tool definition
├── main.go             # Plugin entry point
├── my_plugin_generated.go  # Auto-generated (by go generate)
└── go.mod              # Go module definition
```

### plugin.yaml

This is the **single source of truth** for your plugin:

```yaml
name: my-plugin
version: 0.1.0
description: A simple example plugin
license: MIT
repository: https://github.com/yourusername/my-plugin

maintainers:
  - name: Your Name
    email: you@example.com

platforms:
  - os: darwin
    architectures: [amd64, arm64]
  - os: linux
    architectures: [amd64, arm64]
  - os: windows
    architectures: [amd64]

requirements:
  min_ori_version: "0.0.10"
  dependencies: []

tool_definition:
  description: "Performs simple operations on numbers"
  parameters:
    - name: operation
      type: string
      description: "Operation to perform"
      required: true
      enum:
        - add
        - subtract
        - multiply
        - divide

    - name: a
      type: number
      description: "First operand"
      required: true

    - name: b
      type: number
      description: "Second operand"
      required: true
```

### main.go (Minimal Example)

```go
package main

//go:generate ../path/to/ori-agent/bin/ori-plugin-gen -yaml=plugin.yaml -output=my_plugin_generated.go

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/johnjallday/ori-agent/pluginapi"
)

//go:embed plugin.yaml
var configYAML string

// myPluginTool implements the PluginTool interface
// Note: Compile-time interface check is in my_plugin_generated.go
type myPluginTool struct {
	pluginapi.BasePlugin
}

// OperationHandler is a function that handles a specific operation
type OperationHandler func(m *myPluginTool, params *MyPluginParams) (string, error)

// operationRegistry maps operation names to their handler functions
var operationRegistry = map[string]OperationHandler{
	"add":      handleAdd,
	"subtract": handleSubtract,
	"multiply": handleMultiply,
	"divide":   handleDivide,
}

// Note: Definition() is inherited from BasePlugin (reads plugin.yaml automatically)
// Note: Call() is auto-generated in my_plugin_generated.go from plugin.yaml

// Execute contains the business logic - called by the generated Call() method
func (m *myPluginTool) Execute(ctx context.Context, params *MyPluginParams) (string, error) {
	// Look up handler in registry
	handler, ok := operationRegistry[params.Operation]
	if !ok {
		return "", fmt.Errorf("unknown operation: %s", params.Operation)
	}

	// Execute the handler
	return handler(m, params)
}

// Operation handlers

func handleAdd(m *myPluginTool, params *MyPluginParams) (string, error) {
	result := params.A + params.B
	return fmt.Sprintf("%g", result), nil
}

func handleSubtract(m *myPluginTool, params *MyPluginParams) (string, error) {
	result := params.A - params.B
	return fmt.Sprintf("%g", result), nil
}

func handleMultiply(m *myPluginTool, params *MyPluginParams) (string, error) {
	result := params.A * params.B
	return fmt.Sprintf("%g", result), nil
}

func handleDivide(m *myPluginTool, params *MyPluginParams) (string, error) {
	if params.B == 0 {
		return "", fmt.Errorf("division by zero")
	}
	result := params.A / params.B
	return fmt.Sprintf("%g", result), nil
}

func main() {
	// Create tool instance - ServePlugin handles all initialization automatically
	tool := &myPluginTool{}

	// Serve the plugin (handles RPC setup and initialization automatically)
	pluginapi.ServePlugin(tool, configYAML)
}
```

## Step-by-Step Tutorial

### Step 1: Define Your Tool in plugin.yaml

Start by defining what your plugin does and what parameters it accepts:

```yaml
tool_definition:
  description: "Brief description of what your tool does"
  parameters:
    - name: operation
      type: string
      description: "What operation to perform"
      required: true
      enum: [create, read, update, delete]

    - name: id
      type: string
      description: "Resource identifier"
      required: false
```

**Parameter Types:**
- `string` - Text values
- `number` - Floating-point numbers
- `integer` - Whole numbers
- `boolean` - true/false
- `array` - Lists of values
- `object` - Nested structures

**Parameter Attributes:**
- `required: true/false` - Whether parameter is mandatory
- `enum: [...]` - List of allowed values
- `min/max` - Numeric constraints
- `default` - Default value if not provided

### Step 2: Create the Plugin Struct

```go
type myPluginTool struct {
	pluginapi.BasePlugin  // Embed BasePlugin for automatic features
	// Add any additional fields your plugin needs
	// e.g., database connection, API client, settings, etc.
}
```

### Step 3: Add the //go:generate Directive

At the top of your main.go:

```go
//go:generate ../path/to/ori-agent/bin/ori-plugin-gen -yaml=plugin.yaml -output=my_plugin_generated.go
```

**Path options:**
- Relative: `../../ori-agent/bin/ori-plugin-gen` (from plugin directory)
- Absolute: `/full/path/to/ori-agent/bin/ori-plugin-gen`

### Step 4: Implement the Execute Method

This is where your business logic goes:

```go
func (m *myPluginTool) Execute(ctx context.Context, params *MyPluginParams) (string, error) {
	// Option 1: Use operation registry pattern (recommended for multiple operations)
	handler, ok := operationRegistry[params.Operation]
	if !ok {
		return "", fmt.Errorf("unknown operation: %s", params.Operation)
	}
	return handler(m, params)

	// Option 2: Simple switch statement
	switch params.Operation {
	case "create":
		return m.handleCreate(params)
	case "read":
		return m.handleRead(params)
	default:
		return "", fmt.Errorf("unknown operation: %s", params.Operation)
	}
}
```

### Step 5: Generate Code

Run `go generate` to create the boilerplate:

```bash
go generate
```

This creates `my_plugin_generated.go` with:
- `MyPluginParams` struct (type-safe parameters)
- `Call()` method (handles JSON unmarshaling and validation)
- Compile-time interface checks

### Step 6: Build and Test

```bash
# Build the plugin
go build -o my-plugin .

# Test it with ori-agent
# 1. Copy to uploaded_plugins/
cp my-plugin /path/to/ori-agent/uploaded_plugins/

# 2. Restart ori-agent
# 3. Test in the chat interface
```

## Code Generation with `ori-plugin-gen`

### What Gets Generated?

The `ori-plugin-gen` tool creates:

**1. Parameter Struct**
```go
type MyPluginParams struct {
	Operation string  `json:"operation"` // What operation to perform
	A         float64 `json:"a"`         // First operand
	B         float64 `json:"b"`         // Second operand
}
```

**2. Call Method with Validation**
```go
func (t *myPluginTool) Call(ctx context.Context, args string) (string, error) {
	var params MyPluginParams

	// Unmarshal JSON arguments
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// Validate required fields
	if params.Operation == "" {
		return "", fmt.Errorf("required field 'operation' is missing")
	}

	// Call your Execute method
	return t.Execute(ctx, &params)
}
```

**3. Interface Check**
```go
var _ pluginapi.PluginTool = (*myPluginTool)(nil)
```

### Regenerating Code

Whenever you modify `plugin.yaml` parameters:

1. Run `go generate`
2. Update your handler signatures to use the new parameter types
3. Rebuild

## Optional Plugin Interfaces

Beyond the basic `PluginTool` interface, you can implement optional interfaces for advanced features:

### VersionedTool
Provides version information:
```go
// Implemented automatically by BasePlugin
// Version comes from plugin.yaml
```

### AgentAwareTool
Receive agent-specific context:
```go
func (t *myPluginTool) SetAgentContext(ctx pluginapi.AgentContext) {
	// Access agent name, config path, etc.
	log.Printf("Running for agent: %s", ctx.AgentName)
}
```

### InitializationProvider
Define configuration requirements:
```go
func (t *myPluginTool) GetRequiredConfig() []pluginapi.ConfigVariable {
	return []pluginapi.ConfigVariable{
		{
			Key:          "api_key",
			Name:         "API Key",
			Description:  "Your API key",
			Type:         pluginapi.ConfigTypeString,
			Required:     true,
			DefaultValue: "",
		},
	}
}

func (t *myPluginTool) ValidateConfig(config map[string]interface{}) error {
	apiKey, ok := config["api_key"].(string)
	if !ok || apiKey == "" {
		return fmt.Errorf("api_key is required")
	}
	return nil
}

func (t *myPluginTool) InitializeWithConfig(config map[string]interface{}) error {
	t.apiKey = config["api_key"].(string)
	return nil
}
```

### WebPageProvider
Serve custom web UIs:
```go
func (t *myPluginTool) GetWebPages() []string {
	return []string{"dashboard", "settings"}
}

func (t *myPluginTool) ServeWebPage(path string, query map[string]string) (string, string, error) {
	switch path {
	case "dashboard":
		html := "<html>...</html>"
		return html, "text/html", nil
	default:
		return "", "", fmt.Errorf("page not found: %s", path)
	}
}
```

**URL**: `http://localhost:8765/api/plugins/my-plugin/pages/dashboard`

### MetadataProvider
Provide rich metadata:
```go
// Implemented automatically by BasePlugin
// Metadata comes from plugin.yaml
```

### PluginCompatibility
Declare version requirements:
```go
// Implemented automatically by BasePlugin
// Requirements come from plugin.yaml
```

## Building and Testing

### Building the Plugin

```bash
# Simple build
go build -o my-plugin .

# Build with version embedding (recommended)
VERSION=$(grep "version:" plugin.yaml | awk '{print $2}')
go build -ldflags "-X main.Version=$VERSION" -o my-plugin .
```

### Testing Locally

**Option 1: Upload via UI**
1. Build your plugin: `go build -o my-plugin .`
2. Open Ori Agent UI: `http://localhost:8765`
3. Go to Settings → Plugins → Upload
4. Select your plugin binary

**Option 2: Copy to uploaded_plugins**
```bash
# Copy plugin
cp my-plugin /path/to/ori-agent/uploaded_plugins/

# Restart ori-agent
# Plugin auto-loads on startup
```

**Option 3: Test with Direct RPC**
```go
// Create a test file
package main

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/pluginloader"
)

func TestPlugin(t *testing.T) {
	tool, err := pluginloader.LoadPluginUnified("./my-plugin")
	if err != nil {
		t.Fatal(err)
	}

	result, err := tool.Call(context.Background(), `{"operation":"add","a":5,"b":3}`)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Result: %s", result)
}
```

### Debugging

**Enable plugin logs:**
```bash
# Plugin stdout/stderr goes to ori-agent's output
./bin/ori-agent
```

**Add debug logging in your plugin:**
```go
import "log"

func (m *myPluginTool) Execute(ctx context.Context, params *MyPluginParams) (string, error) {
	log.Printf("[my-plugin] Execute called with: %+v", params)
	// ...
}
```

**Check plugin health:**
```bash
curl http://localhost:8765/api/plugins
```

## Best Practices

### 1. Use the Operation Registry Pattern

For plugins with multiple operations:

```go
type OperationHandler func(m *myPluginTool, params *MyPluginParams) (string, error)

var operationRegistry = map[string]OperationHandler{
	"create": handleCreate,
	"read":   handleRead,
	"update": handleUpdate,
	"delete": handleDelete,
}

func (m *myPluginTool) Execute(ctx context.Context, params *MyPluginParams) (string, error) {
	handler, ok := operationRegistry[params.Operation]
	if !ok {
		return "", fmt.Errorf("unknown operation: %s", params.Operation)
	}
	return handler(m, params)
}
```

**Benefits:**
- Easy to add new operations
- Clear separation of concerns
- Testable individual handlers

### 2. Return Structured Data

Use the result helpers for rich UI rendering:

```go
import "github.com/johnjallday/ori-agent/pluginapi"

func (m *myPluginTool) listUsers() (string, error) {
	users := []User{...}

	result := pluginapi.NewTableResult(
		"Users",
		[]string{"Name", "Email", "Role"},
		users,
	)
	result.Description = fmt.Sprintf("Found %d users", len(users))

	return result.ToJSON()
}
```

### 3. Handle Errors Gracefully

```go
func (m *myPluginTool) Execute(ctx context.Context, params *MyPluginParams) (string, error) {
	// Validate business logic
	if params.Count < 1 || params.Count > 100 {
		return "", fmt.Errorf("count must be between 1 and 100, got %d", params.Count)
	}

	// Handle external errors
	result, err := m.externalAPI.Call()
	if err != nil {
		return "", fmt.Errorf("external API failed: %w", err)
	}

	return result, nil
}
```

### 4. Use Context for Cancellation

```go
func (m *myPluginTool) Execute(ctx context.Context, params *MyPluginParams) (string, error) {
	// Check if request was cancelled
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}

	// Pass context to long-running operations
	result, err := m.longOperation(ctx, params)
	if err != nil {
		return "", err
	}

	return result, nil
}
```

### 5. Version Your Plugin

Include version in plugin.yaml and embed in binary:

```bash
VERSION=$(grep "version:" plugin.yaml | awk '{print $2}')
go build -ldflags "-X main.Version=$VERSION" -o my-plugin .
```

### 6. Document Your Plugin

Add a README.md:

```markdown
# My Plugin

Brief description of what your plugin does.

## Installation

Copy `my-plugin` to ori-agent's `uploaded_plugins/` directory.

## Configuration

Requires the following configuration:
- `api_key`: Your API key

## Operations

- `create` - Creates a new resource
- `read` - Reads an existing resource
- `update` - Updates a resource
- `delete` - Deletes a resource

## Examples

Ask the agent:
- "Create a new task called 'Buy groceries'"
- "Read task with ID 123"
```

## Troubleshooting

### "cannot find package" errors

Make sure you have the pluginapi dependency:
```bash
go get github.com/johnjallday/ori-agent/pluginapi
go mod tidy
```

### "undefined: MyPluginParams"

Run `go generate` to create the generated code:
```bash
go generate
```

### Plugin not loading in Ori Agent

Check that:
1. Plugin is executable: `chmod +x my-plugin`
2. Plugin is in the correct location
3. Plugin.yaml has correct structure
4. Check ori-agent logs for errors

### "operation not found" errors

Make sure the operation name in your handler registry matches the enum values in plugin.yaml exactly (case-sensitive).

### Parameters are empty

Check that:
1. You ran `go generate` after modifying plugin.yaml
2. Parameter names in plugin.yaml match the JSON field names
3. The generated struct has correct field tags

## Next Steps

- Check out the [example plugins](example_plugins/) for complete examples
- Read the [Plugin Optimization Guide](PLUGIN_OPTIMIZATION_GUIDE.md) for advanced features
- See [CLAUDE.md](CLAUDE.md) for the complete architecture overview

## Example Plugins

The ori-agent repository includes several example plugins:

- **math** - Basic arithmetic operations (simplest example)
- **weather** - Fetches weather data from an API
- **webapp** - Demonstrates web page provider interface
- **minimal** - Bare minimum plugin implementation

Browse them in `example_plugins/` directory.
