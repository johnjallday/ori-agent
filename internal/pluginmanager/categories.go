package pluginmanager

import (
	"fmt"
	"strings"
	"sync"
)

// Standard plugin categories
const (
	CategorySystemTools    = "System Tools"
	CategoryAIML           = "AI/ML"
	CategoryDataProcessing = "Data Processing"
	CategoryUtilities      = "Utilities"
	CategoryDevelopment    = "Development"
	CategoryNetworking     = "Networking"
	CategorySecurity       = "Security"
	CategoryMultimedia     = "Multimedia"
	CategoryCustom         = "Custom"
)

// StandardCategories returns a list of all standard category names.
var StandardCategories = []string{
	CategorySystemTools,
	CategoryAIML,
	CategoryDataProcessing,
	CategoryUtilities,
	CategoryDevelopment,
	CategoryNetworking,
	CategorySecurity,
	CategoryMultimedia,
	CategoryCustom,
}

// CategoryManager manages plugin categorization and provides category-based lookups.
type CategoryManager struct {
	mu sync.RWMutex
	// pluginCategories maps plugin name to its category/categories (comma-separated)
	pluginCategories map[string]string
	// categoryPlugins maps category to list of plugin names in that category
	categoryPlugins map[string][]string
}

// NewCategoryManager creates a new CategoryManager instance.
func NewCategoryManager() *CategoryManager {
	return &CategoryManager{
		pluginCategories: make(map[string]string),
		categoryPlugins:  make(map[string][]string),
	}
}

// AssignCategory assigns a category (or comma-separated categories) to a plugin.
// This updates both the plugin->category and category->plugins mappings.
func (cm *CategoryManager) AssignCategory(pluginName, category string) error {
	if pluginName == "" {
		return fmt.Errorf("plugin name cannot be empty")
	}
	if category == "" {
		category = CategoryCustom // Default to Custom if no category provided
	}

	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Remove plugin from old categories if it exists
	if oldCategory, exists := cm.pluginCategories[pluginName]; exists {
		cm.removePluginFromCategories(pluginName, oldCategory)
	}

	// Assign new category
	cm.pluginCategories[pluginName] = category

	// Add plugin to new categories
	cm.addPluginToCategories(pluginName, category)

	return nil
}

// GetCategory returns the category (or comma-separated categories) for a plugin.
// Returns empty string if plugin has no assigned category.
func (cm *CategoryManager) GetCategory(pluginName string) string {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.pluginCategories[pluginName]
}

// GetPluginsByCategory returns all plugin names in the specified category.
func (cm *CategoryManager) GetPluginsByCategory(category string) []string {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	plugins, exists := cm.categoryPlugins[category]
	if !exists {
		return []string{}
	}

	// Return a copy to prevent external modification
	result := make([]string, len(plugins))
	copy(result, plugins)
	return result
}

// GetAllCategories returns a list of all categories that have at least one plugin assigned.
func (cm *CategoryManager) GetAllCategories() []string {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	categories := make([]string, 0, len(cm.categoryPlugins))
	for category := range cm.categoryPlugins {
		categories = append(categories, category)
	}
	return categories
}

// GetAllPluginCategories returns a map of all plugin names to their categories.
func (cm *CategoryManager) GetAllPluginCategories() map[string]string {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	// Return a copy to prevent external modification
	result := make(map[string]string, len(cm.pluginCategories))
	for name, category := range cm.pluginCategories {
		result[name] = category
	}
	return result
}

// RemovePlugin removes a plugin from all category mappings.
func (cm *CategoryManager) RemovePlugin(pluginName string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if category, exists := cm.pluginCategories[pluginName]; exists {
		cm.removePluginFromCategories(pluginName, category)
		delete(cm.pluginCategories, pluginName)
	}
}

// LoadCategories loads plugin categories from a map (used when restoring from registry).
func (cm *CategoryManager) LoadCategories(categories map[string]string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Clear existing mappings
	cm.pluginCategories = make(map[string]string)
	cm.categoryPlugins = make(map[string][]string)

	// Load new mappings
	for pluginName, category := range categories {
		cm.pluginCategories[pluginName] = category
		cm.addPluginToCategories(pluginName, category)
	}
}

// ParseCategories splits a comma-separated category string into individual categories.
func ParseCategories(categoryString string) []string {
	if categoryString == "" {
		return []string{}
	}

	parts := strings.Split(categoryString, ",")
	categories := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			categories = append(categories, trimmed)
		}
	}
	return categories
}

// IsStandardCategory checks if a category is one of the standard categories.
func IsStandardCategory(category string) bool {
	for _, std := range StandardCategories {
		if category == std {
			return true
		}
	}
	return false
}

// --- Internal helper methods ---

// removePluginFromCategories removes a plugin from its categories (internal, no lock).
func (cm *CategoryManager) removePluginFromCategories(pluginName, categoryString string) {
	categories := ParseCategories(categoryString)
	for _, category := range categories {
		if plugins, exists := cm.categoryPlugins[category]; exists {
			// Remove plugin from this category's list
			newPlugins := make([]string, 0, len(plugins))
			for _, p := range plugins {
				if p != pluginName {
					newPlugins = append(newPlugins, p)
				}
			}
			if len(newPlugins) == 0 {
				delete(cm.categoryPlugins, category)
			} else {
				cm.categoryPlugins[category] = newPlugins
			}
		}
	}
}

// addPluginToCategories adds a plugin to its categories (internal, no lock).
func (cm *CategoryManager) addPluginToCategories(pluginName, categoryString string) {
	categories := ParseCategories(categoryString)
	for _, category := range categories {
		if _, exists := cm.categoryPlugins[category]; !exists {
			cm.categoryPlugins[category] = []string{}
		}
		// Only add if not already present
		found := false
		for _, p := range cm.categoryPlugins[category] {
			if p == pluginName {
				found = true
				break
			}
		}
		if !found {
			cm.categoryPlugins[category] = append(cm.categoryPlugins[category], pluginName)
		}
	}
}
