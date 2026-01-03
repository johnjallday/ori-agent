package modelcategoryhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/config"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/store"
)

// AutoCategorizeHandler handles auto-categorization requests for models
type AutoCategorizeHandler struct {
	store         store.ModelCategoryStore
	llmFactory    *llm.Factory
	configManager *config.Manager
}

// NewAutoCategorizeHandler creates a new AutoCategorizeHandler
func NewAutoCategorizeHandler(
	store store.ModelCategoryStore,
	llmFactory *llm.Factory,
	configManager *config.Manager,
) *AutoCategorizeHandler {
	return &AutoCategorizeHandler{
		store:         store,
		llmFactory:    llmFactory,
		configManager: configManager,
	}
}

// AutoCategorizeRequest represents the request to auto-categorize models
type AutoCategorizeRequest struct {
	ModelIDs []string `json:"model_ids"`
}

// CategorySuggestion represents a single model's category suggestion
type CategorySuggestion struct {
	ModelID         string  `json:"model_id"`
	CategoryID      string  `json:"category_id"`      // Empty string means uncategorized
	CategoryName    string  `json:"category_name"`    // For display purposes
	CurrentCategory string  `json:"current_category"` // Current category name if any
	Confidence      float64 `json:"confidence"`       // 0.0 to 1.0
	Reasoning       string  `json:"reasoning"`        // Brief explanation
}

// AutoCategorizeResponse represents the auto-categorization suggestions
type AutoCategorizeResponse struct {
	Suggestions []CategorySuggestion `json:"suggestions"`
	Message     string               `json:"message,omitempty"`
}

// AvailabilityResponse represents the response for availability check
type AvailabilityResponse struct {
	Available             bool   `json:"available"`
	SystemModelConfigured bool   `json:"system_model_configured"`
	HasCategories         bool   `json:"has_categories"`
	CategoryCount         int    `json:"category_count"`
	Message               string `json:"message,omitempty"`
}

// CheckAvailabilityHandler returns whether auto-categorization is available
// GET /api/models/auto-categorize/availability
func (h *AutoCategorizeHandler) CheckAvailabilityHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}

	// Check if system model is configured
	systemModelConfigured := h.configManager.IsSystemModelConfigured()

	// Check if there are any categories (excluding defaults that are hidden)
	categories := h.store.GetCategories()
	visibleCategories := 0
	for _, cat := range categories {
		if !cat.IsHidden {
			visibleCategories++
		}
	}
	hasCategories := visibleCategories > 0

	// Auto-categorize is available if both conditions are met
	available := systemModelConfigured && hasCategories

	response := AvailabilityResponse{
		Available:             available,
		SystemModelConfigured: systemModelConfigured,
		HasCategories:         hasCategories,
		CategoryCount:         visibleCategories,
	}

	if !available {
		if !systemModelConfigured {
			response.Message = "System model not configured. Please configure a system model in Settings to use auto-categorize."
		} else if !hasCategories {
			response.Message = "No categories available. Please create at least one category before using auto-categorize."
		}
	}

	orihttp.WriteJSON(w, response)
}

// AutoCategorizeHandler handles the auto-categorization request
// POST /api/models/auto-categorize
func (h *AutoCategorizeHandler) AutoCategorizeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}

	var req AutoCategorizeRequest
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodySize)
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	// Validate request
	if len(req.ModelIDs) == 0 {
		_ = orihttp.RespondBadRequest(w, "At least one model ID is required")
		return
	}

	// Limit batch size to prevent abuse
	const maxBatchSize = 50
	if len(req.ModelIDs) > maxBatchSize {
		_ = orihttp.RespondBadRequest(w, fmt.Sprintf("Maximum batch size is %d models", maxBatchSize))
		return
	}

	// Get available categories
	categories := h.store.GetCategories()
	visibleCategories := make([]categoryInfo, 0)
	for _, cat := range categories {
		if !cat.IsHidden {
			visibleCategories = append(visibleCategories, categoryInfo{
				ID:   cat.ID,
				Name: cat.Name,
			})
		}
	}

	if len(visibleCategories) == 0 {
		_ = orihttp.RespondBadRequest(w, "No categories available. Please create at least one category first.")
		return
	}

	// Get the configured system model
	systemProvider, systemModel := h.configManager.GetSystemModel()
	result, err := h.llmFactory.GetSystemModelProvider(systemProvider, systemModel)
	if err != nil {
		if errors.Is(err, llm.ErrSystemModelNotConfigured) {
			orihttp.RespondErrorWithErr(w, http.StatusServiceUnavailable,
				"System model not configured. Please configure a system model in Settings to use auto-categorize.", err)
		} else {
			orihttp.RespondErrorWithErr(w, http.StatusServiceUnavailable,
				"System model provider not available: "+err.Error(), err)
		}
		return
	}

	// Get current assignments for context
	currentAssignments := make(map[string]string)
	for _, modelID := range req.ModelIDs {
		assignments := h.store.GetModelAssignments(modelID)
		if len(assignments) > 0 {
			// Get the first category name
			for _, cat := range categories {
				if cat.ID == assignments[0] {
					currentAssignments[modelID] = cat.Name
					break
				}
			}
		}
	}

	// Generate suggestions using LLM
	suggestions, err := h.generateSuggestions(r.Context(), result.Provider, result.Model, req.ModelIDs, visibleCategories, currentAssignments)
	if err != nil {
		logger.Error("Auto-categorize generation failed", logger.Fields{"error": err})
		_ = orihttp.RespondInternalError(w, "Failed to generate suggestions: "+err.Error())
		return
	}

	// Enrich suggestions with category names and current categories
	for i := range suggestions {
		// Add current category
		if current, ok := currentAssignments[suggestions[i].ModelID]; ok {
			suggestions[i].CurrentCategory = current
		}

		// Add suggested category name
		for _, cat := range categories {
			if cat.ID == suggestions[i].CategoryID {
				suggestions[i].CategoryName = cat.Name
				break
			}
		}

		// If no category found, mark as uncategorized
		if suggestions[i].CategoryID != "" && suggestions[i].CategoryName == "" {
			suggestions[i].CategoryID = ""
			suggestions[i].CategoryName = "Uncategorized"
		}
	}

	response := AutoCategorizeResponse{
		Suggestions: suggestions,
	}

	orihttp.WriteJSON(w, response)
}

// categoryInfo is a simplified category struct for the AI prompt
type categoryInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// aiSuggestion represents the AI's response format
type aiSuggestion struct {
	ModelID    string  `json:"model_id"`
	CategoryID string  `json:"category_id"`
	Confidence float64 `json:"confidence"`
	Reasoning  string  `json:"reasoning"`
}

// generateSuggestions uses LLM to analyze model names and suggest categories
func (h *AutoCategorizeHandler) generateSuggestions(
	ctx context.Context,
	provider llm.Provider,
	model string,
	modelIDs []string,
	categories []categoryInfo,
	currentAssignments map[string]string,
) ([]CategorySuggestion, error) {

	// Build category list for prompt
	var categoryList strings.Builder
	for _, cat := range categories {
		categoryList.WriteString(fmt.Sprintf("- ID: %q, Name: %q\n", cat.ID, cat.Name))
	}

	systemPrompt := fmt.Sprintf(`You are a model categorization assistant. Given a list of AI model names and existing categories, suggest the best category for each model.

Available Categories:
%s
For each model, analyze the model name/ID and determine which category best fits based on:
- Model naming conventions (e.g., "mini" = lightweight/tool-calling, "opus" = research-grade)
- Provider patterns (e.g., haiku = fast/cheap, sonnet = balanced, opus = powerful)
- Size indicators (e.g., 7b, 70b parameters suggest capability level)
- Purpose indicators (e.g., "coder", "instruct", "chat")

You must respond with a valid JSON array (and nothing else):
[
  {
    "model_id": "exact-model-id-from-input",
    "category_id": "category-id-from-list-or-empty-string",
    "confidence": 0.0-1.0,
    "reasoning": "Brief explanation (1 sentence max)"
  }
]

Rules:
- Only use category IDs from the provided list above
- Use empty string "" for category_id if no category fits well (confidence should be low)
- Return exactly one suggestion per input model
- Confidence: 0.9+ = very confident, 0.7-0.9 = confident, 0.4-0.7 = uncertain, <0.4 = guess`, categoryList.String())

	// Build user message with model IDs
	var modelList strings.Builder
	modelList.WriteString("Categorize these models:\n")
	for _, modelID := range modelIDs {
		modelList.WriteString(fmt.Sprintf("- %s", modelID))
		if current, ok := currentAssignments[modelID]; ok {
			modelList.WriteString(fmt.Sprintf(" (currently in: %s)", current))
		}
		modelList.WriteString("\n")
	}

	userMessage := modelList.String()

	// Create a context with timeout (2 minutes for local models processing large batches)
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	resp, err := provider.Chat(ctx, llm.ChatRequest{
		Model: model,
		Messages: []llm.Message{
			{Role: "user", Content: userMessage},
		},
		SystemPrompt: systemPrompt,
		Temperature:  0,    // Deterministic output for consistent suggestions
		MaxTokens:    4000, // Allow enough tokens for 50 model suggestions
	})

	if err != nil {
		return nil, fmt.Errorf("LLM request failed: %w", err)
	}

	// Parse the JSON response
	responseText := strings.TrimSpace(resp.Content)

	// Check for empty response
	if responseText == "" {
		return nil, fmt.Errorf("LLM returned empty response - the configured System Model may not support this task. Try using a more capable model like gpt-4o-mini or claude-3-haiku")
	}

	// Try to extract JSON if wrapped in markdown code blocks
	if strings.HasPrefix(responseText, "```") {
		lines := strings.Split(responseText, "\n")
		var jsonLines []string
		inJSON := false
		for _, line := range lines {
			if strings.HasPrefix(line, "```") {
				inJSON = !inJSON
				continue
			}
			if inJSON {
				jsonLines = append(jsonLines, line)
			}
		}
		responseText = strings.Join(jsonLines, "\n")
	}

	var aiSuggestions []aiSuggestion
	if err := json.Unmarshal([]byte(responseText), &aiSuggestions); err != nil {
		return nil, fmt.Errorf("failed to parse LLM response as JSON: %w (response: %s)", err, responseText)
	}

	// Convert to CategorySuggestion and validate
	suggestions := make([]CategorySuggestion, 0, len(aiSuggestions))
	validCategoryIDs := make(map[string]bool)
	for _, cat := range categories {
		validCategoryIDs[cat.ID] = true
	}

	for _, s := range aiSuggestions {
		suggestion := CategorySuggestion{
			ModelID:    s.ModelID,
			CategoryID: s.CategoryID,
			Confidence: s.Confidence,
			Reasoning:  s.Reasoning,
		}

		// Validate category ID
		if suggestion.CategoryID != "" && !validCategoryIDs[suggestion.CategoryID] {
			// Invalid category ID, mark as uncategorized
			suggestion.CategoryID = ""
			suggestion.Confidence = 0.3
			suggestion.Reasoning = "AI suggested invalid category, defaulting to uncategorized"
		}

		// Clamp confidence
		if suggestion.Confidence < 0 {
			suggestion.Confidence = 0
		} else if suggestion.Confidence > 1 {
			suggestion.Confidence = 1
		}

		suggestions = append(suggestions, suggestion)
	}

	// Ensure we have a suggestion for each input model
	suggestedModels := make(map[string]bool)
	for _, s := range suggestions {
		suggestedModels[s.ModelID] = true
	}
	for _, modelID := range modelIDs {
		if !suggestedModels[modelID] {
			suggestions = append(suggestions, CategorySuggestion{
				ModelID:    modelID,
				CategoryID: "",
				Confidence: 0.1,
				Reasoning:  "No suggestion generated",
			})
		}
	}

	return suggestions, nil
}
