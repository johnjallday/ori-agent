package onboardinghttp

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
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
	// onPersonalized is an optional hook invoked after the user saves their
	// profile, used to complete the onboarding "personalize" quest. May be nil.
	onPersonalized func()
}

// SetOnPersonalized registers a hook invoked after the user saves their
// personalization profile.
func (h *SmartOnboardingHandler) SetOnPersonalized(fn func()) {
	h.onPersonalized = fn
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
	provider, model, err := h.getSystemProviderAndModel()
	if err != nil {
		logger.Error("Failed to get LLM provider", logger.Fields{"error": err})
		orihttp.InternalError(w, "Failed to initialize AI provider")
		return
	}

	// Create profiler and infer profile
	prof := profiler.New(provider, model)
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
	provider, model, err := h.getSystemProviderAndModel()
	if err != nil {
		logger.Error("Failed to get LLM provider", logger.Fields{"error": err})
		orihttp.InternalError(w, "Failed to initialize AI provider")
		return
	}

	// Create profiler and infer from description
	prof := profiler.New(provider, model)
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
	providerName, model := h.getConfiguratorDefaults()
	cfg := configurator.New(h.store, providerName, model)
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
	cfg := configurator.New(h.store, "", "")

	// Get list of agents that will be created
	agentsToCreate := cfg.GetCreatedAgentNames(req.Config)

	if err := cfg.ApplyConfig(ctx, req.Config); err != nil {
		logger.Error("Failed to apply config", logger.Fields{"error": err})
		orihttp.InternalError(w, "Failed to apply configuration: "+err.Error())
		return
	}

	// Save user profile to app state
	if req.Config.Profile != nil && h.onboardingMgr != nil {
		userProfile := &types.InferredProfile{
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
	provider, model, err := h.getSystemProviderAndModel()
	if err != nil {
		orihttp.InternalError(w, "Failed to initialize AI provider")
		return
	}

	// Infer new profile
	prof := profiler.New(provider, model)
	profile, err := prof.InferFromApps(ctx, apps)
	if err != nil {
		orihttp.InternalError(w, "Failed to analyze profile")
		return
	}

	// Generate new config suggestions
	providerName, model := h.getConfiguratorDefaults()
	configuratorInstance := configurator.New(h.store, providerName, model)
	config, err := configuratorInstance.GenerateConfig(profile)
	if err != nil {
		orihttp.InternalError(w, "Failed to generate suggestions")
		return
	}

	// Filter to only new agents (not already existing)
	newAgents := configuratorInstance.GetCreatedAgentNames(config)

	h.sendJSON(w, map[string]any{
		"success":          true,
		"profile":          profile,
		"config":           config,
		"new_agents_count": len(newAgents),
		"suggested_agents": newAgents,
	})
}

// getSystemProviderAndModel returns the LLM provider and model for system tasks.
func (h *SmartOnboardingHandler) getSystemProviderAndModel() (llm.Provider, string, error) {
	// Use the configured system provider if set
	if h.systemProvider != "" {
		provider, err := h.llmFactory.GetProvider(h.systemProvider)
		if err == nil {
			model, err := h.resolveModelForProvider(provider, h.systemProvider)
			if err != nil {
				return nil, "", err
			}
			return provider, model, nil
		}
		logger.Warn("Configured system provider not available, falling back", logger.Fields{
			"provider": h.systemProvider,
			"error":    err,
		})
	}

	// Fallback: try providers in order
	providers := []string{"openai", "codex", "claude_code", "claude", "gemini", "ollama", "lmstudio", "mlx_lm"}
	for _, name := range providers {
		provider, err := h.llmFactory.GetProvider(name)
		if err == nil {
			model, err := h.resolveModelForProvider(provider, name)
			if err != nil {
				return nil, "", err
			}
			return provider, model, nil
		}
	}

	return nil, "", fmt.Errorf("no LLM provider available")
}

func (h *SmartOnboardingHandler) resolveModelForProvider(provider llm.Provider, providerName string) (string, error) {
	if h.systemModel != "" && strings.EqualFold(h.systemProvider, providerName) {
		return h.systemModel, nil
	}

	models := provider.DefaultModels()
	if len(models) == 0 {
		return "", fmt.Errorf("no default models available for provider %s", providerName)
	}

	preferred := preferredModelsForProvider(providerName)
	if len(preferred) > 0 {
		available := make(map[string]struct{}, len(models))
		for _, model := range models {
			available[model] = struct{}{}
		}
		for _, model := range preferred {
			if _, ok := available[model]; ok {
				return model, nil
			}
		}
	}

	sort.Strings(models)
	return models[0], nil
}

func (h *SmartOnboardingHandler) getConfiguratorDefaults() (string, string) {
	provider, model, err := h.getSystemProviderAndModel()
	if err != nil {
		logger.Warn("Failed to resolve onboarding defaults", logger.Fields{"error": err})
		return "", ""
	}
	return provider.Name(), model
}

func preferredModelsForProvider(providerName string) []string {
	switch strings.ToLower(providerName) {
	case "openai":
		return []string{"gpt-4o-mini", "gpt-5-nano", "gpt-4o", "gpt-5-mini"}
	case "codex":
		return []string{"gpt-5.1-codex-mini", "gpt-5.1-codex", "gpt-5.1-codex-max"}
	case "claude_code":
		return []string{"opus", "sonnet", "haiku"}
	case "claude":
		return []string{"claude-3-haiku-20240307", "claude-3-5-sonnet-20241022", "claude-3-sonnet-20240229"}
	case "ollama":
		return []string{"llama3", "llama3:latest", "llama2", "mistral"}
	case "lmstudio":
		return []string{"openai/gpt-oss-20b", "mlx-community/Llama-3.2-3B-Instruct-4bit"}
	case "mlx_lm":
		return []string{"mlx-community/Llama-3.2-3B-Instruct-4bit", "mlx-community/Qwen2.5-7B-Instruct-4bit"}
	case "gemini":
		return []string{"gemini-2.5-flash", "gemini-2.5-pro"}
	default:
		return nil
	}
}

// PersonalizeRequest represents a personalization save request.
type PersonalizeRequest struct {
	Interests      []string `json:"interests"`
	PreferredTools []string `json:"preferred_tools"`
	WorkStyle      string   `json:"work_style"`
	Description    string   `json:"description"`
}

// PersonalizeResponse represents the personalization save response.
type PersonalizeResponse struct {
	Success   bool                   `json:"success"`
	Profile   *types.InferredProfile `json:"profile"`
	XPAwarded int64                  `json:"xp_awarded"`
	Message   string                 `json:"message,omitempty"`
}

// SavePersonalization saves user personalization data and awards XP on first completion.
// POST /api/onboarding/personalize
func (h *SmartOnboardingHandler) SavePersonalization(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	var req PersonalizeRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if h.onboardingMgr == nil {
		orihttp.InternalError(w, "Onboarding manager not available")
		return
	}

	// Get existing profile or create a new one
	profile := h.onboardingMgr.GetUserProfile()
	if profile == nil {
		profile = &types.InferredProfile{}
	}

	// Merge personalization fields (preserve detection-derived fields)
	profile.Interests = req.Interests
	profile.PreferredTools = req.PreferredTools
	profile.WorkStyle = req.WorkStyle
	if req.Description != "" {
		profile.Description = req.Description
	}

	// Check if this is first-time personalization
	isFirstTime := profile.PersonalizedAt.IsZero()
	profile.PersonalizedAt = time.Now()

	// Save updated profile
	if err := h.onboardingMgr.SetUserProfile(profile); err != nil {
		logger.Error("Failed to save personalization", logger.Fields{"error": err})
		orihttp.InternalError(w, "Failed to save personalization")
		return
	}

	// Award XP on first-time personalization
	var xpAwarded int64
	if isFirstTime {
		xpAwarded = 25
		progress := h.onboardingMgr.GetAssistantProgress()
		progress.Experience += xpAwarded
		progress.Level = int(progress.Experience / 100)

		// Update rank based on level
		switch {
		case progress.Level >= 10:
			progress.Rank = "master"
		case progress.Level >= 5:
			progress.Rank = "expert"
		case progress.Level >= 2:
			progress.Rank = "intermediate"
		case progress.Level >= 1:
			progress.Rank = "beginner"
		default:
			progress.Rank = "novice"
		}
		progress.UpdatedAt = time.Now()

		if err := h.onboardingMgr.SetAssistantProgress(&progress); err != nil {
			logger.Error("Failed to award personalization XP", logger.Fields{"error": err})
			// Non-fatal: continue with success
		}
	}

	if h.onPersonalized != nil {
		h.onPersonalized()
	}

	h.sendJSON(w, PersonalizeResponse{
		Success:   true,
		Profile:   profile,
		XPAwarded: xpAwarded,
	})
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

	h.sendJSON(w, map[string]any{
		"success": true,
		"profile": profile,
	})
}

// sendJSON sends a JSON response.
func (h *SmartOnboardingHandler) sendJSON(w http.ResponseWriter, data any) {
	orihttp.WriteJSON(w, data)
}
