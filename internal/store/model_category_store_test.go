package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/types"
)

func TestNewFileModelCategoryStore(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "model_categories.json")

	store, err := NewFileModelCategoryStore(path)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Should have default categories
	categories := store.GetCategories()
	if len(categories) != 3 {
		t.Errorf("Expected 3 default categories, got %d", len(categories))
	}

	// Verify file was created
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("Expected config file to be created")
	}
}

func TestCreateCategory(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "model_categories.json")

	store, err := NewFileModelCategoryStore(path)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Create a new category
	cat, err := store.CreateCategory("Coding", "#3b82f6", "code-slash")
	if err != nil {
		t.Fatalf("Failed to create category: %v", err)
	}

	if cat.Name != "Coding" {
		t.Errorf("Expected name 'Coding', got '%s'", cat.Name)
	}
	if cat.Color != "#3b82f6" {
		t.Errorf("Expected color '#3b82f6', got '%s'", cat.Color)
	}
	if cat.Icon != "code-slash" {
		t.Errorf("Expected icon 'code-slash', got '%s'", cat.Icon)
	}
	if cat.IsDefault {
		t.Error("Expected IsDefault to be false")
	}

	// Should now have 4 categories
	categories := store.GetCategories()
	if len(categories) != 4 {
		t.Errorf("Expected 4 categories, got %d", len(categories))
	}
}

func TestCreateCategoryDuplicateName(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "model_categories.json")

	store, err := NewFileModelCategoryStore(path)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Try to create category with duplicate name (case-insensitive)
	_, err = store.CreateCategory("tool calling", "#3b82f6", "code-slash")
	if err == nil {
		t.Error("Expected error for duplicate name")
	}
}

func TestCreateCategoryMaxLimit(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "model_categories.json")

	store, err := NewFileModelCategoryStore(path)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Create max number of custom categories
	for i := 0; i < types.MaxCustomCategories; i++ {
		_, err := store.CreateCategory("Category"+string(rune('A'+i)), "#3b82f6", "code-slash")
		if err != nil {
			t.Fatalf("Failed to create category %d: %v", i, err)
		}
	}

	// Try to create one more
	_, err = store.CreateCategory("OneMoreCategory", "#3b82f6", "code-slash")
	if err == nil {
		t.Error("Expected error when exceeding max categories")
	}
}

func TestUpdateCategory(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "model_categories.json")

	store, err := NewFileModelCategoryStore(path)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Create a category
	cat, err := store.CreateCategory("Coding", "#3b82f6", "code-slash")
	if err != nil {
		t.Fatalf("Failed to create category: %v", err)
	}

	// Update it
	err = store.UpdateCategory(cat.ID, "Programming", "#ef4444", "terminal")
	if err != nil {
		t.Fatalf("Failed to update category: %v", err)
	}

	// Verify update
	updated, ok := store.GetCategory(cat.ID)
	if !ok {
		t.Fatal("Category not found after update")
	}
	if updated.Name != "Programming" {
		t.Errorf("Expected name 'Programming', got '%s'", updated.Name)
	}
	if updated.Color != "#ef4444" {
		t.Errorf("Expected color '#ef4444', got '%s'", updated.Color)
	}
}

func TestDeleteCategory(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "model_categories.json")

	store, err := NewFileModelCategoryStore(path)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Create a category
	cat, err := store.CreateCategory("Coding", "#3b82f6", "code-slash")
	if err != nil {
		t.Fatalf("Failed to create category: %v", err)
	}

	// Assign it to a model
	err = store.SetModelAssignments("gpt-4o", []string{cat.ID})
	if err != nil {
		t.Fatalf("Failed to set model assignments: %v", err)
	}

	// Delete the category
	err = store.DeleteCategory(cat.ID)
	if err != nil {
		t.Fatalf("Failed to delete category: %v", err)
	}

	// Verify it's gone
	_, ok := store.GetCategory(cat.ID)
	if ok {
		t.Error("Category should be deleted")
	}

	// Verify model assignment is also removed
	assignments := store.GetModelAssignments("gpt-4o")
	if len(assignments) != 0 {
		t.Errorf("Expected 0 assignments after category deletion, got %d", len(assignments))
	}
}

func TestDeleteDefaultCategory(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "model_categories.json")

	store, err := NewFileModelCategoryStore(path)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Try to delete a default category
	err = store.DeleteCategory("cat_default_tool_calling")
	if err == nil {
		t.Error("Expected error when deleting default category")
	}
}

func TestReorderCategories(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "model_categories.json")

	store, err := NewFileModelCategoryStore(path)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Get current categories
	categories := store.GetCategories()
	if len(categories) < 3 {
		t.Fatal("Expected at least 3 default categories")
	}

	// Reverse the order
	orderedIDs := make([]string, len(categories))
	for i, cat := range categories {
		orderedIDs[len(categories)-1-i] = cat.ID
	}

	err = store.ReorderCategories(orderedIDs)
	if err != nil {
		t.Fatalf("Failed to reorder: %v", err)
	}

	// Verify new order
	newCategories := store.GetCategories()
	for i, cat := range newCategories {
		if cat.ID != orderedIDs[i] {
			t.Errorf("Expected category %s at position %d, got %s", orderedIDs[i], i, cat.ID)
		}
	}
}

func TestSetCategoryVisibility(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "model_categories.json")

	store, err := NewFileModelCategoryStore(path)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Hide a default category
	err = store.SetCategoryVisibility("cat_default_tool_calling", true)
	if err != nil {
		t.Fatalf("Failed to set visibility: %v", err)
	}

	// Verify
	cat, ok := store.GetCategory("cat_default_tool_calling")
	if !ok {
		t.Fatal("Category not found")
	}
	if !cat.IsHidden {
		t.Error("Expected category to be hidden")
	}

	// Unhide
	err = store.SetCategoryVisibility("cat_default_tool_calling", false)
	if err != nil {
		t.Fatalf("Failed to set visibility: %v", err)
	}

	cat, _ = store.GetCategory("cat_default_tool_calling")
	if cat.IsHidden {
		t.Error("Expected category to be visible")
	}
}

func TestModelAssignments(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "model_categories.json")

	store, err := NewFileModelCategoryStore(path)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Create custom category
	cat, err := store.CreateCategory("Coding", "#3b82f6", "code-slash")
	if err != nil {
		t.Fatalf("Failed to create category: %v", err)
	}

	// Assign multiple categories to a model
	categoryIDs := []string{"cat_default_tool_calling", cat.ID}
	err = store.SetModelAssignments("gpt-4o", categoryIDs)
	if err != nil {
		t.Fatalf("Failed to set assignments: %v", err)
	}

	// Verify
	assignments := store.GetModelAssignments("gpt-4o")
	if len(assignments) != 2 {
		t.Errorf("Expected 2 assignments, got %d", len(assignments))
	}

	// Clear assignments
	err = store.SetModelAssignments("gpt-4o", []string{})
	if err != nil {
		t.Fatalf("Failed to clear assignments: %v", err)
	}

	assignments = store.GetModelAssignments("gpt-4o")
	if len(assignments) != 0 {
		t.Errorf("Expected 0 assignments, got %d", len(assignments))
	}
}

func TestInvalidCategoryAssignment(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "model_categories.json")

	store, err := NewFileModelCategoryStore(path)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Try to assign a non-existent category
	err = store.SetModelAssignments("gpt-4o", []string{"invalid_category_id"})
	if err == nil {
		t.Error("Expected error for invalid category ID")
	}
}

func TestViewPreference(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "model_categories.json")

	store, err := NewFileModelCategoryStore(path)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Default should be "provider"
	pref := store.GetViewPreference()
	if pref != "provider" {
		t.Errorf("Expected default preference 'provider', got '%s'", pref)
	}

	// Set to category
	err = store.SetViewPreference("category")
	if err != nil {
		t.Fatalf("Failed to set preference: %v", err)
	}

	pref = store.GetViewPreference()
	if pref != "category" {
		t.Errorf("Expected preference 'category', got '%s'", pref)
	}

	// Invalid preference
	err = store.SetViewPreference("invalid")
	if err == nil {
		t.Error("Expected error for invalid preference")
	}
}

func TestPersistence(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "model_categories.json")

	// Create store and add data
	store1, err := NewFileModelCategoryStore(path)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	cat, err := store1.CreateCategory("Coding", "#3b82f6", "code-slash")
	if err != nil {
		t.Fatalf("Failed to create category: %v", err)
	}

	err = store1.SetModelAssignments("gpt-4o", []string{cat.ID})
	if err != nil {
		t.Fatalf("Failed to set assignments: %v", err)
	}

	err = store1.SetViewPreference("category")
	if err != nil {
		t.Fatalf("Failed to set preference: %v", err)
	}

	// Create new store from same file
	store2, err := NewFileModelCategoryStore(path)
	if err != nil {
		t.Fatalf("Failed to create second store: %v", err)
	}

	// Verify data persisted
	categories := store2.GetCategories()
	found := false
	for _, c := range categories {
		if c.Name == "Coding" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Custom category not persisted")
	}

	assignments := store2.GetModelAssignments("gpt-4o")
	if len(assignments) != 1 {
		t.Errorf("Expected 1 assignment, got %d", len(assignments))
	}

	pref := store2.GetViewPreference()
	if pref != "category" {
		t.Errorf("Expected preference 'category', got '%s'", pref)
	}
}

func TestGetConfig(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "model_categories.json")

	store, err := NewFileModelCategoryStore(path)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	config := store.GetConfig()
	if config == nil {
		t.Fatal("GetConfig returned nil")
	}

	if len(config.Categories) != 3 {
		t.Errorf("Expected 3 categories in config, got %d", len(config.Categories))
	}

	if config.ViewPreference != "provider" {
		t.Errorf("Expected default view preference, got '%s'", config.ViewPreference)
	}
}

func TestDefaultModelAssignments(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "model_categories.json")

	store, err := NewFileModelCategoryStore(path)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Check that default model assignments are populated
	allAssignments := store.GetAllModelAssignments()
	if len(allAssignments) == 0 {
		t.Error("Expected default model assignments to be populated")
	}

	// Check specific model has expected assignment
	gpt5NanoAssignments := store.GetModelAssignments("gpt-5-nano")
	if len(gpt5NanoAssignments) == 0 {
		t.Error("Expected gpt-5-nano to have default assignments")
	}

	// Verify it includes tool-calling category
	hasToolCalling := false
	for _, catID := range gpt5NanoAssignments {
		if catID == "cat_default_tool_calling" {
			hasToolCalling = true
			break
		}
	}
	if !hasToolCalling {
		t.Error("Expected gpt-5-nano to be assigned to tool-calling category")
	}
}

func TestDefaultAssignmentsNotOverridden(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "model_categories.json")

	// Create store
	store, err := NewFileModelCategoryStore(path)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Override gpt-5-nano assignments with custom assignment
	customCat, _ := store.CreateCategory("Custom", "#ff0000", "star")
	err = store.SetModelAssignments("gpt-5-nano", []string{customCat.ID})
	if err != nil {
		t.Fatalf("Failed to set custom assignment: %v", err)
	}

	// Create a new store instance (simulates restart)
	store2, err := NewFileModelCategoryStore(path)
	if err != nil {
		t.Fatalf("Failed to create second store: %v", err)
	}

	// Verify custom assignment was preserved, not overwritten by defaults
	assignments := store2.GetModelAssignments("gpt-5-nano")
	if len(assignments) != 1 {
		t.Errorf("Expected 1 assignment (custom), got %d", len(assignments))
	}
	if len(assignments) > 0 && assignments[0] != customCat.ID {
		t.Errorf("Expected custom category assignment to be preserved")
	}
}
