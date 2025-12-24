package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/johnjallday/ori-agent/internal/types"
)

// ModelCategoryStore defines the interface for managing model categories
type ModelCategoryStore interface {
	// Load reads the configuration from disk
	Load() error
	// Save writes the configuration to disk
	Save() error

	// Category management
	GetCategories() []types.ModelCategory
	GetCategory(id string) (*types.ModelCategory, bool)
	CreateCategory(name, color, icon string) (*types.ModelCategory, error)
	UpdateCategory(id, name, color, icon string) error
	DeleteCategory(id string) error
	ReorderCategories(orderedIDs []string) error
	SetCategoryVisibility(id string, hidden bool) error

	// Model assignments
	GetModelAssignments(modelID string) []string
	SetModelAssignments(modelID string, categoryIDs []string) error
	GetAllModelAssignments() map[string][]string

	// View preference
	GetViewPreference() string
	SetViewPreference(preference string) error

	// Full config access
	GetConfig() *types.ModelCategoryConfig
}

// fileModelCategoryStore implements ModelCategoryStore using a JSON file
type fileModelCategoryStore struct {
	mu     sync.RWMutex
	path   string
	config *types.ModelCategoryConfig
}

// NewFileModelCategoryStore creates a new file-based model category store
func NewFileModelCategoryStore(path string) (ModelCategoryStore, error) {
	s := &fileModelCategoryStore{
		path:   path,
		config: types.NewModelCategoryConfig(),
	}

	// Try to load existing config
	if err := s.Load(); err != nil && !os.IsNotExist(err) {
		// Non-fatal: use defaults if load fails
		s.config = types.NewModelCategoryConfig()
	}

	// Ensure default categories exist
	s.ensureDefaultCategories()

	// Save to create file if it doesn't exist
	if err := s.Save(); err != nil {
		return nil, err
	}

	return s, nil
}

// ensureDefaultCategories makes sure all default categories exist
func (s *fileModelCategoryStore) ensureDefaultCategories() {
	defaults := types.DefaultCategories()
	existingDefaults := make(map[string]bool)

	for _, cat := range s.config.Categories {
		if cat.IsDefault {
			existingDefaults[cat.ID] = true
		}
	}

	for _, defCat := range defaults {
		if !existingDefaults[defCat.ID] {
			s.config.Categories = append(s.config.Categories, defCat)
		}
	}

	// Re-sort by order
	sort.Slice(s.config.Categories, func(i, j int) bool {
		return s.config.Categories[i].Order < s.config.Categories[j].Order
	})

	// Ensure default model assignments exist (don't override user assignments)
	s.ensureDefaultModelAssignments()
}

// ensureDefaultModelAssignments adds default assignments for models that don't have any
func (s *fileModelCategoryStore) ensureDefaultModelAssignments() {
	defaults := types.DefaultModelAssignments()

	for modelID, categoryIDs := range defaults {
		// Only add if model has no existing assignments
		if _, exists := s.config.ModelAssignments[modelID]; !exists {
			s.config.ModelAssignments[modelID] = categoryIDs
		}
	}
}

// Load reads the configuration from disk
func (s *fileModelCategoryStore) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}

	var config types.ModelCategoryConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return err
	}

	// Initialize map if nil
	if config.ModelAssignments == nil {
		config.ModelAssignments = make(map[string][]string)
	}

	// Set default view preference if empty
	if config.ViewPreference == "" {
		config.ViewPreference = "provider"
	}

	s.config = &config
	return nil
}

// Save writes the configuration to disk using atomic write
func (s *fileModelCategoryStore) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.saveUnlocked()
}

func (s *fileModelCategoryStore) saveUnlocked() error {
	// Ensure directory exists
	dir := filepath.Dir(s.path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	data, err := json.MarshalIndent(s.config, "", "  ")
	if err != nil {
		return err
	}

	// Atomic write: write to temp file, then rename
	tempPath := s.path + ".tmp"
	if err := os.WriteFile(tempPath, data, 0o644); err != nil {
		return err
	}

	return os.Rename(tempPath, s.path)
}

// GetCategories returns all categories sorted by order
func (s *fileModelCategoryStore) GetCategories() []types.ModelCategory {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Return a copy sorted by order
	categories := make([]types.ModelCategory, len(s.config.Categories))
	copy(categories, s.config.Categories)

	sort.Slice(categories, func(i, j int) bool {
		return categories[i].Order < categories[j].Order
	})

	return categories
}

// GetCategory returns a category by ID
func (s *fileModelCategoryStore) GetCategory(id string) (*types.ModelCategory, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for i := range s.config.Categories {
		if s.config.Categories[i].ID == id {
			cat := s.config.Categories[i]
			return &cat, true
		}
	}
	return nil, false
}

// CreateCategory creates a new custom category
func (s *fileModelCategoryStore) CreateCategory(name, color, icon string) (*types.ModelCategory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Validate name
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("category name is required")
	}

	// Check for duplicate name (case-insensitive)
	for _, cat := range s.config.Categories {
		if strings.EqualFold(cat.Name, name) {
			return nil, errors.New("category name already exists")
		}
	}

	// Count custom categories
	customCount := 0
	for _, cat := range s.config.Categories {
		if !cat.IsDefault {
			customCount++
		}
	}

	if customCount >= types.MaxCustomCategories {
		return nil, errors.New("maximum number of custom categories reached")
	}

	// Validate and set defaults
	if color == "" {
		color = types.PredefinedColors[0]
	}
	if icon == "" {
		icon = types.PredefinedIcons[0]
	}

	// Generate unique ID
	id := generateCategoryID()

	// Find highest order
	maxOrder := -1
	for _, cat := range s.config.Categories {
		if cat.Order > maxOrder {
			maxOrder = cat.Order
		}
	}

	category := types.ModelCategory{
		ID:        id,
		Name:      name,
		Color:     color,
		Icon:      icon,
		Order:     maxOrder + 1,
		IsDefault: false,
		IsHidden:  false,
	}

	s.config.Categories = append(s.config.Categories, category)

	if err := s.saveUnlocked(); err != nil {
		return nil, err
	}

	return &category, nil
}

// UpdateCategory updates an existing category
func (s *fileModelCategoryStore) UpdateCategory(id, name, color, icon string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Validate name
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("category name is required")
	}

	// Find category
	idx := -1
	for i := range s.config.Categories {
		if s.config.Categories[i].ID == id {
			idx = i
			break
		}
	}

	if idx == -1 {
		return errors.New("category not found")
	}

	// Check for duplicate name (case-insensitive, excluding current)
	for i, cat := range s.config.Categories {
		if i != idx && strings.EqualFold(cat.Name, name) {
			return errors.New("category name already exists")
		}
	}

	// Update fields
	s.config.Categories[idx].Name = name
	if color != "" {
		s.config.Categories[idx].Color = color
	}
	if icon != "" {
		s.config.Categories[idx].Icon = icon
	}

	return s.saveUnlocked()
}

// DeleteCategory deletes a category (cannot delete default categories)
func (s *fileModelCategoryStore) DeleteCategory(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Find category
	idx := -1
	for i := range s.config.Categories {
		if s.config.Categories[i].ID == id {
			idx = i
			break
		}
	}

	if idx == -1 {
		return errors.New("category not found")
	}

	// Cannot delete default categories
	if s.config.Categories[idx].IsDefault {
		return errors.New("cannot delete default categories")
	}

	// Remove category
	s.config.Categories = append(s.config.Categories[:idx], s.config.Categories[idx+1:]...)

	// Remove from all model assignments
	for modelID, categoryIDs := range s.config.ModelAssignments {
		filtered := make([]string, 0, len(categoryIDs))
		for _, catID := range categoryIDs {
			if catID != id {
				filtered = append(filtered, catID)
			}
		}
		if len(filtered) == 0 {
			delete(s.config.ModelAssignments, modelID)
		} else {
			s.config.ModelAssignments[modelID] = filtered
		}
	}

	return s.saveUnlocked()
}

// ReorderCategories updates the order of categories
func (s *fileModelCategoryStore) ReorderCategories(orderedIDs []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Build a map of ID to category
	catMap := make(map[string]*types.ModelCategory)
	for i := range s.config.Categories {
		catMap[s.config.Categories[i].ID] = &s.config.Categories[i]
	}

	// Update order based on position in orderedIDs
	for order, id := range orderedIDs {
		if cat, ok := catMap[id]; ok {
			cat.Order = order
		}
	}

	// Sort by new order
	sort.Slice(s.config.Categories, func(i, j int) bool {
		return s.config.Categories[i].Order < s.config.Categories[j].Order
	})

	return s.saveUnlocked()
}

// SetCategoryVisibility sets the hidden state of a category
func (s *fileModelCategoryStore) SetCategoryVisibility(id string, hidden bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Find category
	for i := range s.config.Categories {
		if s.config.Categories[i].ID == id {
			s.config.Categories[i].IsHidden = hidden
			return s.saveUnlocked()
		}
	}

	return errors.New("category not found")
}

// GetModelAssignments returns the category IDs assigned to a model
func (s *fileModelCategoryStore) GetModelAssignments(modelID string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if assignments, ok := s.config.ModelAssignments[modelID]; ok {
		result := make([]string, len(assignments))
		copy(result, assignments)
		return result
	}
	return []string{}
}

// SetModelAssignments sets the category IDs for a model
func (s *fileModelCategoryStore) SetModelAssignments(modelID string, categoryIDs []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Validate category IDs exist
	validCatIDs := make(map[string]bool)
	for _, cat := range s.config.Categories {
		validCatIDs[cat.ID] = true
	}

	for _, catID := range categoryIDs {
		if !validCatIDs[catID] {
			return errors.New("invalid category ID: " + catID)
		}
	}

	if len(categoryIDs) == 0 {
		delete(s.config.ModelAssignments, modelID)
	} else {
		s.config.ModelAssignments[modelID] = categoryIDs
	}

	return s.saveUnlocked()
}

// GetAllModelAssignments returns all model-to-category assignments
func (s *fileModelCategoryStore) GetAllModelAssignments() map[string][]string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string][]string)
	for modelID, catIDs := range s.config.ModelAssignments {
		copied := make([]string, len(catIDs))
		copy(copied, catIDs)
		result[modelID] = copied
	}
	return result
}

// GetViewPreference returns the current view preference
func (s *fileModelCategoryStore) GetViewPreference() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.config.ViewPreference == "" {
		return "provider"
	}
	return s.config.ViewPreference
}

// SetViewPreference sets the view preference
func (s *fileModelCategoryStore) SetViewPreference(preference string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if preference != "provider" && preference != "category" {
		return errors.New("invalid view preference: must be 'provider' or 'category'")
	}

	s.config.ViewPreference = preference
	return s.saveUnlocked()
}

// GetConfig returns the full configuration
func (s *fileModelCategoryStore) GetConfig() *types.ModelCategoryConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Return a copy
	config := &types.ModelCategoryConfig{
		Categories:       make([]types.ModelCategory, len(s.config.Categories)),
		ModelAssignments: make(map[string][]string),
		ViewPreference:   s.config.ViewPreference,
	}

	copy(config.Categories, s.config.Categories)

	for k, v := range s.config.ModelAssignments {
		copied := make([]string, len(v))
		copy(copied, v)
		config.ModelAssignments[k] = copied
	}

	return config
}

// generateCategoryID generates a unique category ID
func generateCategoryID() string {
	bytes := make([]byte, 8)
	if _, err := rand.Read(bytes); err != nil {
		// Fallback to less random but still unique
		return "cat_" + string(rune(os.Getpid()))
	}
	return "cat_" + hex.EncodeToString(bytes)
}
