# Minimal Plugin Example

This example demonstrates the **optimized plugin development experience** using the new plugin optimization APIs:

1. **YAML-based Tool Definitions** - Define your tool's parameters in `plugin.yaml` instead of code
2. **Settings API** - Simple key-value storage for plugin configuration and state
3. **Simplified Development** - Focus on your plugin's logic, not boilerplate

## What's Different?

### Before (Old Way)
```go
func (t *MyTool) Definition() pluginapi.Tool {
    return pluginapi.NewTool(
        "my-tool",
        "Description here",
        pluginapi.ObjectProperty("", map[string]interface{}{
            "operation": pluginapi.StringEnumProperty(
                "Operation to perform",
                []string{"echo", "status"},
            ),
            "message": pluginapi.StringProperty("Message to echo"),
            "count": pluginapi.WithMinMax(
                pluginapi.IntegerProperty("Repeat count"),
                1, 10,
            ),
        }, []string{"operation"}),
    )
}
// 20+ lines of parameter building!
```

### After (New Way)
```go
func (t *MyTool) Definition() pluginapi.Tool {
    tool, _ := t.GetToolDefinition()
    return tool
}
// Just 3 lines! Parameters defined in plugin.yaml
```

## Key Features Demonstrated

### 1. YAML-Based Tool Definition
All parameters are defined in `plugin.yaml`:
- Parameter types (string, integer, boolean, enum, etc.)
- Validation rules (min, max, required, enum values)
- Descriptions and defaults
- No code needed!

### 2. Settings API
Simple configuration storage:
```go
// Get the settings manager
sm := t.Settings()

// Store values
sm.Set("api_endpoint", "https://api.example.com")
sm.Set("timeout_seconds", 30)
sm.Set("debug_mode", true)

// Retrieve values (type-safe)
endpoint, _ := sm.GetString("api_endpoint")
timeout, _ := sm.GetInt("timeout_seconds")
debug, _ := sm.GetBool("debug_mode")

// Get all settings
all, _ := sm.GetAll()
```

### 3. Automatic Configuration Handling
Configuration variables from `plugin.yaml` are automatically:
- Presented in the UI
- Validated against schema
- Stored using Settings API
- Available throughout plugin lifecycle

## Building

```bash
go build -o minimal-plugin main.go
```

## Testing

```bash
# Build the plugin
go build -o minimal-plugin main.go

# Copy to ori-agent (for testing)
cp minimal-plugin ../../uploaded_plugins/

# Restart ori-agent to load the plugin
# Then test via chat:
# - "Use minimal plugin to echo hello"
# - "Use minimal plugin to show status"
```

## Code Organization

```
minimal/
├── plugin.yaml       # All configuration and tool definition
├── main.go          # Plugin implementation (< 200 lines!)
└── README.md        # This file
```

## What You'll Learn

1. How to define tool parameters in YAML
2. How to use the Settings API for configuration
3. How to implement required plugin interfaces
4. How to validate and initialize configuration
5. How to build a complete plugin with minimal boilerplate

## Next Steps

See `example_plugins/webapp/` for a more advanced example that includes:
- Custom web page serving
- Template rendering with the Template API
- More complex plugin interactions
