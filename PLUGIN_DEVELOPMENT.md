# 🔌 Plugin Development Guide

This guide provides comprehensive information on creating custom plugins for Dolphin Agent.

## Overview

Plugins are Go packages compiled as shared libraries (`.so` files) that implement the `pluginapi.Tool` interface. They extend Dolphin Agent's capabilities by providing custom functions that the AI can call during conversations.

## Table of Contents

- [Basic Plugin Structure](#basic-plugin-structure)
- [Interface Requirements](#interface-requirements)
- [Advanced Features](#advanced-features)
- [Building and Deployment](#building-and-deployment)
- [Example Plugins](#example-plugins)
- [Best Practices](#best-practices)
- [Troubleshooting](#troubleshooting)

## Basic Plugin Structure

### 1. Minimal Plugin Implementation

```go
package main

import (
    "context"
    "encoding/json"
    "github.com/johnjallday/dolphin-agent/pluginapi"
    "github.com/openai/openai-go/v2"
)

type MyTool struct{}

// Ensure interface compliance
var _ pluginapi.Tool = MyTool{}

func (t MyTool) Definition() openai.FunctionDefinitionParam {
    return openai.FunctionDefinitionParam{
        Name:        "my_function",
        Description: openai.String("Description of what this tool does"),
        Parameters: openai.FunctionParameters{
            "type": "object",
            "properties": map[string]any{
                "param1": map[string]any{
                    "type":        "string",
                    "description": "Parameter description",
                },
            },
            "required": []string{"param1"},
        },
    }
}

func (t MyTool) Call(ctx context.Context, args string) (string, error) {
    var params struct {
        Param1 string `json:"param1"`
    }
    if err := json.Unmarshal([]byte(args), &params); err != nil {
        return "", err
    }

    // Your tool logic here
    result := "Processed: " + params.Param1
    return result, nil
}

// Export the tool - this is required
var Tool MyTool
```

## Interface Requirements

### Core Interface: `pluginapi.Tool`

Every plugin must implement the `Tool` interface:

```go
type Tool interface {
    Definition() openai.FunctionDefinitionParam
    Call(ctx context.Context, args string) (string, error)
}
```

#### `Definition()` Method

Returns the OpenAI function definition that describes:
- Function name
- Description of what it does
- Input parameters and their types
- Which parameters are required

#### `Call()` Method

Executes the plugin logic:
- Receives context for cancellation/timeout
- Gets JSON string of arguments
- Returns result as string or error

### Parameter Types

Common parameter types for function definitions:

```go
// String parameter
"param_name": map[string]any{
    "type":        "string",
    "description": "Description of the parameter",
},

// Integer parameter with constraints
"number_param": map[string]any{
    "type":        "integer",
    "description": "A number parameter",
    "minimum":     1,
    "maximum":     100,
},

// Enum parameter
"option_param": map[string]any{
    "type":        "string",
    "description": "Select an option",
    "enum":        []string{"option1", "option2", "option3"},
},

// Boolean parameter
"flag_param": map[string]any{
    "type":        "boolean",
    "description": "True or false flag",
},
```

## Advanced Features

### 1. Plugin Initialization

For plugins that need configuration, implement `InitializationProvider`:

```go
import "errors"

type MyTool struct {
    initialized bool
    config      map[string]string
}

// Implement InitializationProvider for configuration
func (t *MyTool) GetRequiredConfig() []pluginapi.ConfigVariable {
    return []pluginapi.ConfigVariable{
        {
            Name:        "api_key",
            Type:        "string",
            Description: "Your API key for external service",
            Required:    true,
        },
        {
            Name:        "endpoint_url",
            Type:        "string",
            Description: "Custom endpoint URL (optional)",
            Required:    false,
        },
    }
}

func (t *MyTool) ValidateConfig(config map[string]interface{}) error {
    if _, ok := config["api_key"]; !ok {
        return errors.New("api_key is required")
    }
    return nil
}

func (t *MyTool) InitializeWithConfig(config map[string]interface{}) error {
    t.config = make(map[string]string)
    for k, v := range config {
        t.config[k] = fmt.Sprintf("%v", v)
    }
    t.initialized = true
    return nil
}
```

### 2. Agent Context Awareness

Access agent-specific information with `AgentAwareTool`:

```go
import "log"

// Implement AgentAwareTool for agent-specific behavior
func (t *MyTool) SetAgentContext(ctx pluginapi.AgentContext) {
    log.Printf("Plugin loaded for agent: %s", ctx.Name)
    // Access agent-specific configuration path: ctx.ConfigPath
    // Customize behavior based on agent
}
```

### 3. Version Information

Add version information to your plugin:

```go
// Version information set at build time via -ldflags
var (
    Version   = "dev"
    BuildTime = "unknown"
    GitCommit = "unknown"
)

func (t MyTool) Version() string {
    return Version
}

func (t MyTool) GetBuildInfo() map[string]string {
    return map[string]string{
        "version":    Version,
        "build_time": BuildTime,
        "git_commit": GitCommit,
    }
}
```

### 4. Error Handling

Implement robust error handling:

```go
func (t MyTool) Call(ctx context.Context, args string) (string, error) {
    // Check if plugin is initialized
    if !t.initialized {
        return "", errors.New("plugin not initialized - please configure first")
    }

    var params struct {
        Param1 string `json:"param1"`
    }

    if err := json.Unmarshal([]byte(args), &params); err != nil {
        return "", fmt.Errorf("failed to parse arguments: %w", err)
    }

    // Validate input
    if params.Param1 == "" {
        return "", errors.New("param1 cannot be empty")
    }

    // Check context cancellation
    select {
    case <-ctx.Done():
        return "", ctx.Err()
    default:
    }

    // Your plugin logic here
    result, err := t.processData(params.Param1)
    if err != nil {
        return "", fmt.Errorf("processing failed: %w", err)
    }

    return result, nil
}
```

## Building and Deployment

### 1. Build the Plugin

```bash
# Basic build
go build -buildmode=plugin -o my_plugin.so my_plugin.go

# Build with version information
go build -buildmode=plugin -ldflags="-X main.Version=v1.0.0 -X main.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" -o my_plugin.so my_plugin.go
```

### 2. Upload via UI

1. Open Dolphin Agent web interface
2. Go to the "Plugins" tab in the sidebar
3. Use the file upload to select your `.so` file
4. Click "Upload" to load the plugin

### 3. Upload via API

```bash
curl -X POST -F "plugin=@my_plugin.so" http://localhost:8080/api/plugins
```

### 4. Configure Plugin (if needed)

If your plugin implements `InitializationProvider`:

1. Navigate to the Plugins tab
2. Find your plugin in the loaded plugins list
3. Click "Configure" to set up required configuration
4. Enter the required values and save

## Example Plugins

### Simple Calculator Plugin

```go
package main

import (
    "context"
    "encoding/json"
    "fmt"
    "github.com/johnjallday/dolphin-agent/pluginapi"
    "github.com/openai/openai-go/v2"
)

type CalculatorTool struct{}

var _ pluginapi.Tool = CalculatorTool{}

func (c CalculatorTool) Definition() openai.FunctionDefinitionParam {
    return openai.FunctionDefinitionParam{
        Name:        "calculator",
        Description: openai.String("Perform basic math operations"),
        Parameters: openai.FunctionParameters{
            "type": "object",
            "properties": map[string]any{
                "operation": map[string]any{
                    "type":        "string",
                    "description": "Math operation to perform",
                    "enum":        []string{"add", "subtract", "multiply", "divide"},
                },
                "a": map[string]any{
                    "type":        "number",
                    "description": "First number",
                },
                "b": map[string]any{
                    "type":        "number",
                    "description": "Second number",
                },
            },
            "required": []string{"operation", "a", "b"},
        },
    }
}

func (c CalculatorTool) Call(ctx context.Context, args string) (string, error) {
    var params struct {
        Operation string  `json:"operation"`
        A         float64 `json:"a"`
        B         float64 `json:"b"`
    }

    if err := json.Unmarshal([]byte(args), &params); err != nil {
        return "", err
    }

    var result float64
    switch params.Operation {
    case "add":
        result = params.A + params.B
    case "subtract":
        result = params.A - params.B
    case "multiply":
        result = params.A * params.B
    case "divide":
        if params.B == 0 {
            return "", fmt.Errorf("cannot divide by zero")
        }
        result = params.A / params.B
    default:
        return "", fmt.Errorf("unknown operation: %s", params.Operation)
    }

    return fmt.Sprintf("%.2f", result), nil
}

var Tool CalculatorTool
```

### File Operations Plugin

```go
package main

import (
    "context"
    "encoding/json"
    "fmt"
    "os"
    "path/filepath"
    "github.com/johnjallday/dolphin-agent/pluginapi"
    "github.com/openai/openai-go/v2"
)

type FileOperationsTool struct{}

var _ pluginapi.Tool = FileOperationsTool{}

func (f FileOperationsTool) Definition() openai.FunctionDefinitionParam {
    return openai.FunctionDefinitionParam{
        Name:        "file_operations",
        Description: openai.String("Perform basic file operations"),
        Parameters: openai.FunctionParameters{
            "type": "object",
            "properties": map[string]any{
                "operation": map[string]any{
                    "type":        "string",
                    "description": "File operation to perform",
                    "enum":        []string{"list", "read", "exists", "size"},
                },
                "path": map[string]any{
                    "type":        "string",
                    "description": "File or directory path",
                },
            },
            "required": []string{"operation", "path"},
        },
    }
}

func (f FileOperationsTool) Call(ctx context.Context, args string) (string, error) {
    var params struct {
        Operation string `json:"operation"`
        Path      string `json:"path"`
    }

    if err := json.Unmarshal([]byte(args), &params); err != nil {
        return "", err
    }

    switch params.Operation {
    case "exists":
        if _, err := os.Stat(params.Path); os.IsNotExist(err) {
            return "false", nil
        }
        return "true", nil

    case "size":
        info, err := os.Stat(params.Path)
        if err != nil {
            return "", err
        }
        return fmt.Sprintf("%d bytes", info.Size()), nil

    case "list":
        entries, err := os.ReadDir(params.Path)
        if err != nil {
            return "", err
        }
        var result []string
        for _, entry := range entries {
            result = append(result, entry.Name())
        }
        return fmt.Sprintf("Files: %v", result), nil

    case "read":
        data, err := os.ReadFile(params.Path)
        if err != nil {
            return "", err
        }
        return string(data), nil

    default:
        return "", fmt.Errorf("unknown operation: %s", params.Operation)
    }
}

var Tool FileOperationsTool
```

## Best Practices

### 1. Error Handling
- Always validate input parameters
- Provide meaningful error messages
- Handle context cancellation
- Use wrapped errors with `fmt.Errorf`

### 2. Performance
- Respect context timeouts
- Avoid blocking operations without timeout
- Use efficient algorithms for data processing
- Consider memory usage for large operations

### 3. Security
- Validate and sanitize all inputs
- Avoid executing arbitrary commands
- Don't expose sensitive information in error messages
- Use proper file path validation

### 4. User Experience
- Provide clear, descriptive function names
- Write helpful parameter descriptions
- Return user-friendly response messages
- Use appropriate parameter types and constraints

### 5. Maintenance
- Add version information to your plugins
- Include comprehensive error handling
- Write clear code comments
- Follow Go conventions and best practices

## Troubleshooting

### Common Issues

#### Plugin Not Loading
```
Error: plugin.Open("my_plugin.so"): plugin was built with a different version of package github.com/openai/openai-go/v2
```

**Solution:** Ensure your plugin uses the same version of dependencies as Dolphin Agent.

#### Function Not Found
```
Error: plugin "my_plugin.so": symbol Tool not found
```

**Solution:** Make sure you export the Tool variable: `var Tool MyTool`

#### Runtime Panic
```
panic: interface conversion: MyTool is not pluginapi.Tool
```

**Solution:** Verify your struct implements all required interface methods.

### Debugging Tips

1. **Add logging:**
   ```go
   import "log"

   func (t MyTool) Call(ctx context.Context, args string) (string, error) {
       log.Printf("Plugin called with args: %s", args)
       // ... rest of implementation
   }
   ```

2. **Test interface compliance:**
   ```go
   var _ pluginapi.Tool = (*MyTool)(nil)
   ```

3. **Validate JSON parsing:**
   ```go
   log.Printf("Received args: %s", args)
   if err := json.Unmarshal([]byte(args), &params); err != nil {
       log.Printf("JSON parse error: %v", err)
       return "", err
   }
   log.Printf("Parsed params: %+v", params)
   ```

### Getting Help

- Check the [main repository](https://github.com/johnjallday/dolphin-agent) for issues
- Look at existing plugins in the `plugins/` directory
- Review the `pluginapi` package for interface definitions
- Test your plugin with a simple implementation first

## Included Example Plugins

The project includes several reference implementations:

- **Math Plugin** (`plugins/math/`): Basic arithmetic operations
- **Weather Plugin** (`plugins/weather/`): Weather information (mock implementation)
- **Result Handler Plugin** (`plugins/result-handler/`): File and URL handling

Study these examples to understand different implementation patterns and features.