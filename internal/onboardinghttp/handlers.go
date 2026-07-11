package onboardinghttp

import (
	"encoding/json"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/onboarding"
)

// Handler handles HTTP requests for onboarding
type Handler struct {
	onboardingMgr *onboarding.Manager
	// onNamesSaved is an optional hook invoked after names are persisted,
	// used to complete the onboarding "personalize" quest. May be nil.
	onNamesSaved func(assistantName string)
}

// NewHandler creates a new onboarding HTTP handler
func NewHandler(onboardingMgr *onboarding.Manager) *Handler {
	return &Handler{
		onboardingMgr: onboardingMgr,
	}
}

// SetOnNamesSaved registers a hook invoked after onboarding names are saved.
func (h *Handler) SetOnNamesSaved(fn func(assistantName string)) {
	h.onNamesSaved = fn
}

// StatusResponse represents the onboarding status response
type StatusResponse struct {
	NeedsOnboarding bool     `json:"needs_onboarding"`
	CurrentStep     int      `json:"current_step"`
	Completed       bool     `json:"completed"`
	Skipped         bool     `json:"skipped"`
	StepsCompleted  []string `json:"steps_completed"`
	StepsSkipped    []string `json:"steps_skipped,omitempty"`
	UserName        string   `json:"user_name,omitempty"`
	AssistantName   string   `json:"assistant_name,omitempty"`
	Timezone        string   `json:"timezone,omitempty"`
}

// SkipStepRequest represents a request to skip a step
type SkipStepRequest struct {
	StepName string `json:"step_name"`
}

// CompleteStepRequest represents a request to complete a step
type CompleteStepRequest struct {
	StepName string `json:"step_name"`
}

// NamesRequest represents a request to save onboarding names.
type NamesRequest struct {
	UserName      string `json:"user_name"`
	AssistantName string `json:"assistant_name"`
}

type TimezoneRequest struct {
	Timezone string `json:"timezone"`
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
	userName, assistantName := h.onboardingMgr.GetNames()
	timezone := h.onboardingMgr.GetTimezone()

	response := StatusResponse{
		NeedsOnboarding: !isComplete,
		CurrentStep:     state.CurrentStep,
		Completed:       state.Completed,
		Skipped:         !state.SkippedAt.IsZero(),
		StepsCompleted:  state.StepsCompleted,
		StepsSkipped:    state.StepsSkipped,
		UserName:        userName,
		AssistantName:   assistantName,
		Timezone:        timezone,
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
	userName, assistantName := h.onboardingMgr.GetNames()
	timezone := h.onboardingMgr.GetTimezone()

	response := StatusResponse{
		NeedsOnboarding: !isComplete,
		CurrentStep:     state.CurrentStep,
		Completed:       state.Completed,
		Skipped:         !state.SkippedAt.IsZero(),
		StepsCompleted:  state.StepsCompleted,
		StepsSkipped:    state.StepsSkipped,
		UserName:        userName,
		AssistantName:   assistantName,
		Timezone:        timezone,
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
	userName, assistantName := h.onboardingMgr.GetNames()
	timezone := h.onboardingMgr.GetTimezone()

	response := StatusResponse{
		NeedsOnboarding: !isComplete,
		CurrentStep:     state.CurrentStep,
		Completed:       state.Completed,
		Skipped:         !state.SkippedAt.IsZero(),
		StepsCompleted:  state.StepsCompleted,
		StepsSkipped:    state.StepsSkipped,
		UserName:        userName,
		AssistantName:   assistantName,
		Timezone:        timezone,
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

// SaveNames persists onboarding user/assistant names.
// POST /api/onboarding/names
func (h *Handler) SaveNames(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	var req NamesRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	assistantName := strings.TrimSpace(req.AssistantName)
	if assistantName == "" {
		assistantName = onboarding.DefaultAssistantName
	}
	if len(assistantName) > 60 {
		orihttp.BadRequest(w, "assistant_name must be 60 characters or fewer")
		return
	}

	if len(strings.TrimSpace(req.UserName)) > 60 {
		orihttp.BadRequest(w, "user_name must be 60 characters or fewer")
		return
	}

	if err := h.onboardingMgr.SetNames(strings.TrimSpace(req.UserName), assistantName); err != nil {
		orihttp.InternalError(w, "Failed to save onboarding names: "+err.Error())
		return
	}

	userName, persistedAssistantName := h.onboardingMgr.GetNames()
	if h.onNamesSaved != nil {
		h.onNamesSaved(persistedAssistantName)
	}
	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"success":        true,
		"user_name":      userName,
		"assistant_name": persistedAssistantName,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// SaveTimezone persists onboarding timezone.
// POST /api/onboarding/timezone
func (h *Handler) SaveTimezone(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	var req TimezoneRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	timezone := strings.TrimSpace(req.Timezone)
	if len(timezone) > 80 {
		orihttp.BadRequest(w, "timezone must be 80 characters or fewer")
		return
	}
	if err := h.onboardingMgr.SetTimezone(timezone); err != nil {
		orihttp.InternalError(w, "Failed to save onboarding timezone: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"success":  true,
		"timezone": h.onboardingMgr.GetTimezone(),
	}); encErr != nil {
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

// NotesOpenBehaviorResponse / SetNotesOpenBehaviorRequest carry the
// preference for how note-open clicks route in the UI.
type NotesOpenBehaviorResponse struct {
	Behavior string `json:"behavior"`
}

type SetNotesOpenBehaviorRequest struct {
	Behavior string `json:"behavior"`
}

// GetNotesOpenBehavior returns the current notes-open-behavior preference.
// GET /api/notes-open-behavior
func (h *Handler) GetNotesOpenBehavior(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	behavior := h.onboardingMgr.GetNotesOpenBehavior()

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(NotesOpenBehaviorResponse{Behavior: behavior}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// SetNotesOpenBehavior persists the user's notes-open-behavior preference.
// POST /api/notes-open-behavior  body: {"behavior":"modal|page|page-new-tab"}
func (h *Handler) SetNotesOpenBehavior(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	var req SetNotesOpenBehaviorRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if req.Behavior == "" {
		orihttp.BadRequest(w, "behavior is required")
		return
	}

	if err := h.onboardingMgr.SetNotesOpenBehavior(req.Behavior); err != nil {
		orihttp.BadRequest(w, "Failed to set notes_open_behavior: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(NotesOpenBehaviorResponse(req)); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}
