// Package progressionhttp exposes the onboarding quest-log progression state
// over HTTP: the quest graph with per-quest status, plus dismiss/reset.
package progressionhttp

import (
	"errors"
	"net/http"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/progression"
)

// Handler serves the progression API.
type Handler struct {
	engine *progression.Engine
}

// NewHandler creates a progression HTTP handler backed by the given engine.
func NewHandler(engine *progression.Engine) *Handler {
	return &Handler{engine: engine}
}

// GetStatus returns the full quest graph with derived status and current tier.
// GET /api/progression
func (h *Handler) GetStatus(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	if h.engine == nil {
		_ = orihttp.RespondServiceUnavailable(w, "progression is not available")
		return
	}
	_ = orihttp.RespondSuccess(w, h.engine.Status())
}

// DismissRequest toggles whether the quest-log widget is hidden.
type DismissRequest struct {
	Dismissed bool `json:"dismissed"`
}

// Dismiss persists the widget-hidden preference and returns updated status.
// POST /api/progression/dismiss  body: {"dismissed": true}
func (h *Handler) Dismiss(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	if h.engine == nil {
		_ = orihttp.RespondServiceUnavailable(w, "progression is not available")
		return
	}
	var req DismissRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if err := h.engine.SetDismissed(req.Dismissed); err != nil {
		_ = orihttp.RespondInternalError(w, "failed to update progression: "+err.Error())
		return
	}
	_ = orihttp.RespondSuccess(w, h.engine.Status())
}

// SkipRequest identifies which optional quest to skip.
type SkipRequest struct {
	QuestID string `json:"quest_id"`
}

// Skip marks a single optional quest as explicitly skipped and returns
// updated status. Kept separate from Dismiss, which hides the whole widget
// rather than resolving one quest.
// POST /api/progression/skip  body: {"quest_id": "t2-build-hq"}
func (h *Handler) Skip(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	if h.engine == nil {
		_ = orihttp.RespondServiceUnavailable(w, "progression is not available")
		return
	}
	var req SkipRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if req.QuestID == "" {
		_ = orihttp.RespondBadRequest(w, "quest_id is required")
		return
	}
	err := h.engine.Skip(req.QuestID)
	switch {
	case err == nil:
		_ = orihttp.RespondSuccess(w, h.engine.Status())
	case errors.Is(err, progression.ErrQuestNotFound):
		_ = orihttp.RespondNotFound(w, err.Error())
	case errors.Is(err, progression.ErrQuestNotOptional):
		_ = orihttp.RespondBadRequest(w, err.Error())
	default:
		_ = orihttp.RespondInternalError(w, "failed to skip quest: "+err.Error())
	}
}

// Reset clears all progression state (dev/test parity with onboarding reset).
// POST /api/progression/reset
func (h *Handler) Reset(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	if h.engine == nil {
		_ = orihttp.RespondServiceUnavailable(w, "progression is not available")
		return
	}
	if err := h.engine.Reset(); err != nil {
		_ = orihttp.RespondInternalError(w, "failed to reset progression: "+err.Error())
		return
	}
	_ = orihttp.RespondSuccess(w, h.engine.Status())
}
