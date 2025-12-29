package onboardinghttp

import (
	"encoding/json"
	"net/http"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/onboarding"
)

// Handler handles HTTP requests for onboarding
type Handler struct {
	onboardingMgr *onboarding.Manager
}

// NewHandler creates a new onboarding HTTP handler
func NewHandler(onboardingMgr *onboarding.Manager) *Handler {
	return &Handler{
		onboardingMgr: onboardingMgr,
	}
}

// StatusResponse represents the onboarding status response
type StatusResponse struct {
	NeedsOnboarding bool     `json:"needs_onboarding"`
	CurrentStep     int      `json:"current_step"`
	Completed       bool     `json:"completed"`
	Skipped         bool     `json:"skipped"`
	StepsCompleted  []string `json:"steps_completed"`
	StepsSkipped    []string `json:"steps_skipped,omitempty"`
}

// SkipStepRequest represents a request to skip a step
type SkipStepRequest struct {
	StepName string `json:"step_name"`
}

// CompleteStepRequest represents a request to complete a step
type CompleteStepRequest struct {
	StepName string `json:"step_name"`
}

// GetStatus checks if onboarding is needed and returns current state
// GET /api/onboarding/status
func (h *Handler) GetStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	state := h.onboardingMgr.GetState()
	isComplete := h.onboardingMgr.IsOnboardingComplete()

	response := StatusResponse{
		NeedsOnboarding: !isComplete,
		CurrentStep:     state.CurrentStep,
		Completed:       state.Completed,
		Skipped:         !state.SkippedAt.IsZero(),
		StepsCompleted:  state.StepsCompleted,
		StepsSkipped:    state.StepsSkipped,
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(response); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// CompleteStep marks a step as completed and advances to the next step
// POST /api/onboarding/step
func (h *Handler) CompleteStep(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	var req CompleteStepRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if req.StepName == "" {
		orihttp.BadRequest(w, "step_name is required")
		return
	}

	if err := h.onboardingMgr.CompleteStep(req.StepName); err != nil {
		orihttp.InternalError(w, "Failed to complete step: "+err.Error())
		return
	}

	// Return updated status

	state := h.onboardingMgr.GetState()
	isComplete := h.onboardingMgr.IsOnboardingComplete()

	response := StatusResponse{
		NeedsOnboarding: !isComplete,
		CurrentStep:     state.CurrentStep,
		Completed:       state.Completed,
		Skipped:         !state.SkippedAt.IsZero(),
		StepsCompleted:  state.StepsCompleted,
		StepsSkipped:    state.StepsSkipped,
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(response); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// SkipStep marks a step as skipped and advances to the next step
// POST /api/onboarding/skip-step
func (h *Handler) SkipStep(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	var req SkipStepRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if req.StepName == "" {
		orihttp.BadRequest(w, "step_name is required")
		return
	}

	if err := h.onboardingMgr.SkipStep(req.StepName); err != nil {
		orihttp.InternalError(w, "Failed to skip step: "+err.Error())
		return
	}

	// Return updated status
	state := h.onboardingMgr.GetState()
	isComplete := h.onboardingMgr.IsOnboardingComplete()

	response := StatusResponse{
		NeedsOnboarding: !isComplete,
		CurrentStep:     state.CurrentStep,
		Completed:       state.Completed,
		Skipped:         !state.SkippedAt.IsZero(),
		StepsCompleted:  state.StepsCompleted,
		StepsSkipped:    state.StepsSkipped,
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(response); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// Skip marks onboarding as skipped
// POST /api/onboarding/skip
func (h *Handler) Skip(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	if err := h.onboardingMgr.SkipOnboarding(); err != nil {
		orihttp.InternalError(w, "Failed to skip onboarding: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]bool{"success": true}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// Complete marks onboarding as completed
// POST /api/onboarding/complete
func (h *Handler) Complete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	if err := h.onboardingMgr.CompleteOnboarding(); err != nil {
		orihttp.InternalError(w, "Failed to complete onboarding: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]bool{"success": true}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// Reset resets onboarding state (useful for testing)
// POST /api/onboarding/reset
func (h *Handler) Reset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	if err := h.onboardingMgr.ResetOnboarding(); err != nil {
		orihttp.InternalError(w, "Failed to reset onboarding: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]bool{"success": true}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// ThemeResponse represents the theme response
type ThemeResponse struct {
	Theme string `json:"theme"`
}

// SetThemeRequest represents a request to set the theme
type SetThemeRequest struct {
	Theme string `json:"theme"`
}

// GetTheme returns the current theme preference
// GET /api/theme
func (h *Handler) GetTheme(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	theme := h.onboardingMgr.GetTheme()

	response := ThemeResponse{
		Theme: theme,
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(response); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// SetTheme sets the theme preference
// POST /api/theme
func (h *Handler) SetTheme(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	var req SetThemeRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if req.Theme == "" {
		orihttp.BadRequest(w, "theme is required")
		return
	}

	if err := h.onboardingMgr.SetTheme(req.Theme); err != nil {
		orihttp.BadRequest(w, "Failed to set theme: "+err.Error())
		return
	}

	response := ThemeResponse(req)

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(response); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}
