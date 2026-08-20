package reaperhttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/reaper"
)

const maxActionRunBody = 4 << 10

type ActionRunner interface {
	RunAction(context.Context, string, reaper.ProjectSource) (reaper.State, error)
}

type actionRunRequest struct {
	Confirmed bool `json:"confirmed"`
}

// ActionRunResponse embeds the same live state shape returned by GET state and
// adds the outcome the console must surface. ErrorReason is intentionally a
// stable, non-sensitive phrase; transport endpoints and ports never enter it.
type ActionRunResponse struct {
	reaper.State
	ActionID    string `json:"action_id"`
	Outcome     string `json:"outcome"`
	ErrorReason string `json:"error_reason,omitempty"`
}

func (h *Handler) GetActions(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	_, applies, err := h.projectSource(ws.ID)
	if err != nil {
		h.respondUnavailable(w)
		return
	}
	if !applies {
		_ = orihttp.RespondAPIError(w, http.StatusConflict,
			orihttp.NewAPIError("reaper_not_selected", "Live REAPER control is not selected for this workspace."))
		return
	}
	_ = orihttp.RespondSuccess(w, reaper.BuiltinActions())
}

func (h *Handler) RunAction(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	project, applies, err := h.projectSource(ws.ID)
	if err != nil {
		h.respondUnavailable(w)
		return
	}
	if !applies {
		_ = orihttp.RespondAPIError(w, http.StatusConflict,
			orihttp.NewAPIError("reaper_not_selected", "Live REAPER control is not selected for this workspace."))
		return
	}
	actionID := strings.TrimSpace(r.PathValue("actionID"))
	action, found := reaper.BuiltinAction(actionID)
	if !found {
		_ = orihttp.RespondAPIError(w, http.StatusNotFound,
			orihttp.NewAPIError("reaper_action_not_found", "REAPER action was not found."))
		return
	}
	request, ok := decodeActionRunRequest(w, r)
	if !ok {
		return
	}
	if action.NeedsConfirmation && !request.Confirmed {
		_ = orihttp.RespondJSON(w, http.StatusConflict, ActionRunResponse{
			ActionID: action.ID, Outcome: "confirmation_required",
			ErrorReason: "Confirm this project change before running it.",
		})
		return
	}
	runner, ok := h.client.(ActionRunner)
	if !ok {
		h.respondUnavailable(w)
		return
	}
	state, runErr := runner.RunAction(r.Context(), action.ID, project)
	state.Applies = true
	response := ActionRunResponse{State: state, ActionID: action.ID, Outcome: "ok"}
	if runErr == nil {
		_ = orihttp.RespondSuccess(w, response)
		return
	}
	response.Outcome = "error"
	status := http.StatusBadGateway
	switch {
	case errors.Is(runErr, reaper.ErrActionDisconnected):
		status = http.StatusConflict
		response.ErrorReason = "REAPER is not connected. Nothing was run."
	case errors.Is(runErr, reaper.ErrInvalidCommandID):
		status = http.StatusBadRequest
		response.ErrorReason = "The REAPER command ID is invalid."
	default:
		response.ErrorReason = "REAPER did not run the action."
	}
	logger.Warn("Live REAPER action failed", logger.Fields{"category": "reaper_action_failed"})
	_ = orihttp.RespondJSON(w, status, response)
}

func decodeActionRunRequest(w http.ResponseWriter, r *http.Request) (actionRunRequest, bool) {
	var request actionRunRequest
	if r.Body == nil || r.ContentLength == 0 {
		return request, true
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxActionRunBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		_ = orihttp.RespondBadRequest(w, "invalid action request")
		return actionRunRequest{}, false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		_ = orihttp.RespondBadRequest(w, "invalid action request")
		return actionRunRequest{}, false
	}
	return request, true
}
