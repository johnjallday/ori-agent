# Plugin Templates

## Minimal Plugin

The simplest working plugin structure.

**main.go:**
```go
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/oriagent/ori-pluginapi"
)

//go:embed plugin.yaml
var configYAML string

type MyPluginTool struct {
	pluginapi.BasePlugin
}

type Params struct {
	Input string `json:"input"`
}

func (t *MyPluginTool) Call(ctx context.Context, args string) (string, error) {
	var params Params
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	result := fmt.Sprintf("Processed: %s", params.Input)
	return result, nil
}

func main() {
	pluginapi.ServePlugin(&MyPluginTool{}, configYAML)
}
```

**plugin.yaml:**
```yaml
name: my-plugin
version: 1.0.0
description: Brief description of what this plugin does
license: MIT
repository: https://github.com/youruser/my-plugin

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
  min_ori_version: "0.0.9"
  api_version: "v1"

tool_definition:
  description: "What this tool does and when to use it"
  parameters:
    - name: input
      type: string
      description: "Parameter description"
      required: true
```

**go.mod:**
```
module my-plugin

go 1.21

require github.com/oriagent/ori-pluginapi v1.0.1
```

---

## Plugin with Multiple Operations

For plugins with create/list/delete style operations.

**main.go:**
```go
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/oriagent/ori-pluginapi"
)

//go:embed plugin.yaml
var configYAML string

type MyPluginTool struct {
	pluginapi.BasePlugin
}

type Params struct {
	Operation string `json:"operation"`
	Name      string `json:"name,omitempty"`
	Value     string `json:"value,omitempty"`
}

func (t *MyPluginTool) Call(ctx context.Context, args string) (string, error) {
	var params Params
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	switch params.Operation {
	case "create":
		return fmt.Sprintf("Created %s", params.Name), nil
	case "list":
		return "List of items...", nil
	case "delete":
		return fmt.Sprintf("Deleted %s", params.Name), nil
	default:
		return "", fmt.Errorf("unknown operation: %s", params.Operation)
	}
}

func main() {
	pluginapi.ServePlugin(&MyPluginTool{}, configYAML)
}
```

**plugin.yaml:**
```yaml
name: my-crud-plugin
version: 1.0.0
description: Plugin with create, list, and delete operations
license: MIT
repository: https://github.com/youruser/my-crud-plugin

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
  min_ori_version: "0.0.9"
  api_version: "v1"

tool_definition:
  description: "Manage items with create, list, and delete operations"
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
      description: "Item name"
      required: false
    - name: value
      type: string
      description: "Item value"
      required: false

  # Operations section defines per-operation required parameters
  # This enables proper validation - e.g., 'name' is only required for create/delete
  operations:
    create:
      parameters:
        - name: name
          type: string
          description: "Item name to create"
          required: true
        - name: value
          type: string
          description: "Item value"
          required: true
    list:
      parameters: []
    delete:
      parameters:
        - name: name
          type: string
          description: "Item name to delete"
          required: true
```

---

## Plugin with External API

For plugins that call HTTP APIs.

**main.go:**
```go
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/oriagent/ori-pluginapi"
)

//go:embed plugin.yaml
var configYAML string

type APIPluginTool struct {
	pluginapi.BasePlugin
	client *http.Client
}

type Params struct {
	Query string `json:"query"`
}

func (t *APIPluginTool) Call(ctx context.Context, args string) (string, error) {
	var params Params
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	if t.client == nil {
		t.client = &http.Client{Timeout: 30 * time.Second}
	}

	// Get API key from settings
	var apiKey string
	if sm := t.Settings(); sm != nil {
		apiKey, _ = sm.GetString("api_key")
	}

	url := fmt.Sprintf("https://api.example.com/search?q=%s", params.Query)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	return string(body), nil
}

func main() {
	pluginapi.ServePlugin(&APIPluginTool{}, configYAML)
}
```

---

## Plugin with Settings

Use the Settings API for configuration storage.

**In Call() method:**
```go
func (t *MyPluginTool) Call(ctx context.Context, args string) (string, error) {
	sm := t.Settings()
	if sm == nil {
		return "", fmt.Errorf("settings not available")
	}

	// Read settings
	apiKey, _ := sm.GetString("api_key")
	timeout, _ := sm.GetInt("timeout_seconds")
	debug, _ := sm.GetBool("debug_mode")

	// Write settings
	_ = sm.Set("last_run", time.Now().Format(time.RFC3339))

	// Your logic here...
}
```

**plugin.yaml config section:**
```yaml
config:
  variables:
    - key: api_key
      name: API Key
      description: Your API key
      type: password
      required: true

    - key: timeout_seconds
      name: Timeout
      description: Request timeout in seconds
      type: int
      required: false
      default_value: "30"
```

---

## Plugin with Web UI

For plugins that serve web pages.

**main.go additions:**
```go
import (
	"embed"
	// ... other imports
)

//go:embed templates/*
var assetsFS embed.FS

type WebPluginTool struct {
	pluginapi.BasePlugin
}

func (t *WebPluginTool) GetWebPages() []string {
	return []string{"dashboard", "settings"}
}

func (t *WebPluginTool) ServeWebPage(path string, query map[string]string) (string, string, error) {
	switch path {
	case "dashboard":
		data := map[string]interface{}{
			"Title": "Dashboard",
			"Items": []string{"Item 1", "Item 2"},
		}
		html, err := pluginapi.RenderTemplate(assetsFS, "templates/dashboard.html", data)
		if err != nil {
			return "", "", err
		}
		return html, "text/html; charset=utf-8", nil
	default:
		return "", "", fmt.Errorf("page not found: %s", path)
	}
}
```

**templates/dashboard.html:**
```html
<!DOCTYPE html>
<html>
<head>
    <title>{{.Title}}</title>
</head>
<body>
    <h1>{{.Title}}</h1>
    <ul>
        {{range .Items}}<li>{{.}}</li>{{end}}
    </ul>
</body>
</html>
```

**plugin.yaml pages section:**
```yaml
pages:
  - path: dashboard
    title: Dashboard
```

---

## Structured Results

Return data that renders nicely in the UI.

**Table:**
```go
result := pluginapi.NewTableResult(
	[]string{"Name", "Value", "Status"},
	[][]string{
		{"Item 1", "100", "Active"},
		{"Item 2", "200", "Inactive"},
	},
)
return result.ToJSON()
```

**List:**
```go
result := pluginapi.NewListResult([]string{
	"First item",
	"Second item",
})
return result.ToJSON()
```

---

## Parameter Types Reference

### tool_definition.parameters

Valid types for tool parameters (used by LLM):

| Type | Description | Example |
|------|-------------|---------|
| `string` | Text value | `type: string` |
| `integer` | Whole number (**NOT `int`**) | `type: integer` |
| `number` | Decimal number | `type: number` |
| `boolean` | True/false | `type: boolean` |
| `enum` | String with enum values | `type: string` + `enum: [a, b, c]` |
| `array` | List of items | `type: array` + `items: {type: string}` |
| `object` | Nested object | `type: object` + `properties: {...}` |

**Important:** Use `integer`, NOT `int` for tool parameters. Using `int` will cause empty Parameters.

### config.variables

Valid types for config variables (used by settings UI):

| Type | Description |
|------|-------------|
| `string` | Text input |
| `int` | Integer input (note: `int` is valid here, unlike tool params) |
| `bool` | Checkbox |
| `password` | Masked text input |
| `dirpath` | Directory path picker |
| `filepath` | File path picker |

---

## Required plugin.yaml Fields

Every plugin.yaml must have these fields:

```yaml
name: plugin-name          # Required: plugin identifier
version: 1.0.0             # Required: semver format
description: What it does  # Required: brief description
license: MIT               # Required: license type
repository: https://...    # Required: valid URL

maintainers:               # Required: at least one
  - name: Your Name
    email: you@example.com

platforms:                 # Required: at least one
  - os: darwin
    architectures: [amd64, arm64]
```
