package pluginmanager

import (
	"reflect"
	"sort"
	"testing"
)

func TestNewCategoryManager(t *testing.T) {
	cm := NewCategoryManager()
	if cm == nil {
		t.Fatal("NewCategoryManager returned nil")
	}
	if cm.pluginCategories == nil {
		t.Error("pluginCategories map not initialized")
	}
	if cm.categoryPlugins == nil {
		t.Error("categoryPlugins map not initialized")
	}
}

func TestCategoryManager_AssignCategory(t *testing.T) {
	cm := NewCategoryManager()

	tests := []struct {
		name       string
		pluginName string
		category   string
		wantErr    bool
	}{
		{
			name:       "assign system tools category",
			pluginName: "plugin1",
			category:   CategorySystemTools,
			wantErr:    false,
		},
		{
			name:       "assign multiple categories",
			pluginName: "plugin2",
			category:   "AI/ML, Data Processing",
			wantErr:    false,
		},
		{
			name:       "empty plugin name",
			pluginName: "",
			category:   CategoryUtilities,
			wantErr:    true,
		},
		{
			name:       "empty category defaults to Custom",
			pluginName: "plugin3",
			category:   "",
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := cm.AssignCategory(tt.pluginName, tt.category)
			if (err != nil) != tt.wantErr {
				t.Errorf("AssignCategory() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCategoryManager_GetCategory(t *testing.T) {
	cm := NewCategoryManager()
	cm.AssignCategory("plugin1", CategorySystemTools)
	cm.AssignCategory("plugin2", "AI/ML, Data Processing")

	tests := []struct {
		name       string
		pluginName string
		want       string
	}{
		{
			name:       "get single category",
			pluginName: "plugin1",
			want:       CategorySystemTools,
		},
		{
			name:       "get multiple categories",
			pluginName: "plugin2",
			want:       "AI/ML, Data Processing",
		},
		{
			name:       "get non-existent plugin",
			pluginName: "nonexistent",
			want:       "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cm.GetCategory(tt.pluginName)
			if got != tt.want {
				t.Errorf("GetCategory() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCategoryManager_GetPluginsByCategory(t *testing.T) {
	cm := NewCategoryManager()
	cm.AssignCategory("plugin1", CategorySystemTools)
	cm.AssignCategory("plugin2", CategorySystemTools)
	cm.AssignCategory("plugin3", CategoryAIML)

	tests := []struct {
		name     string
		category string
		want     []string
	}{
		{
			name:     "get plugins in System Tools",
			category: CategorySystemTools,
			want:     []string{"plugin1", "plugin2"},
		},
		{
			name:     "get plugins in AI/ML",
			category: CategoryAIML,
			want:     []string{"plugin3"},
		},
		{
			name:     "get plugins in empty category",
			category: CategoryNetworking,
			want:     []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cm.GetPluginsByCategory(tt.category)
			sort.Strings(got)
			sort.Strings(tt.want)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetPluginsByCategory() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCategoryManager_GetAllCategories(t *testing.T) {
	cm := NewCategoryManager()
	cm.AssignCategory("plugin1", CategorySystemTools)
	cm.AssignCategory("plugin2", CategoryAIML)
	cm.AssignCategory("plugin3", CategoryDataProcessing)

	got := cm.GetAllCategories()
	want := []string{CategorySystemTools, CategoryAIML, CategoryDataProcessing}

	sort.Strings(got)
	sort.Strings(want)

	if !reflect.DeepEqual(got, want) {
		t.Errorf("GetAllCategories() = %v, want %v", got, want)
	}
}

func TestCategoryManager_RemovePlugin(t *testing.T) {
	cm := NewCategoryManager()
	cm.AssignCategory("plugin1", CategorySystemTools)
	cm.AssignCategory("plugin2", CategorySystemTools)

	// Remove plugin1
	cm.RemovePlugin("plugin1")

	// Check plugin1 is removed
	if cat := cm.GetCategory("plugin1"); cat != "" {
		t.Errorf("Expected plugin1 to be removed, got category %s", cat)
	}

	// Check plugin2 still exists
	if cat := cm.GetCategory("plugin2"); cat != CategorySystemTools {
		t.Errorf("Expected plugin2 to still exist with category %s, got %s", CategorySystemTools, cat)
	}

	// Check category still has plugin2
	plugins := cm.GetPluginsByCategory(CategorySystemTools)
	if len(plugins) != 1 || plugins[0] != "plugin2" {
		t.Errorf("Expected System Tools to have [plugin2], got %v", plugins)
	}
}

func TestCategoryManager_LoadCategories(t *testing.T) {
	cm := NewCategoryManager()

	// Pre-load some data
	cm.AssignCategory("old-plugin", CategoryCustom)

	// Load new categories
	newCategories := map[string]string{
		"plugin1": CategorySystemTools,
		"plugin2": CategoryAIML,
		"plugin3": "Data Processing, Utilities",
	}
	cm.LoadCategories(newCategories)

	// Check old data is cleared
	if cat := cm.GetCategory("old-plugin"); cat != "" {
		t.Errorf("Expected old-plugin to be cleared, got category %s", cat)
	}

	// Check new data is loaded
	if cat := cm.GetCategory("plugin1"); cat != CategorySystemTools {
		t.Errorf("Expected plugin1 category %s, got %s", CategorySystemTools, cat)
	}
	if cat := cm.GetCategory("plugin2"); cat != CategoryAIML {
		t.Errorf("Expected plugin2 category %s, got %s", CategoryAIML, cat)
	}
	if cat := cm.GetCategory("plugin3"); cat != "Data Processing, Utilities" {
		t.Errorf("Expected plugin3 category 'Data Processing, Utilities', got %s", cat)
	}
}

func TestParseCategories(t *testing.T) {
	tests := []struct {
		name           string
		categoryString string
		want           []string
	}{
		{
			name:           "single category",
			categoryString: "System Tools",
			want:           []string{"System Tools"},
		},
		{
			name:           "multiple categories",
			categoryString: "AI/ML, Data Processing, Utilities",
			want:           []string{"AI/ML", "Data Processing", "Utilities"},
		},
		{
			name:           "categories with extra spaces",
			categoryString: " AI/ML , Data Processing , Utilities ",
			want:           []string{"AI/ML", "Data Processing", "Utilities"},
		},
		{
			name:           "empty string",
			categoryString: "",
			want:           []string{},
		},
		{
			name:           "single category with spaces",
			categoryString: "  System Tools  ",
			want:           []string{"System Tools"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseCategories(tt.categoryString)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseCategories() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsStandardCategory(t *testing.T) {
	tests := []struct {
		name     string
		category string
		want     bool
	}{
		{
			name:     "standard category - System Tools",
			category: CategorySystemTools,
			want:     true,
		},
		{
			name:     "standard category - AI/ML",
			category: CategoryAIML,
			want:     true,
		},
		{
			name:     "custom category",
			category: "My Custom Category",
			want:     false,
		},
		{
			name:     "empty string",
			category: "",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsStandardCategory(tt.category)
			if got != tt.want {
				t.Errorf("IsStandardCategory() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCategoryManager_ReassignCategory(t *testing.T) {
	cm := NewCategoryManager()

	// Assign initial category
	cm.AssignCategory("plugin1", CategorySystemTools)

	// Verify initial assignment
	if cat := cm.GetCategory("plugin1"); cat != CategorySystemTools {
		t.Errorf("Expected initial category %s, got %s", CategorySystemTools, cat)
	}
	plugins := cm.GetPluginsByCategory(CategorySystemTools)
	if len(plugins) != 1 || plugins[0] != "plugin1" {
		t.Errorf("Expected plugin1 in System Tools, got %v", plugins)
	}

	// Reassign to new category
	cm.AssignCategory("plugin1", CategoryAIML)

	// Verify new assignment
	if cat := cm.GetCategory("plugin1"); cat != CategoryAIML {
		t.Errorf("Expected new category %s, got %s", CategoryAIML, cat)
	}

	// Verify removed from old category
	plugins = cm.GetPluginsByCategory(CategorySystemTools)
	if len(plugins) != 0 {
		t.Errorf("Expected plugin1 removed from System Tools, got %v", plugins)
	}

	// Verify added to new category
	plugins = cm.GetPluginsByCategory(CategoryAIML)
	if len(plugins) != 1 || plugins[0] != "plugin1" {
		t.Errorf("Expected plugin1 in AI/ML, got %v", plugins)
	}
}

func TestCategoryManager_MultipleCategories(t *testing.T) {
	cm := NewCategoryManager()

	// Assign plugin to multiple categories
	cm.AssignCategory("plugin1", "AI/ML, Data Processing")

	// Verify plugin appears in both categories
	aimlPlugins := cm.GetPluginsByCategory("AI/ML")
	if len(aimlPlugins) != 1 || aimlPlugins[0] != "plugin1" {
		t.Errorf("Expected plugin1 in AI/ML, got %v", aimlPlugins)
	}

	dpPlugins := cm.GetPluginsByCategory("Data Processing")
	if len(dpPlugins) != 1 || dpPlugins[0] != "plugin1" {
		t.Errorf("Expected plugin1 in Data Processing, got %v", dpPlugins)
	}

	// Verify GetAllCategories includes both
	allCategories := cm.GetAllCategories()
	sort.Strings(allCategories)
	want := []string{"AI/ML", "Data Processing"}
	sort.Strings(want)
	if !reflect.DeepEqual(allCategories, want) {
		t.Errorf("GetAllCategories() = %v, want %v", allCategories, want)
	}
}
