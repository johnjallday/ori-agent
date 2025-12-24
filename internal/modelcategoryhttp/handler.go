package modelcategoryhttp

import (
	"encoding/json"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

// Handler handles model category HTTP requests
type Handler struct {
	store store.ModelCategoryStore
}

// NewHandler creates a new model category HTTP handler
func NewHandler(categoryStore store.ModelCategoryStore) *Handler {
	return &Handler{
		store: categoryStore,
	}
}

// GetAllHandler returns all categories, assignments, and view preference
// GET /api/model-categories
func (h *Handler) GetAllHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.RespondMethodNotAllowed(w)
		return
	}

	config := h.store.GetConfig()

	response := struct {
		Categories       []types.ModelCategory `json:"categories"`
		ModelAssignments map[string][]string   `json:"model_assignments"`
		ViewPreference   string                `json:"view_preference"`
		PredefinedColors []string              `json:"predefined_colors"`
		PredefinedIcons  []string              `json:"predefined_icons"`
		MaxCategories    int                   `json:"max_categories"`
	}{
		Categories:       config.Categories,
		ModelAssignments: config.ModelAssignments,
		ViewPreference:   config.ViewPreference,
		PredefinedColors: types.PredefinedColors,
		PredefinedIcons:  types.PredefinedIcons,
		MaxCategories:    types.MaxCustomCategories,
	}

	orihttp.WriteJSON(w, response)
}

// CreateCategoryHandler creates a new category
// POST /api/model-categories
func (h *Handler) CreateCategoryHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.RespondMethodNotAllowed(w)
		return
	}

	var req struct {
		Name  string `json:"name"`
		Color string `json:"color"`
		Icon  string `json:"icon"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		orihttp.RespondBadRequest(w, "invalid request body")
		return
	}

	if strings.TrimSpace(req.Name) == "" {
		orihttp.RespondBadRequest(w, "name is required")
		return
	}

	category, err := h.store.CreateCategory(req.Name, req.Color, req.Icon)
	if err != nil {
		if strings.Contains(err.Error(), "already exists") {
			orihttp.RespondBadRequest(w, err.Error())
			return
		}
		if strings.Contains(err.Error(), "maximum") {
			orihttp.RespondBadRequest(w, err.Error())
			return
		}
		orihttp.RespondInternalError(w, err.Error())
		return
	}

	w.WriteHeader(http.StatusCreated)
	orihttp.WriteJSON(w, category)
}

// UpdateCategoryHandler updates an existing category
// PUT /api/model-categories/{id}
func (h *Handler) UpdateCategoryHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		orihttp.RespondMethodNotAllowed(w)
		return
	}

	// Extract category ID from path
	id := extractIDFromPath(r.URL.Path, "/api/model-categories/")
	if id == "" {
		orihttp.RespondBadRequest(w, "category ID is required")
		return
	}

	var req struct {
		Name  string `json:"name"`
		Color string `json:"color"`
		Icon  string `json:"icon"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		orihttp.RespondBadRequest(w, "invalid request body")
		return
	}

	if err := h.store.UpdateCategory(id, req.Name, req.Color, req.Icon); err != nil {
		if strings.Contains(err.Error(), "not found") {
			orihttp.RespondNotFound(w, err.Error())
			return
		}
		if strings.Contains(err.Error(), "already exists") {
			orihttp.RespondBadRequest(w, err.Error())
			return
		}
		orihttp.RespondInternalError(w, err.Error())
		return
	}

	// Return updated category
	category, _ := h.store.GetCategory(id)
	orihttp.WriteJSON(w, category)
}

// DeleteCategoryHandler deletes a category
// DELETE /api/model-categories/{id}
func (h *Handler) DeleteCategoryHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		orihttp.RespondMethodNotAllowed(w)
		return
	}

	// Extract category ID from path
	id := extractIDFromPath(r.URL.Path, "/api/model-categories/")
	if id == "" {
		orihttp.RespondBadRequest(w, "category ID is required")
		return
	}

	if err := h.store.DeleteCategory(id); err != nil {
		if strings.Contains(err.Error(), "not found") {
			orihttp.RespondNotFound(w, err.Error())
			return
		}
		if strings.Contains(err.Error(), "cannot delete default") {
			orihttp.RespondBadRequest(w, err.Error())
			return
		}
		orihttp.RespondInternalError(w, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ReorderCategoriesHandler updates the order of categories
// PUT /api/model-categories/reorder
func (h *Handler) ReorderCategoriesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		orihttp.RespondMethodNotAllowed(w)
		return
	}

	var req struct {
		CategoryIDs []string `json:"category_ids"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		orihttp.RespondBadRequest(w, "invalid request body")
		return
	}

	if err := h.store.ReorderCategories(req.CategoryIDs); err != nil {
		orihttp.RespondInternalError(w, err.Error())
		return
	}

	// Return updated categories
	categories := h.store.GetCategories()
	orihttp.WriteJSON(w, map[string]interface{}{
		"categories": categories,
	})
}

// SetVisibilityHandler sets the visibility of a category (hide/show)
// PUT /api/model-categories/{id}/visibility
func (h *Handler) SetVisibilityHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		orihttp.RespondMethodNotAllowed(w)
		return
	}

	// Extract category ID from path
	path := strings.TrimSuffix(r.URL.Path, "/visibility")
	id := extractIDFromPath(path, "/api/model-categories/")
	if id == "" {
		orihttp.RespondBadRequest(w, "category ID is required")
		return
	}

	var req struct {
		Hidden bool `json:"hidden"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		orihttp.RespondBadRequest(w, "invalid request body")
		return
	}

	if err := h.store.SetCategoryVisibility(id, req.Hidden); err != nil {
		if strings.Contains(err.Error(), "not found") {
			orihttp.RespondNotFound(w, err.Error())
			return
		}
		orihttp.RespondInternalError(w, err.Error())
		return
	}

	// Return updated category
	category, _ := h.store.GetCategory(id)
	orihttp.WriteJSON(w, category)
}

// SetModelAssignmentsHandler updates the categories for a model
// PUT /api/models/{modelId}/categories
func (h *Handler) SetModelAssignmentsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		orihttp.RespondMethodNotAllowed(w)
		return
	}

	// Extract model ID from path
	modelID := extractModelIDFromPath(r.URL.Path)
	if modelID == "" {
		orihttp.RespondBadRequest(w, "model ID is required")
		return
	}

	var req struct {
		CategoryIDs []string `json:"category_ids"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		orihttp.RespondBadRequest(w, "invalid request body")
		return
	}

	if err := h.store.SetModelAssignments(modelID, req.CategoryIDs); err != nil {
		if strings.Contains(err.Error(), "invalid category") {
			orihttp.RespondBadRequest(w, err.Error())
			return
		}
		orihttp.RespondInternalError(w, err.Error())
		return
	}

	// Return updated assignments
	assignments := h.store.GetModelAssignments(modelID)
	orihttp.WriteJSON(w, map[string]interface{}{
		"model_id":     modelID,
		"category_ids": assignments,
	})
}

// SetViewPreferenceHandler updates the view preference
// PUT /api/model-categories/view-preference
func (h *Handler) SetViewPreferenceHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		orihttp.RespondMethodNotAllowed(w)
		return
	}

	var req struct {
		Preference string `json:"preference"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		orihttp.RespondBadRequest(w, "invalid request body")
		return
	}

	if err := h.store.SetViewPreference(req.Preference); err != nil {
		orihttp.RespondBadRequest(w, err.Error())
		return
	}

	orihttp.WriteJSON(w, map[string]interface{}{
		"view_preference": h.store.GetViewPreference(),
	})
}

// CategoriesHandler handles /api/model-categories routes
func (h *Handler) CategoriesHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.GetAllHandler(w, r)
	case http.MethodPost:
		h.CreateCategoryHandler(w, r)
	default:
		orihttp.RespondMethodNotAllowed(w)
	}
}

// CategoryHandler handles /api/model-categories/{id} routes
func (h *Handler) CategoryHandler(w http.ResponseWriter, r *http.Request) {
	// Check for special endpoints
	if strings.HasSuffix(r.URL.Path, "/visibility") {
		h.SetVisibilityHandler(w, r)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.UpdateCategoryHandler(w, r)
	case http.MethodDelete:
		h.DeleteCategoryHandler(w, r)
	default:
		orihttp.RespondMethodNotAllowed(w)
	}
}

// extractIDFromPath extracts the ID from a URL path
func extractIDFromPath(path, prefix string) string {
	path = strings.TrimPrefix(path, prefix)
	// Remove any trailing slashes or additional path segments
	if idx := strings.Index(path, "/"); idx != -1 {
		path = path[:idx]
	}
	return strings.TrimSpace(path)
}

// extractModelIDFromPath extracts the model ID from /api/models/{modelId}/categories
func extractModelIDFromPath(path string) string {
	path = strings.TrimPrefix(path, "/api/models/")
	if idx := strings.Index(path, "/categories"); idx != -1 {
		return strings.TrimSpace(path[:idx])
	}
	return ""
}
