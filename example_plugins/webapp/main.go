package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/go-plugin"
	"github.com/johnjallday/ori-agent/pluginapi"
)

//go:embed plugin.yaml
var configYAML string

//go:embed templates
var templatesFS embed.FS

// Item represents a simple item in our list
type Item struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// webappTool demonstrates web page templates and the Template Rendering API
type webappTool struct {
	pluginapi.BasePlugin
}

// Ensure compile-time interface conformance
var _ pluginapi.PluginTool = (*webappTool)(nil)
var _ pluginapi.VersionedTool = (*webappTool)(nil)
var _ pluginapi.AgentAwareTool = (*webappTool)(nil)
var _ pluginapi.InitializationProvider = (*webappTool)(nil)
var _ pluginapi.WebPageProvider = (*webappTool)(nil)

// Definition returns the tool definition from plugin.yaml
func (t *webappTool) Definition() pluginapi.Tool {
	tool, err := t.GetToolDefinition()
	if err != nil {
		return pluginapi.Tool{
			Name:        "webapp-plugin",
			Description: "Example web app plugin",
			Parameters:  map[string]interface{}{},
		}
	}
	return tool
}

// Call executes the tool with the given arguments
func (t *webappTool) Call(ctx context.Context, args string) (string, error) {
	var params struct {
		Operation       string `json:"operation"`
		ItemName        string `json:"item_name"`
		ItemDescription string `json:"item_description"`
	}

	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	switch params.Operation {
	case "add_item":
		return t.addItem(params.ItemName, params.ItemDescription)
	case "list_items":
		return t.listItems()
	case "delete_item":
		return t.deleteItem(params.ItemName)
	case "open_dashboard":
		return t.openDashboard()
	default:
		return "", fmt.Errorf("unknown operation: %s", params.Operation)
	}
}

// addItem adds a new item to the list
func (t *webappTool) addItem(name, description string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("item_name is required")
	}

	sm := t.Settings()
	if sm == nil {
		return "", fmt.Errorf("settings not available")
	}

	// Get existing items
	items := t.getItems()

	// Add new item
	items = append(items, Item{
		Name:        name,
		Description: description,
	})

	// Save items
	if err := t.saveItems(items); err != nil {
		return "", fmt.Errorf("failed to save items: %w", err)
	}

	return fmt.Sprintf("Added item: %s", name), nil
}

// listItems returns a list of all items
func (t *webappTool) listItems() (string, error) {
	items := t.getItems()

	if len(items) == 0 {
		return "No items found. Add some items first!", nil
	}

	result := fmt.Sprintf("Found %d items:\n\n", len(items))
	for i, item := range items {
		result += fmt.Sprintf("%d. %s", i+1, item.Name)
		if item.Description != "" {
			result += fmt.Sprintf(" - %s", item.Description)
		}
		result += "\n"
	}

	return result, nil
}

// deleteItem removes an item from the list
func (t *webappTool) deleteItem(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("item_name is required")
	}

	items := t.getItems()
	found := false
	newItems := []Item{}

	for _, item := range items {
		if item.Name != name {
			newItems = append(newItems, item)
		} else {
			found = true
		}
	}

	if !found {
		return fmt.Sprintf("Item not found: %s", name), nil
	}

	if err := t.saveItems(newItems); err != nil {
		return "", fmt.Errorf("failed to save items: %w", err)
	}

	return fmt.Sprintf("Deleted item: %s", name), nil
}

// openDashboard returns the URL to the web dashboard
func (t *webappTool) openDashboard() (string, error) {
	return "🌐 Open the dashboard at:\nhttp://localhost:8080/api/plugins/webapp-plugin/pages/dashboard", nil
}

// getItems retrieves items from settings
func (t *webappTool) getItems() []Item {
	sm := t.Settings()
	if sm == nil {
		return []Item{}
	}

	itemsJSON, err := sm.GetString("items")
	if err != nil || itemsJSON == "" {
		return []Item{}
	}

	var items []Item
	if err := json.Unmarshal([]byte(itemsJSON), &items); err != nil {
		return []Item{}
	}

	return items
}

// saveItems stores items to settings
func (t *webappTool) saveItems(items []Item) error {
	sm := t.Settings()
	if sm == nil {
		return fmt.Errorf("settings not available")
	}

	itemsJSON, err := json.Marshal(items)
	if err != nil {
		return err
	}

	return sm.Set("items", string(itemsJSON))
}

// GetWebPages returns the list of available web pages
func (t *webappTool) GetWebPages() []string {
	return []string{"dashboard"}
}

// ServeWebPage serves the requested web page using template rendering
func (t *webappTool) ServeWebPage(path string, query map[string]string) (string, string, error) {
	if path != "dashboard" {
		return "", "", fmt.Errorf("page not found: %s", path)
	}

	// Get items
	items := t.getItems()

	// Get items per page setting
	sm := t.Settings()
	itemsPerPage := 10
	if sm != nil {
		itemsPerPage, _ = sm.GetInt("items_per_page")
		if itemsPerPage == 0 {
			itemsPerPage = 10
		}
	}

	// Prepare template data
	data := map[string]interface{}{
		"Title":        "WebApp Plugin Dashboard",
		"Subtitle":     "Example plugin demonstrating the Template Rendering API",
		"TotalItems":   len(items),
		"ItemsPerPage": itemsPerPage,
		"Items":        items,
	}

	// Render template using the Template Rendering API
	html, err := pluginapi.RenderTemplate(templatesFS, "templates/dashboard.html", data)
	if err != nil {
		return "", "", fmt.Errorf("failed to render template: %w", err)
	}

	return html, "text/html; charset=utf-8", nil
}

// GetRequiredConfig returns configuration requirements
func (t *webappTool) GetRequiredConfig() []pluginapi.ConfigVariable {
	return t.GetConfigFromYAML()
}

// ValidateConfig validates the provided configuration
func (t *webappTool) ValidateConfig(config map[string]interface{}) error {
	if itemsPerPage, ok := config["items_per_page"]; ok {
		var ipp int
		switch v := itemsPerPage.(type) {
		case float64:
			ipp = int(v)
		case int:
			ipp = v
		default:
			return fmt.Errorf("items_per_page must be a number")
		}

		if ipp < 1 || ipp > 100 {
			return fmt.Errorf("items_per_page must be between 1 and 100")
		}
	}

	return nil
}

// InitializeWithConfig stores the configuration
func (t *webappTool) InitializeWithConfig(config map[string]interface{}) error {
	sm := t.Settings()
	if sm == nil {
		return fmt.Errorf("settings manager not available")
	}

	for key, value := range config {
		if err := sm.Set(key, value); err != nil {
			return fmt.Errorf("failed to store config %s: %w", key, err)
		}
	}

	return nil
}

func main() {
	// Parse plugin config from embedded YAML
	config := pluginapi.ReadPluginConfig(configYAML)

	// Create plugin instance
	tool := &webappTool{
		BasePlugin: pluginapi.NewBasePlugin(
			"webapp-plugin",
			config.Version,
			config.Requirements.MinOriVersion,
			"",
			"v1",
		),
	}

	// Set plugin config for YAML-based features
	tool.SetPluginConfig(&config)

	// Set metadata from config
	if metadata, err := config.ToMetadata(); err == nil {
		tool.SetMetadata(metadata)
	}

	// Serve the plugin
	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: pluginapi.Handshake,
		Plugins: map[string]plugin.Plugin{
			"tool": &pluginapi.ToolRPCPlugin{Impl: tool},
		},
		GRPCServer: plugin.DefaultGRPCServer,
	})
}
