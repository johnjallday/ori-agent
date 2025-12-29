package onboardinghttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/onboarding"
	"github.com/johnjallday/ori-agent/internal/onboarding/configurator"
	"github.com/johnjallday/ori-agent/internal/onboarding/detector"
	"github.com/johnjallday/ori-agent/internal/onboarding/profiler"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

// SmartOnboardingHandler handles HTTP requests for smart onboarding.
type SmartOnboardingHandler struct {
	store          store.Store
	llmFactory     *llm.Factory
	onboardingMgr  *onboarding.Manager
	systemProvider string
	systemModel    string
}

// NewSmartOnboardingHandler creates a new smart onboarding handler.
func NewSmartOnboardingHandler(s store.Store, llmFactory *llm.Factory, onboardingMgr *onboarding.Manager, systemProvider, systemModel string) *SmartOnboardingHandler {
	return &SmartOnboardingHandler{
		store:          s,
		llmFactory:     llmFactory,
		onboardingMgr:  onboardingMgr,
		systemProvider: systemProvider,
		systemModel:    systemModel,
	}
}

// DetectResponse represents the app detection response.
type DetectResponse struct {
	Success  bool                   `json:"success"`
	Apps     []detector.DetectedApp `json:"apps"`
	Platform string                 `json:"platform"`
	Message  string                 `json:"message,omitempty"`
}

// ProfileResponse represents the profile inference response.
type ProfileResponse struct {
	Success bool                  `json:"success"`
	Profile *profiler.UserProfile `json:"profile"`
	Message string                `json:"message,omitempty"`
}

// ConfigResponse represents the configuration preview response.
type ConfigResponse struct {
	Success bool                           `json:"success"`
	Config  *configurator.OnboardingConfig `json:"config"`
	Message string                         `json:"message,omitempty"`
}

// ApplyResponse represents the apply configuration response.
type ApplyResponse struct {
	Success       bool     `json:"success"`
	AgentsCreated []string `json:"agents_created"`
	Message       string   `json:"message,omitempty"`
}

// DescribeRequest represents a self-description request.
type DescribeRequest struct {
	Description string `json:"description"`
}

// ApplyRequest represents a request to apply configuration.
type ApplyRequest struct {
	Config *configurator.OnboardingConfig `json:"config"`
}

// PluginRecommendationRequest represents a request for AI plugin recommendations.
type PluginRecommendationRequest struct {
	Profile  *profiler.UserProfile `json:"profile"`
	Plugins  []PluginInfo          `json:"plugins"`
	MaxCount int                   `json:"max_count,omitempty"`
}

// PluginInfo represents basic plugin information for recommendation.
type PluginInfo struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags,omitempty"`
}

// PluginRecommendation represents an AI-generated plugin recommendation.
type PluginRecommendation struct {
	Name      string `json:"name"`
	Reason    string `json:"reason"`
	Relevance string `json:"relevance"` // "high", "medium", "low"
	UseCase   string `json:"use_case,omitempty"`
}

// PluginRecommendationResponse represents the plugin recommendation response.
type PluginRecommendationResponse struct {
	Success         bool                   `json:"success"`
	Recommendations []PluginRecommendation `json:"recommendations"`
	Message         string                 `json:"message,omitempty"`
}

// Detect handles app detection requests.
// POST /api/onboarding/detect
func (h *SmartOnboardingHandler) Detect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	// Create detector for current platform
	cfg := detector.DefaultConfig()
	det, err := detector.New(cfg)
	if err != nil {
		logger.Error("Failed to create detector", logger.Fields{"error": err})
		orihttp.InternalError(w, "Failed to initialize app detection")
		return
	}

	// Detect apps
	apps, err := det.DetectApps(ctx)
	if err != nil {
		logger.Error("Failed to detect apps", logger.Fields{"error": err})
		// Return empty list instead of error - fallback to self-description
		h.sendJSON(w, DetectResponse{
			Success:  true,
			Apps:     []detector.DetectedApp{},
			Platform: det.Platform(),
			Message:  "No apps detected. Please describe yourself instead.",
		})
		return
	}

	h.sendJSON(w, DetectResponse{
		Success:  true,
		Apps:     apps,
		Platform: det.Platform(),
	})
}

// InferProfile handles profile inference from detected apps.
// POST /api/onboarding/profile
func (h *SmartOnboardingHandler) InferProfile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	var req struct {
		Apps []detector.DetectedApp `json:"apps"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	// Get LLM provider for profile inference
	provider, err := h.getSystemProvider()
	if err != nil {
		logger.Error("Failed to get LLM provider", logger.Fields{"error": err})
		orihttp.InternalError(w, "Failed to initialize AI provider")
		return
	}

	// Create profiler and infer profile
	prof := profiler.New(provider, h.systemModel)
	profile, err := prof.InferFromApps(ctx, req.Apps)
	if err != nil {
		logger.Error("Failed to infer profile", logger.Fields{"error": err})
		orihttp.InternalError(w, "Failed to analyze profile")
		return
	}

	h.sendJSON(w, ProfileResponse{
		Success: true,
		Profile: profile,
	})
}

// Describe handles profile inference from user self-description.
// POST /api/onboarding/describe
func (h *SmartOnboardingHandler) Describe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	var req DescribeRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if req.Description == "" {
		orihttp.BadRequest(w, "description is required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	// Get LLM provider
	provider, err := h.getSystemProvider()
	if err != nil {
		logger.Error("Failed to get LLM provider", logger.Fields{"error": err})
		orihttp.InternalError(w, "Failed to initialize AI provider")
		return
	}

	// Create profiler and infer from description
	prof := profiler.New(provider, h.systemModel)
	profile, err := prof.InferFromDescription(ctx, req.Description)
	if err != nil {
		logger.Error("Failed to infer profile from description", logger.Fields{"error": err})
		orihttp.InternalError(w, "Failed to analyze description")
		return
	}

	h.sendJSON(w, ProfileResponse{
		Success: true,
		Profile: profile,
	})
}

// GenerateConfig generates an onboarding configuration from a profile.
// POST /api/onboarding/config
func (h *SmartOnboardingHandler) GenerateConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	var req struct {
		Profile *profiler.UserProfile `json:"profile"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if req.Profile == nil {
		orihttp.BadRequest(w, "profile is required")
		return
	}

	// Create configurator and generate config
	cfg := configurator.New(h.store)
	config, err := cfg.GenerateConfig(req.Profile)
	if err != nil {
		logger.Error("Failed to generate config", logger.Fields{"error": err})
		orihttp.InternalError(w, "Failed to generate configuration")
		return
	}

	h.sendJSON(w, ConfigResponse{
		Success: true,
		Config:  config,
	})
}

// Apply applies the onboarding configuration to create agents.
// POST /api/onboarding/apply
func (h *SmartOnboardingHandler) Apply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	var req ApplyRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if req.Config == nil {
		orihttp.BadRequest(w, "config is required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	// Create configurator and apply config
	cfg := configurator.New(h.store)

	// Get list of agents that will be created
	agentsToCreate := cfg.GetCreatedAgentNames(req.Config)

	if err := cfg.ApplyConfig(ctx, req.Config); err != nil {
		logger.Error("Failed to apply config", logger.Fields{"error": err})
		orihttp.InternalError(w, "Failed to apply configuration: "+err.Error())
		return
	}

	// Save user profile to app state
	if req.Config.Profile != nil && h.onboardingMgr != nil {
		userProfile := &types.UserProfile{
			PrimaryCategory:     string(req.Config.Profile.PrimaryCategory),
			SecondaryCategories: make([]string, len(req.Config.Profile.SecondaryCategories)),
			Specializations:     req.Config.Profile.Specializations,
			Summary:             req.Config.Profile.Summary,
			Confidence:          req.Config.Profile.Confidence,
			DetectedApps:        req.Config.Profile.DetectedApps,
		}
		for i, cat := range req.Config.Profile.SecondaryCategories {
			userProfile.SecondaryCategories[i] = string(cat)
		}
		if err := h.onboardingMgr.SetUserProfile(userProfile); err != nil {
			logger.Error("Failed to save user profile", logger.Fields{"error": err})
			// Non-fatal: continue with success response
		}
	}

	h.sendJSON(w, ApplyResponse{
		Success:       true,
		AgentsCreated: agentsToCreate,
		Message:       "Configuration applied successfully",
	})
}

// UpdateProfile re-scans apps and suggests updates.
// POST /api/onboarding/update-profile
func (h *SmartOnboardingHandler) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	// Detect apps
	cfg := detector.DefaultConfig()
	det, err := detector.New(cfg)
	if err != nil {
		orihttp.InternalError(w, "Failed to initialize app detection")
		return
	}

	apps, err := det.DetectApps(ctx)
	if err != nil {
		orihttp.InternalError(w, "Failed to detect apps")
		return
	}

	// Get LLM provider
	provider, err := h.getSystemProvider()
	if err != nil {
		orihttp.InternalError(w, "Failed to initialize AI provider")
		return
	}

	// Infer new profile
	prof := profiler.New(provider, h.systemModel)
	profile, err := prof.InferFromApps(ctx, apps)
	if err != nil {
		orihttp.InternalError(w, "Failed to analyze profile")
		return
	}

	// Generate new config suggestions
	configuratorInstance := configurator.New(h.store)
	config, err := configuratorInstance.GenerateConfig(profile)
	if err != nil {
		orihttp.InternalError(w, "Failed to generate suggestions")
		return
	}

	// Filter to only new agents (not already existing)
	newAgents := configuratorInstance.GetCreatedAgentNames(config)

	h.sendJSON(w, map[string]interface{}{
		"success":          true,
		"profile":          profile,
		"config":           config,
		"new_agents_count": len(newAgents),
		"suggested_agents": newAgents,
	})
}

// getSystemProvider returns the LLM provider for system tasks.
func (h *SmartOnboardingHandler) getSystemProvider() (llm.Provider, error) {
	// Use the configured system provider if set
	if h.systemProvider != "" {
		provider, err := h.llmFactory.GetProvider(h.systemProvider)
		if err == nil {
			return provider, nil
		}
		logger.Warn("Configured system provider not available, falling back", logger.Fields{
			"provider": h.systemProvider,
			"error":    err,
		})
	}

	// Fallback: try providers in order
	providers := []string{"openai", "anthropic", "ollama"}
	for _, name := range providers {
		provider, err := h.llmFactory.GetProvider(name)
		if err == nil {
			return provider, nil
		}
	}

	return nil, fmt.Errorf("no LLM provider available")
}

// GetStoredProfile returns the user's stored profile.
// GET /api/onboarding/user-profile
func (h *SmartOnboardingHandler) GetStoredProfile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	if h.onboardingMgr == nil {
		orihttp.InternalError(w, "Onboarding manager not available")
		return
	}

	profile := h.onboardingMgr.GetUserProfile()

	h.sendJSON(w, map[string]interface{}{
		"success": true,
		"profile": profile,
	})
}

// RecommendPlugins generates AI-powered plugin recommendations based on user profile.
// POST /api/onboarding/recommend-plugins
func (h *SmartOnboardingHandler) RecommendPlugins(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	var req PluginRecommendationRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if req.Profile == nil {
		orihttp.BadRequest(w, "profile is required")
		return
	}

	if len(req.Plugins) == 0 {
		orihttp.BadRequest(w, "plugins list is required")
		return
	}

	// Default max count
	maxCount := req.MaxCount
	if maxCount <= 0 {
		maxCount = 5
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	// Get LLM provider
	provider, err := h.getSystemProvider()
	if err != nil {
		logger.Error("Failed to get LLM provider", logger.Fields{"error": err})
		orihttp.InternalError(w, "Failed to initialize AI provider")
		return
	}

	// Build the prompt for plugin recommendations
	prompt := h.buildPluginRecommendationPrompt(req.Profile, req.Plugins, maxCount)

	// Call the LLM
	chatReq := llm.ChatRequest{
		Model: h.systemModel,
		Messages: []llm.Message{
			{Role: "user", Content: prompt},
		},
	}

	response, err := provider.Chat(ctx, chatReq)
	if err != nil {
		logger.Error("Failed to get plugin recommendations from LLM", logger.Fields{"error": err})
		orihttp.InternalError(w, "Failed to generate recommendations")
		return
	}

	// Parse the LLM response
	recommendations, err := h.parsePluginRecommendations(response.Content, req.Plugins)
	if err != nil {
		logger.Warn("Failed to parse LLM recommendations, using fallback", logger.Fields{"error": err})
		// Fallback to basic recommendations based on tags
		recommendations = h.fallbackRecommendations(req.Profile, req.Plugins, maxCount)
	}

	h.sendJSON(w, PluginRecommendationResponse{
		Success:         true,
		Recommendations: recommendations,
	})
}

// buildPluginRecommendationPrompt creates the LLM prompt for plugin recommendations.
func (h *SmartOnboardingHandler) buildPluginRecommendationPrompt(profile *profiler.UserProfile, plugins []PluginInfo, maxCount int) string {
	// Build profile summary
	profileSummary := fmt.Sprintf("User Profile:\n- Primary role: %s\n", profile.PrimaryCategory)
	if len(profile.SecondaryCategories) > 0 {
		profileSummary += fmt.Sprintf("- Secondary interests: %v\n", profile.SecondaryCategories)
	}
	if len(profile.Specializations) > 0 {
		profileSummary += fmt.Sprintf("- Specializations: %v\n", profile.Specializations)
	}
	if profile.Summary != "" {
		profileSummary += fmt.Sprintf("- Summary: %s\n", profile.Summary)
	}

	// Build plugins list
	pluginsList := "Available Plugins:\n"
	for _, p := range plugins {
		pluginsList += fmt.Sprintf("- %s: %s", p.Name, p.Description)
		if len(p.Tags) > 0 {
			pluginsList += fmt.Sprintf(" (tags: %v)", p.Tags)
		}
		pluginsList += "\n"
	}

	return fmt.Sprintf(`You are an AI assistant helping to recommend plugins for a user based on their profile.

%s
%s
Please recommend up to %d plugins that would be most useful for this user. For each recommendation, provide:
1. The exact plugin name (must match one from the list)
2. A brief reason why this plugin is relevant (1-2 sentences)
3. Relevance level: "high", "medium", or "low"
4. A specific use case example

Respond in JSON format only, with no additional text:
{
  "recommendations": [
    {
      "name": "plugin-name",
      "reason": "Why this plugin is useful for this user",
      "relevance": "high",
      "use_case": "Example: Use this to..."
    }
  ]
}`, profileSummary, pluginsList, maxCount)
}

// parsePluginRecommendations parses the LLM response into PluginRecommendation structs.
func (h *SmartOnboardingHandler) parsePluginRecommendations(content string, availablePlugins []PluginInfo) ([]PluginRecommendation, error) {
	// Try to extract JSON from the response
	content = extractJSON(content)

	var response struct {
		Recommendations []PluginRecommendation `json:"recommendations"`
	}

	if err := json.Unmarshal([]byte(content), &response); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	// Build a set of valid plugin names
	validNames := make(map[string]bool)
	for _, p := range availablePlugins {
		validNames[p.Name] = true
	}

	// Filter to only valid plugin names and validate relevance
	var valid []PluginRecommendation
	for _, rec := range response.Recommendations {
		if validNames[rec.Name] {
			// Normalize relevance
			switch rec.Relevance {
			case "high", "medium", "low":
				// valid
			default:
				rec.Relevance = "medium"
			}
			valid = append(valid, rec)
		}
	}

	if len(valid) == 0 {
		return nil, fmt.Errorf("no valid recommendations found")
	}

	return valid, nil
}

// extractJSON attempts to extract JSON from a string that may contain markdown code blocks.
func extractJSON(content string) string {
	// Check for JSON code block
	if idx := findIndex(content, "```json"); idx != -1 {
		start := idx + 7
		if end := findIndex(content[start:], "```"); end != -1 {
			return content[start : start+end]
		}
	}
	// Check for generic code block
	if idx := findIndex(content, "```"); idx != -1 {
		start := idx + 3
		// Skip language identifier if present
		if nlIdx := findIndex(content[start:], "\n"); nlIdx != -1 {
			start += nlIdx + 1
		}
		if end := findIndex(content[start:], "```"); end != -1 {
			return content[start : start+end]
		}
	}
	// Try to find JSON object directly
	if idx := findIndex(content, "{"); idx != -1 {
		// Find matching closing brace
		depth := 0
		for i := idx; i < len(content); i++ {
			switch content[i] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					return content[idx : i+1]
				}
			}
		}
	}
	return content
}

// findIndex returns the index of substr in s, or -1 if not found.
func findIndex(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// fallbackRecommendations provides basic recommendations when LLM fails.
func (h *SmartOnboardingHandler) fallbackRecommendations(profile *profiler.UserProfile, plugins []PluginInfo, maxCount int) []PluginRecommendation {
	var recommendations []PluginRecommendation

	// Map profile categories to relevant tags
	categoryTags := map[profiler.ProfileCategory][]string{
		profiler.CategoryDeveloper:      {"development", "code", "programming", "git", "api"},
		profiler.CategoryDesigner:       {"design", "creative", "image", "ui", "ux"},
		profiler.CategoryWriter:         {"writing", "text", "content", "notes", "documents"},
		profiler.CategoryDataScientist:  {"data", "analytics", "analysis", "math", "statistics"},
		profiler.CategoryDevOps:         {"devops", "cloud", "deployment", "docker", "kubernetes"},
		profiler.CategoryProjectManager: {"project", "tasks", "planning", "organization", "productivity"},
		profiler.CategoryGeneral:        {"general", "utility", "productivity", "tools"},
	}

	relevantTags := categoryTags[profile.PrimaryCategory]
	for _, cat := range profile.SecondaryCategories {
		relevantTags = append(relevantTags, categoryTags[cat]...)
	}

	// Score plugins by tag matches
	type scoredPlugin struct {
		plugin PluginInfo
		score  int
	}
	var scored []scoredPlugin

	for _, p := range plugins {
		score := 0
		for _, tag := range p.Tags {
			for _, relTag := range relevantTags {
				if tag == relTag {
					score++
				}
			}
		}
		if score > 0 {
			scored = append(scored, scoredPlugin{plugin: p, score: score})
		}
	}

	// Sort by score (simple bubble sort for small lists)
	for i := 0; i < len(scored); i++ {
		for j := i + 1; j < len(scored); j++ {
			if scored[j].score > scored[i].score {
				scored[i], scored[j] = scored[j], scored[i]
			}
		}
	}

	// Take top recommendations
	for i := 0; i < len(scored) && i < maxCount; i++ {
		relevance := "medium"
		if scored[i].score >= 3 {
			relevance = "high"
		} else if scored[i].score == 1 {
			relevance = "low"
		}

		recommendations = append(recommendations, PluginRecommendation{
			Name:      scored[i].plugin.Name,
			Reason:    fmt.Sprintf("Matches your %s profile based on plugin capabilities.", profile.PrimaryCategory),
			Relevance: relevance,
		})
	}

	return recommendations
}

// sendJSON sends a JSON response.
func (h *SmartOnboardingHandler) sendJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(data); err != nil {
		logger.Error("Failed to encode JSON response", logger.Fields{"error": err})
	}
}
