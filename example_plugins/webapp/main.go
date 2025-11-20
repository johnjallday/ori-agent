package main

//go:generate ../../bin/ori-plugin-gen -yaml=plugin.yaml -output=webapp_generated.go

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"

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

// webapp_pluginTool demonstrates web page templates and the Template Rendering API
// Note: Compile-time interface check is in webapp_generated.go
type webapp_pluginTool struct {
	pluginapi.BasePlugin
}

// OperationHandler is a function that handles a specific operation
type OperationHandler func(t *webapp_pluginTool, params *WebappPluginParams) (string, error)

// operationRegistry maps operation names to their handler functions
var operationRegistry = map[string]OperationHandler{
	"add_item":       handleAddItem,
	"list_items":     handleListItems,
	"delete_item":    handleDeleteItem,
	"open_dashboard": handleOpenDashboard,
}

// Ensure compile-time interface conformance for optional interfaces
var _ pluginapi.VersionedTool = (*webapp_pluginTool)(nil)
var _ pluginapi.AgentAwareTool = (*webapp_pluginTool)(nil)
var _ pluginapi.InitializationProvider = (*webapp_pluginTool)(nil)
var _ pluginapi.WebPageProvider = (*webapp_pluginTool)(nil)

// Note: Definition() is inherited from BasePlugin, which automatically reads from plugin.yaml
// Note: Call() is auto-generated in webapp_generated.go from plugin.yaml

// Execute contains the business logic - called by the generated Call() method
func (t *webapp_pluginTool) Execute(ctx context.Context, params *WebappPluginParams) (string, error) {
	// Look up handler in registry
	handler, ok := operationRegistry[params.Operation]
	if !ok {
		return "", fmt.Errorf("unknown operation: %s. Valid operations: add_item, list_items, delete_item, open_dashboard", params.Operation)
	}

	// Execute the handler
	return handler(t, params)
}

// Operation handlers

func handleAddItem(t *webapp_pluginTool, params *WebappPluginParams) (string, error) {
	return t.addItem(params.ItemName, params.ItemDescription)
}

func handleListItems(t *webapp_pluginTool, params *WebappPluginParams) (string, error) {
	return t.listItems()
}

func handleDeleteItem(t *webapp_pluginTool, params *WebappPluginParams) (string, error) {
	return t.deleteItem(params.ItemName)
}

func handleOpenDashboard(t *webapp_pluginTool, params *WebappPluginParams) (string, error) {
	return t.openDashboard()
}

// addItem adds a new item to the list
func (t *webapp_pluginTool) addItem(name, description string) (string, error) {
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
func (t *webapp_pluginTool) listItems() (string, error) {
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
func (t *webapp_pluginTool) deleteItem(name string) (string, error) {
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
func (t *webapp_pluginTool) openDashboard() (string, error) {
	return "🌐 Open the dashboard at:\nhttp://localhost:8080/api/plugins/webapp-plugin/pages/dashboard", nil
}

// getItems retrieves items from settings
func (t *webapp_pluginTool) getItems() []Item {
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
func (t *webapp_pluginTool) saveItems(items []Item) error {
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
func (t *webapp_pluginTool) GetWebPages() []string {
	return []string{"dashboard"}
}

// ServeWebPage serves the requested web page using template rendering
func (t *webapp_pluginTool) ServeWebPage(path string, query map[string]string) (string, string, error) {
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
func (t *webapp_pluginTool) GetRequiredConfig() []pluginapi.ConfigVariable {
	return t.GetConfigFromYAML()
}

// ValidateConfig validates the provided configuration
func (t *webapp_pluginTool) ValidateConfig(config map[string]interface{}) error {
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
func (t *webapp_pluginTool) InitializeWithConfig(config map[string]interface{}) error {
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
	pluginapi.ServePlugin(&webapp_pluginTool{}, configYAML)
}
