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

// sendJSON sends a JSON response.
func (h *SmartOnboardingHandler) sendJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(data); err != nil {
		logger.Error("Failed to encode JSON response", logger.Fields{"error": err})
	}
}
