package main

//go:generate ../../bin/ori-plugin-gen -yaml=plugin.yaml -output=webapp_generated.go

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/oriagent/ori-pluginapi"
)

// Item represents a simple item in our list
type Item struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// WebappPluginTool demonstrates web page templates and the Template Rendering API
// Note: Compile-time interface check is in webapp_generated.go
type WebappPluginTool struct {
	pluginapi.BasePlugin
}

// Operation handlers

func handleAddItem(ctx context.Context, t *WebappPluginTool, params *Params) (string, error) {
	return t.addItem(params.ItemName, params.ItemDescription)
}

func handleListItems(ctx context.Context, t *WebappPluginTool, params *Params) (string, error) {
	return t.listItems()
}

func handleDeleteItem(ctx context.Context, t *WebappPluginTool, params *Params) (string, error) {
	return t.deleteItem(params.ItemName)
}

func handleOpenDashboard(ctx context.Context, t *WebappPluginTool, params *Params) (string, error) {
	return t.openDashboard()
}

// addItem adds a new item to the list
func (t *WebappPluginTool) addItem(name, description string) (string, error) {
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
func (t *WebappPluginTool) listItems() (string, error) {
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
func (t *WebappPluginTool) deleteItem(name string) (string, error) {
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
func (t *WebappPluginTool) openDashboard() (string, error) {
	return "🌐 Open the dashboard at:\nhttp://localhost:8080/api/plugins/webapp-plugin/pages/dashboard", nil
}

// getItems retrieves items from settings
func (t *WebappPluginTool) getItems() []Item {
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
func (t *WebappPluginTool) saveItems(items []Item) error {
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

// --- Web page handler ---
// The naming convention serve{PascalCase}Page is auto-wired by the generator

func serveDashboardPage(t *WebappPluginTool, query map[string]string) (string, string, error) {
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
	html, err := pluginapi.RenderTemplate(assetsFS, "templates/dashboard.html", data)
	if err != nil {
		return "", "", fmt.Errorf("failed to render template: %w", err)
	}

	return html, "text/html; charset=utf-8", nil
}

func main() {
	pluginapi.ServeGRPCPlugin(&WebappPluginTool{}, configYAML)
}
