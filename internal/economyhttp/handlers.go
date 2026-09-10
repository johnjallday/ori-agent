// Package economyhttp exposes the City Economy over HTTP: what the user has,
// what a schedule change would cost, and collecting a Farm's output.
//
// Every route in this package answers 404 when the feature flag is off, so a
// client that predates the flag being flipped sees "this does not exist" rather
// than an empty economy it might render an empty HUD for (city-economy FR47).
package economyhttp

import (
	"net/http"

	"github.com/johnjallday/ori-agent/internal/economy"
	"github.com/johnjallday/ori-agent/internal/featureflags"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
)

// Handler serves the economy API.
type Handler struct {
	service *economy.Service
}

// NewHandler creates an economy HTTP handler. A nil service is a valid state:
// it means the economy is not wired, and every route answers 404 exactly as it
// does when the flag is off.
func NewHandler(service *economy.Service) *Handler {
	return &Handler{service: service}
}

// available reports whether the economy should answer at all, and writes the 404
// when it should not.
func (h *Handler) available(w http.ResponseWriter) bool {
	if !featureflags.EconomyEnabled() || h == nil || !h.service.Available() {
		_ = orihttp.RespondNotFound(w, "the economy is not enabled")
		return false
	}
	return true
}

// GetOverview returns balances, energy, Farms, and pending harvest counts.
// GET /api/economy
func (h *Handler) GetOverview(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	if !h.available(w) {
		return
	}
	overview, err := h.service.Overview(r.Context())
	if err != nil {
		logger.Error("Failed to read the economy overview", logger.Fields{"error": err})
		_ = orihttp.RespondInternalError(w, "failed to read the economy")
		return
	}
	_ = orihttp.RespondSuccess(w, overview)
}

// HarvestRequest names the Farm whose pending runs are being collected.
type HarvestRequest struct {
	WorkspaceID string `json:"workspace_id"`
	TaskID      string `json:"task_id"`
}

// Harvest banks every pending run for one Farm and returns the new balances.
// POST /api/economy/harvest
//
// Collecting nothing is a success with banked: 0. The client calls this from
// every path that opens a result modal, including results that were opened
// before, so "already collected" is an expected outcome and not an error (FR26).
func (h *Handler) Harvest(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	if !h.available(w) {
		return
	}
	var req HarvestRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if req.TaskID == "" {
		_ = orihttp.RespondBadRequest(w, "task_id is required")
		return
	}
	result, err := h.service.BankPendingForTask(r.Context(), req.WorkspaceID, req.TaskID)
	if err != nil {
		logger.Error("Failed to bank pending Harvest",
			logger.Fields{"task_id": req.TaskID, "error": err})
		_ = orihttp.RespondInternalError(w, "failed to harvest")
		return
	}
	_ = orihttp.RespondSuccess(w, result)
}
