package modelcategoryhttp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

func setupTestHandler(t *testing.T) (*Handler, func()) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "model_categories.json")

	categoryStore, err := store.NewFileModelCategoryStore(path)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	handler := NewHandler(categoryStore)
	return handler, func() {}
}

func TestGetAllHandler(t *testing.T) {
	handler, cleanup := setupTestHandler(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/model-categories", nil)
	w := httptest.NewRecorder()

	handler.GetAllHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response struct {
		Categories       []types.ModelCategory `json:"categories"`
		ModelAssignments map[string][]string   `json:"model_assignments"`
		ViewPreference   string                `json:"view_preference"`
		PredefinedColors []string              `json:"predefined_colors"`
		PredefinedIcons  []string              `json:"predefined_icons"`
		MaxCategories    int                   `json:"max_categories"`
	}

	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if len(response.Categories) != 3 {
		t.Errorf("Expected 3 default categories, got %d", len(response.Categories))
	}

	if len(response.PredefinedColors) == 0 {
		t.Error("Expected predefined colors")
	}

	if len(response.PredefinedIcons) == 0 {
		t.Error("Expected predefined icons")
	}
}

func TestCreateCategoryHandler(t *testing.T) {
	handler, cleanup := setupTestHandler(t)
	defer cleanup()

	body := bytes.NewBufferString(`{"name":"Coding","color":"#3b82f6","icon":"code-slash"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/model-categories", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.CreateCategoryHandler(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("Expected status 201, got %d: %s", w.Code, w.Body.String())
	}

	var category types.ModelCategory
	if err := json.NewDecoder(w.Body).Decode(&category); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if category.Name != "Coding" {
		t.Errorf("Expected name 'Coding', got '%s'", category.Name)
	}
}

func TestCreateCategoryDuplicateName(t *testing.T) {
	handler, cleanup := setupTestHandler(t)
	defer cleanup()

	// Try to create with a name that matches default category
	body := bytes.NewBufferString(`{"name":"Tool Calling","color":"#3b82f6","icon":"code-slash"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/model-categories", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.CreateCategoryHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}
}

func TestUpdateCategoryHandler(t *testing.T) {
	handler, cleanup := setupTestHandler(t)
	defer cleanup()

	// First create a category
	createBody := bytes.NewBufferString(`{"name":"Coding","color":"#3b82f6","icon":"code-slash"}`)
	createReq := httptest.NewRequest(http.MethodPost, "/api/model-categories", createBody)
	createReq.Header.Set("Content-Type", "application/json")
	createW := httptest.NewRecorder()
	handler.CreateCategoryHandler(createW, createReq)

	var created types.ModelCategory
	json.NewDecoder(createW.Body).Decode(&created)

	// Now update it
	updateBody := bytes.NewBufferString(`{"name":"Programming","color":"#ef4444","icon":"terminal"}`)
	updateReq := httptest.NewRequest(http.MethodPut, "/api/model-categories/"+created.ID, updateBody)
	updateReq.Header.Set("Content-Type", "application/json")
	updateW := httptest.NewRecorder()

	handler.UpdateCategoryHandler(updateW, updateReq)

	if updateW.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d: %s", updateW.Code, updateW.Body.String())
	}

	var updated types.ModelCategory
	json.NewDecoder(updateW.Body).Decode(&updated)

	if updated.Name != "Programming" {
		t.Errorf("Expected name 'Programming', got '%s'", updated.Name)
	}
}

func TestDeleteCategoryHandler(t *testing.T) {
	handler, cleanup := setupTestHandler(t)
	defer cleanup()

	// First create a category
	createBody := bytes.NewBufferString(`{"name":"Coding","color":"#3b82f6","icon":"code-slash"}`)
	createReq := httptest.NewRequest(http.MethodPost, "/api/model-categories", createBody)
	createReq.Header.Set("Content-Type", "application/json")
	createW := httptest.NewRecorder()
	handler.CreateCategoryHandler(createW, createReq)

	var created types.ModelCategory
	json.NewDecoder(createW.Body).Decode(&created)

	// Delete it
	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/model-categories/"+created.ID, nil)
	deleteW := httptest.NewRecorder()

	handler.DeleteCategoryHandler(deleteW, deleteReq)

	if deleteW.Code != http.StatusNoContent {
		t.Errorf("Expected status 204, got %d", deleteW.Code)
	}
}

func TestDeleteDefaultCategory(t *testing.T) {
	handler, cleanup := setupTestHandler(t)
	defer cleanup()

	// Try to delete a default category
	req := httptest.NewRequest(http.MethodDelete, "/api/model-categories/cat_default_tool_calling", nil)
	w := httptest.NewRecorder()

	handler.DeleteCategoryHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}
}

func TestSetVisibilityHandler(t *testing.T) {
	handler, cleanup := setupTestHandler(t)
	defer cleanup()

	body := bytes.NewBufferString(`{"hidden":true}`)
	req := httptest.NewRequest(http.MethodPut, "/api/model-categories/cat_default_tool_calling/visibility", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.SetVisibilityHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var category types.ModelCategory
	json.NewDecoder(w.Body).Decode(&category)

	if !category.IsHidden {
		t.Error("Expected category to be hidden")
	}
}

func TestSetModelAssignmentsHandler(t *testing.T) {
	handler, cleanup := setupTestHandler(t)
	defer cleanup()

	body := bytes.NewBufferString(`{"category_ids":["cat_default_tool_calling","cat_default_general_purpose"]}`)
	req := httptest.NewRequest(http.MethodPut, "/api/models/gpt-4o/categories", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.SetModelAssignmentsHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var response struct {
		ModelID     string   `json:"model_id"`
		CategoryIDs []string `json:"category_ids"`
	}
	json.NewDecoder(w.Body).Decode(&response)

	if len(response.CategoryIDs) != 2 {
		t.Errorf("Expected 2 category IDs, got %d", len(response.CategoryIDs))
	}
}

func TestSetViewPreferenceHandler(t *testing.T) {
	handler, cleanup := setupTestHandler(t)
	defer cleanup()

	body := bytes.NewBufferString(`{"preference":"category"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/model-categories/view-preference", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.SetViewPreferenceHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var response struct {
		ViewPreference string `json:"view_preference"`
	}
	json.NewDecoder(w.Body).Decode(&response)

	if response.ViewPreference != "category" {
		t.Errorf("Expected preference 'category', got '%s'", response.ViewPreference)
	}
}

func TestReorderCategoriesHandler(t *testing.T) {
	handler, cleanup := setupTestHandler(t)
	defer cleanup()

	// Reverse the default order
	body := bytes.NewBufferString(`{"category_ids":["cat_default_research","cat_default_general_purpose","cat_default_tool_calling"]}`)
	req := httptest.NewRequest(http.MethodPut, "/api/model-categories/reorder", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ReorderCategoriesHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var response struct {
		Categories []types.ModelCategory `json:"categories"`
	}
	json.NewDecoder(w.Body).Decode(&response)

	if len(response.Categories) < 3 {
		t.Fatal("Expected at least 3 categories")
	}

	if response.Categories[0].ID != "cat_default_research" {
		t.Errorf("Expected first category to be research, got %s", response.Categories[0].ID)
	}
}
