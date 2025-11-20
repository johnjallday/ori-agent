# Plugin Optimization Guide

This guide covers the new **Plugin Optimization APIs** that dramatically simplify plugin development:

1. **YAML-Based Tool Definitions** - Define parameters in YAML, not code
2. **Settings API** - Simple key-value storage for plugin configuration
3. **Template Rendering API** - Serve web pages with templates

## Table of Contents

- [Overview](#overview)
- [Migration Guide](#migration-guide)
- [YAML Tool Definitions](#yaml-tool-definitions)
- [Settings API](#settings-api)
- [Template Rendering API](#template-rendering-api)
- [Before & After Examples](#before--after-examples)
- [Breaking Changes](#breaking-changes)
- [Troubleshooting](#troubleshooting)

## Overview

### What's New?

The optimization APIs reduce boilerplate and improve maintainability:

| Feature | Before | After | Benefit |
|---------|--------|-------|---------|
| Tool Parameters | 30+ lines of code | 10 lines of YAML | 70% less code |
| Configuration Storage | Custom JSON file handling | `sm.Set("key", value)` | Simple API |
| Web Templates | String concatenation | Template files | Separation of concerns |

### Example: Simplified Plugin

**Before** (old way):
```go
func (t *MyTool) Definition() pluginapi.Tool {
    return pluginapi.NewTool(
        "my-tool",
        "Description...",
        pluginapi.ObjectProperty("", map[string]interface{}{
            "operation": pluginapi.StringEnumProperty("...", []string{"a", "b"}),
            "count": pluginapi.WithMinMax(pluginapi.IntegerProperty("..."), 1, 10),
            // 20+ more lines...
        }, []string{"operation"}),
    )
}
```

**After** (new way):
```go
func (t *MyTool) Definition() pluginapi.Tool {
    tool, _ := t.GetToolDefinition()
    return tool
}
```

Parameters move to `plugin.yaml`:
```yaml
tool_definition:
  description: "My tool description"
  parameters:
    - name: operation
      type: string
      enum: [a, b]
      required: true
    - name: count
      type: integer
      min: 1
      max: 10
```

## Migration Guide

### Step 1: Add Tool Definition to plugin.yaml

Add a `tool_definition` section to your existing `plugin.yaml`:

```yaml
name: my-plugin
version: 1.0.0
# ... existing fields ...

tool_definition:
  description: "Your tool description"
  parameters:
    - name: operation
      type: string
      description: "Operation to perform"
      required: true
      enum:
        - create
        - list
        - delete

    - name: name
      type: string
      description: "Resource name"
      required: false

    - name: count
      type: integer
      description: "Number of items"
      required: false
      min: 1
      max: 100
      default: 10
```

### Step 2: Simplify Definition() Method

Replace your manual parameter building:

```go
// Old way - DELETE THIS
func (t *MyTool) Definition() pluginapi.Tool {
    return pluginapi.NewTool(
        "my-tool",
        "Description",
        pluginapi.ObjectProperty("", map[string]interface{}{
            // 30+ lines of parameters...
        }, []string{"operation"}),
    )
}

// New way - USE THIS
func (t *MyTool) Definition() pluginapi.Tool {
    tool, err := t.GetToolDefinition()
    if err != nil {
        // Fallback in case of YAML parsing error
        return pluginapi.Tool{
            Name:        "my-tool",
            Description: "Description",
            Parameters:  map[string]interface{}{},
        }
    }
    return tool
}
```

### Step 3: Use Settings API (Optional)

If you have custom configuration handling, migrate to Settings API:

```go
// Old way - custom file handling
func (t *MyTool) loadSettings() (*Settings, error) {
    data, err := os.ReadFile("settings.json")
    // ... manual JSON parsing ...
}

// New way - Settings API
func (t *MyTool) getConfig() string {
    sm := t.Settings()
    value, _ := sm.GetString("api_key")
    return value
}
```

### Step 4: Use Template Rendering (Optional)

If your plugin serves web pages:

```go
// Old way - string concatenation
html := "<html><body>" + data + "</body></html>"

// New way - template files
//go:embed templates
var templatesFS embed.FS

html, err := pluginapi.RenderTemplate(templatesFS, "templates/page.html", data)
```

## YAML Tool Definitions

### Supported Parameter Types

| Type | Description | Example |
|------|-------------|---------|
| `string` | Text value | `"hello"` |
| `integer` | Whole number | `42` |
| `number` | Decimal number | `3.14` |
| `boolean` | True/false | `true` |
| `enum` | Fixed set of values | Type `string` with `enum` field |
| `array` | List of values | `["a", "b", "c"]` |
| `object` | Nested object | `{"key": "value"}` |

### Parameter Schema

```yaml
parameters:
  - name: param_name          # Required: parameter name
    type: string              # Required: parameter type
    description: "..."        # Required: parameter description
    required: true            # Optional: is this parameter required?
    default: "value"          # Optional: default value

    # String-specific (type: string)
    enum: [a, b, c]          # Optional: allowed values
    min_length: 1             # Optional: minimum length
    max_length: 100           # Optional: maximum length
    pattern: "^[a-z]+$"       # Optional: regex pattern

    # Number-specific (type: integer or number)
    min: 1                    # Optional: minimum value
    max: 100                  # Optional: maximum value

    # Array-specific (type: array)
    items:                    # Required for arrays
      type: string            # Type of array elements

    # Object-specific (type: object)
    properties:               # Required for objects
      nested_param:
        type: string
        description: "..."
```

### Complete Example

```yaml
tool_definition:
  description: "Complete example with all parameter types"
  parameters:
    # String parameter
    - name: message
      type: string
      description: "A text message"
      required: true
      min_length: 1
      max_length: 500

    # Enum parameter (string with limited values)
    - name: operation
      type: string
      description: "Operation to perform"
      required: true
      enum:
        - create
        - update
        - delete
      default: create

    # Integer parameter with constraints
    - name: count
      type: integer
      description: "Number of items"
      required: false
      min: 1
      max: 100
      default: 10

    # Number parameter (decimal)
    - name: threshold
      type: number
      description: "Threshold value"
      required: false
      min: 0.0
      max: 1.0
      default: 0.5

    # Boolean parameter
    - name: verbose
      type: boolean
      description: "Enable verbose output"
      required: false
      default: false

    # Array parameter
    - name: tags
      type: array
      description: "List of tags"
      required: false
      items:
        type: string

    # Object parameter
    - name: config
      type: object
      description: "Configuration object"
      required: false
      properties:
        host:
          type: string
          description: "Server host"
          required: true
        port:
          type: integer
          description: "Server port"
          required: true
          min: 1024
          max: 65535
```

## Settings API

The Settings API provides simple key-value storage for plugin configuration and state.

### Basic Usage

```go
// Get the settings manager (available after agent context is set)
sm := t.Settings()

// Store values (any JSON-serializable type)
sm.Set("api_key", "sk-123456")
sm.Set("max_retries", 5)
sm.Set("debug_mode", true)
sm.Set("last_sync", time.Now().Unix())

// Retrieve values (type-safe getters)
apiKey, err := sm.GetString("api_key")      // "sk-123456"
retries, err := sm.GetInt("max_retries")    // 5
debug, err := sm.GetBool("debug_mode")      // true
lastSync, err := sm.GetFloat("last_sync")   // 1234567890.0

// Generic getter (returns interface{})
value, err := sm.Get("api_key")

// Get all settings
allSettings, err := sm.GetAll()  // map[string]interface{}

// Delete a setting
err := sm.Delete("api_key")
```

### Storing Complex Data

Store JSON-serializable structs:

```go
type Config struct {
    Host string
    Port int
}

// Save
config := Config{Host: "localhost", Port: 8080}
configJSON, _ := json.Marshal(config)
sm.Set("config", string(configJSON))

// Load
configJSON, _ := sm.GetString("config")
var config Config
json.Unmarshal([]byte(configJSON), &config)
```

### Features

- **Thread-safe**: Concurrent access is protected with mutexes
- **Atomic writes**: Uses temp file + rename pattern to prevent corruption
- **Auto-persistence**: Changes are automatically saved to disk
- **Agent-isolated**: Each agent has separate settings storage

### Storage Location

Settings are stored in:
```
agents/<agent-name>/plugins/<plugin-name>/settings.json
```

Example:
```
agents/my-agent/plugins/my-plugin/settings.json
```

## Template Rendering API

Serve beautiful web pages from your plugin with Go's `html/template` engine.

### Basic Usage

```go
package main

import (
    "embed"
    "github.com/johnjallday/ori-agent/pluginapi"
)

// Embed your templates
//go:embed templates
var templatesFS embed.FS

// Implement WebPageProvider interface
func (t *MyTool) GetWebPages() []string {
    return []string{"dashboard", "settings"}
}

func (t *MyTool) ServeWebPage(path string, query map[string]string) (string, string, error) {
    switch path {
    case "dashboard":
        return t.serveDashboard(query)
    case "settings":
        return t.serveSettings(query)
    default:
        return "", "", fmt.Errorf("page not found: %s", path)
    }
}

func (t *MyTool) serveDashboard(query map[string]string) (string, string, error) {
    // Prepare data
    data := map[string]interface{}{
        "Title": "My Dashboard",
        "Items": []string{"Item 1", "Item 2", "Item 3"},
        "Count": 3,
    }

    // Render template
    html, err := pluginapi.RenderTemplate(templatesFS, "templates/dashboard.html", data)
    if err != nil {
        return "", "", err
    }

    return html, "text/html; charset=utf-8", nil
}
```

### Template Syntax

Templates use Go's `html/template` syntax:

```html
<!DOCTYPE html>
<html>
<head>
    <title>{{.Title}}</title>
</head>
<body>
    <h1>{{.Title}}</h1>

    <!-- Conditionals -->
    {{if .Items}}
        <p>Found {{.Count}} items</p>
    {{else}}
        <p>No items found</p>
    {{end}}

    <!-- Loops -->
    <ul>
    {{range .Items}}
        <li>{{.}}</li>
    {{end}}
    </ul>

    <!-- Nested data -->
    <p>User: {{.User.Name}} ({{.User.Email}})</p>
</body>
</html>
```

### Features

- **Automatic XSS protection**: HTML is escaped by default
- **Template caching**: Templates are parsed once and cached
- **Data binding**: Pass any struct or map to your template
- **Fast rendering**: Optimized for performance

### URL Pattern

Your web pages are accessible at:
```
http://localhost:8080/api/plugins/<plugin-name>/pages/<page-path>
```

Example:
```
http://localhost:8080/api/plugins/my-plugin/pages/dashboard
```

### Query Parameters

Access URL query parameters in `ServeWebPage`:

```go
func (t *MyTool) ServeWebPage(path string, query map[string]string) (string, string, error) {
    page := query["page"]    // From ?page=2
    filter := query["filter"] // From ?filter=active

    data := map[string]interface{}{
        "Page": page,
        "Filter": filter,
    }

    html, err := pluginapi.RenderTemplate(templatesFS, "templates/list.html", data)
    return html, "text/html", err
}
```

## Before & After Examples

### Example 1: Tool Definition

**Before** (~40 lines):
```go
func (m *MusicProjectManagerTool) Definition() pluginapi.Tool {
    return pluginapi.NewTool(
        "ori-music-project-manager",
        "Manage Reaper DAW music projects (.RPP files). Use this for creating new music projects, opening existing Reaper projects in the DAW, opening project locations in Finder, scanning for project files, listing projects, filtering by BPM, and renaming projects. Examples: 'create project mash', 'open project beats', 'open beats in finder', 'show me my 140 BPM projects', 'rename China girl EDM to okok'",
        pluginapi.ObjectProperty("", map[string]interface{}{
            "operation": pluginapi.StringEnumProperty(
                "Music project operation: create new Reaper project, open existing project in Reaper DAW, reveal project in Finder file browser, scan for .RPP files, list projects, filter by name/BPM, or rename an existing project",
                []string{"create_project", "scan", "list_projects", "open_project", "open_in_finder", "filter_project", "rename_project"},
            ),
            "name":     pluginapi.StringProperty("Project name for creating new Reaper projects, filtering existing ones, finding projects to open in Finder, or the current name of a project to rename (e.g., 'mash', 'beats', 'Rich Daddy', 'China girl EDM')"),
            "new_name": pluginapi.StringProperty("New name for the project when using rename_project operation (e.g., 'okok')"),
            "path":     pluginapi.StringProperty("Full file path to a Reaper project file (.RPP) to open in Reaper DAW or reveal in Finder (e.g., '/Users/name/Music/Projects/song.RPP')"),
            "bpm": pluginapi.WithMinMax(
                pluginapi.IntegerProperty("BPM for the project (optional for create_project, exact BPM for filter_project)"),
                30,
                300,
            ),
            "min_bpm": pluginapi.WithMinMax(
                pluginapi.IntegerProperty("Minimum BPM for filter_project (optional)"),
                30,
                300,
            ),
            "max_bpm": pluginapi.WithMinMax(
                pluginapi.IntegerProperty("Maximum BPM for filter_project (optional)"),
                30,
                300,
            ),
        }, []string{"operation"}),
    )
}
```

**After** (~10 lines):
```go
func (m *MusicProjectManagerTool) Definition() pluginapi.Tool {
    tool, err := m.GetToolDefinition()
    if err != nil {
        return pluginapi.Tool{
            Name:        "ori-music-project-manager",
            Description: "Manage Reaper DAW music projects",
            Parameters:  map[string]interface{}{},
        }
    }
    return tool
}
```

With parameters in `plugin.yaml`:
```yaml
tool_definition:
  description: "Manage Reaper DAW music projects (.RPP files)..."
  parameters:
    - name: operation
      type: string
      enum: [create_project, scan, list_projects, ...]
      required: true
    - name: name
      type: string
      required: false
    # ... more parameters ...
```

**Savings**: 70% less code, easier to maintain

### Example 2: Settings Storage

**Before** (~50 lines):
```go
func (m *MusicProjectManagerTool) loadSettings() (*types.Settings, error) {
    currentAgent, err := m.getCurrentAgentFromFile()
    if err != nil {
        return m.getDefaultSettings()
    }

    settingsPath := filepath.Join(".", "agents", currentAgent, "music-project-manager_settings.json")
    data, err := os.ReadFile(settingsPath)
    if err != nil {
        return m.getDefaultSettings()
    }

    var settings types.Settings
    if err := json.Unmarshal(data, &settings); err != nil {
        return m.getDefaultSettings()
    }

    return &settings, nil
}

func (m *MusicProjectManagerTool) saveSettings(settings *types.Settings) error {
    currentAgent, err := m.getCurrentAgentFromFile()
    if err != nil {
        return err
    }

    settingsPath := filepath.Join(".", "agents", currentAgent, "music-project-manager_settings.json")
    data, err := json.MarshalIndent(settings, "", "  ")
    if err != nil {
        return err
    }

    return os.WriteFile(settingsPath, data, 0644)
}
```

**After** (~5 lines):
```go
func (m *MusicProjectManagerTool) getSetting(key string) string {
    sm := m.Settings()
    value, _ := sm.GetString(key)
    return value
}

func (m *MusicProjectManagerTool) saveSetting(key string, value interface{}) error {
    return m.Settings().Set(key, value)
}
```

**Savings**: 90% less code, thread-safe, atomic writes

### Example 3: Web Page Templates

**Before** (string concatenation):
```go
func getMarketplaceTemplate() string {
    return `<!DOCTYPE html>
<html><head><title>Marketplace</title></head>
<body><h1>Scripts</h1>` + scriptsHTML + `</body></html>`
}

func generateScriptCards(scripts []Script) string {
    html := ""
    for _, script := range scripts {
        html += fmt.Sprintf(`<div class="card">
            <h3>%s</h3>
            <p>%s</p>
        </div>`, script.Name, script.Description)
    }
    return html
}
```

**After** (template files):
```html
<!-- templates/marketplace.html -->
<!DOCTYPE html>
<html>
<head><title>Marketplace</title></head>
<body>
    <h1>Scripts</h1>
    {{range .Scripts}}
    <div class="card">
        <h3>{{.Name}}</h3>
        <p>{{.Description}}</p>
    </div>
    {{end}}
</body>
</html>
```

```go
data := map[string]interface{}{
    "Scripts": scripts,
}
html, _ := pluginapi.RenderTemplate(templatesFS, "templates/marketplace.html", data)
```

**Benefits**: Separation of concerns, easier to style, safer (XSS protection)

## Breaking Changes

### No Breaking Changes for Existing Plugins

The optimization APIs are **opt-in**. Existing plugins continue to work without modification.

### If You Choose to Migrate

1. **Tool Definition**: `Definition()` now returns from YAML instead of manual building
   - **Impact**: Change is in plugin code only
   - **Fix**: Implement new `Definition()` pattern shown above

2. **Settings Location**: Settings now stored in `agents/<agent>/plugins/<plugin>/settings.json`
   - **Impact**: Previous custom settings files won't be automatically migrated
   - **Fix**: Read old settings and save via Settings API, or document migration in README

3. **Template Path**: Templates must be embedded via `go:embed`
   - **Impact**: Templates must be in plugin directory at build time
   - **Fix**: Use `//go:embed templates` directive

## Troubleshooting

### YAML Tool Definition Not Loading

**Problem**: `GetToolDefinition()` returns an error

**Solutions**:
1. Check that `plugin.yaml` has a `tool_definition` section
2. Verify YAML syntax (use a YAML validator)
3. Ensure all required fields are present (name, type, description)
4. Check that parameter types are valid (string, integer, number, boolean, enum, array, object)

**Debug**:
```go
tool, err := t.GetToolDefinition()
if err != nil {
    fmt.Printf("Tool definition error: %v\n", err)
    // This will show what's wrong with the YAML
}
```

### Settings Not Persisting

**Problem**: Settings are lost after restart

**Solutions**:
1. Ensure agent context is set before accessing settings
2. Check that `agents/<agent-name>/plugins/<plugin-name>/` directory exists
3. Verify file permissions for the agent directory
4. Make sure you're calling `Set()`, not just modifying a map

**Debug**:
```go
sm := t.Settings()
if sm == nil {
    fmt.Println("Settings manager is nil - agent context not set")
}

err := sm.Set("test", "value")
if err != nil {
    fmt.Printf("Failed to save: %v\n", err)
}
```

### Template Rendering Fails

**Problem**: Template fails to render

**Solutions**:
1. Check that template path is correct (`templates/page.html` not `/templates/page.html`)
2. Verify template syntax (test with simple template first)
3. Ensure data matches template expectations
4. Check for nil values in template data

**Debug**:
```go
html, err := pluginapi.RenderTemplate(templatesFS, "templates/page.html", data)
if err != nil {
    fmt.Printf("Template error: %v\n", err)
    fmt.Printf("Data: %+v\n", data)
}
```

### Enum Values Not Working

**Problem**: String parameters with enum values don't show enum constraint

**Solution**: Use `type: string` with `enum` field (NOT `type: enum`):

```yaml
# Correct
parameters:
  - name: operation
    type: string
    enum: [create, update, delete]

# Incorrect
parameters:
  - name: operation
    type: enum  # This doesn't work!
    enum: [create, update, delete]
```

### Common Mistakes

1. **Forgetting to embed templates**:
   ```go
   //go:embed templates  // Don't forget this!
   var templatesFS embed.FS
   ```

2. **Wrong template path**:
   ```go
   // Correct
   pluginapi.RenderTemplate(templatesFS, "templates/page.html", data)

   // Wrong (leading slash)
   pluginapi.RenderTemplate(templatesFS, "/templates/page.html", data)
   ```

3. **Not handling nil Settings**:
   ```go
   // Safe
   sm := t.Settings()
   if sm != nil {
       value, _ := sm.GetString("key")
   }

   // Unsafe (can panic if no agent context)
   value, _ := t.Settings().GetString("key")
   ```

4. **Parameter type mismatch**:
   ```yaml
   # JSON unmarshals numbers as float64
   - name: count
     type: integer  # Correct
     # NOT: type: int (invalid type)
   ```

## Examples

See complete working examples in:
- `example_plugins/minimal/` - Basic plugin with YAML tool definitions and Settings API
- `example_plugins/webapp/` - Advanced plugin with web page templates

## Need Help?

- Check the examples in `example_plugins/`
- Review migrated plugins: `plugins/ori-reaper/`, `plugins/ori-music-project-manager/`
- File an issue: https://github.com/johnjallday/ori-agent/issues
