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
}

// CompleteStepRequest represents a request to complete a step
type CompleteStepRequest struct {
	StepName string `json:"step_name"`
}

// GetStatus checks if onboarding is needed and returns current state
// GET /api/onboarding/status
func (h *Handler) GetStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
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
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// CompleteStep marks a step as completed and advances to the next step
// POST /api/onboarding/step
func (h *Handler) CompleteStep(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	var req CompleteStepRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if err := orihttp.RespondBadRequest(w, "Invalid request body"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if req.StepName == "" {
		if err := orihttp.RespondBadRequest(w, "step_name is required"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if err := h.onboardingMgr.CompleteStep(req.StepName); err != nil {
		if err := orihttp.RespondInternalError(w, "Failed to complete step: "+err.Error()); err != nil {
			logger.

				// Return updated status
				Error("Failed to write response", logger.Fields{"error": err})
		}
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
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// Skip marks onboarding as skipped
// POST /api/onboarding/skip
func (h *Handler) Skip(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if err := h.onboardingMgr.SkipOnboarding(); err != nil {
		if err := orihttp.RespondInternalError(w, "Failed to skip onboarding: "+err.Error()); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]bool{"success": true}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// Complete marks onboarding as completed
// POST /api/onboarding/complete
func (h *Handler) Complete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if err := h.onboardingMgr.CompleteOnboarding(); err != nil {
		if err := orihttp.RespondInternalError(w, "Failed to complete onboarding: "+err.Error()); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]bool{"success": true}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// Reset resets onboarding state (useful for testing)
// POST /api/onboarding/reset
func (h *Handler) Reset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if err := h.onboardingMgr.ResetOnboarding(); err != nil {
		if err := orihttp.RespondInternalError(w, "Failed to reset onboarding: "+err.Error()); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]bool{"success": true}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
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
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	theme := h.onboardingMgr.GetTheme()

	response := ThemeResponse{
		Theme: theme,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// SetTheme sets the theme preference
// POST /api/theme
func (h *Handler) SetTheme(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	var req SetThemeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if err := orihttp.RespondBadRequest(w, "Invalid request body"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if req.Theme == "" {
		if err := orihttp.RespondBadRequest(w, "theme is required"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if err := h.onboardingMgr.SetTheme(req.Theme); err != nil {
		if err := orihttp.RespondBadRequest(w, "Failed to set theme: "+err.Error()); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	response := ThemeResponse(req)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}
