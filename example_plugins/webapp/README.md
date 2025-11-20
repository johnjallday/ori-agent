# WebApp Plugin Example

This example demonstrates **advanced plugin features** including:

1. **Template Rendering API** - Serve beautiful web pages from your plugin
2. **Settings API** - Store and retrieve plugin data
3. **WebPageProvider Interface** - Custom web UI integration

## Key Features

### 1. Template Rendering
Render HTML templates with data binding:

```go
// Embed templates
//go:embed templates
var templatesFS embed.FS

// Render with data
html, err := pluginapi.RenderTemplate(templatesFS, "templates/dashboard.html", data)
```

### 2. Custom Web Pages
Serve custom web pages through ori-agent:

```go
func (t *webappTool) GetWebPages() []string {
    return []string{"dashboard"}
}

func (t *webappTool) ServeWebPage(path string, query map[string]string) (string, string, error) {
    // Prepare data
    data := map[string]interface{}{
        "Title": "My Dashboard",
        "Items": items,
    }

    // Render template
    html, err := pluginapi.RenderTemplate(templatesFS, "templates/dashboard.html", data)
    return html, "text/html", err
}
```

URL: `http://localhost:8080/api/plugins/webapp-plugin/pages/dashboard`

### 3. Data Persistence
Store complex data structures using Settings API:

```go
// Save items as JSON
itemsJSON, _ := json.Marshal(items)
sm.Set("items", string(itemsJSON))

// Load items
itemsJSON, _ := sm.GetString("items")
json.Unmarshal([]byte(itemsJSON), &items)
```

## Template Features

The example template demonstrates:
- **Data binding** with `{{.Variable}}`
- **Conditionals** with `{{if .Items}}...{{end}}`
- **Loops** with `{{range .Items}}...{{end}}`
- **Automatic XSS protection** - HTML is escaped by default
- **Responsive design** with CSS Grid
- **Modern styling** with gradients and shadows

## Building

```bash
go build -o webapp-plugin main.go
```

## Testing

```bash
# Build the plugin
go build -o webapp-plugin main.go

# Copy to ori-agent
cp webapp-plugin ../../uploaded_plugins/

# Restart ori-agent
# Then test via chat:
# - "add item notebook with description my ideas"
# - "list items"
# - "open dashboard"
# - Visit http://localhost:8080/api/plugins/webapp-plugin/pages/dashboard
```

## Project Structure

```
webapp/
├── plugin.yaml              # Configuration and tool definition
├── main.go                  # Plugin implementation
├── templates/
│   └── dashboard.html       # Web page template
└── README.md               # This file
```

## Template Data Binding

Your template can access any data you pass:

```go
data := map[string]interface{}{
    "Title": "My Dashboard",
    "Items": items,
    "Count": len(items),
}
```

```html
<h1>{{.Title}}</h1>
<p>Total: {{.Count}}</p>
{{range .Items}}
    <div>{{.Name}}</div>
{{end}}
```

## Advanced Features

### Custom Styles
The template includes embedded CSS for a polished look:
- Gradient backgrounds
- Card-based layout
- Hover effects
- Responsive grid

### Empty States
Gracefully handle empty data:
```html
{{if .Items}}
    {{range .Items}}...{{end}}
{{else}}
    <div class="empty-state">No items yet!</div>
{{end}}
```

### Query Parameters
Access URL parameters in `ServeWebPage`:
```go
func (t *webappTool) ServeWebPage(path string, query map[string]string) (string, string, error) {
    page := query["page"]  // From ?page=2
    filter := query["filter"]  // From ?filter=active
    // Use in template rendering...
}
```

## Next Steps

- Customize the template with your own design
- Add more pages (settings, help, about)
- Implement search and filtering
- Add JavaScript interactivity
- Connect to external APIs
