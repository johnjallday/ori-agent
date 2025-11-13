# Plugin Migration Guide: Generic Tool Interface

## Overview

**Version:** 1.0
**Date:** November 2024
**Breaking Change:** Yes

This guide helps plugin developers migrate from the OpenAI-specific plugin interface to the new generic, provider-agnostic interface.

## Why This Change?

### Before
- Plugins were tightly coupled to OpenAI's `openai-go` library
- Required importing `github.com/openai/openai-go/v2` in every plugin
- Larger binary sizes due to unnecessary dependencies
- Limited to OpenAI's function calling format

### After
- **Provider-agnostic**: Works with OpenAI, Claude, Ollama, and future providers
- **Smaller binaries**: No OpenAI dependency in plugin code
- **Cleaner interface**: Simple `map[string]interface{}` for JSON Schema
- **Future-proof**: Easy to add new LLM providers

## Migration Steps

### Step 1: Update Imports

**Before:**
```go
import (
    "github.com/hashicorp/go-plugin"
    "github.com/johnjallday/ori-agent/pluginapi"
    "github.com/openai/openai-go/v2"
)
```

**After:**
```go
import (
    "github.com/hashicorp/go-plugin"
    "github.com/johnjallday/ori-agent/pluginapi"
)
```

### Step 2: Update Interface Assertions

**Before:**
```go
var _ pluginapi.Tool = (*myTool)(nil)
```

**After:**
```go
var _ pluginapi.PluginTool = (*myTool)(nil)
```

### Step 3: Update Definition() Method

**Before:**
```go
func (t *myTool) Definition() openai.FunctionDefinitionParam {
    return openai.FunctionDefinitionParam{
        Name:        "my_tool",
        Description: openai.String("Does something useful"),
        Parameters: openai.FunctionParameters{
            "type": "object",
            "properties": map[string]any{
                "param1": map[string]any{
                    "type":        "string",
                    "description": "First parameter",
                },
            },
            "required": []string{"param1"},
        },
    }
}
```

**After:**
```go
func (t *myTool) Definition() pluginapi.Tool {
    return pluginapi.Tool{
        Name:        "my_tool",
        Description: "Does something useful",
        Parameters: map[string]interface{}{
            "type": "object",
            "properties": map[string]interface{}{
                "param1": map[string]interface{}{
                    "type":        "string",
                    "description": "First parameter",
                },
            },
            "required": []string{"param1"},
        },
    }
}
```

### Step 4: Update go.mod

Remove the OpenAI dependency from your plugin's `go.mod`:

**Before:**
```go
require (
    github.com/hashicorp/go-plugin v1.7.0
    github.com/johnjallday/ori-agent v0.0.0
    github.com/openai/openai-go/v2 v2.0.2
)
```

**After:**
```go
require (
    github.com/hashicorp/go-plugin v1.7.0
    github.com/johnjallday/ori-agent v0.0.0
)
```

Then run:
```bash
GOWORK=off go mod tidy
```

### Step 5: Rebuild Your Plugin

```bash
GOWORK=off go build -o your-plugin .
```

## Removing Boilerplate with BasePlugin

**NEW:** The `pluginapi` package now provides a `BasePlugin` struct that eliminates the need to write getter methods for common interfaces:

```go
package main

import (
    "context"
    _ "embed"
    "github.com/hashicorp/go-plugin"
    "github.com/johnjallday/ori-agent/pluginapi"
)

//go:embed plugin.yaml
var configYAML string

type myTool struct {
    pluginapi.BasePlugin // Embed to get version/metadata methods for free!
}

func (m *myTool) Definition() pluginapi.Tool {
    // Your tool definition
}

func (m *myTool) Call(ctx context.Context, args string) (string, error) {
    // Your implementation
}

func main() {
    config := pluginapi.ReadPluginConfig(configYAML)

    tool := &myTool{
        BasePlugin: pluginapi.NewBasePlugin(
            "my-tool",                         // Plugin name
            config.Version,                    // Version
            config.Requirements.MinOriVersion, // Min agent version
            "",                                // Max agent version (empty = no limit)
            "v1",                              // API version
        ),
    }

    // Optional: Set metadata from config
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
```

**What BasePlugin provides:**
- `Version()` - Returns plugin version
- `MinAgentVersion()` - Returns minimum agent version
- `MaxAgentVersion()` - Returns maximum agent version
- `APIVersion()` - Returns API version
- `SetAgentContext()` / `GetAgentContext()` - Agent context management
- `SetMetadata()` / `GetMetadata()` - Plugin metadata
- `SetDefaultSettings()` / `GetDefaultSettings()` - Default settings

**Before (lots of boilerplate):**
```go
type myTool struct {
    config pluginapi.PluginConfig
}

func (m *myTool) Version() string {
    return m.config.Version
}

func (m *myTool) MinAgentVersion() string {
    return m.config.Requirements.MinOriVersion
}

func (m *myTool) MaxAgentVersion() string {
    return ""
}

func (m *myTool) APIVersion() string {
    return "v1"
}

func (m *myTool) GetMetadata() (*pluginapi.PluginMetadata, error) {
    return m.config.ToMetadata()
}
```

**After (no boilerplate!):**
```go
type myTool struct {
    pluginapi.BasePlugin // All methods provided automatically!
}
```

## Helper Functions

The `pluginapi` package also provides helper functions to make JSON Schema construction easier:

```go
import "github.com/johnjallday/ori-agent/pluginapi"

func (t *myTool) Definition() pluginapi.Tool {
    return pluginapi.NewTool(
        "my_tool",
        "Does something useful",
        map[string]interface{}{
            "type": "object",
            "properties": map[string]interface{}{
                "name":   pluginapi.StringProperty("The name"),
                "age":    pluginapi.IntegerProperty("The age"),
                "active": pluginapi.BooleanProperty("Is active"),
                "tags":   pluginapi.ArrayProperty("List of tags", "string"),
            },
            "required": []string{"name"},
        },
    )
}
```

Available helpers:
- `NewTool(name, description, parameters)` - Create a tool
- `StringProperty(description)` - String parameter
- `NumberProperty(description)` - Numeric parameter
- `IntegerProperty(description)` - Integer parameter
- `BooleanProperty(description)` - Boolean parameter
- `ArrayProperty(description, itemType)` - Array parameter
- `ObjectProperty(description, properties)` - Nested object
- `EnumProperty(description, values)` - Enum with any values
- `StringEnumProperty(description, values)` - String enum
- `WithMinMax(prop, min, max)` - Add min/max constraints
- `WithDefault(prop, defaultValue)` - Add default value
- `WithPattern(prop, pattern)` - Add regex pattern

## Complete Example

### Before (OpenAI-coupled)

```go
package main

import (
    "context"
    "encoding/json"

    "github.com/hashicorp/go-plugin"
    "github.com/johnjallday/ori-agent/pluginapi"
    "github.com/openai/openai-go/v2"
)

type mathTool struct{}

var _ pluginapi.Tool = (*mathTool)(nil)

func (m *mathTool) Definition() openai.FunctionDefinitionParam {
    return openai.FunctionDefinitionParam{
        Name:        "math",
        Description: openai.String("Perform basic math operations"),
        Parameters: openai.FunctionParameters{
            "type": "object",
            "properties": map[string]any{
                "operation": map[string]any{
                    "type": "string",
                    "enum": []string{"add", "subtract"},
                },
                "a": map[string]any{"type": "number"},
                "b": map[string]any{"type": "number"},
            },
            "required": []string{"operation", "a", "b"},
        },
    }
}

func (m *mathTool) Call(ctx context.Context, args string) (string, error) {
    var p struct {
        Operation string  `json:"operation"`
        A         float64 `json:"a"`
        B         float64 `json:"b"`
    }
    json.Unmarshal([]byte(args), &p)

    if p.Operation == "add" {
        return fmt.Sprintf("%g", p.A + p.B), nil
    }
    return fmt.Sprintf("%g", p.A - p.B), nil
}

func main() {
    plugin.Serve(&plugin.ServeConfig{
        HandshakeConfig: pluginapi.Handshake,
        Plugins: map[string]plugin.Plugin{
            "tool": &pluginapi.ToolRPCPlugin{Impl: &mathTool{}},
        },
        GRPCServer: plugin.DefaultGRPCServer,
    })
}
```

### After (Generic, Provider-Agnostic)

```go
package main

import (
    "context"
    "encoding/json"

    "github.com/hashicorp/go-plugin"
    "github.com/johnjallday/ori-agent/pluginapi"
)

type mathTool struct{}

var _ pluginapi.PluginTool = (*mathTool)(nil)

func (m *mathTool) Definition() pluginapi.Tool {
    return pluginapi.Tool{
        Name:        "math",
        Description: "Perform basic math operations",
        Parameters: map[string]interface{}{
            "type": "object",
            "properties": map[string]interface{}{
                "operation": map[string]interface{}{
                    "type": "string",
                    "enum": []string{"add", "subtract"},
                },
                "a": map[string]interface{}{"type": "number"},
                "b": map[string]interface{}{"type": "number"},
            },
            "required": []string{"operation", "a", "b"},
        },
    }
}

func (m *mathTool) Call(ctx context.Context, args string) (string, error) {
    var p struct {
        Operation string  `json:"operation"`
        A         float64 `json:"a"`
        B         float64 `json:"b"`
    }
    json.Unmarshal([]byte(args), &p)

    if p.Operation == "add" {
        return fmt.Sprintf("%g", p.A + p.B), nil
    }
    return fmt.Sprintf("%g", p.A - p.B), nil
}

func main() {
    plugin.Serve(&plugin.ServeConfig{
        HandshakeConfig: pluginapi.Handshake,
        Plugins: map[string]plugin.Plugin{
            "tool": &pluginapi.ToolRPCPlugin{Impl: &mathTool{}},
        },
        GRPCServer: plugin.DefaultGRPCServer,
    })
}
```

## Common Issues

### Issue: Binary size didn't decrease
**Cause:** You may have other dependencies pulling in large libraries.
**Solution:** Run `go mod tidy` to remove unused dependencies.

### Issue: Interface not implemented error
**Cause:** Using `pluginapi.Tool` (struct) instead of `pluginapi.PluginTool` (interface).
**Solution:** Update interface assertions to use `pluginapi.PluginTool`.

### Issue: Description.String() undefined
**Cause:** Old code called `.String()` on OpenAI's `Opt[string]` type.
**Solution:** Description is now a plain string - remove `.String()` calls.

## Testing Your Migration

After migrating, verify your plugin works:

```bash
# Build your plugin
GOWORK=off go build -o my-plugin .

# Test with ori-agent
# 1. Place plugin in plugins/ directory
# 2. Start ori-agent
# 3. Load your plugin in the UI
# 4. Test with both OpenAI and Claude providers
```

## Questions?

- Check the [API Reference](API_REFERENCE.md) for detailed interface documentation
- Review [example plugins](example_plugins/) for reference implementations
- See [CLAUDE.md](CLAUDE.md) for development patterns

## Breaking Changes Summary

| Component | Old | New |
|-----------|-----|-----|
| Import | `"github.com/openai/openai-go/v2"` | Removed |
| Interface | `pluginapi.Tool` | `pluginapi.PluginTool` |
| Return Type | `openai.FunctionDefinitionParam` | `pluginapi.Tool` |
| Description | `openai.String("text")` | `"text"` (plain string) |
| Parameters | `openai.FunctionParameters{...}` | `map[string]interface{}{...}` |

---

**Note:** This is a one-time breaking change to make the plugin system provider-agnostic and future-proof. All existing plugins must be updated to work with the new interface.
